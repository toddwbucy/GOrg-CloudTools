package exec

import (
	"context"
	"testing"
	"time"

	ssmtypes "github.com/toddwbucy/GOrg-CloudTools/internal/aws/ssm"
	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
)

// ── Runner.resume (white-box) ─────────────────────────────────────────────────

func TestResume_BatchNotFound_ReturnsError(t *testing.T) {
	db := newTestDB(t)
	r := New(db, 1, 30)
	if err := r.resume(context.Background(), successMock(), 9999); err == nil {
		t.Fatal("expected error for non-existent batch, got nil")
	}
}

func TestResume_NotInterrupted_ReturnsError(t *testing.T) {
	db := newTestDB(t)
	r := New(db, 1, 30)
	s := seedScript(t, db)

	b := models.ExecutionBatch{ScriptID: s.ID, TotalInstances: 1, Status: models.BatchStatusRunning}
	if err := db.Create(&b).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}

	if err := r.resume(context.Background(), successMock(), b.ID); err == nil {
		t.Fatal("expected error for non-interrupted batch, got nil")
	}
}

func TestResume_WithCommandID_ReattachesPollingAndCompletes(t *testing.T) {
	db := newTestDB(t)
	r := New(db, 1, 30)
	s := seedScript(t, db)

	b := models.ExecutionBatch{ScriptID: s.ID, TotalInstances: 1, Status: models.BatchStatusInterrupted}
	if err := db.Create(&b).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	ex := models.Execution{
		ScriptID:   s.ID,
		BatchID:    &b.ID,
		InstanceID: "i-001",
		CommandID:  "cmd-resume-001",
		Status:     models.ExecutionStatusInterrupted,
		StartTime:  time.Now(),
	}
	if err := db.Create(&ex).Error; err != nil {
		t.Fatalf("seed execution: %v", err)
	}

	if err := r.resume(context.Background(), successMock(), b.ID); err != nil {
		t.Fatalf("resume: %v", err)
	}

	final := waitForBatch(t, db, b.ID, 3*time.Second)
	if final.Status != models.BatchStatusCompleted {
		t.Errorf("batch status: want completed, got %q", final.Status)
	}
	if final.CompletedInstances != 1 {
		t.Errorf("completed_instances: want 1, got %d", final.CompletedInstances)
	}
}

func TestResume_WithoutCommandID_MarksExecutionFailed(t *testing.T) {
	db := newTestDB(t)
	r := New(db, 1, 30)
	s := seedScript(t, db)

	b := models.ExecutionBatch{ScriptID: s.ID, TotalInstances: 1, Status: models.BatchStatusInterrupted}
	if err := db.Create(&b).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	// No CommandID — SSM never received the send before the restart.
	ex := models.Execution{
		ScriptID:   s.ID,
		BatchID:    &b.ID,
		InstanceID: "i-nosend",
		CommandID:  "",
		Status:     models.ExecutionStatusInterrupted,
		StartTime:  time.Now(),
	}
	if err := db.Create(&ex).Error; err != nil {
		t.Fatalf("seed execution: %v", err)
	}

	if err := r.resume(context.Background(), successMock(), b.ID); err != nil {
		t.Fatalf("resume: %v", err)
	}

	final := waitForBatch(t, db, b.ID, 3*time.Second)
	if final.Status != models.BatchStatusFailed {
		t.Errorf("batch status: want failed, got %q", final.Status)
	}
	if final.FailedInstances != 1 {
		t.Errorf("failed_instances: want 1, got %d", final.FailedInstances)
	}

	var gotEx models.Execution
	db.First(&gotEx, ex.ID)
	if gotEx.Status != models.ExecutionStatusFailed {
		t.Errorf("execution status: want failed, got %q", gotEx.Status)
	}
}

func TestResume_MixedCommandIDs_CorrectCounters(t *testing.T) {
	db := newTestDB(t)
	r := New(db, 2, 30)
	s := seedScript(t, db)

	b := models.ExecutionBatch{ScriptID: s.ID, TotalInstances: 3, Status: models.BatchStatusInterrupted}
	if err := db.Create(&b).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}

	// One execution with a command (will poll → success), one without (→ failed).
	hasCmdEx := models.Execution{
		ScriptID: s.ID, BatchID: &b.ID, InstanceID: "i-with-cmd",
		CommandID: "cmd-ok", Status: models.ExecutionStatusInterrupted, StartTime: time.Now(),
	}
	noCmdEx := models.Execution{
		ScriptID: s.ID, BatchID: &b.ID, InstanceID: "i-no-cmd",
		CommandID: "", Status: models.ExecutionStatusInterrupted, StartTime: time.Now(),
	}
	// Already-completed execution — should not be touched by resume.
	doneEx := models.Execution{
		ScriptID: s.ID, BatchID: &b.ID, InstanceID: "i-done",
		CommandID: "cmd-done", Status: models.ExecutionStatusCompleted, StartTime: time.Now(),
	}
	for _, ex := range []models.Execution{hasCmdEx, noCmdEx, doneEx} {
		e := ex
		if err := db.Create(&e).Error; err != nil {
			t.Fatalf("seed execution: %v", err)
		}
	}

	if err := r.resume(context.Background(), successMock(), b.ID); err != nil {
		t.Fatalf("resume: %v", err)
	}

	final := waitForBatch(t, db, b.ID, 3*time.Second)
	// 1 success (hasCmdEx), 1 failure (noCmdEx) → not all failed → completed.
	if final.Status != models.BatchStatusCompleted {
		t.Errorf("batch status: want completed, got %q", final.Status)
	}
	if final.CompletedInstances != 1 {
		t.Errorf("completed_instances: want 1, got %d", final.CompletedInstances)
	}
	if final.FailedInstances != 1 {
		t.Errorf("failed_instances: want 1, got %d", final.FailedInstances)
	}
}

func TestResume_SSMTerminalFailure_MarksExecutionFailed(t *testing.T) {
	db := newTestDB(t)
	r := New(db, 1, 30)
	s := seedScript(t, db)

	b := models.ExecutionBatch{ScriptID: s.ID, TotalInstances: 1, Status: models.BatchStatusInterrupted}
	if err := db.Create(&b).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	ex := models.Execution{
		ScriptID: s.ID, BatchID: &b.ID, InstanceID: "i-fail",
		CommandID: "cmd-fail", Status: models.ExecutionStatusInterrupted, StartTime: time.Now(),
	}
	if err := db.Create(&ex).Error; err != nil {
		t.Fatalf("seed execution: %v", err)
	}

	timedOutMock := &mockSSMExecutor{
		sendFn: func(_ context.Context, _ []string, _, _ string) (string, error) {
			t.Error("Send should not be called during resume")
			return "", nil
		},
		waitForDone: func(_ context.Context, _, _ string) (*ssmtypes.InvocationStatus, error) {
			return &ssmtypes.InvocationStatus{Status: "TimedOut", ExitCode: -1, Done: true}, nil
		},
	}

	if err := r.resume(context.Background(), timedOutMock, b.ID); err != nil {
		t.Fatalf("resume: %v", err)
	}

	final := waitForBatch(t, db, b.ID, 3*time.Second)
	if final.Status != models.BatchStatusFailed {
		t.Errorf("batch status: want failed, got %q", final.Status)
	}
}
