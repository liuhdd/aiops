# LLM 链路可观测性与评测打通设计（Langfuse 接入 + 埋点）

- 日期：2026-09-09
- 状态：待确认
- 依赖：Agent 评测体系（`2026-08-12-agent-evaluation`）、检索质量（`2026-08-27-retrieval-quality`）
- 范围：LLM 调用层埋点（tokens/耗时/归属）、流水线与 chat agent 的 trace/span 上报、本地 `llm_calls`/`react_turns` 落库、`tool_events.duration_ms` 补齐、评测报告负向指标（延迟/token/回答长度/超时率）、rageval 增加 Precision@K 与 JSON 落盘、grader 结果挂 Langfuse score。
- 不在范围（后续独立 spec）：OpenTelemetry SDK 接入、前端 trace 展示与 `llm_calls` 查询 API、golden set 留出集/轮换、`eval-ci` 挂真实 CI workflow、`rageval -generate` 从真实会话出题、Langfuse prompt 管理。

## 1. 背景与目标

### 1.1 现状问题

对照业界 LLM 可观测实践（OTel GenAI 语义约定把 token 用量与耗时列为 Recommended，prompt/completion 全文为 Opt-In）与 RAG 评测方法（正向指标之外必须同时报告延迟、成本、超时率等负向指标），本项目存在四类缺口：

1. **LLM 层零打点**：`llm.Client.Complete`（`backend/internal/llm/llm.go:54`）直接 `Generate` 返回，无 token 用量、无耗时、无错误记录、不知道调用方是谁。它是全项目所有 LLM 调用的唯一咽喉（pipeline 各阶段、agent 意图解析与回复、ReAct、kb 富化、eval 评委都汇到这里），却完全没有观测数据。
2. **链路耗时缺失**：`StageResult` 有 `StartedAt/EndedAt` 但无显式 duration，`PipelineRun` 无总耗时；`tool_events.duration_ms` 列与 `model.ToolEvent.DurationMS` 字段已建好但三个写入点（`pipeline/evidence.go:31,36`、`context/engine.go:102`）都没赋值；ReAct 的 `ReActTurn` 明细只在内存（`context/react.go:26-32`），不落库也不进评测报告。
3. **评测报告缺负向维度**：`eval-report.json` 只有 pass@k/均分/stddev，无延迟分布、token 成本、回答长度、超时率——正向提升是否以负向退步为代价无从判断；`rageval` 只有 MRR/Recall@K/NDCG/RejectRate，缺 Precision@K，且结果只打 stdout 无法做趋势对比。
4. **离线评测与运行时数据割裂**：grader 结果只进独立报告文件，不挂在产生它的 run/trace 上，无法在平台里对照"这次 run 的 LLM 调用与评分"。

### 1.2 目标

1. **单一咽喉全覆盖**：所有 `llm.Client.Complete` 调用自动产生一条本地记录（模型、prompt/completion/total tokens、耗时、finish_reason、caller、run/会话归属、错误），无需各调用方改造。
2. **trace 三层模型**：run（或一次 chat 会话）= trace，stage/tool/agent 步骤 = span，LLM 调用 = generation；字段命名对齐 OTel GenAI（`gen_ai.usage.input_tokens` 等）。
3. **Langfuse 可选上报**：配置 `LANGFUSE_*` 即上报 Langfuse（自写轻量 ingestion 客户端，风格对齐 `retrieve/reranker.go` 的自写 HTTP 客户端约定），未配置时零外部请求、仅本地落库。
4. **评测负向指标**：eval 报告增加每 trial 耗时、LLM 调用次数/tokens、回答长度、超时率，配置级聚合 P50/P95 延迟；rageval 增加 Precision@K 并支持 `-o` JSON 落盘。
5. **评分挂 trace**：grader 结果以 score 形式同时写入本地（报告内）与 Langfuse（trace 级），打通"离线评分 ↔ 运行时 trace"。
6. `go test ./...` + `make eval-ci` 全绿；未配置 Langfuse 时全系统行为零变化。

## 2. 已确认决策

| # | 决策 | 理由 |
|---|---|---|
| D1 | 上报方案选**显式埋点 + 自写 Langfuse 客户端**（方案 B，见 §3.3），不用 eino-ext callbacks/langfuse 自动插桩 | 已核实：本项目 `llm.Client` 直调 `model.Generate`，而 eino-ext openai 组件直调路径只做 `callbacks.EnsureRunInfo`（`eino-ext/components/model/openai/chatmodel.go:252`），**不发射** `OnStart/OnEnd/OnError` 事件——自动插桩在本项目调用形态下拿不到任何数据；且 eino-ext 依赖 sonic 等重依赖。自写 client（~150 行，`net/http + encoding/json`）与 `embedder.go`/`reranker.go` 同风格，零第三方依赖 |
| D2 | Langfuse 关闭语义沿用凭据空值约定：`LANGFUSE_PUBLIC_KEY/SECRET_KEY` 为空即完全关闭，仅本地记录 | 与 `LLM_API_KEY` 为空 → LLM client 为 nil 的既有降级约定一致；测试与 CI 天然零外呼 |
| D3 | 本地表是唯一权威（source of truth），Langfuse 只是导出视图 | 上报失败/丢弃不影响本地功能；本地记录可随时重放 |
| D4 | 调用归属（run_id / conversation_id / caller）经 ctx 传播，注入点收敛在 `engine.Run`、`agent.Handle`、grader 三处，stage 内部零改动 | 避免 `Complete` 签名变更波及全部调用方 |
| D5 | 字段命名对齐 OTel GenAI 语义约定（`prompt_tokens`/`completion_tokens`/`total_tokens`/`duration_ms`/`finish_reason`） | 将来接任何平台（含 OTel）可平滑映射导出 |
| D6 | 本地记录全量、Langfuse 上报可采样（`LANGFUSE_SAMPLE_RATE` 默认 1.0） | SQLite 本地量级可控（sim 每 20s 一 run，每 run 数次 LLM 调用）；上报是外部成本 |
| D7 | prompt/completion 全文**默认不上报** Langfuse（`LANGFUSE_LOG_PAYLOAD=0` 默认），本地 `llm_calls` 也不存全文（只存元数据） | 对齐 OTel Opt-In 原则；运维话术可能含敏感上下文，开关显式打开才导出 |
| D8 | eval 的 LLM 用量聚合直接查 trial 临时库 `llm_calls`，不引入内存 collector | 每 trial 独立 DB（既有约定），按 run 聚合一条 SQL 即可，零新接口 |
| D9 | 负向指标聚合**排除 caller=judge** 的调用（评委耗时不计入被测系统） | 评的是系统不是评委；judge tokens 可单独统计 |
| D10 | `llm_calls` 由 llm 层经 raw `*store.Store` 写入（观测旁路），不经 `ScopedStore` | 观测数据无领域语义、只写不读（本期无查询端点）；scope 读写隔离约定针对领域数据，此处显式豁免并在代码注释说明 |
| D11 | `docker compose` 默认不含 Langfuse，提供 `--profile observability` 可选档 | 保持默认两容器克制栈；想看 UI 的人一条命令起 |
| D12 | trace ID 采用 `run.ID` / `conv-<id>`，eval 多 trial 场景加 `#t<n>` 后缀去重 | Langfuse 接受任意字符串 ID；同 alert 多 trial 的 run.ID 形如 `run-<alertID>` 会撞车 |

## 3. 总体架构

### 3.1 数据流

```
pipeline run / chat 会话
  ├─ engine.Run / agent.Handle
  │    ├─ observe.StartTrace（Langfuse 开启时上报 trace-create）
  │    ├─ 每 stage / agent 步骤 → observe.Span（span-create）
  │    │      └─ llm.Client.Complete ──→ observe.RecordLLM
  │    │             ├─ 本地：llm_calls 表（必写）
  │    │             └─ 上报：generation-create（可采样/可关）
  │    └─ run 结束 → trace end
  └─ eval grader → score-create（trace 级）+ 本地报告
```

### 3.2 新增/改动模块

| 模块 | 文件 | 职责 |
|---|---|---|
| 观测核心 | `internal/observe/observe.go`（新） | `CallMeta` ctx 传播、`Recorder`/`Exporter` 接口、Noop/Multi 组合 |
| 本地记录 | `internal/observe/local.go`（新） | SQLite `llm_calls` 写入器 |
| Langfuse | `internal/observe/langfuse.go`（新） | 自写 ingestion 客户端：批量内存队列、定时/手动 Flush、Basic Auth、失败静默丢弃 + 计数 |
| LLM 层 | `internal/llm/llm.go`（改） | `Complete` 计时、从 `schema.Message.ResponseMeta.Usage` 提取 tokens、ctx 取 `CallMeta`、调 `Recorder`；`WithRecorder` 派生构造器 |
| 引擎接线 | `internal/pipeline/engine.go`（改） | run 创建后 StartTrace + ctx 注入 `CallMeta{RunID}`；stage 循环内换 caller + Span 上报 |
| 会话接线 | `internal/agent/agent.go`（改） | `Handle` 注入 `CallMeta{ConversationID, Caller:"agent"}` + trace |
| ReAct | `internal/context/engine.go`（改） | ReAct 循环落 `react_turns` |
| 工具耗时 | `internal/pipeline/evidence.go`、`internal/context/engine.go`（改） | `AddToolEvent` 处补 `DurationMS` |
| 存储 | `internal/store/store.go`（改） | `llm_calls`/`react_turns` 建表 + 索引 |
| 评测 | `internal/eval/runner.go`/`report.go`（改） | trial 计时、`llm_calls` 聚合、报告负向指标 |
| 检索评测 | `cmd/rageval/main.go`/`metrics.go`（改） | Precision@K、`-o` JSON 落盘 |
| 配置 | `internal/config/config.go`、README 环境变量表、`.env.example`、`docker-compose`（改） | 新增 `LANGFUSE_*` |

### 3.3 方案对比（为什么是 B）

| | A：eino-ext callbacks/langfuse 自动插桩 | **B：显式埋点 + 自写 Langfuse client（采用）** | C：纯本地不上报 |
|---|---|---|---|
| 对本项目调用形态 | **失效**：直调 `Generate` 不发 callbacks；需再包 wrapper 补发事件才能工作 | 咽喉点一处封口，全调用方自动覆盖 | 同 B |
| trace 树能力 | 受 handler 能力限制（trace 归属靠 ctx SetTrace，stage/tool span 需另做） | trace/span/generation/score 完全自主，与本地概念一一对应 | 无 |
| 依赖 | eino-ext monorepo + sonic | 零新第三方依赖 | 零 |
| 成本 | wrapper + 适配 handler 行为不确定性 | ~150 行 client + 三处接线 | 最小 |
| 结果 | — | Langfuse UI 免费获得 trace 瀑布/用量/评分视图 | 只有 SQL，无 UI |

## 4. 数据模型

`store.migrate()` 一律 `CREATE TABLE IF NOT EXISTS`，兼容存量库。

### 4.1 新表 `llm_calls`

```sql
CREATE TABLE IF NOT EXISTS llm_calls (
  id TEXT PRIMARY KEY,
  run_id TEXT DEFAULT '',
  conversation_id TEXT DEFAULT '',
  caller TEXT DEFAULT '',          -- pipeline:<stage_id> | agent-intent | agent-reply | react | kb-enrich | judge
  model TEXT DEFAULT '',
  prompt_tokens INTEGER DEFAULT 0,        -- 对齐 gen_ai.usage.input_tokens
  completion_tokens INTEGER DEFAULT 0,    -- 对齐 gen_ai.usage.output_tokens
  total_tokens INTEGER DEFAULT 0,
  duration_ms INTEGER DEFAULT 0,
  finish_reason TEXT DEFAULT '',          -- 对齐 gen_ai.response.finish_reasons（取首值）
  status TEXT DEFAULT 'ok',               -- ok | error
  error TEXT DEFAULT '',
  created_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_llm_run ON llm_calls(run_id, created_at);
CREATE INDEX IF NOT EXISTS idx_llm_conv ON llm_calls(conversation_id, created_at);
```

- tokens 来源：`schema.Message.ResponseMeta.Usage`（`TokenUsage{PromptTokens, CompletionTokens, TotalTokens}`）。OpenAI 兼容实现未回传 usage 时记 0、不视为错误。
- `caller` 缺省 `unknown`（如 kb 富化等未逐点注入的场景二期补）。

### 4.2 新表 `react_turns`

```sql
CREATE TABLE IF NOT EXISTS react_turns (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL,
  turn INTEGER NOT NULL,
  thought TEXT DEFAULT '',
  tool TEXT DEFAULT '',
  args_json TEXT DEFAULT '{}',
  result_json TEXT DEFAULT '',
  status TEXT DEFAULT 'ok',
  duration_ms INTEGER DEFAULT 0,
  created_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_react_run ON react_turns(run_id, turn);
```

- 写入点：`context/engine.go` ReAct 循环每 turn 结束（替代仅内存的 `ReActTurn` 收集，内存结构保留用于返回值）。
- `eval` 的 `RunOutcome` 增加 `ReActTurns`（从 trial 库读取），供报告与后续行为分析。

### 4.3 既有表增改

- `tool_events.duration_ms`：三个写入点补 `time.Since(start).Milliseconds()`（工具执行前后计时，`status=error` 同样记录）。
- 不改 `stage_results`/`pipeline_runs` 表结构：本地耗时统一以 `llm_calls` + trial 计时呈现，stage 耗时由 `EndedAt-StartedAt` 推导（避免列冗余）；Langfuse 侧 span 自带起止时间。

## 5. 观测核心（`internal/observe`）

### 5.1 归属传播与接口

```go
type CallMeta struct {
    RunID, ConversationID, Caller string
}
func WithCallMeta(ctx context.Context, m CallMeta) context.Context
func MetaFromCtx(ctx context.Context) (CallMeta, bool)

type LLMCall struct {   // 一次 Complete 调用的观测快照
    model.CallMeta
    Model         string
    PromptTokens  int
    CompletionTokens int
    TotalTokens   int
    DurationMS    int64
    FinishReason  string
    Status        string // ok | error
    Error         string
}

type Recorder interface { RecordLLM(ctx context.Context, c LLMCall) }
// MultiRecorder(recorders ...Recorder)：本地 + 上报组合
```

### 5.2 Langfuse exporter（`observe/langfuse.go`）

自写 ingestion 客户端，对接 `POST {host}/api/public/ingestion`（Basic Auth `pk:sk`，批量事件数组）：

| 本地概念 | ingestion event | 关键 body 字段 |
|---|---|---|
| pipeline run / chat 会话 | `trace-create`（id=`run.ID` / `conv-<id>`，见 D12） | name、sessionId=device 或 conversation、metadata{device,metric,iteration} |
| stage / agent 步骤 | `span-create`（parent=trace） | name=stage_id/步骤名、startTime/endTime、level、statusMessage=error |
| `llm.Complete` | `generation-create`（parent=当前 span，无 span 则挂 trace） | model、usage{input,output,unit:"TOKENS"}、latency、finishReason、caller 放 metadata |
| grader 结果 | `score-create`（trace 级） | name=GraderName、value、dataType:"NUMERIC" |

行为约定：

- 内存批量队列 + `FlushInterval`（默认 1s）定时与手动 `Flush()`；进程退出路径必须 flush（含 `main.go:107` 的 `log.Fatal` 错误分支——现有 `defer` 在 `os.Exit` 不执行，需改为显式 flush 后退出）。
- 请求失败：静默丢弃并累加计数，计数非零时周期性 `log.Printf` 汇总（沿用"失败静默降级"约定）。
- 采样：`SampleRate` 按 trace 粒度采样（整条 trace 全有或全无，避免断树）。
- `LANGFUSE_LOG_PAYLOAD=0`（默认）时 input/output/metadata 不含 prompt/completion 全文。

### 5.3 接线点

- `engine.Run`：run 创建后 `ctx = observe.WithCallMeta(ctx, CallMeta{RunID: run.ID, Caller: "pipeline"})` + StartTrace；stage 循环内 stage ctx 换 `Caller: stage.StageID()` + Span 起止上报。triage skipped / 协调器锁 degraded 等提前返回路径同样结束 trace（status 体现到 metadata）。
- `agent.Handle`：`CallMeta{ConversationID, Caller:"agent"}` + trace；意图解析子步骤 caller=`agent-intent`、回复 caller=`agent-reply`。
- `grader.Grade`：评委调用 ctx 注入 `Caller:"judge"`。
- `kb` 富化调用点：`Caller:"kb-enrich"`。
- `cmd/server/main.go`：调整构造顺序（store 先于 llm client），`lc = lc.WithRecorder(observe.MultiRecorder(local, exporter))`；eval 的 `RunTask` 内 `cl = lc.WithRecorder(observe.NewLocalRecorder(st))`（trial 临时库）。不 Attach 时为 Noop，测试零影响。

## 6. 评测报告负向指标

### 6.1 `TrialResult` 扩展

```go
type LLMUsage struct {
    Calls, PromptTokens, CompletionTokens, TotalTokens int
    DurationMS int64
}
type TrialResult struct {
    // ...现有字段
    DurationMS int64     // RunTask 内围计时（引擎执行/chat 处理，不含评分）
    LLM        LLMUsage  // trial 库 SUM(llm_calls)，排除 caller='judge'
    ReplyLen   int       // chat 对象：len([]rune(Reply))
    Timeout    bool      // ctx 超时错误
}
```

### 6.2 聚合口径

`Stats` 增加 `Negative *NegativeStats`（overall/by_config 填充，任务级省略）：

```go
type NegativeStats struct {
    LatencyP50MS, LatencyP95MS float64 // trial DurationMS 分布
    TokensMean                 float64 // 每 trial 总 tokens（rule 配置恒 0）
    ReplyLenMean               float64
    TimeoutRate                float64
    LLMDurationP95MS           float64 // 单次 LLM 调用耗时 P95（来自 llm_calls）
}
```

- stdout 摘要追加一行负向指标，便于人工对比"正向提升是否以负向恶化换"。
- P50/P95 为纯函数（排序 + 线性插值），独立单测。

## 7. rageval 变更

- **Precision@K**：`hit` 题按 `expect.doc_ids` 与返回前 K chunk 所属 doc 的交集 / K 计算（K 默认取题目 `top_k`，与 Recall@K 同口径），分题型与总表输出；`reject` 题不参与。
- **JSON 落盘**：`-o out.json` 输出 `{meta:{ts,enhance,top_k}, overall:{...}, by_type:{...}}`，结构与 `eval-report.json` 风格一致，供趋势对比。
- 顺带在结果表输出 `fingerprint_hit` 快路径命中率与 `rerank_used` 统计（从 Searcher 行为推导，仅评测态打印）。

## 8. 配置

`backend/internal/config` 新增（README 环境变量表同步）：

| env | 默认 | 说明 |
|-----|------|------|
| `LANGFUSE_HOST` | 空 | Langfuse 地址；空 = 不上报（仅本地记录） |
| `LANGFUSE_PUBLIC_KEY` | 空 | 公钥；空 = 不上报 |
| `LANGFUSE_SECRET_KEY` | 空 | 私钥 |
| `LANGFUSE_SAMPLE_RATE` | `1.0` | trace 级采样率 0~1 |
| `LANGFUSE_LOG_PAYLOAD` | `0` | 是否上报 prompt/completion 全文（Opt-In） |

`.env.example` 追加注释示例；`docker-compose.yml` 增加 `observability` profile（langfuse server + postgres），默认不启。

## 9. 错误处理与边界

- **usage 缺失**：OpenAI 兼容端点未回传 usage → 记 0，不报错、不阻塞业务路径。
- **Recorder 故障**（本地写库失败）：忽略并 `log.Printf`，绝不影响 `Complete` 返回值（观测旁路原则）。
- **Langfuse 不可达**：队列丢弃 + 计数，不重试阻塞（`MaxRetry` 仅对单批请求做 2 次退避）。
- **eval 无 LLM key**：`llm_calls` 无记录，`LLM.Calls=0`，`Negative.TokensMean=0`——报告结构仍完整。
- **run 提前返回**（triage skipped / 锁超时 degraded）：trace/span 正常闭合，status 进 metadata。
- **并发**：exporter 队列单 goroutine 消费，SQLite 写入受既有 `SetMaxOpenConns(1)` 串行保护，不新增连接。

## 10. 测试计划（TDD，每任务独立可验证）

| # | 任务 | 失败测试先行 | 通过标准 |
|---|---|---|---|
| 1 | observe ctx 传播 + Noop/Multi | `observe_test.go: TestCallMetaRoundTrip` | 无 meta 时 `MetaFromCtx` 返回 false |
| 2 | llm.Complete 埋点 | `llm_test.go: TestCompleteRecordsUsage`（fake ChatModel 返回带 `ResponseMeta.Usage` 的 Message；httptest 模拟 OpenAI 兼容端点） | tokens/duration/finish_reason/error 正确；Recorder 故障不影响返回 |
| 3 | store 迁移 + LocalRecorder | `store/observe_test.go` | 存量 DB 无损升级；索引生效 |
| 4 | engine 接线 | `engine_observe_test.go`（fake stage 断言 ctx meta + fake exporter 断言事件序列） | stage caller 逐段变化；提前返回路径 trace 闭合 |
| 5 | agent 接线 + ReAct 落库 | `agent_observe_test.go`、`context/react_turn_test.go` | conversation_id 注入；turn 数与内存结构一致 |
| 6 | tool duration 补齐 | `evidence_test.go` 追加断言 | `duration_ms > 0`，error 路径同样 |
| 7 | eval 负向指标 | `report_test.go`（P50/P95 纯函数 + 聚合排除 judge） | rule 配置 tokens=0；judge 调用不计入 |
| 8 | rageval Precision@K + JSON | `cmd/rageval` 单测 | 固定 scores 下 precision 计算正确；-o 落盘 |
| 9 | Langfuse 客户端 | `observe/langfuse_test.go`（httptest 模拟 ingestion：批量格式、Basic Auth、失败静默、Flush） | 事件映射符合 §5.2；采样整 trace 生效 |
| 10 | 全量回归 | `go test ./...` + `make eval-ci` | 全绿；未配置 Langfuse 时 e2e 行为零变化 |

## 11. 里程碑（每期独立交付、独立回归）

- **M1 本地记录通道**：任务 1-6（llm_calls/react_turns/ctx 传播/耗时补齐）。交付后 `llm_calls` 已可 SQL 分析，Langfuse 可暂不接。
- **M2 评测报告增强**：任务 7-8（负向指标 + Precision@K + JSON）。交付后`eval-report.json`/rageval 支持趋势对比。
- **M3 Langfuse 上报**：任务 9 + exporter 接线（engine/agent/grader score）+ 配置/compose/README。交付后在 Langfuse UI 看 trace 瀑布、用量与评分。

## 12. 成功标准

1. 一次完整流水线 run：`llm_calls` 行数 = LLM 调用次数，tokens 总和与供应商账单口径一致（抽查）；Langfuse UI 出现 1 trace + N stage spans + N generations + 4 条 score（M3 后）。
2. `eval-report.json` 含 `negative` 区块；同一任务集对比时可见 rule 与 llm 配置的延迟/tokens 差异。
3. `rageval` 输出 Precision@K 且 `-o` JSON 可 diff。
4. `LANGFUSE_*` 全空时：`go test ./...`、`make eval-ci` 全绿，服务行为与现版本无差异（除新表写入）。

## 13. 明确不做（YAGNI）

- 不引 OpenTelemetry Go SDK / otel-collector（字段对齐即可，保留导出平滑性）。
- 不做通用 span 树框架，仅固定三层映射（trace/span/generation）。
- 不做前端 trace 页与 `llm_calls`/`react_turns` 查询 API（二期）。
- 不做 golden 留出集/轮换、CI workflow 挂接、`-generate` 真实会话出题（各自独立 spec）。
- 不启用 Langfuse prompt 管理/标注队列（单人开发，自研 eval 已覆盖）。
- 不做检索层事件全量表（rerank 失败仅 slog 一行 + rageval 统计列）。

## 14. 风险与备注

- Langfuse ingestion API 字段以官方文档为准，任务 9 的 httptest 契约测试先行固化映射；若 API 变更仅改 observe 单点。
- `eino-ext` 若未来在直调路径补发 callbacks，可评估切回官方 handler（D1 重议），本地记录通道不受影响。
- trace 级采样下，generation 的 parentObservationID 依赖 ctx 中的 span id——ctx 断链（如 goroutine 旁路）时 generation 挂 trace 根，不允许丢调用记录（本地必写）。
