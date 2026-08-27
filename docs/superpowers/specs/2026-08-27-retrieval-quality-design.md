# 检索质量升级设计文档（A 期：运维案例召回质量）

- 日期：2026-08-27
- 状态：已确认
- 依赖：知识库 + 混合检索基础（`2026-08-17-knowledge-base`、`2026-08-20-knowledge-hybrid-retrieval`）
- 范围：A 期检索质量五项 — 评测基线 → RRF 融合 → rerank 精排 → 查询改写 → 指纹快路径；B 期（知识治理：结构化 schema / 反馈闭环 / Parent-Child 切块）另起 spec。

## 1. 背景与目标

现有混合检索（`internal/retrieve`）以 BM25 + 向量 cosine + 信号三路加权融合（minmax 归一化 + 固定权重）实现，并按 rca/recommend 阶段配额注入上下文。调研表明其短板：加权融合存在量纲不兼容与索引漂移风险；未引入 rerank 精排；无查询侧改写；含告警 ID/错误码的查询未做确定性快路径；且无统一评测集，无法验证后续优化收益。

目标：

1. **可验证**：建立统一离线评测集与工具 `rageval`，覆盖常规题与干扰题（含无答案拒答），为每项改动积累基线与对比数据。
2. **抗漂移融合**：改用按排名的 RRF（k=60）替代加权原始分。
3. **提升精度**：当配置 rerank API 时对其融合结果进行精排提精。
4. **降低查无可查的检索落差**：对对话上下文缺失等做查询改写，对含指纹的精确查询做快路径命中。

## 2. 已确认决策

- **拆两期**：本 spec 仅覆盖 A 期（检索质量）；B 期知识治理（结构化、反馈、切块）另立 spec，各自独立走 planning→实现。
- **精排来源**：外部 rerank API（OpenAI 兼容 `/rerank`）。未配置时该步静默跳过，不新增基座模型依赖。
- **评测集构造**：LLM 基于种子文档自动生成 + 人工抽检/修正，提交为固化 JSONL。每题附类型标签与预期行为，包含干扰题（无答案拒答、跨对象混淆、误导前提），用于同时校准命中率与误报控制。
- **HyDE 不纳入本期**：有 rerank + 改写后其边际收益低且额外增加一次 LLM 调用，遵循 YAGNI 省略。
- **管线策略**：渐进式管线升级——保持 `Searcher` 接口不变，调用方（Assembler/工具/Agent）零改动；改动集中于 `internal/retrieve/searcher.go` 内流水线结构与 `rerank.go` 的融合实现；每步可独立通过环境变量开关/降级，并可随时回退至旧加权融合。

## 3. 管线总体架构

保持对外接口不变：

```go
type Searcher interface {
  SearchCases(ctx, q Query, opts Options) ([]Result, error)
  SearchManuals(ctx, q Query, opts Options) ([]Result, error)
}
```

`searcher.search` 重构为流水线式子步骤（每步均为小函数，可单测 / 单开关）：

```
search(q):
  1. fingerprintFastPath(q)   →  命中指纹则直接返回对应文档 chunks，跳过 2-6
  2. rewriteQuery(q)          →  将对话上下文补全为独立检索查询（见 §7）
  3. dualRecall(q')            →  BM25 与向量并行召回，各取 TopN 排名列表
  4. fuseRRF(ranks)            →  各通道排名 RRF(k=60) 融合，输出统一序列
  5. rerank(ctx, q', top20)    →  调 /rerank 精排（未配置则跳过）
  6. gateAndTruncate(cands)    →  相关性门限 + 干扰题拒答判定 + 按 TopK 截断
```

相关性截断采用**两道闸**：
- **Gate A**（融合前判定）：沿用现有 `hasRelevance`（BM25>0 或 Signal>0 或原始余弦≥`vectorRelevanceFloor`）——是否"值得继续"。
- **Gate B**（精排后判定）：精排分 < `RERANK_FLOOR`（默认 0.2，可经评测校准）视为不相关，返回空结果供上层展示"未命中"话术而非硬凑答案。精排缺席时仅以 Gate A 定空结果。

环境开关（见 §9 配置节）：

- `RETRIEVE_FINGERPRINT=0/1`（默认开）
- `RETRIEVE_RRF=0/1`（默认开；关则回退现有加权融合，便于对照评测）
- `RERANK_BASE_URL` / `RERANK_API_KEY` / `RERANK_MODEL`：空即跳过精排

失败策略统一**静默降级**（沿用现有 inline 注释风格与 `context/candidate` 节"单次检索失败静默降级为空知识"约定）：每步失败均回退到上一步的输入，不抛错阻断检索承接方。

## 4. 评测基线（rageval）

### 4.1 标注集（backend/testdata/golden_set.jsonl，提交入库）

```json
{"id":"q001","type":"fact","question":"GPU 掉卡该如何排查","expect":{"doc_ids":["d1"],"behavior":"hit"}}
{"id":"q014","type":"no_answer","question":"C7000 机箱声卡蓝屏如何处置","expect":{"doc_ids":[],"behavior":"reject"}}
{"id":"q021","type":"cross_object","question":"将 A 产品的磁盘扩容步骤用于 B 型号是否适用","expect":{"doc_ids":["d-b-correct"],"behavior":"hit","distractor_doc_ids":["d-a-wrong"]}}
```

- `type` 取值：
  - **常规题**：`fact`（单文档事实）、`multi_doc`（需两文档综合）、`negative`（否定/边界，如"哪些情况不会导致…"）、`ambiguous`（模糊歧义）、`reasoning`（需推理）。
  - **干扰题**：`no_answer`（知识库无答案，应拒答）、`cross_object`（将 A 产品问题套到 B）、`misleading_premise`（前提错误，如"为什么 A 正确"而实际 A 错误）。
- `behavior` 为 `hit` 或 `reject`；干扰题对命中错误文档与"不应有答案却给出答案"均判为失败。

### 4.2 工具 `backend/cmd/rageval`

- **默认评估模式**：读标注集 + 种子库，逐题调 `Searcher`（SearchCases/SearchManuals 按题面意图路由或类型指定），计算：
  - `hit` 题：**Recall@K / MRR / NDCG@10** 按题型分项与总分；
  - `reject` 题：**拒答正确率**（返回空或被 Gate B 判空视为正确）；
  - 输出 per-type 明细与总表，支持按 env 开关跑对照实验（RRF on/off、rerank on/off）。

- **生成模式 `-generate`**：读种子文档，按题型 prompt 调用 LLM 批量出题（含候选 doc 引用），落地为 JSONL 供人工抽检修正（commit 前要求至少 30 题以上、多类型覆盖）。生成逻辑不在回归路径内，仅作选题辅助。

- 固定随机种子；指标在测试态无需线上 rerank 健康即可跑（rerank 不可用时回退基线可回归）。

## 5. RRF 融合改造（rerank.go / searcher.go）

### 5.1 新融合定义

每通道在其召回候选集内计算**排名**（分数并列按稳定排序位次），再按权重应用 RRF：

```
RRF(c) = Σ_channel  w_channel × 1/(k + rank_in_channel(c))    , k=60
```

|  | 运维案例 (SearchCases) | 手册 (SearchManuals) |
|---|---|---|
| 向量排名 w_vec | 1.0 | 0.5 |
| 信号排名 w_sig（LabelScore/FieldScore） | 0.8 | 0.8 |
| BM25 排名 w_bm25 | 0.5 | 1.0 |
| 时效排名 w_recency（仅案例） | 0.3 | — |

- 各通道均参与同一次融合（不做早期去重子集），缺席位次的通道用该通道排名表长度作为惩罚位（或 1/(k+N) 的下界近似，取实现为"未进入该通道 TopN 的候选在该通道贡献为 0"）。
- `cand.Final` 语义由"加权归一分"改为"RRF 加权和"，不跨查询可比，仅用于查询内排序。
- `get` / `minmax` 保留但不再用于融合主路径（相关性门限所需的原始分判定与排后打印可保留）；融合分支由新函数 `fuseRRF` 承接。

### 5.2 门限

- Gate A 判定使用的原始分（BM25 / Signal / 原始 cosine）保留于 `cand`，并在融合前计算完备份。
- 不引入基于 RRF 分的绝对阈值——RRF 值跨查询无物理含义。

## 6. rerank 精排（新增 retrieve/reranker.go）

### 6.1 新增抽象

```go
// Reranker 将候选块按查询重新排序/打分；不可用时返回 (nil, ErrNoRerank)。
type Reranker interface {
  Rerank(ctx context.Context, query string, docs []string) ([]float64, error) // 与 docs 等长
}

var ErrNoRerank = errors.New("rerank 不可用")

func NewReranker(cfg config.Config) (Reranker, error) // 未配置时返回 ErrNoRerank
```

- OpenAI 兼容 `/rerank` 客户端实现（`net/http + encoding/json`，与 `embedder.go` 同演进风格）：请求 `{model, query, documents, top_n}`，响应按 `{results:[{index, relevance_score}]}` 回填为与输入等长 float 数组（未回传项视为 0）。
- 超时 3s；非 2xx / JSON 异常视作失败，回退为跳过精排（不抛给上游）。

### 6.2 集成

- 仅在 `len(fused) > 0` 时对**融合后前 20** 调用 rerank，按 `relevance_score` 降序重排；`Result.Score` 语义从 RRF 分替换为精排分（[0,1]，便于上游展示与 Gate B 截断）。
- Gate B：精排分 < `RERANK_FLOOR`（默认 0.2，具体值由 §4 评测校准）视为不相关被剔除；剔空后即 `reject`。

## 7. 查询改写（仅聊天路径生效）

- 适用范围：聊天 agent 路径（流水线 rca/recommend 无对话历史，不改写直通）。
- 触发点：agent 的多意图拆分（`RewriteAndSplit`）之后，对每个子查询在调 `Searcher` 前执行一次 standalone 改写。
- 规则先行：若子查询已含设备/指标实体词（`Device`/`Metric` 非空）或 query 文本已超过 40 runes 且明确含动宾结构，则跳过 LLM 改写直接原文检索。
- LLM 改写：复用轻量意图模型（与 `RewriteAndSplit` 共享 `lightLLM` / `LLMLightBaseURL` / `LLMLightModel`），prompt 职责仅为"将含代词/上下文缺省的查询改写为独立句"，**不做扩写或假设生成**；超时 2s，失败回退原文。
- 接口形式：不新增全局重写器单例，在 `agent` 调 `Searcher` 前内联改写文本；`retrieve` 层不感知改写来源，保持无状态。

### 未来扩展（不在本期实现）

- 仅单次改写，不做 HyDE / multi-query 扩写；若评测证明某类模糊题需扩写，可在后续针对该类型增量接入 RRF 形式的 multi-query 融合。

## 8. 指纹快路径（新增 kb/fingerprint.go + kb_documents.fingerprints）

### 8.1 指纹定义

```go
// Fingerprints 为从标题/内容解析出的精确可匹配片段，JSON 存储。
type Fingerprints []string // 如 ["ALERT-GPU-001", "Xid 48", "ERR-NVME-0x4004"]
```

- 正则集集中定义于 `backend/internal/kb/fingerprint.go`：告警 ID、错误码/状态码（如 `Xid \s*\d+`、`ERR[-_]\w+` 等）、设备型号码等，沿用现有打标正则演进，不发散引入新实体词典。
- 写入：`kb.Service.Upload` / `Seed` 入库时由 `ExtractFingerprints(title, content)` 生成并写入新列；存量文档由迁移/种子导入时一次性回填。

### 8.2 检索侧命中

- 检索入口先以同一正则集从 `q.Text` 提取指纹集合 `F_q`；若非空则对 `kb_documents` 按 `fingerprints` JSON 数组做交集检索（SQLite 下用 `LIKE '%"fp"%'` 的子串匹配，复用现有 `List` JSON 数组过滤手法；指纹正则受控、无注入风险）。
- 命中 doc：直接加载其关联 chunks，按原文 seq/相关性转化为 `Result` 返回并跳过 BM25/向量/rerank；`Score=1.0`；仍需通过 `hasRelevance` 中至少一种通道"命中"的宽松判定后再计入上层，否则当作未命中落入正常管线（避免指纹误提取导致错误强推）。
- 未命中 / 提取为空 / 开关关闭：进入主流水线，不引入额外延迟。

### 8.3 数据模型变更

`store.migrate()` 增量变更（幂等 `ALTER TABLE kb_documents ADD COLUMN fingerprints TEXT NOT NULL DEFAULT '[]'`），兼容无列历史库。指纹列为 JSON 数组文本，索引不新建（数据量小且精确匹配走全表扫描可接受）。

## 9. 配置

`backend/internal/config` 新增（其余复用既有 env 命名风格）：

| env | 默认 | 说明 |
|-----|------|------|
| `RETRIEVE_FINGERPRINT` | `1` | 指纹快路径开关（0 则流程跳过） |
| `RETRIEVE_RRF` | `1` | RRF 融合开关（0 则回退现有加权融合，用于对照评测） |
| `RERANK_BASE_URL` | 空 | rerank API base URL；空表示精排不启用 |
| `RERANK_API_KEY` | 空 | rerank API key |
| `RERANK_MODEL` | `rerank` | rerank 模型名 |
| `RERANK_FLOOR` | `0.2` | Gate B 精排门限 |

既有复用：`LLM_API_KEY` / `LLM_BASE_URL` / `LLM_LIGHT_BASE_URL` / `LLM_LIGHT_MODEL`（改写共享轻量模型，无新增 prompt 由调用方控制）。

## 10. 集成改动清单

- `config/config.go`：新增 6 个 env 的读入与默认。
- `retrieve/rerank.go`：新增 `fuseRRF` 与 SearchCases/SearchManuals 专有 RRF 权重的排分路径；保留旧加权路径受 `RETRIEVE_RRF` 开关控制（或抽为 `fuseWeighted` 供对照）。
- `retrieve/searcher.go`：拆分为管线子函数——`fingerprintFastPath` / `dualRecall` / `fuseRRF` / `applyRerank` / `gateAndTruncate`；`ensureIndex` / `Vector` 通道逻辑保持不动。
- `retrieve/reranker.go`：新增（Reranker 接口 + `/rerank` 客户端 + ErrNoRerank）。
- `kb/fingerprint.go`：新增指纹正则与 `ExtractFingerprints`。
- `kb/service.go`：入库时写 `fingerprints` 列；`Seed` 回填存量。
- `store/store.go`：增量迁移 `fingerprints` 列；`store/kb.go` 相关读写的 JSON 列处理随之更新。
- `store/kb.go` / `retrieve` 读路径：新增按指纹集合匹配 doc 的辅助查询（复用 LIKE JSON 过滤实现）。
- `backend/cmd/rageval`：新增（`main.go`，生成 + 评估双模式）。
- `backend/testdata/golden_set.jsonl`：新增提交的固化标注集。
- **不改动**：`Searcher` 公开接口；`context/Assembler` 的 `candidates` 中关于失败静默降级的约定与注释风格；领域 store 表之外的数据层与前端。

## 11. 错误处理

- 指纹提取/匹配失败、缺列兼容、正则异常：返回空集合并进入主流水线。
- 改写超时/失败：回退原文检索。
- Embed/Rerank HTTP 失败：各自通道静默跳过，不阻断同级其它通道；全通道失败时由 `hasRelevance` 判空返回空结果。
- 评测集与种子库不一致、文件缺失：`rageval` 明确报错并退出非零，提示需先执行 `Seed`。

## 12. 测试

- `retrieve/rerank_test.go`：RRF 在各类型（案例/手册）下的权重体现、Rank 并列稳定性、Gate（融合前判定）不影响排后的判定位置；保留既有加权路径的回归用例通过开关覆盖。
- `retrieve/reranker_test.go`：httptest 模拟 `/rerank`（正常 / 非 2xx / 超时 / 空结果），验证 Score 回填与失败静默降级的边界。
- `retrieve/searcher_test.go`：追加端到端子管线用例——指纹命中直通（mock doc 快路径）、改写不改 pipeline 路径、RRF vs 加权对照、双 Gate 共同判定的空结果用例。
- `kb/fingerprint_test.go`：正则覆盖（Xid/告警 ID 等）与 `ExtractFingerprints` 的去重/空输入。
- `store/store_test.go`：增量迁移对含历史数据的库的兼容、指纹列读写。
- `cmd/rageval`：指标计算的确定性单测（给定固定 scores/标注计算 MRR/NDCG 正确性）。
- 既有测试全量回归绿（含无 LLM Key 的回退路径）。
