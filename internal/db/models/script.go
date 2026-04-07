package models

import "gorm.io/gorm"

// ToolScope constants define the two valid values for Tool.Scope.
// Use these instead of raw strings to prevent typos.
const (
	// ScopeOS marks tools that run scripts on instances via the provider's
	// remote execution agent (SSM, Azure RunCommand, GCP RunCommand). These
	// tools are cloud-agnostic and visible regardless of which provider is active.
	ScopeOS = "os"

	// ScopeCloud marks tools that call provider APIs directly (VPC recon,
	// org traversal). These require provider-specific credentials and are only
	// shown when the matching provider's credentials are loaded.
	ScopeCloud = "cloud"
)

// Script is a reusable script that can be executed on EC2 instances via SSM.
type Script struct {
	gorm.Model
	Name        string  `gorm:"index;not null" json:"name"`
	Content     string  `gorm:"type:text;not null" json:"content"`
	Description string  `gorm:"type:text" json:"description"`
	ScriptType  string  `gorm:"not null" json:"script_type"`        // "bash", "powershell"
	Interpreter string  `gorm:"not null;default:bash" json:"interpreter"`
	IsTemplate  bool    `gorm:"default:false" json:"is_template"`
	// Ephemeral marks scripts created from inline requests. These are excluded
	// from the public scripts API so they don't pollute the script catalog.
	Ephemeral   bool    `gorm:"not null;default:false;index" json:"-"`
	ChangeID    *uint   `gorm:"index" json:"change_id,omitempty"`
	ToolID      *uint   `gorm:"index" json:"tool_id,omitempty"`
	Change      *Change `gorm:"foreignKey:ChangeID" json:"-"`
	Tool        *Tool   `gorm:"foreignKey:ToolID" json:"-"`
}

// Tool is a logical grouping of scripts for a specific operational purpose.
type Tool struct {
	gorm.Model
	Name        string   `gorm:"uniqueIndex;not null" json:"name"`
	Description string   `gorm:"type:text" json:"description"`
	ToolType    string   `gorm:"not null" json:"tool_type"`
	// Scope distinguishes tools that run ON instances (cloud-agnostic) from
	// tools that call cloud-provider APIs directly.
	//
	//   "os"    — script runs on any cloud's instances via the provider's
	//             remote execution agent (SSM, RunCommand, etc.). Shown in the
	//             TUI regardless of which cloud env is active.
	//
	//   "cloud" — calls cloud-provider APIs directly (VPC recon, org traversal).
	//             Only shown when the matching provider's credentials are loaded.
	Scope       string   `gorm:"not null;default:os" json:"scope"`
	Platform    string   `gorm:"" json:"platform"`    // "linux", "windows"
	ScriptPath  string   `gorm:"" json:"script_path"` // path to embedded script file
	Scripts     []Script `gorm:"foreignKey:ToolID" json:"scripts,omitempty"`
}
