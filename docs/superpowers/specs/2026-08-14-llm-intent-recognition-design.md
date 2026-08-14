# LLM 意图识别设计

## 背景与目标

当前聊天 Agent 的意图识别由 `agent.ParseIntent`（`backend/internal/agent/intent.go`）纯规则关键词匹配实现，只能覆盖预置措辞，泛化能力弱，且不支持一次输入包含多个意图。

本设计将意图识别升级为 **LLM 重写拆分 + 规则优先 + 轻量 LLM 兜底** 的多意图流程：

```
用户输入 → 轻量LLM重写+拆分 → 逐子问题（规则意图优先，Unknown 用轻量LLM分类）→ 分别处理记录状态 → 聚合为一条回复
```

实体提取（告警 ID、设备名）保持规则实现不变。

**目标**：让模糊/未见过的表述被 LLM 正确分类，支持多意图输入分别处理并聚合回复，Unknown 时给出增强引导语。

**非目标**：不新增/删改意图枚举（仍为 6 类）；不改 `extractAlertID`/`extractQuery`；不改 `answerFor`/`answerWithLLM` 的回答逻辑；不引入新的第三方依赖。

## 意图集合（不变）

```
Unknown, ListAlerts, InspectAlert, AskRootCause, AskRecommendation, AskStatus
```

## 处理流程

```
用户输入
  → ① 轻量LLM 一次调用（重写+拆分）：输出 {rewritten, sub_questions[]}（上限可配，默认3）
  → ② 逐子问题（串行）：
       规则 ParseIntent(子问题) → 命中? → 用之
                            └── Unknown → 轻量LLM分类（同轻模型）
       执行该意图的回复逻辑，记录状态
  → ③ 聚合：所有子问题回复+状态拼接成一条消息
```

无 LLM key / completer 为 nil 时：回退现有**单意图规则** `ParseIntent`（不拆不重写，CI/测试照常）。

## 架构与组件（`internal/agent/`）

### 新增函数（`intent.go`）

```go
// RewriteAndSplit 一次轻量 LLM 调用完成问题重写与多意图拆分。
func RewriteAndSplit(ctx context.Context, text string, c ctxpkg.Completer, maxSplit int) (rewritten string, subs []string, err error)

// classifyWithLight 轻量 LLM 分类为 6 类之一；失败/非法映射 Unknown。
func classifyWithLight(ctx context.Context, text string, c ctxpkg.Completer) Intent
```

- `ParseIntent`（现有规则函数）**保持原样**，作为规则路径。
- `RewriteAndSplit` 提示词：要求返回 JSON `{"rewritten":"...","sub_questions":["...","..."]}`；`rewritten` 为规范化后的完整问题文本，`sub_questions` 为拆分出的子问题（`<= maxSplit` 条）；解析用 `llm.ParseJSON`，失败返回 err。
- `classifyWithLight` 提示词：定义 6 类意图及示例，要求只输出 JSON `{"intent":"AskRootCause"}`（闭集），解析失败映射 `Unknown`。
- 意图识别链路（重写+拆分+分类）统一走**轻量模型**（`LLM_LIGHT_*` 配置）。

### `Agent` 扩展（`agent.go`）

- `Agent` 新增字段 `lightLLM ctxpkg.Completer` 与 `maxSplit int`。
- `NewWithContext(s, lc, light, eng, cs, topK, maxSplit)` 签名扩展，`light` 为轻量 completer、`maxSplit` 为拆分上限；`New` 内 `light=lc`、`maxSplit=3`。`cmd/server/main.go`、`cmd/eval`、测试构造点同步。
- `Handle` 重构：
  1. 若 `lightLLM` 可用：调 `RewriteAndSplit`，成功则逐子问题处理（步骤②/③），失败回退单意图路径。
  2. 单意图路径（无轻模型 / 重写拆分失败 / 兜底）：沿用现有 `ParseIntent` 分支逻辑。
  3. `intent` 只解析一次并复用（消除现有重复调用）。

### 状态记录与聚合（步骤③）

- 每子问题记录状态：`success`（正常回复）/ `no_alert`（未找到告警）/ `unknown`（意图未命中，输出引导语）/ `llm_failed`（LLM 链路失败，走兜底）/ `skipped`（超过上限被忽略）。
- 聚合格式示例：
  ```
  [1] 有哪些告警 → success
  当前告警列表：...
  [2] alarm-123 什么原因 → success
  根因：...
  ```

### Unknown 增强引导语

子问题意图为 `Unknown` 时输出引导语，不触发流水线、不查询告警：

```
你好，我是AIOps运维助手。我可以帮你：
- 查看告警列表：如"有哪些告警"
- 排查单个告警：如"查一下 alarm-123 什么原因"
- 获取处置建议：如"alarm-123 怎么处理"
- 查看排查进度：如"排查进度如何"
如果输入和以上无关，请重新描述你的问题。
```

## 配置（`internal/config/config.go`）

| 环境变量 | 默认 | 说明 |
|---|---|---|
| `LLM_LIGHT_BASE_URL` | 同 `LLM_BASE_URL` | 轻量模型 BaseURL（复用主 key） |
| `LLM_LIGHT_MODEL` | 同 `LLM_MODEL` | 轻量分类模型；未配置则轻模型步骤回退规则 |
| `INTENT_MAX_SPLIT` | `3` | 单条输入拆分上限 |

主模型 `LLM_BASE_URL`/`LLM_MODEL` 仍用于回答生成（`answerWithLLM`）。

## eval 适配

- `Expected` 新增 `Intents []string`：**全部必须出现**才过（多意图任务校验）；保留 `Intent` 单意图兼容。
- 新增多意图 chat 任务 `chat-multi`：输入"有哪些告警？alarm-1 什么原因？"，`Expected.Intents=[ListAlerts, AskRootCause]`。
- `internal/eval/runner.go` `runChat`：构造 agent 时按 `cfg.LLM` 注入 `cl`（主 client，eval 中复用为轻模型），`res.Intents` 记录解析出的子意图集合。
- `internal/eval/grader.go` `gradeChat`：与运行时同一解析器，用子意图集合比对 `Expected.Intents`（多意图）或 `Expected.Intent`（单意图）。
- 既有回归护栏（`TestRuleRegressionMainTasks`、`TestRuleRegressionGpuTempFails`）保持通过。

## 错误处理

| 场景 | 行为 |
|---|---|
| 无 LLM key / typed-nil | `IsNilCompleter` 短路，走单意图规则路径 |
| 重写+拆分失败 | 回退单意图规则路径 |
| 子问题规则命中 | 直接使用，零额外 LLM 调用 |
| 轻量 LLM 分类失败/非法 | 该子问题映射 `Unknown`，输出引导语 |
| 超过 `maxSplit` | 多余子问题标记 `skipped`，在聚合中提示 |

## 测试

- `internal/agent/agent_test.go`：
  - 新增 `TestRewriteAndSplit`（fake completer）：合法 JSON 拆出子问题、超限截断、解析失败回退。
  - 新增 `TestClassifyWithLight`：合法 intent 采纳、非法映射 Unknown、出错回退。
  - 新增 `TestHandleMultiIntent`：多意图输入串行处理、聚合格式含分项状态。
  - 现有 `TestParseIntent`、`TestExtractAlertIDHex` 不变（护栏）。
  - 新增 Unknown 增强引导语断言。
- `internal/eval/`：fake completer 验证 `runChat` 注入后 `res.Intents` 正确、`gradeChat` 多意图比对一致；新增 `chat-multi` 任务断言。

## 验证

- `go test ./internal/agent/ ./internal/eval/ ./internal/api/`
- `go build ./...`

## 设计决策与取舍

1. **重写+拆分一次轻量 LLM 调用**：相比两次主模型调用，延迟/成本更低，且轻模型足够完成结构化输出；用户选定。
2. **意图识别链路统一走轻量模型**：重写、拆分、分类三处共用 `LLM_LIGHT_*`，主模型只做回答生成，职责分离。
3. **规则优先 + LLM 兜底**：子问题先规则命中，Unknown 才调轻量 LLM，省调用、行为可预测；无 key 时规则路径完整可用（CI 护栏）。
4. **意图与实体分离**：LLM 只产出意图与子问题文本，实体提取保持规则。降低 LLM 幻觉对告警 ID/设备名的影响。
5. **eval 完全对齐生产**：llm 配置下 `runChat` 注入 LLM，意图解析与回答都与运行时一致；多意图任务校验 `Expected.Intents` 全部命中。
6. **可配置上限**：`INTENT_MAX_SPLIT` 默认 3，避免单轮无限触发流水线。
7. **Unknown 增强引导语**：改善模糊输入体验，仍满足反例（不触发流水线）。
