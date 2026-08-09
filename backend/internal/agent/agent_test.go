package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ctxpkg "aiops/internal/context"
	"aiops/internal/model"
	"aiops/internal/store"
)

func TestParseIntent(t *testing.T) {
	cases := []struct {
		text string
		want Intent
	}{
		{"有哪些告警", ListAlerts},
		{"查一下alarm-123是什么原因", AskRootCause},
		{"alarm-100怎么处理", AskRecommendation},
		{"排查下compute-01的告警", InspectAlert},
		{"进度如何", AskStatus},
		{"你好", Unknown},
	}
	for _, c := range cases {
		if got := ParseIntent(c.text); got != c.want {
			t.Fatalf("ParseIntent(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestExtractAlertIDHex(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cases := []struct {
		text string
		want string
	}{
		{"alarm-1859f1a2b什么原因", "alarm-1859f1a2b"},
		{"ALARM-1859F1A2B怎么处理", "alarm-1859F1A2B"},
	}
	for _, c := range cases {
		if got := extractAlertID(c.text, s); got != c.want {
			t.Fatalf("extractAlertID(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestAgentInspectAlertTriggersRun(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	now := time.Now()
	a := model.Alert{ID: "alarm-1", Device: "db-01", Metric: "disk_usage",
		Severity: "critical", FiredAt: now, CreatedAt: now}
	_ = s.SaveAlert(a)
	ag := New(s, nil, nil)
	// 用 nil engine 时 Handle 直接基于 store 回复
	msg, err := ag.Handle(context.Background(), "alarm-1什么原因")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if msg.Role != model.RoleAssistant || msg.Content == "" {
		t.Fatalf("msg = %+v", msg)
	}
	msgs, _ := s.ListMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
}

func TestAgentRuleAnswerIncludesLongTermMemory(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := ctxpkg.NewStore(s)
	now := time.Now()
	_ = cs.AddLongTermMemory(model.LongTermMemory{ID: "m1", Device: "db-01", Metric: "disk_usage",
		RootCause: "磁盘空间不足", Confidence: 0.8, ActionsJSON: `["清理日志"]`, RunID: "run-0", CreatedAt: now.Add(-time.Hour)})
	a := model.Alert{ID: "alarm-1", Device: "db-01", Metric: "disk_usage",
		Severity: "critical", FiredAt: now, CreatedAt: now}
	_ = s.SaveAlert(a)
	_ = s.SaveRun(model.PipelineRun{ID: "run-" + a.ID, AlertID: a.ID, Status: model.StatusCompleted, Iteration: 0, MaxIteration: 2, CreatedAt: now, UpdatedAt: now})
	_ = s.SaveStage(model.StageResult{ID: "st1", RunID: "run-" + a.ID, StageID: "rca", StageName: "根因分析",
		Status: model.StageCompleted, Output: `{"root_cause":"磁盘空间不足","evidence_chain":["e"],"confidence":0.8,"gaps":[]}`, Iteration: 0, StartedAt: now, EndedAt: now})
	ag := NewWithContext(s, nil, nil, cs, 3)
	msg, err := ag.Handle(context.Background(), "alarm-1什么原因")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(msg.Content, "上次") {
		t.Fatalf("reply missing long-term memory: %q", msg.Content)
	}
}

type fakeChatLLM struct {
	raw      string
	err      error
	lastUser string
}

func (f *fakeChatLLM) Complete(_ context.Context, _, user string) (string, error) {
	f.lastUser = user
	return f.raw, f.err
}

func TestAgentLLMAnswerUsesAssembledContext(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := ctxpkg.NewStore(s)
	now := time.Now()
	_ = cs.AddLongTermMemory(model.LongTermMemory{ID: "m1", Device: "db-01", Metric: "disk_usage",
		RootCause: "磁盘空间不足", Confidence: 0.8, ActionsJSON: `["清理日志"]`, RunID: "run-0", CreatedAt: now.Add(-time.Hour)})
	a := model.Alert{ID: "alarm-1", Device: "db-01", Metric: "disk_usage",
		Severity: "critical", FiredAt: now, CreatedAt: now}
	_ = s.SaveAlert(a)
	_ = s.SaveRun(model.PipelineRun{ID: "run-" + a.ID, AlertID: a.ID, Status: model.StatusCompleted, Iteration: 0, MaxIteration: 2, CreatedAt: now, UpdatedAt: now})
	_ = s.SaveStage(model.StageResult{ID: "st1", RunID: "run-" + a.ID, StageID: "rca", StageName: "根因分析",
		Status: model.StageCompleted, Output: `{"root_cause":"磁盘空间不足","evidence_chain":["e"],"confidence":0.8,"gaps":[]}`, Iteration: 0, StartedAt: now, EndedAt: now})
	fake := &fakeChatLLM{raw: "LLM回答：磁盘空间不足，建议清理日志"}
	ag := NewWithContext(s, fake, nil, cs, 3)
	msg, err := ag.Handle(context.Background(), "alarm-1什么原因")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(msg.Content, "LLM回答") {
		t.Fatalf("expected LLM answer, got %q", msg.Content)
	}
	if !strings.Contains(fake.lastUser, "【设备历史经验】") {
		t.Fatalf("assembled context missing in LLM prompt: %q", fake.lastUser)
	}
}

func TestAgentLLMFallsBackToRule(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	now := time.Now()
	a := model.Alert{ID: "alarm-1", Device: "db-01", Metric: "disk_usage",
		Severity: "critical", FiredAt: now, CreatedAt: now}
	_ = s.SaveAlert(a)
	_ = s.SaveRun(model.PipelineRun{ID: "run-" + a.ID, AlertID: a.ID, Status: model.StatusCompleted, Iteration: 0, MaxIteration: 2, CreatedAt: now, UpdatedAt: now})
	_ = s.SaveStage(model.StageResult{ID: "st1", RunID: "run-" + a.ID, StageID: "rca", StageName: "根因分析",
		Status: model.StageCompleted, Output: `{"root_cause":"磁盘空间不足","evidence_chain":["e"],"confidence":0.8,"gaps":[]}`, Iteration: 0, StartedAt: now, EndedAt: now})
	fake := &fakeChatLLM{err: errors.New("llm down")}
	cs := ctxpkg.NewStore(s)
	ag := NewWithContext(s, fake, nil, cs, 3)
	msg, err := ag.Handle(context.Background(), "alarm-1什么原因")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(msg.Content, "根因：") {
		t.Fatalf("expected rule fallback, got %q", msg.Content)
	}
}

func TestExtractQuery(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	dev, met := extractQuery("排查一下db-01的disk_usage", s)
	if dev != "db-01" || met != "disk_usage" {
		t.Fatalf("extractQuery = %q %q", dev, met)
	}
}
