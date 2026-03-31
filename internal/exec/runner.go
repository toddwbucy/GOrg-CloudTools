// Package exec is the single script execution primitive for all workflows.
// Script Runner, Linux QC, RHSA checks, disk recon, and decom surveys all
// reduce to the same operation: push a script to one or more EC2 instances
// via SSM and record the results. The payload differs; the mechanism does not.
package exec

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/toddwbucy/GOrg-CloudTools/internal/aws/ssm"
	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
	"gorm.io/gorm"
)

// ScriptRequest describes a single batch script execution.
type ScriptRequest struct {
	// Exactly one of ScriptID or InlineScript must be set.
	ScriptID     *uint  // references models.Script.ID
	InlineScript string // ad-hoc script content

	Platform     string // "linux" (default) or "windows"
	InstanceIDs  []string
	AccountID    string
	Region       string
	SessionID    *uint  // optional: groups this job under an ExecutionSession
	ChangeNumber string // optional: change management reference
}

// Runner executes scripts against EC2 instances via SSM and persists results.
type Runner struct {
	db          *gorm.DB
	maxConc     int
	timeoutSecs int
}

// New creates a Runner. maxConcurrent limits simultaneous SSM invocations.
func New(db *gorm.DB, maxConcurrent, timeoutSecs int) *Runner {
	return &Runner{db: db, maxConc: maxConcurrent, timeoutSecs: timeoutSecs}
}

// Start creates an ExecutionBatch record, launches execution asynchronously,
// and returns the batch ID immediately. The caller should poll
// GET /api/exec/jobs/{id} for status.
func (r *Runner) Start(ctx context.Context, cfg aws.Config, req ScriptRequest) (uint, error) {
	script, err := r.resolveScript(req)
	if err != nil {
		return 0, err
	}

	platform := req.Platform
	if platform == "" {
		platform = "linux"
	}

	batch := &models.ExecutionBatch{
		ScriptID:       script.ID,
		TotalInstances: len(req.InstanceIDs),
		Status:         models.BatchStatusPending,
		SessionID:      req.SessionID,
	}
	if err := r.db.Create(batch).Error; err != nil {
		return 0, fmt.Errorf("creating batch: %w", err)
	}

	// Detach from request context so the job outlives the HTTP connection.
	go r.run(context.Background(), cfg, batch.ID, script, req.InstanceIDs, platform, req)
	return batch.ID, nil
}

func (r *Runner) run(
	ctx context.Context,
	cfg aws.Config,
	batchID uint,
	script *models.Script,
	instanceIDs []string,
	platform string,
	req ScriptRequest,
) {
	r.db.Model(&models.ExecutionBatch{}).Where("id = ?", batchID).
		Update("status", models.BatchStatusRunning)

	executor := ssm.New(cfg, r.timeoutSecs)
	sem := make(chan struct{}, r.maxConc)
	var wg sync.WaitGroup

	for _, iid := range instanceIDs {
		wg.Add(1)
		go func(instanceID string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r.runOne(ctx, executor, batchID, script, instanceID, platform, req)
		}(iid)
	}
	wg.Wait()

	var final models.ExecutionBatch
	r.db.First(&final, batchID)
	if final.FailedInstances == final.TotalInstances {
		r.db.Model(&final).Update("status", models.BatchStatusFailed)
	} else {
		r.db.Model(&final).Update("status", models.BatchStatusCompleted)
	}
}

func (r *Runner) runOne(
	ctx context.Context,
	executor *ssm.Executor,
	batchID uint,
	script *models.Script,
	instanceID, platform string,
	req ScriptRequest,
) {
	now := time.Now()
	exec := &models.Execution{
		ScriptID:     script.ID,
		BatchID:      &batchID,
		InstanceID:   instanceID,
		AccountID:    req.AccountID,
		Region:       req.Region,
		Status:       models.ExecutionStatusRunning,
		StartTime:    now,
		ChangeNumber: req.ChangeNumber,
	}
	r.db.Create(exec)

	// Send first so the commandID is recorded before we block on polling.
	// This means GET /api/aws/ssm/commands/{id} can find the record immediately.
	commandID, err := executor.Send(ctx, []string{instanceID}, script.Content, platform)
	if err != nil {
		exec.Status = models.ExecutionStatusFailed
		exec.Error = err.Error()
		r.db.Save(exec)
		r.db.Model(&models.ExecutionBatch{}).Where("id = ?", batchID).
			UpdateColumn("failed_instances", gorm.Expr("failed_instances + 1"))
		return
	}
	exec.CommandID = commandID
	r.db.Save(exec) // persist commandID before blocking

	result, err := executor.WaitForDone(ctx, commandID, instanceID)
	endTime := time.Now()
	exec.EndTime = &endTime

	if err != nil {
		exec.Status = models.ExecutionStatusFailed
		exec.Error = err.Error()
		r.db.Save(exec)
		r.db.Model(&models.ExecutionBatch{}).Where("id = ?", batchID).
			UpdateColumn("failed_instances", gorm.Expr("failed_instances + 1"))
		return
	}

	exitCode := result.ExitCode
	exec.Status = models.ExecutionStatus(result.Status)
	exec.Output = result.Output
	exec.Error = result.Error
	exec.ExitCode = &exitCode
	r.db.Save(exec)
	r.db.Model(&models.ExecutionBatch{}).Where("id = ?", batchID).
		UpdateColumn("completed_instances", gorm.Expr("completed_instances + 1"))
}

// resolveScript returns the script to execute, either from the DB or inline.
func (r *Runner) resolveScript(req ScriptRequest) (*models.Script, error) {
	if req.ScriptID != nil {
		var s models.Script
		if err := r.db.First(&s, *req.ScriptID).Error; err != nil {
			return nil, fmt.Errorf("loading script %d: %w", *req.ScriptID, err)
		}
		return &s, nil
	}
	if req.InlineScript != "" {
		return &models.Script{
			Name:       "_inline",
			Content:    req.InlineScript,
			ScriptType: "bash",
			Interpreter: "bash",
		}, nil
	}
	return nil, fmt.Errorf("one of script_id or inline_script is required")
}
