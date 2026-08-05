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

func TestEngineBacktrackRerunsOnlyEvidenceAndRCA(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	stages := []Stage{
		fakeStage{id: "normalize", name: "规范化", run: func(st *State) error {
			st.RawAlerts["normalize"] = "ok"
			return nil
		}},
		fakeStage{id: "triage", name: "分诊", run: func(st *State) error {
			st.Triage = &model.TriageResult{Proceed: true}
			return nil
		}},
		fakeStage{id: "context", name: "上下文", run: func(st *State) error {
			st.Context = &model.Context{}
			return nil
		}},
		fakeStage{id: "evidence", name: "证据收集", run: func(st *State) error {
			if st.Iteration == 0 {
				st.Evidence = &model.Evidence{Gaps: []string{"missing log window"}}
			} else {
				st.Evidence = &model.Evidence{Gaps: []string{}}
			}
			return nil
		}},
		fakeStage{id: "rca", name: "根因分析", run: func(st *State) error {
			st.RCA = &model.RCA{Gaps: st.Evidence.Gaps, Confidence: 0.9}
			return nil
		}},
		fakeStage{id: "recommend", name: "建议", run: func(st *State) error {
			st.Recommendation = &model.Recommendation{}
			return nil
		}},
	}
	e := NewEngine(s, stages)
	alert := model.Alert{ID: "alarm-3", Device: "gpu-01", Metric: "gpu_util"}
	if err := e.Run(context.Background(), alert, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	results, _ := s.ListStages("run-" + alert.ID)
	counts := map[string]map[int]int{}
	for _, r := range results {
		if counts[r.StageID] == nil {
			counts[r.StageID] = map[int]int{}
		}
		counts[r.StageID][r.Iteration]++
	}
	for _, id := range []string{"normalize", "triage", "context"} {
		if counts[id][0] != 1 || counts[id][1] != 0 {
			t.Fatalf("stage %q rows = %v, want only iteration 0 once", id, counts[id])
		}
	}
	for _, id := range []string{"evidence", "rca"} {
		if counts[id][0] != 1 || counts[id][1] != 1 {
			t.Fatalf("stage %q rows = %v, want one row per iteration 0 and 1", id, counts[id])
		}
	}
	if counts["recommend"][0] != 0 || counts["recommend"][1] != 1 {
		t.Fatalf("stage %q rows = %v, want only iteration 1 once", "recommend", counts["recommend"])
	}
	var recommendStatus string
	for _, r := range results {
		if r.StageID == "recommend" {
			recommendStatus = r.Status
		}
	}
	if recommendStatus != model.StageCompleted {
		t.Fatalf("recommend status = %q, want %q", recommendStatus, model.StageCompleted)
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
