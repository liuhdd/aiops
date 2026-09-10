# Phoenix 评测数据集与实验回写设计

日期：2026-09-10
状态：已确认（brainstorm 澄清 + 方案 A + 分节设计经用户确认；采纳 Extended 缓解、一次做完含 RAG）
依赖：Phoenix Prompt Hub 提示词管理（`2026-09-09-phoenix-prompt-management-design.md`，其分支 `feat/phoenix-prompt-management` 需先合并到 main）

## 1. 背景与目标

现有 Phoenix 集成只托管提示词（Prompt Hub）。项目另有两类评测资产，均只落本地文件、无平台视图：

1. **Agent 评测任务集** `eval.DefaultTasks()`：19 条 pipeline/chat/context 任务，含输入与无歧义 ground-truth，由 `cmd/eval` 跑、`cmd/eval ci` 门禁。
2. **检索 golden set** `backend/testdata/golden_set.jsonl`：32 条 query + 期望 doc_ids/reject，由 `cmd/rageval` 跑。

目标：

1. 把两类评测资产上传为 **Phoenix Datasets**（在 Phoenix UI 可见、可筛分、可版本化）。
2. 把本地 `cmd/eval` / `cmd/rageval` 的评测结果作为 **Phoenix Experiment + runs + evaluations** 回写，在 Phoenix UI 做配置/版本对比。
3. 为两类评测集各新增 **20+ 条手工校准题**，并入现有评测资产（本地评测与 Phoenix Dataset 同源）。
4. 新增独立命令 `cmd/datasetctl`（风格对齐 `cmd/promptctl`）承担 push/list/experiment；`cmd/eval`/`cmd/rageval` 不自动外呼。
5. `PHOENIX_BASE_URL` 未配置时，服务与测试行为零变化。

## 2. 与既有 spec 的关系

- `2026-09-09-phoenix-prompt-management-design.md`：本设计复用其已确立的「本地权威 + Phoenix 导出视图 + 未配置零外呼」哲学，但**不复用** `internal/prompt.PhoenixFetcher`——该包禁止 import 任何 `aiops/internal/*` 且仅负责提示词，塞入 datasets/experiments 会污染职责。新增独立 `internal/phoenix` 客户端。
- `2026-09-09-llm-observability-design.md`（待确认、未实现）：该 spec 计划扩展 `TrialResult`（负向指标）并将评分挂 trace（Langfuse）。本设计仅给 `TrialResult` 加 `StartedAt/EndedAt` 供 Phoenix run 计时；若将来该 spec 落地，字段不冲突（`StartedAt/EndedAt` 语义一致）。
- `2026-08-27-retrieval-quality-design.md`：`golden_set.jsonl` 与本设计新增题目沿用其题型约定（8 类 type 与 hit/reject 行为）。

## 3. 已确认决策

| # | 决策 | 理由 |
|---|---|---|
| D1 | 新增独立 `internal/phoenix` 纯 REST 客户端，不扩展 `internal/prompt` | 职责单一；prompt 包有「禁止 import internal」约束，天然不可共享 |
| D2 | 新增 `internal/goldenset`，把 `GoldenItem` 及解析从 `cmd/rageval` 迁出 | `cmd/rageval` 是 `package main` 不可被 `datasetctl` 导入；迁移同时消除解析复制 |
| D3 | 一个 config 建一个 Phoenix experiment（agent 侧） | Phoenix 实验=被测系统变体；`rule`/`llm-single`/`llm-react` 并排对比，且避免同 example+repetition 撞车 |
| D4 | 实验一律本地 Go 跑，Phoenix 只存 runs/scores | Phoenix 无法执行本项目 Go 任务；回写贴近「导出视图」哲学 |
| D5 | 数据集 `output` 存作者编写的原始 id（mock 概念 id），实验评测用解析后真实 id | 数据集是人类可读的 ground-truth 文档；评测口径独立，不混用 |
| D6 | `push` 幂等：先 `action=create`，遇 409 改 `action=update` + 稳定 `example_ids` | Phoenix 上传支持 `example_ids` 原地 upsert；避免每次 push 版本爆炸 |
| D7 | 复用现有 `PHOENIX_BASE_URL` / `PHOENIX_API_KEY`，不新增 env | 数据集工具与提示词工具指向同一 Phoenix；YAGNI |
| D8 | `Task` 加 `Extended bool`；`cmd/eval ci` 默认跳过 Extended，`--all` 强制 | 20+ 新题若全进 CI，LLM 配置 ×3 trial 会显著拉长耗时与 token 成本；`eval run`/Phoenix 数据集仍全量 |
| D9 | 关系映射用 `metadata` 反查 example id | `UploadDataset` 响应只回 dataset/version 与计数，不回逐条 id；`ListExamples` 回 metadata，按 `task_id`/`id` 匹配最稳 |

## 4. 总体架构与数据流

```
eval.DefaultTasks() ─┐                          ┌─ Phoenix UI: Datasets
                     ├─► internal/phoenix ──────┤
golden_set.jsonl ────┘   (REST client)          └─ Phoenix UI: Experiments + scores
        ▲                        ▲
   internal/goldenset       cmd/datasetctl
                                 ▲
        cmd/eval -report ────────┤  (读到报告后回写)
       cmd/rageval -o ───────────┘
```

- 单向：本地是唯一权威（source of truth），Phoenix 只是导出视图。回写失败不阻塞本地评测。
- 运行时 `cmd/server` **完全不碰** datasets/experiments；`PHOENIX_*` 仅 `internal/prompt` 注册中心在 server 侧使用。未配置 → 零外呼、零行为变化。

## 5. Phoenix REST 端点（已核实）

自托管 Phoenix，全部挂在 `/v1` 下，认证 `Authorization: Bearer <PHOENIX_API_KEY>`（自托管默认无鉴权，key 空则不带）。

| 用途 | 方法与路径 | 请求/响应要点 |
|---|---|---|
| 上传数据集 | `POST /v1/datasets/upload?sync=true` | body `{action: create\|append\|update, name, description?, inputs[], outputs[], metadata[], splits[], example_ids[]}`（各数组按 example 平行）；resp `{data:{dataset_id, version_id, num_created_examples, num_updated_examples, num_deleted_examples}}`；`action=create` 且重名返回 409 |
| 列数据集 | `GET /v1/datasets` | resp `{data:[{id,name}]}`（形状实现时以 Phoenix OpenAPI/实测校准） |
| 列例子 | `GET /v1/datasets/{id}/examples` | resp `{data:{dataset_id, version_id, examples:[{id, node_id, input, output, metadata, updated_at}]}}` |
| 建实验 | `POST /v1/datasets/{dataset_id}/experiments` | body `{name, description?, metadata?, version_id?, repetitions}`；resp `{data:{id, ...}}` |
| 记 run | `POST /v1/experiments/{experiment_id}/runs` | body `{dataset_example_id, output, repetition_number, start_time, end_time, trace_id?, error?}`；resp `{data:{id}}` |
| 记评分 | `POST /v1/experiment_evaluations` | body `{experiment_run_id, name, annotator_kind: LLM\|CODE\|HUMAN, start_time, end_time, result?:{label?,score?,explanation?}, error?, metadata?, trace_id?}`（upsert on run+name） |

注：Phoenix v14（2026-04）移除了旧的 `/v1/evaluations`（span evals），实验评分走 `POST /v1/experiment_evaluations`，本设计只用后者。

## 6. 包与职责

### 6.1 新增 `internal/phoenix`（纯 REST 客户端）

零 `aiops/internal/*` 依赖、零新增第三方依赖（仅标准库 `net/http`/`encoding/json`），风格对齐 `prompt/phoenix.go`。

```go
type Client struct {
    BaseURL string        // 如 http://localhost:6006
    APIKey  string        // 非空时带 Bearer
    HTTP    *http.Client
}
func NewClient(baseURL, apiKey string) *Client

type Example struct {
    ID       string         // 上传时为稳定公开 id（example_ids）；ListExamples 返回时为可用于 run 的 dataset_example_id
    Input    map[string]any
    Output   map[string]any
    Metadata map[string]any
    Splits   []string
}
type UploadResult struct { DatasetID, VersionID string; NumCreated, NumUpdated int }
type Dataset struct { ID, Name string }
type Experiment struct { ID, DatasetID, DatasetVersionID, Name string }
type RunRequest struct {
    DatasetExampleID string
    Output           any
    RepetitionNumber int
    StartTime, EndTime time.Time
    TraceID, Error     string
}
type EvaluationRequest struct {
    ExperimentRunID, Name, AnnotatorKind string // AnnotatorKind: LLM | CODE | HUMAN
    StartTime, EndTime                     time.Time
    Score                                  *float64 // pointer 以区分 0 与缺省
    Label, Explanation, Error              string
    Metadata                               map[string]any
}

func (c *Client) UploadDataset(action, name, description string, exs []Example) (UploadResult, error)
func (c *Client) ListDatasets() ([]Dataset, error)
func (c *Client) ListExamples(datasetID, versionID string) ([]Example, error) // versionID 可空=最新
func (c *Client) CreateExperiment(datasetID, name string, repetitions int, metadata map[string]any) (Experiment, error)
func (c *Client) LogRun(experimentID string, r RunRequest) (runID string, error)
func (c *Client) LogEvaluation(e EvaluationRequest) (evalID string, error)
```

- 超时：上传/实验默认 10s，列表 2s（可字段覆盖）。
- 任何非 2xx 返回带状态码与响应片段的 error；`UploadDataset` 遇 409 返回可判别错误（供上层退 `update`）。
- `RunRequest`/`EvaluationRequest` 用 `time.Time`，序列化为 RFC3339。

### 6.2 新增 `internal/goldenset`

从 `cmd/rageval/main.go` 迁出：

```go
type Item struct { ID, Type, Question string; Expect Expect; TopK int }
type Expect struct { DocIDs, DistractorDocIDs []string; Behavior string }
func Load(path string) ([]Item, error)          // JSONL，含默认值归一（TopK=3、behavior 兜底）
func ToPhoenixExamples(items []Item) []phoenix.Example
```

`cmd/rageval` 改为 import `internal/goldenset`；行为不变（`resolveGoldenPath` 等保留在 cmd）。

### 6.3 扩展 `internal/eval`

```go
const AgentDatasetName = "aiops-agents-eval"
func AgentDatasetExamples() []phoenix.Example

// PhoenixWriter 回写所需的最小客户端接口（*phoenix.Client 实现），便于 fake 注入测试。
type PhoenixWriter interface {
    ListExamples(datasetID, versionID string) ([]phoenix.Example, error)
    CreateExperiment(datasetID, name string, repetitions int, metadata map[string]any) (phoenix.Experiment, error)
    LogRun(experimentID string, r phoenix.RunRequest) (string, error)
    LogEvaluation(e phoenix.EvaluationRequest) (string, error)
}
func PushAgentExperiment(w PhoenixWriter, datasetID string, report Report, cfgFilter []string) error
```

- `Task` 增 `Extended bool`（见 §10）。
- `TrialResult` 增 `StartedAt, EndedAt time.Time`；`cmd/eval` 在 `RunTask`+`Grade` 前后计时并写回（`Report` 自动带出）。
- `datasetctl` 的 `push` 幂等逻辑抽为接受注入接口的可测 helper（`phoenix.Client` 实现接口），409 退 `update` 的单测在 `cmd/datasetctl` 用 fake 覆盖。

### 6.4 新增 `cmd/datasetctl`

见 §9。

## 7. 数据集定义与映射

### 7.1 `aiops-agents-eval`（来源 `eval.DefaultTasks()`）

| 字段 | 内容 |
|---|---|
| `example_id` | `t.ID`（如 `cpu-hot-compute-01`） |
| `input` | pipeline/context：`{"object":"pipeline"\|"context","alert":{"device","metric","severity","message"}}`；chat：`{"object":"chat","chat_input":t.ChatInput}` |
| `output` | `{"root_cause_keywords":[...],"log_keywords":[...],"no_gaps":bool,"runbook":"...","intent":"...","intents":[...],"tool_use":[...]}`（空字段省略） |
| `metadata` | `{"task_id":t.ID,"object":t.Object,"configs":[...]}` |
| `splits` | `[t.Object]` |

### 7.2 `aiops-rag-golden`（来源 `testdata/golden_set.jsonl`）

| 字段 | 内容 |
|---|---|
| `example_id` | `item.ID`（如 `q001`） |
| `input` | `{"question":item.Question,"top_k":item.TopK}` |
| `output` | `{"doc_ids":[...],"distractor_doc_ids":[...],"behavior":"hit"\|"reject"}` |
| `metadata` | `{"id":item.ID,"type":item.Type}` |
| `splits` | `[item.Type]` |

## 8. 实验回写映射

### 8.1 Agent eval（每 config 一 experiment）

- 实验：`name = "<prefix>/<config>"`（`--name` 前缀默认 `aiops-agents-eval`），`metadata = {config, ts, llm_enabled}`，`repetitions = 该 config 最大 trial 数`。
- 反查映射：`ListExamples(datasetID)` → `metadata.task_id → example.id`。
- 每个 trial → 一条 run：
  - `dataset_example_id`（按所属 `TaskReport.ID` 反查，`TrialResult` 本身无 task 字段）
  - `output = {run_status, rca, recommend, reply, intent, intents, err_msg}`
  - `error = err_msg`（空则省略）
  - `start_time/end_time = trial.StartedAt/EndedAt`（零值回退报告 `meta.ts`）
- 每个非 NA `GraderResult` → 一条 evaluation：
  - `name = s.Name`，`annotator_kind = LLM`（`JudgeAccuracy`/`JudgeGroundedness`）否则 `CODE`
  - `result = {score: s.Score, label: PASS|FAIL, explanation: s.Detail}`，`metadata = {weight: s.Weight}`
- 另加总体评测：`name="WeightedScore"`，`score=trial.Score`，`label=PASS|FAIL`。

### 8.2 RAG（一 experiment）

- 实验：`name = "<prefix>/rag"`（默认前缀 `aiops-rag-golden`），`metadata = {enhance, top_k, ts}`，`repetitions=1`。
- 每个 golden item → 一条 run：`output = {ranked_doc_ids:[...]}`，`error` 空或取报告项错误；`start/end` 取报告项计时。
- evaluations（CODE）：`MRR / NDCG@K / Recall@K / Precision@K` 各自 `score`；reject 题加 `RejectOK`（1/0）。
- 需 `cmd/rageval -o` 落盘结构化报告（§9.3）。

## 9. CLI 设计

### 9.1 `cmd/datasetctl`

```
datasetctl push       [--dataset agents|rag|all]   # 默认 all
datasetctl list
datasetctl experiment --dataset agents --report eval-report.json [--name <prefix>] [--config <cfg>]
datasetctl experiment --dataset rag    --report rageval-report.json [--name <prefix>]
```

- 配置：`config.Load()` 的 `PHOENIX_BASE_URL`/`PHOENIX_API_KEY`；`BaseURL` 为空 → 明确报错、退出码非 0。
- `push`：构建 examples → `action=create`；409 → `action=update`（带 `example_ids`）重试，打印 `dataset_id` 与增改计数。
- `experiment --dataset agents`：读 `eval.Report`，可选 `--config` 过滤；每 config 建实验 + 回写 runs/evals；打印实验 ID 汇总。
- `experiment --dataset rag`：读 rageval 报告，建单实验 + 回写。
- 与 `promptctl` 同风格（薄封装，逻辑在 `internal/*`）。

### 9.2 `cmd/eval` 改动

- `TrialResult` 计时：`cmdRun`/`cmdCI` 中在 `RunTask`+`Grade` 前后打点写回 `StartedAt/EndedAt`。
- `ci` 子命令默认跳过 `Extended` 任务，新增 `--all` 强制全量；`run` 不变（全量）。

### 9.3 `cmd/rageval` 改动

- 新增 `-o <path>`：输出结构化报告 `{meta:{ts,enhance,top_k}, items:[{id,type,question,top_k,ranked_doc_ids,behavior,mrr,ndcg,recall,precision,reject_ok,start,end}], overall:{...}}`（JSON，风格对齐 `eval-report.json`）。
- 报告类型放 `internal/goldenset`（`Report`/`ItemResult`），供 `datasetctl` 复用；不落盘时行为不变。

## 10. 新增评测题扩充

- **Agent**：向 `DefaultTasks()` 增 20+ 条，覆盖 pipeline 新/边界场景与 chat 多意图、反例；新题 `Extended=true`。约束：满足 `ValidateTasks`（pipeline device+metric 唯一）、对齐 `mockdata` 场景/日志/runbook（规则路径可确定）或明确声明 LLM-only 期望。
- **RAG**：向 `testdata/golden_set.jsonl` 增 20+ 条，覆盖 8 类题型；`doc_ids` 用 mock 概念 id（经 `mockdata.Docs()` title 可解析），不得引用不存在文档。
- **CI 口径**：`eval run` 与 Phoenix 数据集全量包含新题；`make eval-ci`（`ci` 子命令）默认跳过 Extended，保持 CI 耗时可控。

## 11. 配置、部署与降级

- 复用 `PHOENIX_BASE_URL`、`PHOENIX_API_KEY`（README 既有表格，无需新增）。
- `PHOENIX_BASE_URL` 空：`datasetctl` 报错退出；server/测试零外呼、零行为变化。
- 回写失败：`datasetctl` 打 warn、按项继续、末尾汇总失败数；不重试阻塞（与 prompt 拉取同款静默降级哲学）。
- 网络超时：上传/实验 10s，列表 2s。
- docker-compose 无需改动（`phoenix` 服务与 backend 指向已由 prompt 特性引入）。

## 12. 测试策略

| 层 | 测试 | 通过标准 |
|---|---|---|
| `internal/phoenix` | httptest 假 Phoenix：6 个端点成功/非 2xx/坏响应/超时；断言请求体 schema（upload 数组平行、run、evaluation upsert） | 请求/响应映射符合 §5；409 可判别 |
| `internal/goldenset` | 加载解析、默认值归一、`ToPhoenixExamples` 字段映射 | 与 golden 文件一致；行为兜底正确 |
| `internal/eval` | `AgentDatasetExamples()` 条数/字段；report→runs/evals 映射（fake client 断言调用序列与 annotator_kind） | 每 trial 1 run、每 grader 1 eval、总体 1 eval |
| `cmd/datasetctl` | 参数解析；空 BaseURL 报错；`push` 幂等（409→update） | 退出码与调用序列正确 |
| `cmd/rageval` | `-o` JSON 结构与 `internal/goldenset.Report` 一致 | 可被 datasetctl 解析 |
| 回归 | `go build ./... && go test ./...`；`PHOENIX_BASE_URL` 空跑 e2e | 全绿，行为零变化 |

## 13. 里程碑（一次交付，但按此顺序实现，每段独立可测）

- **M0** 合并 `feat/phoenix-prompt-management` 到 main，基于 main 建 worktree `.worktrees/phoenix-eval-datasets`（分支 `feat/phoenix-eval-datasets`）。
- **M1** `internal/phoenix` 客户端（TDD）。
- **M2** `internal/goldenset` 抽取 + `internal/eval.AgentDatasetExamples` + `cmd/datasetctl push/list`。
- **M3** `internal/eval.PushAgentExperiment` + `cmd/datasetctl experiment --dataset agents`（含 `TrialResult` 计时）。
- **M4** `cmd/rageval -o` + `cmd/datasetctl experiment --dataset rag`。
- **M5** 两类评测集各增 20+ 题 + `Task.Extended` + `ci --all` 口径调整。
- **M6** 全量回归 + README/AGENTS.md 补充 `internal/phoenix`、`internal/goldenset`、`datasetctl` 说明。

## 14. 明确不做（YAGNI）

- 不由 Phoenix 侧执行任务（实验一律本地 Go 跑）。
- 不反向从 Phoenix 拉数据集作为本地评测输入。
- 不做 tracing/span 上报、span/trace annotations API、前端 Phoenix 页面。
- 不新增 env；不改 server 运行时；不引第三方依赖。

## 15. 风险与备注

- **Phoenix schema 以实测为准**：`GET /v1/datasets` 列表形状与 `upload` 的 `action`/`example_ids` 幂等行为需在实现时用真实 Phoenix（compose 起）校准；契约测试先行固化，若 API 变更仅改 `internal/phoenix` 单点（同 promptctl push 曾据实测修正）。
- **新题质量风险**：20+ pipeline 题若规则路径无确定 ground-truth 会拉低 `eval run` 正分；实现时逐题对照 mockdata，失败题要么修正期望要么归入 Extended 并声明 LLM-only。
- **CI 耗时**：Extended 缓解只覆盖 CI；`eval run` 全量 + LLM ×3 trial 仍偏慢，属预期（本地/按需执行）。
- **报告兼容**：`TrialResult` 新增时间戳为追加字段，旧报告 JSON 反序列化后为零值，回写时回退 `meta.ts`。
