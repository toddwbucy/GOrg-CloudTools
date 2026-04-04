package exec

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
	"gorm.io/gorm"
)

// RecoverOrphanedJobs finds any ExecutionBatch left in 'pending' or 'running'
// state by a previous server instance and marks them — and their non-terminal
// Executions — as 'interrupted'. This is called once at startup before the
// HTTP server begins accepting requests.
//
// Interrupted batches can be recovered by the user via:
//
//	POST /api/exec/jobs/{id}/resume
//
// Returns the number of batches that were interrupted, or an error if the
// initial query fails. Per-batch errors are logged but do not stop processing.
func RecoverOrphanedJobs(_ context.Context, db *gorm.DB) (int, error) {
	var batches []models.ExecutionBatch
	if err := db.Where("status IN ?", []models.ExecutionBatchStatus{
		models.BatchStatusPending,
		models.BatchStatusRunning,
	}).Find(&batches).Error; err != nil {
		return 0, fmt.Errorf("querying orphaned batches: %w", err)
	}

	if len(batches) == 0 {
		return 0, nil
	}

	for i := range batches {
		b := &batches[i]

		// Mark non-terminal executions interrupted first so the batch row
		// is only updated after its children are consistent.
		if err := db.Model(&models.Execution{}).
			Where("batch_id = ? AND status IN ?", b.ID, []models.ExecutionStatus{
				models.ExecutionStatusPending,
				models.ExecutionStatusRunning,
			}).
			Update("status", models.ExecutionStatusInterrupted).Error; err != nil {
			slog.Error("startup recovery: failed to mark executions interrupted",
				"batch_id", b.ID, "err", err)
		}

		if err := db.Model(b).Update("status", models.BatchStatusInterrupted).Error; err != nil {
			slog.Error("startup recovery: failed to mark batch interrupted",
				"batch_id", b.ID, "err", err)
		}
	}

	return len(batches), nil
}
