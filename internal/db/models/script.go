package models

import "gorm.io/gorm"

// Script is a reusable script that can be executed on EC2 instances via SSM.
type Script struct {
	gorm.Model
	Name        string  `gorm:"index;not null" json:"name"`
	Content     string  `gorm:"type:text;not null" json:"content"`
	Description string  `gorm:"type:text" json:"description"`
	ScriptType  string  `gorm:"not null" json:"script_type"`        // "bash", "powershell"
	Interpreter string  `gorm:"not null;default:bash" json:"interpreter"`
	IsTemplate  bool    `gorm:"default:false" json:"is_template"`
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
	Platform    string   `gorm:"" json:"platform"`    // "linux", "windows"
	ScriptPath  string   `gorm:"" json:"script_path"` // path to embedded script file
	Scripts     []Script `gorm:"foreignKey:ToolID" json:"scripts,omitempty"`
}
