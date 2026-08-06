# 跨Agent三维度数据隔离 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为硬件AIOps智能体实现任务级/设备级/全局级三维度嵌套数据隔离（写隔离+读向上），解决跨 Agent 数据协作的可见性作用域与并发协调问题。

**Architecture:** 新增 `scope` 包定义层级与作用域；store 表加 `scope` 列并新增 `device_context` 表；新增 `store.ScopedStore` 视图做写校验与读过滤；新增 `coordinator` 包做设备级串行锁；`pipeline.Engine` 按告警设备取锁、注入任务级 ScopedStore 到 `State`，各阶段经 `st.Store` 访问数据；`build.go` 装配 coordinator，`main.go` 接线。

**Tech Stack:** Go 1.24 + modernc.org/sqlite（已有）+ 现有 aiops/internal 包结构

## Global Constraints

- Go `go.mod` 中 `go 1.24`（module aiops），测试用标准库 `testing`，不添加计划外的注释
- 三维度嵌套：全局(⊃)设备(⊃)任务；权限矩阵 = **写隔离 + 读向上**（任务可读宿主设备+全局只读；设备可读全局；全局读全部）
- Scope 编码：`global` / `device:<name>` / `task:<id>`；越权返回 `scope.ErrScopeDenied`
- 表加作用域字段：`alerts.scope`（`device:<device>`）、`pipeline_runs.scope`（`task:<id>`）、`stage_results.scope`（`task:<run_id>`）；新增 `device_context` 表（device_id 主键、state_json、updated_by、updated_at）
- `store.ScopedStore(sc scope.Scope)`：读自动过滤、写自动校验；底层 `Store` 的既有方法签名不变（scope 由行数据自动推导写入），仅新增方法
- `coordinator.Coordinator`：按设备串行，`Acquire(device) func()` 阻塞，`TryAcquire(device, timeout) (func(), bool)`；引擎锁超时 30s
- `pipeline.Build` 签名变为 `Build(s *store.Store, lc *llm.Client, coord *coordinator.Coordinator) *Engine`（更新所有调用方：main.go、build_test.go、e2e_test.go）
- `State` 新增字段：`Scope scope.Scope`、`Store *store.ScopedStore`、`DeviceContext *model.DeviceContext`

---

### Task 1: scope 包

**Files:**
- Create: `backend/internal/scope/scope.go`
- Test: `backend/internal/scope/scope_test.go`

**Interfaces:**
- Produces: `scope.Level`（`LevelGlobal`=iota, `LevelDevice`, `LevelTask`）、`scope.Scope{Level, TaskID, DeviceID}`、`scope.Global() Scope`、`scope.Device(name string) Scope`、`scope.Task(runID, device string) Scope`、`(*Scope).Encode() string`、`scope.Parse(s string) (Scope, error)`、`scope.ErrScopeDenied error`

- [ ] **Step 1: Write the failing test**

`backend/internal/scope/scope_test.go`:
```go
package scope

import (
	"errors"
	"testing"
)

func TestEncode(t *testing.T) {
	cases := []struct {
		sc   Scope
		want string
	}{
		{Global(), "global"},
		{Device("compute-01"), "device:compute-01"},
		{Task("run-alarm-1", "compute-01"), "task:run-alarm-1"},
	}
	for _, c := range cases {
		if got := c.sc.Encode(); got != c.want {
			t.Fatalf("Encode(%+v) = %q, want %q", c.sc, got, c.want)
		}
	}
}

func TestParseRoundTrip(t *testing.T) {
	for _, s := range []string{"global", "device:compute-01", "task:run-alarm-1"} {
		sc, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		if sc.Encode() != s {
			t.Fatalf("roundtrip %q -> %q", s, sc.Encode())
		}
	}
}

func TestParseUnknown(t *testing.T) {
	if _, err := Parse("tenant:foo"); err == nil {
		t.Fatalf("expected error for unknown scope")
	}
}

func TestErrScopeDeniedIsComparable(t *testing.T) {
	if !errors.Is(ErrScopeDenied, ErrScopeDenied) {
		t.Fatalf("ErrScopeDenied must be sentinel")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/scope/...`
Expected: FAIL（scope package 不存在）

- [ ] **Step 3: Write minimal implementation**

`backend/internal/scope/scope.go`:
```go
package scope

import (
	"errors"
	"fmt"
	"strings"
)

var ErrScopeDenied = errors.New("scope denied")

type Level int

const (
	LevelGlobal Level = iota
	LevelDevice
	LevelTask
)

type Scope struct {
	Level    Level
	TaskID   string
	DeviceID string
}

func Global() Scope { return Scope{Level: LevelGlobal} }

func Device(name string) Scope { return Scope{Level: LevelDevice, DeviceID: name} }

func Task(runID, device string) Scope {
	return Scope{Level: LevelTask, TaskID: runID, DeviceID: device}
}

func (sc Scope) Encode() string {
	switch sc.Level {
	case LevelDevice:
		return "device:" + sc.DeviceID
	case LevelTask:
		return "task:" + sc.TaskID
	default:
		return "global"
	}
}

func Parse(s string) (Scope, error) {
	switch {
	case s == "" || s == "global":
		return Global(), nil
	case strings.HasPrefix(s, "device:"):
		return Device(strings.TrimPrefix(s, "device:")), nil
	case strings.HasPrefix(s, "task:"):
		return Task(strings.TrimPrefix(s, "task:"), ""), nil
	}
	return Scope{}, fmt.Errorf("unknown scope %q", s)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/scope/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/scope && git commit -m "feat: scope层级定义与编解码"
```

---

### Task 2: store 表改造与 device_context

**Files:**
- Modify: `backend/internal/model/pipeline.go`（新增 `DeviceContext` 类型）
- Modify: `backend/internal/store/store.go`（migrate 加列与建表、SaveAlert/SaveRun/SaveStage 写 scope、新增 DeviceContext/ListAlertsByDevice 方法）
- Test: `backend/internal/store/store_test.go`

**Interfaces:**
- Consumes: `scope.Parse`（仅用于迁移兼容判断，可选）、`model.DeviceContext`
- Produces: `model.DeviceContext{DeviceID, StateJSON, UpdatedBy string; UpdatedAt time.Time}`；`store.Store.SaveAlert/SaveRun/SaveStage` 行为扩展为写入 `scope` 列（签名不变）；新方法 `ListAlertsByDevice(device string) ([]model.Alert, error)`、`SaveDeviceContext(dc model.DeviceContext) error`（UPSERT）、`GetDeviceContext(deviceID string) (model.DeviceContext, error)`

- [ ] **Step 1: Write the failing test**

在 `backend/internal/store/store_test.go` 追加：
```go
func TestAlertScopeColumnWritten(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	a := model.Alert{ID: "alarm-s", Device: "compute-01", Metric: "cpu_usage",
		Severity: "warning", Status: "active", FiredAt: now, CreatedAt: now}
	if err := s.SaveAlert(a); err != nil {
		t.Fatalf("SaveAlert: %v", err)
	}
	var scopeVal string
	if err := s.db.QueryRow(`SELECT scope FROM alerts WHERE id = ?`, a.ID).Scan(&scopeVal); err != nil {
		t.Fatalf("query scope: %v", err)
	}
	if scopeVal != "device:compute-01" {
		t.Fatalf("scope = %q, want device:compute-01", scopeVal)
	}
}

func TestRunAndStageScopeColumns(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	r := model.PipelineRun{ID: "run-s", AlertID: "alarm-s", Status: model.StatusRunning,
		Iteration: 0, MaxIteration: 2, CreatedAt: now, UpdatedAt: now}
	if err := s.SaveRun(r); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	st := model.StageResult{ID: "st-s", RunID: "run-s", StageID: "rca", StageName: "根因分析",
		Status: model.StageCompleted, Output: "{}", StartedAt: now, EndedAt: now}
	if err := s.SaveStage(st); err != nil {
		t.Fatalf("SaveStage: %v", err)
	}
	var runScope, stageScope string
	if err := s.db.QueryRow(`SELECT scope FROM pipeline_runs WHERE id = ?`, r.ID).Scan(&runScope); err != nil {
		t.Fatalf("run scope: %v", err)
	}
	if err := s.db.QueryRow(`SELECT scope FROM stage_results WHERE id = ?`, st.ID).Scan(&stageScope); err != nil {
		t.Fatalf("stage scope: %v", err)
	}
	if runScope != "task:run-s" || stageScope != "task:run-s" {
		t.Fatalf("run=%q stage=%q, want task:run-s", runScope, stageScope)
	}
}

func TestDeviceContextRoundTrip(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	dc := model.DeviceContext{DeviceID: "compute-01", StateJSON: `{"cpu":85}`,
		UpdatedBy: "run-1", UpdatedAt: now}
	if err := s.SaveDeviceContext(dc); err != nil {
		t.Fatalf("SaveDeviceContext: %v", err)
	}
	got, err := s.GetDeviceContext("compute-01")
	if err != nil {
		t.Fatalf("GetDeviceContext: %v", err)
	}
	if got.DeviceID != "compute-01" || got.StateJSON != `{"cpu":85}` || got.UpdatedBy != "run-1" {
		t.Fatalf("roundtrip = %+v", got)
	}
	// UPSERT updates state
	dc2 := model.DeviceContext{DeviceID: "compute-01", StateJSON: `{"cpu":95}`, UpdatedBy: "run-2", UpdatedAt: now}
	if err := s.SaveDeviceContext(dc2); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, _ = s.GetDeviceContext("compute-01")
	if got.StateJSON != `{"cpu":95}` {
		t.Fatalf("upsert state = %q", got.StateJSON)
	}
}

func TestListAlertsByDevice(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	_ = s.SaveAlert(model.Alert{ID: "alarm-1", Device: "compute-01", Metric: "cpu_usage", Status: "active", FiredAt: now, CreatedAt: now})
	_ = s.SaveAlert(model.Alert{ID: "alarm-2", Device: "db-01", Metric: "disk_usage", Status: "active", FiredAt: now, CreatedAt: now})
	list, err := s.ListAlertsByDevice("compute-01")
	if err != nil || len(list) != 1 || list[0].ID != "alarm-1" {
		t.Fatalf("ListAlertsByDevice: %v %+v", err, list)
	}
	all, _ := s.ListAlerts()
	if len(all) != 2 {
		t.Fatalf("ListAlerts len = %d, want 2", len(all))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/store/... ./internal/model/...`
Expected: FAIL（`DeviceContext` 类型、scope 列、新方法不存在）

- [ ] **Step 3: Write implementation**

`backend/internal/model/pipeline.go` 追加：
```go
type DeviceContext struct {
	DeviceID  string    `json:"device_id"`
	StateJSON string    `json:"state_json"`
	UpdatedBy string    `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}
```

`backend/internal/store/store.go`：
- 在 `migrate()` 中，在现有建表语句后追加列迁移与建表。SQLite 不支持 `ADD COLUMN IF NOT EXISTS`，用 `ensureColumn` 辅助：
```go
func ensureColumn(db *sql.DB, table, column, ddl string) error {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", table, ddl))
	return err
}
```
- `migrate()` 追加（放在现有 `CREATE TABLE` 之后）：
```go
if err := ensureColumn(s.db, "alerts", "scope", "scope TEXT"); err != nil {
	return err
}
if err := ensureColumn(s.db, "pipeline_runs", "scope", "scope TEXT"); err != nil {
	return err
}
if err := ensureColumn(s.db, "stage_results", "scope", "scope TEXT"); err != nil {
	return err
}
if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS device_context (
	device_id TEXT PRIMARY KEY,
	state_json TEXT NOT NULL DEFAULT '{}',
	updated_by TEXT,
	updated_at DATETIME)`); err != nil {
	return err
}
```
- `migrate()` 需要新增 `"fmt"` import。
- `SaveAlert` 改 SQL（签名不变，scope 由 device 推导）：
```go
func (s *Store) SaveAlert(a model.Alert) error {
	scopeVal := "device:" + a.Device
	_, err := s.db.Exec(`INSERT INTO alerts (id, device, metric, severity, message, status, fired_at, created_at, scope)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		a.ID, a.Device, a.Metric, a.Severity, a.Message, a.Status, a.FiredAt, a.CreatedAt, scopeVal)
	return err
}
```
- `SaveRun` 改 SQL：新增 `scope` 列，值 `"task:" + r.ID`。
- `SaveStage` 改 SQL：新增 `scope` 列，值 `"task:" + st.RunID`；`ON CONFLICT(id) DO UPDATE` 分支也更新 `scope=excluded.scope`。
- 新增方法：
```go
func (s *Store) ListAlertsByDevice(device string) ([]model.Alert, error) {
	rows, err := s.db.Query(`SELECT id, device, metric, severity, message, status, fired_at, created_at
		FROM alerts WHERE device = ? ORDER BY created_at DESC`, device)
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

func (s *Store) SaveDeviceContext(dc model.DeviceContext) error {
	_, err := s.db.Exec(`INSERT INTO device_context (device_id, state_json, updated_by, updated_at)
		VALUES (?,?,?,?)
		ON CONFLICT(device_id) DO UPDATE SET
			state_json=excluded.state_json, updated_by=excluded.updated_by, updated_at=excluded.updated_at`,
		dc.DeviceID, dc.StateJSON, dc.UpdatedBy, dc.UpdatedAt)
	return err
}

func (s *Store) GetDeviceContext(deviceID string) (model.DeviceContext, error) {
	var dc model.DeviceContext
	err := s.db.QueryRow(`SELECT device_id, state_json, updated_by, updated_at
		FROM device_context WHERE device_id = ?`, deviceID).
		Scan(&dc.DeviceID, &dc.StateJSON, &dc.UpdatedBy, &dc.UpdatedAt)
	return dc, err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/store/... ./internal/model/...`
Expected: PASS（含既有 store 测试）

- [ ] **Step 5: Commit**

```bash
git add backend/internal/model/pipeline.go backend/internal/store/store.go backend/internal/store/store_test.go && git commit -m "feat: store表加作用域列并新增device_context"
```

---

### Task 3: store.ScopedStore 隔离视图

**Files:**
- Create: `backend/internal/store/scoped.go`
- Test: `backend/internal/store/scoped_test.go`

**Interfaces:**
- Consumes: `scope.Scope`（`Global/Device/Task/ErrScopeDenied`）、`Store` 既有方法与新增方法
- Produces: `store.NewScoped(st *Store, sc scope.Scope) *ScopedStore`；方法 `SaveStage(s model.StageResult) error`、`SaveRun(r model.PipelineRun) error`、`UpdateRunStatus(runID, status string) error`、`ListStages(runID string) ([]model.StageResult, error)`、`GetLatestStage(runID, stageID string) (model.StageResult, bool, error)`、`ListAlerts() ([]model.Alert, error)`、`GetAlert(id string) (model.Alert, error)`、`GetRun(runID string) (model.PipelineRun, error)`、`SaveAlert(a model.Alert) error`、`GetDeviceContext(device string) (model.DeviceContext, error)`、`UpdateDeviceContext(dc model.DeviceContext) error`

权限语义（写隔离 + 读向上）：
- `SaveStage`/`SaveRun`/`UpdateRunStatus`：仅 `LevelTask` 且 ID 匹配，否则 `ErrScopeDenied`
- `ListStages`/`GetLatestStage`：`LevelTask` 时 runID 必须等于 sc.TaskID，否则 `ErrScopeDenied`
- `ListAlerts`：`LevelGlobal`→全部；否则按 sc.DeviceID 过滤（`ListAlertsByDevice`）
- `GetAlert`：`LevelGlobal`→任意；否则目标告警 device 必须等于 sc.DeviceID
- `GetRun`：`LevelGlobal`→任意；`LevelTask` 且 runID==sc.TaskID→允许；否则查该 run 的告警，告警 device 必须等于 sc.DeviceID（读向上到设备层）
- `SaveAlert`：`LevelGlobal`→允许；否则 a.Device 必须等于 sc.DeviceID
- `GetDeviceContext`/`UpdateDeviceContext`：`LevelGlobal`→任意；否则 device 必须等于 sc.DeviceID

- [ ] **Step 1: Write the failing test**

`backend/internal/store/scoped_test.go`:
```go
package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"aiops/internal/model"
	"aiops/internal/scope"
)

func TestScopedTaskWriteOwnRunAllowed(t *testing.T) {
	s := newTestStore(t)
	sc := scope.Task("run-1", "compute-01")
	ss := NewScoped(s, sc)
	now := time.Now()
	st := model.StageResult{ID: "st-1", RunID: "run-1", StageID: "rca", StageName: "根因分析",
		Status: model.StageCompleted, Output: "{}", StartedAt: now, EndedAt: now}
	if err := ss.SaveStage(st); err != nil {
		t.Fatalf("SaveStage own run: %v", err)
	}
}

func TestScopedTaskWriteOtherRunDenied(t *testing.T) {
	s := newTestStore(t)
	sc := scope.Task("run-1", "compute-01")
	ss := NewScoped(s, sc)
	now := time.Now()
	st := model.StageResult{ID: "st-2", RunID: "run-999", StageID: "rca", StageName: "根因分析",
		Status: model.StageCompleted, Output: "{}", StartedAt: now, EndedAt: now}
	if err := ss.SaveStage(st); !errors.Is(err, scope.ErrScopeDenied) {
		t.Fatalf("SaveStage other run err = %v, want ErrScopeDenied", err)
	}
}

func TestScopedTaskListStagesOtherRunDenied(t *testing.T) {
	s := newTestStore(t)
	ss := NewScoped(s, scope.Task("run-1", "compute-01"))
	if _, err := ss.ListStages("run-999"); !errors.Is(err, scope.ErrScopeDenied) {
		t.Fatalf("ListStages other run err = %v, want ErrScopeDenied", err)
	}
}

func TestScopedListAlertsDeviceFilter(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	_ = s.SaveAlert(model.Alert{ID: "alarm-1", Device: "compute-01", Metric: "cpu_usage", Status: "active", FiredAt: now, CreatedAt: now})
	_ = s.SaveAlert(model.Alert{ID: "alarm-2", Device: "db-01", Metric: "disk_usage", Status: "active", FiredAt: now, CreatedAt: now})

	taskSS := NewScoped(s, scope.Task("run-1", "compute-01"))
	list, err := taskSS.ListAlerts()
	if err != nil || len(list) != 1 || list[0].ID != "alarm-1" {
		t.Fatalf("task ListAlerts = %+v err=%v, want only compute-01", list, err)
	}

	globalSS := NewScoped(s, scope.Global())
	list, err = globalSS.ListAlerts()
	if err != nil || len(list) != 2 {
		t.Fatalf("global ListAlerts len = %d, want 2", len(list))
	}
}

func TestScopedGetAlertOtherDeviceDenied(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	_ = s.SaveAlert(model.Alert{ID: "alarm-1", Device: "compute-01", Metric: "cpu_usage", Status: "active", FiredAt: now, CreatedAt: now})
	ss := NewScoped(s, scope.Task("run-1", "db-01"))
	if _, err := ss.GetAlert("alarm-1"); !errors.Is(err, scope.ErrScopeDenied) {
		t.Fatalf("GetAlert other device err = %v, want ErrScopeDenied", err)
	}
}

func TestScopedGetRunReadUpward(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	_ = s.SaveAlert(model.Alert{ID: "alarm-1", Device: "compute-01", Metric: "cpu_usage", Status: "active", FiredAt: now, CreatedAt: now})
	_ = s.SaveRun(model.PipelineRun{ID: "run-1", AlertID: "alarm-1", Status: model.StatusCompleted,
		Iteration: 0, MaxIteration: 2, CreatedAt: now, UpdatedAt: now})
	// 同设备任务可读 run（读向上）
	ss := NewScoped(s, scope.Task("run-2", "compute-01"))
	if _, err := ss.GetRun("run-1"); err != nil {
		t.Fatalf("GetRun read-upward same device err = %v", err)
	}
	// 他设备任务读 run 被拒
	other := NewScoped(s, scope.Task("run-3", "db-01"))
	if _, err := other.GetRun("run-1"); !errors.Is(err, scope.ErrScopeDenied) {
		t.Fatalf("GetRun other device err = %v, want ErrScopeDenied", err)
	}
}

func TestScopedDeviceContextPermission(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	_ = s.SaveDeviceContext(model.DeviceContext{DeviceID: "compute-01", StateJSON: `{}`, UpdatedBy: "x", UpdatedAt: now})

	ok := NewScoped(s, scope.Task("run-1", "compute-01"))
	if _, err := ok.GetDeviceContext("compute-01"); err != nil {
		t.Fatalf("GetDeviceContext own device err = %v", err)
	}
	denied := NewScoped(s, scope.Task("run-1", "db-01"))
	if _, err := denied.GetDeviceContext("compute-01"); !errors.Is(err, scope.ErrScopeDenied) {
		t.Fatalf("GetDeviceContext other device err = %v, want ErrScopeDenied", err)
	}
	if err := denied.UpdateDeviceContext(model.DeviceContext{DeviceID: "compute-01", StateJSON: `{}`, UpdatedBy: "r", UpdatedAt: now}); !errors.Is(err, scope.ErrScopeDenied) {
		t.Fatalf("UpdateDeviceContext other device err = %v, want ErrScopeDenied", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/store/...`
Expected: FAIL（`NewScoped` 不存在）

- [ ] **Step 3: Write implementation**

`backend/internal/store/scoped.go`:
```go
package store

import (
	"errors"

	"aiops/internal/model"
	"aiops/internal/scope"
)

type ScopedStore struct {
	st *Store
	sc scope.Scope
}

func NewScoped(st *Store, sc scope.Scope) *ScopedStore {
	return &ScopedStore{st: st, sc: sc}
}

func (ss *ScopedStore) SaveStage(s model.StageResult) error {
	if ss.sc.Level != scope.LevelTask || s.RunID != ss.sc.TaskID {
		return scope.ErrScopeDenied
	}
	return ss.st.SaveStage(s)
}

func (ss *ScopedStore) SaveRun(r model.PipelineRun) error {
	if ss.sc.Level != scope.LevelTask || r.ID != ss.sc.TaskID {
		return scope.ErrScopeDenied
	}
	return ss.st.SaveRun(r)
}

func (ss *ScopedStore) UpdateRunStatus(runID, status string) error {
	if ss.sc.Level != scope.LevelTask || runID != ss.sc.TaskID {
		return scope.ErrScopeDenied
	}
	return ss.st.UpdateRunStatus(runID, status)
}

func (ss *ScopedStore) ListStages(runID string) ([]model.StageResult, error) {
	if ss.sc.Level == scope.LevelTask && runID != ss.sc.TaskID {
		return nil, scope.ErrScopeDenied
	}
	return ss.st.ListStages(runID)
}

func (ss *ScopedStore) GetLatestStage(runID, stageID string) (model.StageResult, bool, error) {
	if ss.sc.Level == scope.LevelTask && runID != ss.sc.TaskID {
		return model.StageResult{}, false, scope.ErrScopeDenied
	}
	return ss.st.GetLatestStage(runID, stageID)
}

func (ss *ScopedStore) ListAlerts() ([]model.Alert, error) {
	if ss.sc.Level == scope.LevelGlobal {
		return ss.st.ListAlerts()
	}
	return ss.st.ListAlertsByDevice(ss.sc.DeviceID)
}

func (ss *ScopedStore) GetAlert(id string) (model.Alert, error) {
	a, err := ss.st.GetAlert(id)
	if err != nil {
		return a, err
	}
	if ss.sc.Level != scope.LevelGlobal && a.Device != ss.sc.DeviceID {
		return a, scope.ErrScopeDenied
	}
	return a, nil
}

func (ss *ScopedStore) GetRun(runID string) (model.PipelineRun, error) {
	if ss.sc.Level == scope.LevelGlobal {
		return ss.st.GetRun(runID)
	}
	if ss.sc.Level == scope.LevelTask && runID == ss.sc.TaskID {
		return ss.st.GetRun(runID)
	}
	r, err := ss.st.GetRun(runID)
	if err != nil {
		return r, err
	}
	a, err := ss.st.GetAlert(r.AlertID)
	if err != nil || a.Device != ss.sc.DeviceID {
		return r, scope.ErrScopeDenied
	}
	return r, nil
}

func (ss *ScopedStore) SaveAlert(a model.Alert) error {
	if ss.sc.Level != scope.LevelGlobal && a.Device != ss.sc.DeviceID {
		return scope.ErrScopeDenied
	}
	return ss.st.SaveAlert(a)
}

func (ss *ScopedStore) GetDeviceContext(device string) (model.DeviceContext, error) {
	if ss.sc.Level != scope.LevelGlobal && device != ss.sc.DeviceID {
		return model.DeviceContext{}, scope.ErrScopeDenied
	}
	return ss.st.GetDeviceContext(device)
}

func (ss *ScopedStore) UpdateDeviceContext(dc model.DeviceContext) error {
	if ss.sc.Level != scope.LevelGlobal && dc.DeviceID != ss.sc.DeviceID {
		return scope.ErrScopeDenied
	}
	return ss.st.SaveDeviceContext(dc)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/store/...`
Expected: PASS（含既有 store 测试 + 新 scoped 测试）

- [ ] **Step 5: Commit**

```bash
git add backend/internal/store/scoped.go backend/internal/store/scoped_test.go && git commit -m "feat: ScopedStore隔离视图"
```

---

### Task 4: coordinator 设备级串行锁

**Files:**
- Create: `backend/internal/coordinator/coordinator.go`
- Test: `backend/internal/coordinator/coordinator_test.go`

**Interfaces:**
- Produces: `coordinator.New() *Coordinator`；`(*Coordinator).Acquire(device string) func()`（阻塞获取，返回释放函数）；`(*Coordinator).TryAcquire(device string, timeout time.Duration) (func(), bool)`；实现用每设备容量为 1 的 `chan struct{}`（`Acquire` 阻塞写入、释放读取）

- [ ] **Step 1: Write the failing test**

`backend/internal/coordinator/coordinator_test.go`:
```go
package coordinator

import (
	"sync"
	"testing"
	"time"
)

func TestSameDeviceSerialized(t *testing.T) {
	c := New()
	release1 := c.Acquire("compute-01")
	var active, maxActive int
	var mu sync.Mutex
	done := make(chan struct{})

	go func() {
		release2 := c.Acquire("compute-01")
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		release2()
		close(done)
	}()

	// 让第二个任务先尝试获取（应阻塞在锁上，active 最多 1）
	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	active++
	if active > maxActive {
		maxActive = active
	}
	mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	active--
	mu.Unlock()
	release1()

	<-done
	if maxActive != 1 {
		t.Fatalf("maxActive = %d, want 1 (same device must serialize)", maxActive)
	}
}

func TestDifferentDevicesParallel(t *testing.T) {
	c := New()
	got := make(chan string, 2)
	go func() {
		rel := c.Acquire("a")
		defer rel()
		time.Sleep(30 * time.Millisecond)
		got <- "a"
	}()
	go func() {
		rel := c.Acquire("b")
		defer rel()
		got <- "b"
	}()
	if len(got) != 0 {
		t.Fatalf("expected both blocked initially")
	}
	<-got
	<-got // 两个都在约30ms后返回，不互相阻塞
}

func TestTryAcquireTimeout(t *testing.T) {
	c := New()
	rel := c.Acquire("d")
	defer rel()
	if fn, ok := c.TryAcquire("d", 20*time.Millisecond); ok {
		fn()
		t.Fatalf("TryAcquire should fail when locked")
	}
	if fn, ok := c.TryAcquire("e", 20*time.Millisecond); !ok {
		t.Fatalf("TryAcquire should succeed for unlocked device")
	} else {
		fn()
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/coordinator/...`
Expected: FAIL（coordinator package 不存在）

- [ ] **Step 3: Write minimal implementation**

`backend/internal/coordinator/coordinator.go`:
```go
package coordinator

import (
	"sync"
	"time"
)

type Coordinator struct {
	mu    sync.Mutex
	slots map[string]chan struct{}
}

func New() *Coordinator {
	return &Coordinator{slots: map[string]chan struct{}{}}
}

func (c *Coordinator) slot(device string) chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, ok := c.slots[device]
	if !ok {
		ch = make(chan struct{}, 1)
		c.slots[device] = ch
	}
	return ch
}

func (c *Coordinator) Acquire(device string) func() {
	ch := c.slot(device)
	ch <- struct{}{}
	return func() { <-ch }
}

func (c *Coordinator) TryAcquire(device string, timeout time.Duration) (func(), bool) {
	ch := c.slot(device)
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, true
	case <-time.After(timeout):
		return nil, false
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/coordinator/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/coordinator && git commit -m "feat: 设备级串行锁coordinator"
```

---

### Task 5: 引擎接入隔离与设备锁

**Files:**
- Modify: `backend/internal/pipeline/engine.go`
- Test: `backend/internal/pipeline/engine_test.go`

**Interfaces:**
- Consumes: `scope.Task`、`store.NewScoped`、`coordinator.Coordinator`
- Produces: `pipeline.Engine` 新增字段 `coord *coordinator.Coordinator`（nil-safe）与方法 `(*Engine).SetCoordinator(c *coordinator.Coordinator)`；`State` 新增字段 `Scope scope.Scope`、`Store *store.ScopedStore`、`DeviceContext *model.DeviceContext`；`Run` 在保存 run 后按 `alert.Device` 尝试 `TryAcquire(30s)`，失败则将 run 标为 `degraded` 并发布 run 事件后返回 nil；成功后 `defer release()`；内部所有 `e.store.SaveStage/SaveRun/UpdateRunStatus` 改经任务级 `st.Store`（`scope.Task(run.ID, alert.Device)`）

- [ ] **Step 1: Write the failing test**

在 `backend/internal/pipeline/engine_test.go` 追加：
```go
func TestEngineInjectsTaskScopedStore(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	stages := []Stage{
		fakeStage{id: "x", name: "X", run: func(st *State) error {
			if st.Store == nil {
				t.Fatalf("State.Store not injected")
			}
			if st.Scope == nil || st.Scope.Level != scope.LevelTask || st.Scope.TaskID != "run-"+st.Alert.ID {
				t.Fatalf("scope = %+v", st.Scope)
			}
			// 越权写其它 run 应被拒绝
			if err := st.Store.SaveStage(model.StageResult{ID: "bad", RunID: "run-other", StageID: "rca",
				StageName: "x", Status: model.StageCompleted}); !errors.Is(err, scope.ErrScopeDenied) {
				t.Fatalf("cross-run write err = %v, want ErrScopeDenied", err)
			}
			return nil
		}},
	}
	e := NewEngine(s, stages)
	alert := model.Alert{ID: "alarm-1", Device: "compute-01", Metric: "cpu_usage"}
	if err := e.Run(context.Background(), alert, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestEngineDeviceLockTimeoutDegrades(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	coord := coordinator.New()
	rel := coord.Acquire("compute-01") // 先占住设备锁
	defer rel()

	stages := []Stage{
		fakeStage{id: "x", name: "X", run: func(st *State) error { return nil }},
	}
	e := NewEngine(s, stages)
	e.SetCoordinator(coord)
	alert := model.Alert{ID: "alarm-1", Device: "compute-01", Metric: "cpu_usage"}
	if err := e.Run(context.Background(), alert, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	run, _ := s.GetRun("run-" + alert.ID)
	if run.Status != model.StatusDegraded {
		t.Fatalf("run status = %q, want degraded (device busy)", run.Status)
	}
}

func TestEngineDeviceLockSerializes(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	coord := coordinator.New()
	var enterMu sync.Mutex
	inside := map[string]bool{}
	var maxInside int
	var wg sync.WaitGroup

	mkStage := func(name string) Stage {
		return fakeStage{id: name, name: name, run: func(st *State) error {
			enterMu.Lock()
			inside[st.Alert.ID] = true
			if len(inside) > maxInside {
				maxInside = len(inside)
			}
			enterMu.Unlock()
			time.Sleep(20 * time.Millisecond)
			enterMu.Lock()
			delete(inside, st.Alert.ID)
			enterMu.Unlock()
			return nil
		}}
	}
	e := NewEngine(s, []Stage{mkStage("x")})
	e.SetCoordinator(coord)

	for _, id := range []string{"alarm-1", "alarm-2"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_ = e.Run(context.Background(), model.Alert{ID: id, Device: "compute-01", Metric: "cpu_usage"}, nil)
		}(id)
	}
	wg.Wait()
	if maxInside != 1 {
		t.Fatalf("maxInside = %d, want 1 (same device serialized)", maxInside)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/pipeline/...`
Expected: FAIL（State.Store/Scope 字段、SetCoordinator 不存在）

- [ ] **Step 3: Write implementation**

`backend/internal/pipeline/engine.go` 修改：
- 增加 import：`"errors"`、`"time"`、`"aiops/internal/coordinator"`、`"aiops/internal/scope"`。
- `State` 追加字段：
```go
	Scope          *scope.Scope  // 本任务的嵌套作用域
	Store          *store.ScopedStore
	DeviceContext  *model.DeviceContext
```
（`Scope` 用指针以兼容既有测试直接构造 `State{...}` 的用法。）

- `Engine` 追加字段与方法：
```go
type Engine struct {
	store   *store.Store
	stages  []Stage
	Publish func(eventType string, data any)
	coord   *coordinator.Coordinator
}

func (e *Engine) SetCoordinator(c *coordinator.Coordinator) { e.coord = c }
```
- `Run` 中，在 `SaveRun` 成功后：
```go
	if e.coord != nil {
		const lockTimeout = 30 * time.Second
		release, ok := e.coord.TryAcquire(alert.Device, lockTimeout)
		if !ok {
			_ = e.store.UpdateRunStatus(run.ID, model.StatusDegraded)
			run.Status = model.StatusDegraded
			if e.Publish != nil {
				e.Publish("run", run)
			}
			return nil
		}
		defer release()
	}
```
- `st` 初始化追加：
```go
	taskScope := scope.Task(run.ID, alert.Device)
	st := &State{
		Run:       run,
		Alert:     alert,
		Iteration: 0,
		RawAlerts: input,
		Scope:     &taskScope,
		Store:     store.NewScoped(e.store, taskScope),
	}
```
- 将 `Run` 内的 `e.store.SaveStage(res)`（两处）、`e.store.UpdateRunStatus(run.ID, status)` 改为 `st.Store.SaveStage(res)`、`st.Store.UpdateRunStatus(run.ID, status)`。`SaveRun` 保持走 `e.store`（run 尚未创建，任务级校验需要 run.ID==TaskID，此时 TaskID 即 run.ID，等价；保持简单用 e.store）。

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/pipeline/...`
Expected: PASS（含既有 3 个 engine 测试 + 3 个新测试 + stages 测试）

- [ ] **Step 5: Commit**

```bash
git add backend/internal/pipeline/engine.go backend/internal/pipeline/engine_test.go && git commit -m "feat: 引擎接入任务级作用域与设备锁"
```

---

### Task 6: 阶段接入隔离（triage/context）

**Files:**
- Modify: `backend/internal/pipeline/triage.go`
- Modify: `backend/internal/pipeline/context.go`
- Test: `backend/internal/pipeline/stages_test.go`

**Interfaces:**
- Consumes: `State.Store`（ScopedStore）、`scope.Global`、`model.DeviceContext`
- Produces: `TriageStage.Run` 改为从 `st.Store.ListAlerts()` 读告警、`st.Store.GetRun(...)` 读 run（st.Store 为 nil 时回退到构造时 store 的全局 ScopedStore，兼容既有测试）；`ContextStage.Run` 在 `st.Store` 非 nil 时读写设备上下文（读 `GetDeviceContext`、写 `UpdateDeviceContext`，state_json=设备信息 JSON）

- [ ] **Step 1: Write the failing test**

在 `backend/internal/pipeline/stages_test.go` 追加：
```go
func TestTriageUsesScopedStore(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	now := time.Now()
	_ = s.SaveAlert(model.Alert{ID: "alarm-1", Device: "compute-01", Metric: "cpu_usage",
		Severity: "warning", Status: "active", FiredAt: now, CreatedAt: now})
	_ = s.SaveAlert(model.Alert{ID: "alarm-2", Device: "db-01", Metric: "disk_usage",
		Severity: "critical", Status: "active", FiredAt: now, CreatedAt: now})

	ts := NewTriageStage(s)
	// 任务作用域绑定 compute-01，只应看到 compute-01 的告警 -> 新告警 proceed
	st := &State{
		Alert: model.Alert{ID: "alarm-3", Device: "compute-01", Metric: "gpu_util",
			Severity: "critical", FiredAt: now, CreatedAt: now},
		Store: store.NewScoped(s, scope.Task("run-alarm-3", "compute-01")),
	}
	if err := ts.Run(context.Background(), st); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !st.Triage.Proceed {
		t.Fatalf("expected proceed for new alert, got %+v", st.Triage)
	}
}

func TestContextStageReadsAndWritesDeviceContext(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	mockdata.GenerateAll(42)
	st := &State{
		Run:   model.PipelineRun{ID: "run-1", AlertID: "alarm-1"},
		Alert: model.Alert{ID: "alarm-1", Device: "compute-01", Metric: "cpu_usage"},
		Store: store.NewScoped(s, scope.Task("run-1", "compute-01")),
	}
	if err := (ContextStage{}).Run(context.Background(), st); err != nil {
		t.Fatalf("Run: %v", err)
	}
	dc, err := s.GetDeviceContext("compute-01")
	if err != nil {
		t.Fatalf("GetDeviceContext: %v", err)
	}
	if dc.UpdatedBy != "run-1" || dc.StateJSON == "" {
		t.Fatalf("device context not written: %+v", dc)
	}
	if st.DeviceContext == nil {
		t.Fatalf("st.DeviceContext not populated")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/pipeline/...`
Expected: FAIL（triage 未用 scoped store、context 未读写设备上下文）

- [ ] **Step 3: Write implementation**

`backend/internal/pipeline/triage.go` 修改（`Run` 使用 scoped store，`store` 字段保留作为回退）：
```go
func (t *TriageStage) Run(_ context.Context, st *State) error {
	ss := st.Store
	if ss == nil {
		if t.store == nil {
			return errors.New("triage: no store available")
		}
		ss = store.NewScoped(t.store, scope.Global())
	}
	alerts, err := ss.ListAlerts()
	if err != nil {
		return err
	}
	// 同 device+metric 已有 completed run => 视为重复
	for _, a := range alerts {
		if a.Device == st.Alert.Device && a.Metric == st.Alert.Metric {
			if run, err := ss.GetRun("run-" + a.ID); err == nil && run.Status == model.StatusCompleted {
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
（需要 import `"errors"`、`"aiops/internal/scope"`。）

`backend/internal/pipeline/context.go` 修改：
```go
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
	if st.Store != nil {
		if dc, err := st.Store.GetDeviceContext(st.Alert.Device); err == nil {
			st.DeviceContext = &dc
		}
		_ = st.Store.UpdateDeviceContext(model.DeviceContext{
			DeviceID:  st.Alert.Device,
			StateJSON: model.MustJSON(dev),
			UpdatedBy: st.Run.ID,
			UpdatedAt: time.Now(),
		})
	}
	return nil
}
```
（需要 import `"time"`。）

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/pipeline/...`
Expected: PASS（含全部既有 pipeline 测试）

- [ ] **Step 5: Commit**

```bash
git add backend/internal/pipeline/triage.go backend/internal/pipeline/context.go backend/internal/pipeline/stages_test.go && git commit -m "feat: triage与context阶段接入隔离"
```

---

### Task 7: 装配与全量回归

**Files:**
- Modify: `backend/internal/pipeline/build.go`
- Modify: `backend/cmd/server/main.go`
- Modify: `backend/internal/pipeline/build_test.go`
- Modify: `backend/internal/e2e/e2e_test.go`

**Interfaces:**
- Consumes: `coordinator.New`、`pipeline.SetCoordinator`
- Produces: `pipeline.Build(s *store.Store, lc *llm.Client, coord *coordinator.Coordinator) *Engine`；`main.go` 装配 `coord := coordinator.New()` 传入 `Build`，`engine.Run` 自动按设备锁串行

- [ ] **Step 1: Write the failing test（更新 build 测试）**

`backend/internal/pipeline/build_test.go`：
```go
package pipeline

import (
	"path/filepath"
	"testing"

	"aiops/internal/coordinator"
	"aiops/internal/store"
)

func TestBuildHasSixStages(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	e := Build(s, nil, coordinator.New())
	if len(e.Stages()) != 6 {
		t.Fatalf("stages = %d, want 6", len(e.Stages()))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/pipeline/... ./internal/e2e/...`
Expected: FAIL（`Build` 签名不匹配）

- [ ] **Step 3: Write implementation**

`backend/internal/pipeline/build.go`：
```go
package pipeline

import (
	"aiops/internal/coordinator"
	"aiops/internal/llm"
	"aiops/internal/store"
)

func Build(s *store.Store, lc *llm.Client, coord *coordinator.Coordinator) *Engine {
	e := NewEngine(s, []Stage{
		NormalizeStage{},
		NewTriageStage(s),
		ContextStage{},
		EvidenceStage{},
		NewRCAStage(lc),
		NewRecommendStage(lc),
	})
	e.SetCoordinator(coord)
	return e
}
```

`backend/internal/e2e/e2e_test.go`：`pipeline.Build(s, nil)` → `pipeline.Build(s, nil, coordinator.New())`，并加 `"aiops/internal/coordinator"` import。

`backend/cmd/server/main.go`：import `"aiops/internal/coordinator"`；`eng := pipeline.Build(st, lc, coordinator.New())`。

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./... && go vet ./...`
Expected: 全部 PASS，vet 干净

- [ ] **Step 5: Commit**

```bash
git add backend/internal/pipeline/build.go backend/internal/pipeline/build_test.go backend/internal/e2e/e2e_test.go backend/cmd/server/main.go && git commit -m "feat: 装配coordinator完成隔离接线"
```

---

### Task 8: 端到端隔离验证

**Files:**
- Modify: `backend/internal/e2e/e2e_test.go`

**Interfaces:**
- Consumes: `store.ScopedStore`、`scope`、`model.DeviceContext`
- Produces: 追加一个 e2e 测试，验证：流水线在隔离下产出全部 6 阶段；device_context 被 context 阶段写入；任务级越权写被拒

- [ ] **Step 1: Write the failing test**

在 `backend/internal/e2e/e2e_test.go` 追加：
```go
func TestEndToEndIsolationWritesDeviceContext(t *testing.T) {
	mockdata.GenerateAll(2)
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	eng := pipeline.Build(s, nil, coordinator.New())
	now := time.Now()
	alert := model.Alert{ID: "alarm-iso", Device: "compute-01", Metric: "cpu_usage",
		Severity: "warning", Message: "CPU高", FiredAt: now, CreatedAt: now}
	if err := eng.Run(context.Background(), alert, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	run, _ := s.GetRun("run-" + alert.ID)
	if run.Status != model.StatusCompleted {
		t.Fatalf("run status = %q", run.Status)
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
	dc, err := s.GetDeviceContext("compute-01")
	if err != nil {
		t.Fatalf("device context not written: %v", err)
	}
	if dc.UpdatedBy != "run-"+alert.ID || dc.StateJSON == "" {
		t.Fatalf("device context = %+v", dc)
	}

	// 越权：任务作用域写其它 run 的 stage 应被拒
	sc := scope.Task("run-other", "compute-01")
	ss := store.NewScoped(s, sc)
	err = ss.SaveStage(model.StageResult{ID: "st-bad", RunID: "run-x", StageID: "rca",
		StageName: "x", Status: model.StageCompleted, StartedAt: now, EndedAt: now})
	if !errors.Is(err, scope.ErrScopeDenied) {
		t.Fatalf("cross-run write err = %v, want ErrScopeDenied", err)
	}
}
```
（import 增加 `"errors"`、`"aiops/internal/coordinator"`、`"aiops/internal/scope"`、`"aiops/internal/store"`。）

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/e2e/...`
Expected: FAIL（隔离下行为未实现时会失败——此时应已由 Task 5/6 实现，通常直接 PASS；若失败请检查）

- [ ] **Step 3: 全量回归**

Run: `cd backend && go test -race ./... && go vet ./...`
Expected: 全部 PASS（含 -race）、vet 干净。若 `TestStartPushesAlerts` 出现竞态，按既有方式（mutex 保护 got）修正——见测试文件现状，此任务不改变它。

- [ ] **Step 4: Commit**

```bash
git add backend/internal/e2e/e2e_test.go && git commit -m "test: 端到端隔离验证"
```

---

## Self-Review 记录

- **Spec 覆盖**：§3 Scope/权限矩阵→Task 1；§4 表加字段+device_context+Encode/Parse→Task 1、2；§5 ScopedStore+coordinator→Task 3、4；引擎/阶段接入+main 装配→Task 5、6、7；错误处理（ErrScopeDenied、锁超时 degraded）→Task 3、5；测试→各任务 + Task 8 e2e。
- **无占位符**：所有步骤含具体代码与断言。
- **类型一致性**：`scope.Scope`（指针字段 `*scope.Scope`）、`State.Store *store.ScopedStore`、`Build(s, lc, coord)` 三参数在 Task 5-7 间一致；`ScopedStore` 方法签名与 Task 3 定义一致，供 Task 5/6/8 使用。
- **既有测试兼容**：`TriageStage` 保留 `store` 字段作回退；`State.Scope` 用指针避免破坏直接构造 `State{}` 的既有测试；`Build` 签名变更同步更新 build_test/e2e/main。
