# 知识增强（Chunk 语义富化）设计文档

- 日期：2026-08-28
- 状态：已确认
- 依赖：知识库 + 混合检索基础（`2026-08-17-knowledge-base`、`2026-08-20-knowledge-hybrid-retrieval`）、检索质量升级（`2026-08-27-retrieval-quality`）
- 范围：文档入库前三项语义富化 — 摘要增强 + Reverse HyDE 问题生成 + CDTA 跨块相似合并（嵌入后、全库、带出处）；检索侧接入“问题对问题”通道。

## 1. 背景与目标

现有知识库按 `Chunk()`（标题切块 → 2000 rune 二次切）产块后直接 embedding 入库，随后以 BM25+向量+信号 RRF 融合 + rerank 检索。调研与 `rageval` 干扰题表明，用户查询表述与文档表述存在语义鸿沟：同义问法、否定/跨对象/误导前提等在纯原始块上召回率偏低；跨文档的近重复块以多行独立分散，增加上下文噪声。

目标：

1. **弥合表述鸿沟**：在切片后、入库前对原始块做语义富化，使“查询侧改写”与“索引侧富化”互为补充。
2. **三项增强**：
   - **摘要增强**：为每块生成 1-2 句 LLM 摘要并拼入增强文本，统一 embedding，使向量兼顾原文细节与主题概括。
   - **Reverse HyDE（问题生成）**：为每块生成 2-3 个“能回答的假设问题”单独索引，检索时“问题对问题”匹配，提升问句式查询的命中。
   - **CDTA 合并**：增强并 embedding 后在全库做相似搜索，合并相似度 ≥ 阈值的块，并在元数据中保留多文档出处，去重并合成统一上下文。
3. **可验证**：同一标注集在 `KB_ENHANCE on/off` 下跑 `rageval` 可对照 Recall/MRR/NDCG 提升；无 key 时静默回退，行为不退步。

## 2. 已确认决策

- **触发**：同步增强 — `Upload`/`Seed` 的 `Chunk → Enrich → Embed → CDTA → 事务入库` 同步执行；有 `LLM_API_KEY` 时执行，无 key 或失败回退原文。
- **CDTA 时机**：嵌入后、最终入库前做全库相似搜索并合并（非定时批处理）；合并后入库前完成 provenance 组装。
- **检索集成**：问题单独索引 — 假设问题存独立表 `kb_chunk_questions` 并以独立向量参与 `dualRecall` 的问题通道，命中后关联回父块；摘要拼入块的增强文本参与块通道，不单独索引。
- **三项均同步落地为单 spec**：后续若上传延迟敏感，可将 CDTA 再抽为后台批处理（接口预留），本期先以同步事务保证出处完整。
- **复用模型**：摘要/问题生成走主 LLM（`LLM_BASE_URL`/`LLM_MODEL`，复用 `LLM_API_KEY`），embedding 复用 `LLM_EMBED_*`；不新增轻量模型依赖。

## 3. 流水线总体架构

保持 `kb.Service` 对外接口（`Seed`/`Upload`）与 `retrieve.Searcher` 接口不变，调用方零改动。

```
Upload/Seed：
  Parse → Tag → Chunk(chunks)                         // 现有
    → enrichChunks(chunks)                            // §4：摘要 + 假设问题，限并发 3，单块超时 5s，失败回退
    → EnhancedContent = 原文 [+ "\n【摘要】" + summary] // 无摘要时为原文
    → Embed(EnhancedContent) + Embed(每个问题)        // 复用 retrieve.Embedder
    → cdtaMerge(增强块, 增强向量, 已有库向量)         // §5：余弦 ≥ 阈值则合并，组装 source_doc_ids
    → WithKBTx 事务入库                                 // doc + chunks/questions + embeddings + fingerprints
```

- `enrich` 失败、超时、空返回均回退原文/空问题；`Embed` 失败向量置 0；CDTA 搜索失败按未命中处理；全链路静默降级，符合现有 inline 注释约定。
- 单文档内 `EnrichChunks` 用 `errgroup` 限 3 并发，避免对 LLM 瞬时打爆；`Seed` 的多文档循环复用同一并发池。

开关（见 §6 配置）：`KB_ENHANCE` 总开关、`KB_ENHANCE_SUMMARY`/`KB_ENHANCE_QUESTIONS`/`KB_ENHANCE_CDTA` 分项开关、`KB_CDTA_THRESHOLD` 阈值；默认全开，`0/false` 关闭便于 `rageval` 对照。

## 4. 单块富化（摘要 + Reverse HyDE）

新增 `backend/internal/kb/enhance.go`，职责单一、可单测：

```go
type Completer interface { Complete(ctx context.Context, prompt string) (string, error) }

func Summarize(ctx context.Context, lc Completer, chunk string) (string, error)
func GenerateQuestions(ctx context.Context, lc Completer, chunk string) ([]string, error)
func EnrichChunks(ctx context.Context, lc Completer, chunks []ChunkResult) []EnrichedChunk

type EnrichedChunk struct {
    ChunkResult
    Summary         string   // 1-2 句，空即未增强
    Questions       []string // 2-3 问，去重后
    EnhancedContent string   // 原文 [+ 摘要]
}
```

- **Prompt**（与 `agent` 的 `RewriteAndSplit` 同风格，短而确定）：
  - 摘要：`"用1-2句中文概括下列运维知识的核心处置/根因，不要扩写：\n" + chunk`
  - 问题：`"基于下列运维知识，列出它能回答的2-3个用户提问，运维口吻，每行一问，不要编号：\n" + chunk`
- **后处理**：摘要取首 300 rune 去空白；问题按行切分、去编号符号、去重、截断 2-3 条、空行丢弃。
- **增强文本**：`EnhancedContent = chunk.Content`；有摘要时 `+= "\n【摘要】" + summary`；问题不拼入（单独索引，避免污染块语义）。

### 失败与回退

- `lc == nil` 或 `IsNilCompleter(lc)` 或 `strings.TrimSpace(chunk)==""` → 直接返回原文/空。
- 单块 `context.WithTimeout(ctx, 5*time.Second)`，超时/非 2xx/空返回 → 该块回退原文/空问题，不阻断同文档其他块。

## 5. CDTA 合并（嵌入后、全库、带出处）

在 `EnrichChunks` 与 `Embed` 之后、`insertDocAndChunks` 之前执行：

```go
func CDTA(ctx context.Context, st *store.Store, docID string, newChunks []EnrichedChunk, newVecs [][]float64, threshold float64) []MergedChunk
type MergedChunk struct {
    EnrichedChunk
    MergedIntoChunkID string   // 非空表示合并到已有块
    SourceDocIDs      []string // 去重后的多文档出处
}
```

- **全库相似搜索**：拉 `s.st.ListKbEmbeddings()` 的已有块向量（`kbchunk_*` 前缀过滤，问题向量 `q_*` 不参与合并判定）与 `newVecs` 全量余弦（复用 `retrieve.Cosine`，知识库块量小，避免 HNSW 漏召回）；对每块取最大相似度与对应 `oldChunkID`。
- **阈值**：`KB_CDTA_THRESHOLD` env，默认 `0.85`（与向量相关性门限 `0.5` 区分，取高以只合并近重复）。
- **批内去重**：本批次新块两两相似（`new-vs-new` ≥ threshold）时，后者合并到前者的待入库条目，不产生额外行。
- **合并策略**（本期不额外调 LLM，保持 cheap 可验证）：
  - 命中（max ≥ threshold）：新块不再单存，改为更新旧块：`content = old.Content + "\n\n---\n\n" + new.EnhancedContent`，`source_doc_ids = 去重合并(old.source_doc_ids ∪ [docID])`，`summary` 保留旧值；问题的 `question` 数组追加到该旧块的问题表（去重）。
  - 未命中：按新块原样入库，`source_doc_ids = [docID]`。
- **出处**：`source_doc_ids` 的 JSON 数组即为“多文档出处”，检索返回时在 `Result.Metadata["source_doc_ids"]` 回显，`context.Assembler` 的 `knowledgeSection` 可带“来源于 N 篇文档”。

## 6. 数据模型

`store.migrate()` 增量（幂等 `ensureColumn` / `CREATE TABLE IF NOT EXISTS`）：

```sql
-- kb_chunks 新增两列
ALTER TABLE kb_chunks ADD COLUMN summary TEXT NOT NULL DEFAULT '';
ALTER TABLE kb_chunks ADD COLUMN source_doc_ids TEXT NOT NULL DEFAULT '[]';

-- 假设问题独立表（问题对问题单独索引）
CREATE TABLE IF NOT EXISTS kb_chunk_questions (
  id         TEXT PRIMARY KEY,
  chunk_id   TEXT NOT NULL,
  doc_id     TEXT NOT NULL,
  question   TEXT NOT NULL,
  created_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_kcq_chunk ON kb_chunk_questions(chunk_id);
CREATE INDEX IF NOT EXISTS idx_kcq_doc   ON kb_chunk_questions(doc_id);
```

- `kb_chunks.content` 存增强文本（原文+摘要）；`summary` 单存摘要；`source_doc_ids` 存 `["kbdoc_xxx", ...]` 的 JSON 数组，未合并时为 `["当前docID"]`。
- `kb_chunk_questions` 存单条假设问题；其向量以 `question.id` 为 `chunk_id`、`doc_id` 为所属文档写入 `kb_embeddings`（复用该表，读取时按 `chunk_id` 前缀 `q_` 与块 `kbchunk_` 区分，或显式 `JOIN kb_chunk_questions` 过滤）。

## 7. 检索集成

`retrieve.Searcher` 不改接口，内部 `dualRecall` 增加问题通道：

```
dualRecall 扩展：
  块通道:  BM25(增强块) + 向量(增强块)
  问题通道: BM25(问题) + 向量(问题)    // 去重后的 max 关联回父块
  信号:    FieldScore/LabelScore/Recency
  → Vec = max(块Vec, 问题Vec)，BM25 = max(块BM25, 问题BM25) 再进 RRF
```

- **块通道**：`BM25Scores(enhancedContent)`、`Vec = Cosine(enhancedVec, qVec)`（已有）。
- **问题通道**：为每块预计算 `QVec = max_{q in questions} Cosine(qVec, userQVec)`、`QBM25 = max BM25(question, userQuery)`；候选的 `Vec/BM25` 取 `max(块, 问题)` 再进 RRF 融合（权重与现有块通道共用 `w_vec/w_bm25`，不新增权重）。
- **Assistant 侧**：`context/assembler.go` 的 `resultChunk` 透传 `source_doc_ids` 到 `Chunk.Metadata`，`knowledgeSection` 回显可带出处。
- **回退**：无问题表或向量时该通道贡献为 0，RRF 中该通道 rank 视为未进入 TopN（贡献 0），静默降级。

## 8. 配置

`backend/internal/config` 新增（复用既有 `LoadFromEnv` 风格）：

| env | 默认 | 说明 |
|-----|------|------|
| `KB_ENHANCE` | `1` | 总开关；`0` 则跳过全部三项，回退当前 |
| `KB_ENHANCE_SUMMARY` | `1` | 单块摘要开关 |
| `KB_ENHANCE_QUESTIONS` | `1` | Reverse HyDE 问题生成开关 |
| `KB_ENHANCE_CDTA` | `1` | 合并开关 |
| `KB_CDTA_THRESHOLD` | `0.85` | CDTA 相似度阈值（余弦） |

复用 `LLM_API_KEY` / `LLM_BASE_URL` / `LLM_MODEL`；无 key 时增强整段跳过。

## 9. 集成改动清单

- `config/config.go`：5 env。
- `model/kb.go`：`KbChunk` 追加 `Summary string` / `SourceDocIDs []string`。
- `store/store.go`：增量列 `summary` / `source_doc_ids` + 建 `kb_chunk_questions` 表。
- `store/kb.go`：`kbChunkCols`/`scan` 同步、`InsertKbChunk` 追加两列、新增 `kb_chunk_questions` 的 `Insert/List/Delete`。
- `kb/enhance.go`：新增 `Summarize`/`GenerateQuestions`/`EnrichChunks`（并发限 3、超时 5s）。
- `kb/service.go`：`Seed`/`Upload` 中 `Chunk → Enrich → Embed(增强文本+问题) → CDTA → insertDocAndChunks`（事务内写入 `chunks/questions/embeddings/fingerprints`，CDTA 命中时更新旧块而非新增）。
- `retrieve/searcher.go`：`dualRecall` 增加问题通道（`QVec`/`QBM25` 取 max 并入融合）。
- **不改动**：`Searcher` 接口、`context.Assembler` 的静默降级约定；`api` 仅在健康检查透传增强开关（如需）。

## 10. 错误处理

- 单块摘要/问题超时 5s、LLM 非 2xx/空返回 → 该块回退原文/空问题。
- `Embed` 失败（单块/单问题）→ 该向量为 0，不阻断同文档其他块。
- CDTA 全库拉取/相似计算失败 → 按未命中处理，不阻断入库。
- 事务 `WithKBTx` 保证 `doc + chunks + questions + embeddings` 原子性；CDTA 的旧块更新与新块插入同事务。

## 11. 测试

- `kb/enhance_test.go`：`Summarize`/`GenerateQuestions` 用 mock `Completer` 验证拼接与去重、空输入、超时回退、并发限 3。
- `kb/service_test.go` 追加：`Upload` 后回读 `summary`/`questions`/`source_doc_ids` 落库；`KB_ENHANCE=0` 时回退原文；CDTA 合并时 `source_doc_ids` 去重合并。
- `store/kb_test.go`：`kb_chunks` 新列读写 + `kb_chunk_questions` CRUD + 历史库兼容（无列 → `DEFAULT ''`/`'[]'`）。
- `retrieve/searcher_test.go`：问题通道命中时 `QVec` 取 max 的排序提升、CDTA 合并后的 `source_doc_ids` 透传；全量 `go test ./...` / `go vet` 绿。
- `rageval`：同一标注集在 `KB_ENHANCE on/off` 下各跑一遍，对比 Recall/MRR/NDCG 提升（预期 `ambiguous`/`reasoning` 类提升最明显）。

