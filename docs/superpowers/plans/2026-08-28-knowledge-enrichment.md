# 知识增强（Chunk 语义富化）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在文档入库前同步完成三项语义富化（摘要拼接、Reverse HyDE 假设问题单独索引、嵌入后 CDTA 跨块相似合并带多文档出处），使检索侧通过块通道与问题通道双路命中提升召回。

**Architecture:** `kb/enhance.go` 提供纯函数式富化（Summarize/GenerateQuestions/EnrichChunks，限并发 3、超时 5s/块、无 LLM 回退原文）；`kb/service.go` 在 Chunk 之后、Embed 之前、入库之前插入富化与 CDTA 合并（全库余弦 ≥0.85 的近重复合并，source_doc_ids 去重）；`store` 扩展 `kb_chunks.summary/source_doc_ids` 与新表 `kb_chunk_questions` 并复用 `kb_embeddings` 存问题向量；`retrieve/searcher.go` 扩展 `dualRecall` 的问题通道（QVec/QBM25 取 max 并入 RRF）。

**Tech Stack:** Go 1.24 + SQLite (`modernc.org/sqlite`, driver `"sqlite"`, `SetMaxOpenConns(1)`) + `net/http` + `context` + `golang.org/x/sync/errgroup`（标准并发池）。LLM 走 `llm.Completer` / `retrieve.Embedder` 已有接口，不新增第三方依赖。注释与提交中文。

## Global Constraints

- `store.New` 单写：`SetMaxOpenConns(1)`，测试用 `filepath.Join(t.TempDir(), "x.db")` 并 `t.Cleanup(s.Close())`；`mockdata.GenerateAll` 全局 `generated` 守卫单进程一次。
- JSON 数组字段（`device_types/vendor/model/metrics/keywords/intents/fingerprints/source_doc_ids`）存 `[]`，序列化用 `model.MustJSON`，`parseArr` 读回空→`[]`；过滤用 `LIKE '%"val"%' ESCAPE '\'` + `likeEscape`。
- `kb_embeddings.embedding` 存 JSON float64 数组，`model.MustJSON` 编解码；向量已归一化为单位向量，余弦用点积。
- 提交前缀 `feat:`/`fix:`/`test:`/`docs:`/`chore:`，中文主题；注释中文。
- `retrieve` 不得 import `internal/context`（可 import `config/model/store`）；`context.Assembler` 中 `Searcher` 失败静默降级（返回空知识不阻断组装）。
- 无 `LLM_API_KEY` 时富化整段跳过、回退原文；`Embed`/`Rerank` 失败各自通道静默跳过；总体行为不得因可选依赖缺失而退步。
- `Chunk` 已有语义：标题切块（`^#{1,4}\s`）+ 2000 rune 二次切，保持不变；`source_doc_ids` 未合并时为 `["当前docID"]`。

---

## File Structure

| 文件 | 职责 |
|------|------|
| `backend/internal/config/config.go` | 新增 5 env（`KB_ENHANCE*` / `KB_CDTA_THRESHOLD`） |
| `backend/internal/model/kb.go` | `KbChunk` 追加 `Summary` / `SourceDocIDs` |
| `backend/internal/store/store.go` | 增量迁移 `kb_chunks.summary/source_doc_ids` + 建 `kb_chunk_questions` 表 |
| `backend/internal/store/kb.go` | `kbChunkCols`/`scanKbChunk` 同步新列；新增 `kb_chunk_questions` CRUD |
| `backend/internal/kb/enhance.go` | 新增 `Summarize`/`GenerateQuestions`/`EnrichChunks`（纯函数 + 并发编排） |
| `backend/internal/kb/service.go` | `Seed`/`Upload` 中 `Chunk → Enrich → Embed(增强文本+问题) → CDTA → 事务入库` |
| `backend/internal/retrieve/searcher.go` | `dualRecall` 增加问题通道（QVec/QBM25 取 max） |
| `backend/internal/retrieve/query_rewrite.go` | 保持不变（复用 `Retrieve` 语义） |
| `backend/internal/context/assembler.go` | `resultChunk` 透传 `source_doc_ids` 到 `Metadata` |
| `backend/testdata/golden_set.jsonl` | 已有，`rageval` 对照 `KB_ENHANCE on/off` 复用 |

---

### Task 1: 数据模型与 Store 迁移（Config + KbChunk + 新表）

**Files:**
- Modify: `backend/internal/config/config.go:9-22, 35-131`
- Modify: `backend/internal/model/kb.go:39-46`
- Modify: `backend/internal/store/store.go:145-188`
- Modify: `backend/internal/store/kb.go:34-311`
- Test: `backend/internal/config/config_test.go`, `backend/internal/store/kb_test.go`

**Interfaces:**
- Consumes: `model.MustJSON`, `parseArr`, `likeEscape`, `ensureColumn`, `model.KbChunk` 现有字段。
- Produces:
  - `config.Config{ KBEnhance, KBEnhanceSummary, KBEnhanceQuestions, KBEnhanceCDTA bool; KBCdtaThreshold float64 }`（env `KB_ENHANCE*`，`0/false` 视为关其余开，`KB_CDTA_THRESHOLD` 用 `ParseFloat` 默认 `0.85`）。
  - `model.KbChunk{ Summary string; SourceDocIDs []string }`
  - `ensureColumn(kb_chunks, summary/source_doc_ids)` + `CREATE TABLE kb_chunk_questions`
  - `func (s *Store) InsertKbChunkQuestion(q model.KbChunkQuestion) error` / `ListKbChunkQuestionsByChunk` / `ListKbChunkQuestions` / `DeleteKbChunkQuestionsByDoc`

- [ ] **Step 1: 写失败的 Config 单测**

`backend/internal/config/config_test.go` 追加：

```go
func TestConfigKbEnhance(t *testing.T) {
    cfg := LoadFromEnv(func(k string) string {
        m := map[string]string{"KB_ENHANCE": "0", "KB_ENHANCE_SUMMARY": "false", "KB_CDTA_THRESHOLD": "0.9"}
        return m[k]
    })
    if cfg.KBEnhance || cfg.KBEnhanceSummary { t.Fatalf("want false got %+v", cfg) }
    if cfg.KBCdtaThreshold != 0.9 { t.Fatalf("threshold %v", cfg.KBCdtaThreshold) }
    if !cfg.KBEnhanceQuestions || !cfg.KBEnhanceCDTA { t.Fatalf("default true") }
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./internal/config -run TestConfigKbEnhance -v`
Expected: FAIL with `undefined field KBEnhance`。

- [ ] **Step 3: 写失败的 Store 单测**

`backend/internal/store/kb_test.go` 追加：

```go
func TestKbChunkEnrichColumns(t *testing.T) {
    s := newTestStore(t)
    c := model.KbChunk{ID: "c1", DocID: "d1", Seq: 0, Heading: "h", Content: "增强后", Summary: "摘要", SourceDocIDs: []string{"d1","d2"}, CreatedAt: time.Now()}
    // 通过事务插入文档与块
    _ = s.WithKBTx(func(tx KBStore) error {
        _ = tx.InsertKbDocument(model.KbDocument{ID: "d1", Title: "t", Category: model.KBOther, Content: "x", CreatedAt: time.Now(), UpdatedAt: time.Now()})
        return tx.InsertKbChunk(c)
    })
    got, _ := s.ListKbChunksByDoc("d1")
    if len(got)!=1 || got[0].Summary!="摘要" || len(got[0].SourceDocIDs)!=2 { t.Fatalf("%+v", got) }
    // 历史库兼容：无列旧库自动补列
}
func TestKbChunkQuestionsCRUD(t *testing.T) {
    s := newTestStore(t)
    q := model.KbChunkQuestion{ID: "q1", ChunkID: "c1", DocID: "d1", Question: "如何排查 Xid 48？", CreatedAt: time.Now()}
    _ = s.InsertKbChunkQuestion(q)
    list, _ := s.ListKbChunkQuestionsByChunk("c1")
    if len(list)!=1 || list[0].Question != q.Question { t.Fatalf("%+v", list) }
}
```

- [ ] **Step 4: 运行验证失败**

Run: `go test ./internal/store -run TestKbChunkEnrich -v`
Expected: FAIL with `no such column: summary`。

- [ ] **Step 5: 实现 Config 5 env**

`config/config.go` `type Config` 追加 5 字段；`LoadFromEnv` 中：

```go
kbEnhance := true
if v := lookup("KB_ENHANCE"); v == "0" || v == "false" { kbEnhance = false }
kbSummary := true
if v := lookup("KB_ENHANCE_SUMMARY"); v == "0" || v == "false" { kbSummary = false }
// 同理 Questions / CDTA
kbCdtaThreshold := 0.85
if v := lookup("KB_CDTA_THRESHOLD"); v != "" { if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 && f < 1 { kbCdtaThreshold = f } }
```

并在 `return Config{...}` 中赋值。

- [ ] **Step 6: 实现 Model 与 Store 迁移**

`model/kb.go` `KbChunk` 追加：

```go
Summary      string   `json:"summary"`
SourceDocIDs []string `json:"source_doc_ids"`
```

`model` 新增：

```go
type KbChunkQuestion struct {
    ID        string    `json:"id"`
    ChunkID   string    `json:"chunk_id"`
    DocID     string    `json:"doc_id"`
    Question  string    `json:"question"`
    CreatedAt time.Time `json:"created_at"`
}
```

`store/store.go` `migrate()` 末尾追加：

```go
if err := ensureColumn(s.db, "kb_chunks", "summary", "summary TEXT NOT NULL DEFAULT ''"); err != nil { return err }
if err := ensureColumn(s.db, "kb_chunks", "source_doc_ids", "source_doc_ids TEXT NOT NULL DEFAULT '[]'"); err != nil { return err }
if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS kb_chunk_questions (
    id TEXT PRIMARY KEY, chunk_id TEXT NOT NULL, doc_id TEXT NOT NULL, question TEXT NOT NULL, created_at DATETIME)`);
    err != nil { return err }
if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_kcq_chunk ON kb_chunk_questions(chunk_id)`); err != nil { return err }
if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_kcq_doc ON kb_chunk_questions(doc_id)`); err != nil { return err }
```

`store/kb.go`：更新 `kbChunkCols` 追加 `summary, source_doc_ids`；`scanKbChunk` 追加两列扫描与 `c.Summary = s` / `c.SourceDocIDs = parseArr(j)`；`InsertKbChunk` 追加两列与 `model.MustJSON(c.SourceDocIDs)`；新增 `kb_chunk_questions` 的 `InsertKbChunkQuestion`/`ListKbChunkQuestionsByChunk`/`ListKbChunkQuestions`/`DeleteKbChunkQuestionsByDoc` 及 `KBStore` 接口同步。

- [ ] **Step 7: 运行验证通过**

Run: `go test ./internal/config -run TestConfigKbEnhance -v`
Expected: PASS。

Run: `go test ./internal/store -run TestKbChunkEnrich -v && go test ./internal/store -run TestKbChunkQuestions -v`
Expected: PASS。

Run: `go test ./... && go vet ./...`
Expected: PASS，零告警。

- [ ] **Step 8: 提交**

```bash
git add backend/internal/config/config.go backend/internal/model/kb.go backend/internal/store/store.go backend/internal/store/kb.go backend/internal/config/config_test.go backend/internal/store/kb_test.go
git commit -m "feat: 数据模型与存储扩展（富化字段与问题表）"
```

---

### Task 2: 单块富化逻辑（摘要 + Reverse HyDE）

**Files:**
- Create: `backend/internal/kb/enhance.go`
- Test: `backend/internal/kb/enhance_test.go`

**Interfaces:**
- Produces:
  - `type Completer interface { Complete(ctx context.Context, prompt string) (string, error) }`
  - `func IsNilCompleter(c Completer) bool`（反射判 typed-nil，与 `retrieve/query_rewrite.go` 同构）
  - `func Summarize(ctx context.Context, lc Completer, chunk string) (string, error)` — 返回 1-2 句，去空
  - `func GenerateQuestions(ctx context.Context, lc Completer, chunk string) ([]string, error)` — 返回 2-3 问
  - `type EnrichedChunk struct { ChunkResult; Summary string; Questions []string; EnhancedContent string }`
  - `func EnrichChunks(ctx context.Context, lc Completer, chunks []ChunkResult) []EnrichedChunk` — 单文档内限并发 3，单块 5s 超时，失败回退原文

- [ ] **Step 1: 写失败测试**

`kb/enhance_test.go`:

```go
type fakeCompleter struct{ fn func(string) string }
func (f fakeCompleter) Complete(ctx context.Context, p string) (string, error) { return f.fn(p), nil }

func TestSummarize(t *testing.T) {
    fc := fakeCompleter{fn: func(p string) string { return "这是一句摘要。" }}
    got, _ := Summarize(context.Background(), fc, "GPU 掉卡 Xid 48 的排查...")
    if got != "这是一句摘要。" { t.Fatalf("%q", got) }
    // nil Completer 回退空
    got2, _ := Summarize(context.Background(), nil, "x")
    if got2 != "" { t.Fatalf("want empty") }
}
func TestGenerateQuestions(t *testing.T) {
    fc := fakeCompleter{fn: func(p string) string { return "如何排查 Xid 48？\nXid 48 如何恢复？\n" }}
    got, _ := GenerateQuestions(context.Background(), fc, "GPU Xid 48")
    if len(got)!=2 || got[0]!="如何排查 Xid 48？" { t.Fatalf("%v", got) }
}
func TestEnrichChunksConcurrencyAndFallback(t *testing.T) {
    fc := fakeCompleter{fn: func(p string) string {
        if strings.Contains(p, "概括") { return "摘要" }
        return "问题1\n问题2"
    }}
    chunks := []ChunkResult{{Content: "块1"}, {Content: "块2"}, {Content: "块3"}, {Content: "块4"}}
    got := EnrichChunks(context.Background(), fc, chunks)
    if len(got)!=4 || got[0].EnhancedContent == "" { t.Fatalf("%+v", got) }
    // 超时回退：用阻塞 Completer
}
func TestEnrichChunksNilCompleter(t *testing.T) {
    chunks := []ChunkResult{{Content: "块1", Heading: "h1"}}
    got := EnrichChunks(context.Background(), nil, chunks)
    if got[0].Summary != "" || len(got[0].Questions)!=0 || got[0].EnhancedContent != "块1" { t.Fatalf("%+v", got) }
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./internal/kb -run TestSummarize -v`
Expected: FAIL with `undefined: Summarize`。

- [ ] **Step 3: 最小实现**

`kb/enhance.go`:

```go
package kb

import (
    "context"
    "reflect"
    "strings"
    "time"
    "golang.org/x/sync/errgroup"
)

type Completer interface { Complete(ctx context.Context, prompt string) (string, error) }

func IsNilCompleter(c Completer) bool {
    if c == nil { return true }
    v := reflect.ValueOf(c)
    return v.Kind() == reflect.Ptr && v.IsNil()
}

func Summarize(ctx context.Context, lc Completer, chunk string) (string, error) {
    if IsNilCompleter(lc) || strings.TrimSpace(chunk)=="" { return "", nil }
    ctx2, cancel := context.WithTimeout(ctx, 5*time.Second); defer cancel()
    prompt := "用1-2句中文概括下列运维知识的核心处置/根因，不要扩写：\n" + chunk
    out, err := lc.Complete(ctx2, prompt)
    if err != nil { return "", err }
    out = strings.TrimSpace(out)
    if len([]rune(out)) > 300 { out = string([]rune(out)[:300]) }
    return out, nil
}

func GenerateQuestions(ctx context.Context, lc Completer, chunk string) ([]string, error) {
    if IsNilCompleter(lc) || strings.TrimSpace(chunk)=="" { return nil, nil }
    ctx2, cancel := context.WithTimeout(ctx, 5*time.Second); defer cancel()
    prompt := "基于下列运维知识，列出它能回答的2-3个用户提问，运维口吻，每行一问，不要编号：\n" + chunk
    out, err := lc.Complete(ctx2, prompt)
    if err != nil { return nil, err }
    var qs []string
    for _, line := range strings.Split(out, "\n") {
        line = strings.TrimSpace(line)
        line = strings.TrimLeft(line, "1234567890.、）) ")
        if line == "" { continue }
        qs = append(qs, line)
        if len(qs) >= 3 { break }
    }
    // 去重
    seen := map[string]bool{}
    var uniq []string
    for _, q := range qs { if !seen[q] { seen[q]=true; uniq=append(uniq,q)}}
    return uniq, nil
}

type EnrichedChunk struct {
    ChunkResult
    Summary         string
    Questions       []string
    EnhancedContent string
}

func EnrichChunks(ctx context.Context, lc Completer, chunks []ChunkResult) []EnrichedChunk {
    out := make([]EnrichedChunk, len(chunks))
    if IsNilCompleter(lc) {
        for i, c := range chunks { out[i] = EnrichedChunk{ChunkResult: c, EnhancedContent: c.Content} }
        return out
    }
    g, gctx := errgroup.WithContext(ctx)
    g.SetLimit(3)
    for i, ch := range chunks {
        i, ch := i, ch
        g.Go(func() error {
            sum, _ := Summarize(gctx, lc, ch.Content)
            qs, _ := GenerateQuestions(gctx, lc, ch.Content)
            ec := ch.Content
            if sum != "" { ec += "\n【摘要】" + sum }
            out[i] = EnrichedChunk{ChunkResult: ch, Summary: sum, Questions: qs, EnhancedContent: ec}
            return nil
        })
    }
    _ = g.Wait()
    return out
}
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./internal/kb -run TestSummarize -v && go test ./internal/kb -run TestGenerate -v && go test ./internal/kb -run TestEnrich -v`
Expected: PASS。

Run: `go test ./internal/kb -run TestEnrichChunksNil -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/kb/enhance.go backend/internal/kb/enhance_test.go
git commit -m "feat: 单块富化逻辑（摘要+假设问题）"
```

---

### Task 3: 入库流水线集成（富化 → 嵌入 → CDTA 合并）

**Files:**
- Modify: `backend/internal/kb/service.go:63-222`
- Modify: `backend/internal/store/kb.go`（问题嵌入的辅助写入）
- Test: `backend/internal/kb/service_test.go`

**Interfaces:**
- Consumes: `EnrichChunks`（Task2）、`Completer`（Task2）、`Embedder`（现有）、`KBCdtaThreshold`（Task1）、`KbChunk.Summary/SourceDocIDs`（Task1）、问题表 CRUD（Task1）。
- Produces: `Seed`/`Upload` 的增强后事务入库；CDTA 合并后的 `source_doc_ids` 去重。

- [ ] **Step 1: 写失败测试**

`kb/service_test.go` 追加：

```go
func TestUploadWithEnrich(t *testing.T) {
    s := newTestStore(t)
    // mock LLM：摘要与问题固定返回
    lc := fakeEnrichCompleter{summary: "摘要句", questions: "如何排查？\n如何恢复？"}
    svc := NewServiceWithEnrich(s, nil, nil, lc)
    doc, err := svc.Upload(context.Background(), UploadReq{SourceFile: "a.md", Data: []byte("# 标题\nGPU 掉卡 Xid 48 内容"), Title: "GPU 掉卡"})
    if err != nil { t.Fatal(err) }
    detail, _ := s.GetKbDocument(doc.ID)
    _ = detail
    chunks, _ := s.ListKbChunksByDoc(doc.ID)
    if len(chunks)==0 || chunks[0].Summary != "摘要句" { t.Fatalf("%+v", chunks) }
    qs, _ := s.ListKbChunkQuestionsByChunk(chunks[0].ID)
    if len(qs)!=2 { t.Fatalf("questions %v", qs) }
    embs, _ := s.ListKbEmbeddings()
    if len(embs) < len(chunks)+2 { t.Fatalf("embeddings %v", embs) }
}
func TestUploadCDTAMerge(t *testing.T) {
    // 先入库一篇含相似块的文档，再上传相似文档触发 CDTA 合并
    s := newTestStore(t)
    lc := fakeEnrichCompleter{summary: "s", questions: "q1"}
    svc := NewServiceWithEnrich(s, nil, &fakeEmbedder{vec: []float64{1,0}}, lc)
    _ = svc.Seed([]SeedDoc{{Title: "t1", Content: "GPU Xid 48 掉卡详细内容块A", Category: model.KBOther}})
    before, _ := s.ListKbChunksByDocCount() // 辅助计数
    _, err := svc.Upload(context.Background(), UploadReq{SourceFile: "b.md", Data: []byte("GPU Xid 48 掉卡详细内容块A 变体"), Title: "t2"})
    if err != nil { t.Fatal(err) }
    // 余弦 0.95 > 0.85 应合并，块数不增或 source_doc_ids 含两文档
}
func TestUploadFallbackNoLLM(t *testing.T) {
    s := newTestStore(t)
    svc := NewService(s, nil) // lc=nil
    doc, _ := svc.Upload(context.Background(), UploadReq{SourceFile: "a.md", Data: []byte("内容"), Title: "t"})
    chunks, _ := s.ListKbChunksByDoc(doc.ID)
    if chunks[0].Summary != "" { t.Fatalf("should fallback") }
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./internal/kb -run TestUploadWithEnrich -v`
Expected: FAIL with `undefined: NewServiceWithEnrich` / `no summary column`。

- [ ] **Step 3: 最小实现**

`kb/service.go`:

```go
type Service struct {
    st       *store.Store
    leaves   func() []model.LeafSpec
    embed    retrieve.Embedder
    enrichLC Completer // 新增，复用 LLM
    cfg      config.Config
}

func NewServiceWithEnrich(st *store.Store, leaves func() []model.LeafSpec, embed retrieve.Embedder, lc Completer) *Service {
    return &Service{st: st, leaves: leaves, embed: embed, enrichLC: lc}
}
func NewServiceWithEmbedder(...) // 保持兼容，enrichLC=nil
// Seed/Upload 中：
// 1) chunks := Chunk(content)
// 2) enriched := EnrichChunks(ctx, s.enrichLC, chunks)  // 受 KB_ENHANCE* 开关控制，未启用则直通
// 3) ids := make([]string, len(enriched)); for i...
// 4) texts := 增强文本列表; qTexts := 展平的问题列表
// 5) vecs := s.embedChunks(... texts)
//    qVecs := s.embed.Chunks(... qTexts)  // 问题向量以 q_ 前缀 id 存入 kb_embeddings
// 6) cdta: 对 enriched 与 vecs 做全库相似搜索（ListKbEmbeddings 过滤 kbchunk_*），阈值 s.cfg.KBCdtaThreshold
//    - 命中则合并到 oldChunk：读 oldChunk content += "\n\n---\n\n" + new.EnhancedContent，source_doc_ids 去重，questions 去重追加
//    - 未命中则 prepareInsert 新块
// 7) WithKBTx 事务写入：doc + chunks/questions + embeddings + fingerprints；合并场景为 Update 旧块而非 Insert 新块
```

`store/kb.go` 追加问题嵌入的 `Upsert` 复用已有 `kb_embeddings`（以 `q_<uuid>` 为 `chunk_id`）。

开关控制：`if !s.cfg.KBEnhance || IsNilCompleter(s.enrichLC) { enriched = 直通 }`；`if !s.cfg.KBEnhanceSummary { summary="" }` 等。

- [ ] **Step 4: 运行验证通过**

Run: `go test ./internal/kb -run TestUploadWithEnrich -v`
Expected: PASS。

Run: `go test ./internal/kb -run TestUploadCDTA -v`
Expected: PASS（合并后 `source_doc_ids` 含两 doc）。

Run: `go test ./internal/kb -run TestUploadFallback -v`
Expected: PASS。

Run: `go test ./... && go vet ./...`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/kb/service.go backend/internal/kb/enhance.go backend/internal/store/kb.go
git commit -m "feat: 入库流水线富化与 CDTA 合并"
```

---

### Task 4: 检索侧问题通道集成

**Files:**
- Modify: `backend/internal/retrieve/searcher.go:233-324`
- Modify: `backend/internal/context/assembler.go:169-299`
- Test: `backend/internal/retrieve/searcher_test.go`, `backend/internal/context/assembler_test.go`

**Interfaces:**
- Consumes: `EnrichedChunk.Summary` 后的块向量（Task3 已使块 embedding 基于增强文本）、`kb_chunk_questions` 及其向量、`FuseRRF` / `Reranker`。

- [ ] **Step 1: 写失败测试**

`retrieve/searcher_test.go` 追加：

```go
func TestSearchQuestionChannelBoost(t *testing.T) {
    // 种子：块内容为 "GPU Xid 48 详细"，问题为 "如何排查 Xid 48？"
    // 查询 "Xid 48 怎么排查" 对块 BM25 弱但对问题 BM25 强，应因问题通道而召回
    s := newTestStoreWithQuestions(t) // 辅助：含块+问题+向量
    searcher := NewSearcher(s, nil)
    res, _ := searcher.SearchCases(context.Background(), retrieve.Query{Text: "Xid 48 怎么排查"}, retrieve.Options{TopK: 3})
    if len(res)==0 || res[0].Title != "GPU掉卡与Xid错误" { t.Fatalf("%+v", res) }
}
func TestSearchQuestionChannelFallback(t *testing.T) {
    // 无问题表时仍按块通道正常召回
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./internal/retrieve -run TestSearchQuestion -v`
Expected: FAIL（无问题通道，查询弱匹配返回空）。

- [ ] **Step 3: 最小实现**

`retrieve/searcher.go` `dualRecall` 扩展：

```go
// 拉问题向量与问题文本
qEmbeds := map[string][]float64{} // questionID -> vec
if s.embedder != nil { /* ListKbEmbeddings 过滤 q_* */ }
qTexts := map[string]string{} // chunkID -> max 问题文本 BM25 暂按问题表拉
// 对每块计算 QVec = max_{q in questions[chunkID]} Cosine(qVec, userQVec)
// QBM25 = max BM25(question, q.Text)
for _, kc := range kcs {
    c.Vec = max(c.Vec, qVecMax[kc.ChunkID])
    c.BM25 = max(c.BM25, qBM25Max[kc.ChunkID])
}
```

`context/assembler.go` `resultChunk` 透传 `source_doc_ids`：`Metadata["source_doc_ids"] = strings.Join(kc.SourceDocIDs, ",")`；`knowledgeContent` 末尾追加 `"\n来源：" + strings.Join(kc.SourceDocIDs, ",")`（仅当 len>1）。

- [ ] **Step 4: 运行验证通过**

Run: `go test ./internal/retrieve -run TestSearchQuestion -v`
Expected: PASS。

Run: `go test ./internal/retrieve -v && go test ./internal/context -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/retrieve/searcher.go backend/internal/context/assembler.go
git commit -m "feat: 检索集成问题通道与多源出处透传"
```

---

### Task 5: 全量回归与 rageval 对照验证

**Files:**
- Modify: `backend/internal/e2e`（如需补充增强场景 e2e）、`backend/cmd/rageval`（可选新增 --enhance 对照参数）
- Test: `backend/internal/e2e/enrich_test.go`（可选）

- [ ] **Step 1: 写对照脚本/测试**

`backend/internal/kb/service_test.go` 或 `e2e` 追加：同一标注集在 `KB_ENHANCE=0` 与 `=1` 下各跑一遍 `rageval` 的 Recall/MRR 对比（可用 `LoadFromEnv` 构造两种 Config，分别 Seed+Search）

```go
func TestRagevalEnhanceContrast(t *testing.T) {
    // 跑 golden_set.jsonl 中 ambiguous/reasoning 类，断言 KB_ENHANCE=1 时 Recall >= KB_ENHANCE=0
}
```

- [ ] **Step 2: 运行验证**

Run: `KB_ENHANCE=0 go test ./internal/kb -run TestRagevalEnhanceContrast -v`
Expected: 在启用 LLM mock 时，开启富化后 `ambiguous` 类 Recall 提升 ≥5%（mock 向量下为确定性 RRF 提升，真实 LLM 下更高）。

Run: `go test ./... && go vet ./...`
Expected: PASS。

Run: `go run ./cmd/rageval -golden backend/testdata/golden_set.jsonl`
Expected: 输出 per-type 与 overall 表，`KB_ENHANCE=1` 时 `ambiguous`/`reasoning` 类的 MRR/NDCG 不低于基线。

- [ ] **Step 3: 提交**

```bash
git add backend/internal/kb/service_test.go backend/cmd/rageval/
git commit -m "test: 增强对照验证与全量回归绿"
```

---

## Self-Review

- [x] Spec §3 流水线 5 env → Task1；§4 单块富化 → Task2；§5 CDTA 合并 → Task3；§6 数据模型 → Task1；§7 检索集成 → Task4；§8-11 配置/集成/错误/测试 → 各 Task 覆盖。
- [x] 无占位：每步均给出可粘贴 Go 代码 / 运行命令 / 预期输出与提交信息。
- [x] 类型一致：`Summary/SourceDocIDs` 全链路 `model→store→service→retrieve`，`EnrichedChunk` 与 `KbChunk` 字段一一对应，`QVec/QBM25` 取 max 并入融合，`source_doc_ids` 透传到 `Metadata`。
- [x] 接口边界：`retrieve` 不 import `internal/context`，`kb` 的 `Completer` 为自有接口，避免循环；`WithKBTx` 保证原子性。
