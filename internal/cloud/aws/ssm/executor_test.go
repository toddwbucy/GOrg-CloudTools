// White-box tests for internal SSM helpers. This file is in package ssm
// (not package ssm_test) so it can access unexported functions.
//
// Tests that require a real SSM endpoint (Send, GetStatus, Run, WaitForDone)
// belong in integration tests and are not present here.
package ssm

import (
	"errors"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// ── New — timeout clamping ────────────────────────────────────────────────────

func TestNew_PositiveTimeout_UsesProvidedValue(t *testing.T) {
	e := New(emptyConfig(), 30)
	if e.timeout.Seconds() != 30 {
		t.Errorf("want 30s, got %v", e.timeout)
	}
}

func TestNew_ZeroTimeout_ClampsToOne(t *testing.T) {
	e := New(emptyConfig(), 0)
	if e.timeout.Seconds() != 1 {
		t.Errorf("want 1s, got %v", e.timeout)
	}
}

func TestNew_NegativeTimeout_ClampsToOne(t *testing.T) {
	e := New(emptyConfig(), -10)
	if e.timeout.Seconds() != 1 {
		t.Errorf("want 1s, got %v", e.timeout)
	}
}

// ── isTerminal ────────────────────────────────────────────────────────────────

func TestIsTerminal_TerminalStates(t *testing.T) {
	terminal := []ssmtypes.CommandInvocationStatus{
		ssmtypes.CommandInvocationStatusSuccess,
		ssmtypes.CommandInvocationStatusFailed,
		ssmtypes.CommandInvocationStatusTimedOut,
		ssmtypes.CommandInvocationStatusCancelled,
	}
	for _, s := range terminal {
		if !isTerminal(s) {
			t.Errorf("expected %q to be terminal", s)
		}
	}
}

func TestIsTerminal_NonTerminalStates(t *testing.T) {
	nonTerminal := []ssmtypes.CommandInvocationStatus{
		ssmtypes.CommandInvocationStatusInProgress,
		ssmtypes.CommandInvocationStatusPending,
		ssmtypes.CommandInvocationStatusDelayed,
	}
	for _, s := range nonTerminal {
		if isTerminal(s) {
			t.Errorf("expected %q to be non-terminal", s)
		}
	}
}

// ── isRetryableSSMError ───────────────────────────────────────────────────────

func TestIsRetryableSSMError_InvocationDoesNotExist_IsRetryable(t *testing.T) {
	err := &ssmtypes.InvocationDoesNotExist{}
	if !isRetryableSSMError(err) {
		t.Error("InvocationDoesNotExist should be retryable")
	}
}

func TestIsRetryableSSMError_WrappedInvocationDoesNotExist_IsRetryable(t *testing.T) {
	wrapped := fmt.Errorf("poll error: %w", &ssmtypes.InvocationDoesNotExist{})
	if !isRetryableSSMError(wrapped) {
		t.Error("wrapped InvocationDoesNotExist should be retryable")
	}
}

func TestIsRetryableSSMError_RetryableInterface_True(t *testing.T) {
	err := &mockRetryableErr{retryable: true}
	if !isRetryableSSMError(err) {
		t.Error("error with Retryable()=true should be retryable")
	}
}

func TestIsRetryableSSMError_RetryableInterface_False(t *testing.T) {
	err := &mockRetryableErr{retryable: false}
	if isRetryableSSMError(err) {
		t.Error("error with Retryable()=false should not be retryable")
	}
}

func TestIsRetryableSSMError_PlainError_NotRetryable(t *testing.T) {
	if isRetryableSSMError(errors.New("some unrelated error")) {
		t.Error("plain error should not be retryable")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// mockRetryableErr implements the smithy retryable error interface.
type mockRetryableErr struct{ retryable bool }

func (e *mockRetryableErr) Error() string   { return "mock retryable error" }
func (e *mockRetryableErr) Retryable() bool { return e.retryable }

// emptyConfig returns a zero-value aws.Config sufficient for constructing an
// Executor in tests that do not make real API calls.
func emptyConfig() aws.Config {
	return aws.Config{}
}
