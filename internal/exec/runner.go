// Package exec is the single script execution primitive for all workflows.
// Script Runner, Linux QC, RHSA checks, disk recon, and decom surveys all
// reduce to the same operation: push a script to one or more EC2 instances
// via SSM and record the results. The payload differs; the mechanism does not.
package exec

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/toddwbucy/GOrg-CloudTools/internal/cloud/aws/ssm"
	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
	"gorm.io/gorm"
)

// ssmAdapter adapts *ssm.Executor to the RemoteExecutor interface.
// It translates *ssm.InvocationStatus → *InvocationResult so that the exec
// package's core logic (Runner, OrgRunner) never imports ssm-specific types.
type ssmAdapter struct{ e *ssm.Executor }

func (a *ssmAdapter) Send(ctx context.Context, instanceIDs []string, script, platform string) (string, error) {
	return a.e.Send(ctx, instanceIDs, script, platform)
}

func (a *ssmAdapter) WaitForDone(ctx context.Context, commandID, instanceID string) (*InvocationResult, error) {
	s, err := a.e.WaitForDone(ctx, commandID, instanceID)
	if err != nil {
		return nil, err
	}
	return &InvocationResult{
		CommandID:  s.CommandID,
		InstanceID: s.InstanceID,
		Status:     s.Status,
		Output:     s.Output,
		Error:      s.Error,
		ExitCode:   s.ExitCode,
		Done:       s.Done,
	}, nil
}

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
// A non-positive value is clamped to 1 to avoid semaphore deadlock.
func New(db *gorm.DB, maxConcurrent, timeoutSecs int) *Runner {
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	return &Runner{db: db, maxConc: maxConcurrent, timeoutSecs: timeoutSecs}
}

// Start creates an ExecutionBatch record, launches execution asynchronously,
// and returns the batch ID immediately. The caller should poll
// GET /api/exec/jobs/{id} for status.
func (r *Runner) Start(ctx context.Context, cfg aws.Config, req ScriptRequest) (uint, error) {
	return r.start(ctx, &ssmAdapter{e: ssm.New(cfg, r.timeoutSecs)}, req)
}

// start is the testable core of Start. It accepts an RemoteExecutor so tests
// can inject a mock without real AWS credentials.
func (r *Runner) start(ctx context.Context, executor RemoteExecutor, req ScriptRequest) (uint, error) {
	// Validate inputs before touching the DB or AWS.
	if req.ScriptID != nil && req.InlineScript != "" {
		return 0, fmt.Errorf("only one of script_id or inline_script may be provided, not both")
	}
	if len(req.InstanceIDs) == 0 {
		return 0, fmt.Errorf("instance_ids must not be empty")
	}

	// Normalize and validate platform before any DB or AWS interaction.
	platform := strings.ToLower(strings.TrimSpace(req.Platform))
	if platform == "" {
		platform = "linux"
	}
	if platform != "linux" && platform != "windows" {
		return 0, fmt.Errorf("unsupported platform %q: must be linux or windows", req.Platform)
	}
	req.Platform = platform

	script, err := r.resolveScript(req)
	if err != nil {
		return 0, err
	}

	batch := &models.ExecutionBatch{
		ScriptID:       script.ID,
		TotalInstances: len(req.InstanceIDs),
		Status:         models.BatchStatusPending,
		SessionID:      req.SessionID,
	}
	if err := r.db.Create(batch).Error; err != nil {
		// Clean up the ephemeral script we just inserted so it doesn't orphan.
		if req.InlineScript != "" {
			if err2 := r.db.Delete(script).Error; err2 != nil {
				slog.Error("failed to clean up orphaned ephemeral script", "script_id", script.ID, "err", err2)
			}
		}
		return 0, fmt.Errorf("creating batch: %w", err)
	}

	// Detach from request context so the job outlives the HTTP connection.
	go r.run(context.Background(), executor, batch.ID, script, req.InstanceIDs, platform, req)
	return batch.ID, nil
}

func (r *Runner) run(
	ctx context.Context,
	executor RemoteExecutor,
	batchID uint,
	script *models.Script,
	instanceIDs []string,
	platform string,
	req ScriptRequest,
) {
	if err := r.db.Model(&models.ExecutionBatch{}).Where("id = ?", batchID).
		Update("status", models.BatchStatusRunning).Error; err != nil {
		slog.Error("failed to mark batch running", "batch_id", batchID, "err", err)
	}

	instancesCh := make(chan string, len(instanceIDs))
	for _, iid := range instanceIDs {
		instancesCh <- iid
	}
	close(instancesCh)

	var wg sync.WaitGroup
	workerCount := min(r.maxConc, len(instanceIDs))
	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for instanceID := range instancesCh {
				r.runOne(ctx, executor, batchID, script, instanceID, platform, req)
			}
		}()
	}
	wg.Wait()
	r.finalizeBatch(batchID)
}

func (r *Runner) runOne(
	ctx context.Context,
	executor RemoteExecutor,
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
	if err := r.db.Create(exec).Error; err != nil {
		slog.Error("failed to create execution record; skipping SSM send", "instance", instanceID, "batch", batchID, "err", err)
		if err2 := r.db.Model(&models.ExecutionBatch{}).Where("id = ?", batchID).
			UpdateColumn("failed_instances", gorm.Expr("failed_instances + 1")).Error; err2 != nil {
			slog.Error("failed to increment failed_instances after create error", "batch_id", batchID, "err", err2)
		}
		return
	}

	// Send first so the commandID is recorded before we block on polling.
	// This means GET /api/aws/ssm/commands/{id} can find the record immediately.
	commandID, err := executor.Send(ctx, []string{instanceID}, script.Content, platform)
	if err != nil {
		exec.Status = models.ExecutionStatusFailed
		exec.Error = err.Error()
		if err2 := r.db.Save(exec).Error; err2 != nil {
			slog.Error("failed to persist send failure", "instance", instanceID, "batch_id", batchID, "err", err2)
		}
		if err2 := r.db.Model(&models.ExecutionBatch{}).Where("id = ?", batchID).
			UpdateColumn("failed_instances", gorm.Expr("failed_instances + 1")).Error; err2 != nil {
			slog.Error("failed to increment failed_instances after send error", "batch_id", batchID, "err", err2)
		}
		return
	}
	exec.CommandID = commandID
	// Persist commandID before blocking — status polling depends on this record.
	// Abort rather than continue: without this DB record, polling is broken and
	// the final result would also not be discoverable.
	if err := r.db.Save(exec).Error; err != nil {
		slog.Error("failed to persist command ID; aborting execution", "instance", instanceID, "command_id", commandID, "err", err)
		exec.Status = models.ExecutionStatusFailed
		exec.Error = "failed to persist command ID before polling"
		if err2 := r.db.Save(exec).Error; err2 != nil {
			slog.Error("failed to persist abort after command ID save failure", "instance", instanceID, "err", err2)
		}
		if err2 := r.db.Model(&models.ExecutionBatch{}).Where("id = ?", batchID).
			UpdateColumn("failed_instances", gorm.Expr("failed_instances + 1")).Error; err2 != nil {
			slog.Error("failed to increment failed_instances after command ID save failure", "batch_id", batchID, "err", err2)
		}
		return
	}

	r.pollAndPersist(ctx, executor, batchID, exec)
}

// Resume re-attaches polling goroutines to an interrupted batch using the
// provided AWS credentials. Returns immediately; polling continues in the
// background. The caller should poll GET /api/exec/jobs/{id} for status.
func (r *Runner) Resume(ctx context.Context, cfg aws.Config, batchID uint) error {
	return r.resume(ctx, &ssmAdapter{e: ssm.New(cfg, r.timeoutSecs)}, batchID)
}

// resume is the testable core of Resume — accepts an RemoteExecutor so tests
// can inject a mock without real AWS credentials.
func (r *Runner) resume(ctx context.Context, executor RemoteExecutor, batchID uint) error {
	var batch models.ExecutionBatch
	if err := r.db.Preload("Executions").First(&batch, batchID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("batch %d not found", batchID)
		}
		return fmt.Errorf("loading batch %d: %w", batchID, err)
	}
	if batch.Status != models.BatchStatusInterrupted {
		return fmt.Errorf("batch %d cannot be resumed: status is %q (must be interrupted)", batchID, batch.Status)
	}

	if err := r.db.Model(&batch).Update("status", models.BatchStatusRunning).Error; err != nil {
		return fmt.Errorf("marking batch %d running: %w", batchID, err)
	}

	go r.resumeRun(context.Background(), executor, &batch)
	return nil
}

func (r *Runner) resumeRun(ctx context.Context, executor RemoteExecutor, batch *models.ExecutionBatch) {
	// Partition: executions with a command_id get re-polled; without one,
	// SSM never received the Send so they are marked failed immediately.
	var toResume []*models.Execution
	for i := range batch.Executions {
		ex := &batch.Executions[i]
		if ex.Status != models.ExecutionStatusInterrupted {
			continue
		}
		if ex.CommandID == "" {
			now := time.Now()
			if err := r.db.Model(ex).Updates(map[string]any{
				"status":   models.ExecutionStatusFailed,
				"error":    "command was not sent before server restart",
				"end_time": &now,
			}).Error; err != nil {
				slog.Error("failed to mark no-command execution failed on resume", "execution_id", ex.ID, "err", err)
			}
			if err := r.db.Model(&models.ExecutionBatch{}).Where("id = ?", batch.ID).
				UpdateColumn("failed_instances", gorm.Expr("failed_instances + 1")).Error; err != nil {
				slog.Error("failed to increment failed_instances for no-command execution", "batch_id", batch.ID, "err", err)
			}
			continue
		}
		toResume = append(toResume, ex)
	}

	if len(toResume) > 0 {
		ch := make(chan *models.Execution, len(toResume))
		for _, ex := range toResume {
			ch <- ex
		}
		close(ch)

		var wg sync.WaitGroup
		workerCount := min(r.maxConc, len(toResume))
		for range workerCount {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for ex := range ch {
					r.resumeOne(ctx, executor, batch.ID, ex)
				}
			}()
		}
		wg.Wait()
	}

	r.finalizeBatch(batch.ID)
}

// resumeOne re-polls a single interrupted execution using its stored commandID.
func (r *Runner) resumeOne(ctx context.Context, executor RemoteExecutor, batchID uint, ex *models.Execution) {
	if err := r.db.Model(ex).Update("status", models.ExecutionStatusRunning).Error; err != nil {
		slog.Error("failed to mark execution running on resume", "execution_id", ex.ID, "err", err)
	}
	r.pollAndPersist(ctx, executor, batchID, ex)
}

// pollAndPersist calls WaitForDone on an already-sent command and writes the
// final result back to the DB. Shared by runOne (after Send) and resumeOne.
//
// WaitForDone returns (result, nil) for ALL terminal SSM states: Success,
// Failed, TimedOut, and Cancelled. Only "Success" counts as completed.
func (r *Runner) pollAndPersist(ctx context.Context, executor RemoteExecutor, batchID uint, ex *models.Execution) {
	result, err := executor.WaitForDone(ctx, ex.CommandID, ex.InstanceID)
	endTime := time.Now()
	ex.EndTime = &endTime

	if err != nil {
		ex.Status = models.ExecutionStatusFailed
		ex.Error = err.Error()
		if err2 := r.db.Save(ex).Error; err2 != nil {
			slog.Error("failed to persist execution wait error", "execution_id", ex.ID, "batch_id", batchID, "err", err2)
		}
		if err2 := r.db.Model(&models.ExecutionBatch{}).Where("id = ?", batchID).
			UpdateColumn("failed_instances", gorm.Expr("failed_instances + 1")).Error; err2 != nil {
			slog.Error("failed to increment failed_instances after wait error", "batch_id", batchID, "err", err2)
		}
		return
	}

	exitCode := result.ExitCode
	ex.Output = result.Output
	ex.Error = result.Error
	ex.ExitCode = &exitCode

	if result.Status == "Success" {
		ex.Status = models.ExecutionStatusCompleted
		if err := r.db.Save(ex).Error; err != nil {
			slog.Error("failed to persist execution success", "execution_id", ex.ID, "batch_id", batchID, "err", err)
		}
		if err := r.db.Model(&models.ExecutionBatch{}).Where("id = ?", batchID).
			UpdateColumn("completed_instances", gorm.Expr("completed_instances + 1")).Error; err != nil {
			slog.Error("failed to increment completed_instances", "batch_id", batchID, "err", err)
		}
	} else {
		ex.Status = models.ExecutionStatusFailed
		if err := r.db.Save(ex).Error; err != nil {
			slog.Error("failed to persist execution terminal failure", "execution_id", ex.ID, "batch_id", batchID, "status", result.Status, "err", err)
		}
		if err := r.db.Model(&models.ExecutionBatch{}).Where("id = ?", batchID).
			UpdateColumn("failed_instances", gorm.Expr("failed_instances + 1")).Error; err != nil {
			slog.Error("failed to increment failed_instances after terminal failure", "batch_id", batchID, "err", err)
		}
	}
}

// finalizeBatch sets the batch's terminal status based on its instance counts.
// All-failed → BatchStatusFailed; any successes → BatchStatusCompleted.
func (r *Runner) finalizeBatch(batchID uint) {
	var final models.ExecutionBatch
	if err := r.db.First(&final, batchID).Error; err != nil {
		slog.Error("failed to load batch for final status update", "batch_id", batchID, "err", err)
		return
	}
	finalStatus := models.BatchStatusCompleted
	if final.FailedInstances == final.TotalInstances {
		finalStatus = models.BatchStatusFailed
	}
	if err := r.db.Model(&final).Update("status", finalStatus).Error; err != nil {
		slog.Error("failed to update batch final status", "batch_id", batchID, "status", finalStatus, "err", err)
	}
}

// resolveScript returns the script to execute, either from the DB or inline.
func (r *Runner) resolveScript(req ScriptRequest) (*models.Script, error) {
	if req.ScriptID != nil {
		var s models.Script
		if err := r.db.Where("ephemeral = ?", false).First(&s, *req.ScriptID).Error; err != nil {
			return nil, fmt.Errorf("loading script %d: %w", *req.ScriptID, err)
		}
		return &s, nil
	}
	if req.InlineScript != "" {
		scriptType := "bash"
		interpreter := "bash"
		if strings.EqualFold(req.Platform, "windows") {
			scriptType = "powershell"
			interpreter = "powershell"
		}
		// Persist so the script gets a real ID, required by the ExecutionBatch FK.
		// Marked Ephemeral so it is excluded from the public scripts API.
		s := &models.Script{
			Name:        "_inline",
			Content:     req.InlineScript,
			ScriptType:  scriptType,
			Interpreter: interpreter,
			Ephemeral:   true,
		}
		if err := r.db.Create(s).Error; err != nil {
			return nil, fmt.Errorf("persisting inline script: %w", err)
		}
		return s, nil
	}
	return nil, fmt.Errorf("one of script_id or inline_script is required")
}
