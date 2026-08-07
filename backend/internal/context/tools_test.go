package context

import (
	"context"
	"errors"
	"testing"

	"aiops/internal/mockdata"
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
