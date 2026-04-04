package exec

import (
	"context"
	"testing"
	"time"

	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
)

// ── RecoverOrphanedJobs ───────────────────────────────────────────────────────

func TestRecoverOrphanedJobs_NoOrphans_ReturnsZero(t *testing.T) {
	db := newTestDB(t)
	n, err := RecoverOrphanedJobs(context.Background(), db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 orphans, got %d", n)
	}
}

func TestRecoverOrphanedJobs_TerminalBatchesUntouched(t *testing.T) {
	db := newTestDB(t)
	s := seedScript(t, db)

	for _, status := range []models.ExecutionBatchStatus{
		models.BatchStatusCompleted,
		models.BatchStatusFailed,
		models.BatchStatusInterrupted,
	} {
		b := models.ExecutionBatch{ScriptID: s.ID, TotalInstances: 1, Status: status}
		if err := db.Create(&b).Error; err != nil {
			t.Fatalf("seed batch (%s): %v", status, err)
		}
	}

	n, err := RecoverOrphanedJobs(context.Background(), db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 orphans (terminal batches should be skipped), got %d", n)
	}
}

func TestRecoverOrphanedJobs_RunningBatchMarkedInterrupted(t *testing.T) {
	db := newTestDB(t)
	s := seedScript(t, db)

	b := models.ExecutionBatch{ScriptID: s.ID, TotalInstances: 1, Status: models.BatchStatusRunning}
	if err := db.Create(&b).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}

	n, err := RecoverOrphanedJobs(context.Background(), db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 orphan, got %d", n)
	}
	var got models.ExecutionBatch
	db.First(&got, b.ID)
	if got.Status != models.BatchStatusInterrupted {
		t.Errorf("batch status: want interrupted, got %q", got.Status)
	}
}

func TestRecoverOrphanedJobs_PendingBatchMarkedInterrupted(t *testing.T) {
	db := newTestDB(t)
	s := seedScript(t, db)

	b := models.ExecutionBatch{ScriptID: s.ID, TotalInstances: 1, Status: models.BatchStatusPending}
	if err := db.Create(&b).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}

	n, err := RecoverOrphanedJobs(context.Background(), db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 orphan, got %d", n)
	}
	var got models.ExecutionBatch
	db.First(&got, b.ID)
	if got.Status != models.BatchStatusInterrupted {
		t.Errorf("batch status: want interrupted, got %q", got.Status)
	}
}

func TestRecoverOrphanedJobs_NonTerminalExecutionsMarkedInterrupted(t *testing.T) {
	db := newTestDB(t)
	s := seedScript(t, db)

	b := models.ExecutionBatch{ScriptID: s.ID, TotalInstances: 2, Status: models.BatchStatusRunning}
	if err := db.Create(&b).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}

	runningEx := models.Execution{
		ScriptID:   s.ID,
		BatchID:    &b.ID,
		InstanceID: "i-running",
		Status:     models.ExecutionStatusRunning,
		StartTime:  time.Now(),
	}
	doneEx := models.Execution{
		ScriptID:   s.ID,
		BatchID:    &b.ID,
		InstanceID: "i-done",
		Status:     models.ExecutionStatusCompleted,
		StartTime:  time.Now(),
	}
	if err := db.Create(&runningEx).Error; err != nil {
		t.Fatalf("seed running execution: %v", err)
	}
	if err := db.Create(&doneEx).Error; err != nil {
		t.Fatalf("seed done execution: %v", err)
	}

	if _, err := RecoverOrphanedJobs(context.Background(), db); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var gotRunning, gotDone models.Execution
	db.First(&gotRunning, runningEx.ID)
	db.First(&gotDone, doneEx.ID)

	if gotRunning.Status != models.ExecutionStatusInterrupted {
		t.Errorf("running execution: want interrupted, got %q", gotRunning.Status)
	}
	if gotDone.Status != models.ExecutionStatusCompleted {
		t.Errorf("completed execution: want completed (unchanged), got %q", gotDone.Status)
	}
}

func TestRecoverOrphanedJobs_MultipleBatches(t *testing.T) {
	db := newTestDB(t)
	s := seedScript(t, db)

	for range 3 {
		b := models.ExecutionBatch{ScriptID: s.ID, TotalInstances: 1, Status: models.BatchStatusRunning}
		if err := db.Create(&b).Error; err != nil {
			t.Fatalf("seed batch: %v", err)
		}
	}

	n, err := RecoverOrphanedJobs(context.Background(), db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 orphans, got %d", n)
	}
}
