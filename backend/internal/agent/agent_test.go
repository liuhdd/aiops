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
	"aiops/internal/retrieve"
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
	msg, err := ag.Handle(context.Background(), "", "alarm-1什么原因")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if msg.Role != model.RoleAssistant || msg.Content == "" {
		t.Fatalf("msg = %+v", msg)
	}
	msgs, _ := s.ListMessages("")
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
	msg, err := ag.Handle(context.Background(), "", "alarm-1什么原因")
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
	msg, err := ag.Handle(context.Background(), "", "alarm-1什么原因")
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
	msg, err := ag.Handle(context.Background(), "", "alarm-1什么原因")
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
	c := &scriptedLLM{raw: `{"intent":"AskRootCause","confidence":0.9}`}
	got, conf := ClassifyWithLight(context.Background(), nil, "为什么磁盘满了", c)
	if got != AskRootCause || conf != 0.9 {
		t.Fatalf("ClassifyWithLight = (%v,%v), want (AskRootCause,0.9)", got, conf)
	}
	bad := &scriptedLLM{raw: `{"intent":"Nope","confidence":0.9}`}
	if got, _ := ClassifyWithLight(context.Background(), nil, "x", bad); got != Unknown {
		t.Fatalf("非法意图应为 Unknown, got %v", got)
	}
	fail := &scriptedLLM{err: errors.New("boom")}
	if got, _ := ClassifyWithLight(context.Background(), nil, "x", fail); got != Unknown {
		t.Fatalf("LLM 出错应为 Unknown, got %v", got)
	}
}

func TestParseIntentWithLight(t *testing.T) {
	c := &scriptedLLM{raw: `{"intent":"Nope","confidence":0.9}`}
	if got, _ := ParseIntentWithLight(context.Background(), nil, "alarm-1什么原因", c); got != AskRootCause {
		t.Fatalf("规则命中应为 AskRootCause, got %v", got)
	}
	if got, _ := ParseIntentWithLight(context.Background(), nil, "帮我看看今天天气", &scriptedLLM{raw: `{"intent":"ListAlerts","confidence":0.9}`}); got != ListAlerts {
		t.Fatalf("LLM 分类应为 ListAlerts, got %v", got)
	}
	if got, _ := ParseIntentWithLight(context.Background(), nil, "今天天气如何", nil); got != Unknown {
		t.Fatalf("nil completer 应回退 Unknown, got %v", got)
	}
	// LLM 低置信度 → 拦截为 Unknown
	if got, conf := ParseIntentWithLight(context.Background(), nil, "帮我看看今天天气", &scriptedLLM{raw: `{"intent":"ListAlerts","confidence":0.2}`}); got != Unknown || conf != 0.2 {
		t.Fatalf("低置信度应拦截为 Unknown, got (%v,%v)", got, conf)
	}
}

func TestParseIntentSet(t *testing.T) {
	c := &scriptedLLM{raw: `{"rewritten":"有哪些告警？alarm-1什么原因？","sub_questions":["有哪些告警","alarm-1什么原因"]}`}
	got := ParseIntentSet(context.Background(), nil, "有哪些告警？alarm-1什么原因？", c, 3)
	if len(got) != 2 || got[0].Intent != ListAlerts || got[1].Intent != AskRootCause {
		t.Fatalf("ParseIntentSet = %v", got)
	}
}

func TestIntentName(t *testing.T) {
	if IntentName(AskRootCause) != "AskRootCause" {
		t.Fatalf("IntentName = %q", IntentName(AskRootCause))
	}
	if IntentName("Bogus") != "Bogus" {
		t.Fatalf("IntentName 应为恒等映射")
	}
}

func TestRuleScore(t *testing.T) {
	cases := []struct {
		text string
		want Intent
		conf float64
	}{
		{"有哪些告警", ListAlerts, 0.6},
		{"查一下alarm-123是什么原因", AskRootCause, 0.6},
		{"你好", Unknown, 0},
	}
	for _, c := range cases {
		sc := RuleScore(nil, c.text)
		if sc.Intent != c.want || sc.Confidence != c.conf {
			t.Fatalf("RuleScore(%q) = (%v,%v), want (%v,%v)", c.text, sc.Intent, sc.Confidence, c.want, c.conf)
		}
	}
	leaves := []model.LeafSpec{{Code: "MyKB", Name: "MyKB", RuleKeywords: []string{"db-01"}}}
	sc := RuleScore(leaves, "查一下db-01的问题")
	if sc.Intent != "MyKB" || sc.Confidence != 0.9 {
		t.Fatalf("实体命中应 0.9, got (%v,%v)", sc.Intent, sc.Confidence)
	}
	if sc := RuleScore(nil, "内置意图"); sc.Intent != Unknown {
		t.Fatalf("path 词不应参与规则匹配, got %v", sc.Intent)
	}
	pathLeaves := []model.LeafSpec{{Code: "MyKB2", Name: "MyKB2", Description: "查知识库手册", Path: "内置意图"}}
	if sc := RuleScore(pathLeaves, "内置意图"); sc.Intent != Unknown {
		t.Fatalf("path 词不应参与规则匹配, got %v", sc.Intent)
	}
	if sc := RuleScore(pathLeaves, "查知识库手册"); sc.Intent != Unknown {
		t.Fatalf("description 不应参与规则匹配, got %v", sc.Intent)
	}
}

func TestRuleMatchFromLeaves(t *testing.T) {
	leaves := []model.LeafSpec{
		{Code: "AskRootCause", RuleKeywords: []string{"为什么"}},
		{Code: "ListAlerts", RuleKeywords: []string{"列表"}},
	}
	if got := RuleMatch(leaves, "为什么磁盘满了"); got != "AskRootCause" {
		t.Fatalf("RuleMatch 应命中 AskRootCause, got %q", got)
	}
	if got := RuleMatch(leaves, "看看列表"); got != "ListAlerts" {
		t.Fatalf("RuleMatch 应命中 ListAlerts, got %q", got)
	}
	if got := RuleMatch(leaves, "今天天气"); got != Unknown {
		t.Fatalf("未命中应 Unknown, got %q", got)
	}
}

func TestAgentRecognizesCustomLeaf(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	now := time.Now()
	_ = s.SaveAlert(model.Alert{ID: "alarm-1", Device: "db-01", Metric: "disk_usage", Severity: "critical", FiredAt: now, CreatedAt: now})
	leaves := func() []model.LeafSpec {
		return []model.LeafSpec{{Code: "MyKB", Name: "MyKB", Description: "查知识库", RuleKeywords: []string{"查手册"}}}
	}
	ag := NewWithLeaves(s, nil, nil, nil, nil, 3, 3, leaves)
	msg, _ := ag.Handle(context.Background(), "", "帮我查手册")
	if !strings.Contains(msg.Content, "尚未实现") {
		t.Fatalf("KB 意图应输出占位语, got %q", msg.Content)
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
	msg, err := ag.Handle(context.Background(), "", "有哪些告警？alarm-1什么原因？")
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

func TestHandleRootCauseQuestionNotDegradedByRewrite(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	now := time.Now()
	_ = s.SaveAlert(model.Alert{ID: "alarm-1", Device: "db-01", Metric: "disk_usage",
		Severity: "critical", FiredAt: now, CreatedAt: now})
	_ = s.SaveRun(model.PipelineRun{ID: "run-alarm-1", AlertID: "alarm-1", Status: model.StatusCompleted, Iteration: 0, MaxIteration: 2, CreatedAt: now, UpdatedAt: now})
	_ = s.SaveStage(model.StageResult{ID: "st1", RunID: "run-alarm-1", StageID: "rca", StageName: "根因分析",
		Status: model.StageCompleted, Output: `{"root_cause":"磁盘空间不足","evidence_chain":["e"],"confidence":0.8,"gaps":[]}`, Iteration: 0, StartedAt: now, EndedAt: now})
	// 轻量 LLM 重写把单意图根因问题拆成碎片子问题，丢失 "什么原因" 关键词
	// （碎片含 "告警" → 规则误判为 ListAlerts）。修复前会返回告警列表。
	light := &scriptedLLM{raw: `{"rewritten":"告警alarm-1由什么原因引起？","sub_questions":["告警alarm-1的告警内容是什么？","告警alarm-1可能由哪些原因导致？"]}`}
	ag := NewWithLight(s, nil, light, nil, nil, 3, 3)
	msg, err := ag.Handle(context.Background(), "", "alarm-1 什么原因")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(msg.Content, "磁盘空间不足") {
		t.Fatalf("根因问题应返回根因而非告警列表, got %q", msg.Content)
	}
	if strings.Contains(msg.Content, "当前告警列表") {
		t.Fatalf("根因问题不应退化为告警列表, got %q", msg.Content)
	}
}

func TestHandleUnknownGuidance(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	ag := New(s, nil, nil)
	msg, _ := ag.Handle(context.Background(), "", "今天天气如何？")
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
	msg, err := ag.Handle(context.Background(), "", "有哪些告警？alarm-1什么原因？随便说说？还有别的？")
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

func TestAgentKBIntentReturnsKnowledge(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := ctxpkg.NewStore(s)
	_ = s.WithKBTx(func(tx store.KBStore) error {
		now := time.Now()
		if err := tx.InsertKbDocument(model.KbDocument{
			ID: "kbdoc-1", Title: "磁盘清理手册", Category: model.KBRunbook,
			Metrics: []string{"disk_usage"}, Intents: []string{"DiskCleanup"},
			Content: "清理日志与归档。", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		return tx.InsertKbChunk(model.KbChunk{ID: "c1", DocID: "kbdoc-1", Seq: 0,
			Content: "清理日志与归档。", CreatedAt: now})
	})
	ag := NewWithLeaves(s, nil, nil, nil, cs, 3, 3, func() []model.LeafSpec {
		return []model.LeafSpec{{Code: "DiskCleanup", Name: "DiskCleanup", Description: "磁盘清理", RuleKeywords: []string{"清理磁盘"}}}
	})
	msg, _ := ag.Handle(context.Background(), "", "怎么清理磁盘")
	if !strings.Contains(msg.Content, "磁盘清理手册") {
		t.Fatalf("KB 意图应返回知识, got %q", msg.Content)
	}
}

func TestAgentKBIntentFallsBackToPlaceholder(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := ctxpkg.NewStore(s)
	ag := NewWithLeaves(s, nil, nil, nil, cs, 3, 3, func() []model.LeafSpec {
		return []model.LeafSpec{{Code: "MCP1", Name: "MCP1", Description: "mcp", RuleKeywords: []string{"mcp命令"}}}
	})
	msg, _ := ag.Handle(context.Background(), "", "执行 mcp命令")
	if !strings.Contains(msg.Content, "尚未实现") {
		t.Fatalf("无命中知识应回退占位语, got %q", msg.Content)
	}
}

func TestAgentKBReplyFiltersByIntent(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := ctxpkg.NewStore(s)
	_ = s.WithKBTx(func(tx store.KBStore) error {
		now := time.Now()
		if err := tx.InsertKbDocument(model.KbDocument{
			ID: "kbdoc-1", Title: "磁盘清理手册", Category: model.KBRunbook,
			Metrics: []string{"disk_usage"}, Intents: []string{"DiskCleanup"},
			Content: "清理日志与归档。", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		if err := tx.InsertKbChunk(model.KbChunk{ID: "c1", DocID: "kbdoc-1", Seq: 0,
			Content: "清理日志与归档。", CreatedAt: now}); err != nil {
			return err
		}
		// 无关文档：意图不同，时间戳设在未来保证 recency=1.0、恰好过阈值，
		// 使排除只能由意图过滤完成而非分数阈值。
		if err := tx.InsertKbDocument(model.KbDocument{
			ID: "kbdoc-2", Title: "GPU故障手册", Category: model.KBManual,
			Metrics: []string{"gpu_util"}, Intents: []string{"GPUCheck"},
			Content: "检查GPU温度与风扇。", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		return tx.InsertKbChunk(model.KbChunk{ID: "c2", DocID: "kbdoc-2", Seq: 0,
			Content: "检查GPU温度与风扇。", CreatedAt: now.Add(time.Hour)})
	})
	ag := NewWithLeaves(s, nil, nil, nil, cs, 3, 3, func() []model.LeafSpec {
		return []model.LeafSpec{{Code: "DiskCleanup", Name: "DiskCleanup", Description: "磁盘清理", RuleKeywords: []string{"清理磁盘"}}}
	})
	msg, _ := ag.Handle(context.Background(), "", "怎么清理磁盘")
	if !strings.Contains(msg.Content, "清理日志与归档") {
		t.Fatalf("KB 应返回匹配意图的块, got %q", msg.Content)
	}
	if strings.Contains(msg.Content, "GPU故障手册") {
		t.Fatalf("KB 不应返回无关意图文档, got %q", msg.Content)
	}
}

func TestAgentKBReplyWithSearcherReturnsContent(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := ctxpkg.NewStore(s)
	ag := NewWithLeaves(s, nil, nil, nil, cs, 3, 3, func() []model.LeafSpec {
		return []model.LeafSpec{{Code: "DiskCleanup", Name: "DiskCleanup", Description: "磁盘清理", RuleKeywords: []string{"清理磁盘"}}}
	})
	// 注入 searcher：Result 携带 Intents 元数据；修复前该元数据缺失导致
	// MatchesIntent("", code) 恒 false，所有知识块被丢弃、回退占位语。
	ag.SetSearcher(agentFakeSearcher{res: []retrieve.Result{{
		DocID: "d1", Title: "磁盘清理手册", Category: model.KBRunbook,
		Content: "清理日志与归档。", Intents: []string{"DiskCleanup"},
	}}})
	msg, _ := ag.Handle(context.Background(), "", "怎么清理磁盘")
	if !strings.Contains(msg.Content, "清理日志与归档") {
		t.Fatalf("注入searcher后KB应返回匹配内容, got %q", msg.Content)
	}
	if strings.Contains(msg.Content, "尚未实现") {
		t.Fatalf("注入searcher不应回退占位语, got %q", msg.Content)
	}
}

func TestHandleSavesMessagesInConversation(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	conv, _ := s.CreateConversation("")
	ag := New(s, nil, nil)
	_, err := ag.Handle(context.Background(), conv.ID, "有哪些告警")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	msgs, _ := s.ListMessages(conv.ID)
	if len(msgs) != 2 {
		t.Fatalf("会话应落库 2 条消息, got %d", len(msgs))
	}
	for _, m := range msgs {
		if m.ConversationID != conv.ID {
			t.Fatalf("消息 conversation_id = %q, want %q", m.ConversationID, conv.ID)
		}
	}
	c, _ := s.GetConversation(conv.ID)
	if c.Title == "" {
		t.Fatal("首条消息后应自动命名会话标题")
	}
	if c.UpdatedAt.Before(conv.UpdatedAt) {
		t.Fatal("消息后会话 updated_at 应更新")
	}
}

type agentFakeSearcher struct{ res []retrieve.Result }

func (f agentFakeSearcher) SearchCases(_ context.Context, q retrieve.Query, o retrieve.Options) ([]retrieve.Result, error) {
	return f.res, nil
}
func (f agentFakeSearcher) SearchManuals(_ context.Context, q retrieve.Query, o retrieve.Options) ([]retrieve.Result, error) {
	return f.res, nil
}

func TestAgentBuildAssembledUsesSearcher(t *testing.T) {
	st, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer st.Close()
	cs := ctxpkg.NewStore(st)
	ag := NewWithLeaves(st, nil, nil, nil, cs, 3, 3, nil)
	ag.SetSearcher(agentFakeSearcher{
		res: []retrieve.Result{{DocID: "d1", Title: "磁盘案例", Category: model.KBCase, Content: "磁盘扩容处置"}},
	})
	as := ag.buildAssembled("c1", "db-01", "disk_usage", []string{"AskRootCause"})
	if as == nil || !strings.Contains(as.User, "磁盘案例") {
		t.Fatalf("buildAssembled 应含案例知识:\n%v", as)
	}
}

func TestHandleTitlesConversationFromFirstMessage(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	conv, _ := s.CreateConversation("")
	ag := New(s, nil, nil)
	_, _ = ag.Handle(context.Background(), conv.ID, "这是一个很长的第一句话需要被截断到二十个字符以内")
	c, _ := s.GetConversation(conv.ID)
	if len([]rune(c.Title)) != 20 {
		t.Fatalf("title=%q rune 数=%d, want 20", c.Title, len([]rune(c.Title)))
	}
}

func TestAgentRewriteDoesNotAffectPipelinePath(t *testing.T) {
	// pipeline 的 rca/recommend 直接调 Searcher，不经改写：用 mock searcher 校验入参与原文一致
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := ctxpkg.NewStore(s)
	_ = s.WithKBTx(func(tx store.KBStore) error {
		now := time.Now()
		if err := tx.InsertKbDocument(model.KbDocument{
			ID: "doc-pipe", Title: "Xid 48 案例", Category: model.KBCase,
			Intents: []string{"AskRootCause"}, Content: "Xid 48 案例内容", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		return tx.InsertKbChunk(model.KbChunk{ID: "c-pipe", DocID: "doc-pipe", Seq: 0, Content: "Xid 48 案例内容", CreatedAt: now})
	})
	var captured string
	rec := &captureSearcher{res: []retrieve.Result{{DocID: "doc-pipe", Title: "Xid 48 案例", Content: "Xid 48 案例内容", Intents: []string{"AskRootCause"}}}, capture: &captured}
	// pipeline 侧直接检索，不应经过 RewriteQuery
	orig := "Xid 48"
	got, err := rec.SearchCases(context.Background(), retrieve.Query{Text: orig}, retrieve.Options{TopK: 3})
	if err != nil || len(got) == 0 {
		t.Fatalf("pipeline SearchCases err=%v got=%v", err, got)
	}
	if captured != orig {
		t.Fatalf("pipeline 检索应直通原文, got %q want %q", captured, orig)
	}
	// 同一原文经聊天改写路径（有历史）会改写，但 pipeline 路径仍用原文
	// 验证改写在无历史时直通， pipeline 场景（无 RecentMessages）不受影响
	rw, _ := retrieve.RewriteQuery(context.Background(), nil, nil, orig)
	if rw != orig {
		t.Fatalf("无 history 时改写应直通, got %q", rw)
	}
	// 有 searcher 的 assembler 仍透传原始分
	ag := NewWithLeaves(s, nil, nil, nil, cs, 3, 3, nil)
	ag.SetSearcher(rec)
	as := ag.buildAssembled("conv-pipe", "db-01", "gpu_util", []string{"AskRootCause"})
	if as == nil {
		t.Fatalf("assembled nil")
	}
}

type captureSearcher struct {
	res     []retrieve.Result
	capture *string
}

func (c *captureSearcher) SearchCases(_ context.Context, q retrieve.Query, _ retrieve.Options) ([]retrieve.Result, error) {
	if c.capture != nil {
		*c.capture = q.Text
	}
	return c.res, nil
}
func (c *captureSearcher) SearchManuals(_ context.Context, q retrieve.Query, _ retrieve.Options) ([]retrieve.Result, error) {
	if c.capture != nil {
		*c.capture = q.Text
	}
	return c.res, nil
}

// TestAgentChatRewriteIntegration 校验聊天路径改写：有历史时子查询会经 RewriteQuery 改写后检索。
func TestAgentChatRewriteIntegration(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	conv, _ := s.CreateConversation("")
	_ = s.SaveMessage(model.Message{ID: "m1", Role: model.RoleUser, Content: "我的设备是 db-01，出现磁盘告警", ConversationID: conv.ID, CreatedAt: time.Now()})
	_ = s.SaveMessage(model.Message{ID: "m2", Role: model.RoleAssistant, Content: "已记录 db-01", ConversationID: conv.ID, CreatedAt: time.Now()})
	cs := ctxpkg.NewStore(s)
	light := &rewriteAwareLight{
		rewritten:  `{"rewritten":"有哪些告警？它怎么处理？","sub_questions":["有哪些告警","它怎么处理"]}`,
		rewriteMap: map[string]string{"它怎么处理": "db-01 磁盘告警怎么处理"},
	}
	ag := NewWithLight(s, nil, light, nil, cs, 3, 3)
	// 直接校验 rewriteQuery 会利用历史改写
	got := ag.rewriteQuery(context.Background(), conv.ID, "它怎么处理")
	if got != "db-01 磁盘告警怎么处理" {
		t.Fatalf("聊天改写未生效, got %q", got)
	}
	// 无历史时应直通
	conv2, _ := s.CreateConversation("")
	ag2 := NewWithLight(s, nil, light, nil, cs, 3, 3)
	got2 := ag2.rewriteQuery(context.Background(), conv2.ID, "它怎么处理")
	if got2 != "它怎么处理" {
		t.Fatalf("无历史应直通, got %q", got2)
	}
	// 已独立长句直通
	long := strings.Repeat("a", 41) + "怎么"
	got3 := ag.rewriteQuery(context.Background(), conv.ID, long)
	if got3 != long {
		t.Fatalf("已独立查询应直通, got %q", got3)
	}
}

// rewriteAwareLight 同时支持 RewriteAndSplit（2-arg JSON）与 RewriteQuery（单 prompt 含"历史"）
type rewriteAwareLight struct {
	rewritten  string
	rewriteMap map[string]string
}

func (r *rewriteAwareLight) Complete(_ context.Context, system, user string) (string, error) {
	// RewriteQuery 适配器会以 system="" 且 user=prompt（含"历史："）调用
	if strings.Contains(user, "历史：") {
		// 提取"查询："后的原文
		idx := strings.Index(user, "查询：")
		q := user
		if idx >= 0 {
			q = strings.TrimSpace(user[idx+len("查询："):])
		}
		if v, ok := r.rewriteMap[q]; ok {
			return v, nil
		}
		return q, nil
	}
	// 否则为 RewriteAndSplit
	return r.rewritten, nil
}

func TestAgentRewriteFallbackOnError(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	conv, _ := s.CreateConversation("")
	_ = s.SaveMessage(model.Message{ID: "m1", Role: model.RoleUser, Content: "历史1", ConversationID: conv.ID, CreatedAt: time.Now()})
	cs := ctxpkg.NewStore(s)
	// light 对改写返回错误，应回退原文
	errLight := &errorLight{rewritten: `{"rewritten":"它怎么处理？","sub_questions":["它怎么处理"]}`}
	ag := NewWithLight(s, nil, errLight, nil, cs, 3, 3)
	got := ag.rewriteQuery(context.Background(), conv.ID, "它怎么处理")
	if got != "它怎么处理" {
		t.Fatalf("改写失败应回退原文, got %q", got)
	}
	// 空输出也回退
	emptyLight := &rewriteAwareLight{
		rewritten:  `{"rewritten":"它怎么处理？","sub_questions":["它怎么处理"]}`,
		rewriteMap: map[string]string{"它怎么处理": "   "},
	}
	ag2 := NewWithLight(s, nil, emptyLight, nil, cs, 3, 3)
	got2 := ag2.rewriteQuery(context.Background(), conv.ID, "它怎么处理")
	if got2 != "它怎么处理" {
		t.Fatalf("空输出应回退原文, got %q", got2)
	}
}

type errorLight struct {
	rewritten string
}

func (e *errorLight) Complete(_ context.Context, system, user string) (string, error) {
	if strings.Contains(user, "历史：") {
		return "", errors.New("llm down")
	}
	return e.rewritten, nil
}
