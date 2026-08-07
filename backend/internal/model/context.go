package model

import "time"

type ScratchpadEntry struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id"`
	Step      string    `json:"step"`
	Content   string    `json:"content"`
	ToolName  string    `json:"tool_name,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type ToolEvent struct {
	ID         string    `json:"id"`
	RunID      string    `json:"run_id"`
	ToolName   string    `json:"tool_name"`
	ArgsJSON   string    `json:"args_json"`
	ResultJSON string    `json:"result_json"`
	Status     string    `json:"status"`
	DurationMS int64     `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
}

const (
	ToolStatusOK    = "ok"
	ToolStatusError = "error"
)

type LongTermMemory struct {
	ID          string    `json:"id"`
	Device      string    `json:"device"`
	Metric      string    `json:"metric"`
	RootCause   string    `json:"root_cause"`
	Confidence  float64   `json:"confidence"`
	ActionsJSON string    `json:"actions_json"`
	RunID       string    `json:"run_id"`
	CreatedAt   time.Time `json:"created_at"`
}
