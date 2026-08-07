package context

import (
	"context"
	"path/filepath"
	"testing"

	"aiops/internal/mockdata"
	"aiops/internal/model"
	"aiops/internal/store"
)

func TestEngineScratchpadAndLongTerm(t *testing.T) {
	mockdata.GenerateAll(42)
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := NewStore(s)
	reg := NewRegistry()
	for _, tt := range DefaultTools() {
		reg.Register(tt)
	}
	e := NewEngine(cs, reg, "run-1", "db-01", "disk_usage", true, 3)
	if !e.ReactEnabled() || e.RunID() != "run-1" {
		t.Fatalf("engine = %+v", e)
	}
	if err := e.AddScratchpad("rca", "假设: 磁盘满", "write_scratchpad"); err != nil {
		t.Fatalf("AddScratchpad: %v", err)
	}
	_ = e.AddToolEvent(model.ToolEvent{ID: model.NewID("evt"), RunID: "run-1", ToolName: "query_metric",
		ArgsJSON: `{"device":"db-01"}`, ResultJSON: "[]", Status: model.ToolStatusOK})
	if err := e.SaveLongTermMemory(&model.RCA{RootCause: "磁盘空间不足", Confidence: 0.8},
		&model.Recommendation{Actions: []string{"清理日志"}, RunbookRef: "disk_usage"}); err != nil {
		t.Fatalf("SaveLongTermMemory: %v", err)
	}
	ltm, _ := cs.ListLongTermMemory("db-01", "disk_usage")
	if len(ltm) != 1 || ltm[0].RunID != "run-1" {
		t.Fatalf("long term = %+v", ltm)
	}
}

func TestEngineRunReactEndToEnd(t *testing.T) {
	mockdata.GenerateAll(42)
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := NewStore(s)
	reg := NewRegistry()
	for _, tt := range DefaultTools() {
		reg.Register(tt)
	}
	e := NewEngine(cs, reg, "run-1", "db-01", "disk_usage", true, 3)
	sc := &scriptedCompleter{steps: []string{
		`{"thought":"查日志","tool_call":{"name":"search_logs","args":{"device":"db-01","keywords":["no space"],"window_minutes":120}}}`,
		`{"thought":"结论","final_answer":{"root_cause":"磁盘空间不足","evidence_chain":["ext4"],"confidence":0.8,"gaps":[]}}`,
	}}
	res, err := e.RunReact(context.Background(), sc, "sys", Query{Device: "db-01", Metric: "disk_usage", RunID: "run-1"}, "【临时笔记】\n")
	if err != nil || res.Final == nil {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	evs, _ := e.ListToolEvents()
	if len(evs) != 1 || evs[0].ToolName != "search_logs" || evs[0].Status != model.ToolStatusOK {
		t.Fatalf("tool events = %+v", evs)
	}
}
