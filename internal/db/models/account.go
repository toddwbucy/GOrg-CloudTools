package models

import "gorm.io/gorm"

// Account represents an AWS account tracked in the system.
type Account struct {
	gorm.Model
	AccountID   string   `gorm:"uniqueIndex;not null" json:"account_id"`
	Name        string   `gorm:"" json:"name"`
	Environment string   `gorm:"not null" json:"environment"` // "com" or "gov"
	Active      bool     `gorm:"default:true" json:"active"`
	Regions     []Region `gorm:"foreignKey:AccountID;constraint:OnDelete:CASCADE" json:"regions,omitempty"`
}

// Region represents an AWS region associated with an account.
type Region struct {
	gorm.Model
	Name      string     `gorm:"not null" json:"name"`
	AccountID uint       `gorm:"not null;index" json:"account_id"`
	Account   Account    `gorm:"foreignKey:AccountID" json:"-"`
	Instances []Instance `gorm:"foreignKey:RegionID;constraint:OnDelete:CASCADE" json:"instances,omitempty"`
}

// Instance represents an EC2 instance.
type Instance struct {
	gorm.Model
	InstanceID string `gorm:"not null" json:"instance_id"`
	RegionID   uint   `gorm:"not null;index" json:"region_id"`
	Region     Region `gorm:"foreignKey:RegionID" json:"-"`
	Platform   string `gorm:"not null" json:"platform"` // "linux" or "windows"
}
