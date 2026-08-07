package pipeline

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	ctxpkg "aiops/internal/context"
	"aiops/internal/coordinator"
	"aiops/internal/model"
	"aiops/internal/scope"
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

func TestEngineInjectsContextAndWritesNotes(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := ctxpkg.NewStore(s)
	reg := ctxpkg.NewRegistry()
	for _, tt := range ctxpkg.DefaultTools() {
		reg.Register(tt)
	}
	e := BuildWithContext(s, nil, coordinator.New(), cs, reg, false, 3)
	alert := model.Alert{ID: "alarm-ctx", Device: "db-01", Metric: "disk_usage",
		Severity: "critical", FiredAt: time.Now(), CreatedAt: time.Now()}
	if err := e.Run(context.Background(), alert, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sp, _ := cs.ListScratchpad("run-" + alert.ID)
	if len(sp) == 0 {
		t.Fatalf("no scratchpad notes written")
	}
	ltm, _ := cs.ListLongTermMemory("db-01", "disk_usage")
	if len(ltm) != 1 || ltm[0].RunID != "run-"+alert.ID {
		t.Fatalf("long term memory = %+v", ltm)
	}
}

func TestEngineSkipsScratchpadForFailedStage(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := ctxpkg.NewStore(s)
	reg := ctxpkg.NewRegistry()
	for _, tt := range ctxpkg.DefaultTools() {
		reg.Register(tt)
	}
	stages := []Stage{
		fakeStage{id: "ok", name: "正常", run: func(st *State) error { return nil }},
		fakeStage{id: "bad", name: "失败", run: func(st *State) error { return errors.New("boom") }},
	}
	e := NewEngine(s, stages)
	e.ctxStore = cs
	e.tools = reg
	alert := model.Alert{ID: "alarm-fail", Device: "db-01", Metric: "disk_usage"}
	if err := e.Run(context.Background(), alert, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	run, _ := s.GetRun("run-" + alert.ID)
	if run.Status != model.StatusDegraded {
		t.Fatalf("run status = %q, want degraded", run.Status)
	}
	sp, _ := cs.ListScratchpad("run-" + alert.ID)
	if len(sp) != 1 || sp[0].Step != "ok" {
		t.Fatalf("scratchpad notes = %+v, want only the completed stage %q", sp, "ok")
	}
}
