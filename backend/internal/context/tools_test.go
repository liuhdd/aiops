package context

import (
	"context"
	"errors"
	"testing"

	"aiops/internal/mockdata"
	"aiops/internal/model"
	"aiops/internal/retrieve"
)

func TestDefaultToolsRegistered(t *testing.T) {
	reg := NewRegistry()
	for _, t := range DefaultTools() {
		reg.Register(t)
	}
	if len(reg.List()) != 6 {
		t.Fatalf("tools = %d, want 6", len(reg.List()))
	}
	if _, ok := reg.Lookup("write_scratchpad"); !ok {
		t.Fatalf("write_scratchpad missing")
	}
}

func TestQueryMetricTool(t *testing.T) {
	mockdata.GenerateAll(42)
	reg := NewRegistry()
	for _, t := range DefaultTools() {
		reg.Register(t)
	}
	res, err := reg.Execute(context.Background(), "query_metric",
		map[string]any{"device": "db-01", "metric": "disk_usage", "window_minutes": 30})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	pts, ok := res.([]mockdata.MetricPointView)
	if !ok || len(pts) == 0 {
		t.Fatalf("res = %#v", res)
	}
}

func TestSearchLogsToolKeywords(t *testing.T) {
	mockdata.GenerateAll(42)
	reg := NewRegistry()
	for _, t := range DefaultTools() {
		reg.Register(t)
	}
	res, err := reg.Execute(context.Background(), "search_logs",
		map[string]any{"device": "db-01", "keywords": []any{"no space"}, "window_minutes": 120})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	logs, ok := res.([]mockdata.LogEntryView)
	if !ok || len(logs) == 0 {
		t.Fatalf("res = %#v", res)
	}
}

func TestWriteScratchpadIntercepted(t *testing.T) {
	reg := NewRegistry()
	for _, t := range DefaultTools() {
		reg.Register(t)
	}
	_, err := reg.Execute(context.Background(), "write_scratchpad", map[string]any{"content": "x"})
	if !errors.Is(err, ErrScratchpadHandled) {
		t.Fatalf("err = %v, want ErrScratchpadHandled", err)
	}
}

func TestUnknownTool(t *testing.T) {
	reg := NewRegistry()
	_, err := reg.Execute(context.Background(), "nope", nil)
	if err == nil {
		t.Fatalf("expected error for unknown tool")
	}
}

type fakeSearcher struct {
	res        []retrieve.Result
	lastCaseQ  retrieve.Query
	lastManual retrieve.Query
}

func (f *fakeSearcher) SearchCases(_ context.Context, q retrieve.Query, o retrieve.Options) ([]retrieve.Result, error) {
	f.lastCaseQ = q
	return f.res, nil
}
func (f *fakeSearcher) SearchManuals(_ context.Context, q retrieve.Query, o retrieve.Options) ([]retrieve.Result, error) {
	f.lastManual = q
	return f.res, nil
}

func TestSearchCaseTool(t *testing.T) {
	svc := &fakeSearcher{res: []retrieve.Result{{DocID: "d1", Title: "GPU掉卡案例", Category: model.KBCase, Content: "正文"}}}
	reg := NewRegistry()
	reg.Register(NewSearchCaseTool(svc))
	reg.Register(NewSearchManualTool(svc))
	if _, ok := reg.Lookup("search_case"); !ok {
		t.Fatalf("search_case missing")
	}
	if _, ok := reg.Lookup("search_manual"); !ok {
		t.Fatalf("search_manual missing")
	}
	res, err := reg.Execute(context.Background(), "search_case",
		map[string]any{"query": "GPU 掉卡", "metric": "gpu_util", "top_k": 5})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	outs, ok := res.([]retrieve.Result)
	if !ok || len(outs) != 1 || outs[0].DocID != "d1" {
		t.Fatalf("res = %#v", res)
	}
	if svc.lastCaseQ.Text != "GPU 掉卡" || svc.lastCaseQ.Metric != "gpu_util" {
		t.Fatalf("search_case 查询透传错误: %+v", svc.lastCaseQ)
	}
}

func TestSearchManualToolCategory(t *testing.T) {
	svc := &fakeSearcher{res: []retrieve.Result{{DocID: "d3", Title: "交换机手册", Category: model.KBManual, Content: "正文"}}}
	reg := NewRegistry()
	reg.Register(NewSearchManualTool(svc))
	res, err := reg.Execute(context.Background(), "search_manual",
		map[string]any{"query": "光模块", "category": "manual", "metric": "net_in", "top_k": 5})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	outs, ok := res.([]retrieve.Result)
	if !ok || len(outs) != 1 || outs[0].DocID != "d3" {
		t.Fatalf("res = %#v", res)
	}
	if svc.lastManual.Category != model.KBManual || svc.lastManual.Text != "光模块" {
		t.Fatalf("search_manual 应透传 category=manual, got %+v", svc.lastManual)
	}
}
