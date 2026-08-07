package model

import (
	"encoding/json"
	"testing"
)

func TestNewID(t *testing.T) {
	a := NewID("alarm")
	b := NewID("alarm")
	if a == b {
		t.Fatalf("ids should differ: %s == %s", a, b)
	}
	if a[:6] != "alarm-" {
		t.Fatalf("id %q missing prefix", a)
	}
}

func TestMustJSON(t *testing.T) {
	rc := RCA{RootCause: "OOM", Confidence: 0.9, Gaps: []string{}}
	raw := MustJSON(rc)
	var out RCA
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.RootCause != "OOM" || out.Confidence != 0.9 {
		t.Fatalf("roundtrip failed: %+v", out)
	}
}

func TestStageStatusConstants(t *testing.T) {
	if StageRunning != "running" || StageCompleted != "completed" {
		t.Fatalf("stage constants wrong")
	}
}

func TestContextTypesJSON(t *testing.T) {
	te := ToolEvent{ID: "e1", RunID: "run-1", ToolName: "query_metric",
		ArgsJSON: "{}", ResultJSON: "[]", Status: ToolStatusOK}
	if te.Status != ToolStatusOK {
		t.Fatalf("status = %q", te.Status)
	}
	if got := MustJSON(te); got == "{}" {
		t.Fatalf("MustJSON empty")
	}
}
