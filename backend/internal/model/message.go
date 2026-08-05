package model

import "time"

type Message struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	AlertID   string    `json:"alert_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)
