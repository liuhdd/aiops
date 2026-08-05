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
