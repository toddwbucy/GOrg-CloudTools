package models

import (
	"time"

	"gorm.io/gorm"
)

// ChangeStatus enumerates the lifecycle states of a change record.
type ChangeStatus string

const (
	ChangeStatusNew       ChangeStatus = "new"
	ChangeStatusApproved  ChangeStatus = "approved"
	ChangeStatusCompleted ChangeStatus = "completed"
)

// Change represents a change-management record (e.g. a CHG ticket).
type Change struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	ChangeNumber   string       `gorm:"uniqueIndex;not null" json:"change_number"`
	Description    string       `gorm:"type:text" json:"description"`
	Status         ChangeStatus `gorm:"not null;default:new" json:"status"`
	ChangeMetadata map[string]any `gorm:"serializer:json" json:"change_metadata,omitempty"`

	Instances []ChangeInstance `gorm:"foreignKey:ChangeID;constraint:OnDelete:CASCADE" json:"instances,omitempty"`
	Scripts   []Script         `gorm:"foreignKey:ChangeID" json:"scripts,omitempty"`
}

// ChangeInstance links a Change to a specific EC2 instance.
// InstanceID is stored as a raw string (not a FK) to allow references
// to instances not yet recorded in the local database.
type ChangeInstance struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	ChangeID   uint   `gorm:"not null;index:idx_change_instance_lookup" json:"change_id"`
	Change     Change `gorm:"foreignKey:ChangeID" json:"-"`
	InstanceID string `gorm:"not null;index:idx_change_instance_lookup" json:"instance_id"`
	AccountID  string `gorm:"not null" json:"account_id"`
	Region     string `gorm:"not null" json:"region"`
	Platform   string `gorm:"not null" json:"platform"` // "linux" or "windows"

	InstanceMetadata map[string]any `gorm:"serializer:json" json:"instance_metadata,omitempty"`
}
