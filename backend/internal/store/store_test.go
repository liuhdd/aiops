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

func TestMigrateCreatesContextTables(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('scratchpad_entries','tool_events','long_term_memory')`).Scan(&n)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 3 {
		t.Fatalf("context tables = %d, want 3", n)
	}
}
