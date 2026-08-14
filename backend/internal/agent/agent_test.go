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

type scriptedLLM struct {
	raw string
	err error
}

func (s *scriptedLLM) Complete(_ context.Context, _, _ string) (string, error) {
	return s.raw, s.err
}

func TestRewriteAndSplit(t *testing.T) {
	c := &scriptedLLM{raw: `{"rewritten":"q1？q2？","sub_questions":["q1？","q2？"]}`}
	rewritten, subs, err := RewriteAndSplit(context.Background(), "q1？q2？", c)
	if err != nil {
		t.Fatalf("RewriteAndSplit: %v", err)
	}
	if rewritten == "" || len(subs) != 2 {
		t.Fatalf("rewritten=%q subs=%v", rewritten, subs)
	}
	// 非法 JSON → err
	bad := &scriptedLLM{raw: "not json"}
	if _, _, err := RewriteAndSplit(context.Background(), "x", bad); err == nil {
		t.Fatal("非法 JSON 应返回 err")
	}
	// completer 出错 → err
	fail := &scriptedLLM{err: errors.New("llm down")}
	if _, _, err := RewriteAndSplit(context.Background(), "x", fail); err == nil {
		t.Fatal("completer 出错应返回 err")
	}
}

func TestClassifyWithLight(t *testing.T) {
	c := &scriptedLLM{raw: `{"intent":"AskRootCause"}`}
	if got := classifyWithLight(context.Background(), "为什么磁盘满了", c); got != AskRootCause {
		t.Fatalf("classifyWithLight = %v, want AskRootCause", got)
	}
	// 非法意图名 → Unknown
	bad := &scriptedLLM{raw: `{"intent":"Nope"}`}
	if got := classifyWithLight(context.Background(), "x", bad); got != Unknown {
		t.Fatalf("非法意图应为 Unknown, got %v", got)
	}
	// 解析失败 → Unknown
	fail := &scriptedLLM{err: errors.New("boom")}
	if got := classifyWithLight(context.Background(), "x", fail); got != Unknown {
		t.Fatalf("LLM 出错应为 Unknown, got %v", got)
	}
}

func TestParseIntentWithLight(t *testing.T) {
	// 规则命中不调 LLM（fake 返回 Nope 也不影响）
	c := &scriptedLLM{raw: `{"intent":"Nope"}`}
	if got := ParseIntentWithLight(context.Background(), "alarm-1什么原因", c); got != AskRootCause {
		t.Fatalf("规则命中应为 AskRootCause, got %v", got)
	}
	// 规则 Unknown → LLM 分类
	if got := ParseIntentWithLight(context.Background(), "帮我看看今天天气", &scriptedLLM{raw: `{"intent":"ListAlerts"}`}); got != ListAlerts {
		t.Fatalf("LLM 分类应为 ListAlerts, got %v", got)
	}
	// completer 为 nil → 回退规则结果（Unknown）
	if got := ParseIntentWithLight(context.Background(), "今天天气如何", nil); got != Unknown {
		t.Fatalf("nil completer 应回退 Unknown, got %v", got)
	}
}

func TestParseIntentSet(t *testing.T) {
	// LLM 拆分两个子问题：规则命中 AskRootCause + 规则命中 ListAlerts
	c := &scriptedLLM{raw: `{"rewritten":"有哪些告警？alarm-1什么原因？","sub_questions":["有哪些告警","alarm-1什么原因"]}`}
	got := ParseIntentSet(context.Background(), "有哪些告警？alarm-1什么原因？", c, 3)
	if len(got) != 2 || got[0] != ListAlerts || got[1] != AskRootCause {
		t.Fatalf("ParseIntentSet = %v, want [ListAlerts AskRootCause]", got)
	}
	// 超过 maxSplit 截断
	got2 := ParseIntentSet(context.Background(), "有哪些告警？alarm-1什么原因？", c, 1)
	if len(got2) != 1 || got2[0] != ListAlerts {
		t.Fatalf("ParseIntentSet 应截断到 1, got %v", got2)
	}
	// 重写拆分失败 → 回退单意图规则
	fail := &scriptedLLM{err: errors.New("boom")}
	got3 := ParseIntentSet(context.Background(), "有哪些告警", fail, 3)
	if len(got3) != 1 || got3[0] != ListAlerts {
		t.Fatalf("重写失败应回退规则, got %v", got3)
	}
	// completer nil → 单意图规则
	got4 := ParseIntentSet(context.Background(), "有哪些告警", nil, 3)
	if len(got4) != 1 || got4[0] != ListAlerts {
		t.Fatalf("nil completer 应回退规则, got %v", got4)
	}
}

func TestIntentName(t *testing.T) {
	if IntentName(AskRootCause) != "AskRootCause" {
		t.Fatalf("IntentName(AskRootCause) = %q", IntentName(AskRootCause))
	}
	if IntentName(Intent(99)) != "Unknown" {
		t.Fatalf("未知意图应为 Unknown")
	}
}

func TestHandleMultiIntentAggregation(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	now := time.Now()
	_ = s.SaveAlert(model.Alert{ID: "alarm-1", Device: "db-01", Metric: "disk_usage",
		Severity: "critical", FiredAt: now, CreatedAt: now})
	_ = s.SaveRun(model.PipelineRun{ID: "run-alarm-1", AlertID: "alarm-1", Status: model.StatusCompleted, Iteration: 0, MaxIteration: 2, CreatedAt: now, UpdatedAt: now})
	_ = s.SaveStage(model.StageResult{ID: "st1", RunID: "run-alarm-1", StageID: "rca", StageName: "根因分析",
		Status: model.StageCompleted, Output: `{"root_cause":"磁盘空间不足","evidence_chain":["e"],"confidence":0.8,"gaps":[]}`, Iteration: 0, StartedAt: now, EndedAt: now})
	light := &scriptedLLM{raw: `{"rewritten":"有哪些告警？alarm-1什么原因？","sub_questions":["有哪些告警","alarm-1什么原因"]}`}
	ag := NewWithLight(s, nil, light, nil, nil, 3, 3)
	msg, err := ag.Handle(context.Background(), "有哪些告警？alarm-1什么原因？")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(msg.Content, "[1]") || !strings.Contains(msg.Content, "success") {
		t.Fatalf("聚合回复缺分项状态: %q", msg.Content)
	}
	if !strings.Contains(msg.Content, "当前告警列表") {
		t.Fatalf("聚合回复缺告警列表: %q", msg.Content)
	}
	if !strings.Contains(msg.Content, "磁盘空间不足") {
		t.Fatalf("聚合回复缺根因: %q", msg.Content)
	}
}

func TestHandleUnknownGuidance(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	ag := New(s, nil, nil)
	msg, _ := ag.Handle(context.Background(), "今天天气如何？")
	if !strings.Contains(msg.Content, "重新描述你的问题") {
		t.Fatalf("Unknown 应输出新引导语, got %q", msg.Content)
	}
}

func TestHandleAggregationUnknownAndSkipped(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	now := time.Now()
	_ = s.SaveAlert(model.Alert{ID: "alarm-1", Device: "db-01", Metric: "disk_usage",
		Severity: "critical", FiredAt: now, CreatedAt: now})
	_ = s.SaveRun(model.PipelineRun{ID: "run-alarm-1", AlertID: "alarm-1", Status: model.StatusCompleted, Iteration: 0, MaxIteration: 2, CreatedAt: now, UpdatedAt: now})
	_ = s.SaveStage(model.StageResult{ID: "st1", RunID: "run-alarm-1", StageID: "rca", StageName: "根因分析",
		Status: model.StageCompleted, Output: `{"root_cause":"磁盘空间不足","evidence_chain":["e"],"confidence":0.8,"gaps":[]}`, Iteration: 0, StartedAt: now, EndedAt: now})
	// 重写拆分返回 4 个子问题（超出 maxSplit=3），其中 "随便说说" 无规则意图，
	// 触发轻量 LLM 分类：fake 返回原始 JSON，其 intent 字段为空 → Unknown。
	light := &scriptedLLM{raw: `{"rewritten":"有哪些告警？alarm-1什么原因？随便说说？还有别的？","sub_questions":["有哪些告警","alarm-1什么原因","随便说说","还有别的"]}`}
	ag := NewWithLight(s, nil, light, nil, nil, 3, 3)
	msg, err := ag.Handle(context.Background(), "有哪些告警？alarm-1什么原因？随便说说？还有别的？")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(msg.Content, "unknown") {
		t.Fatalf("Unknown 子问题应输出 unknown 状态行: %q", msg.Content)
	}
	if !strings.Contains(msg.Content, "skipped") {
		t.Fatalf("超限子问题应输出 skipped 状态行: %q", msg.Content)
	}
}

func TestNewBackwardCompat(t *testing.T) {
	ag := New(nil, nil, nil)
	if ag.maxSplit != 3 || ag.lightLLM != nil {
		t.Fatalf("New 应设 maxSplit=3 light=nil, got %+v", ag)
	}
	ag2 := NewWithContext(nil, nil, nil, nil, 3)
	if ag2.maxSplit != 3 {
		t.Fatalf("NewWithContext 应设 maxSplit=3, got %d", ag2.maxSplit)
	}
}
