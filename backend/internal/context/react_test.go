package context

import (
	"context"
	"errors"
	"testing"

	"aiops/internal/mockdata"
)

type scriptedCompleter struct {
	steps []string
	i     int
}

func (s *scriptedCompleter) Complete(_ context.Context, _, _ string) (string, error) {
	if s.i >= len(s.steps) {
		return "", errors.New("no more scripted steps")
	}
	r := s.steps[s.i]
	s.i++
	return r, nil
}

func newTestReg(t *testing.T) *Registry {
	t.Helper()
	mockdata.GenerateAll(42)
	reg := NewRegistry()
	for _, tt := range DefaultTools() {
		reg.Register(tt)
	}
	return reg
}

func TestReactToolThenFinal(t *testing.T) {
	reg := newTestReg(t)
	sc := &scriptedCompleter{steps: []string{
		`{"thought":"查磁盘","tool_call":{"name":"query_metric","args":{"device":"db-01","metric":"disk_usage","window_minutes":30}}}`,
		`{"thought":"有数据","final_answer":{"root_cause":"磁盘空间不足","evidence_chain":["e"],"confidence":0.8,"gaps":[]}}`,
	}}
	res, err := RunReact(context.Background(), sc, "sys", "usr", reg, nil, ReactOptions{MaxSteps: 5, ToolErrorsAllowed: 2})
	if err != nil {
		t.Fatalf("RunReact: %v", err)
	}
	if res.Final == nil {
		t.Fatalf("no final answer")
	}
	if len(res.Turns) != 1 || res.Turns[0].Tool != "query_metric" || res.Turns[0].Status != "ok" {
		t.Fatalf("turns = %+v", res.Turns)
	}
}

func TestReactImmediateFinal(t *testing.T) {
	reg := newTestReg(t)
	sc := &scriptedCompleter{steps: []string{
		`{"thought":"直接答","final_answer":{"root_cause":"x","evidence_chain":[],"confidence":0.5,"gaps":[]}}`,
	}}
	res, err := RunReact(context.Background(), sc, "s", "u", reg, nil, ReactOptions{MaxSteps: 5, ToolErrorsAllowed: 2})
	if err != nil || res.Final == nil || len(res.Turns) != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestReactToolErrorThenFinal(t *testing.T) {
	reg := newTestReg(t)
	sc := &scriptedCompleter{steps: []string{
		`{"thought":"调未知工具","tool_call":{"name":"nope","args":{}}}`,
		`{"thought":"换预案","final_answer":{"root_cause":"x","evidence_chain":[],"confidence":0.5,"gaps":[]}}`,
	}}
	res, err := RunReact(context.Background(), sc, "s", "u", reg, nil, ReactOptions{MaxSteps: 5, ToolErrorsAllowed: 2})
	if err != nil {
		t.Fatalf("RunReact: %v", err)
	}
	if res.Final == nil {
		t.Fatalf("expected final after tolerated error")
	}
	if res.Turns[0].Status != "error" {
		t.Fatalf("turn0 status = %q, want error", res.Turns[0].Status)
	}
}

func TestReactAbortsOnTooManyToolErrors(t *testing.T) {
	reg := newTestReg(t)
	sc := &scriptedCompleter{steps: []string{
		`{"thought":"错1","tool_call":{"name":"nope","args":{}}}`,
		`{"thought":"错2","tool_call":{"name":"nope2","args":{}}}`,
	}}
	_, err := RunReact(context.Background(), sc, "s", "u", reg, nil, ReactOptions{MaxSteps: 5, ToolErrorsAllowed: 2})
	if err == nil {
		t.Fatalf("expected abort error")
	}
}

func TestReactMaxStepsExhausted(t *testing.T) {
	reg := newTestReg(t)
	steps := []string{}
	for i := 0; i < 3; i++ {
		steps = append(steps, `{"thought":"重复","tool_call":{"name":"search_runbook","args":{"metric":"disk_usage"}}}`)
	}
	sc := &scriptedCompleter{steps: steps}
	res, err := RunReact(context.Background(), sc, "s", "u", reg, nil, ReactOptions{MaxSteps: 3, ToolErrorsAllowed: 2})
	if err != nil {
		t.Fatalf("exhaustion should not error: %v", err)
	}
	if res.Final != nil {
		t.Fatalf("should have no final on exhaustion")
	}
	if len(res.Turns) != 3 {
		t.Fatalf("turns = %d, want 3", len(res.Turns))
	}
}

func TestReactSideEffectIntercepts(t *testing.T) {
	reg := newTestReg(t)
	var wrote bool
	sc := &scriptedCompleter{steps: []string{
		`{"thought":"记笔记","tool_call":{"name":"write_scratchpad","args":{"step":"rca","content":"hypothesis"}}}`,
		`{"thought":"done","final_answer":{"root_cause":"x","evidence_chain":[],"confidence":0.5,"gaps":[]}}`,
	}}
	side := func(name string, args map[string]any) (any, error) {
		if name == "write_scratchpad" {
			wrote = true
			return "ok", nil
		}
		return reg.Execute(context.Background(), name, args)
	}
	res, err := RunReact(context.Background(), sc, "s", "u", reg, side, ReactOptions{MaxSteps: 5, ToolErrorsAllowed: 2})
	if err != nil || res.Final == nil {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !wrote {
		t.Fatalf("side effect not invoked")
	}
}
