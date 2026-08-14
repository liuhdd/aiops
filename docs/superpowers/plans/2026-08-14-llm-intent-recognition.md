# LLM 意图识别（多意图重写拆分+轻模型）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将聊天 Agent 的意图识别从纯规则升级为"轻量 LLM 一次调用重写+拆分多意图 → 逐子问题规则意图优先、Unknown 用轻量 LLM 分类 → 串行处理记录状态 → 聚合回复"，无 LLM 时回退现有单意图规则路径，并同步 eval 评测体系。

**Architecture:** 在 `internal/agent` 增加 `RewriteAndSplit`/`ParseIntentWithLight`/`classifyWithLight`/`ParseIntentSet` 四个函数与 `IntentName`；`Agent` 增加 `lightLLM`（独立轻模型 completer）与 `maxSplit` 字段，`Handle` 拆分为"单意图规则路径（兜底）"与"多意图聚合路径"。新增 `llm.NewLightClient` 构造轻量客户端；config 新增 `LLM_LIGHT_BASE_URL`/`LLM_LIGHT_MODEL`/`INTENT_MAX_SPLIT`。eval 的 `Expected` 增加 `Intents`，runner/grader 与运行时共用同一解析器。

**Tech Stack:** Go 1.24，复用现有 `ctxpkg.Completer`/`llm.ParseJSON`/eino LLM client。不引入新的第三方依赖。注释与 commit message 用中文。

## Global Constraints

- Go 模块 `aiops`，包路径以 `aiops/internal/...` 开头。
- SQLite 单写：`store.New` 已设 `SetMaxOpenConns(1)`，测试用 `filepath.Join(t.TempDir(), "*.db")`，用完 `s.Close()`。
- `mockdata.GenerateAll(seed)` 进程内一次性生成，测试须先调用。
- 无 `LLM_API_KEY` 时 LLM 类配置跳过，rule 路径仍可跑（CI 护栏 `TestRuleRegression*` 保持通过）。
- 提交信息用 conventional prefixes（`feat:`/`test:`/`docs:`/`chore:`）。
- 测试命令：`go test ./internal/agent/ ./internal/eval/ ./internal/api/`；全量：`go test ./...`。

---

### Task 1: config 新增轻量模型与拆分上限配置

**Files:**
- Modify: `backend/internal/config/config.go:16-66`
- Test: `backend/internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Config` 新增字段 `LLMLightBaseURL string`、`LLMLightModel string`、`MaxSplit int`
- Consumes: 无（独立于其他任务）

- [ ] **Step 1: 写失败测试（追加到 `config_test.go`）**

```go
func TestLoadFromEnvLightIntentDefaults(t *testing.T) {
	env := map[string]string{"LLM_BASE_URL": "https://main.example.com/v1", "LLM_MODEL": "main-model"}
	cfg := LoadFromEnv(func(k string) string { return env[k] })
	if cfg.LLMLightBaseURL != "https://main.example.com/v1" {
		t.Fatalf("LLMLightBaseURL = %q, want 同 LLM_BASE_URL", cfg.LLMLightBaseURL)
	}
	if cfg.LLMLightModel != "main-model" {
		t.Fatalf("LLMLightModel = %q, want 同 LLM_MODEL", cfg.LLMLightModel)
	}
	if cfg.MaxSplit != 3 {
		t.Fatalf("MaxSplit = %d, want 3", cfg.MaxSplit)
	}
}

func TestLoadFromEnvLightIntentOverride(t *testing.T) {
	env := map[string]string{
		"LLM_BASE_URL":     "https://main.example.com/v1",
		"LLM_MODEL":        "main-model",
		"LLM_LIGHT_BASE_URL": "https://light.example.com/v1",
		"LLM_LIGHT_MODEL":  "light-model",
		"INTENT_MAX_SPLIT": "5",
	}
	cfg := LoadFromEnv(func(k string) string { return env[k] })
	if cfg.LLMLightBaseURL != "https://light.example.com/v1" {
		t.Fatalf("LLMLightBaseURL = %q, want light url", cfg.LLMLightBaseURL)
	}
	if cfg.LLMLightModel != "light-model" {
		t.Fatalf("LLMLightModel = %q, want light-model", cfg.LLMLightModel)
	}
	if cfg.MaxSplit != 5 {
		t.Fatalf("MaxSplit = %d, want 5", cfg.MaxSplit)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/config/ -run TestLoadFromEnvLight -v`
Expected: FAIL——`cfg.LLMLightBaseURL` 等字段不存在，编译错误 `unknown field`。

- [ ] **Step 3: 写实现（修改 `config.go`）**

在 `Config` 结构体追加字段：

```go
	React       bool // CONTEXT_REACT，ReAct 循环开关，默认 true
	TopK        int  // CONTEXT_TOP_K，每源上下文条数，默认 3
	LLMLightBaseURL string // LLM_LIGHT_BASE_URL，轻量意图模型 BaseURL，默认同 LLM_BASE_URL
	LLMLightModel   string // LLM_LIGHT_MODEL，轻量意图模型，默认同 LLM_MODEL
	MaxSplit        int    // INTENT_MAX_SPLIT，单条输入多意图拆分上限，默认 3
```

在 `LoadFromEnv` 的 `return Config{...}` 前追加：

```go
	lightBase := lookup("LLM_LIGHT_BASE_URL")
	if lightBase == "" {
		lightBase = base
	}
	lightModel := lookup("LLM_LIGHT_MODEL")
	if lightModel == "" {
		lightModel = model
	}
	maxSplit := 3
	if v := lookup("INTENT_MAX_SPLIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxSplit = n
		}
	}
```

`return Config{...}` 补三个字段：`LLMLightBaseURL: lightBase, LLMLightModel: lightModel, MaxSplit: maxSplit`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/config/ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/config/config.go backend/internal/config/config_test.go
git commit -m "feat: config新增轻量意图模型与INTENT_MAX_SPLIT配置"
```

---

### Task 2: llm.NewLightClient 轻量客户端

**Files:**
- Modify: `backend/internal/llm/llm.go:17-47`
- Test: `backend/internal/llm/llm_test.go`

**Interfaces:**
- Consumes: `config.Config`（Task 1 的 `LLMLightBaseURL`/`LLMLightModel`）
- Produces: `func NewLightClient(cfg config.Config) (*Client, error)`；无 key 或未配置轻量模型时返回 `ErrNoAPIKey`

- [ ] **Step 1: 写失败测试（追加到 `llm_test.go`）**

```go
func TestNewLightClientNoKey(t *testing.T) {
	cfg := config.Config{LLMAPIKey: ""}
	c, err := NewLightClient(cfg)
	if err == nil || c != nil {
		t.Fatalf("无 key 应返回 nil+err, got c=%v err=%v", c, err)
	}
}

func TestNewLightClientDefaultsToMain(t *testing.T) {
	cfg := config.Config{
		LLMAPIKey: "sk-test", LLMBaseURL: "https://main.example.com/v1", LLMModel: "main-model",
	}
	c, err := NewLightClient(cfg)
	if err != nil {
		t.Fatalf("NewLightClient: %v", err)
	}
	if c == nil {
		t.Fatal("有 key 应返回非 nil client")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/llm/ -run TestNewLightClient -v`
Expected: FAIL——`undefined: NewLightClient`。

- [ ] **Step 3: 写实现（修改 `llm.go`，在 `NewClient` 之后追加）**

```go
// NewLightClient 构造轻量意图模型客户端；未配置 key 或未配置轻量模型时返回 ErrNoAPIKey。
func NewLightClient(cfg config.Config) (*Client, error) {
	if cfg.LLMAPIKey == "" {
		return nil, ErrNoAPIKey
	}
	cm, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL: cfg.LLMLightBaseURL,
		APIKey:  cfg.LLMAPIKey,
		Model:   cfg.LLMLightModel,
	})
	if err != nil {
		return nil, fmt.Errorf("new light chat model: %w", err)
	}
	return &Client{model: cm}, nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/llm/ -v`
Expected: 全部 PASS（`TestNewLightClientDefaultsToMain` 只验证构造成功，不发起网络请求）。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/llm/llm.go backend/internal/llm/llm_test.go
git commit -m "feat: llm.NewLightClient构造轻量意图模型客户端"
```

---

### Task 3: agent 意图解析新增 LLM 重写拆分与分类

**Files:**
- Modify: `backend/internal/agent/intent.go:1-37`
- Test: `backend/internal/agent/agent_test.go`

**Interfaces:**
- Consumes: `ctxpkg.Completer`、`ctxpkg.IsNilCompleter`、`llm.ParseJSON`
- Produces:
  - `func RewriteAndSplit(ctx context.Context, text string, c ctxpkg.Completer) (rewritten string, subs []string, err error)`
  - `func ParseIntentWithLight(ctx context.Context, text string, c ctxpkg.Completer) Intent`
  - `func ParseIntentSet(ctx context.Context, text string, c ctxpkg.Completer, maxSplit int) []Intent`
  - `func IntentName(i Intent) string`
  - 私有 `classifyWithLight(ctx context.Context, text string, c ctxpkg.Completer) Intent`
- 依赖 Task 4 之前完成（`Agent.Handle` 与 eval 均调用这些函数）

- [ ] **Step 1: 写失败测试（追加到 `agent_test.go`）**

```go
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
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent/ -run 'TestRewriteAndSplit|TestClassifyWithLight|TestParseIntentWithLight|TestParseIntentSet|TestIntentName' -v`
Expected: FAIL——`undefined: RewriteAndSplit` 等。注意 `scriptedLLM` 需在 `fakeChatLLM` 定义旁追加（同文件）。

- [ ] **Step 3: 写实现（修改 `intent.go`）**

文件头新增 import：

```go
import (
	"context"
	"errors"
	"regexp"
	"strings"

	ctxpkg "aiops/internal/context"
	"aiops/internal/llm"
)
```

在文件末尾追加：

```go
// RewriteAndSplit 轻量 LLM 一次调用完成问题重写与多意图拆分。
// 返回 rewritten（规范化完整问题）与 subs（拆分出的子问题列表）；LLM 不可用/失败返回 err。
func RewriteAndSplit(ctx context.Context, text string, c ctxpkg.Completer) (rewritten string, subs []string, err error) {
	system := "你是 AIOps 运维助手的问题理解模块。把用户提问重写为规范、完整的中文问题，并拆分为相互独立的子问题。只输出 JSON：{\"rewritten\":\"<重写后的完整问题>\",\"sub_questions\":[\"<子问题1>\",\"<子问题2>\"]}。若只有一个意图，sub_questions 只含一项。子问题必须完整自包含（补全指代、告警ID、设备名等）。"
	raw, cerr := c.Complete(ctx, system, text)
	if cerr != nil || strings.TrimSpace(raw) == "" {
		return "", nil, cerr
	}
	var out struct {
		Rewritten string   `json:"rewritten"`
		Subs      []string `json:"sub_questions"`
	}
	if err := llm.ParseJSON(raw, &out); err != nil {
		return "", nil, err
	}
	if out.Rewritten == "" || len(out.Subs) == 0 {
		return "", nil, errors.New("rewrite: 空结果")
	}
	return out.Rewritten, out.Subs, nil
}

// classifyWithLight 轻量 LLM 分类为 6 类之一；失败/非法映射 Unknown。
func classifyWithLight(ctx context.Context, text string, c ctxpkg.Completer) Intent {
	system := "你是意图分类器。判断用户请求属于哪个意图，只输出 JSON：{\"intent\":\"<意图名>\"}。意图名必须是 ListAlerts(查看告警列表)、InspectAlert(排查某个告警)、AskRootCause(询问根因)、AskRecommendation(询问处置建议)、AskStatus(询问状态/进度)、Unknown(与告警运维无关)。无法确定时返回 Unknown。"
	raw, err := c.Complete(ctx, system, text)
	if err != nil || strings.TrimSpace(raw) == "" {
		return Unknown
	}
	var out struct {
		Intent string `json:"intent"`
	}
	if err := llm.ParseJSON(raw, &out); err != nil {
		return Unknown
	}
	switch out.Intent {
	case "ListAlerts":
		return ListAlerts
	case "InspectAlert":
		return InspectAlert
	case "AskRootCause":
		return AskRootCause
	case "AskRecommendation":
		return AskRecommendation
	case "AskStatus":
		return AskStatus
	}
	return Unknown
}

// ParseIntentWithLight 规则优先；规则判 Unknown 时用轻量 LLM 分类；LLM 不可用/失败回退 Unknown。
func ParseIntentWithLight(ctx context.Context, text string, c ctxpkg.Completer) Intent {
	if intent := ParseIntent(text); intent != Unknown {
		return intent
	}
	if c == nil || ctxpkg.IsNilCompleter(c) {
		return Unknown
	}
	return classifyWithLight(ctx, text, c)
}

// ParseIntentSet 解析一条输入的全部意图：LLM 重写+拆分 → 逐子问题 ParseIntentWithLight，截断到 maxSplit。
// LLM 不可用或拆分失败时回退为单意图规则 ParseIntent(text)。
func ParseIntentSet(ctx context.Context, text string, c ctxpkg.Completer, maxSplit int) []Intent {
	if c == nil || ctxpkg.IsNilCompleter(c) {
		return []Intent{ParseIntent(text)}
	}
	_, subs, err := RewriteAndSplit(ctx, text, c)
	if err != nil || len(subs) == 0 {
		return []Intent{ParseIntent(text)}
	}
	if len(subs) > maxSplit {
		subs = subs[:maxSplit]
	}
	out := make([]Intent, 0, len(subs))
	for _, s := range subs {
		out = append(out, ParseIntentWithLight(ctx, s, c))
	}
	return out
}

// IntentName 返回意图的字符串名；未知值返回 "Unknown"。
func IntentName(i Intent) string {
	switch i {
	case ListAlerts:
		return "ListAlerts"
	case InspectAlert:
		return "InspectAlert"
	case AskRootCause:
		return "AskRootCause"
	case AskRecommendation:
		return "AskRecommendation"
	case AskStatus:
		return "AskStatus"
	}
	return "Unknown"
}
```

`intent.go` 头部需补 `"errors"` import（`RewriteAndSplit` 中 `errors.New`）。`Intent` 常量保持原 iota 顺序不变。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agent/ -run 'TestRewriteAndSplit|TestClassifyWithLight|TestParseIntentWithLight|TestParseIntentSet|TestIntentName|TestParseIntent|TestExtractAlertIDHex' -v`
Expected: 全部 PASS（既有 `TestParseIntent`/`TestExtractAlertIDHex` 不受影响）。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/agent/intent.go backend/internal/agent/agent_test.go
git commit -m "feat: agent意图解析支持LLM重写拆分与轻量分类"
```

---

### Task 4: Agent.Handle 多意图聚合重构 + 增强引导语

**Files:**
- Modify: `backend/internal/agent/agent.go:17-120`
- Test: `backend/internal/agent/agent_test.go`

**Interfaces:**
- Consumes: `RewriteAndSplit`/`ParseIntentWithLight`/`ParseIntent`/`IntentName`（Task 3）、`cfg` 的 light 字段（Task 1）
- Produces:
  - `type Agent` 新增字段 `lightLLM ctxpkg.Completer`、`maxSplit int`
  - `func NewWithLight(s *store.Store, lc, light ctxpkg.Completer, eng *pipeline.Engine, cs *ctxpkg.Store, topK, maxSplit int) *Agent`
  - `New(s, lc, eng)` 与 `NewWithContext(s, lc, eng, cs, topK)` 签名**保持不变**，内部走 `NewWithLight`
  - `Handle` 行为：有 light 时多意图聚合；无 light 时单意图规则（新增 Unknown 引导语）

- [ ] **Step 1: 写失败测试（追加到 `agent_test.go`）**

```go
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
	if !strings.Contains(msg.Content, "AIOps运维助手") {
		t.Fatalf("Unknown 应输出引导语, got %q", msg.Content)
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
```

注意：`New(nil, nil, nil)` 中 store 为 nil 是合法的——`Handle` 未调用时不会访问 store；测试只检查构造字段。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent/ -run 'TestHandleMultiIntent|TestHandleUnknownGuidance|TestNewBackwardCompat' -v`
Expected: FAIL——`undefined: NewWithLight`；`TestHandleUnknownGuidance` 因引导语未实现而失败。

- [ ] **Step 3: 写实现（修改 `agent.go`）**

`Agent` 结构体追加字段：

```go
type Agent struct {
	store    *store.Store
	llm      ctxpkg.Completer
	lightLLM ctxpkg.Completer // 轻量意图模型（重写+拆分+分类）
	maxSplit int              // 多意图拆分上限
	eng      *pipeline.Engine
	ctxStore *ctxpkg.Store
	topK     int
}
```

构造器重写：

```go
func New(s *store.Store, lc ctxpkg.Completer, eng *pipeline.Engine) *Agent {
	return NewWithLight(s, lc, lc, eng, nil, 3, 3)
}

func NewWithContext(s *store.Store, lc ctxpkg.Completer, eng *pipeline.Engine, cs *ctxpkg.Store, topK int) *Agent {
	return NewWithLight(s, lc, lc, eng, cs, topK, 3)
}

func NewWithLight(s *store.Store, lc, light ctxpkg.Completer, eng *pipeline.Engine, cs *ctxpkg.Store, topK, maxSplit int) *Agent {
	return &Agent{store: s, llm: lc, lightLLM: light, maxSplit: maxSplit, eng: eng, ctxStore: cs, topK: topK}
}
```

`Handle` 重写：

```go
func (a *Agent) Handle(ctx context.Context, userText string) (model.Message, error) {
	userMsg := model.Message{
		ID: model.NewID("msg"), Role: model.RoleUser,
		Content: userText, CreatedAt: time.Now(),
	}
	_ = a.store.SaveMessage(userMsg)

	reply := a.answer(ctx, userText)

	assistantMsg := model.Message{
		ID: model.NewID("msg"), Role: model.RoleAssistant,
		Content: reply, CreatedAt: time.Now(),
	}
	if alertID := extractAlertID(userText, a.store); alertID != "" {
		assistantMsg.AlertID = alertID
	}
	_ = a.store.SaveMessage(assistantMsg)
	return assistantMsg, nil
}

// answer 多意图优先：有轻量 LLM 时重写拆分后逐子问题聚合；否则回退单意图规则路径。
func (a *Agent) answer(ctx context.Context, userText string) string {
	if a.lightLLM == nil || ctxpkg.IsNilCompleter(a.lightLLM) {
		return a.answerSingle(ctx, userText)
	}
	_, subs, err := RewriteAndSplit(ctx, userText, a.lightLLM)
	if err != nil || len(subs) == 0 {
		return a.answerSingle(ctx, userText)
	}
	if len(subs) == 1 {
		return a.answerOne(ctx, subs[0])
	}
	limit := a.maxSplit
	if limit <= 0 {
		limit = 3
	}
	var b strings.Builder
	for i, sub := range subs {
		if i >= limit {
			fmt.Fprintf(&b, "[%d] %s → skipped（超出上限 %d）\n", i+1, sub, limit)
			continue
		}
		content, status := a.answerOneStatus(ctx, sub)
		fmt.Fprintf(&b, "[%d] %s → %s\n%s\n", i+1, sub, status, content)
	}
	return strings.TrimRight(b.String(), "\n")
}

// answerSingle 无轻量 LLM 时的单意图规则路径（原 Handle 逻辑）。
func (a *Agent) answerSingle(ctx context.Context, userText string) string {
	alertID := extractAlertID(userText, a.store)
	switch ParseIntent(userText) {
	case ListAlerts:
		return a.listAlertsReply()
	case InspectAlert, AskRootCause, AskRecommendation:
		if alertID == "" {
			return "请指明告警ID，例如：alarm-123 什么原因？"
		}
		alert, err := a.store.GetAlert(alertID)
		if err != nil {
			return fmt.Sprintf("未找到告警 %s。", alertID)
		}
		if a.eng != nil {
			if _, runErr := a.store.GetRun("run-" + alertID); runErr != nil {
				_ = a.eng.Run(ctx, alert, nil)
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !ctxpkg.IsNilCompleter(a.llm) {
			return a.answerWithLLM(ctx, alertID, ParseIntent(userText), userText)
		}
		return a.answerFor(alertID, ParseIntent(userText))
	case AskStatus:
		return "告警状态可在左侧列表查看，点击告警查看排查时间线。"
	default:
		return a.guideText()
	}
}

// answerOne 单子问题的回复（不带头部状态行）。
func (a *Agent) answerOne(ctx context.Context, sub string) string {
	content, _ := a.answerOneStatus(ctx, sub)
	return content
}

// answerOneStatus 单个子问题：规则意图优先，Unknown 用轻量 LLM 分类；返回内容与状态。
func (a *Agent) answerOneStatus(ctx context.Context, sub string) (string, string) {
	intent := ParseIntentWithLight(ctx, sub, a.lightLLM)
	switch intent {
	case ListAlerts:
		return a.listAlertsReply(), "success"
	case AskStatus:
		return "告警状态可在左侧列表查看，点击告警查看排查时间线。", "success"
	case InspectAlert, AskRootCause, AskRecommendation:
		alertID := extractAlertID(sub, a.store)
		if alertID == "" {
			return "请指明告警ID，例如：alarm-123 什么原因？", "no_alert"
		}
		alert, err := a.store.GetAlert(alertID)
		if err != nil {
			return fmt.Sprintf("未找到告警 %s。", alertID), "no_alert"
		}
		if a.eng != nil {
			if _, runErr := a.store.GetRun("run-" + alertID); runErr != nil {
				_ = a.eng.Run(ctx, alert, nil)
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !ctxpkg.IsNilCompleter(a.llm) {
			return a.answerWithLLM(ctx, alertID, intent, sub), "success"
		}
		return a.answerFor(alertID, intent), "success"
	default:
		return a.guideText(), "unknown"
	}
}

func (a *Agent) listAlertsReply() string {
	alerts, _ := a.store.ListAlerts()
	if len(alerts) == 0 {
		return "当前没有告警。"
	}
	var b strings.Builder
	b.WriteString("当前告警列表：\n")
	for _, al := range alerts {
		fmt.Fprintf(&b, "- %s [%s] %s %s\n", al.ID, al.Severity, al.Device, al.Message)
	}
	return b.String()
}

// guideText Unknown 意图的增强引导语。
func (a *Agent) guideText() string {
	return "你好，我是AIOps运维助手。我可以帮你：\n- 查看告警列表：如\"有哪些告警\"\n- 排查单个告警：如\"查一下 alarm-123 什么原因\"\n- 获取处置建议：如\"alarm-123 怎么处理\"\n- 查看排查进度：如\"排查进度如何\"\n如果输入和以上无关，请重新描述你的问题。"
}
```

删除原 `Handle` 中重复的告警列表内联逻辑（已收敛到 `listAlertsReply`）。`answerFor`/`answerWithLLM`/`memorySuffix`/`extractAlertID`/`extractQuery` 保持不变。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agent/ -v`
Expected: 全部 PASS，包括既有 `TestAgentInspectAlertTriggersRun`/`TestAgentRuleAnswerIncludesLongTermMemory`/`TestAgentLLMAnswerUsesAssembledContext`/`TestAgentLLMFallsBackToRule`。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/agent/agent.go backend/internal/agent/agent_test.go
git commit -m "feat: Agent.Handle多意图重写拆分聚合回复与Unknown引导语"
```

---

### Task 5: main.go 装配轻量模型

**Files:**
- Modify: `backend/cmd/server/main.go:25-48`

**Interfaces:**
- Consumes: `llm.NewLightClient`（Task 2）、`agent.NewWithLight`（Task 4）、`config.Config` 新字段（Task 1）

- [ ] **Step 1: 修改 `main.go`**

在 `NewClient` 之后追加轻量客户端：

```go
	light, err := llm.NewLightClient(cfg)
	if err != nil {
		log.Printf("light llm unavailable (intent fallback to rules): %v", err)
	}
```

Agent 构造改为：

```go
	ag := agent.NewWithLight(st, lc, light, eng, cs, cfg.TopK, cfg.MaxSplit)
```

- [ ] **Step 2: 验证编译**

Run: `go build ./...`
Expected: 无编译错误。

- [ ] **Step 3: 运行全量测试**

Run: `go test ./internal/agent/ ./internal/api/`
Expected: 全部 PASS（`api/server_test.go` 用 `agent.New(s, nil, nil)`，签名未变）。

- [ ] **Step 4: 提交**

```bash
git add backend/cmd/server/main.go
git commit -m "feat: main装配轻量意图模型客户端"
```

---

### Task 6: eval 适配多意图评测

**Files:**
- Modify: `backend/internal/eval/task.go:34-41,114-153`（`Expected` 增 `Intents`、新增 `chat-multi` 任务、`ValidateTasks`）
- Modify: `backend/internal/eval/runner.go:128-171`（`runChat` 注入 LLM、记录 `res.Intents`）
- Modify: `backend/internal/eval/grader.go:150-173`（`gradeChat` 用子意图集合）
- Modify: `backend/internal/eval/helpers.go:24-41`（`intentNames` 辅助）
- Test: `backend/internal/eval/runner_test.go`、`backend/internal/eval/grader_test.go`

**Interfaces:**
- Consumes: `agent.NewWithLight`/`agent.ParseIntentSet`/`agent.IntentName`（Tasks 3-4）
- Produces: `Expected.Intents []string`、`TrialResult.Intents []string`、`chat-multi` 任务

- [ ] **Step 1: 写失败测试**

`grader_test.go` 追加：

```go
func TestGradeChatMultiIntent(t *testing.T) {
	task, _ := TaskByID("chat-multi")
	if len(task.Expected.Intents) != 2 {
		t.Fatalf("chat-multi 应期望 2 个意图, got %v", task.Expected.Intents)
	}
	res := &TrialResult{Intents: []string{"ListAlerts", "AskRootCause"}, Reply: "多意图聚合回复"}
	scores := gradeChat(task, res)
	found := map[string]GraderResult{}
	for _, s := range scores {
		found[s.Name] = s
	}
	if !found["IntentMatch"].Pass {
		t.Fatalf("多意图全命中应通过: %+v", found["IntentMatch"])
	}
	// 缺一个意图 → 不通过
	res2 := &TrialResult{Intents: []string{"ListAlerts"}}
	found2 := map[string]GraderResult{}
	for _, s := range gradeChat(task, res2) {
		found2[s.Name] = s
	}
	if found2["IntentMatch"].Pass {
		t.Fatalf("缺意图不应通过: %+v", found2["IntentMatch"])
	}
}
```

`runner_test.go` 追加：

```go
func TestRunTaskChatMultiRuleFallback(t *testing.T) {
	mockdata.GenerateAll(42)
	task, _ := TaskByID("chat-multi")
	// rule 配置（无 LLM）下回退单意图规则解析
	res, err := RunTask(context.Background(), task, Config{Name: ConfigRule}, nil, 1)
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if len(res.Intents) == 0 {
		t.Fatal("res.Intents 不应为空")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/eval/ -run 'TestGradeChatMultiIntent|TestRunTaskChatMulti' -v`
Expected: FAIL——`undefined: TaskByID("chat-multi")` 或 `Expected.Intents` 字段不存在。

- [ ] **Step 3: 写实现**

`task.go` 的 `Expected` 增加字段：

```go
type Expected struct {
	RootCause []string // 期望根因关键词（任一命中即通过）
	Logs      []string // 期望日志关键字
	NoGaps    bool     // 期望证据/根因无 gaps
	Runbook   string   // 期望 runbook_ref
	Intent    string   // chat: 期望意图（单意图）
	Intents   []string // chat: 期望子意图集合（多意图，全部命中才过）
	ToolUse   []string // context: 期望调用的工具
}
```

`DefaultTasks` 中 `chat-irrelevant` 之后追加：

```go
		{
			ID: "chat-multi", Object: ObjectChat,
			ChatInput: "有哪些告警？alarm-1 什么原因？",
			Expected:  Expected{Intents: []string{"ListAlerts", "AskRootCause"}},
			Configs:   []string{ConfigLLMSingle, ConfigLLMReact},
		},
```

`ValidateTasks` 的 chat 校验改为（Intent 或 Intents 任一非空）：

```go
		if t.Object == ObjectChat {
			if t.ChatInput == "" {
				return fmt.Errorf("chat 任务 %s 缺 ChatInput", t.ID)
			}
			if t.Expected.Intent == "" && len(t.Expected.Intents) == 0 {
				return fmt.Errorf("chat 任务 %s 缺期望 Intent/Intents", t.ID)
			}
			continue
		}
```

`runner.go` 的 `TrialResult` 增加字段：

```go
type TrialResult struct {
	Trial     int
	Config    string
	RunStatus string
	ErrMsg    string `json:"err_msg,omitempty"`
	Intent    string
	Intents   []string `json:"intents,omitempty"`
	Reply     string
	Outcome   RunOutcome
}
```

`runChat` 重写为（注入 LLM 对齐生产 + 记录子意图集合）：

```go
func runChat(ctx context.Context, st *store.Store, eng *pipeline.Engine, t Task, cfg Config, lc *llm.Client, cs *ctxpkg.Store, res *TrialResult) (*TrialResult, error) {
	// 需要 alert 供流水线触发；chat 任务若引用设备，预先写入一条告警
	if _, err := ensureAlertForChat(st, t); err != nil {
		res.RunStatus = "chat-failed"
		res.ErrMsg = err.Error()
		return res, err
	}
	var ag *agent.Agent
	if cfg.LLM && !isNilClient(lc) {
		// 对齐生产：注入主 client（eval 中复用为轻模型），Handle 走多意图路径
		ag = agent.NewWithLight(st, lc, lc, eng, cs, cfg.TopK, 3)
	} else {
		ag = agent.New(st, nil, eng)
	}
	msg, err := ag.Handle(ctx, t.ChatInput)
	if err != nil {
		res.RunStatus = "chat-failed"
		res.ErrMsg = err.Error()
		return res, err
	}
	res.Reply = msg.Content
	res.RunStatus = "chat"
	var light ctxpkg.Completer
	if cfg.LLM && !isNilClient(lc) {
		light = lc
	}
	res.Intents = intentNames(agent.ParseIntentSet(ctx, t.ChatInput, light, 3))
	if len(res.Intents) > 0 {
		res.Intent = res.Intents[0]
	}
	return res, nil
}
```

`RunTask` 中调用 `runChat` 的语句改为传 `cfg`/`lc`/`cs`：

```go
	case ObjectChat:
		return runChat(ctx, st, eng, t, cfg, lc, cs, res)
```

（`lc` 已是 `*llm.Client`，`cs` 已在 RunTask 内构造。原 `runChat` 内 `res.Intent = intentName(...)` 一行移除，`intentName` 函数保留或随 `IntentName` 收敛。）

`helpers.go` 追加：

```go
// intentNames 将子意图集合转为字符串名（与运行时同一命名）。
func intentNames(its []agent.Intent) []string {
	out := make([]string, 0, len(its))
	for _, i := range its {
		out = append(out, agent.IntentName(i))
	}
	return out
}
```

`grader.go` 的 `gradeChat` 重写：

```go
func gradeChat(t Task, res *TrialResult) []GraderResult {
	got := res.Intents
	if len(got) == 0 {
		got = []string{res.Intent}
	}
	ok := false
	if len(t.Expected.Intents) > 0 {
		ok = containsAll(got, t.Expected.Intents)
	} else {
		ok = contains(got, t.Expected.Intent)
	}
	detail := "intents=" + strings.Join(got, ",")
	if ok {
		detail += " reply=" + truncate(res.Reply, 80)
	}
	out := []GraderResult{{
		Name: "IntentMatch", Pass: ok, Score: boolScore(ok), Detail: detail, Weight: 0.4,
	}}
	// 期望回复包含关键词（如根因/处置）时追加内容检查
	if len(t.Expected.RootCause) > 0 {
		contentOK := false
		for _, kw := range t.Expected.RootCause {
			if strings.Contains(res.Reply, kw) {
				contentOK = true
				break
			}
		}
		out = append(out, GraderResult{Name: "ReplyContent", Pass: contentOK,
			Score: boolScore(contentOK), Detail: "reply=" + truncate(res.Reply, 80), Weight: 0.6})
	}
	return out
}
```

`grader.go` 追加 `containsAll` 辅助（放在 `contains` 旁）：

```go
func containsAll(haystack, needles []string) bool {
	for _, n := range needles {
		if !contains(haystack, n) {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/eval/ -v`
Expected: 全部 PASS，含既有 `TestRunTaskChatIntent`/`TestGradeChatIntent`/`TestGradeFailedRunRecordedAsFail`/`TestRuleRegression*`。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/eval/task.go backend/internal/eval/runner.go backend/internal/eval/grader.go backend/internal/eval/helpers.go backend/internal/eval/runner_test.go backend/internal/eval/grader_test.go
git commit -m "feat: eval支持多意图子意图集合评测与chat-multi任务"
```

---

### Task 7: 全量回归验证

**Files:**
- 无新增；验证全部包

**Interfaces:**
- Consumes: 全部已实现符号

- [ ] **Step 1: 全量单测**

Run: `go test ./...`
Expected: 全部 PASS。重点确认 `internal/eval` 的 `TestRuleRegressionMainTasks`/`TestRuleRegressionGpuTempFails`、`internal/agent`、`internal/api`、`internal/config`、`internal/llm` 均通过。

- [ ] **Step 2: 静态检查**

Run: `go vet ./...`
Expected: 无告警。

- [ ] **Step 3: 无 LLM key 冒烟（CI 路径）**

Run: `LLM_API_KEY= go run ./cmd/eval ci --threshold 0.9`（在 `backend/` 目录）
Expected: 退出码 0，rule 路径 pass@k ≥ 0.9。

- [ ] **Step 4: 提交（若本步骤产生文件改动）**

```bash
git status
# 无未提交改动时跳过 commit
```

---

## 自检清单（执行前确认）

- **Spec 覆盖**：✅ 轻量 LLM 一次调用重写+拆分 → Task 3 `RewriteAndSplit`；逐子问题规则优先+轻量分类 → Task 3 `ParseIntentWithLight`/Task 4 `answerOneStatus`；串行处理+状态记录+聚合 → Task 4 `answer`；`skipped` 超限提示 → Task 4；Unknown 增强引导语 → Task 4 `guideText`；无 LLM key 回退单意图规则 → Task 4 `answerSingle`；`LLM_LIGHT_*`/`INTENT_MAX_SPLIT` 配置 → Task 1；轻量客户端 → Task 2；main 装配 → Task 5；`Expected.Intents` 全命中+`chat-multi` 任务+runner/grader 同一解析器 → Task 6；回归护栏保持 → Task 7。
- **placeholder 扫描**：所有代码步骤均给出完整实现；无 TBD/TODO。
- **类型一致性**：`RewriteAndSplit(ctx, text, c) (string, []string, error)`、`ParseIntentWithLight(ctx, text, c) Intent`、`ParseIntentSet(ctx, text, c, maxSplit) []Intent`、`IntentName(Intent) string`、`NewWithLight(s, lc, light, eng, cs, topK, maxSplit) *Agent` 在各 Task 引用一致；`New`/`NewWithContext` 旧签名保持不变；`TrialResult.Intents []string`、`Expected.Intents []string` 在 Task 6 定义与使用一致。
