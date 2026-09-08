# RAG 生成端提示词调优 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新建 `internal/prompt` 集中常量包，将 RAG 链路 5 处生成端提示词（answerWithLLM / assembler / rca 非 ReAct / recommend / query_rewrite）按「核心规则 + 异常兜底 + 输出格式」三阶框架重构。

**Architecture:** 纯常量包 `internal/prompt`（零内部依赖），agent/context/pipeline/retrieve 四包单向引用替换各自内联提示词；`answerWithLLM` 首次消费 `as.System`（拼接 ChatAnswerSystem）。不改检索管线、JSON schema 与降级路径。

**Tech Stack:** Go 1.x（`aiops` module）、标准库、`go test`。

**Spec:** `docs/superpowers/specs/2026-09-08-rag-prompt-tuning-design.md`

## Global Constraints

- 所有代码、注释、commit message 用中文；commit 前缀 `feat:`/`test:`/`refactor:`。
- SQLite driver `modernc.org/sqlite`；测试建库惯用法 `store.New(filepath.Join(t.TempDir(), "t.db"))`。
- 提示词约束总数 ≤5 条/处，核心规则（禁止编造/兜底口径）前置。
- `internal/prompt` 不 import 任何 `aiops/internal/*` 包。
- ReAct 分支（rca.react 内联 system）、eval grader、kb enhance 提示词不动。
- `backend/testdata/`（eval golden set）不改动。
- 静态检查只有 `go vet ./...`（无 golangci-lint）。
- 每个任务完成时 `go test ./...` 全绿再 commit。

---

### Task 1: 新建 `internal/prompt` 常量包

**Files:**
- Create: `backend/internal/prompt/prompt.go`
- Test: `backend/internal/prompt/prompt_test.go`

**Interfaces:**
- Produces（后续任务全部依赖，签名固定）:
  - `const ChatAnswerSystem string`
  - `const KnowledgeIntro string`
  - `const RCASystem string`
  - `const RecommendSystem string`
  - `func ChatAnswerUser(alertID, device, metric, rca, actions, context, question string) string`
  - `func QueryRewriteInstruction(history string, q string) string`

- [ ] **Step 1: Write the failing test**

```go
package prompt

import (
	"strings"
	"testing"
)

func TestChatAnswerSystemContainsCoreRules(t *testing.T) {
	for _, kw := range []string{"暂无相关信息", "禁止编造", "禁止使用自身知识补充", "算力中心硬件运维助手", "分点回答"} {
		if !strings.Contains(ChatAnswerSystem, kw) {
			t.Fatalf("ChatAnswerSystem 应含 %q: %s", kw, ChatAnswerSystem)
		}
	}
}

func TestChatAnswerUserKeepsStructure(t *testing.T) {
	u := ChatAnswerUser("alarm-1", "db-01", "disk_usage", "磁盘空间不足", "清理日志", "【知识库】\n块A", "为什么")
	for _, kw := range []string{"告警 alarm-1", "设备=db-01", "指标=disk_usage", "已有根因：磁盘空间不足", "已有处置：清理日志", "【相关上下文】", "【知识库】", "用户问题：为什么"} {
		if !strings.Contains(u, kw) {
			t.Fatalf("ChatAnswerUser 应含 %q:\n%s", kw, u)
		}
	}
}

func TestKnowledgeIntroContainsUsageRules(t *testing.T) {
	for _, kw := range []string{"标注来源", "无相关信息时", "以下是当前任务相关的上下文信息"} {
		if !strings.Contains(KnowledgeIntro, kw) {
			t.Fatalf("KnowledgeIntro 应含 %q: %s", kw, KnowledgeIntro)
		}
	}
}

func TestRCASystemContainsGapRule(t *testing.T) {
	for _, kw := range []string{"gaps", "禁止编造 evidence_chain", "只输出JSON", "算力中心硬件运维专家"} {
		if !strings.Contains(RCASystem, kw) {
			t.Fatalf("RCASystem 应含 %q: %s", kw, RCASystem)
		}
	}
}

func TestRecommendSystemContainsRunbookRule(t *testing.T) {
	for _, kw := range []string{"无预案参考", "runbook_ref", "只输出JSON"} {
		if !strings.Contains(RecommendSystem, kw) {
			t.Fatalf("RecommendSystem 应含 %q: %s", kw, RecommendSystem)
		}
	}
}

func TestQueryRewriteInstructionKeepsEntities(t *testing.T) {
	p := QueryRewriteInstruction("历史行", "磁盘满了怎么办")
	for _, kw := range []string{"原样保留", "历史：历史行", "查询：磁盘满了怎么办", "仅输出改写句"} {
		if !strings.Contains(p, kw) {
			t.Fatalf("QueryRewriteInstruction 应含 %q: %s", kw, p)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/prompt/ -v`（workdir `backend/`）
Expected: FAIL，`undefined: ChatAnswerSystem`（包未实现）

- [ ] **Step 3: Write minimal implementation**

```go
// Package prompt 集中管理 RAG 生成端提示词常量（三阶避坑法：核心规则前置 + 异常兜底 + 输出格式，约束总数 ≤5 条）。
// 本包禁止 import 任何 aiops/internal/* 包，供 agent/context/pipeline/retrieve 单向引用。
package prompt

import "fmt"

// ChatAnswerSystem answerWithLLM 系统提示：核心规则 + 角色（领域+身份+核心要求）+ 输出要求。
const ChatAnswerSystem = "【核心规则】必须严格基于【相关上下文】与已有排查结论回答；上下文中没有的信息回答\"暂无相关信息\"，禁止使用自身知识补充，禁止编造。" +
	"你是算力中心硬件运维助手，结论需注明依据来源（上下文段落或已有根因）。" +
	"【输出要求】分点回答，每点不超过3句，语言简洁专业。"

// KnowledgeIntro 组装结果 system 提示：知识使用规则。
const KnowledgeIntro = "以下是当前任务相关的上下文信息。回答时优先依据【知识库】【设备历史经验】等分段内容，引用结论时标注来源文档；分段中无相关信息时直接说明，不要引用不存在的段落。"

// RCASystem rca 非 ReAct 分支系统提示：补证据兜底口径。
const RCASystem = "你是算力中心硬件运维专家。基于给定证据输出JSON：{\"root_cause\":string,\"evidence_chain\":[string],\"confidence\":0~1,\"gaps\":[string]}。证据中不存在的内容必须写入 gaps，禁止编造 evidence_chain。只输出JSON。"

// RecommendSystem 处置建议系统提示：步骤须与预案对应。
const RecommendSystem = "你是算力中心运维助手。基于根因和预案，给出简明的处置建议步骤列表。步骤须与 runbook_ref 对应，无对应预案时 risk 填\"无预案参考\"。只输出JSON：{\"actions\":[string],\"runbook_ref\":string,\"risk\":string}"

// ChatAnswerUser answerWithLLM 用户消息（保持现有结构：告警行 + 已有结论 + 上下文 + 问题）。
func ChatAnswerUser(alertID, device, metric, rca, actions, context, question string) string {
	return fmt.Sprintf("告警 %s：设备=%s 指标=%s\n已有根因：%s\n已有处置：%s\n\n【相关上下文】\n%s\n\n用户问题：%s",
		alertID, device, metric, rca, actions, context, question)
}

// QueryRewriteInstruction 检索改写提示：实体词原样保留。
func QueryRewriteInstruction(history, q string) string {
	return "将下述查询结合对话历史改写为独立检索句，仅输出改写句。设备名、告警ID、指标名等实体词必须原样保留。\n历史：" + history + "\n查询：" + q
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/prompt/ -v`（workdir `backend/`）
Expected: PASS，6 个用例全绿

- [ ] **Step 5: Commit**

```bash
git add backend/internal/prompt/prompt.go backend/internal/prompt/prompt_test.go
git commit -m "feat: 新增 internal/prompt 集中管理 RAG 生成端提示词常量"
```

---

### Task 2: `retrieve.RewriteQuery` 接入 QueryRewriteInstruction

**Files:**
- Modify: `backend/internal/retrieve/query_rewrite.go:39`
- Test: `backend/internal/retrieve/query_rewrite_test.go`

**Interfaces:**
- Consumes: `prompt.QueryRewriteInstruction(history, q string) string`（Task 1）
- Produces: `RewriteQuery` 对外签名不变；发出的 prompt 含"原样保留"（Task 7 回归依赖）

- [ ] **Step 1: Write the failing test**

在 `backend/internal/retrieve/query_rewrite_test.go` 追加（复用文件内已有的 `rewriteFake`，其 `Complete` 记录 `f.prompt`）：

```go
func TestRewriteQueryPromptKeepsEntities(t *testing.T) {
	f := &rewriteFake{out: "改写后的句子"}
	_, err := RewriteQuery(context.Background(), f, []string{"上一句"}, "磁盘满了怎么办")
	if err != nil {
		t.Fatalf("RewriteQuery: %v", err)
	}
	for _, kw := range []string{"原样保留", "历史：上一句", "查询：磁盘满了怎么办"} {
		if !strings.Contains(f.prompt, kw) {
			t.Fatalf("prompt 应含 %q, got %q", kw, f.prompt)
		}
	}
}
```

若文件缺 `context`/`strings` import 则补上（现文件已含两者）。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/retrieve/ -run TestRewriteQueryPromptKeepsEntities -v`（workdir `backend/`）
Expected: FAIL，prompt 不含"原样保留"

- [ ] **Step 3: Write minimal implementation**

`query_rewrite.go:39` 的 prompt 行替换为：

```go
	prompt := prompt.QueryRewriteInstruction(strings.Join(history, "\n"), q)
```

并在 import 块加 `"aiops/internal/prompt"`。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/retrieve/ -v`（workdir `backend/`）
Expected: PASS（含既有 `TestRewriteQuery*`，其对"历史"与原查询的断言仍满足）

- [ ] **Step 5: Commit**

```bash
git add backend/internal/retrieve/query_rewrite.go backend/internal/retrieve/query_rewrite_test.go
git commit -m "feat: 检索改写提示词补实体词保留规则"
```

---

### Task 3: `assembler.Assemble` 接入 KnowledgeIntro

**Files:**
- Modify: `backend/internal/context/assembler.go:64`
- Test: `backend/internal/context/assembler_test.go`

**Interfaces:**
- Consumes: `prompt.KnowledgeIntro`（Task 1）
- Produces: `Assembled.System` 语义变为知识使用规则（Task 6 的 `answerWithLLM` 拼接依赖）

- [ ] **Step 1: Write the failing test**

在 `backend/internal/context/assembler_test.go` 追加（复用文件内现有建库辅助；参考既有 `TestAssemble*` 的 `cs` 构造方式，可直接用 `NewStore(s)` + `NewAssembler(cs, 3)`，无需数据——System 不依赖候选）：

```go
func TestAssembleSystemIsKnowledgeIntro(t *testing.T) {
	mockdata.GenerateAll(42)
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := NewStore(s)
	as, err := NewAssembler(cs, 3).Assemble(Query{Stage: "chat"}, false, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	for _, kw := range []string{"标注来源", "无相关信息时", "以下是当前任务相关的上下文信息"} {
		if !strings.Contains(as.System, kw) {
			t.Fatalf("System 应含 %q, got %q", kw, as.System)
		}
	}
}
```

（所需 import `mockdata`/`store`/`path/filepath`/`strings` 该文件均已存在。）

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/context/ -run TestAssembleSystemIsKnowledgeIntro -v`（workdir `backend/`）
Expected: FAIL，System 不含"标注来源"

- [ ] **Step 3: Write minimal implementation**

`assembler.go:64` 替换为：

```go
	out.System = prompt.KnowledgeIntro
```

并在 import 块加 `"aiops/internal/prompt"`。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/context/ -v`（workdir `backend/`）
Expected: PASS（既有 `TestAssemble*` 不断言 System 文案，应全绿）

- [ ] **Step 5: Commit**

```bash
git add backend/internal/context/assembler.go backend/internal/context/assembler_test.go
git commit -m "feat: 组装器 system 提示升级为知识使用规则"
```

---

### Task 4: `rca` 非 ReAct 分支接入 RCASystem

**Files:**
- Modify: `backend/internal/pipeline/rca.go:67`
- Test: `backend/internal/pipeline/stages_test.go`

**Interfaces:**
- Consumes: `prompt.RCASystem`（Task 1）
- Produces: `singleCall` 发出的 system 含 gaps 兜底口径；`ParseRCAJSON`/`ruleRCA` 不变
- 局部新增测试 fake（Task 5 复用）: `sysCapturingLLM{system, raw string}`，方法 `Complete(_ context.Context, system, _ string) (string, error)`

- [ ] **Step 1: Write the failing test**

在 `backend/internal/pipeline/stages_test.go` 追加（同包，无需建库：`singleCall` 在 `st.Ctx == nil` 时直接拼证据，`State` 只需 `Alert` 字段；import 若缺 `context`/`time` 等以文件现状为准补齐）：

```go
type sysCapturingLLM struct {
	system string
	raw    string
}

func (f *sysCapturingLLM) Complete(_ context.Context, system, _ string) (string, error) {
	f.system = system
	return f.raw, nil
}

func TestRCASingleCallSystemHasGapRule(t *testing.T) {
	fake := &sysCapturingLLM{raw: `{"root_cause":"磁盘空间不足","evidence_chain":["e"],"confidence":0.8,"gaps":[]}`}
	st := &State{Alert: model.Alert{ID: "a1", Device: "db-01", Metric: "disk_usage"}}
	if err := (NewRCAStage(fake)).Run(context.Background(), st); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, kw := range []string{"gaps", "禁止编造 evidence_chain", "只输出JSON", "算力中心硬件运维专家"} {
		if !strings.Contains(fake.system, kw) {
			t.Fatalf("system 应含 %q, got %q", kw, fake.system)
		}
	}
	if st.RCA == nil || st.RCA.RootCause != "磁盘空间不足" {
		t.Fatalf("RCA = %+v", st.RCA)
	}
}
```

若 `stages_test.go` 缺 `strings` import 则补上。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pipeline/ -run TestRCASingleCallSystemHasGapRule -v`（workdir `backend/`）
Expected: FAIL，system 不含"禁止编造 evidence_chain"

- [ ] **Step 3: Write minimal implementation**

`rca.go:67` 替换为：

```go
	system := prompt.RCASystem
```

并在 import 块加 `"aiops/internal/prompt"`。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/pipeline/ -v`（workdir `backend/`）
Expected: PASS 全绿

- [ ] **Step 5: Commit**

```bash
git add backend/internal/pipeline/rca.go backend/internal/pipeline/stages_test.go
git commit -m "feat: RCA 非ReAct分支补证据不足兜底口径"
```

---

### Task 5: `recommend` 阶段接入 RecommendSystem

**Files:**
- Modify: `backend/internal/pipeline/recommend.go:29`
- Test: `backend/internal/pipeline/stages_test.go`

**Interfaces:**
- Consumes: `prompt.RecommendSystem`（Task 1）、`sysCapturingLLM`（Task 4 已加入 stages_test.go）
- Produces: recommend 阶段 LLM system 含"无预案参考"；JSON schema 与规则兜底不变

- [ ] **Step 1: Write the failing test**

在 `backend/internal/pipeline/stages_test.go` 追加（复用 Task 4 的 `sysCapturingLLM`；`st.Ctx == nil` 时 user 只拼根因与预案）：

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pipeline/ -run TestRecommendSystemHasRunbookRule -v`（workdir `backend/`）
Expected: FAIL，system 不含"无预案参考"

- [ ] **Step 3: Write minimal implementation**

`recommend.go:29` 替换为：

```go
		system := prompt.RecommendSystem
```

并在 import 块加 `"aiops/internal/prompt"`。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/pipeline/ -v`（workdir `backend/`）
Expected: PASS 全绿

- [ ] **Step 5: Commit**

```bash
git add backend/internal/pipeline/recommend.go backend/internal/pipeline/recommend_test.go
git commit -m "feat: 处置建议提示词补预案对应约束"
```

---

### Task 6: `agent.answerWithLLM` 接入 ChatAnswerSystem + as.System

**Files:**
- Modify: `backend/internal/agent/agent.go:403-405`
- Test: `backend/internal/agent/agent_test.go`

**Interfaces:**
- Consumes: `prompt.ChatAnswerSystem`、`prompt.ChatAnswerUser`、`KnowledgeIntro`（经 `as.System`，来自 Task 1/3）
- Produces: `answerWithLLM` 发出的 system = `prompt.ChatAnswerSystem + "\n" + as.System`；user 消息结构不变（文案归口 `ChatAnswerUser`）

- [ ] **Step 1: Write the failing test**

改造 `agent_test.go:98-102` 的 `fakeChatLLM` 增加 `lastSystem` 字段并记录：

```go
type fakeChatLLM struct {
	raw        string
	err        error
	lastUser   string
	lastSystem string
}

func (f *fakeChatLLM) Complete(_ context.Context, system, user string) (string, error) {
	f.lastUser = user
	f.lastSystem = system
	return f.raw, f.err
}
```

在 `TestAgentLLMAnswerUsesAssembledContext`（agent_test.go:109）内、`if !strings.Contains(fake.lastUser, "【设备历史经验】")` 断言之后追加：

```go
	for _, kw := range []string{"暂无相关信息", "禁止编造", "分点回答", "以下是当前任务相关的上下文信息"} {
		if !strings.Contains(fake.lastSystem, kw) {
			t.Fatalf("system 应含 %q, got %q", kw, fake.lastSystem)
		}
	}
```

（该测试的 cs 用 `mockdata` 造过设备历史经验，`as.System` 为 `KnowledgeIntro`，拼接后应同时含两类关键词。）

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run TestAgentLLMAnswerUsesAssembledContext -v`（workdir `backend/`）
Expected: FAIL，system 不含"暂无相关信息"

- [ ] **Step 3: Write minimal implementation**

`agent.go:399-405`（`ctxStr := ""` 起到 `user := fmt.Sprintf(...)` 止）整体替换为：

```go
	ctxStr := ""
	system := prompt.ChatAnswerSystem
	if as := a.buildAssembled(conversationID, alert.Device, alert.Metric, []string{intent}); as != nil {
		ctxStr = as.User
		system += "\n" + as.System
	}
	user := prompt.ChatAnswerUser(alertID, alert.Device, alert.Metric,
		rca.RootCause, strings.Join(rec.Actions, "；"), ctxStr, userText)
```

（import 块加 `"aiops/internal/prompt"`；`fmt` 若因此不再被该函数使用，看文件内其他处是否仍用 `fmt`——`agent.go` 其他函数仍用，保留 import。）

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent/ -v`（workdir `backend/`）
Expected: PASS 全绿（`TestAgentLLMFallsBackToRule` 等降级路径不变）

- [ ] **Step 5: Commit**

```bash
git add backend/internal/agent/agent.go backend/internal/agent/agent_test.go
git commit -m "feat: 对话根因/处置回答接入核心规则与知识使用规则提示词"
```

---

### Task 7: 全量回归 + eval 不回退

**Files:**
- 无新文件（验证任务）

**Interfaces:**
- Consumes: Task 1-6 全部改动
- Produces: 回归结论

- [ ] **Step 1: 全量单测**

Run: `go test ./...`（workdir `backend/`）
Expected: PASS 全绿（含 `internal/e2e`）

- [ ] **Step 2: 静态检查**

Run: `go vet ./...`（workdir `backend/`）
Expected: 无输出（通过）

- [ ] **Step 3: import 环检查**

Run: `go list -deps ./internal/prompt`（workdir `backend/`）
Expected: 输出不含任何 `aiops/internal/*` 包

- [ ] **Step 4: eval 回归**

Run: `make eval`（workdir `backend/`，无 `LLM_API_KEY` 时 llm 配置走规则兜底，可照常运行）
Expected: 产出生成 `eval-report.json`，三配置（rule/llm-single/llm-react）总分不低于改动前基线；有 key 环境下 CI 门禁阈值 0.9 不变。若总分下降超过 0.02，回到对应提示词排查（优先检查核心规则是否被模型误读为拒答），不得擅自改 golden set。

- [ ] **Step 5: 记录结论并提交（如有遗留小修）**

若有因回归暴露的小修（如测试文案断言），修复后：

```bash
git add -A backend/
git commit -m "test: 提示词调优回归修正"
```

无遗留则跳过本步（无空提交）。
