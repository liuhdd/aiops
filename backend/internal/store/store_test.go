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
