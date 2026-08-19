# 知识库混合检索设计文档（向量 + BM25 + 案例/手册差异化）

- 日期：2026-08-20
- 状态：已确认
- 范围：在现有知识库（kb_documents/kb_chunks）基础上引入语义检索能力：LLM embedding 向量通道 + BM25 关键词通道 + 现有确定性信号，按「运维案例」与「产品/操作手册」两类资料特点差异化融合排序；新增 `search_case` / `search_manual` 两个 ReAct 工具供 LLM 主动检索；被动注入改走同一混合检索入口。

## 1. 背景与目标

现有知识库已完成文件上传/解析/打标/分块/管理页（2026-08-17 知识库功能），RAG 采用确定性 Selector（关键词重叠 + 标签命中 + 意图加权 + 时效衰减）。诊断流水线 rca/recommend 阶段与聊天 agent 通过 `context.Assembler` 把命中的知识块被动注入 LLM。

目标：

1. **语义检索**：引入 LLM embedding 向量通道，支撑「查询相似运维案例」的语义匹配；BM25 关键词通道保证术语/型号/告警码精确命中。
2. **差异化检索**：运维案例（找相似故障史）以向量语义为主；产品/操作手册（查事实与步骤）以 BM25 关键词为主，两套通道组合与融合权重独立。
3. **主动检索**：ReAct 循环新增 `search_case` / `search_manual` 两个工具，LLM 诊断中可主动检索知识库，而不只是被动拿到注入上下文。
4. **被动注入增强**：组装器知识源由「全量扫描 + Selector」改为按阶段意图调混合检索入口，案例/手册按比例分配注入量。

## 2. 已确认决策

- **向量来源**：LLM embedding 接口（OpenAI 兼容 `POST {base}/embeddings`，复用 `LLM_API_KEY`/`LLM_BASE_URL`，模型取新 env `LLM_EMBED_MODEL`）；无 key/失败回退确定性检索，不引入本地 embedding 模型、不新增第三方依赖。
- **向量存储**：入库时预计算——新增 `kb_embeddings` 表（chunk_id 主键、JSON 向量数组、模型名、时间），上传/更新时对每块生成向量落库；查询时只对 query 生成向量。删除文档级联删向量。
- **多通道 + rerank**：BM25 关键词通道 + 向量余弦通道 + 现有 Selector 信号，三者确定性融合排序（min-max 归一化 + 加权）；不采用 LLM 逐条 rerank。
- **差异化策略**：case 类文档「向量 0.45 / 标签 0.30 / BM25 0.15 / 时效 0.10」；manual/runbook 类「BM25 0.50 / 字段 0.30 / 向量 0.20 / 无时效」。
- **工具粒度**：两个独立工具 `search_case` 与 `search_manual`，意图清晰、策略天然分开。
- **架构**：新增独立包 `internal/retrieve`（embedder/bm25/vector/rerank/searcher），统一供组装器与两个工具使用；context 包只负责组装，retrieve 只负责检索。
- **生效范围**：诊断流水线 rca/recommend（被动注入 + ReAct 主动检索）与聊天 agent（被动注入改走混合检索）均生效。

## 3. 架构总览

新增包 `backend/internal/retrieve/`，五个文件、单一职责：

| 文件 | 职责 |
|------|------|
| `embedder.go` | `Embedder` 接口 + `LLMEmbedder`（REST 调 embeddings，无 key/失败回退哨兵错误） |
| `bm25.go` | BM25 关键词通道（倒排索引 + 打分） |
| `vector.go` | 向量余弦相似度 + `kb_embeddings` 读写 |
| `rerank.go` | 确定性融合 rerank：`rerankCase` / `rerankManual` 两套权重 |
| `searcher.go` | `Searcher` 接口 + `SearchCases` / `SearchManuals` 编排入口 |

依赖方向：`retrieve → (store, model, config)`；`context → retrieve`；`kb → retrieve`（Embedder 接口）；`pipeline → context`（工具经 registry）；`api` 无新依赖。不产生环。

数据流：

- 写入侧：kb.Service 上传/更新文档时，对每块调 `Embedder` 生成向量写入 `kb_embeddings`（失败跳过该块）；删除文档级联删向量。
- 查询侧（组装器 + 两工具共用）：
  1. 对 query 文本生成向量（失败回退）；
  2. 按类型路由召回：BM25 topK + 向量 topK + 标签/意图过滤候选；
  3. 融合 rerank（case/manual 两套权重）；
  4. 返回 TopK 知识块（含文档元数据、分类、分数）。

## 4. 数据模型

`store.migrate()` 新增 1 张表：

```sql
CREATE TABLE IF NOT EXISTS kb_embeddings (
  chunk_id   TEXT PRIMARY KEY,
  doc_id     TEXT NOT NULL,
  embedding  TEXT NOT NULL,      -- JSON float64 数组
  model      TEXT NOT NULL,      -- 生成模型名
  created_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_kb_embeddings_doc ON kb_embeddings(doc_id);
```

- embedding 为 JSON 数组字符串列（复用 `model.MustJSON` 编解码）。
- 同一 chunk 重复嵌入使用 `INSERT OR REPLACE`（chunk_id 主键）。
- 删除文档时 service 事务内级联删 `kb_embeddings`。

## 5. 向量通道（embedder.go / vector.go）

**`Embedder` 接口**

```go
type Embedder interface {
    // Embed 批量生成向量；实现方保证返回长度与输入一致。失败返回哨兵错误。
    Embed(ctx context.Context, texts []string) ([][]float64, error)
}
```

- `LLMEmbedder`：`net/http` + `encoding/json` 调 `POST {base}/embeddings`（`model=LLM_EMBED_MODEL`，`input=texts`，可拆批）；无 `LLM_API_KEY`、非 2xx、维度异常 → 返回 `ErrNoEmbed`。
- 维度归一化：取首个成功响应长度 `DIM`，向量统一归一化为单位向量（后续直接用点积算余弦）。
- 单块超长：截断到 8000 字符再嵌入。
- 新增 `retrieve.NewLLMEmbedder(cfg config.Config) (*LLMEmbedder, error)`；无 key 时返回 `nil, ErrNoEmbed`（调用方按不可用处理）。

**`vector.go`**

- `Cosine(a, b []float64) float64`：预归一化向量直接点积；维度不一致返回 0。
- store 方法：`UpsertKbEmbedding` / `GetKbEmbeddingsByDoc(docID)` / `DeleteKbEmbeddingsByDoc(docID)`。
- 召回：query 向量与候选块向量全量点积（块量级小，成本可接受，避免漏召回），取 topK。

## 6. 关键词通道（bm25.go）

- BM25 打分（`k1=1.5, b=0.75`），中文按 rune（单字）切分并过滤空白；英文/数字按空格分词。
- 倒排索引从知识库 chunks 构建：`term → []{chunkID, tf}`；`docLen`/`avgDocLen` 来自 chunks。
- 检索返回 `chunkID → bm25Score` 映射，供融合排序。
- 索引在 Searcher 构建时从 `ListKnowledgeChunks()` 一次性构建（内存），知识库变更后由 service 触发重建或按需重建（见 §8）。

## 7. 差异化检索策略与融合（rerank.go / searcher.go）

**候选召回**（公共）：BM25 topK ∪ 向量 topK ∪ 标签/意图过滤命中，去重后进入融合。

**运维案例（case）——找相似故障史**

- 特征：历史故障事件，价值在症状/现象相似 + 同设备同指标同根因最相关 + 旧案例价值递减。
- 通道权重：向量 0.45 / 标签 0.30 / BM25 0.15 / 时效 0.10。
  - 标签信号：device_types / metrics / keywords 命中（与 query 的 device/metric/Text 重叠比例）；
  - 意图信号：query.Intents 与块文档 intents 交集（命中=1，否则 0）；
  - 时效信号：`exp(-ageMin / 60)` 衰减。
  - 以上均为 retrieve 内自实现的纯函数打分，**不依赖 context.Selector**（避免 retrieve→context 依赖环）。

**产品/操作手册（manual/runbook）——查事实与步骤**

- 特征：静态事实文档（规格/型号/告警码/命令/步骤），要求精确术语命中，不以年龄衰减。
- 通道权重：BM25 0.50 / 字段 0.30 / 向量 0.20。
  - 字段信号：vendor/model/device_types/metrics 命中（强过滤：全不命中则大降权）；
  - 无时效分量。

**融合**：各通道分先 min-max 归一化到 [0,1]，加权求和；某通道不可用（无向量/无 BM25）时按剩余权重归一化；全部不可用回退现有 `Selector.Select` 原逻辑。

**`Searcher` 接口**

```go
type Searcher interface {
    SearchCases(ctx context.Context, q Query, opts Options) ([]Result, error)   // 语义为主
    SearchManuals(ctx context.Context, q Query, opts Options) ([]Result, error) // 关键词为主
}
type Query struct { Text string; Category model.KBCategory; Metric, Device string }
type Options struct { TopK int }   // 默认 3
type Result struct { DocID, Title string; Category model.KBCategory; Heading, Content string; Score float64 }
```

- `SearchCases` 过滤 `category='case'`；`SearchManuals` 过滤 `category IN ('manual','runbook')`。

## 8. search_case / search_manual 工具（context 包）

```go
func NewSearchCaseTool(s retrieve.Searcher) Tool   // search_case
func NewSearchManualTool(s retrieve.Searcher) Tool // search_manual
```

| 工具 | 名称 | 描述 | 参数 |
|---|---|---|---|
| 查相似案例 | `search_case` | 检索相似历史运维案例（故障症状→根因→处置） | `query`（必填）、`metric`、`device`、`top_k` |
| 查手册 | `search_manual` | 检索产品手册/操作手册（规格、告警含义、操作步骤） | `query`（必填）、`category`(manual/runbook)、`metric`、`top_k` |

- 返回值：JSON 数组 `[{doc_id,title,category,heading,content,score}]`。
- 注册：`main.go` 构造 searcher 后 `reg.Register(context.NewSearchCaseTool(svc))` / `reg.Register(context.NewSearchManualTool(svc))`，与 DefaultTools 并列。
- 工具列表由 registry 提供，组装器 `【可用工具】` 段自动带上；ReAct `side()` 自动记录 ToolEvent 进时间线，无需改 engine。
- 失败降级：库空/无候选返回空数组；执行错误返回错误文本给 LLM 可重试，不 panic。

## 9. 被动注入增强（context/assembler.go）

- `candidates()` 知识源由「全量 `ListKnowledgeChunks` + Selector 打分」改为按阶段意图调 `Searcher`：
  - rca 阶段：`SearchCases`（意图 AskRootCause）为主，`SearchManuals` 少量补充手册块；
  - recommend 阶段：`SearchManuals` 为主（取操作步骤/预案），`SearchCases` 少量补充相似处置案例；
  - 聊天 agent：按识别出的意图 code 路由——根因类意图走案例，操作/处置类意图走手册。
- 注入量：默认知识块 topK=3；案例/手册按阶段比例分配——
  - rca：案例 2 块 + 手册 1 块；
  - recommend：手册 2 块 + 案例 1 块；
  - 聊天 agent：由意图路由决定（见下），单一类型为主时即 3:0。
- 聊天 agent 意图路由规则：内置意图 code `AskRootCause` 走案例；`AskRecommendation` 走手册；其余叶子 code 依其 `RuleKeywords` 是否含操作类词（处置/修复/操作/步骤/命令/更换/升级）判定——含则走手册，不含走案例；未命中任何叶子回退「案例 2 + 手册 1」。
- 回退：混合检索任一通道不可用降级为纯 BM25 或纯标签；全部不可用回退现有 Selector，保证无 key 环境行为不退步。
- `Assembler` 构造增加 Searcher 参数（或 `context.Engine` 持有），`buildAssembled` / rca / recommend 调用点随之透传。
- `assembler.go` 现有 `Selector` 与 `knowledgeSection` 保留：`Selector` 作为混合检索全不可用时的最终回退；`knowledgeSection` 回显格式不变（带文档来源）。

## 10. 配置

`backend/internal/config` 新增：

| env | 默认 | 说明 |
|-----|------|------|
| `LLM_EMBED_MODEL` | `text-embedding-3-small` | embedding 模型名 |
| `LLM_EMBED_BASE_URL` | 复用 `LLM_BASE_URL` | embeddings 接口 base URL |

- embedding 复用 `LLM_API_KEY`；无 key 时 `LLMEmbedder` 构造返回不可用，检索走 BM25+标签回退。

## 11. 错误处理

- embedding 批量部分失败：成功块照写，失败块跳过（该块向量通道为 0）。
- query embedding 失败：向量通道跳过，BM25+标签照常。
- 工具执行失败：返回错误文本给 LLM，ToolEvent 记录失败状态；不 panic、不阻断诊断。
- 库空/无候选：返回空数组。

## 12. 集成改动清单

- `store/store.go`：`migrate()` 新增 `kb_embeddings` 表 + 索引；`store/kb.go` 新增 `UpsertKbEmbedding` / `GetKbEmbeddingsByDoc` / `DeleteKbEmbeddingsByDoc`。
- `config/config.go`：新增两个 env。
- `retrieve/` 新包 5 文件 + 测试。
- `kb/service.go`：`Service` 增加 `Embedder` 注入；上传/更新后写向量、删除级联删向量；失败不阻断入库。
- `context/assembler.go`：知识源改走 `retrieve.Searcher`；`context/tools.go` 新增两个工具工厂；`context/store.go`/`engine.go` 透传 Searcher。
- `agent/agent.go`：按意图路由案例/手册注入。
- `cmd/server/main.go`：装配 embedder → searcher → kb service 注入 → 注册两工具。
- **前端无改动**。

## 13. 测试

- `retrieve/bm25_test.go`：倒排构建、打分单调、中文/英文 token 化、空 query。
- `retrieve/vector_test.go`：余弦、归一化、维度不一致、JSON 存取。
- `retrieve/rerank_test.go`：case/manual 两套权重、通道缺失降级、回退 Selector。
- `retrieve/searcher_test.go`：mock Embedder（固定向量）+ 种子知识库 → 类型路由、TopK、分数单调、无 key 回退。
- `store/kb_test.go` 追加：kb_embeddings upsert/按 doc 读取/级联删除。
- `kb/service_test.go` 追加：上传生成向量（mock Embedder）、embedding 失败不阻断入库、删除级联删向量。
- `context/tools_test.go` 追加：search_case/search_manual 注册与执行（mock Searcher）。
- `context/assembler_test.go` 更新：知识源走 retrieve（mock Searcher），案例/手册按比例分配。
- 既有测试（含无 key 回退路径）全部保持通过。
