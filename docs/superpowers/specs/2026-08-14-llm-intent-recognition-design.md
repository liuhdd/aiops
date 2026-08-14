# LLM 意图识别设计

## 背景与目标

当前聊天 Agent 的意图识别由 `agent.ParseIntent`（`backend/internal/agent/intent.go`）纯规则关键词匹配实现，只能覆盖预置措辞，泛化能力弱。本设计将意图识别升级为 **LLM 分类 + 规则兜底**：规则优先，仅当规则判 `Unknown` 时调用 LLM 二次分类，LLM 不可用/失败时回退规则结果。实体提取（告警 ID、设备名）保持规则实现不变。

**目标**：在保持现有行为与测试护栏的前提下，让模糊/未见过的表述也能被 LLM 正确分类，Unknown 时给出增强引导语。

**非目标**：不新增/删改意图枚举（仍为 6 类）；不改 `extractAlertID`/`extractQuery`；不改 `answerFor`/`answerWithLLM` 的回答逻辑；不引入新的第三方依赖。

## 意图集合（不变）

```
Unknown, ListAlerts, InspectAlert, AskRootCause, AskRecommendation, AskStatus
```

## 架构与组件

### `ParseIntentWithLLM`（新增，`internal/agent/intent.go`）

```go
// ParseIntentWithLLM 规则优先；仅当规则判 Unknown 时用 LLM 分类；
// LLM 不可用/超时/解析失败时回退规则结果（Unknown）。
func ParseIntentWithLLM(ctx context.Context, text string, c ctxpkg.Completer) Intent
```

- `ParseIntent`（现有规则函数）**保持原样**，作为规则路径。
- 新增私有 `classifyWithLLM(ctx, text, c) (Intent, error)`：
  - 提示词：定义 6 类意图及每类示例，要求只输出 JSON `{"intent":"AskRootCause"}`（闭集）。
  - 解析用 `llm.ParseJSON`（兼容 ```json 包裹）；intent 值不在枚举内映射为 `Unknown`。
- `ParseIntentWithLLM` 流程：
  1. `ParseIntent(text)` 命中（非 `Unknown`）→ 直接返回，不调 LLM。
  2. `ctxpkg.IsNilCompleter(c)` → 返回规则结果（`Unknown`）。
  3. 调 `classifyWithLLM`；出错或结果为 `Unknown` → 返回规则结果。
  4. 否则返回 LLM 分类结果。

### `Agent.Handle` 接入（`internal/agent/agent.go`）

`Handle` 内所有 `ParseIntent(userText)` 调用点（第 69、95、97、102、104 行）统一改为
`ParseIntentWithLLM(ctx, userText, a.llm)`，`intent` 只解析一次并复用（消除现有重复调用）。

### Unknown 增强引导语

`switch` 新增 `default`（`Unknown`）分支，`reply` 覆盖默认引导语：

```
你好，我是AIOps运维助手。我可以帮你：
- 查看告警列表：如"有哪些告警"
- 排查单个告警：如"查一下 alarm-123 什么原因"
- 获取处置建议：如"alarm-123 怎么处理"
- 查看排查进度：如"排查进度如何"
如果输入和以上无关，请重新描述你的问题。
```

Unknown 时不触发流水线、不查询告警（与现有行为一致，满足 eval 反例 `chat-irrelevant`）。

## eval 同步

按"与运行时同一解析器 + 行为完全对齐生产"：

- `internal/eval/runner.go` `runChat`：构造 agent 时按 `cfg.LLM` 注入 `cl`（`agent.New(st, cl, eng)`），使 llm 配置下意图解析与回答（`answerWithLLM`）都走 LLM；`res.Intent = intentName(agent.ParseIntentWithLLM(ctx, t.ChatInput, cl))`。
- `internal/eval/grader.go` `gradeChat`：改用 `agent.ParseIntentWithLLM(ctx, t.ChatInput, judge)`（`Grade` 已有 `judge` 参数即 `lc`），意图评分与运行时同一解析器。
- 注意：`runner_test.go`、`grader_test.go` 中依赖规则 `ParseIntent` 的断言同步更新。

## 错误处理

| 场景 | 行为 |
|---|---|
| 规则命中 | 直接返回，零 LLM 调用 |
| LLM 不可用 / typed-nil | `IsNilCompleter` 短路，返回规则结果 |
| LLM 调用超时/出错 | 回退规则结果（`Unknown`），不 panic |
| LLM 输出非法 / intent 不在枚举 | 映射 `Unknown` |

## 测试

- `internal/agent/agent_test.go`：
  - 新增 `TestParseIntentWithLLM`（fake completer）：规则命中不调 LLM（fake 未被调用）；规则 Unknown + LLM 返回合法 intent → 采纳；LLM 返回非法 → 回退；LLM 出错 → 回退；completer 为 nil → 回退。
  - 现有 `TestParseIntent`、`TestExtractAlertIDHex` 不变（护栏）。
  - 新增/更新 Unknown 增强引导语断言。
- `internal/eval/`：fake completer 验证 `runChat` 注入后 `TrialResult.Intent` 正确、`gradeChat` 用 LLM 解析一致；既有回归护栏（`TestRuleRegressionMainTasks`、`TestRuleRegressionGpuTempFails`）保持通过。

## 验证

- `go test ./internal/agent/ ./internal/eval/ ./internal/api/`
- `go build ./...`

## 设计决策与取舍

1. **LLM 介入时机**（方案 B）：规则优先、仅 Unknown 时 LLM。相比"全部走 LLM"（方案 A）更省调用、行为更可预测；相比"只 LLM"（方案 C）保留无 key 时的完整可用性。用户选定。
2. **意图与实体分离**：LLM 只分类意图，实体提取保持规则。降低 LLM 幻觉对告警 ID/设备名的影响，改动面最小。
3. **eval 完全对齐生产**：llm 配置下 `runChat` 注入 LLM，意图解析与回答都与运行时一致，使评分反映真实生产行为。
4. **Unknown 增强引导语**：用户选定，改善模糊输入的用户体验，仍满足反例（不触发流水线）。
