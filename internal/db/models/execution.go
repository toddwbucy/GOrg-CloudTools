package models

import "time"

// ExecutionStatus enumerates the states of an individual script execution.
type ExecutionStatus string

const (
	ExecutionStatusPending     ExecutionStatus = "pending"
	ExecutionStatusRunning     ExecutionStatus = "running"
	ExecutionStatusCompleted   ExecutionStatus = "completed"
	ExecutionStatusFailed      ExecutionStatus = "failed"
	// ExecutionStatusInterrupted means the server restarted while this execution
	// was in-flight. The SSM command may have completed; use the resume endpoint
	// to re-attach polling and recover the final result.
	ExecutionStatusInterrupted ExecutionStatus = "interrupted"
)

// Execution records a single script run against one EC2 instance.
type Execution struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	ScriptID  uint   `gorm:"not null;index" json:"script_id"`
	Script    Script `gorm:"foreignKey:ScriptID" json:"-"`

	InstanceID   string          `gorm:"index:idx_exec_instance_status;not null" json:"instance_id"`
	AccountID    string          `gorm:"index:idx_exec_command_lookup" json:"account_id"`
	Region       string          `gorm:"index:idx_exec_command_lookup" json:"region"`
	Status       ExecutionStatus `gorm:"not null;default:pending;index:idx_exec_batch_status;index:idx_exec_instance_status" json:"status"`
	StartTime    time.Time       `gorm:"not null;autoCreateTime" json:"start_time"`
	EndTime      *time.Time      `gorm:"" json:"end_time,omitempty"`
	Output       string          `gorm:"type:text" json:"output"`
	Error        string          `gorm:"type:text" json:"error"`
	ExitCode     *int            `gorm:"" json:"exit_code,omitempty"`
	// CommandID is the SSM command ID; indexed for the universal status-poll endpoint.
	CommandID    string  `gorm:"index:idx_exec_command_lookup" json:"command_id"`
	BatchID      *uint   `gorm:"index:idx_exec_batch_status" json:"batch_id,omitempty"`
	ChangeNumber string  `gorm:"" json:"change_number"`

	ExecutionMetadata map[string]any `gorm:"serializer:json" json:"execution_metadata,omitempty"`
}

// ExecutionBatchStatus enumerates the states of a batch run.
type ExecutionBatchStatus string

const (
	BatchStatusPending     ExecutionBatchStatus = "pending"
	BatchStatusRunning     ExecutionBatchStatus = "running"
	BatchStatusCompleted   ExecutionBatchStatus = "completed"
	BatchStatusFailed      ExecutionBatchStatus = "failed"
	// BatchStatusInterrupted means the server restarted while this batch was
	// in-flight. Use POST /api/exec/jobs/{id}/resume to re-attach polling.
	BatchStatusInterrupted ExecutionBatchStatus = "interrupted"
)

// ExecutionBatch aggregates results for a script executed across multiple instances.
type ExecutionBatch struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	ScriptID           uint                 `gorm:"not null;index" json:"script_id"`
	Script             Script               `gorm:"foreignKey:ScriptID" json:"-"`
	TotalInstances     int                  `gorm:"not null" json:"total_instances"`
	CompletedInstances int                  `gorm:"default:0;not null" json:"completed_instances"`
	FailedInstances    int                  `gorm:"default:0;not null" json:"failed_instances"`
	Status             ExecutionBatchStatus `gorm:"not null;default:pending" json:"status"`

	// SessionID links this batch to a multi-step ExecutionSession.
	SessionID *uint            `gorm:"index" json:"session_id,omitempty"`
	Session   *ExecutionSession `gorm:"foreignKey:SessionID" json:"-"`

	BatchMetadata map[string]any `gorm:"serializer:json" json:"batch_metadata,omitempty"`

	// Executions are loaded via Preload when the full job view is requested.
	Executions []Execution `gorm:"foreignKey:BatchID;references:ID" json:"executions,omitempty"`
}
