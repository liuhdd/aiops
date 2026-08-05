package pipeline

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"aiops/internal/mockdata"
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
