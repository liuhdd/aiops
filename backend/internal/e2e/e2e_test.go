package e2e

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"aiops/internal/coordinator"
	"aiops/internal/mockdata"
	"aiops/internal/model"
	"aiops/internal/pipeline"
	"aiops/internal/store"
)

func TestEndToEndDiskUsage(t *testing.T) {
	mockdata.GenerateAll(1)
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	eng := pipeline.Build(s, nil, coordinator.New())
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
