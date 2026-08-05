package model

import (
	"encoding/json"
	"fmt"
	"time"
)

type TriageResult struct {
	Deduped  bool   `json:"deduped"`
	Proceed  bool   `json:"proceed"`
	Priority string `json:"priority"`
	Reason   string `json:"reason"`
}

type DeviceInfo struct {
	Name  string   `json:"name"`
	Model string   `json:"model"`
	Rack  string   `json:"rack"`
	Zone  string   `json:"zone"`
	Role  string   `json:"role"`
	Notes []string `json:"notes"`
}

type Context struct {
	DeviceInfo    DeviceInfo   `json:"device_info"`
	Neighbors     []DeviceInfo `json:"neighbors"`
	HistoryAlerts []Alert      `json:"history_alerts"`
}

type MetricPoint struct {
	Time  time.Time `json:"time"`
	Value float64   `json:"value"`
}

type LogEntry struct {
	Time    time.Time `json:"time"`
	Source  string    `json:"source"`
	Level   string    `json:"level"`
	Content string    `json:"content"`
}

type Evidence struct {
	Metrics []MetricPoint `json:"metrics"`
	Logs    []LogEntry    `json:"logs"`
	Gaps    []string      `json:"gaps"`
}

type RCA struct {
	RootCause     string   `json:"root_cause"`
	EvidenceChain []string `json:"evidence_chain"`
	Confidence    float64  `json:"confidence"`
	Gaps          []string `json:"gaps"`
}

type Recommendation struct {
	Actions    []string `json:"actions"`
	RunbookRef string   `json:"runbook_ref"`
	Risk       string   `json:"risk"`
}

type PipelineRun struct {
	ID           string    `json:"id"`
	AlertID      string    `json:"alert_id"`
	Status       string    `json:"status"`
	Iteration    int       `json:"iteration"`
	MaxIteration int       `json:"max_iteration"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

const (
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusSkipped   = "skipped"
	StatusDegraded  = "degraded"
	StatusFailed    = "failed"
)

type StageResult struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id"`
	StageID   string    `json:"stage_id"`
	StageName string    `json:"stage_name"`
	Status    string    `json:"status"`
	Iteration int       `json:"iteration"`
	Output    string    `json:"output"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
}

const (
	StagePending   = "pending"
	StageRunning   = "running"
	StageCompleted = "completed"
	StageFailed    = "failed"
	StageSkipped   = "skipped"
)

func NewID(prefix string) string {
	return fmt.Sprintf("%s-%x", prefix, time.Now().UnixNano())
}

func MustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
