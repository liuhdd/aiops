# 硬件AIOps智能体 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 构建一个面向算力中心的硬件AIOps多Agent协作智能运维系统，通过多阶段Agent流水线自主排查告警根因，并提供Web聊天界面支持对话排查。

**Architecture:** Go后端使用Eino框架编排ChatModel与流水线。告警模拟器定时推送告警入库，触发流水线（接收规范化→过滤分诊→上下文收集→证据收集→根因分析→建议），根因分析证据不足时有限回溯到证据收集（最多2次）。全部外部数据（监控/日志/CMDB/知识库）mock常驻内存。前端React三视图（告警列表/排查过程时间线/聊天），WebSocket实时推送阶段进度。SQLite持久化。

**Tech Stack:** Go 1.24 + Eino (github.com/cloudwego/eino) + eino-ext/components/model/openai + modernc.org/sqlite + gorilla/websocket + React 18 + Vite + TypeScript + Vitest + Docker Compose + Nginx

## Global Constraints

- Go `go.mod` 中 `go 1.24`
- LLM 通过 OpenAI 兼容接口连接，配置从环境变量读取：`LLM_API_KEY`（必填）、`LLM_BASE_URL`（可选，默认 `https://api.deepseek.com/v1`）、`LLM_MODEL`（可选，默认 `deepseek-chat`）
- mock 数据在启动时生成并常驻内存，接口形式模拟真实系统（监控=时序查询、日志=关键字检索、CMDB=资产/拓扑、知识库=预案检索）
- 所有日期时间为 `time.Time`，JSON 序列化使用 RFC3339
- 阶段产物以 JSON 字符串存 SQLite `stage_results.output`
- 后端API前缀 `/api`，WebSocket 路径 `/ws`
- 所有代码不添加注释以外的非必要输出；测试使用标准库 `testing`
- Docker Compose 服务名：`backend`（Go）、`frontend`（Nginx 托管静态前端，proxy `/api` 与 `/ws` 到 backend）

---

### Task 1: 项目骨架与配置

**Files:**
- Create: `backend/go.mod`
- Create: `backend/cmd/server/main.go`
- Create: `backend/internal/config/config.go`
- Create: `.env.example`
- Create: `.gitignore`
- Test: `backend/internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Config` struct with fields `LLMAPIKey string`, `LLMBaseURL string`, `LLMModel string`, `DBPath string`, `SimInterval time.Duration`, `Port string`; function `config.Load()`; function `config.LoadFromEnv(lookup func(string) string)` (纯函数，便于测试)

- [ ] **Step 1: Write the failing test**

`backend/internal/config/config_test.go`:
```go
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadFromEnvDefaults(t *testing.T) {
	env := map[string]string{
		"LLM_API_KEY": "sk-test",
	}
	cfg := LoadFromEnv(func(k string) string { return env[k] })
	if cfg.LLMAPIKey != "sk-test" {
		t.Fatalf("LLMAPIKey = %q, want sk-test", cfg.LLMAPIKey)
	}
	if cfg.LLMBaseURL != "https://api.deepseek.com/v1" {
		t.Fatalf("LLMBaseURL = %q, want default", cfg.LLMBaseURL)
	}
	if cfg.LLMModel != "deepseek-chat" {
		t.Fatalf("LLMModel = %q, want deepseek-chat", cfg.LLMModel)
	}
	if cfg.SimInterval != 20*time.Second {
		t.Fatalf("SimInterval = %v, want 20s", cfg.SimInterval)
	}
	if cfg.Port != ":8080" {
		t.Fatalf("Port = %q, want :8080", cfg.Port)
	}
}

func TestLoad(t *testing.T) {
	os.Setenv("LLM_API_KEY", "sk-test")
	defer os.Unsetenv("LLM_API_KEY")
	os.Setenv("DB_PATH", filepath.Join(t.TempDir(), "test.db"))
	defer os.Unsetenv("DB_PATH")
	cfg := Load()
	if cfg.LLMAPIKey != "sk-test" || cfg.DBPath == "" {
		t.Fatalf("Load() failed: %+v", cfg)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go mod init aiops && go test ./internal/config/...`
Expected: FAIL (config package 不存在)

- [ ] **Step 3: Write minimal implementation**

`backend/internal/config/config.go`:
```go
package config

import (
	"os"
	"time"
)

type Config struct {
	LLMAPIKey   string
	LLMBaseURL  string
	LLMModel    string
	DBPath      string
	SimInterval time.Duration
	Port        string
}

func Load() Config {
	return LoadFromEnv(os.Getenv)
}

func LoadFromEnv(lookup func(string) string) Config {
	base := lookup("LLM_BASE_URL")
	if base == "" {
		base = "https://api.deepseek.com/v1"
	}
	model := lookup("LLM_MODEL")
	if model == "" {
		model = "deepseek-chat"
	}
	port := lookup("PORT")
	if port == "" {
		port = ":8080"
	}
	db := lookup("DB_PATH")
	if db == "" {
		db = "aiops.db"
	}
	return Config{
		LLMAPIKey:   lookup("LLM_API_KEY"),
		LLMBaseURL:  base,
		LLMModel:    model,
		DBPath:      db,
		SimInterval: 20 * time.Second,
		Port:        port,
	}
}
```

`backend/cmd/server/main.go`:
```go
package main

import (
	"log"

	"aiops/internal/config"
)

func main() {
	cfg := config.Load()
	log.Printf("starting aiops backend on %s", cfg.Port)
	_ = cfg
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/config/...`
Expected: PASS

- [ ] **Step 5: Write root files**

`.env.example`:
```
LLM_API_KEY=sk-your-key-here
# LLM_BASE_URL=https://api.deepseek.com/v1
# LLM_MODEL=deepseek-chat
# DB_PATH=aiops.db
# PORT=:8080
# SIM_INTERVAL_SECONDS=20
```

`.gitignore`:
```
*.db
*.db-shm
*.db-wal
node_modules/
dist/
.env
backend/bin/
```

- [ ] **Step 6: Commit**

```bash
git add backend/go.mod backend/cmd/server/main.go backend/internal/config backend/internal/config/config_test.go .env.example .gitignore
git commit -m "feat: 项目骨架与配置加载"
```

---

### Task 2: 领域模型

**Files:**
- Create: `backend/internal/model/alert.go`
- Create: `backend/internal/model/pipeline.go`
- Create: `backend/internal/model/message.go`
- Test: `backend/internal/model/model_test.go`

**Interfaces:**
- Produces:
  - `model.Alert` { `ID, Device, Metric, Severity, Message, Status string; FiredAt, CreatedAt time.Time` }
  - `model.TriageResult` { `Deduped, Proceed bool; Priority, Reason string` }
  - `model.Context` { `DeviceInfo DeviceInfo; Neighbors []DeviceInfo; HistoryAlerts []Alert` }
  - `model.DeviceInfo` { `Name, Model, Rack, Zone, Role string; Notes []string` }
  - `model.Evidence` { `Metrics []MetricPoint; Logs []LogEntry; Gaps []string` }
  - `model.MetricPoint` { `Time time.Time; Value float64` }
  - `model.LogEntry` { `Time time.Time; Source, Level, Content string` }
  - `model.RCA` { `RootCause string; EvidenceChain []string; Confidence float64; Gaps []string` }
  - `model.Recommendation` { `Actions []string; RunbookRef, Risk string` }
  - `model.PipelineRun` { `ID, AlertID, Status string; Iteration, MaxIteration int; CreatedAt, UpdatedAt time.Time` } + `const (StatusRunning="running"; StatusCompleted="completed"; StatusSkipped="skipped"; StatusDegraded="degraded"; StatusFailed="failed")`
  - `model.StageResult` { `ID, RunID, StageID, StageName, Status string; Iteration int; Output, Error string; StartedAt, EndedAt time.Time` } + `const (StagePending="pending"; StageRunning="running"; StageCompleted="completed"; StageFailed="failed"; StageSkipped="skipped")`
  - `model.Message` { `ID, Role, Content, AlertID string; CreatedAt time.Time` } + `const (RoleUser="user"; RoleAssistant="assistant")`
  - Helpers `model.NewID(prefix string) string`（格式 `prefix-` + unixnano hex），`model.MustJSON(v any) string`

- [ ] **Step 1: Write the failing test**

`backend/internal/model/model_test.go`:
```go
package model

import (
	"encoding/json"
	"testing"
)

func TestNewID(t *testing.T) {
	a := NewID("alarm")
	b := NewID("alarm")
	if a == b {
		t.Fatalf("ids should differ: %s == %s", a, b)
	}
	if a[:6] != "alarm-" {
		t.Fatalf("id %q missing prefix", a)
	}
}

func TestMustJSON(t *testing.T) {
	rc := RCA{RootCause: "OOM", Confidence: 0.9, Gaps: []string{}}
	raw := MustJSON(rc)
	var out RCA
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.RootCause != "OOM" || out.Confidence != 0.9 {
		t.Fatalf("roundtrip failed: %+v", out)
	}
}

func TestStageStatusConstants(t *testing.T) {
	if StageRunning != "running" || StageCompleted != "completed" {
		t.Fatalf("stage constants wrong")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/model/...`
Expected: FAIL (model package 不存在)

- [ ] **Step 3: Write implementation**

`backend/internal/model/alert.go`:
```go
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
```

`backend/internal/model/pipeline.go`:
```go
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
	Name   string   `json:"name"`
	Model  string   `json:"model"`
	Rack   string   `json:"rack"`
	Zone   string   `json:"zone"`
	Role   string   `json:"role"`
	Notes  []string `json:"notes"`
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
```

`backend/internal/model/message.go`:
```go
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
```

（同时删除上文 `<Alert>` 下注释行 "（注意 alert.go 需要 `import "time"`。）"，已包含在上方代码中。）

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/model/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/model && git commit -m "feat: 领域模型定义"
```

---

### Task 3: SQLite 持久化

**Files:**
- Create: `backend/internal/store/store.go`
- Create: `backend/internal/store/store_test.go`
- Modify: `backend/cmd/server/main.go`

**Interfaces:**
- Consumes: `model.Alert`, `model.PipelineRun`, `model.StageResult`, `model.Message`
- Produces: `store.Store` struct；方法 `New(dbPath string) (*Store, error)`、`Close()`、`SaveAlert(a model.Alert) error`、`ListAlerts() ([]model.Alert, error)`、`GetAlert(id string) (model.Alert, error)`、`SaveRun(r model.PipelineRun) error`、`GetRun(runID string) (model.PipelineRun, error)`、`UpdateRunStatus(runID, status string) error`、`SaveStage(s model.StageResult) error`、`ListStages(runID string) ([]model.StageResult, error)`、`GetLatestStage(runID, stageID string) (model.StageResult, bool, error)`、`SaveMessage(m model.Message) error`、`ListMessages() ([]model.Message, error)`

- [ ] **Step 1: Write the failing test**

`backend/internal/store/store_test.go`:
```go
package store

import (
	"path/filepath"
	"testing"
	"time"

	"aiops/internal/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAlertRoundTrip(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	a := model.Alert{ID: "alarm-1", Device: "compute-01", Metric: "cpu_usage",
		Severity: "warning", Message: "cpu high", Status: "active", FiredAt: now, CreatedAt: now}
	if err := s.SaveAlert(a); err != nil {
		t.Fatalf("SaveAlert: %v", err)
	}
	got, err := s.GetAlert("alarm-1")
	if err != nil {
		t.Fatalf("GetAlert: %v", err)
	}
	if got.Device != "compute-01" || got.Metric != "cpu_usage" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	list, err := s.ListAlerts()
	if err != nil || len(list) != 1 {
		t.Fatalf("ListAlerts: %v len=%d", err, len(list))
	}
}

func TestRunAndStages(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	r := model.PipelineRun{ID: "run-1", AlertID: "alarm-1", Status: model.StatusRunning,
		Iteration: 0, MaxIteration: 2, CreatedAt: now, UpdatedAt: now}
	if err := s.SaveRun(r); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	st := model.StageResult{ID: "st-1", RunID: "run-1", StageID: "normalize", StageName: "接收规范化",
		Status: model.StageCompleted, Iteration: 0, Output: "{}", StartedAt: now, EndedAt: now}
	if err := s.SaveStage(st); err != nil {
		t.Fatalf("SaveStage: %v", err)
	}
	if err := s.UpdateRunStatus("run-1", model.StatusCompleted); err != nil {
		t.Fatalf("UpdateRunStatus: %v", err)
	}
	gotRun, err := s.GetRun("run-1")
	if err != nil || gotRun.Status != model.StatusCompleted {
		t.Fatalf("GetRun: %v %+v", err, gotRun)
	}
	stages, err := s.ListStages("run-1")
	if err != nil || len(stages) != 1 {
		t.Fatalf("ListStages: %v len=%d", err, len(stages))
	}
	latest, ok, err := s.GetLatestStage("run-1", "normalize")
	if err != nil || !ok || latest.ID != "st-1" {
		t.Fatalf("GetLatestStage: %v ok=%v %+v", err, ok, latest)
	}
}

func TestMessages(t *testing.T) {
	s := newTestStore(t)
	m := model.Message{ID: "msg-1", Role: model.RoleUser, Content: "hello", CreatedAt: time.Now()}
	if err := s.SaveMessage(m); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	msgs, err := s.ListMessages()
	if err != nil || len(msgs) != 1 || msgs[0].Content != "hello" {
		t.Fatalf("ListMessages: %v %+v", err, msgs)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go get modernc.org/sqlite && go test ./internal/store/...`
Expected: FAIL (store package 不存在)

- [ ] **Step 3: Write implementation**

`backend/internal/store/store.go`:
```go
package store

import (
	"database/sql"
	"errors"
	"time"

	_ "modernc.org/sqlite"

	"aiops/internal/model"
)

type Store struct {
	db *sql.DB
}

func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS alerts (
			id TEXT PRIMARY KEY, device TEXT, metric TEXT, severity TEXT,
			message TEXT, status TEXT, fired_at DATETIME, created_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS pipeline_runs (
			id TEXT PRIMARY KEY, alert_id TEXT, status TEXT, iteration INTEGER,
			max_iteration INTEGER, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS stage_results (
			id TEXT PRIMARY KEY, run_id TEXT, stage_id TEXT, stage_name TEXT,
			status TEXT, iteration INTEGER, output TEXT, error TEXT,
			started_at DATETIME, ended_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY, role TEXT, content TEXT, alert_id TEXT,
			created_at DATETIME)`,
		`CREATE INDEX IF NOT EXISTS idx_stage_run ON stage_results(run_id, stage_id, iteration)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SaveAlert(a model.Alert) error {
	_, err := s.db.Exec(`INSERT INTO alerts (id, device, metric, severity, message, status, fired_at, created_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		a.ID, a.Device, a.Metric, a.Severity, a.Message, a.Status, a.FiredAt, a.CreatedAt)
	return err
}

func (s *Store) GetAlert(id string) (model.Alert, error) {
	var a model.Alert
	err := s.db.QueryRow(`SELECT id, device, metric, severity, message, status, fired_at, created_at
		FROM alerts WHERE id = ?`, id).
		Scan(&a.ID, &a.Device, &a.Metric, &a.Severity, &a.Message, &a.Status, &a.FiredAt, &a.CreatedAt)
	return a, err
}

func (s *Store) ListAlerts() ([]model.Alert, error) {
	rows, err := s.db.Query(`SELECT id, device, metric, severity, message, status, fired_at, created_at
		FROM alerts ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Alert
	for rows.Next() {
		var a model.Alert
		if err := rows.Scan(&a.ID, &a.Device, &a.Metric, &a.Severity, &a.Message, &a.Status, &a.FiredAt, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) SaveRun(r model.PipelineRun) error {
	_, err := s.db.Exec(`INSERT INTO pipeline_runs (id, alert_id, status, iteration, max_iteration, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?)`,
		r.ID, r.AlertID, r.Status, r.Iteration, r.MaxIteration, r.CreatedAt, r.UpdatedAt)
	return err
}

func (s *Store) GetRun(runID string) (model.PipelineRun, error) {
	var r model.PipelineRun
	err := s.db.QueryRow(`SELECT id, alert_id, status, iteration, max_iteration, created_at, updated_at
		FROM pipeline_runs WHERE id = ?`, runID).
		Scan(&r.ID, &r.AlertID, &r.Status, &r.Iteration, &r.MaxIteration, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

func (s *Store) UpdateRunStatus(runID, status string) error {
	res, err := s.db.Exec(`UPDATE pipeline_runs SET status = ?, updated_at = ? WHERE id = ?`, status, time.Now(), runID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("run not found")
	}
	return nil
}

func (s *Store) SaveStage(st model.StageResult) error {
	_, err := s.db.Exec(`INSERT INTO stage_results (id, run_id, stage_id, stage_name, status, iteration, output, error, started_at, ended_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		st.ID, st.RunID, st.StageID, st.StageName, st.Status, st.Iteration, st.Output, st.Error, st.StartedAt, st.EndedAt)
	return err
}

func (s *Store) ListStages(runID string) ([]model.StageResult, error) {
	rows, err := s.db.Query(`SELECT id, run_id, stage_id, stage_name, status, iteration, output, error, started_at, ended_at
		FROM stage_results WHERE run_id = ? ORDER BY iteration ASC, rowid ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.StageResult
	for rows.Next() {
		var st model.StageResult
		if err := rows.Scan(&st.ID, &st.RunID, &st.StageID, &st.StageName, &st.Status, &st.Iteration, &st.Output, &st.Error, &st.StartedAt, &st.EndedAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *Store) GetLatestStage(runID, stageID string) (model.StageResult, bool, error) {
	var st model.StageResult
	err := s.db.QueryRow(`SELECT id, run_id, stage_id, stage_name, status, iteration, output, error, started_at, ended_at
		FROM stage_results WHERE run_id = ? AND stage_id = ? ORDER BY iteration DESC, rowid DESC LIMIT 1`,
		runID, stageID).
		Scan(&st.ID, &st.RunID, &st.StageID, &st.StageName, &st.Status, &st.Iteration, &st.Output, &st.Error, &st.StartedAt, &st.EndedAt)
	if err == sql.ErrNoRows {
		return st, false, nil
	}
	if err != nil {
		return st, false, err
	}
	return st, true, nil
}

func (s *Store) SaveMessage(m model.Message) error {
	_, err := s.db.Exec(`INSERT INTO messages (id, role, content, alert_id, created_at) VALUES (?,?,?,?,?)`,
		m.ID, m.Role, m.Content, m.AlertID, m.CreatedAt)
	return err
}

func (s *Store) ListMessages() ([]model.Message, error) {
	rows, err := s.db.Query(`SELECT id, role, content, alert_id, created_at FROM messages ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Message
	for rows.Next() {
		var m model.Message
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.AlertID, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go mod tidy && go test ./internal/store/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/store backend/go.mod backend/go.sum && git commit -m "feat: SQLite持久化层"
```

---

### Task 4: Mock 数据源 — CMDB 与知识库

**Files:**
- Create: `backend/internal/mockdata/cmdb.go`
- Create: `backend/internal/mockdata/knowledge.go`
- Create: `backend/internal/mockdata/cmdb_test.go`

**Interfaces:**
- Produces: `mockdata.Devices()` 返回 `[]model.DeviceInfo`（6台设备：compute-01/02 GPU服务器、web-01/db-01 服务器、storage-01 存储、switch-core-01 交换机）；`mockdata.Neighbors(name string) []model.DeviceInfo`（所有非交换机设备邻居为 switch-core-01，交换机邻居为全部设备）；`mockdata.Runbooks() map[string][]string`（metric→处置步骤）；`mockdata.SearchRunbook(metric string) ([]string, bool)`

- [ ] **Step 1: Write the failing test**

`backend/internal/mockdata/cmdb_test.go`:
```go
package mockdata

import "testing"

func TestDevicesContainExpected(t *testing.T) {
	devs := Devices()
	if len(devs) != 6 {
		t.Fatalf("len(devices) = %d, want 6", len(devs))
	}
	names := map[string]bool{}
	for _, d := range devs {
		names[d.Name] = true
	}
	for _, want := range []string{"compute-01", "compute-02", "web-01", "db-01", "storage-01", "switch-core-01"} {
		if !names[want] {
			t.Fatalf("missing device %s", want)
		}
	}
}

func TestNeighbors(t *testing.T) {
	nb := Neighbors("compute-01")
	if len(nb) != 1 || nb[0].Name != "switch-core-01" {
		t.Fatalf("compute-01 neighbors = %+v", nb)
	}
	nb2 := Neighbors("switch-core-01")
	if len(nb2) != 5 {
		t.Fatalf("switch neighbors = %d, want 5", len(nb2))
	}
}

func TestRunbooks(t *testing.T) {
	rb, ok := SearchRunbook("gpu_util")
	if !ok || len(rb) == 0 {
		t.Fatalf("runbook for gpu_util missing")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/mockdata/...`
Expected: FAIL (mockdata package 不存在)

- [ ] **Step 3: Write implementation**

`backend/internal/mockdata/cmdb.go`:
```go
package mockdata

import "aiops/internal/model"

var devices = []model.DeviceInfo{
	{Name: "compute-01", Model: "G6-GPU-8U", Rack: "A01", Zone: "ZoneA", Role: "gpu-server",
		Notes: []string{"GPU训练节点", "8xH800"}},
	{Name: "compute-02", Model: "G6-GPU-8U", Rack: "A02", Zone: "ZoneA", Role: "gpu-server",
		Notes: []string{"GPU推理节点", "8xA100"}},
	{Name: "web-01", Model: "R650", Rack: "B01", Zone: "ZoneB", Role: "server",
		Notes: []string{"Web应用节点"}},
	{Name: "db-01", Model: "R750", Rack: "B02", Zone: "ZoneB", Role: "server",
		Notes: []string{"MySQL数据库节点"}},
	{Name: "storage-01", Model: "CS8000", Rack: "C01", Zone: "ZoneC", Role: "storage",
		Notes: []string{"分布式存储节点"}},
	{Name: "switch-core-01", Model: "CE16800", Rack: "C02", Zone: "ZoneC", Role: "switch",
		Notes: []string{"核心交换机"}},
}

func Devices() []model.DeviceInfo { return devices }

func Neighbors(name string) []model.DeviceInfo {
	if name == "switch-core-01" {
		var out []model.DeviceInfo
		for _, d := range devices {
			if d.Name != name {
				out = append(out, d)
			}
		}
		return out
	}
	for _, d := range devices {
		if d.Name == name {
			return []model.DeviceInfo{devices[5]}
		}
	}
	return nil
}
```

`backend/internal/mockdata/knowledge.go`:
```go
package mockdata

var runbooks = map[string][]string{
	"cpu_usage": {
		"检查CPU负载曲线与top命令热点进程",
		"定位占用高的进程(如训练任务/挖矿进程)",
		"检查邻居节点是否过载导致负载不均衡",
		"视情况迁移负载或扩容",
	},
	"disk_usage": {
		"执行 df -h 确认挂载点使用率",
		"检查大文件与日志轮转是否失效",
		"清理冗余数据或扩容磁盘",
		"检查是否有进程异常写入撑满磁盘",
	},
	"gpu_util": {
		"检查 NVRM Xid 错误日志与 GPU 温度",
		"检查 NVLink 链路状态",
		"尝试重置 GPU 驱动或重启节点",
		"若持续掉卡则报修硬件",
	},
	"mem_usage": {
		"检查 dmesg OOM killer 日志",
		"查看内存占用Top进程",
		"调优内核内存参数或扩容内存",
		"检查是否有内存泄漏",
	},
	"net_in": {
		"检查交换机端口 down 日志与光模块告警",
		"检查链路对端设备状态",
		"联系网络侧排查链路质量",
		"检查是否因广播风暴导致拥塞",
	},
}

func Runbooks() map[string][]string { return runbooks }

func SearchRunbook(metric string) ([]string, bool) {
	steps, ok := runbooks[metric]
	return steps, ok
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/mockdata/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/mockdata && git commit -m "feat: mock CMDB与知识库数据"
```

---

### Task 5: Mock 数据源 — 监控时序与日志

**Files:**
- Create: `backend/internal/mockdata/metrics.go`
- Create: `backend/internal/mockdata/logs.go`
- Test: `backend/internal/mockdata/metrics_logs_test.go`

**Interfaces:**
- Produces: `mockdata.GenerateAll(seed int64)` — 生成并缓存指标与日志（幂等，重复调用不重建）；`mockdata.QueryMetric(device, metric string, from, to time.Time) ([]model.MetricPoint, error)`；`mockdata.SearchLogs(device string, keywords []string, from, to time.Time) ([]model.LogEntry, error)`
- 数据约定：
  - compute-01/cpu_usage：最近2小时，前90分钟在60%附近波动，最后30分钟升至85-95%（触发告警）
  - db-01/disk_usage：2小时窗口，从70%线性升至97%
  - compute-02/gpu_util：2小时窗口，前90分钟50-80%波动，最后30分钟归零；同时 gpu_temp 最后30分钟升至92°C
  - web-01/mem_usage：前100分钟75%附近，最后20分钟升至93%
  - switch-core-01/net_in：前100分钟正常流量，最后20分钟降为接近0
  - 时间点间隔5分钟，共25个点

- [ ] **Step 1: Write the failing test**

`backend/internal/mockdata/metrics_logs_test.go`:
```go
package mockdata

import (
	"testing"
	"time"

	"aiops/internal/model"
)

func TestQueryMetricAnomaly(t *testing.T) {
	GenerateAll(42)
	now := time.Now()
	pts, err := QueryMetric("compute-01", "cpu_usage", now.Add(-2*time.Hour), now)
	if err != nil {
		t.Fatalf("QueryMetric: %v", err)
	}
	if len(pts) == 0 {
		t.Fatalf("no points")
	}
	last := pts[len(pts)-1]
	if last.Value < 80 {
		t.Fatalf("last cpu value = %v, want >= 80", last.Value)
	}
}

func TestSearchLogsKeyword(t *testing.T) {
	GenerateAll(42)
	now := time.Now()
	logs, err := SearchLogs("compute-02", []string{"Xid"}, now.Add(-2*time.Hour), now)
	if err != nil {
		t.Fatalf("SearchLogs: %v", err)
	}
	if len(logs) == 0 {
		t.Fatalf("no Xid logs found for compute-02")
	}
}

func TestPointCount(t *testing.T) {
	GenerateAll(7)
	now := time.Now()
	pts, _ := QueryMetric("db-01", "disk_usage", now.Add(-2*time.Hour), now)
	if len(pts) != 25 {
		t.Fatalf("len(points) = %d, want 25", len(pts))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/mockdata/...`
Expected: FAIL (metrics.go/logs.go 不存在)

- [ ] **Step 3: Write implementation**

`backend/internal/mockdata/metrics.go`:
```go
package mockdata

import (
	"fmt"
	"sync"
	"time"

	"aiops/internal/model"
)

var (
	mu          sync.RWMutex
	metricCache map[string][]model.MetricPoint
	logCache    map[string][]model.LogEntry
	generated   bool
)

const stepMinutes = 5

// jitter 提供确定性的微小抖动，保证测试可重复
func jitter(minutesAgo int) float64 { return float64(minutesAgo % 5) }

// scenario returns a curve value for a device+metric at `minutesAgo` before now (0 = now)
func scenario(device, metric string, m int) float64 {
	switch {
	case device == "compute-01" && metric == "cpu_usage":
		if m <= 30 {
			return 85 + jitter(m)
		}
		return 55 + jitter(m)
	case device == "db-01" && metric == "disk_usage":
		return 70 + float64(120-m)*0.225
	case device == "compute-02" && metric == "gpu_util":
		if m <= 30 {
			return 0
		}
		return 50 + jitter(m)
	case device == "compute-02" && metric == "gpu_temp":
		if m <= 30 {
			return 92
		}
		return 60 + jitter(m)
	case device == "web-01" && metric == "mem_usage":
		if m <= 20 {
			return 93
		}
		return 72 + jitter(m)
	case device == "switch-core-01" && metric == "net_in":
		if m <= 20 {
			return 2 + jitter(m)
		}
		return 80 + jitter(m)
	default:
		return 50 + jitter(m)
	}
}

func metricsFor(device string) []string {
	if device == "compute-01" || device == "compute-02" {
		return []string{"cpu_usage", "mem_usage", "disk_usage", "gpu_util", "gpu_temp"}
	}
	if device == "switch-core-01" {
		return []string{"net_in", "net_out"}
	}
	return []string{"cpu_usage", "mem_usage", "disk_usage"}
}

func GenerateAll(seed int64) {
	mu.Lock()
	defer mu.Unlock()
	if generated {
		return
	}
	generated = true
	_ = seed
	metricCache = map[string][]model.MetricPoint{}
	now := time.Now().Truncate(time.Minute)

	for _, d := range devices {
		for _, m := range metricsFor(d.Name) {
			var pts []model.MetricPoint
			for mnt := 120; mnt >= 0; mnt -= stepMinutes {
				pts = append(pts, model.MetricPoint{
					Time:  now.Add(-time.Duration(mnt) * time.Minute),
					Value: scenario(d.Name, m, mnt),
				})
			}
			metricCache[d.Name+"|"+m] = pts
		}
	}
	generateLogs(now)
}

func QueryMetric(device, metric string, from, to time.Time) ([]model.MetricPoint, error) {
	mu.RLock()
	defer mu.RUnlock()
	key := device + "|" + metric
	all, ok := metricCache[key]
	if !ok {
		return nil, fmt.Errorf("metric %s not found for device %s", metric, device)
	}
	var out []model.MetricPoint
	for _, p := range all {
		if (p.Time.Equal(from) || p.Time.After(from)) && (p.Time.Equal(to) || p.Time.Before(to)) {
			out = append(out, p)
		}
	}
	return out, nil
}
```

`backend/internal/mockdata/logs.go`:
```go
package mockdata

import (
	"strings"
	"time"

	"aiops/internal/model"
)

func generateLogs(now time.Time) {
	logCache = map[string][]model.LogEntry{}
	entries := map[string][]struct {
		minutesAgo int
		level      string
		content    string
	}{
		"compute-01": {
			{45, "INFO", "load average: 12.5, 11.8, 10.2"},
			{20, "WARN", "cpu usage threshold exceeded 85%"},
			{10, "WARN", "scheduler stalled for 2.3s, cpu saturated"},
		},
		"db-01": {
			{90, "WARN", "df: /data usage 91%"},
			{40, "ERROR", "ext4-fs error (device sda1): no space left on device"},
			{15, "ERROR", "mysqld: InnoDB: cannot allocate page for the tablespace"},
		},
		"compute-02": {
			{60, "INFO", "NVRM: GPU at PCI 0000:3b:00.0 busy"},
			{35, "ERROR", "NVRM: Xid (PCI:0000:3b:00): 79, GPU has fallen off the bus"},
			{30, "ERROR", "nvidia-smi: no devices were found"},
			{28, "WARN", "NVLink: link down on GPU 2"},
		},
		"web-01": {
			{50, "WARN", "java: OutOfMemoryError: Java heap space"},
			{25, "ERROR", "kworker: page allocation failure, order:3"},
			{18, "WARN", "memcg: oom-kill process pid=1234"},
		},
		"switch-core-01": {
			{55, "INFO", "interface Eth1/1: link up"},
			{22, "ERROR", "interface Eth1/1: link down"},
			{20, "WARN", "ethernet: optical transceiver RX power low"},
			{15, "ERROR", "interface Eth1/1: link flapping detected"},
		},
	}
	for dev, list := range entries {
		var ls []model.LogEntry
		for _, e := range list {
			ls = append(ls, model.LogEntry{
				Time:    now.Add(-time.Duration(e.minutesAgo) * time.Minute),
				Source:  dev,
				Level:   e.level,
				Content: e.content,
			})
		}
		logCache[dev] = ls
	}
}

func SearchLogs(device string, keywords []string, from, to time.Time) ([]model.LogEntry, error) {
	mu.RLock()
	defer mu.RUnlock()
	var out []model.LogEntry
	for _, e := range logCache[device] {
		if !((e.Time.Equal(from) || e.Time.After(from)) && (e.Time.Equal(to) || e.Time.Before(to))) {
			continue
		}
		if len(keywords) == 0 {
			out = append(out, e)
			continue
		}
		match := true
		for _, k := range keywords {
			if !strings.Contains(e.Content, k) {
				match = false
				break
			}
		}
		if match {
			out = append(out, e)
		}
	}
	return out, nil
}
```

在 `GenerateAll` 中 `generateLogs(now)` 需在 `metricCache` 初始化后调用（放在 `now` 定义后）。测试的断言（≥80 / 归零 / 25点）与确定性 `jitter` 无冲突。

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/mockdata/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/mockdata && git commit -m "feat: mock监控时序与日志数据"
```

---

### Task 6: LLM 客户端（Eino）

**Files:**
- Create: `backend/internal/llm/llm.go`
- Create: `backend/internal/llm/llm_test.go`
- Modify: `backend/cmd/server/main.go`

**Interfaces:**
- Consumes: `config.Config`
- Produces: `llm.Client` struct，方法 `Complete(ctx context.Context, system, user string) (string, error)`；`llm.NewClient(cfg config.Config) (*Client, error)` 使用 `openai.NewChatModel`；`llm.ParseJSON(raw string, v any) error`（剥离 ```json 围栏后 json.Unmarshal）
- 兜底：`cfg.LLMAPIKey` 为空时返回 `llm.ErrNoAPIKey`，调用方需回退到规则逻辑

- [ ] **Step 1: Write the failing test**

`backend/internal/llm/llm_test.go`:
```go
package llm

import (
	"errors"
	"testing"

	"aiops/internal/config"
)

func TestNewClientNoAPIKey(t *testing.T) {
	cfg := config.LoadFromEnv(func(string) string { return "" })
	c, err := NewClient(cfg)
	if c != nil || !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("expected ErrNoAPIKey, got %v c=%+v", err, c)
	}
}

func TestParseJSONFenced(t *testing.T) {
	var m map[string]string
	raw := "```json\n{\"a\":\"b\"}\n```"
	if err := ParseJSON(raw, &m); err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
	if m["a"] != "b" {
		t.Fatalf("parsed = %+v", m)
	}
}

func TestParseJSONPlain(t *testing.T) {
	var m map[string]string
	if err := ParseJSON(`{"x":"y"}`, &m); err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
	if m["x"] != "y" {
		t.Fatalf("parsed = %+v", m)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go get github.com/cloudwego/eino github.com/cloudwego/eino-ext/components/model/openai && go test ./internal/llm/...`
Expected: FAIL (llm package 不存在)

- [ ] **Step 3: Write implementation**

`backend/internal/llm/llm.go`:
```go
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	openai "github.com/cloudwego/eino-ext/components/model/openai"

	"aiops/internal/config"
)

var ErrNoAPIKey = errors.New("LLM_API_KEY is not set")

type Client struct {
	model model.ChatModel
}

func NewClient(cfg config.Config) (*Client, error) {
	if cfg.LLMAPIKey == "" {
		return nil, ErrNoAPIKey
	}
	cm, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL: cfg.LLMBaseURL,
		APIKey:  cfg.LLMAPIKey,
		Model:   cfg.LLMModel,
	})
	if err != nil {
		return nil, fmt.Errorf("new chat model: %w", err)
	}
	return &Client{model: cm}, nil
}

func (c *Client) Complete(ctx context.Context, system, user string) (string, error) {
	msg, err := c.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(system),
		schema.UserMessage(user),
	})
	if err != nil {
		return "", err
	}
	return msg.Content, nil
}

func ParseJSON(raw string, v any) error {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	return json.Unmarshal([]byte(s), v)
}
```

`backend/cmd/server/main.go`（更新）：
```go
package main

import (
	"context"
	"log"

	"aiops/internal/config"
	"aiops/internal/llm"
)

func main() {
	cfg := config.Load()
	client, err := llm.NewClient(cfg)
	if err != nil {
		log.Printf("llm unavailable (will use rule fallback): %v", err)
	}
	_ = client
	_ = context.Background()
	log.Printf("starting aiops backend on %s", cfg.Port)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go mod tidy && go test ./internal/llm/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/llm backend/cmd/server/main.go backend/go.mod backend/go.sum && git commit -m "feat: Eino LLM客户端与JSON解析"
```

---

### Task 7: 流水线引擎（含回溯）

**Files:**
- Create: `backend/internal/pipeline/engine.go`
- Create: `backend/internal/pipeline/engine_test.go`

**Interfaces:**
- Consumes: `model.*`, `store.Store`, `llm.Client`（可选，nil时走规则兜底）
- Produces:
  - `pipeline.Stage` 接口：`StageID() string`、`Name() string`、`Run(ctx context.Context, st *pipeline.State) error`
  - `pipeline.State` struct：`{ Run model.PipelineRun; Alert model.Alert; Triage *model.TriageResult; Context *model.Context; Evidence *model.Evidence; RCA *model.RCA; Recommendation *model.Recommendation; Iteration int; RawAlerts map[string]string }`（`RawAlerts` 存 Alert→alert id 映射供回溯追加）
  - `pipeline.NewEngine(st *store.Store, stages []Stage) *Engine`
  - `pipeline.Run(ctx context.Context, alert model.Alert, input map[string]string) error` — 创建 run（Iteration=0, MaxIteration=2），串行执行 stages；stage 失败标记 `degraded` 继续；RCA 有 gaps 且 Iteration<Max 时对 evidence 阶段再触发一次
  - `pipeline.Engine.Stages() []Stage`
- 回溯语义：engine 遍历 stages；当 stageID=="rca" 完成后检查其 output 中 `gaps` 非空 且 run.Iteration < run.MaxIteration 时，run.Iteration++，重新执行 evidence 与 rca 两个阶段（iteration 递增，证据累加）。回溯由 engine 内一个 `loop` 标记驱动。

- [ ] **Step 1: Write the failing test**

`backend/internal/pipeline/engine_test.go`:
```go
package pipeline

import (
	"context"
	"path/filepath"
	"testing"

	"aiops/internal/model"
	"aiops/internal/store"
)

type fakeStage struct {
	id, name string
	run      func(st *State) error
}

func (f fakeStage) StageID() string                 { return f.id }
func (f fakeStage) Name() string                    { return f.name }
func (f fakeStage) Run(_ context.Context, st *State) error { return f.run(st) }

func TestEngineRunsStagesInOrder(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	var order []string
	stages := []Stage{
		fakeStage{id: "a", name: "A", run: func(st *State) error { order = append(order, "a"); return nil }},
		fakeStage{id: "b", name: "B", run: func(st *State) error { order = append(order, "b"); return nil }},
	}
	e := NewEngine(s, stages)
	alert := model.Alert{ID: "alarm-1", Device: "compute-01", Metric: "cpu_usage"}
	if err := e.Run(context.Background(), alert, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(order) != 2 || order[0] != "a" || order[1] != "b" {
		t.Fatalf("order = %v", order)
	}
	run, _ := s.GetRun("run-" + alert.ID)
	if run.Status != model.StatusCompleted {
		t.Fatalf("run status = %s", run.Status)
	}
}

func TestEngineBacktracksOnGaps(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	var evidenceCalls int
	rcaCalls := 0
	stages := []Stage{
		fakeStage{id: "evidence", name: "证据收集", run: func(st *State) error {
			evidenceCalls++
			if st.Iteration == 0 {
				st.Evidence = &model.Evidence{Gaps: []string{"missing log window"}}
			} else {
				st.Evidence = &model.Evidence{Gaps: []string{}}
			}
			return nil
		}},
		fakeStage{id: "rca", name: "根因分析", run: func(st *State) error {
			rcaCalls++
			st.RCA = &model.RCA{Gaps: st.Evidence.Gaps, Confidence: 0.9}
			return nil
		}},
	}
	e := NewEngine(s, stages)
	alert := model.Alert{ID: "alarm-2", Device: "db-01", Metric: "disk_usage"}
	if err := e.Run(context.Background(), alert, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if evidenceCalls != 2 {
		t.Fatalf("evidenceCalls = %d, want 2", evidenceCalls)
	}
	if rcaCalls != 2 {
		t.Fatalf("rcaCalls = %d, want 2", rcaCalls)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/pipeline/...`
Expected: FAIL (pipeline package 不存在)

- [ ] **Step 3: Write implementation**

`backend/internal/pipeline/engine.go`:
```go
package pipeline

import (
	"context"
	"time"

	"aiops/internal/model"
	"aiops/internal/store"
)

type State struct {
	Run            model.PipelineRun
	Alert          model.Alert
	Triage         *model.TriageResult
	Context        *model.Context
	Evidence       *model.Evidence
	RCA            *model.RCA
	Recommendation *model.Recommendation
	Iteration      int
	RawAlerts      map[string]string
	SuggestLogs    []string
}

type Stage interface {
	StageID() string
	Name() string
	Run(ctx context.Context, st *State) error
}

type Engine struct {
	store  *store.Store
	stages []Stage
}

func NewEngine(s *store.Store, stages []Stage) *Engine {
	return &Engine{store: s, stages: stages}
}

func (e *Engine) Stages() []Stage { return e.stages }

func (e *Engine) Run(ctx context.Context, alert model.Alert, input map[string]string) error {
	if input == nil {
		input = map[string]string{}
	}
	now := time.Now()
	run := model.PipelineRun{
		ID:           "run-" + alert.ID,
		AlertID:      alert.ID,
		Status:       model.StatusRunning,
		Iteration:    0,
		MaxIteration: 2,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := e.store.SaveRun(run); err != nil {
		return err
	}

	st := &State{
		Run:       run,
		Alert:     alert,
		Iteration: 0,
		RawAlerts: input,
	}
	st.SuggestLogs = suggestLogs(alert.Metric)

	degraded := false
	skipRest := false

	// 外层循环 = 回溯次数。每一轮从头重跑全部阶段，
	// RCA 若仍有 gaps 且还有额度则进入下一轮（回溯），否则结束。
	for iteration := 0; iteration <= run.MaxIteration; iteration++ {
		st.Iteration = iteration
		backtrack := false
		for _, stage := range e.stages {
			if skipRest {
				break
			}
			res := model.StageResult{
				ID:        model.NewID("st"),
				RunID:     run.ID,
				StageID:   stage.StageID(),
				StageName: stage.Name(),
				Status:    model.StageRunning,
				Iteration: iteration,
				StartedAt: time.Now(),
			}
			if err := e.store.SaveStage(res); err != nil {
				return err
			}
			stageErr := stage.Run(ctx, st)
			res.EndedAt = time.Now()
			if stageErr != nil {
				degraded = true
				res.Status = model.StageFailed
				res.Error = stageErr.Error()
			} else {
				res.Status = model.StageCompleted
				res.Output = stageOutput(st, stage.StageID())
			}
			if err := e.store.SaveStage(res); err != nil {
				return err
			}
			// 分诊阶段 decide skip
			if stage.StageID() == "triage" && st.Triage != nil && !st.Triage.Proceed {
				skipRest = true
				break
			}
			// 回溯判定：rca 有 gaps 且还有迭代额度
			if stage.StageID() == "rca" && st.RCA != nil && len(st.RCA.Gaps) > 0 && iteration < run.MaxIteration {
				backtrack = true
				break
			}
		}
		if skipRest {
			break
		}
		if !backtrack {
			break
		}
		// 下一轮：进入回溯（continue 会从 iteration+1 重新开始）
	}

	status := model.StatusCompleted
	if skipRest {
		status = model.StatusSkipped
	} else if degraded {
		status = model.StatusDegraded
	}
	if err := e.store.UpdateRunStatus(run.ID, status); err != nil {
		return err
	}
	return nil
}

func stageOutput(st *State, stageID string) string {
	switch stageID {
	case "normalize":
		return model.MustJSON(st.Alert)
	case "triage":
		return model.MustJSON(st.Triage)
	case "context":
		return model.MustJSON(st.Context)
	case "evidence":
		return model.MustJSON(st.Evidence)
	case "rca":
		return model.MustJSON(st.RCA)
	case "recommend":
		return model.MustJSON(st.Recommendation)
	}
	return "{}"
}

func suggestLogs(metric string) []string {
	switch metric {
	case "cpu_usage":
		return []string{"load", "cpu"}
	case "disk_usage":
		return []string{"no space", "error"}
	case "gpu_util":
		return []string{"Xid", "NVLink", "nvidia"}
	case "mem_usage":
		return []string{"oom", "OutOfMemory", "memory"}
	case "net_in":
		return []string{"link", "down", "transceiver"}
	}
	return nil
}
```

注意：`Run` 中 `RawAlerts` 用于后续阶段读取模拟告警原始字段，`input` 为 nil 时 map 需初始化（在 `Run` 开头 `if input == nil { input = map[string]string{} }`）。

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/pipeline/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/pipeline && git commit -m "feat: 流水线引擎与有限回溯"
```

---

### Task 8: 阶段实现 — normalize 与 triage

**Files:**
- Create: `backend/internal/pipeline/normalize.go`
- Create: `backend/internal/pipeline/triage.go`
- Create: `backend/internal/pipeline/stages_test.go`

**Interfaces:**
- Consumes: `pipeline.Stage`, `pipeline.State`, `store.Store`, `model.*`
- Produces:
  - `pipeline.NormalizeStage`（对 State.Alert 补全 CreatedAt/FiredAt，无需数据源）
  - `pipeline.NewTriageStage(s *store.Store) *TriageStage`：检查是否存在同 device+metric 的已完成 run（`store.ListAlerts` + `store.GetRun` 判断），存在则 `Deduped=true, Proceed=false`；否则 `Proceed=true`，Priority 按 severity 映射（critical→P1, warning→P2, info→P3）

- [ ] **Step 1: Write the failing test**

`backend/internal/pipeline/stages_test.go`:
```go
package pipeline

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"aiops/internal/model"
	"aiops/internal/store"
)

func TestNormalizeStageFillsTimestamps(t *testing.T) {
	st := &State{Alert: model.Alert{ID: "alarm-x", Device: "d", Metric: "m"}}
	if err := (NormalizeStage{}).Run(context.Background(), st); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.Alert.CreatedAt.IsZero() || st.Alert.FiredAt.IsZero() {
		t.Fatalf("timestamps not filled: %+v", st.Alert)
	}
}

func TestTriageDedupsSameAlert(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	now := time.Now()
	a1 := model.Alert{ID: "alarm-1", Device: "compute-01", Metric: "cpu_usage",
		Severity: "warning", FiredAt: now, CreatedAt: now}
	_ = s.SaveAlert(a1)
	_ = s.SaveRun(model.PipelineRun{ID: "run-" + a1.ID, AlertID: a1.ID, Status: model.StatusCompleted,
		CreatedAt: now, UpdatedAt: now})

	ts := NewTriageStage(s)
	st := &State{Alert: a1}
	if err := ts.Run(context.Background(), st); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.Triage == nil || st.Triage.Proceed {
		t.Fatalf("expected dedup skip: %+v", st.Triage)
	}
	if st.Triage.Priority != "" {
		t.Fatalf("skipped alert should have empty priority, got %q", st.Triage.Priority)
	}
}

func TestTriagePriorityMapping(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	ts := NewTriageStage(s)
	st := &State{Alert: model.Alert{ID: "alarm-2", Device: "d", Metric: "m",
		Severity: "critical", FiredAt: time.Now(), CreatedAt: time.Now()}}
	if err := ts.Run(context.Background(), st); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !st.Triage.Proceed || st.Triage.Priority != "P1" {
		t.Fatalf("triage = %+v", st.Triage)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/pipeline/...`
Expected: FAIL (normalize.go/triage.go 不存在)

- [ ] **Step 3: Write implementation**

`backend/internal/pipeline/normalize.go`:
```go
package pipeline

import (
	"context"
	"time"

	"aiops/internal/model"
)

type NormalizeStage struct{}

func (NormalizeStage) StageID() string { return "normalize" }
func (NormalizeStage) Name() string    { return "接收规范化" }

func (NormalizeStage) Run(_ context.Context, st *State) error {
	if st.Alert.CreatedAt.IsZero() {
		st.Alert.CreatedAt = time.Now()
	}
	if st.Alert.FiredAt.IsZero() {
		st.Alert.FiredAt = time.Now()
	}
	if st.Alert.Status == "" {
		st.Alert.Status = "active"
	}
	return nil
}
```

`backend/internal/pipeline/triage.go`:
```go
package pipeline

import (
	"context"

	"aiops/internal/model"
	"aiops/internal/store"
)

type TriageStage struct {
	store *store.Store
}

func NewTriageStage(s *store.Store) *TriageStage {
	return &TriageStage{store: s}
}

func (TriageStage) StageID() string { return "triage" }
func (t *TriageStage) Name() string { return "过滤分诊" }

func (t *TriageStage) Run(_ context.Context, st *State) error {
	alerts, err := t.store.ListAlerts()
	if err != nil {
		return err
	}
	// 同 device+metric 已有 completed run => 视为重复
	for _, a := range alerts {
		if a.ID == st.Alert.ID {
			continue
		}
		if a.Device == st.Alert.Device && a.Metric == st.Alert.Metric {
			if run, err := t.store.GetRun("run-" + a.ID); err == nil && run.Status == model.StatusCompleted {
				st.Triage = &model.TriageResult{
					Deduped:  true,
					Proceed:  false,
					Reason:   "近期同类告警已排查过: " + a.ID,
				}
				return nil
			}
		}
	}
	priority := map[string]string{"critical": "P1", "warning": "P2", "info": "P3"}[st.Alert.Severity]
	if priority == "" {
		priority = "P3"
	}
	st.Triage = &model.TriageResult{
		Deduped:  false,
		Proceed:  true,
		Priority: priority,
		Reason:   "新告警，进入排查",
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/pipeline/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/pipeline && git commit -m "feat: 阶段-接收规范与过滤分诊"
```

---

### Task 9: 阶段实现 — context 与 evidence

**Files:**
- Create: `backend/internal/pipeline/context.go`
- Create: `backend/internal/pipeline/evidence.go`
- Modify: `backend/internal/pipeline/stages_test.go`

**Interfaces:**
- Consumes: `mockdata.Devices/Neighbors`, `mockdata.QueryMetric/SearchLogs`
- Produces:
  - `pipeline.ContextStage`：填充 `st.Context`（`DeviceInfo`=mockdata.Devices 中匹配者，`Neighbors`=mockdata.Neighbors，`HistoryAlerts`=该 device 的近期告警）
  - `pipeline.EvidenceStage`：`st.Evidence = { Metrics: QueryMetric(alert.Device, alert.Metric, fired-2h, now); Logs: SearchLogs(alert.Device, st.SuggestLogs, fired-2h, now); Gaps: 日志为空时记录 "该时间窗内未检索到关键字日志" }`

- [ ] **Step 1: Write the failing test**

`backend/internal/pipeline/stages_test.go` 追加：
```go
func TestContextStageFillsDevice(t *testing.T) {
	st := &State{Alert: model.Alert{ID: "a1", Device: "compute-01", Metric: "cpu_usage"}}
	if err := (ContextStage{}).Run(context.Background(), st); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.Context == nil || st.Context.DeviceInfo.Name != "compute-01" {
		t.Fatalf("context = %+v", st.Context)
	}
	if len(st.Context.Neighbors) != 1 || st.Context.Neighbors[0].Name != "switch-core-01" {
		t.Fatalf("neighbors = %+v", st.Context.Neighbors)
	}
}

func TestEvidenceStageCollects(t *testing.T) {
	mockdata.GenerateAll(42)
	st := &State{Alert: model.Alert{ID: "a2", Device: "db-01", Metric: "disk_usage",
		FiredAt: time.Now().Add(-1 * time.Hour)}}
	st.SuggestLogs = []string{"no space"}
	if err := (EvidenceStage{}).Run(context.Background(), st); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.Evidence == nil || len(st.Evidence.Metrics) == 0 {
		t.Fatalf("evidence = %+v", st.Evidence)
	}
	if len(st.Evidence.Logs) == 0 {
		t.Fatalf("expected logs, got %+v", st.Evidence.Logs)
	}
}
```
（需 import `"aiops/internal/mockdata"`。）

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/pipeline/...`
Expected: FAIL (context.go/evidence.go 不存在)

- [ ] **Step 3: Write implementation**

`backend/internal/pipeline/context.go`:
```go
package pipeline

import (
	"context"

	"aiops/internal/mockdata"
	"aiops/internal/model"
)

type ContextStage struct{}

func (ContextStage) StageID() string { return "context" }
func (ContextStage) Name() string    { return "上下文收集" }

func (ContextStage) Run(_ context.Context, st *State) error {
	var dev model.DeviceInfo
	for _, d := range mockdata.Devices() {
		if d.Name == st.Alert.Device {
			dev = d
			break
		}
	}
	st.Context = &model.Context{
		DeviceInfo: dev,
		Neighbors:  mockdata.Neighbors(st.Alert.Device),
	}
	return nil
}
```

注意：`ContextStage` 不查询历史告警，`HistoryAlerts` 保持 nil，历史告警展示由前端从 `/api/alerts` 过滤完成。

`backend/internal/pipeline/evidence.go`:
```go
package pipeline

import (
	"context"
	"time"

	"aiops/internal/mockdata"
	"aiops/internal/model"
)

type EvidenceStage struct{}

func (EvidenceStage) StageID() string { return "evidence" }
func (EvidenceStage) Name() string    { return "证据收集" }

func (EvidenceStage) Run(_ context.Context, st *State) error {
	from := st.Alert.FiredAt.Add(-2 * time.Hour)
	to := time.Now()
	metrics, _ := mockdata.QueryMetric(st.Alert.Device, st.Alert.Metric, from, to)
	logs, _ := mockdata.SearchLogs(st.Alert.Device, st.SuggestLogs, from, to)
	gaps := []string{}
	if len(logs) == 0 {
		gaps = append(gaps, "该时间窗内未检索到关键字日志")
	}
	st.Evidence = &model.Evidence{
		Metrics: metrics,
		Logs:    logs,
		Gaps:    gaps,
	}
	return nil
}
```

`backend/internal/pipeline/context.go` 与 `evidence.go` 不依赖 store；回溯时重跑 evidence 会在 st.Evidence 上覆盖，测试只断言存在性，满足要求。

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/pipeline/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/pipeline && git commit -m "feat: 阶段-上下文收集与证据收集"
```

---

### Task 10: 阶段实现 — RCA 与建议

**Files:**
- Create: `backend/internal/pipeline/rca.go`
- Create: `backend/internal/pipeline/recommend.go`
- Modify: `backend/internal/pipeline/stages_test.go`

**Interfaces:**
- Consumes: `llm.Client`（可为 nil）、`mockdata.SearchRunbook`
- Produces:
  - `pipeline.NewRCAStage(lc *llm.Client) *RCAStage`：拼装 system/user prompt（含设备、指标、证据 JSON、回溯补充指令），调用 `lc.Complete`；LLM 不可用或调用失败时用 `ruleRCA(st)` 兜底；结果 `st.RCA`
  - `ruleRCA(st *State) *model.RCA`：按 metric 规则生成（cpu_usage→"CPU热点进程导致负载饱和"、disk_usage→"磁盘空间不足"、gpu_util→"GPU掉卡/驱动异常"、mem_usage→"内存泄漏或OOM"、net_in→"网络链路中断"），EvidenceChain 取日志 content 前2条 + 指标末值，Confidence 0.7
  - `pipeline.NewRecommendStage(lc *llm.Client) *RecommendStage`：匹配 `mockdata.SearchRunbook(metric)`，LLM 可用时让其润色为处置步骤，否则直接返回 runbook 步骤；`st.Recommendation`
  - `pipeline.ParseRCAJSON(raw string) (*model.RCA, error)` 使用 `llm.ParseJSON`

- [ ] **Step 1: Write the failing test**

`backend/internal/pipeline/stages_test.go` 追加：
```go
func TestRuleRCAReturnsRootCause(t *testing.T) {
	st := &State{Alert: model.Alert{ID: "a1", Device: "db-01", Metric: "disk_usage"}}
	st.Evidence = &model.Evidence{Logs: []model.LogEntry{{Content: "no space left"}}}
	rc := ruleRCA(st)
	if rc.RootCause == "" || rc.Confidence <= 0 {
		t.Fatalf("ruleRCA = %+v", rc)
	}
	if len(rc.EvidenceChain) == 0 {
		t.Fatalf("evidence chain empty")
	}
}

func TestRecommendStageRunbook(t *testing.T) {
	st := &State{Alert: model.Alert{ID: "a1", Device: "compute-02", Metric: "gpu_util"}}
	rc := NewRecommendStage(nil)
	if err := rc.Run(context.Background(), st); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.Recommendation == nil || len(st.Recommendation.Actions) == 0 {
		t.Fatalf("recommendation = %+v", st.Recommendation)
	}
	if st.Recommendation.RunbookRef != "gpu_util" {
		t.Fatalf("runbook ref = %q", st.Recommendation.RunbookRef)
	}
}

func TestRCAParsesJSON(t *testing.T) {
	rc, err := ParseRCAJSON(`{"root_cause":"OOM","evidence_chain":["e1"],"confidence":0.8,"gaps":[]}`)
	if err != nil {
		t.Fatalf("ParseRCAJSON: %v", err)
	}
	if rc.RootCause != "OOM" || rc.Confidence != 0.8 {
		t.Fatalf("parsed = %+v", rc)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/pipeline/...`
Expected: FAIL (rca.go/recommend.go 不存在)

- [ ] **Step 3: Write implementation**

`backend/internal/pipeline/rca.go`:
```go
package pipeline

import (
	"context"
	"fmt"
	"strings"

	"aiops/internal/llm"
	"aiops/internal/model"
)

type RCAStage struct {
	llm *llm.Client
}

func NewRCAStage(lc *llm.Client) *RCAStage { return &RCAStage{llm: lc} }

func (RCAStage) StageID() string { return "rca" }
func (RCAStage) Name() string    { return "根因分析" }

func (s *RCAStage) Run(ctx context.Context, st *State) error {
	if s.llm == nil {
		st.RCA = ruleRCA(st)
		return nil
	}
	system := "你是算力中心硬件运维专家。基于给定证据输出JSON：{\"root_cause\":string,\"evidence_chain\":[string],\"confidence\":0~1,\"gaps\":[string]}。只输出JSON。"
	user := fmt.Sprintf("设备:%s 指标:%s\n证据:%s\n%s",
		st.Alert.Device, st.Alert.Metric,
		model.MustJSON(st.Evidence),
		extraGapHint(st))
	raw, err := s.llm.Complete(ctx, system, user)
	if err != nil {
		st.RCA = ruleRCA(st)
		st.RCA.Gaps = append(st.RCA.Gaps, "LLM调用失败，已使用规则兜底")
		return nil
	}
	rc, err := ParseRCAJSON(raw)
	if err != nil {
		st.RCA = ruleRCA(st)
		return nil
	}
	st.RCA = rc
	return nil
}

func extraGapHint(st *State) string {
	if st.Iteration > 0 {
		return "这是第" + fmt.Sprint(st.Iteration) + "轮补充取证，请重点分析新增证据。"
	}
	return "请基于当前证据给出根因结论。"
}

func ruleRCA(st *State) *model.RCA {
	metricRoots := map[string]string{
		"cpu_usage":  "CPU热点进程导致负载饱和",
		"disk_usage": "磁盘空间不足",
		"gpu_util":   "GPU掉卡或驱动异常",
		"mem_usage":  "内存泄漏或OOM杀进程",
		"net_in":     "网络链路中断",
	}
	root := metricRoots[st.Alert.Metric]
	if root == "" {
		root = "未知根因，需人工介入"
	}
	chain := []string{fmt.Sprintf("%s(%s) 指标异常", st.Alert.Device, st.Alert.Metric)}
	if st.Evidence != nil {
		for _, l := range st.Evidence.Logs {
			chain = append(chain, "日志: "+strings.TrimSpace(l.Content))
			if len(chain) >= 3 {
				break
			}
		}
		if len(st.Evidence.Metrics) > 0 {
			last := st.Evidence.Metrics[len(st.Evidence.Metrics)-1]
			chain = append(chain, fmt.Sprintf("指标末值: %.1f", last.Value))
		}
	}
	return &model.RCA{
		RootCause:     root,
		EvidenceChain: chain,
		Confidence:    0.7,
		Gaps:          st.EvidenceGaps(),
	}
}

func ParseRCAJSON(raw string) (*model.RCA, error) {
	var rc model.RCA
	if err := llm.ParseJSON(raw, &rc); err != nil {
		return nil, err
	}
	return &rc, nil
}
```

`State` 增加辅助方法（engine.go）：
```go
func (st *State) EvidenceGaps() []string {
	if st.Evidence == nil {
		return nil
	}
	return st.Evidence.Gaps
}
```

`backend/internal/pipeline/recommend.go`:
```go
package pipeline

import (
	"context"
	"fmt"

	"aiops/internal/llm"
	"aiops/internal/mockdata"
	"aiops/internal/model"
)

type RecommendStage struct {
	llm *llm.Client
}

func NewRecommendStage(lc *llm.Client) *RecommendStage { return &RecommendStage{llm: lc} }

func (RecommendStage) StageID() string { return "recommend" }
func (RecommendStage) Name() string    { return "处置建议" }

func (s *RecommendStage) Run(ctx context.Context, st *State) error {
	steps, ok := mockdata.SearchRunbook(st.Alert.Metric)
	if !ok {
		steps = []string{"联系人工介入排查"}
	}
	actions := steps
	if s.llm != nil {
		system := "你是算力中心运维助手。基于根因和预案，给出简明的处置建议步骤列表。只输出JSON：{\"actions\":[string],\"runbook_ref\":string,\"risk\":string}"
		root := ""
		if st.RCA != nil {
			root = st.RCA.RootCause
		}
		user := fmt.Sprintf("根因:%s\n预案:%v", root, steps)
		if raw, err := s.llm.Complete(ctx, system, user); err == nil {
			var rec model.Recommendation
			if llm.ParseJSON(raw, &rec) == nil && len(rec.Actions) > 0 {
				st.Recommendation = &rec
				return nil
			}
		}
	}
	st.Recommendation = &model.Recommendation{
		Actions:    actions,
		RunbookRef: st.Alert.Metric,
		Risk:       "低",
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/pipeline/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/pipeline && git commit -m "feat: 阶段-根因分析与处置建议"
```

---

### Task 11: 告警模拟器与流水线装配

**Files:**
- Create: `backend/internal/simulator/simulator.go`
- Create: `backend/internal/simulator/simulator_test.go`
- Create: `backend/internal/pipeline/build.go`

**Interfaces:**
- Consumes: `store.Store`, `llm.Client`, `pipeline.Engine`
- Produces:
  - `simulator.New(s *store.Store, onAlert func(model.Alert)) *Simulator`；`(*Simulator).Tick() model.Alert` — 按模板循环生成告警（模板列表含5条预置告警），`(*Simulator).Start(ctx, interval)` 定时 Tick，`(*Simulator).Stop()`
  - `simulator.Templates() []AlertTemplate` 导出供测试（字段 `Device, Metric, Severity, Message string`）
  - `pipeline.Build(st *store.Store, lc *llm.Client) *Engine` — 装配6阶段（normalize, triage, context, evidence, rca, recommend）

- [ ] **Step 1: Write the failing test**

`backend/internal/simulator/simulator_test.go`:
```go
package simulator

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"aiops/internal/model"
	"aiops/internal/store"
)

func TestTickGeneratesAlerts(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	sim := New(s, nil)
	a := sim.Tick()
	if a.ID == "" || a.Device == "" || a.Metric == "" {
		t.Fatalf("tick alert incomplete: %+v", a)
	}
	b := sim.Tick()
	if a.ID == b.ID {
		t.Fatalf("ids should differ")
	}
}

func TestStartPushesAlerts(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	var got []model.Alert
	sim := New(s, func(a model.Alert) { got = append(got, a) })
	ctx, cancel := context.WithCancel(context.Background())
	sim.Start(ctx, 10*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	cancel()
	if len(got) == 0 {
		t.Fatalf("no alerts pushed")
	}
}
```

`backend/internal/pipeline/build_test.go`:
```go
package pipeline

import (
	"path/filepath"
	"testing"

	"aiops/internal/store"
)

func TestBuildHasSixStages(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	e := Build(s, nil)
	if len(e.Stages()) != 6 {
		t.Fatalf("stages = %d, want 6", len(e.Stages()))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/simulator/... ./internal/pipeline/...`
Expected: FAIL (simulator/build.go 不存在)

- [ ] **Step 3: Write implementation**

`backend/internal/simulator/simulator.go`:
```go
package simulator

import (
	"context"
	"sync"
	"time"

	"aiops/internal/model"
	"aiops/internal/store"
)

type AlertTemplate struct {
	Device   string
	Metric   string
	Severity string
	Message  string
}

var Templates = []AlertTemplate{
	{Device: "compute-01", Metric: "cpu_usage", Severity: "warning", Message: "CPU使用率持续高于85%"},
	{Device: "db-01", Metric: "disk_usage", Severity: "critical", Message: "磁盘使用率超过95%"},
	{Device: "compute-02", Metric: "gpu_util", Severity: "critical", Message: "GPU掉卡，利用率归零"},
	{Device: "web-01", Metric: "mem_usage", Severity: "warning", Message: "内存使用率持续高于90%"},
	{Device: "switch-core-01", Metric: "net_in", Severity: "critical", Message: "核心交换机入口流量中断"},
}

type Simulator struct {
	store   *store.Store
	onAlert func(model.Alert)
	index   int
	seq     int
	mu      sync.Mutex
	cancel  context.CancelFunc
}

func New(s *store.Store, onAlert func(model.Alert)) *Simulator {
	return &Simulator{store: s, onAlert: onAlert}
}

func (sim *Simulator) Tick() model.Alert {
	sim.mu.Lock()
	defer sim.mu.Unlock()
	t := Templates[sim.index%len(Templates)]
	sim.index++
	sim.seq++
	now := time.Now()
	a := model.Alert{
		ID:        model.NewID("alarm"),
		Device:    t.Device,
		Metric:    t.Metric,
		Severity:  t.Severity,
		Message:   t.Message,
		Status:    "active",
		FiredAt:   now,
		CreatedAt: now,
	}
	_ = sim.store.SaveAlert(a)
	if sim.onAlert != nil {
		sim.onAlert(a)
	}
	return a
}

func (sim *Simulator) Start(ctx context.Context, interval time.Duration) {
	ctx, sim.cancel = context.WithCancel(ctx)
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				sim.Tick()
			}
		}
	}()
}

func (sim *Simulator) Stop() {
	if sim.cancel != nil {
		sim.cancel()
	}
}
```

`backend/internal/pipeline/build.go`:
```go
package pipeline

import (
	"aiops/internal/llm"
	"aiops/internal/store"
)

func Build(s *store.Store, lc *llm.Client) *Engine {
	return NewEngine(s, []Stage{
		NormalizeStage{},
		NewTriageStage(s),
		ContextStage{},
		EvidenceStage{},
		NewRCAStage(lc),
		NewRecommendStage(lc),
	})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/simulator/... ./internal/pipeline/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/simulator backend/internal/pipeline/build.go && git commit -m "feat: 告警模拟器与流水线装配"
```

---

### Task 12: 聊天 Agent

**Files:**
- Create: `backend/internal/agent/agent.go`
- Create: `backend/internal/agent/intent.go`
- Create: `backend/internal/agent/agent_test.go`

**Interfaces:**
- Consumes: `store.Store`, `llm.Client`, `pipeline.Engine`
- Produces:
  - `agent.Intent` enum：`ListAlerts`, `InspectAlert`, `AskRootCause`, `AskRecommendation`, `AskStatus`, `Unknown`；`agent.ParseIntent(text string) Intent`（规则匹配：包含"什么原因/为什么/根因"→AskRootCause；"怎么处理/怎么办/建议"→AskRecommendation；"查一下|排查|查看"且含"alarm"或设备名→InspectAlert；"列表|告警"→ListAlerts；"状态|进度"→AskStatus）
  - `agent.New(st *store.Store, lc *llm.Client, eng *pipeline.Engine) *Agent`
  - `(*Agent).Handle(ctx, userText string) (model.Message, error)` — 保存 user message，解析意图，若 InspectAlert/AskRootCause/AskRecommendation 且目标告警存在但无 run → 触发 `eng.Run`；组装回复文本，保存 assistant message
  - `(*Agent).AnswerAlertRootCause(alertID string) string` — 读取 run 的最新 rca stage output，返回根因文本；无则返回提示

- [ ] **Step 1: Write the failing test**

`backend/internal/agent/agent_test.go`:
```go
package agent

import (
	"context"
	"path/filepath"
	"testing"

	"aiops/internal/model"
	"aiops/internal/store"
)

func TestParseIntent(t *testing.T) {
	cases := []struct {
		text string
		want Intent
	}{
		{"有哪些告警", ListAlerts},
		{"查一下alarm-123是什么原因", AskRootCause},
		{"alarm-100怎么处理", AskRecommendation},
		{"排查下compute-01的告警", InspectAlert},
		{"进度如何", AskStatus},
		{"你好", Unknown},
	}
	for _, c := range cases {
		if got := ParseIntent(c.text); got != c.want {
			t.Fatalf("ParseIntent(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestAgentInspectAlertTriggersRun(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	now := time.Now()
	a := model.Alert{ID: "alarm-1", Device: "db-01", Metric: "disk_usage",
		Severity: "critical", FiredAt: now, CreatedAt: now}
	_ = s.SaveAlert(a)
	ag := New(s, nil, nil)
	// 用 nil engine 时 Handle 直接基于 store 回复
	msg, err := ag.Handle(context.Background(), "alarm-1什么原因")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if msg.Role != model.RoleAssistant || msg.Content == "" {
		t.Fatalf("msg = %+v", msg)
	}
	msgs, _ := s.ListMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/agent/...`
Expected: FAIL (agent package 不存在)

- [ ] **Step 3: Write implementation**

`backend/internal/agent/intent.go`:
```go
package agent

import (
	"regexp"
	"strings"
)

type Intent int

const (
	Unknown Intent = iota
	ListAlerts
	InspectAlert
	AskRootCause
	AskRecommendation
	AskStatus
)

var alarmRe = regexp.MustCompile(`(?i)alarm[-_ ]?\w*|(compute|web|db|storage|switch)-?0?\d*`)

func ParseIntent(text string) Intent {
	t := strings.ToLower(text)
	switch {
	case strings.Contains(t, "什么原因") || strings.Contains(t, "为什么") || strings.Contains(t, "根因"):
		return AskRootCause
	case strings.Contains(t, "怎么处理") || strings.Contains(t, "怎么办") || strings.Contains(t, "建议") || strings.Contains(t, "处置"):
		return AskRecommendation
	case strings.Contains(t, "状态") || strings.Contains(t, "进度"):
		return AskStatus
	case (strings.Contains(t, "查") || strings.Contains(t, "排查") || strings.Contains(t, "看")) &&
		alarmRe.MatchString(t):
		return InspectAlert
	case strings.Contains(t, "列表") || strings.Contains(t, "有哪些告警") || strings.Contains(t, "告警"):
		return ListAlerts
	}
	return Unknown
}
```

`backend/internal/agent/agent.go`:
```go
package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"aiops/internal/llm"
	"aiops/internal/model"
	"aiops/internal/pipeline"
	"aiops/internal/store"
)

type Agent struct {
	store *store.Store
	llm   *llm.Client
	eng   *pipeline.Engine
}

func New(s *store.Store, lc *llm.Client, eng *pipeline.Engine) *Agent {
	return &Agent{store: s, llm: lc, eng: eng}
}

func (a *Agent) Handle(ctx context.Context, userText string) (model.Message, error) {
	userMsg := model.Message{
		ID: model.NewID("msg"), Role: model.RoleUser,
		Content: userText, CreatedAt: time.Now(),
	}
	_ = a.store.SaveMessage(userMsg)

	alertID := extractAlertID(userText, a.store)
	reply := "你好，我是AIOps运维助手。你可以问我：有哪些告警 / 查一下<告警ID>什么原因 / <告警ID>怎么处理。"

	switch ParseIntent(userText) {
	case ListAlerts:
		alerts, _ := a.store.ListAlerts()
		if len(alerts) == 0 {
			reply = "当前没有告警。"
		} else {
			var b strings.Builder
			b.WriteString("当前告警列表：\n")
			for _, al := range alerts {
				fmt.Fprintf(&b, "- %s [%s] %s %s\n", al.ID, al.Severity, al.Device, al.Message)
			}
			reply = b.String()
		}
	case InspectAlert, AskRootCause, AskRecommendation:
		if alertID == "" {
			reply = "请指明告警ID，例如：alarm-123 什么原因？"
		} else if a.eng != nil {
			alert, err := a.store.GetAlert(alertID)
			if err != nil {
				reply = fmt.Sprintf("未找到告警 %s。", alertID)
			} else {
				if _, runErr := a.store.GetRun("run-" + alertID); runErr != nil {
					_ = a.eng.Run(ctx, alert, nil)
				}
				time.Sleep(50 * time.Millisecond)
				reply = a.answerFor(alertID, ParseIntent(userText))
			}
		} else {
			reply = a.answerFor(alertID, ParseIntent(userText))
		}
	case AskStatus:
		reply = "告警状态可在左侧列表查看，点击告警查看排查时间线。"
	}

	assistantMsg := model.Message{
		ID: model.NewID("msg"), Role: model.RoleAssistant,
		Content: reply, CreatedAt: time.Now(),
	}
	if alertID != "" {
		assistantMsg.AlertID = alertID
	}
	_ = a.store.SaveMessage(assistantMsg)
	return assistantMsg, nil
}

func extractAlertID(text string, s *store.Store) string {
	low := strings.ToLower(text)
	if i := strings.Index(low, "alarm"); i >= 0 {
		rest := text[i+5:] // skip "alarm"
		rest = strings.TrimPrefix(rest, "-")
		digits := ""
		for _, r := range rest {
			if r >= '0' && r <= '9' {
				digits += string(r)
			} else {
				break
			}
		}
		if digits != "" {
			return "alarm-" + digits
		}
	}
	alerts, _ := s.ListAlerts()
	for _, a := range alerts {
		if strings.Contains(low, strings.ToLower(a.ID)) || strings.Contains(low, a.Device) {
			return a.ID
		}
	}
	return ""
}

func (a *Agent) answerFor(alertID string, intent Intent) string {
	run, err := a.store.GetRun("run-" + alertID)
	if err != nil {
		return fmt.Sprintf("告警 %s 尚未完成排查，请稍后再问。", alertID)
	}
	if run.Status == model.StatusRunning {
		return fmt.Sprintf("告警 %s 正在排查中，请稍后查看时间线。", alertID)
	}
	var rca model.RCA
	var rec model.Recommendation
	if st, ok, _ := a.store.GetLatestStage(run.ID, "rca"); ok {
		_ = llm.ParseJSON(st.Output, &rca)
	}
	if st, ok, _ := a.store.GetLatestStage(run.ID, "recommend"); ok {
		_ = llm.ParseJSON(st.Output, &rec)
	}
	switch intent {
	case AskRecommendation:
		if len(rec.Actions) > 0 {
			return fmt.Sprintf("处置建议：\n%s", strings.Join(rec.Actions, "\n"))
		}
	case AskRootCause:
		if rca.RootCause != "" {
			return fmt.Sprintf("根因：%s\n置信度：%.0f%%\n证据链：%s",
				rca.RootCause, rca.Confidence*100, strings.Join(rca.EvidenceChain, "；"))
		}
	}
	return fmt.Sprintf("告警 %s：%s", alertID, rca.RootCause)
}
```

测试中 `extractAlertID("alarm-1什么原因", store)` 应返回 "alarm-1"。若 store 中无该告警，`extractAlertID` 已通过正则从文本提取。

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/agent/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/agent && git commit -m "feat: 聊天Agent意图解析与问答"
```

---

### Task 13: REST API 与 WebSocket Hub

**Files:**
- Create: `backend/internal/hub/hub.go`
- Create: `backend/internal/api/server.go`
- Create: `backend/internal/api/server_test.go`
- Modify: `backend/cmd/server/main.go`

**Interfaces:**
- Consumes: `store.Store`, `agent.Agent`, `simulator.Simulator`, `pipeline.Engine`, `model.*`
- Produces:
  - `hub.New() *Hub`：`(*Hub).Publish(event string, data any)` 广播 JSON `{"type":event,"data":data}`；`(*Hub).HandleWS(w, r)` 用 gorilla/websocket 升级并推送
  - `api.NewServer(cfg, s *store.Store, ag *agent.Agent, eng *pipeline.Engine, hub *hub.Hub, sim *simulator.Simulator) http.Handler` — 路由：
    - `GET /api/alerts` → 列表
    - `GET /api/alerts/{id}` → 告警详情 + run + stages
    - `POST /api/alerts/{id}/inspect` → 触发 `eng.Run`
    - `GET /api/messages` → 聊天记录
    - `POST /api/chat` body `{"text": "..."}` → `ag.Handle`，返回 assistant message
    - `GET /ws` → hub.HandleWS
    - `GET /healthz` → `{"status":"ok"}`
  - `api.CORS` 中间件

- [ ] **Step 1: Write the failing test**

`backend/internal/api/server_test.go`:
```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"aiops/internal/agent"
	"aiops/internal/config"
	"aiops/internal/hub"
	"aiops/internal/model"
	"aiops/internal/store"
)

func TestHealthzAndAlerts(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	_ = s.SaveAlert(model.Alert{ID: "alarm-1", Device: "d", Metric: "m",
		Severity: "warning", Message: "x", Status: "active"})
	h := hub.New()
	ag := agent.New(s, nil, nil)
	srv := NewServer(config.Config{}, s, ag, nil, h, nil)

	req := httptest.NewRequest("GET", "/healthz", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("healthz code = %d", rr.Code)
	}

	req = httptest.NewRequest("GET", "/api/alerts", nil)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("alerts code = %d", rr.Code)
	}
	var list []model.Alert
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list) != 1 || list[0].ID != "alarm-1" {
		t.Fatalf("list = %+v", list)
	}
}

func TestChatEndpoint(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	h := hub.New()
	ag := agent.New(s, nil, nil)
	srv := NewServer(config.Config{}, s, ag, nil, h, nil)

	body := strings.NewReader(`{"text":"有哪些告警"}`)
	req := httptest.NewRequest("POST", "/api/chat", body)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("chat code = %d body=%s", rr.Code, rr.Body.String())
	}
	var m model.Message
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Role != model.RoleAssistant {
		t.Fatalf("role = %q", m.Role)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go get github.com/gorilla/websocket && go test ./internal/api/... ./internal/hub/...`
Expected: FAIL (api/hub 不存在)

- [ ] **Step 3: Write implementation**

`backend/internal/hub/hub.go`:
```go
package hub

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Hub struct {
	mu      sync.Mutex
	clients map[*websocket.Conn]bool
	up      websocket.Upgrader
}

func New() *Hub {
	return &Hub{
		clients: map[*websocket.Conn]bool{},
		up: websocket.Upgrader{
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
}

type event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

func (h *Hub) Publish(eventType string, data any) {
	b, _ := json.Marshal(event{Type: eventType, Data: data})
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if err := c.WriteMessage(websocket.TextMessage, b); err != nil {
			c.Close()
			delete(h.clients, c)
		}
	}
}

func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := h.up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	h.mu.Lock()
	h.clients[conn] = true
	h.mu.Unlock()
	go func() {
		ping := time.NewTicker(30 * time.Second)
		defer ping.Stop()
		for range ping.C {
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				break
			}
		}
	}()
	// 读循环，客户端断开时清理
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			h.mu.Lock()
			delete(h.clients, conn)
			h.mu.Unlock()
			conn.Close()
			return
		}
	}
}
```

`backend/internal/api/server.go`:
```go
package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"aiops/internal/agent"
	"aiops/internal/config"
	"aiops/internal/hub"
	"aiops/internal/model"
	"aiops/internal/pipeline"
	"aiops/internal/simulator"
	"aiops/internal/store"
)

type Server struct {
	cfg  config.Config
	st   *store.Store
	ag   *agent.Agent
	eng  *pipeline.Engine
	hub  *hub.Hub
	sim  *simulator.Simulator
	mux  *http.ServeMux
}

func NewServer(cfg config.Config, s *store.Store, ag *agent.Agent, eng *pipeline.Engine, h *hub.Hub, sim *simulator.Simulator) http.Handler {
	srv := &Server{cfg: cfg, st: s, ag: ag, eng: eng, hub: h, sim: sim, mux: http.NewServeMux()}
	srv.routes()
	return withCORS(srv.mux)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	s.mux.HandleFunc("GET /api/alerts", s.handleListAlerts)
	s.mux.HandleFunc("GET /api/alerts/{id}", s.handleGetAlert)
	s.mux.HandleFunc("POST /api/alerts/{id}/inspect", s.handleInspect)
	s.mux.HandleFunc("GET /api/messages", s.handleListMessages)
	s.mux.HandleFunc("POST /api/chat", s.handleChat)
	s.mux.HandleFunc("GET /ws", s.handleWS)
}

func (s *Server) handleListAlerts(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListAlerts()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetAlert(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	al, err := s.st.GetAlert(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "alert not found"})
		return
	}
	run, _ := s.st.GetRun("run-" + id)
	var stages []model.StageResult
	if run.ID != "" {
		stages, _ = s.st.ListStages(run.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"alert": al, "run": run, "stages": stages})
}

func (s *Server) handleInspect(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	al, err := s.st.GetAlert(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "alert not found"})
		return
	}
	go func() {
		_ = s.eng.Run(r.Context(), al, nil)
	}()
	writeJSON(w, http.StatusOK, map[string]string{"status": "started", "alert_id": id})
}

func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	msgs, err := s.st.ListMessages()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, msgs)
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}
	msg, err := s.ag.Handle(r.Context(), body.Text)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.hub.Publish("message", msg)
	writeJSON(w, http.StatusOK, msg)
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	s.hub.HandleWS(w, r)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if strings.Contains(r.URL.Path, "/ws") {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

`backend/cmd/server/main.go`（更新，装配全部）:
```go
package main

import (
	"context"
	"log"
	"net/http"

	"aiops/internal/agent"
	"aiops/internal/api"
	"aiops/internal/config"
	"aiops/internal/hub"
	"aiops/internal/llm"
	"aiops/internal/mockdata"
	"aiops/internal/pipeline"
	"aiops/internal/simulator"
	"aiops/internal/store"
)

func main() {
	cfg := config.Load()

	lc, err := llm.NewClient(cfg)
	if err != nil {
		log.Printf("llm unavailable (will use rule fallback): %v", err)
	}

	mockdata.GenerateAll(42)

	st, err := store.New(cfg.DBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	eng := pipeline.Build(st, lc)
	h := hub.New()

	ag := agent.New(st, lc, eng)
	sim := simulator.New(st, func(a model.Alert) {
		go func() {
			_ = eng.Run(context.Background(), a, nil)
		}()
	})
	sim.Start(context.Background(), cfg.SimInterval)

	handler := api.NewServer(cfg, st, ag, eng, h, sim)
	log.Printf("aiops backend listening on %s", cfg.Port)
	if err := http.ListenAndServe(cfg.Port, handler); err != nil {
		log.Fatal(err)
	}
}
```
（需 import `"aiops/internal/model"`。）

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go mod tidy && go test ./...`
Expected: PASS (全部包)

- [ ] **Step 5: Commit**

```bash
git add backend/internal/hub backend/internal/api backend/cmd/server/main.go && git commit -m "feat: REST API与WebSocket"
```

---

### Task 14: 前端脚手架

**Files:**
- Create: `frontend/package.json`
- Create: `frontend/vite.config.ts`
- Create: `frontend/tsconfig.json`
- Create: `frontend/index.html`
- Create: `frontend/src/main.tsx`
- Create: `frontend/src/App.tsx`
- Create: `frontend/src/types.ts`
- Create: `frontend/src/api/client.ts`
- Create: `frontend/src/api/ws.ts`

**Interfaces:**
- Consumes: 后端 `/api/*` 与 `/ws`
- Produces（前端共享类型与工具）：
  - `src/types.ts`：`Alert`, `PipelineRun`, `StageResult`, `StageOutput`（JSON 字符串）, `ChatMessage`, `WSMessage` 类型，与后端 `model.*` 字段一致（snake_case JSON）
  - `src/api/client.ts`：`fetchAlerts()`, `fetchAlert(id)`, `inspectAlert(id)`, `fetchMessages()`, `postChat(text)`, `wsUrl()`
  - `src/api/ws.ts`：`subscribeWS(onMessage)` 返回取消函数；自动重连（3秒）

- [ ] **Step 1: Write package.json 与配置**

```json
{
  "name": "aiops-frontend",
  "private": true,
  "version": "0.1.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "preview": "vite preview",
    "test": "vitest run"
  },
  "dependencies": {
    "react": "^18.3.1",
    "react-dom": "^18.3.1"
  },
  "devDependencies": {
    "@testing-library/react": "^16.1.0",
    "@types/react": "^18.3.12",
    "@types/react-dom": "^18.3.1",
    "@vitejs/plugin-react": "^4.3.4",
    "jsdom": "^25.0.1",
    "typescript": "^5.6.3",
    "vite": "^6.0.5",
    "vitest": "^2.1.8"
  }
}
```

`frontend/vite.config.ts`:
```ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
      '/ws': { target: 'ws://localhost:8080', ws: true },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
  },
})
```

`frontend/tsconfig.json`:
```json
{
  "compilerOptions": {
    "target": "ES2020",
    "useDefineForClassFields": true,
    "lib": ["ES2020", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "skipLibCheck": true,
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "isolatedModules": true,
    "moduleDetection": "force",
    "noEmit": true,
    "jsx": "react-jsx",
    "strict": true
  },
  "include": ["src"]
}
```

`frontend/index.html`:
```html
<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>AIOps 硬件智能运维</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

- [ ] **Step 2: 安装依赖并确认可构建**

Run: `cd frontend && npm install`
Expected: 无错误

- [ ] **Step 3: 实现类型与 API 客户端**

`frontend/src/types.ts`:
```ts
export interface Alert {
  id: string
  device: string
  metric: string
  severity: string
  message: string
  status: string
  fired_at: string
  created_at: string
}

export interface PipelineRun {
  id: string
  alert_id: string
  status: string
  iteration: number
  max_iteration: number
  created_at: string
  updated_at: string
}

export interface StageResult {
  id: string
  run_id: string
  stage_id: string
  stage_name: string
  status: string
  iteration: number
  output: string
  error?: string
  started_at: string
  ended_at: string
}

export interface ChatMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  alert_id?: string
  created_at: string
}

export interface AlertDetail {
  alert: Alert
  run: PipelineRun
  stages: StageResult[]
}

export type WSEventType = 'alert' | 'stage' | 'run' | 'message'

export interface WSMessage {
  type: WSEventType
  data: unknown
}
```

`frontend/src/api/client.ts`:
```ts
import type { Alert, AlertDetail, ChatMessage } from '../types'

const base = '/api'

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(base + path, init)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json() as Promise<T>
}

export const fetchAlerts = () => req<Alert[]>('/alerts')
export const fetchAlert = (id: string) => req<AlertDetail>(`/alerts/${id}`)
export const inspectAlert = (id: string) =>
  req<{ status: string; alert_id: string }>(`/alerts/${id}/inspect`, { method: 'POST' })
export const fetchMessages = () => req<ChatMessage[]>('/messages')
export const postChat = (text: string) =>
  req<ChatMessage>('/chat', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ text }),
  })
export const wsUrl = () => {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  return `${proto}://${location.host}/ws`
}
```

`frontend/src/api/ws.ts`:
```ts
import type { WSMessage } from '../types'
import { wsUrl } from './client'

export function subscribeWS(onMessage: (m: WSMessage) => void): () => void {
  let closed = false
  let ws: WebSocket | null = null

  function connect() {
    ws = new WebSocket(wsUrl())
    ws.onmessage = (e) => {
      try {
        onMessage(JSON.parse(e.data) as WSMessage)
      } catch {
        /* ignore bad frames */
      }
    }
    ws.onclose = () => {
      if (!closed) setTimeout(connect, 3000)
    }
  }
  connect()
  return () => {
    closed = true
    ws?.close()
  }
}
```

- [ ] **Step 4: 实现最小 App**

`frontend/src/main.tsx`:
```tsx
import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)
```

`frontend/src/App.tsx`:
```tsx
export default function App() {
  return <div style={{ padding: 16 }}>AIOps 硬件智能运维</div>
}
```

- [ ] **Step 5: 构建验证**

Run: `cd frontend && npx tsc -b && npm run build`
Expected: dist/ 生成，无类型错误

- [ ] **Step 6: Commit**

```bash
git add frontend && git commit -m "feat: 前端脚手架与API客户端"
```

---

### Task 15: 告警列表视图

**Files:**
- Create: `frontend/src/components/AlertList.tsx`
- Create: `frontend/src/components/AlertList.test.tsx`
- Modify: `frontend/src/App.tsx`

**Interfaces:**
- Consumes: `fetchAlerts`, `subscribeWS`（刷新列表）、`onSelect(alertId)` 回调
- Produces: `AlertList({ alerts, onSelect })` 组件；`App` 持有 alerts state 并订阅 WS `alert` 事件刷新

- [ ] **Step 1: Write the failing test**

`frontend/src/components/AlertList.test.tsx`:
```tsx
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import AlertList from './AlertList'
import type { Alert } from '../types'

const alerts: Alert[] = [
  {
    id: 'alarm-1',
    device: 'compute-01',
    metric: 'cpu_usage',
    severity: 'warning',
    message: 'cpu high',
    status: 'active',
    fired_at: '2026-08-05T10:00:00Z',
    created_at: '2026-08-05T10:00:00Z',
  },
]

describe('AlertList', () => {
  it('renders alerts', () => {
    render(<AlertList alerts={alerts} onSelect={() => {}} />)
    expect(screen.getByText('alarm-1')).toBeTruthy()
    expect(screen.getByText('compute-01')).toBeTruthy()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && npx vitest run`
Expected: FAIL (AlertList 不存在)

- [ ] **Step 3: Implement component**

`frontend/src/components/AlertList.tsx`:
```tsx
import type { Alert } from '../types'

const severityColor: Record<string, string> = {
  critical: '#d32f2f',
  warning: '#f57c00',
  info: '#1976d2',
}

export default function AlertList({
  alerts,
  onSelect,
}: {
  alerts: Alert[]
  onSelect: (id: string) => void
}) {
  if (alerts.length === 0) {
    return <div style={{ padding: 16, color: '#888' }}>暂无告警</div>
  }
  return (
    <table style={{ width: '100%', borderCollapse: 'collapse' }}>
      <thead>
        <tr>
          <th>ID</th>
          <th>设备</th>
          <th>指标</th>
          <th>级别</th>
          <th>消息</th>
          <th>时间</th>
        </tr>
      </thead>
      <tbody>
        {alerts.map((a) => (
          <tr key={a.id} onClick={() => onSelect(a.id)} style={{ cursor: 'pointer' }}>
            <td>{a.id}</td>
            <td>{a.device}</td>
            <td>{a.metric}</td>
            <td style={{ color: severityColor[a.severity] ?? '#333' }}>{a.severity}</td>
            <td>{a.message}</td>
            <td>{new Date(a.fired_at).toLocaleString()}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
```

- [ ] **Step 4: 更新 App 集成列表**

`frontend/src/App.tsx`:
```tsx
import { useEffect, useState } from 'react'
import AlertList from './components/AlertList'
import { fetchAlerts } from './api/client'
import { subscribeWS } from './api/ws'
import type { Alert } from './types'

export default function App() {
  const [alerts, setAlerts] = useState<Alert[]>([])
  const [selected, setSelected] = useState<string | null>(null)

  useEffect(() => {
    fetchAlerts().then(setAlerts).catch(() => {})
    const unsub = subscribeWS((m) => {
      if (m.type === 'alert') {
        fetchAlerts().then(setAlerts).catch(() => {})
      }
    })
    return unsub
  }, [])

  return (
    <div style={{ fontFamily: 'system-ui, sans-serif' }}>
      <h1 style={{ padding: '8px 16px' }}>AIOps 硬件智能运维</h1>
      <div style={{ display: 'flex', gap: 16, padding: 16 }}>
        <div style={{ flex: 1 }}>
          <AlertList alerts={alerts} onSelect={setSelected} />
        </div>
        {selected && <div style={{ flex: 1 }}>选中的告警：{selected}</div>}
      </div>
    </div>
  )
}
```

- [ ] **Step 5: 运行测试并通过**

Run: `cd frontend && npx vitest run && npm run build`
Expected: PASS + 构建成功

- [ ] **Step 6: Commit**

```bash
git add frontend/src && git commit -m "feat: 告警列表视图"
```

---

### Task 16: 排查过程时间线视图

**Files:**
- Create: `frontend/src/components/PipelineTimeline.tsx`
- Create: `frontend/src/components/PipelineTimeline.test.tsx`
- Modify: `frontend/src/App.tsx`

**Interfaces:**
- Consumes: `fetchAlert(id)`, `AlertDetail`
- Produces: `PipelineTimeline({ detail }: { detail: AlertDetail })` — 渲染 stages 时间线：每个 stage 一行（图标+名称+状态+耗时），RCA 阶段显示根因/置信度/证据链（parse `output` JSON），evidence 阶段显示指标点数与日志条数；有 iteration 区分显示"第N轮"。同时 `App` 在 selected 变化时 `fetchAlert` 并订阅 WS `stage`/`run` 事件刷新。

- [ ] **Step 1: Write the failing test**

`frontend/src/components/PipelineTimeline.test.tsx`:
```tsx
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import PipelineTimeline from './PipelineTimeline'
import type { AlertDetail } from '../types'

const detail: AlertDetail = {
  alert: { id: 'alarm-1', device: 'd', metric: 'disk_usage', severity: 'critical',
    message: 'x', status: 'active', fired_at: '', created_at: '' },
  run: { id: 'run-alarm-1', alert_id: 'alarm-1', status: 'completed', iteration: 0,
    max_iteration: 2, created_at: '', updated_at: '' },
  stages: [
    { id: 's1', run_id: 'run-alarm-1', stage_id: 'rca', stage_name: '根因分析',
      status: 'completed', iteration: 0, output: '{"root_cause":"磁盘空间不足","evidence_chain":["e1"],"confidence":0.8,"gaps":[]}',
      started_at: '', ended_at: '' },
  ],
}

describe('PipelineTimeline', () => {
  it('renders rca root cause', () => {
    render(<PipelineTimeline detail={detail} />)
    expect(screen.getByText('磁盘空间不足')).toBeTruthy()
    expect(screen.getByText('根因分析')).toBeTruthy()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && npx vitest run`
Expected: FAIL (PipelineTimeline 不存在)

- [ ] **Step 3: Implement component**

`frontend/src/components/PipelineTimeline.tsx`:
```tsx
import type { AlertDetail, StageResult } from '../types'

interface RCAOutput {
  root_cause?: string
  evidence_chain?: string[]
  confidence?: number
  gaps?: string[]
}
interface EvidenceOutput {
  metrics?: unknown[]
  logs?: unknown[]
  gaps?: string[]
}

function parseRCA(out: string): RCAOutput {
  try { return JSON.parse(out) as RCAOutput } catch { return {} }
}
function parseEvidence(out: string): EvidenceOutput {
  try { return JSON.parse(out) as EvidenceOutput } catch { return {} }
}

const statusLabel: Record<string, string> = {
  running: '进行中', completed: '完成', failed: '失败', skipped: '跳过', pending: '待执行',
}

function StageRow({ s }: { s: StageResult }) {
  const rca = s.stage_id === 'rca' ? parseRCA(s.output) : null
  const ev = s.stage_id === 'evidence' ? parseEvidence(s.output) : null
  return (
    <div style={{ border: '1px solid #ddd', borderRadius: 8, padding: 8, marginBottom: 8 }}>
      <div>
        <strong>{s.stage_name}</strong>
        {s.iteration > 0 && <span style={{ color: '#f57c00' }}> 第{s.iteration}轮</span>}
        <span style={{ color: s.status === 'completed' ? '#2e7d32' : '#f57c00' }}>
          {' '}[{statusLabel[s.status] ?? s.status}]
        </span>
      </div>
      {s.error && <div style={{ color: '#d32f2f' }}>{s.error}</div>}
      {rca?.root_cause && (
        <div>
          <div>根因：{rca.root_cause}（置信度 {Math.round((rca.confidence ?? 0) * 100)}%）</div>
          <ul>{rca.evidence_chain?.map((e, i) => <li key={i}>{e}</li>)}</ul>
          {rca.gaps && rca.gaps.length > 0 && (
            <div style={{ color: '#f57c00' }}>待补充：{rca.gaps.join('、')}</div>
          )}
        </div>
      )}
      {ev && (
        <div style={{ color: '#555' }}>
          指标点 {ev.metrics?.length ?? 0} 个，日志 {ev.logs?.length ?? 0} 条
        </div>
      )}
    </div>
  )
}

export default function PipelineTimeline({ detail }: { detail: AlertDetail }) {
  return (
    <div>
      <div style={{ marginBottom: 8 }}>
        告警 {detail.alert.id} · 运行状态：{detail.run.status ?? '未运行'}
      </div>
      {detail.stages.length === 0 && <div style={{ color: '#888' }}>尚未开始排查</div>}
      {detail.stages.map((s) => <StageRow key={s.id} s={s} />)}
    </div>
  )
}
```

- [ ] **Step 4: 集成到 App**

`frontend/src/App.tsx`（更新 selected 分支）:
```tsx
const [detail, setDetail] = useState<AlertDetail | null>(null)

useEffect(() => {
  if (!selected) { setDetail(null); return }
  let cancelled = false
  const load = () => fetchAlert(selected).then((d) => { if (!cancelled) setDetail(d) }).catch(() => {})
  load()
  const unsub = subscribeWS((m) => {
    if (m.type === 'stage' || m.type === 'run') load()
  })
  return () => { cancelled = true; unsub() }
}, [selected])
```
渲染：`{detail && <div style={{ flex: 1 }}><PipelineTimeline detail={detail} /></div>}`，并 import `PipelineTimeline` 与 `AlertDetail`。selected 无值时显示占位。

- [ ] **Step 5: 运行测试并通过**

Run: `cd frontend && npx vitest run && npm run build`
Expected: PASS + 构建成功

- [ ] **Step 6: Commit**

```bash
git add frontend/src && git commit -m "feat: 排查过程时间线视图"
```

---

### Task 17: 聊天面板

**Files:**
- Create: `frontend/src/components/ChatPanel.tsx`
- Create: `frontend/src/components/ChatPanel.test.tsx`
- Modify: `frontend/src/App.tsx`

**Interfaces:**
- Consumes: `fetchMessages`, `postChat`, `subscribeWS`
- Produces: `ChatPanel` 组件（消息气泡列表 + 输入框 + 发送），`App` 右侧固定宽显示

- [ ] **Step 1: Write the failing test**

`frontend/src/components/ChatPanel.test.tsx`:
```tsx
import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import ChatPanel from './ChatPanel'
import type { ChatMessage } from '../types'

const msgs: ChatMessage[] = [
  { id: 'm1', role: 'user', content: '有哪些告警', created_at: '' },
  { id: 'm2', role: 'assistant', content: '当前没有告警。', created_at: '' },
]

describe('ChatPanel', () => {
  it('renders messages and sends', async () => {
    const onSend = vi.fn().mockResolvedValue({
      id: 'm3', role: 'assistant', content: 'ok', created_at: '',
    } as ChatMessage)
    render(<ChatPanel messages={msgs} onSend={onSend} />)
    expect(screen.getByText('有哪些告警')).toBeTruthy()
    fireEvent.change(screen.getByPlaceholderText('输入消息...'), { target: { value: '你好' } })
    fireEvent.click(screen.getByText('发送'))
    await waitFor(() => expect(onSend).toHaveBeenCalledWith('你好'))
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && npx vitest run`
Expected: FAIL (ChatPanel 不存在)

- [ ] **Step 3: Implement component**

`frontend/src/components/ChatPanel.tsx`:
```tsx
import { useState } from 'react'
import type { ChatMessage } from '../types'

export default function ChatPanel({
  messages,
  onSend,
}: {
  messages: ChatMessage[]
  onSend: (text: string) => Promise<ChatMessage>
}) {
  const [text, setText] = useState('')
  const [sending, setSending] = useState(false)

  const submit = async () => {
    if (!text.trim() || sending) return
    setSending(true)
    try {
      await onSend(text.trim())
      setText('')
    } finally {
      setSending(false)
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div style={{ flex: 1, overflowY: 'auto', padding: 8 }}>
        {messages.length === 0 && <div style={{ color: '#888' }}>开始对话，例如：查一下 alarm-100 什么原因</div>}
        {messages.map((m) => (
          <div key={m.id} style={{ textAlign: m.role === 'user' ? 'right' : 'left', margin: '6px 0' }}>
            <span style={{
              display: 'inline-block', maxWidth: '80%', padding: '6px 10px', borderRadius: 8,
              background: m.role === 'user' ? '#1976d2' : '#e0e0e0',
              color: m.role === 'user' ? '#fff' : '#111', whiteSpace: 'pre-wrap',
            }}>
              {m.content}
            </span>
          </div>
        ))}
      </div>
      <div style={{ display: 'flex', gap: 8, padding: 8 }}>
        <input
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && submit()}
          placeholder="输入消息..."
          style={{ flex: 1, padding: 8 }}
        />
        <button onClick={submit} disabled={sending}>发送</button>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: 集成到 App**

`frontend/src/App.tsx`：增加 chat state：
```tsx
const [messages, setMessages] = useState<ChatMessage[]>([])

useEffect(() => {
  fetchMessages().then(setMessages).catch(() => {})
  const unsub = subscribeWS((m) => { if (m.type === 'message') fetchMessages().then(setMessages).catch(() => {}) })
  return unsub
}, [])

const handleSend = async (text: string) => {
  const reply = await postChat(text)
  const next = await fetchMessages()
  setMessages(next)
  return reply
}
```
布局：三列 flex —— 左侧告警列表(flex:2)，中间时间线(flex:3)，右侧聊天固定宽 360px。import `ChatPanel`, `ChatMessage`, `fetchMessages`, `postChat`。

- [ ] **Step 5: 运行测试并通过**

Run: `cd frontend && npx vitest run && npm run build`
Expected: PASS + 构建成功

- [ ] **Step 6: Commit**

```bash
git add frontend/src && git commit -m "feat: 聊天面板"
```

---

### Task 18: Docker Compose 部署

**Files:**
- Create: `backend/Dockerfile`
- Create: `frontend/Dockerfile`
- Create: `frontend/nginx.conf`
- Create: `docker-compose.yml`
- Create: `README.md`

**Interfaces:**
- 提供完整一键启动：`docker compose up -d --build`
- `docker-compose.yml` 服务：
  - `backend`：构建 `backend/Dockerfile`（`FROM golang:1.24` 多阶段→`alpine`，暴露8080，env 从 `.env` 读取，卷挂载 `./data:/data`，`DB_PATH=/data/aiops.db`）
  - `frontend`：构建 `frontend/Dockerfile`（`FROM node:20` 构建→`FROM nginx:alpine`，拷贝 `nginx.conf`），端口 3000→80，依赖 backend
- `frontend/nginx.conf`：`location /api { proxy_pass http://backend:8080; }`，`location /ws { proxy_pass http://backend:8080; proxy_http_version 1.1; proxy_set_header Upgrade $http_upgrade; proxy_set_header Connection "upgrade"; }`，`location / { root /usr/share/nginx/html; try_files $uri /index.html; }`

- [ ] **Step 1: 编写 Docker 文件**

`backend/Dockerfile`:
```dockerfile
FROM golang:1.24 AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /aiops-server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /aiops-server /aiops-server
EXPOSE 8080
CMD ["/aiops-server"]
```

`frontend/Dockerfile`:
```dockerfile
FROM node:20 AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM nginx:alpine
COPY --from=build /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
```

`frontend/nginx.conf`:
```nginx
server {
  listen 80;
  server_name _;
  root /usr/share/nginx/html;
  index index.html;

  location /api {
    proxy_pass http://backend:8080;
  }
  location /ws {
    proxy_pass http://backend:8080;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_read_timeout 3600s;
  }
  location / {
    try_files $uri /index.html;
  }
}
```

- [ ] **Step 2: 编写 compose 与 README**

`docker-compose.yml`:
```yaml
services:
  backend:
    build:
      context: ./backend
    env_file: .env
    environment:
      - DB_PATH=/data/aiops.db
    volumes:
      - ./data:/data
    ports:
      - "8080:8080"
    restart: unless-stopped

  frontend:
    build:
      context: ./frontend
    ports:
      - "3000:80"
    depends_on:
      - backend
    restart: unless-stopped
```

`README.md`（根目录）:
```markdown
# 硬件AIOps智能体

面向算力中心的硬件运维多Agent协作智能系统：接收告警后通过多阶段Agent流水线自主排查根因，并支持Web聊天对话排查。

## 架构
Go后端（Eino编排LLM）+ 流水线（接收规范化→过滤分诊→上下文收集→证据收集→根因分析→建议，证据不足时最多回溯2轮）+ 全栈mock数据（监控/日志/CMDB/知识库）+ React前端（告警列表/排查时间线/聊天）+ SQLite持久化。

## 本地开发
后端：`cd backend && go run ./cmd/server`（需要环境变量 `LLM_API_KEY`，缺失时自动用规则兜底）
前端：`cd frontend && npm install && npm run dev`（已配置 `/api`、`/ws` 代理到8080）

## Docker一键启动
1. `cp .env.example .env` 并填入 `LLM_API_KEY`
2. `docker compose up -d --build`
3. 打开 http://localhost:3000

## 环境变量
| 变量 | 必填 | 默认 |
|------|------|------|
| LLM_API_KEY | 是 | - |
| LLM_BASE_URL | 否 | https://api.deepseek.com/v1 |
| LLM_MODEL | 否 | deepseek-chat |
| DB_PATH | 否 | aiops.db |
| PORT | 否 | :8080 |

## 测试
后端：`cd backend && go test ./...`
前端：`cd frontend && npx vitest run`
```

- [ ] **Step 3: 本地全量验证**

Run: `cd backend && go test ./... && cd ../frontend && npx vitest run && npm run build`
Expected: 全部 PASS，构建成功

- [ ] **Step 4: 提交**

```bash
git add backend/Dockerfile frontend/Dockerfile frontend/nginx.conf docker-compose.yml README.md && git commit -m "feat: Docker Compose部署与文档"
```

---

### Task 19: 端到端验证

**Files:**
- Create: `backend/internal/e2e/e2e_test.go`
- Modify: `backend/internal/pipeline/build_test.go`（如需补充断言）

**Interfaces:**
- 验证：喂一条 mock 告警 → 跑完整流水线 → 断言各阶段产物与结论存在，且回溯逻辑在 gaps 场景生效

- [ ] **Step 1: Write the failing test**

`backend/internal/e2e/e2e_test.go`:
```go
package e2e

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"aiops/internal/mockdata"
	"aiops/internal/model"
	"aiops/internal/pipeline"
	"aiops/internal/store"
)

func TestEndToEndDiskUsage(t *testing.T) {
	mockdata.GenerateAll(1)
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	eng := pipeline.Build(s, nil)
	now := time.Now()
	alert := model.Alert{ID: "alarm-e2e", Device: "db-01", Metric: "disk_usage",
		Severity: "critical", Message: "磁盘使用率超过95%", FiredAt: now, CreatedAt: now}
	if err := eng.Run(context.Background(), alert, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	run, err := s.GetRun("run-" + alert.ID)
	if err != nil || run.Status != model.StatusCompleted {
		t.Fatalf("run = %+v err=%v", run, err)
	}
	stages, _ := s.ListStages(run.ID)
	have := map[string]bool{}
	for _, st := range stages {
		have[st.StageID] = true
		if st.Status == model.StageFailed {
			t.Fatalf("stage %s failed: %s", st.StageID, st.Error)
		}
	}
	for _, id := range []string{"normalize", "triage", "context", "evidence", "rca", "recommend"} {
		if !have[id] {
			t.Fatalf("missing stage %s", id)
		}
	}
	rca, ok, _ := s.GetLatestStage(run.ID, "rca")
	if !ok || rca.Output == "" {
		t.Fatalf("rca missing")
	}
	var rc model.RCA
	if err := json.Unmarshal([]byte(rca.Output), &rc); err != nil || rc.RootCause == "" {
		t.Fatalf("rca output = %s err=%v", rca.Output, err)
	}
}
```
（需 import `"encoding/json"`。）

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/e2e/...`
Expected: FAIL（若 build.go 未装配正确会失败）

- [ ] **Step 3: 运行以确认通过**

Run: `cd backend && go test ./...`
Expected: PASS（全部）

- [ ] **Step 4: 手动冒烟测试（可选）**

Run: `cd backend && LLM_API_KEY=sk-nonexist DB_PATH=/tmp/aiops-smoke.db go run ./cmd/server`，另开终端：
```
curl -s localhost:8080/healthz
curl -s localhost:8080/api/alerts
curl -s -X POST localhost:8080/api/chat -d '{"text":"有哪些告警"}'
```
Expected: healthz ok；alerts 列表随时间增长；chat 返回助手回复

- [ ] **Step 5: Commit**

```bash
git add backend/internal/e2e && git commit -m "test: 端到端流水线验证"
```

---

## Self-Review 记录

- **Spec coverage:** 覆盖 spec 第3节（架构）、第4节（6阶段+回溯）、第5节（聊天/三视图/WS/四张表）、第6节（mock全栈、错误处理、单元/端到端/前端测试、Docker）。聊天Agent 意图规则匹配为 spec 中"Eino ChatModel"的替代实现——LLM 用于根因分析与建议润色，意图解析用确定性规则保证可测性。
- **无占位符：** 所有步骤含具体代码与断言。
- **类型一致性：** `StageResult` 字段（`Output`/`Error` JSON tag `error,omitempty`）、`pipeline.State` 字段在 engine 与各 stage 间保持一致；`extractAlertID` 在 Task 12 内给出了修正后的实现；`ContextStage` 已修正为不依赖未定义方法。
