package model

import "time"

type Conversation struct {
	ID                         uint64     `gorm:"primaryKey" json:"id"`
	SiteID                     string     `gorm:"size:64;not null;index" json:"site_id"`
	VisitorToken               string     `gorm:"size:128;not null;index" json:"visitor_token"`
	Status                     string     `gorm:"size:32;not null;index" json:"status"`
	AssignedAgentID            *uint64    `gorm:"index" json:"assigned_agent_id,omitempty"`
	PendingTransferToAgentID   *uint64    `gorm:"index" json:"pending_transfer_to_agent_id,omitempty"`
	PendingTransferFromAgentID *uint64    `gorm:"index" json:"pending_transfer_from_agent_id,omitempty"`
	PendingTransferRequestedAt *time.Time `gorm:"index" json:"pending_transfer_requested_at,omitempty"`
	CreatedAt                  time.Time  `json:"created_at"`
	UpdatedAt                  time.Time  `json:"updated_at"`
	ClosedAt                   *time.Time `gorm:"index" json:"closed_at,omitempty"`
	ClosedByAgentID            *uint64    `gorm:"index" json:"closed_by_agent_id,omitempty"`
}

type Message struct {
	ID             uint64    `gorm:"primaryKey" json:"id"`
	ConversationID uint64    `gorm:"not null;index;index:idx_conv_client_msg,unique" json:"conversation_id"`
	SenderType     string    `gorm:"size:32;not null;index" json:"sender_type"`
	SenderID       string    `gorm:"size:64" json:"sender_id,omitempty"`
	Content        string    `gorm:"type:text;not null" json:"content"`
	ClientMsgID    string    `gorm:"size:64;not null;index:idx_conv_client_msg,unique" json:"client_msg_id"`
	Status         string    `gorm:"size:16;not null;default:sent;index" json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
