package models

import "time"

// ExecutionSession groups related ExecutionBatches for multi-step workflows
// and provides an audit trail. The backend stores the session; the frontend
// decides what the session means (which workflow, which steps, in what order).
type ExecutionSession struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// WorkflowType is a free-form label set by the frontend (e.g. "linux-qc",
	// "script-runner"). The backend treats it as opaque metadata.
	WorkflowType string `gorm:"index" json:"workflow_type"`
	Description  string `gorm:"type:text" json:"description"`

	// Status is maintained by the frontend via PATCH; backend sets it to
	// "in_progress" on creation.
	Status string `gorm:"not null;default:in_progress" json:"status"`

	// AccountID and Env are the primary credentials context for this session.
	AccountID string `gorm:"" json:"account_id"`
	Env       string `gorm:"" json:"env"` // "com" or "gov"

	// Batches are loaded via Preload when the full session view is requested.
	Batches []ExecutionBatch `gorm:"foreignKey:SessionID" json:"batches,omitempty"`
}
