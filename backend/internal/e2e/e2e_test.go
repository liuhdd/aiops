package e2e

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	ctxpkg "aiops/internal/context"
	"aiops/internal/coordinator"
	"aiops/internal/llm"
	"aiops/internal/mockdata"
	"aiops/internal/model"
	"aiops/internal/pipeline"
	"aiops/internal/scope"
	"aiops/internal/store"
)

func TestEndToEndMissingLLMFailsRCAAndPreservesIsolation(t *testing.T) {
	mockdata.GenerateAll(1)
	s, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()
	cs := ctxpkg.NewStore(s)
	reg := ctxpkg.NewRegistry()
	for _, tool := range ctxpkg.DefaultTools() {
		reg.Register(tool)
	}
	eng := pipeline.Build(s, nil, coordinator.New(), cs, reg, 3)
	now := time.Now()
	alert := model.Alert{ID: "alarm-iso", Device: "compute-01", Metric: "cpu_usage",
		Severity: "warning", Message: "CPU高", FiredAt: now, CreatedAt: now}
	if err := eng.Run(context.Background(), alert, nil); !errors.Is(err, llm.ErrNoAPIKey) {
		t.Fatalf("Run: %v, want ErrNoAPIKey", err)
	}
	run, err := s.GetRun("run-" + alert.ID)
	if err != nil || run.Status != model.StatusFailed {
		t.Fatalf("run = %+v err=%v", run, err)
	}
	stages, err := s.ListStages(run.ID)
	if err != nil {
		t.Fatalf("ListStages: %v", err)
	}
	wantStages := []string{"normalize", "triage", "context", "evidence", "rca"}
	if len(stages) != len(wantStages) {
		t.Fatalf("stages = %+v, want completed pre-RCA stages followed by failed RCA only", stages)
	}
	for i, stage := range stages {
		if stage.StageID != wantStages[i] {
			t.Fatalf("stage %d = %q, want %q", i, stage.StageID, wantStages[i])
		}
		if stage.StageID == "rca" {
			if stage.Status != model.StageFailed || stage.Output != "" || stage.Error == "" {
				t.Fatalf("failed RCA = %+v, want error without output", stage)
			}
		} else if stage.Status != model.StageCompleted {
			t.Fatalf("pre-RCA stage = %+v, want completed", stage)
		}
	}
	ltm, err := cs.ListLongTermMemory(alert.Device, alert.Metric)
	if err != nil || len(ltm) != 0 {
		t.Fatalf("long term memory = %+v err=%v, want no memory for failed RCA", ltm, err)
	}
	dc, err := s.GetDeviceContext("compute-01")
	if err != nil {
		t.Fatalf("device context not written: %v", err)
	}
	if dc.UpdatedBy != "run-"+alert.ID || dc.StateJSON == "" {
		t.Fatalf("device context = %+v", dc)
	}

	// 拒绝任务作用域写入其他 run。Reject writes to another run from a task scope.
	sc := scope.Task("run-other", "compute-01")
	ss := store.NewScoped(s, sc)
	err = ss.SaveStage(model.StageResult{ID: "st-bad", RunID: "run-x", StageID: "rca",
		StageName: "x", Status: model.StageCompleted, StartedAt: now, EndedAt: now})
	if !errors.Is(err, scope.ErrScopeDenied) {
		t.Fatalf("cross-run write err = %v, want ErrScopeDenied", err)
	}
}
