package model

import "time"

type Site struct {
	ID        uint64    `gorm:"primaryKey" json:"id"`
	SiteID    string    `gorm:"size:64;not null;uniqueIndex" json:"site_id"`
	Name      string    `gorm:"size:128;not null" json:"name"`
	Domain    string    `gorm:"size:255;not null;index" json:"domain"`
	WidgetKey string    `gorm:"size:128;not null;uniqueIndex" json:"widget_key"`
	Status    string    `gorm:"size:32;not null;index" json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Agent struct {
	ID           uint64    `gorm:"primaryKey" json:"id"`
	Email        string    `gorm:"size:190;not null;uniqueIndex" json:"email"`
	PasswordHash string    `gorm:"size:255;not null" json:"-"`
	DisplayName  string    `gorm:"size:128;not null" json:"display_name"`
	Role         string    `gorm:"size:32;not null;index" json:"role"`
	Status       string    `gorm:"size:32;not null;index" json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
