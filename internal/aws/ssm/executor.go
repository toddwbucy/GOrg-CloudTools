// Package ssm wraps AWS SSM SendCommand and GetCommandInvocation.
//
// Primitives are intentionally separated:
//
//   - Send()      — issues the command, returns commandID immediately
//   - GetStatus() — single non-blocking invocation check
//   - Run()       — convenience: Send + poll until terminal (used by exec.Runner)
//
// Callers that need live progress use Send() + repeated GetStatus() calls.
// The backend exposes GetStatus as an API endpoint so the frontend never needs
// to poll SSM directly or know about role assumptions.
package ssm

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

const (
	pollInterval    = 5 * time.Second
	documentLinux   = "AWS-RunShellScript"
	documentWindows = "AWS-RunPowerShellScript"
)

// InvocationStatus holds the result of a single GetCommandInvocation call.
type InvocationStatus struct {
	CommandID  string
	InstanceID string
	Status     string // mirrors ssmtypes.CommandInvocationStatus
	Output     string
	Error      string
	ExitCode   int
	Done       bool // true when status is terminal (Success/Failed/TimedOut/Cancelled)
}

// Executor wraps the SSM client.
type Executor struct {
	client  *ssm.Client
	timeout time.Duration
}

// New creates an Executor. timeoutSecs is used as the SSM command timeout
// and as the deadline for the blocking Run() helper.
func New(cfg aws.Config, timeoutSecs int) *Executor {
	return &Executor{
		client:  ssm.NewFromConfig(cfg),
		timeout: time.Duration(timeoutSecs) * time.Second,
	}
}

// Send issues a SendCommand call and returns the commandID immediately.
// instanceIDs may contain one or many targets.
func (e *Executor) Send(ctx context.Context, instanceIDs []string, script, platform string) (string, error) {
	doc := documentLinux
	if platform == "windows" {
		doc = documentWindows
	}
	out, err := e.client.SendCommand(ctx, &ssm.SendCommandInput{
		InstanceIds:    instanceIDs,
		DocumentName:   aws.String(doc),
		Parameters:     map[string][]string{"commands": {script}},
		TimeoutSeconds: aws.Int32(int32(e.timeout.Seconds())),
	})
	if err != nil {
		return "", fmt.Errorf("ssm SendCommand: %w", err)
	}
	return aws.ToString(out.Command.CommandId), nil
}

// GetStatus makes a single GetCommandInvocation call (no polling).
// Returns the current state regardless of whether the command has finished.
func (e *Executor) GetStatus(ctx context.Context, commandID, instanceID string) (*InvocationStatus, error) {
	out, err := e.client.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{
		CommandId:  aws.String(commandID),
		InstanceId: aws.String(instanceID),
	})
	if err != nil {
		return nil, fmt.Errorf("GetCommandInvocation (%s/%s): %w", commandID, instanceID, err)
	}
	return &InvocationStatus{
		CommandID:  commandID,
		InstanceID: instanceID,
		Status:     string(out.Status),
		Output:     aws.ToString(out.StandardOutputContent),
		Error:      aws.ToString(out.StandardErrorContent),
		ExitCode:   int(out.ResponseCode),
		Done:       isTerminal(out.Status),
	}, nil
}

// Run is a convenience wrapper: Send + poll until terminal. Used internally
// when the caller does not need the commandID before completion.
func (e *Executor) Run(ctx context.Context, instanceID, script, platform string) (*InvocationStatus, error) {
	commandID, err := e.Send(ctx, []string{instanceID}, script, platform)
	if err != nil {
		return nil, err
	}
	return e.pollUntilDone(ctx, commandID, instanceID)
}

// WaitForDone polls an already-sent command until it reaches a terminal state.
// Use this after Send() when the commandID must be recorded before blocking.
func (e *Executor) WaitForDone(ctx context.Context, commandID, instanceID string) (*InvocationStatus, error) {
	return e.pollUntilDone(ctx, commandID, instanceID)
}

func (e *Executor) pollUntilDone(ctx context.Context, commandID, instanceID string) (*InvocationStatus, error) {
	deadline := time.Now().Add(e.timeout)
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("SSM command %s timed out after %v", commandID, e.timeout)
		}
		status, err := e.GetStatus(ctx, commandID, instanceID)
		if err != nil {
			return nil, err
		}
		if status.Done {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func isTerminal(s ssmtypes.CommandInvocationStatus) bool {
	switch s {
	case ssmtypes.CommandInvocationStatusSuccess,
		ssmtypes.CommandInvocationStatusFailed,
		ssmtypes.CommandInvocationStatusTimedOut,
		ssmtypes.CommandInvocationStatusCancelled:
		return true
	}
	return false
}
