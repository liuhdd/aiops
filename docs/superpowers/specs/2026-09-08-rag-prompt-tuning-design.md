# RAG 生成端提示词调优（三阶避坑法落地）设计文档

- 日期：2026-09-08
- 状态：已确认
- 依赖：混合检索（`2026-08-20-knowledge-hybrid-retrieval`）、上下文组装（`internal/context`）、评测体系（`make eval`）
- 范围：RAG 链路 5 处生成端提示词的重构 — 新建 `internal/prompt` 集中常量包，按「指令精度 → 角色适配 → 约束分层」框架补齐核心规则层 / 异常处理层 / 输出格式层。不改检索逻辑、不改数据模型。

## 1. 背景与目标

对照 RAG 提示词调优方法论（三阶避坑法：指令精度 > 角色适配 > 约束分层），排查本项目 RAG 链路发现：检索端（指纹快路径 → 双路召回 → RRF → rerank → 门限）已较完善，短板集中在生成端提示词，4 类问题：

1. **指令模糊**：`answerWithLLM`（`internal/agent/agent.go`）只有一句"结合给定上下文回答"，无"必须基于参考内容、禁止编造"的核心规则；`ctxStr` 为空（组装降级/无命中）时模型必然自由发挥，幻觉风险最高。
2. **知识使用规则缺失**：`assembler`（`internal/context/assembler.go`）的 `System` 仅一句"以下是当前任务相关的上下文信息。"，未告知模型如何使用各分段（引用、标注来源、无信息时明说）。
3. **兜底口径缺失**：`rca` 非 ReAct 分支有 `gaps` 字段但未指令"证据中没有的写入 gaps"；`recommend` 未要求步骤与 runbook 对应。
4. **改写丢实体**：`query_rewrite` 改写提示词未要求保留设备名/告警ID/指标名，实体丢失直接伤 BM25 通道。

目标：

1. 5 处提示词全部符合「核心规则层（1-2 条前置）+ 异常处理层 + 输出格式层 + 场景化角色（领域+身份+核心要求）」框架，约束总数 ≤5 条。
2. 提示词文案集中到单一包，单测断言 + `make eval` 回归不回退（门禁阈值 0.9 不变）。
3. 无 key 时行为不变（规则兜底路径不经过这些提示词）。

## 2. 已确认决策

- **范围 5 处**：`answerWithLLM`、`assembler`、`rca` 非 ReAct 分支、`recommend`、`query_rewrite`；`rca` ReAct 分支、`eval` grader、`kb enhance` 提示词已合规，不动。
- **集中管理**：新建 `backend/internal/prompt` 包（纯常量 + 构造函数，零业务依赖，无循环引用风险）。
- **验证**：单测断言常量含关键要素（核心规则/异常兜底/实体保留）+ 跑 `make eval` 确认三配置（rule/llm-single/llm-react）不回退；不新增评测任务。
- **不配置化**：常量包即可满足集中调优；如后续需在线调优再升级为配置（改动面小）。
- `kbReply` 非 LLM 路径直接返回知识原文，无幻觉风险，不动。

## 3. `internal/prompt` 包设计

新增 `backend/internal/prompt/prompt.go`，仅字符串常量与 `fmt.Sprintf` 构造函数：

```go
package prompt

// ChatAnswer：answerWithLLM 系统提示（核心规则前置 + 角色补核心要求 + 输出格式 + 异常兜底）
const ChatAnswerSystem = "【核心规则】必须严格基于【相关上下文】与已有排查结论回答；上下文中没有的信息回答\"暂无相关信息\"，禁止使用自身知识补充，禁止编造。" +
    "你是算力中心硬件运维助手，结论需注明依据来源（上下文段落或已有根因）。" +
    "【输出要求】分点回答，每点不超过3句，语言简洁专业。"

func ChatAnswerUser(alertID, device, metric, rca, actions, context, question string) string

// KnowledgeIntro：assembler 组装结果的 system 提示（知识使用规则）
const KnowledgeIntro = "以下是当前任务相关的上下文信息。回答时优先依据【知识库】【设备历史经验】等分段内容，引用结论时标注来源文档；分段中无相关信息时直接说明，不要引用不存在的段落。"

// RCA：rca 非 ReAct 分支（补证据兜底口径）
const RCASystem = "你是算力中心硬件运维专家。基于给定证据输出JSON：{\"root_cause\":string,\"evidence_chain\":[string],\"confidence\":0~1,\"gaps\":[string]}。证据中不存在的内容必须写入 gaps，禁止编造 evidence_chain。只输出JSON。"

// Recommend：处置建议（补 runbook 对应约束）
const RecommendSystem = "你是算力中心运维助手。基于根因和预案，给出简明的处置建议步骤列表。步骤须与 runbook_ref 对应，无对应预案时 risk 填\"无预案参考\"。只输出JSON：{\"actions\":[string],\"runbook_ref\":string,\"risk\":string}"

// QueryRewrite：检索改写（补实体词保留）
const QueryRewriteInstruction = "将下述查询结合对话历史改写为独立检索句，仅输出改写句。设备名、告警ID、指标名等实体词必须原样保留。\n历史：%s\n查询：%s"
```

要点：

- **约束分层**：核心规则（禁止编造/兜底口径）置首，格式规则随后，总数 ≤5 条。
- **异常处理层**：`暂无相关信息`（ChatAnswer）、`写入 gaps`（RCA）、`无预案参考`（Recommend）、`保留实体词`（Rewrite）各自覆盖信息不足场景。
- **角色公式**：领域+身份已有，统一补齐第三要素"核心要求"（标注来源/与预案对应）。
- 现有各处内联字符串删除，改引常量；`ChatAnswerUser` 保持现有 user 消息结构（告警行 + 已有根因/处置 + 【相关上下文】 + 问题），仅文案归口。

## 4. 各调用点改动

| 调用点 | 文件 | 改动 |
|---|---|---|
| `answerWithLLM` | `internal/agent/agent.go` | system 换 `prompt.ChatAnswerSystem`；user 拼 `prompt.ChatAnswerUser` |
| `Assemble` | `internal/context/assembler.go` | `out.System` 换 `prompt.KnowledgeIntro` |
| `rcaRun` 非 ReAct | `internal/pipeline/rca.go` | system 换 `prompt.RCASystem`（仅补兜底句，JSON schema 不变） |
| `recommend` 阶段 | `internal/pipeline/recommend.go` | system 换 `prompt.RecommendSystem`（risk 口径明确，schema 不变） |
| `RewriteQuery` | `internal/retrieve/query_rewrite.go` | 拼接模板换 `prompt.QueryRewriteInstruction`（新增实体词保留句） |

`internal/prompt` 不 import 任何内部包，`agent`/`context`/`pipeline`/`retrieve` 单向引用，无环。

## 5. 错误处理与兼容

- 所有改动仅替换提示词文案，LLM 失败/空返回的既有降级路径（`answerFor` 规则回复、rerank 静默降级等）不变。
- 无 `LLM_API_KEY` 时 client 为 nil，LLM 分支不执行，行为与现状一致。
- **system 提示的叠加口径**：`ChatAnswerSystem`（回答规则）与 `KnowledgeIntro`（知识使用规则）职责不同、二者叠加使用——`answerWithLLM` 的最终 system = `prompt.ChatAnswerSystem` + "\n" + `as.System`；ReAct 分支（`react.go`）继续只消费 `as.System`，自动获得新的知识使用规则（属预期增强，单测覆盖）。

## 6. 测试与验证

1. **`internal/prompt/prompt_test.go`**：断言各常量含关键要素——ChatAnswerSystem 含"暂无相关信息"+"禁止编造"；RCASystem 含"gaps"+"禁止编造"；RecommendSystem 含"无预案参考"；KnowledgeIntro 含"标注来源"；QueryRewriteInstruction 含"原样保留"。
2. **既有单测适配**：`agent_test.go`（prompt 断言）、`assembler_test.go`、`rca`/`recommend` 相关测试、`query_rewrite_test.go` 中对内联文案的断言改为对 `prompt.*` 常量的断言。
3. **回归**：`go test ./...` 全绿；`make eval` 三配置总分不低于基线（CI 门禁 0.9 不变）。eval golden set（`backend/testdata/`）不改动。

## 7. 不做的事

- 不改检索管线（指纹/RRF/rerank/门限）、不改分块与富化逻辑。
- 不做提示词配置化/热更新。
- 不动 ReAct 分支、grader、kb enhance 的提示词（已合规）。
- 不改 `kbReply` 非 LLM 路径。
- 不新增评测任务或 golden 样本。
