# RAG 查询链路可插拔与变体矩阵评测设计（Phase 1：骨架 + 查询侧）

- 日期：2026-09-14
- 状态：已确认
- 依赖：检索质量（`2026-08-27-retrieval-quality`）、知识库混合检索（`2026-08-20-knowledge-hybrid-retrieval`）、Agent 评测体系（`2026-08-12-agent-evaluation`）、Phoenix 数据集（`2026-09-10-phoenix-eval-datasets`）
- 范围：把查询侧检索链路的各环节（指纹快路径、查询改写、多路召回、融合、相关性门、重排、截断）拆解为可注册的 Link，并让 `rageval` 支持一次运行多变体、横向对比链路工程改动收益。入库侧环节（解析切块、向量化、富化/CDTA）与父子「二次检索」因需改 schema、重建索引，另起 spec。
- 不在范围：入库侧变体、Parent-Child 二次检索、运行时热切换、ANN/持久化索引、除 pipeline/chat/rageval 外的自定义 profile、通用插件/动态加载。

## 1. 背景与目标

### 1.1 现状问题

1. **链路无阶段抽象**：查询侧整条链封在 `backend/internal/retrieve/searcher.go:118` 的 `searcher.search()` 内，指纹 → 召回 → 融合 → 相关性门 → rerank → 截断全部是私有方法直调，顺序写死。
2. **能力接口只有「最终模型」级**：`Embedder`（`retrieve/embedder.go:20`）、`Reranker`（`retrieve/reranker.go:20`）、`Searcher`（`retrieve/searcher.go:33`）可注入，但无「策略」级接口（Recall/Fuse/Rewrite/Gate）；融合权重、RRF k、向量相关门限、rerank 候选数等全为包级常量。
3. **评测无法切换配置**：`cmd/rageval/main.go:149` 用 `retrieve.NewSearcher(s, embedder)`，内部固定 `config.Default()` 且 `reranker=nil`（`searcher.go:62-66`），`RETRIEVE_RRF`/`RERANK_*` 等 env 对 rageval 无效；只能手工多次运行后人工比较。
4. **查询改写是链路外挂**：`RewriteQuery`（`retrieve/query_rewrite.go:32`）只在 agent 聊天路径内联调用（`agent/agent.go:126,139`），pipeline 直接检索，评测无法在同一矩阵中度量改写。

### 1.2 目标

1. **可插拔**：查询链拆为按 Kind 固定顺序装配的 Link 列表，实现按名字注册；新增/替换环节不需改编排代码。
2. **变体可配置**：变体 = 「Kind → 实现名(+参数)」集合，可用 env/规格串/JSON/命令行声明；生产默认变体逐位复刻现行为。
3. **矩阵可对比**：`rageval` 一次命令跑多个变体，产出本地对比报告（含相对基线 delta），并按 Phoenix experiment 并排回写。
4. **evaluable**：查询改写成为一等 Link，评测可通过 golden 题 `history` 字段度量其对 pipeline 的效果。
5. 既有 `go test ./...` + `make eval-ci` 全绿，默认行为零变化。

## 2. 已确认决策

| # | 决策 | 理由 |
|---|---|---|
| D1 | 抽象采用**统一 `Link` 管道 + 共享 `QueryCtx` + 注册表**（方案 B） | 环节可任意增删/换序（未来二次检索、HyDE、multi-query 无需改结构体），每环节天然可打点/计时；与既有 `pipeline.Stage` 范式同构 |
| D2 | Link 的 **Kind 固定规范顺序**，变体按 Kind 覆盖；不支持任意重排 | 满足「增删环节」需求，重排属 YAGNI |
| D3 | 变体对外表达：**命令行内联（`;` 分变体、`,` 分覆盖）+ 可选 JSON 文件** | 零配置可跑基线，命名变体可复现/入库 |
| D4 | 生产配置：**单项 env 构造基础变体 + `RETRIEVE_VARIANT` 补丁 + `RETRIEVE_VARIANT_PIPELINE/_CHAT` 路径覆盖** | 保持 README 环境变量表向后兼容 |
| D5 | 路径身份经 **`Options.Profile` 显式 + ctx 注入**双通道解析，默认 `pipeline` | `search_case`/`search_manual` 工具由 chat 与 pipeline 共用同一 Registry，无法静态区分 |
| D6 | `rewrite` 默认行为：pipeline/base = `passthrough`，chat = `llm`；agent 不再自行改写 | D5 所选方案 2；改写成为单一实现来源，pipeline 保持「不改写直通」既有约定 |
| D7 | Phoenix 为**主视图**：每变体一个 experiment；本地对比报告仍产出 | 多变体并排看收益；本地报告保证 CI/离线可 diff |
| D8 | 变体解析/未知名 **fail-fast**；运行期 Link 失败**静默降级** | 配置错误应显式暴露；运行时失败沿用既有降级约定 |
| D9 | Phase 1 只做查询侧；入库侧与 Parent-Child 二次检索另起 spec | 查询侧变体复用同一索引，一次 seed 跑全矩阵；入库侧变体需重建索引+改 schema |

## 3. 总体架构

```
Searcher (SearchCases / SearchManuals)         ← 对外接口不变
  └─ Chain（按 Kind 规范顺序装配的 Link 列表）
       fingerprint → rewrite → recall → fuse → relevance → rerank → gate
```

- 生产装配：`cmd/server/main.go` 用 `config` 解析出的**每 profile 变体**构造 `Chain`，注入 `Engine.SetSearcher`（`pipeline/engine.go:60`）与各 agent（`agent/agent.go:32`）。
- 评测装配：`cmd/rageval` 为每个变体构造固定 chain 的 `Searcher`，不依赖 profile。

新增/改动模块：

| 模块 | 文件 | 职责 |
|---|---|---|
| 链路核心 | `internal/retrieve/link.go`（新） | `Link` 接口、`QueryCtx`、`LinkTrace`、`Candidate` |
| 链路编排 | `internal/retrieve/chain.go`（新） | `Chain`、`LinkFactories`、`BuildChain`、profile 解析 |
| 注册表 | `internal/retrieve/registry.go`（新） | `RegisterLink(kind,name,factory)` 与内置实现 `init()` 注册 |
| 变体 | `internal/retrieve/variant.go`（新） | `Variant` 类型、规格串解析、JSON 解析、校验 |
| 内置环节 | `internal/retrieve/link_*.go`（新，按 Kind 拆文件） | 7 个 Kind 的实现（复用既有函数） |
| 检索器 | `internal/retrieve/searcher.go`（改） | `searcher` 持有 `map[profile]*Chain`；`search()` 改为组装 `QueryCtx` + `chain.Run` |
| 配置 | `internal/config/config.go`（改） | 新增 `RETRIEVE_*` 变体项与 `RetrieveVariant*()` 解析 |
| 路径注入 | `internal/pipeline/engine.go`、`internal/agent/agent.go`、`internal/context/{assembler,tools,chunk}.go`（改） | profile/history 透传 |
| 评测 | `cmd/rageval/main.go`、`internal/goldenset/*`（改） | 变体矩阵、ComparisonReport、golden history、Phoenix 逐变体回写 |

## 4. 核心抽象

### 4.1 Link 与 QueryCtx

```go
// Link 链路环节；Kind 标识槽位（fingerprint|rewrite|recall|fuse|relevance|rerank|gate）。
type Link interface {
    Kind() string
    Run(ctx context.Context, qc *QueryCtx) error
}

// Candidate 融合候选（原 cand 导出，字段名不变）。
type Candidate struct {
    Chunk   model.KbKnowledgeChunk
    BM25    float64
    Vec     float64
    Signal  float64
    Recency float64
    Final   float64
}

// LinkTrace 单环节运行痕迹，供评测报告逐环节对比。
type LinkTrace struct {
    Kind       string
    Impl       string
    DurationMS int64
    Hit        bool   // 是否产生/改变了候选（指纹命中、rerank 生效等）
    Note       string // 降级原因/命中数等
}

// QueryCtx 环节间共享的受控中间态；每环节只读写自己的段。
type QueryCtx struct {
    Raw      Query // 原始查询（Text/Category/Metric/Device/Version/Intents/History/Profile...）
    Text     string
    Options  Options
    Category model.KBCategory
    IsManual bool

    ShortCircuit bool         // 指纹命中/相关性门判空 → 跳过剩余环节
    Fallback     []Result     // 指纹快路径结果
    Cands        []Candidate  // recall → fuse → rerank 逐步更新的候选
    Results      []Result     // gate 产出的最终结果
    Trace        []LinkTrace
}
```

- `Query` 新增字段 `History []string`（本 spec 用于改写）；`Options` 新增 `Profile string`。
- `Chain.Run` 依序执行 Link；命中 `ShortCircuit` 即停止后续；每环节前后计时并追加 `LinkTrace`。

### 4.2 注册表与装配

```go
type Deps struct {
    Store     *store.Store
    Embedder  Embedder
    Reranker  Reranker
    Completer Completer
    Cfg       config.Config
    Now       func() time.Time
}

type LinkFactory func(Deps) Link

func RegisterLink(kind, name string, f LinkFactory) // 内置实现于 init() 注册

// canonicalKinds 固定规范顺序；变体未指定某 Kind 时使用该 Kind 的默认实现名。
func BuildChain(v Variant, deps Deps) (*Chain, error)
```

- `BuildChain` 对每个 Kind：变体指定名 → 查注册表；未指定 → 默认实现；名 `none`/`off` → 跳过该环节。
- 未知 Kind/实现名 → 返回错误（生产启动 fail-fast，评测非零退出）。

## 5. 内置环节实现（默认 = 现行为）

| Kind | 实现名 | 复用的既有逻辑 |
|---|---|---|
| `fingerprint` | `exact` / `off` | `searcher.fingerprintFastPath`（`searcher.go:171`）；命中置 `ShortCircuit` |
| `rewrite` | `passthrough` / `llm` | `retrieve.RewriteQuery`（`query_rewrite.go:32`）；消费 `Query.History`，空 History 直通 |
| `recall` | `hybrid` | `ensureIndex`（`searcher.go:84`）+ `dualRecall`（`searcher.go:254`） |
| `fuse` | `rrf` / `weighted` | `FuseRRF`（`rerank.go:81`）/ `RankCases`+`RankManuals`（`rerank.go:32,46`） |
| `relevance` | `default` | `hasRelevance`（`searcher.go:469`）；不相关 → 空结果 + `ShortCircuit` |
| `rerank` | `none` / `openai` | `applyRerank`（`searcher.go:374`），top20，失败静默 |
| `gate` | `floor` | `gateB`（`searcher.go:399`）+ topK 截断 + 构造 `Result` |

- `recall` 的 BM25 懒索引（`idx/docCount/idxVersion`）随实现迁移，保持并发安全。
- `baseline` 变体逐位复刻现 `search()` 的短路/降级顺序（指纹 → 召回 → 融合 → 相关性 → rerank → 截断）。

## 6. 变体规格与配置

### 6.1 Variant 类型

```go
type Variant struct {
    Name   string
    Steps  map[string]string            // Kind → 实现名（"none"/"off" 跳过）
    Params map[string]map[string]string // Kind → 参数（字符串承载，构造时解析为强类型）
}
```

### 6.2 生产配置

| env | 作用 | 默认 |
|---|---|---|
| `RETRIEVE_FINGERPRINT` | 单项便捷项：`1/0` → `fingerprint=exact/off` | 开 |
| `RETRIEVE_REWRITE`（新） | `llm` / `passthrough` | `passthrough` |
| `RETRIEVE_RECALL`（新） | `hybrid` | `hybrid` |
| `RETRIEVE_RRF` | `1/0` → `fuse=rrf/weighted` | 开 |
| `RERANK_BASE_URL` / `RERANK_API_KEY` | 空 → `rerank=none`，否则 `openai` | — |
| `RETRIEVE_GATE`（新） | `floor` | `floor` |
| `RETRIEVE_VARIANT`（新，可选） | 规格串，在单项之上打补丁基础变体 | 空 |
| `RETRIEVE_VARIANT_PIPELINE`（新，可选） | pipeline 路径覆盖 | 继承基础 |
| `RETRIEVE_VARIANT_CHAT`（新，可选） | chat 路径覆盖 | 基础 + `rewrite=llm` |

优先级：单项 env → `RETRIEVE_VARIANT` → 路径覆盖。规格串语法同 §6.3（`kind=impl` 逗号分隔）。

### 6.3 评测声明

- `-variants "baseline;rerank=none;fuse=weighted"`：`;` 分隔变体；同变体内 `,` 分隔覆盖项。
  - 含 `=` 的条目 = 在 `baseline` 上打补丁，自动以条目文本命名。
  - 不含 `=` 的条目 = 具名变体（来自 `-variants-file` 或内置 `baseline`）。
- `-variants-file variants.json`：

```json
[
  {"name": "no-rerank",   "steps": {"rerank": "none"}},
  {"name": "weighted",    "steps": {"fuse": "weighted"}},
  {"name": "low-floor",   "steps": {"rerank": "openai"}, "params": {"gate": {"floor": "0.15"}}}
]
```

- 未传 `-variants`/`-variants-file` 时默认 `[baseline]`，与现行为一致。

## 7. 路径身份与改写迁移

- 解析顺序：`Options.Profile`（显式）→ ctx 注入 → 默认 `"pipeline"`。
- 注入点：
  - `pipeline/engine.go` 的 `Run`：`ctx = retrieve.WithProfile(ctx, "pipeline")`。
  - `agent/agent.go` 的 `Handle`：`ctx = retrieve.WithProfile(ctx, "chat")` + `retrieve.WithHistory(ctx, history)`。
  - `context.Assembler`：`context.Query` 增加 `Profile`/`History` 字段，组装时填入 `retrieve.Options.Profile` / `retrieve.Query.History`（组装器内部使用 `context.Background()`，故须显式填充）。
  - ReAct 检索工具（`context/tools.go`）：`Query.History = retrieve.HistoryFromCtx(ctx)`、`Options.Profile = retrieve.ProfileFromCtx(ctx)`。
- agent 迁移：删除 `agent.go` 的 `rewriteWithHistory` 内联调用，改为随 Query/ctx 下传历史，由 `rewrite` Link 统一承接；`retrieve.RewriteQuery` 保留为 `llm` 实现内部函数。
- 默认下 pipeline 无 History 且 `rewrite=passthrough`，chat 有 History 且 `rewrite=llm`，与现状一致。

## 8. rageval 变体矩阵

流程：

1. 解析 `-variants` / `-variants-file` → `[]Variant`；`baseline` 始终作为对比基准被评估（即使未显式列出），含 `=` 的条目以 baseline 为父变体。
2. Seed 知识库一次（`-enhance` 仍生效）；Phase 1 查询侧变体复用同一索引。
3. 逐变体：`NewSearcherVariant(deps, variant)` → 逐题检索（`Options{TopK:k}`，可选 `it.History`）→ 收集 `ItemResult`（含 `LinkTrace`）。
4. 聚合 `ComparisonReport`（§9.1）。
5. stdout：每变体一行总表 + 相对基线 delta；`-o out.json` 写结构化报告。
6. 配置 `PHOENIX_BASE_URL` 时逐变体回写 Phoenix。

CLI 变更：

| flag | 说明 |
|---|---|
| `-variants` | 内联变体串（§6.3） |
| `-variants-file` | 变体 JSON 文件 |
| `-o`（既有） | 对比报告 JSON 输出 |
| `-golden` / `-enhance`（既有） | 不变 |

## 9. 报告与 Phoenix

### 9.1 对比报告

```go
type Metrics struct {
    MRR, NDCG, Recall, Precision, RejectRate float64
}

type VariantReport struct {
    Variant Variant                `json:"variant"`
    Overall Metrics                `json:"overall"`
    ByType  map[string]Metrics     `json:"by_type"`
    Items   []goldenset.ItemResult `json:"items"`
}

type ComparisonReport struct {
    Meta     CompareMeta        `json:"meta"`     // ts/golden/enhance/baseline/top_k
    Variants []VariantReport    `json:"variants"`
    Deltas   map[string]Metrics `json:"deltas"`   // 变体名 → 相对基线差（负值即退步）
}
```

- `goldenset.ItemResult` 增加可选 `Traces []LinkTrace`（JSON `traces`），向后兼容。

### 9.2 Phoenix 主视图

- 未配置 `PHOENIX_BASE_URL`：跳过回写并提示，本地报告照常产出。
- 已配置：逐变体调用 `goldenset.PushRagExperiment`，实验名 `<prefix>/<variant>`，`RagReport.Meta` 增加 `Variant` 与 `VariantSpec` 字段；一个变体一个 experiment，UI 并排比较。
- 数据集解析复用既有 `aiops-rag-golden`（`internal/phoenix` + `cmd/datasetctl` 路径）。

## 10. 数据模型与字段变更

- `retrieve.Query` 增 `History []string`；`retrieve.Options` 增 `Profile string`。
- `context.Query` 增 `Profile string` / `History []string`。
- `goldenset.Item` 增 `History []string`（`json:"history"`）；`goldenset.ItemResult` 增 `Traces []LinkTrace`；`RagMeta` 增 `Variant`/`VariantSpec`。
- 无数据库 schema 变更（无新表/新列）。

## 11. 兼容性

- `Searcher` 接口不变；`NewSearcher`/`NewSearcherWithConfig` 保留并绑定 baseline 固定 chain（旧调用与 `rageval` 旧路径不受影响）。
- `FuseRRF`/`RankCases`/`RankManuals`/`hasRelevance`/`gateB`/`fingerprintFastPath`/`dualRecall`/`RewriteQuery` 保留，被 Link 复用；对应单测不动。
- `cand` 改名为导出 `Candidate`（字段不变），引用点同步更新。
- README 环境变量表与 `.env.example` 同步新增 `RETRIEVE_REWRITE`/`RETRIEVE_RECALL`/`RETRIEVE_GATE`/`RETRIEVE_VARIANT*`。

## 12. 错误处理

- 变体解析失败/未知 Kind 或实现名：生产启动即报错退出；`rageval` 非零退出。
- 运行期 Link 失败（rerank/embed/LLM 超时等）：该 Link 保持输入态不变、不阻断链路，Chain 记 `LinkTrace.Note`；沿用既有「失败静默降级」约定。
- `Params` 解析失败：构造时报错，不静默。

## 13. 测试计划（TDD，每任务独立可验证）

| # | 任务 | 失败测试先行 | 通过标准 |
|---|---|---|---|
| 1 | Link/QueryCtx/Chain/注册表骨架 | `chain_test.go` | baseline chain 与旧 `search()` 对同一库输出等价 |
| 2 | fingerprint / relevance / gate Link | 迁移旧用例 | 快路径命中短路、空库空结果 |
| 3 | recall / fuse Link | `recall_test.go`/`fuse_test.go` 参数化 | rrf 与 weighted 可选且各自等价旧行为 |
| 4 | rewrite Link + agent 迁移 | `rewrite_link_test.go` + 改 `agent_test.go` | 空 History 直通；chat 行为不变、pipeline 不改写 |
| 5 | rerank Link | `rerank_link_test.go`（httptest） | none/openai 切换；失败静默降级 |
| 6 | Variant 解析（CLI 语法 + JSON）+ config 映射 + profile 解析 | `variant_test.go` | `;`/`,` 语法、优先级、未知名报错 |
| 7 | rageval 矩阵 + ComparisonReport + delta | `cmd/rageval` 单测 | 基线行 == 单变体运行；delta 计算正确 |
| 8 | Phoenix 逐变体回写 | `goldenset/report_test.go` | 每变体一 experiment、名含变体 |
| 9 | golden `history` 字段 | `goldenset_test.go` | 缺省空、向后兼容 |
| 10 | 全量回归 | `go test ./...` + `make eval-ci` | 全绿，默认行为零变化 |

## 14. 成功标准

1. `go test ./...` + `make eval-ci` 全绿；默认变体与重构前输出逐位一致（等价性测试佐证）。
2. `rageval -variants "baseline;rerank=none"` 产出对比报告与 delta，基线行与单变体运行一致。
3. 新增链路实现 = 写一个 Link + `RegisterLink`，不改编排代码（以 `fuse=weighted` 经 CLI 可选用作佐证）。
4. 配置 Phoenix 时每变体一个 experiment，UI 可并排比较。

## 15. 明确不做（YAGNI / 下一期）

- 入库侧环节变体（解析切块、向量化、富化/CDTA）与父子「二次检索」→ 另起 spec（需改 schema、重建索引）。
- 运行时热切换；变体在启动时确定。
- ANN/持久化索引，`recall` 仍全量扫描。
- 除 pipeline/chat/rageval 外的任意自定义 profile。
- 通用插件/动态加载；变体仍是编译期注册的 Go 实现。
- 不暴露全部常量（融合权重、RRF k 等）为配置，仅暴露变体所需的参数。

## 16. 风险

- **行为等价性**：链路重构若顺序/短路处理不当会改变线上行为——用 baseline 等价性测试兜底。
- **改写迁移**：agent 改写从外挂迁入 Link，可能影响 chat 回复——用既有 `agent_test.go` 兜底。
- **Phoenix 命名冲突**：多变体实验名需含变体名避免撞车。
- **`QueryCtx` 共享态**：环节顺序依赖靠规范顺序 + 每环节单测保证；后续新增环节须补顺序测试。
