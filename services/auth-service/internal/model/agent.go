package model

import "time"

type Agent struct {
	ID           uint64    `gorm:"primaryKey" json:"id"`
	Email        string    `gorm:"size:190;not null;uniqueIndex" json:"email"`
	PasswordHash string    `gorm:"size:255;not null" json:"-"`
	DisplayName  string    `gorm:"size:128;not null;uniqueIndex" json:"display_name"`
	SiteID       string    `gorm:"size:64;not null;default:'';index" json:"site_id"`
	Role         string    `gorm:"size:32;not null;index" json:"role"`
	Status       string    `gorm:"size:32;not null;index" json:"status"`
	TokenVersion uint64    `gorm:"not null;default:1" json:"token_version"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
