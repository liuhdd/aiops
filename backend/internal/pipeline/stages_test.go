package pipeline

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ctxpkg "aiops/internal/context"
	"aiops/internal/llm"
	"aiops/internal/mockdata"
	"aiops/internal/model"
	"aiops/internal/scope"
	"aiops/internal/store"
)

type fakeCompleter struct {
	raw string
	err error
}

func (f *fakeCompleter) Complete(_ context.Context, _, _ string) (string, error) {
	return f.raw, f.err
}

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

func TestRCARequiresLLM(t *testing.T) {
	st := &State{
		Alert: model.Alert{ID: "a1", Device: "db-01", Metric: "disk_usage"},
		RCA:   &model.RCA{RootCause: "旧结论"},
	}
	err := NewRCAStage(nil).Run(context.Background(), st)
	if !errors.Is(err, llm.ErrNoAPIKey) {
		t.Fatalf("Run error = %v, want ErrNoAPIKey", err)
	}
	if st.RCA != nil {
		t.Fatalf("failed RCA retained a conclusion: %+v", st.RCA)
	}
}

func TestRCARequiresContext(t *testing.T) {
	st := &State{
		Alert: model.Alert{ID: "a1", Device: "db-01", Metric: "disk_usage"},
		RCA:   &model.RCA{RootCause: "旧结论"},
	}
	err := NewRCAStage(&fakeCompleter{raw: `{"root_cause":"磁盘空间不足","evidence_chain":["e"],"confidence":0.8,"gaps":[]}`}).Run(context.Background(), st)
	if err == nil || !strings.Contains(err.Error(), "上下文") {
		t.Fatalf("Run error = %v, want missing context", err)
	}
	if st.RCA != nil {
		t.Fatalf("failed RCA retained a conclusion: %+v", st.RCA)
	}
}

func TestRCAFailsWhenContextUnavailable(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	cs := ctxpkg.NewStore(s)
	if err := s.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}
	st := &State{
		Run:   model.PipelineRun{ID: "run-rca"},
		Alert: model.Alert{ID: "alarm-rca", Device: "db-01", Metric: "disk_usage"},
		RCA:   &model.RCA{RootCause: "旧结论"},
		Ctx:   ctxpkg.NewEngine(cs, ctxpkg.NewRegistry(), "run-rca", "db-01", "disk_usage", 3),
	}
	err = NewRCAStage(&fakeCompleter{}).Run(context.Background(), st)
	if err == nil || !strings.Contains(err.Error(), "上下文组装失败") {
		t.Fatalf("Run error = %v, want context assembly failure", err)
	}
	if st.RCA != nil {
		t.Fatalf("failed RCA retained a conclusion: %+v", st.RCA)
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
	st := newRCAState(t)
	fake := &fakeCompleter{raw: `{"final_answer":{"root_cause":"OOM","evidence_chain":["e1"],"confidence":0.8,"gaps":[]}}`}
	if err := NewRCAStage(fake).Run(context.Background(), st); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.RCA == nil || st.RCA.RootCause != "OOM" || st.RCA.Confidence != 0.8 || len(st.RCA.EvidenceChain) != 1 || st.RCA.EvidenceChain[0] != "e1" || len(st.RCA.Gaps) != 0 {
		t.Fatalf("RCA = %+v", st.RCA)
	}
}

func TestTriageUsesScopedStore(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	now := time.Now()
	_ = s.SaveAlert(model.Alert{ID: "alarm-1", Device: "compute-01", Metric: "cpu_usage",
		Severity: "warning", Status: "active", FiredAt: now, CreatedAt: now})
	_ = s.SaveRun(model.PipelineRun{ID: "run-alarm-1", AlertID: "alarm-1", Status: model.StatusCompleted,
		Iteration: 0, MaxIteration: 2, CreatedAt: now, UpdatedAt: now})

	ts := NewTriageStage(s)

	// 同设备任务作用域（compute-01）：读到宿主设备的已完成 run -> 去重
	sameDev := &State{
		Alert: model.Alert{ID: "alarm-new", Device: "compute-01", Metric: "cpu_usage",
			Severity: "warning", FiredAt: now, CreatedAt: now},
		Store: store.NewScoped(s, scope.Task("run-alarm-new", "compute-01")),
	}
	if err := ts.Run(context.Background(), sameDev); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !sameDev.Triage.Deduped || sameDev.Triage.Proceed {
		t.Fatalf("same-device task should dedup: %+v", sameDev.Triage)
	}

	// 他设备任务作用域（db-01）：即使告警与 alarm-1 类型相同，也看不到 compute-01 的 run -> 新告警 proceed
	otherDev := &State{
		Alert: model.Alert{ID: "alarm-new-2", Device: "compute-01", Metric: "cpu_usage",
			Severity: "critical", FiredAt: now, CreatedAt: now},
		Store: store.NewScoped(s, scope.Task("run-alarm-new-2", "db-01")),
	}
	if err := ts.Run(context.Background(), otherDev); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if otherDev.Triage.Deduped || !otherDev.Triage.Proceed {
		t.Fatalf("other-device task should proceed: %+v", otherDev.Triage)
	}
}

func TestEvidenceRecordsToolEvents(t *testing.T) {
	mockdata.GenerateAll(42)
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := ctxpkg.NewStore(s)
	st := &State{
		Run:   model.PipelineRun{ID: "run-1", AlertID: "alarm-1"},
		Alert: model.Alert{ID: "alarm-1", Device: "db-01", Metric: "disk_usage", FiredAt: time.Now().Add(-time.Hour)},
	}
	st.SuggestLogs = []string{"no space"}
	st.Ctx = ctxpkg.NewEngine(cs, nil, "run-1", "db-01", "disk_usage", 3)
	if err := (EvidenceStage{}).Run(context.Background(), st); err != nil {
		t.Fatalf("Run: %v", err)
	}
	evs, _ := cs.ListToolEvents("run-1")
	if len(evs) != 2 { // query_metric + search_logs
		t.Fatalf("tool events = %d, want 2", len(evs))
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

func TestRecommendInjectsAssembledContext(t *testing.T) {
	mockdata.GenerateAll(42)
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := ctxpkg.NewStore(s)
	st := &State{
		Run:   model.PipelineRun{ID: "run-1", AlertID: "alarm-1"},
		Alert: model.Alert{ID: "alarm-1", Device: "db-01", Metric: "disk_usage", Severity: "critical"},
		RCA:   &model.RCA{RootCause: "磁盘空间不足"},
	}
	_ = cs.AddLongTermMemory(model.LongTermMemory{ID: "m1", Device: "db-01", Metric: "disk_usage",
		RootCause: "磁盘空间不足", Confidence: 0.8, ActionsJSON: `["清理"]`, RunID: "run-0", CreatedAt: time.Now()})
	rec := NewRecommendStage(&fakeCompleter{raw: `{"actions":["清理日志"],"runbook_ref":"disk_usage","risk":"低"}`})
	if err := rec.Run(context.Background(), st); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.Recommendation == nil || len(st.Recommendation.Actions) == 0 {
		t.Fatalf("recommendation = %+v", st.Recommendation)
	}
}

func newRCAState(t *testing.T) *State {
	t.Helper()
	mockdata.GenerateAll(42)
	s, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})
	cs := ctxpkg.NewStore(s)
	reg := ctxpkg.NewRegistry()
	for _, tt := range ctxpkg.DefaultTools() {
		reg.Register(tt)
	}
	return &State{
		Run:   model.PipelineRun{ID: "run-rca", AlertID: "alarm-rca"},
		Alert: model.Alert{ID: "alarm-rca", Device: "db-01", Metric: "disk_usage", Severity: "critical"},
		RCA:   &model.RCA{RootCause: "旧结论"},
		Ctx:   ctxpkg.NewEngine(cs, reg, "run-rca", "db-01", "disk_usage", 3),
	}
}

func TestRCAReactFailsWhenStepsExhausted(t *testing.T) {
	st := newRCAState(t)
	// 模型持续调用工具而不给结论，耗尽步数后必须失败。 / Tool-only turns must fail when the step limit is reached.
	fake := &fakeCompleter{raw: `{"thought":"调工具","tool_call":{"name":"search_runbook","args":{"metric":"disk_usage"}}}`}
	err := NewRCAStage(fake).Run(context.Background(), st)
	if err == nil || !strings.Contains(err.Error(), "最大步数") {
		t.Fatalf("Run error = %v, want exhausted steps", err)
	}
	if st.RCA != nil {
		t.Fatalf("failed RCA retained a conclusion: %+v", st.RCA)
	}
	evs, err := st.Ctx.ListToolEvents()
	if err != nil || len(evs) != 5 {
		t.Fatalf("tool events = %d, error = %v, want 5", len(evs), err)
	}
}

type sysCapturingLLM struct {
	system string
	raw    string
}

func (f *sysCapturingLLM) Complete(_ context.Context, system, _ string) (string, error) {
	f.system = system
	return f.raw, nil
}

func TestRCAReactPropagatesFailures(t *testing.T) {
	modelErr := errors.New("model request failed")
	cases := []struct {
		name string
		raw  string
		err  error
	}{
		{name: "model", err: modelErr},
		{name: "step JSON", raw: `not-json`},
		{name: "missing step action", raw: `{}`},
		{name: "tool", raw: `{"tool_call":{"name":"unknown_tool","args":{}}}`},
		{name: "final JSON", raw: `{"final_answer":[]}`},
		{name: "null final", raw: `{"final_answer":null}`},
		{name: "missing final fields", raw: `{"final_answer":{"root_cause":"磁盘空间不足"}}`},
		{name: "empty root cause", raw: `{"final_answer":{"root_cause":" ","evidence_chain":[],"confidence":0.8,"gaps":[]}}`},
		{name: "invalid confidence", raw: `{"final_answer":{"root_cause":"磁盘空间不足","evidence_chain":["e"],"confidence":1.2,"gaps":[]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newRCAState(t)
			err := NewRCAStage(&fakeCompleter{raw: tc.raw, err: tc.err}).Run(context.Background(), st)
			if err == nil {
				t.Fatal("Run succeeded for an invalid ReAct result")
			}
			if tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatalf("Run error = %v, want %v", err, tc.err)
			}
			if st.RCA != nil {
				t.Fatalf("failed RCA retained a conclusion: %+v", st.RCA)
			}
		})
	}
}

func TestRecommendSystemHasRunbookRule(t *testing.T) {
	fake := &sysCapturingLLM{raw: `{"actions":["清理日志"],"runbook_ref":"disk_usage","risk":"低"}`}
	st := &State{Alert: model.Alert{ID: "a1", Device: "db-01", Metric: "disk_usage"},
		RCA: &model.RCA{RootCause: "磁盘空间不足"}}
	if err := (NewRecommendStage(fake)).Run(context.Background(), st); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, kw := range []string{"无预案参考", "runbook_ref", "只输出JSON"} {
		if !strings.Contains(fake.system, kw) {
			t.Fatalf("system 应含 %q, got %q", kw, fake.system)
		}
	}
	if st.Recommendation == nil || len(st.Recommendation.Actions) == 0 {
		t.Fatalf("应解析出处置建议, got %+v", st.Recommendation)
	}
}
