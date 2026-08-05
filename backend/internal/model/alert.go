package model

import "time"

type Alert struct {
	ID        string    `json:"id"`
	Device    string    `json:"device"`
	Metric    string    `json:"metric"`
	Severity  string    `json:"severity"`
	Message   string    `json:"message"`
	Status    string    `json:"status"`
	FiredAt   time.Time `json:"fired_at"`
	CreatedAt time.Time `json:"created_at"`
}
