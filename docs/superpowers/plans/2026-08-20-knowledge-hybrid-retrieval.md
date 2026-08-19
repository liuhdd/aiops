# 知识库混合检索（向量 + BM25 + 案例/手册差异化）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在现有知识库基础上引入语义检索：LLM embedding 向量通道 + BM25 关键词通道 + 确定性信号，按「运维案例」与「产品/操作手册」两套差异化权重融合排序；新增 `search_case` / `search_manual` 两个 ReAct 工具；组装器被动注入与聊天 agent 改走同一混合检索入口。

**Architecture:** 新增独立包 `backend/internal/retrieve`（embedder/bm25/vector/rerank/searcher 五文件）；`kb_embeddings` 表存每块向量（入库时预计算）；`retrieve.Searcher` 统一供 context 组装器与两个工具使用；`context` 包只组装、`retrieve` 只检索，依赖方向 `retrieve→(store,model,config)`、`context→retrieve`、`kb→retrieve`，无环。

**Tech Stack:** Go 1.24 + SQLite（`modernc.org/sqlite`，driver 名 `"sqlite"`）。embedding 用标准库 `net/http` + `encoding/json` 直连 OpenAI 兼容 `POST {base}/embeddings`，不新增第三方依赖。注释与 commit message 用中文。

## Global Constraints

- Go 模块 `aiops`，包路径 `aiops/internal/...`。SQLite 单写：`store.New` 已设 `SetMaxOpenConns(1)`，测试用 `filepath.Join(t.TempDir(), "*.db")`，用完 `s.Close()`。
- 标签/意图字段均为 JSON 数组字符串列，空数组存 `[]`；用 `model.MustJSON` 序列化。
- 提交信息用 conventional prefixes（`feat:`/`fix:`/`test:`/`docs:`/`chore:`）。
- 测试命令：后端 `go test ./...`；构建 `go build ./...`。
- 现有 `TestRuleRegression*` 与 `mockdata` 相关测试必须保持通过；无 `LLM_API_KEY` 环境必须回退确定性检索，行为不退步。
- `retrieve` 包**不得 import `context` 包**（依赖环）；`context` 包 import `retrieve` 仅限类型与工厂函数。
- `Selector` 保留作为组装器全不可用时的最终回退；`knowledgeSection` 回显格式不变。

---

### Task 1: model + store 新增 kb_embeddings 表与 chunk_id 检索视图

**Files:**
- Modify: `backend/internal/model/kb.go`
- Modify: `backend/internal/store/store.go`（`migrate()` 追加建表）
- Modify: `backend/internal/store/kb.go`
- Test: `backend/internal/store/kb_test.go`

**Interfaces:**
- Produces:
  - `type KbEmbedding struct{ ChunkID, DocID string; Embedding []float64; Model string; CreatedAt time.Time }`（model/kb.go）
  - `model.KbKnowledgeChunk` 新增字段 `ChunkID string`
  - `KBStore` 接口新增 `UpsertKbEmbedding(e model.KbEmbedding) error`、`DeleteKbEmbeddingsByDoc(docID string) error`
  - `func (s *Store) GetKbEmbeddingsByDoc(docID string) ([]model.KbEmbedding, error)`
  - `func (s *Store) ListKbEmbeddings() ([]model.KbEmbedding, error)`
  - `func (s *Store) DeleteKbEmbeddingsByDoc(docID string) error`

- [ ] **Step 1: 写失败测试**

`store/kb_test.go` 追加（保留 `mkDoc` 辅助函数）：

```go
func TestKbEmbeddingRoundTrip(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	_ = s.WithKBTx(func(tx KBStore) error {
		_ = tx.InsertKbDocument(mkDoc("d1", "t", model.KBOther))
		_ = tx.UpsertKbEmbedding(model.KbEmbedding{ChunkID: "c1", DocID: "d1", Embedding: []float64{0.1, 0.2, 0.3}, Model: "m", CreatedAt: now})
		return tx.UpsertKbEmbedding(model.KbEmbedding{ChunkID: "c1", DocID: "d1", Embedding: []float64{0.9}, Model: "m2", CreatedAt: now})
	})
	byDoc, err := s.GetKbEmbeddingsByDoc("d1")
	if err != nil || len(byDoc) != 1 || len(byDoc[0].Embedding) != 1 || byDoc[0].Embedding[0] != 0.9 {
		t.Fatalf("GetKbEmbeddingsByDoc: %v %+v", err, byDoc)
	}
	all, err := s.ListKbEmbeddings()
	if err != nil || len(all) != 1 || all[0].ChunkID != "c1" {
		t.Fatalf("ListKbEmbeddings: %v %+v", err, all)
	}
}

func TestKbEmbeddingCascadeDelete(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	_ = s.WithKBTx(func(tx KBStore) error {
		_ = tx.InsertKbDocument(mkDoc("d1", "t", model.KBOther))
		_ = tx.InsertKbChunk(model.KbChunk{ID: "c1", DocID: "d1", Seq: 0, Content: "x", CreatedAt: now})
		_ = tx.UpsertKbEmbedding(model.KbEmbedding{ChunkID: "c1", DocID: "d1", Embedding: []float64{1}, Model: "m", CreatedAt: now})
		return tx.DeleteKbEmbeddingsByDoc("d1")
	})
	got, err := s.ListKbEmbeddings()
	if err != nil || len(got) != 0 {
		t.Fatalf("删除后应无向量, got %v %+v", err, got)
	}
}

func TestKbKnowledgeChunksJoinHasChunkID(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	_ = s.WithKBTx(func(tx KBStore) error {
		_ = tx.InsertKbDocument(mkDoc("d1", "交换机手册", model.KBManual))
		_ = tx.InsertKbChunk(model.KbChunk{ID: "c1", DocID: "d1", Seq: 0, Heading: "排查步骤", Content: "检查光模块", CreatedAt: now})
		return tx.InsertKbChunk(model.KbChunk{ID: "c2", DocID: "d1", Seq: 1, Heading: "处理", Content: "更换光模块", CreatedAt: now})
	})
	kcs, err := s.ListKnowledgeChunks()
	if err != nil || len(kcs) != 2 {
		t.Fatalf("ListKnowledgeChunks: %v %+v", err, kcs)
	}
	if kcs[0].ChunkID != "c1" || kcs[1].ChunkID != "c2" {
		t.Fatalf("chunk ids = %s,%s want c1,c2", kcs[0].ChunkID, kcs[1].ChunkID)
	}
}

func TestKbEmbeddingTablesCreated(t *testing.T) {
	s := newTestStore(t)
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='kb_embeddings'`).Scan(&n)
	if err != nil || n != 1 {
		t.Fatalf("kb_embeddings table = %d %v, want 1", n, err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/store/ -run 'TestKbEmbeddingRoundTrip|TestKbEmbeddingCascadeDelete|TestKbKnowledgeChunksJoinHasChunkID|TestKbEmbeddingTablesCreated' -v`
Expected: FAIL——`undefined: KbEmbedding` / 缺表。

- [ ] **Step 3: 写实现**

`model/kb.go` 追加：

```go
// KbEmbedding 知识块向量（kb_embeddings 表）。
type KbEmbedding struct {
	ChunkID   string
	DocID     string
	Embedding []float64
	Model     string
	CreatedAt time.Time
}
```

`model/kb.go` 的 `KbKnowledgeChunk` 增加字段（在 `DocID` 前）：

```go
type KbKnowledgeChunk struct {
	ChunkID     string
	DocID       string
	...
}
```

`store/store.go` 的 `migrate()` 在 `kb_chunks` 索引后追加：

```go
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS kb_embeddings (
		chunk_id   TEXT PRIMARY KEY,
		doc_id     TEXT NOT NULL,
		embedding  TEXT NOT NULL,
		model      TEXT NOT NULL,
		created_at DATETIME)`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_kb_embeddings_doc ON kb_embeddings(doc_id)`); err != nil {
		return err
	}
```

`store/kb.go` 修改 `KBStore` 接口，追加两方法：

```go
type KBStore interface {
	InsertKbDocument(d model.KbDocument) error
	InsertKbChunk(c model.KbChunk) error
	GetKbDocument(id string) (model.KbDocument, error)
	ListKbChunksByDoc(docID string) ([]model.KbChunk, error)
	DeleteKbChunks(docID string) error
	DeleteKbDocument(id string) error
	UpsertKbEmbedding(e model.KbEmbedding) error
	DeleteKbEmbeddingsByDoc(docID string) error
}
```

`store/kb.go` 追加辅助与 repo 方法：

```go
// parseVec 反序列化 JSON 向量数组。
func parseVec(s string) []float64 {
	var v []float64
	if s != "" {
		_ = json.Unmarshal([]byte(s), &v)
	}
	return v
}

func (r *kbRepo) UpsertKbEmbedding(e model.KbEmbedding) error {
	_, err := r.db.Exec(`INSERT INTO kb_embeddings (chunk_id, doc_id, embedding, model, created_at)
		VALUES (?,?,?,?,?)
		ON CONFLICT(chunk_id) DO UPDATE SET embedding=excluded.embedding, model=excluded.model, created_at=excluded.created_at`,
		e.ChunkID, e.DocID, model.MustJSON(e.Embedding), e.Model, e.CreatedAt)
	return err
}

func scanKbEmbedding(scan func(dest ...any) error) (model.KbEmbedding, error) {
	var e model.KbEmbedding
	var emb string
	if err := scan(&e.ChunkID, &e.DocID, &emb, &e.Model, &e.CreatedAt); err != nil {
		return e, err
	}
	e.Embedding = parseVec(emb)
	return e, nil
}

func (r *kbRepo) GetKbEmbeddingsByDoc(docID string) ([]model.KbEmbedding, error) {
	rows, err := r.db.Query(`SELECT chunk_id, doc_id, embedding, model, created_at FROM kb_embeddings WHERE doc_id = ?`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.KbEmbedding, 0)
	for rows.Next() {
		e, err := scanKbEmbedding(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *kbRepo) ListKbEmbeddings() ([]model.KbEmbedding, error) {
	rows, err := r.db.Query(`SELECT chunk_id, doc_id, embedding, model, created_at FROM kb_embeddings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.KbEmbedding, 0)
	for rows.Next() {
		e, err := scanKbEmbedding(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *kbRepo) DeleteKbEmbeddingsByDoc(docID string) error {
	_, err := r.db.Exec(`DELETE FROM kb_embeddings WHERE doc_id = ?`, docID)
	return err
}
```

`store/kb.go` 的 `ListKnowledgeChunks` 改为联查 `c.id` 并扫描 ChunkID（`SELECT c.id, c.doc_id, ...`，scan 顺序对应）：

```go
func (s *Store) ListKnowledgeChunks() ([]model.KbKnowledgeChunk, error) {
	rows, err := s.db.Query(`SELECT c.id, c.doc_id, d.title, d.category, d.device_types, d.vendor, d.model, d.metrics, d.keywords, d.intents, c.heading, c.content, d.created_at
		FROM kb_chunks c JOIN kb_documents d ON d.id = c.doc_id
		ORDER BY d.created_at DESC, c.seq ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.KbKnowledgeChunk, 0)
	for rows.Next() {
		var k model.KbKnowledgeChunk
		var dt, vd, md, mtr, kw, in string
		if err := rows.Scan(&k.ChunkID, &k.DocID, &k.Title, &k.Category, &dt, &vd, &md, &mtr, &kw, &in, &k.Heading, &k.Content, &k.CreatedAt); err != nil {
			return nil, err
		}
		k.DeviceTypes = parseArr(dt)
		k.Vendor = parseArr(vd)
		k.Model = parseArr(md)
		k.Metrics = parseArr(mtr)
		k.Keywords = parseArr(kw)
		k.Intents = parseArr(in)
		out = append(out, k)
	}
	return out, rows.Err()
}
```

`store/kb.go` 的 `*Store` 委托方法（追加在 `ListKbChunksByDoc` 之后）：

```go
func (s *Store) GetKbEmbeddingsByDoc(docID string) ([]model.KbEmbedding, error) {
	return (&kbRepo{db: s.db}).GetKbEmbeddingsByDoc(docID)
}

func (s *Store) ListKbEmbeddings() ([]model.KbEmbedding, error) {
	return (&kbRepo{db: s.db}).ListKbEmbeddings()
}

func (s *Store) DeleteKbEmbeddingsByDoc(docID string) error {
	return (&kbRepo{db: s.db}).DeleteKbEmbeddingsByDoc(docID)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/store/ ./internal/model/ -v`
Expected: 全部 PASS，含既有测试。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/model/kb.go backend/internal/store/store.go backend/internal/store/kb.go backend/internal/store/kb_test.go
git commit -m "feat: 新增kb_embeddings表与chunk_id检索视图"
```

---

### Task 2: config 新增 embedding 环境变量

**Files:**
- Modify: `backend/internal/config/config.go`
- Test: `backend/internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `Config` 新增字段 `LLMEmbedModel string`（`LLM_EMBED_MODEL`，默认 `text-embedding-3-small`）、`LLMEmbedBaseURL string`（`LLM_EMBED_BASE_URL`，默认同 `LLM_BASE_URL`）

- [ ] **Step 1: 写失败测试**

`config_test.go` 追加：

```go
func TestLoadFromEnvEmbedDefaults(t *testing.T) {
	env := map[string]string{"LLM_BASE_URL": "https://main.example.com/v1", "LLM_MODEL": "main-model"}
	cfg := LoadFromEnv(func(k string) string { return env[k] })
	if cfg.LLMEmbedBaseURL != "https://main.example.com/v1" {
		t.Fatalf("LLMEmbedBaseURL = %q, want 同 LLM_BASE_URL", cfg.LLMEmbedBaseURL)
	}
	if cfg.LLMEmbedModel != "text-embedding-3-small" {
		t.Fatalf("LLMEmbedModel = %q, want text-embedding-3-small", cfg.LLMEmbedModel)
	}
}

func TestLoadFromEnvEmbedOverride(t *testing.T) {
	env := map[string]string{
		"LLM_BASE_URL":      "https://main.example.com/v1",
		"LLM_EMBED_MODEL":   "text-embedding-3-large",
		"LLM_EMBED_BASE_URL": "https://embed.example.com/v1",
	}
	cfg := LoadFromEnv(func(k string) string { return env[k] })
	if cfg.LLMEmbedBaseURL != "https://embed.example.com/v1" {
		t.Fatalf("LLMEmbedBaseURL = %q, want override", cfg.LLMEmbedBaseURL)
	}
	if cfg.LLMEmbedModel != "text-embedding-3-large" {
		t.Fatalf("LLMEmbedModel = %q, want override", cfg.LLMEmbedModel)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/config/ -run 'TestLoadFromEnvEmbed' -v`
Expected: FAIL——`cfg.LLMEmbedBaseURL` 无此字段。

- [ ] **Step 3: 写实现**

`config.go` 的 `Config` 结构体追加两字段：

```go
	LLMLightBaseURL string // LLM_LIGHT_BASE_URL，轻量意图模型 BaseURL，默认同 LLM_BASE_URL
	LLMLightModel   string // LLM_LIGHT_MODEL，轻量意图模型，默认同 LLM_MODEL
	LLMEmbedBaseURL string // LLM_EMBED_BASE_URL，embedding 接口 BaseURL，默认同 LLM_BASE_URL
	LLMEmbedModel   string // LLM_EMBED_MODEL，embedding 模型，默认 text-embedding-3-small
	MaxSplit        int    // INTENT_MAX_SPLIT，单条输入多意图拆分上限，默认 3
```

`LoadFromEnv` 中 `maxSplit` 赋值后追加：

```go
	embedBase := lookup("LLM_EMBED_BASE_URL")
	if embedBase == "" {
		embedBase = base
	}
	embedModel := lookup("LLM_EMBED_MODEL")
	if embedModel == "" {
		embedModel = "text-embedding-3-small"
	}
```

`return Config{...}` 追加：

```go
		LLMEmbedBaseURL: embedBase,
		LLMEmbedModel:   embedModel,
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/config/ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/config/config.go backend/internal/config/config_test.go
git commit -m "feat: config新增LLM_EMBED_MODEL/LLM_EMBED_BASE_URL"
```

---

### Task 3: retrieve 包 — Embedder 接口与 LLMEmbedder

**Files:**
- Create: `backend/internal/retrieve/embedder.go`
- Test: `backend/internal/retrieve/embedder_test.go`

**Interfaces:**
- Consumes: `config.Config`（Task 2）
- Produces:
  - `var ErrNoEmbed = errors.New("embedding 不可用")`
  - `type Embedder interface{ Model() string; Embed(ctx context.Context, texts []string) ([][]float64, error) }`
  - `type LLMEmbedder struct{...}`
  - `func NewLLMEmbedder(cfg config.Config) (*LLMEmbedder, error)`（无 key 返回 `nil, ErrNoEmbed`）
  - `func Normalize(v []float64) []float64`（原地归一化为单位向量；零向量返回原样）

- [ ] **Step 1: 写失败测试（新建 `embedder_test.go`）**

```go
package retrieve

import (
	"context"
	"errors"
	"math"
	"testing"

	"aiops/internal/config"
)

func TestNewLLMEmbedderNoKey(t *testing.T) {
	e, err := NewLLMEmbedder(config.Config{})
	if !errors.Is(err, ErrNoEmbed) || e != nil {
		t.Fatalf("无 key 应 ErrNoEmbed, got %v %v", e, err)
	}
}

func TestNormalize(t *testing.T) {
	v := Normalize([]float64{3, 4})
	if math.Abs(math.Sqrt(v[0]*v[0]+v[1]*v[1])-1) > 1e-9 {
		t.Fatalf("非单位向量: %v", v)
	}
	z := Normalize([]float64{0, 0})
	if z[0] != 0 || z[1] != 0 {
		t.Fatalf("零向量应原样: %v", z)
	}
}

type fakeCompleter struct{}

func (fakeCompleter) Model() string { return "test" }
func (fakeCompleter) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i := range texts {
		out[i] = []float64{float64(i + 1), 1}
	}
	return out, nil
}

func TestEmbedderContract(t *testing.T) {
	var e Embedder = fakeCompleter{}
	if e.Model() != "test" {
		t.Fatalf("model = %q", e.Model())
	}
	vecs, err := e.Embed(context.Background(), []string{"a", "b"})
	if err != nil || len(vecs) != 2 || len(vecs[0]) != 2 {
		t.Fatalf("embed = %v %v", vecs, err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/retrieve/ -run 'TestNewLLMEmbedderNoKey|TestNormalize|TestEmbedderContract' -v`
Expected: FAIL——包目录/符号不存在。

- [ ] **Step 3: 写实现（新建 `embedder.go`）**

```go
package retrieve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"aiops/internal/config"
)

var ErrNoEmbed = errors.New("embedding 不可用")

// Embedder 向量生成接口；Model 返回生成模型名（用于落库记录）。
type Embedder interface {
	Model() string
	Embed(ctx context.Context, texts []string) ([][]float64, error)
}

// LLMEmbedder 通过 OpenAI 兼容 embeddings REST 接口生成向量。
type LLMEmbedder struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
	dim     int
}

// NewLLMEmbedder 构造 LLMEmbedder；未配置 LLM_API_KEY 时返回 ErrNoEmbed。
func NewLLMEmbedder(cfg config.Config) (*LLMEmbedder, error) {
	if cfg.LLMAPIKey == "" {
		return nil, ErrNoEmbed
	}
	base := cfg.LLMEmbedBaseURL
	if base == "" {
		base = cfg.LLMBaseURL
	}
	model := cfg.LLMEmbedModel
	if model == "" {
		model = "text-embedding-3-small"
	}
	return &LLMEmbedder{
		baseURL: strings.TrimRight(base, "/"),
		apiKey:  cfg.LLMAPIKey,
		model:   model,
		client:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (e *LLMEmbedder) Model() string { return e.model }

// Embed 批量生成向量；返回的向量已归一化为单位向量。
func (e *LLMEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(map[string]any{"model": e.model, "input": texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embeddings status %d", resp.StatusCode)
	}
	var parsed struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if len(parsed.Data) == 0 {
		return nil, errors.New("embeddings 响应为空")
	}
	if e.dim == 0 {
		e.dim = len(parsed.Data[0].Embedding)
	}
	out := make([][]float64, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		if len(d.Embedding) != e.dim {
			return nil, fmt.Errorf("embeddings 维度不一致: %d != %d", len(d.Embedding), e.dim)
		}
		out = append(out, Normalize(d.Embedding))
	}
	return out, nil
}

// Normalize 将向量归一化为单位向量（原地修改）；零向量返回原样。
func Normalize(v []float64) []float64 {
	norm := 0.0
	for _, x := range v {
		norm += x * x
	}
	norm = math.Sqrt(norm)
	if norm < 1e-12 {
		return v
	}
	for i := range v {
		v[i] /= norm
	}
	return v
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/retrieve/ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/retrieve/embedder.go backend/internal/retrieve/embedder_test.go
git commit -m "feat: retrieve新增Embedder接口与LLMEmbedder"
```

---

### Task 4: retrieve — BM25 关键词通道

**Files:**
- Create: `backend/internal/retrieve/bm25.go`
- Test: `backend/internal/retrieve/bm25_test.go`

**Interfaces:**
- Consumes: `model.KbKnowledgeChunk`（Task 1）
- Produces:
  - `func Tokenize(s string) []string`（英文/数字按空格小写分词；含 CJK 的 token 按单字拆）
  - `type BM25Index struct{...}`
  - `func BuildBM25(chunks []model.KbKnowledgeChunk, ids []string) *BM25Index`（每块一个"文档"，id 与 chunk 一一对应）
  - `func (ix *BM25Index) Score(query string) map[string]float64`（返回 chunkID → 分数；无命中为空 map）

- [ ] **Step 1: 写失败测试（新建 `bm25_test.go`）**

```go
package retrieve

import (
	"testing"

	"aiops/internal/model"
)

func TestTokenize(t *testing.T) {
	got := Tokenize("GPU Xid 掉卡")
	want := map[string]bool{"gpu": true, "xid": true, "掉": true, "卡": true}
	if len(got) != 4 {
		t.Fatalf("tokens = %v", got)
	}
	for _, g := range got {
		if !want[g] {
			t.Fatalf("unexpected token %q in %v", g, got)
		}
	}
}

func TestBM25ScoresRelevantHigher(t *testing.T) {
	chunks := []model.KbKnowledgeChunk{
		{ChunkID: "c1", Content: "GPU Xid 错误导致掉卡，检查驱动与NVLink"},
		{ChunkID: "c2", Content: "磁盘空间不足，清理日志或扩容"},
	}
	ids := []string{"c1", "c2"}
	ix := BuildBM25(chunks, ids)
	got := ix.Score("GPU 掉卡")
	if got["c1"] <= 0 {
		t.Fatalf("c1 应命中: %v", got)
	}
	if got["c1"] <= got["c2"] {
		t.Fatalf("c1 相关度应高于 c2: %v", got)
	}
}

func TestBM25Empty(t *testing.T) {
	ix := BuildBM25(nil, nil)
	if got := ix.Score("x"); len(got) != 0 {
		t.Fatalf("空索引应返回空: %v", got)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/retrieve/ -run 'TestTokenize|TestBM25' -v`
Expected: FAIL——`undefined: Tokenize`/`BM25Index`。

- [ ] **Step 3: 写实现（新建 `bm25.go`）**

```go
package retrieve

import (
	"math"
	"strings"

	"aiops/internal/model"
)

// Tokenize 切词：含 CJK 的 token 按单字拆，其余按空格分词并小写。
func Tokenize(s string) []string {
	var out []string
	for _, tok := range strings.Fields(s) {
		if hasCJK(tok) {
			for _, r := range tok {
				out = append(out, string(r))
			}
		} else {
			out = append(out, strings.ToLower(tok))
		}
	}
	return out
}

func hasCJK(s string) bool {
	for _, r := range s {
		if r >= 0x4e00 && r <= 0x9fff {
			return true
		}
	}
	return false
}

// BM25Index 内存倒排索引；每个知识块视为一个文档。
type BM25Index struct {
	k1, b  float64
	avgLen float64
	docLen []float64
	df     map[string]int
	tf     map[string]map[int]int
	ids    []string
}

// BuildBM25 从知识块构建索引；ids 与 chunks 一一对应。
func BuildBM25(chunks []model.KbKnowledgeChunk, ids []string) *BM25Index {
	ix := &BM25Index{k1: 1.5, b: 0.75, df: map[string]int{}, tf: map[string]map[int]int{}, ids: ids}
	totalLen := 0.0
	for i, c := range chunks {
		toks := Tokenize(c.Content)
		ix.docLen = append(ix.docLen, float64(len(toks)))
		totalLen += float64(len(toks))
		seen := map[string]bool{}
		for _, t := range toks {
			if ix.tf[t] == nil {
				ix.tf[t] = map[int]int{}
			}
			ix.tf[t][i]++
			if !seen[t] {
				seen[t] = true
				ix.df[t]++
			}
		}
	}
	if len(chunks) > 0 {
		ix.avgLen = totalLen / float64(len(chunks))
	}
	return ix
}

// Score 返回 chunkID → BM25 分数；查询无命中返回空 map。
func (ix *BM25Index) Score(query string) map[string]float64 {
	out := map[string]float64{}
	n := len(ix.docLen)
	if n == 0 {
		return out
	}
	for _, t := range Tokenize(query) {
		df := ix.df[t]
		if df == 0 {
			continue
		}
		idf := math.Log(1 + (float64(n)-float64(df)+0.5)/(float64(df)+0.5))
		for idx, tf := range ix.tf[t] {
			denom := tf + ix.k1*(1-ix.b+ix.b*ix.docLen[idx]/ix.avgLen)
			out[ix.ids[idx]] += idf * tf * (ix.k1 + 1) / denom
		}
	}
	return out
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/retrieve/ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/retrieve/bm25.go backend/internal/retrieve/bm25_test.go
git commit -m "feat: retrieve新增BM25关键词通道"
```

---

### Task 5: retrieve — 向量余弦与信号打分

**Files:**
- Create: `backend/internal/retrieve/vector.go`
- Test: `backend/internal/retrieve/vector_test.go`

**Interfaces:**
- Consumes: `model.KbKnowledgeChunk`、`Tokenize`（Task 4）
- Produces:
  - `func Cosine(a, b []float64) float64`（维度不一致返回 0）
  - `type Query struct{ Text string; Category model.KBCategory; Metric, Device, Vendor, Model string; Intents []string }`
  - `func LabelScore(chunk model.KbKnowledgeChunk, q Query) float64`（案例信号：metric/device/keywords/意图命中）
  - `func FieldScore(chunk model.KbKnowledgeChunk, q Query) float64`（手册信号：vendor/model/device_types/metrics；有字段全不命中则 0.1）
  - `func RecencyScore(createdAt time.Time, now time.Time) float64`（`exp(-ageMin/60)`）

- [ ] **Step 1: 写失败测试（新建 `vector_test.go`）**

```go
package retrieve

import (
	"math"
	"testing"
	"time"

	"aiops/internal/model"
)

func TestCosine(t *testing.T) {
	if got := Cosine([]float64{1, 0}, []float64{1, 0}); math.Abs(got-1) > 1e-9 {
		t.Fatalf("cos = %v, want 1", got)
	}
	if got := Cosine([]float64{1, 0}, []float64{0, 1}); math.Abs(got) > 1e-9 {
		t.Fatalf("cos = %v, want 0", got)
	}
	if got := Cosine([]float64{1}, []float64{1, 0}); got != 0 {
		t.Fatalf("维度不一致应为0, got %v", got)
	}
}

func TestLabelScore(t *testing.T) {
	kc := model.KbKnowledgeChunk{DeviceTypes: []string{"server"}, Metrics: []string{"disk_usage"},
		Keywords: []string{"扩容"}, Intents: []string{"AskRootCause"}}
	q := Query{Metric: "disk_usage", Device: "server", Text: "磁盘扩容", Intents: []string{"AskRootCause"}}
	if s := LabelScore(kc, q); s <= 0 {
		t.Fatalf("label score = %v, want > 0", s)
	}
	if s := LabelScore(kc, Query{Metric: "gpu_util"}); s != 0 {
		t.Fatalf("不相关应 0, got %v", s)
	}
}

func TestFieldScore(t *testing.T) {
	kc := model.KbKnowledgeChunk{Vendor: []string{"H3C"}, Model: []string{"S5560"},
		DeviceTypes: []string{"switch"}, Metrics: []string{"net_in"}}
	if s := FieldScore(kc, Query{Vendor: "H3C", Metric: "net_in"}); s < 2 {
		t.Fatalf("field score = %v, want >= 2", s)
	}
	if s := FieldScore(kc, Query{Vendor: "Dell"}); s != 0.1 {
		t.Fatalf("有字段全不命中应 0.1, got %v", s)
	}
	if s := FieldScore(kc, Query{}); s != 0 {
		t.Fatalf("无字段应 0, got %v", s)
	}
}

func TestRecencyScore(t *testing.T) {
	now := time.Now()
	old := now.Add(-120 * time.Minute)
	if s := RecencyScore(old, now); s <= 0 || s >= 1 {
		t.Fatalf("recency = %v, want (0,1)", s)
	}
	if s := RecencyScore(now, now); s != 1 {
		t.Fatalf("recency now = %v, want 1", s)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/retrieve/ -run 'TestCosine|TestLabelScore|TestFieldScore|TestRecencyScore' -v`
Expected: FAIL——`undefined: Cosine`/`Query` 等。

- [ ] **Step 3: 写实现（新建 `vector.go`）**

```go
package retrieve

import (
	"math"
	"strings"
	"time"

	"aiops/internal/model"
)

// Cosine 计算两向量余弦相似度；维度不一致返回 0。
func Cosine(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	dot := 0.0
	for i := range a {
		dot += a[i] * b[i]
	}
	return dot
}

// Query 混合检索查询条件；空字段忽略。
type Query struct {
	Text     string
	Category model.KBCategory
	Metric   string
	Device   string
	Vendor   string
	Model    string
	Intents  []string
}

// LabelScore 案例信号：metric/device 标签命中 + keywords 词重叠 + 意图命中。
func LabelScore(kc model.KbKnowledgeChunk, q Query) float64 {
	score := 0.0
	if q.Metric != "" && containsStr(kc.Metrics, q.Metric) {
		score += 1
	}
	if q.Device != "" && containsStr(kc.DeviceTypes, q.Device) {
		score += 1
	}
	if q.Text != "" {
		for _, t := range Tokenize(q.Text) {
			if len(t) > 1 && containsStr(kc.Keywords, t) {
				score += 0.5
			}
		}
	}
	if len(q.Intents) > 0 && intersect(q.Intents, kc.Intents) {
		score += 1
	}
	return score
}

// FieldScore 手册信号：vendor/model/device_types/metrics 命中；
// query 携带字段但全部不命中时返回 0.1 强降权。
func FieldScore(kc model.KbKnowledgeChunk, q Query) float64 {
	hits := 0.0
	if q.Vendor != "" && containsStr(kc.Vendor, q.Vendor) {
		hits++
	}
	if q.Model != "" && containsStr(kc.Model, q.Model) {
		hits++
	}
	if q.Device != "" && containsStr(kc.DeviceTypes, q.Device) {
		hits++
	}
	if q.Metric != "" && containsStr(kc.Metrics, q.Metric) {
		hits++
	}
	if hits == 0 && (q.Vendor != "" || q.Model != "" || q.Device != "" || q.Metric != "") {
		return 0.1
	}
	return hits
}

// RecencyScore 时效衰减：60 分钟半衰期。
func RecencyScore(createdAt time.Time, now time.Time) float64 {
	ageMin := now.Sub(createdAt).Minutes()
	if ageMin < 0 {
		ageMin = 0
	}
	return math.Exp(-ageMin / 60)
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

func intersect(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/retrieve/ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/retrieve/vector.go backend/internal/retrieve/vector_test.go
git commit -m "feat: retrieve新增余弦相似度与案例/手册信号打分"
```

---

### Task 6: retrieve — 融合 rerank（case/manual 两套权重）

**Files:**
- Create: `backend/internal/retrieve/rerank.go`
- Test: `backend/internal/retrieve/rerank_test.go`

**Interfaces:**
- Consumes: `Query`/`LabelScore`/`FieldScore`/`RecencyScore`（Task 5）
- Produces:
  - `type cand struct{ Chunk model.KbKnowledgeChunk; BM25, Vec, Signal, Recency, Final float64 }`
  - `func RankCases(cands []cand, now time.Time) []cand`（权重 向量 0.45 / 信号 0.30 / BM25 0.15 / 时效 0.10，按 Final 降序）
  - `func RankManuals(cands []cand, now time.Time) []cand`（权重 BM25 0.50 / 信号 0.30 / 向量 0.20，无时效，按 Final 降序）

- [ ] **Step 1: 写失败测试（新建 `rerank_test.go`）**

```go
package retrieve

import (
	"testing"
	"time"

	"aiops/internal/model"
)

func mkCand(id string, bm25, vec, sig, rec float64) cand {
	return cand{Chunk: model.KbKnowledgeChunk{ChunkID: id}, BM25: bm25, Vec: vec, Signal: sig, Recency: rec}
}

func TestRankCasesOrderByFinal(t *testing.T) {
	now := time.Now()
	cands := []cand{
		mkCand("semantic", 0.0, 1.0, 0.0, 0.5),
		mkCand("labeled", 0.0, 0.0, 1.0, 0.5),
	}
	got := RankCases(cands, now)
	if got[0].Chunk.ChunkID != "semantic" {
		t.Fatalf("向量权重最高应排前, got %s first", got[0].Chunk.ChunkID)
	}
}

func TestRankManualsBM25Dominates(t *testing.T) {
	now := time.Now()
	cands := []cand{
		mkCand("kw", 1.0, 0.0, 0.0, 0.0),
		mkCand("vec", 0.0, 0.9, 0.0, 0.0),
	}
	got := RankManuals(cands, now)
	if got[0].Chunk.ChunkID != "kw" {
		t.Fatalf("手册 BM25 权重最高应排前, got %s first", got[0].Chunk.ChunkID)
	}
}

func TestRankManualsNoRecency(t *testing.T) {
	now := time.Now()
	cands := []cand{mkCand("a", 1, 0, 0, 0.9), mkCand("b", 1, 0, 0, 0.1)}
	got := RankManuals(cands, now)
	if got[0].Final != got[1].Final {
		t.Fatalf("手册不应受时效影响: %f != %f", got[0].Final, got[1].Final)
	}
}

func TestRankEmpty(t *testing.T) {
	got := RankCases(nil, time.Now())
	if len(got) != 0 {
		t.Fatalf("空输入应空输出, got %v", got)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/retrieve/ -run 'TestRank' -v`
Expected: FAIL——`undefined: cand`/`RankCases`。

- [ ] **Step 3: 写实现（新建 `rerank.go`）**

```go
package retrieve

import (
	"sort"
	"time"

	"aiops/internal/model"
)

const (
	caseVecWeight    = 0.45
	caseLabelWeight  = 0.30
	caseBM25Weight   = 0.15
	caseRecencyW    = 0.10

	manualBM25Weight = 0.50
	manualFieldWeight = 0.30
	manualVecWeight   = 0.20
)

// cand 融合候选：各通道原始分 + 融合分。
type cand struct {
	Chunk   model.KbKnowledgeChunk
	BM25    float64
	Vec     float64
	Signal  float64
	Recency float64
	Final   float64
}

// RankCases 运维案例融合排序（语义为主）。输入候选原地排序后返回。
func RankCases(cands []cand, now time.Time) []cand {
	bm := minmax(get(cands, func(c cand) float64 { return c.BM25 }))
	vec := minmax(get(cands, func(c cand) float64 { return c.Vec }))
	sig := minmax(get(cands, func(c cand) float64 { return c.Signal }))
	rec := minmax(get(cands, func(c cand) float64 { return c.Recency }))
	for i := range cands {
		cands[i].Final = caseVecWeight*vec[i] + caseLabelWeight*sig[i] +
			caseBM25Weight*bm[i] + caseRecencyW*rec[i]
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Final > cands[j].Final })
	return cands
}

// RankManuals 手册融合排序（关键词为主，无时效）。输入候选原地排序后返回。
func RankManuals(cands []cand, now time.Time) []cand {
	bm := minmax(get(cands, func(c cand) float64 { return c.BM25 }))
	vec := minmax(get(cands, func(c cand) float64 { return c.Vec }))
	sig := minmax(get(cands, func(c cand) float64 { return c.Signal }))
	for i := range cands {
		cands[i].Final = manualBM25Weight*bm[i] + manualFieldWeight*sig[i] + manualVecWeight*vec[i]
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Final > cands[j].Final })
	return cands
}

func get(cands []cand, f func(cand) float64) []float64 {
	out := make([]float64, len(cands))
	for i, c := range cands {
		out[i] = f(c)
	}
	return out
}

// minmax 归一化到 [0,1]；最大值=最小值（含全 0）时全部置 0。
func minmax(vals []float64) []float64 {
	if len(vals) == 0 {
		return vals
	}
	lo, hi := vals[0], vals[0]
	for _, v := range vals[1:] {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	if hi-lo < 1e-12 {
		for i := range vals {
			vals[i] = 0
		}
		return vals
	}
	for i := range vals {
		vals[i] = (vals[i] - lo) / (hi - lo)
	}
	return vals
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/retrieve/ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/retrieve/rerank.go backend/internal/retrieve/rerank_test.go
git commit -m "feat: retrieve新增案例/手册两套融合rerank"
```

---

### Task 7: retrieve — Searcher 编排与 Router

**Files:**
- Create: `backend/internal/retrieve/searcher.go`
- Test: `backend/internal/retrieve/searcher_test.go`

**Interfaces:**
- Consumes: `Embedder`/`ErrNoEmbed`（Task 3）、`BuildBM25`/`BM25Index`（Task 4）、`Cosine`/`Query`/`LabelScore`/`FieldScore`/`RecencyScore`（Task 5）、`RankCases`/`RankManuals`/`cand`（Task 6）、`store.Store`/`model.KbKnowledgeChunk`（Task 1）
- Produces:
  - `type Options struct{ TopK int }`（默认 3）
  - `type Result struct{ DocID, Title string; Category model.KBCategory; Heading, Content string; Score float64 }`
  - `type Searcher struct{...}`
  - `func NewSearcher(st *store.Store, embedder Embedder) *Searcher`
  - `func (s *Searcher) SearchCases(ctx context.Context, q Query, opts Options) ([]Result, error)`（过滤 `category='case'`）
  - `func (s *Searcher) SearchManuals(ctx context.Context, q Query, opts Options) ([]Result, error)`（过滤 `category IN ('manual','runbook')`）
  - `func Router(stage string, intents []string, leaves []model.LeafSpec) (int, int)`（返回案例 topK、手册 topK 配额）

- [ ] **Step 1: 写失败测试（新建 `searcher_test.go`）**

```go
package retrieve

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aiops/internal/model"
	"aiops/internal/store"
)

type fakeEmbedder struct{}

func (fakeEmbedder) Model() string { return "fake" }
func (fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i, t := range texts {
		switch {
		case strings.Contains(t, "gpu"), strings.Contains(t, "掉卡"):
			out[i] = []float64{1, 0}
		case strings.Contains(t, "磁盘"), strings.Contains(t, "扩容"):
			out[i] = []float64{0, 1}
		default:
			out[i] = []float64{0.7071, 0.7071}
		}
	}
	return out, nil
}

func seedRetrieveStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Now()
	_ = s.WithKBTx(func(tx store.KBStore) error {
		_ = tx.InsertKbDocument(model.KbDocument{ID: "d1", Title: "GPU掉卡案例", Category: model.KBCase,
			DeviceTypes: []string{"gpu-server"}, Metrics: []string{"gpu_util"}, Keywords: []string{"掉卡"},
			Intents: []string{"AskRootCause"}, Content: "GPU Xid 错误导致掉卡", CreatedAt: now, UpdatedAt: now})
		_ = tx.InsertKbChunk(model.KbChunk{ID: "c1", DocID: "d1", Seq: 0, Content: "GPU Xid 错误导致掉卡，检查驱动与NVLink", CreatedAt: now})
		_ = tx.InsertKbDocument(model.KbDocument{ID: "d2", Title: "磁盘扩容案例", Category: model.KBCase,
			DeviceTypes: []string{"server"}, Metrics: []string{"disk_usage"}, Keywords: []string{"扩容"},
			Content: "磁盘空间不足时扩容", CreatedAt: now, UpdatedAt: now})
		_ = tx.InsertKbChunk(model.KbChunk{ID: "c2", DocID: "d2", Seq: 0, Content: "磁盘空间不足时扩容或清理日志", CreatedAt: now})
		_ = tx.InsertKbDocument(model.KbDocument{ID: "d3", Title: "交换机手册", Category: model.KBManual,
			Vendor: []string{"H3C"}, DeviceTypes: []string{"switch"}, Metrics: []string{"net_in"},
			Content: "光模块告警排查与更换步骤", CreatedAt: now, UpdatedAt: now})
		_ = tx.InsertKbChunk(model.KbChunk{ID: "c3", DocID: "d3", Seq: 0, Heading: "排查", Content: "光模块告警排查与更换步骤", CreatedAt: now})
		_ = tx.UpsertKbEmbedding(model.KbEmbedding{ChunkID: "c1", DocID: "d1", Embedding: []float64{1, 0}, Model: "fake", CreatedAt: now})
		_ = tx.UpsertKbEmbedding(model.KbEmbedding{ChunkID: "c2", DocID: "d2", Embedding: []float64{0, 1}, Model: "fake", CreatedAt: now})
		_ = tx.UpsertKbEmbedding(model.KbEmbedding{ChunkID: "c3", DocID: "d3", Embedding: []float64{0.7071, 0.7071}, Model: "fake", CreatedAt: now})
		return nil
	})
	return s
}

func TestSearchCasesSemanticRank(t *testing.T) {
	s := seedRetrieveStore(t)
	se := NewSearcher(s, fakeEmbedder{})
	res, err := se.SearchCases(context.Background(), Query{Text: "GPU 掉卡"}, Options{TopK: 2})
	if err != nil {
		t.Fatalf("SearchCases: %v", err)
	}
	if len(res) != 2 || res[0].DocID != "d1" {
		t.Fatalf("案例应命中 d1 优先: %+v", res)
	}
}

func TestSearchCasesOnlyCases(t *testing.T) {
	s := seedRetrieveStore(t)
	se := NewSearcher(s, fakeEmbedder{})
	res, err := se.SearchCases(context.Background(), Query{Text: "光模块"}, Options{TopK: 3})
	if err != nil || len(res) != 0 {
		t.Fatalf("案例检索不应返回手册: %v %+v", err, res)
	}
}

func TestSearchManualsOnlyManuals(t *testing.T) {
	s := seedRetrieveStore(t)
	se := NewSearcher(s, fakeEmbedder{})
	res, err := se.SearchManuals(context.Background(), Query{Text: "光模块"}, Options{TopK: 2})
	if err != nil {
		t.Fatalf("SearchManuals: %v", err)
	}
	if len(res) != 1 || res[0].DocID != "d3" {
		t.Fatalf("手册检索应命中 d3: %+v", res)
	}
}

func TestSearchFallbackWithoutEmbedder(t *testing.T) {
	s := seedRetrieveStore(t)
	se := NewSearcher(s, nil)
	res, err := se.SearchCases(context.Background(), Query{Text: "GPU 掉卡"}, Options{TopK: 2})
	if err != nil {
		t.Fatalf("无embedder不应报错: %v", err)
	}
	if len(res) == 0 {
		t.Fatalf("无embedder 应退化为BM25/信号检索: %+v", res)
	}
}

func TestRouter(t *testing.T) {
	if c, m := Router("rca", nil, nil); c != 2 || m != 1 {
		t.Fatalf("rca 配额 = %d,%d want 2,1", c, m)
	}
	if c, m := Router("recommend", nil, nil); c != 1 || m != 2 {
		t.Fatalf("recommend 配额 = %d,%d want 1,2", c, m)
	}
	if c, m := Router("chat", []string{"AskRootCause"}, nil); c != 3 || m != 0 {
		t.Fatalf("AskRootCause 配额 = %d,%d want 3,0", c, m)
	}
	if c, m := Router("chat", []string{"AskRecommendation"}, nil); c != 0 || m != 3 {
		t.Fatalf("AskRecommendation 配额 = %d,%d want 0,3", c, m)
	}
	if c, m := Router("chat", []string{"DiskCleanup"}, []model.LeafSpec{
		{Code: "DiskCleanup", RuleKeywords: []string{"清理", "处置"}},
	}); c != 0 || m != 3 {
		t.Fatalf("操作类意图配额 = %d,%d want 0,3", c, m)
	}
	if c, m := Router("chat", nil, nil); c != 2 || m != 1 {
		t.Fatalf("无意图默认配额 = %d,%d want 2,1", c, m)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/retrieve/ -run 'TestSearch|TestRouter' -v`
Expected: FAIL——`undefined: Searcher`/`Router`。

- [ ] **Step 3: 写实现（新建 `searcher.go`）**

```go
package retrieve

import (
	"context"
	"strings"
	"time"

	"aiops/internal/model"
	"aiops/internal/store"
)

// Options 检索选项。
type Options struct {
	TopK int
}

// Result 检索结果块（供工具与组装器消费）。
type Result struct {
	DocID    string
	Title    string
	Category model.KBCategory
	Heading  string
	Content  string
	Score    float64
}

// Searcher 混合检索引擎：BM25 + 向量 + 信号，按类型差异化融合。
type Searcher struct {
	st       *store.Store
	embedder Embedder
	idx      *BM25Index
	docCount int
}

// NewSearcher 构造 Searcher；embedder 可为 nil（向量通道回退）。
func NewSearcher(st *store.Store, embedder Embedder) *Searcher {
	return &Searcher{st: st, embedder: embedder}
}

// ensureIndex 知识库文档数变化时重建 BM25 索引。
func (s *Searcher) ensureIndex() error {
	n, err := s.st.CountKbDocuments()
	if err != nil {
		return err
	}
	if s.idx != nil && n == s.docCount {
		return nil
	}
	kcs, err := s.st.ListKnowledgeChunks()
	if err != nil {
		return err
	}
	ids := make([]string, len(kcs))
	for i, kc := range kcs {
		ids[i] = kc.ChunkID
	}
	s.idx = BuildBM25(kcs, ids)
	s.docCount = n
	return nil
}

// SearchCases 检索相似运维案例（category='case'），语义为主。
func (s *Searcher) SearchCases(ctx context.Context, q Query, opts Options) ([]Result, error) {
	return s.search(ctx, q, model.KBCase, false, opts)
}

// SearchManuals 检索产品/操作手册（category IN manual/runbook），关键词为主。
func (s *Searcher) SearchManuals(ctx context.Context, q Query, opts Options) ([]Result, error) {
	return s.search(ctx, q, "", true, opts)
}

func (s *Searcher) search(ctx context.Context, q Query, cat model.KBCategory, isManual bool, opts Options) ([]Result, error) {
	topK := opts.TopK
	if topK <= 0 {
		topK = 3
	}
	if err := s.ensureIndex(); err != nil {
		return nil, err
	}
	kcs, err := s.st.ListKnowledgeChunks()
	if err != nil {
		return nil, err
	}
	embeds := map[string][]float64{}
	if s.embedder != nil {
		if evs, err := s.st.ListKbEmbeddings(); err == nil {
			for _, e := range evs {
				embeds[e.ChunkID] = e.Embedding
			}
		}
	}
	bmScores := s.idx.Score(q.Text)
	var qVec []float64
	if s.embedder != nil && q.Text != "" {
		if vs, err := s.embedder.Embed(ctx, []string{q.Text}); err == nil && len(vs) > 0 {
			qVec = vs[0]
		}
	}
	now := time.Now()
	var cands []cand
	for _, kc := range kcs {
		if isManual {
			if kc.Category != model.KBManual && kc.Category != model.KBRunbook {
				continue
			}
		} else if kc.Category != cat {
			continue
		}
		c := cand{Chunk: kc}
		c.BM25 = bmScores[kc.ChunkID]
		if v, ok := embeds[kc.ChunkID]; ok && qVec != nil {
			c.Vec = Cosine(v, qVec)
		}
		if isManual {
			c.Signal = FieldScore(kc, q)
		} else {
			c.Signal = LabelScore(kc, q)
			c.Recency = RecencyScore(kc.CreatedAt, now)
		}
		cands = append(cands, c)
	}
	if isManual {
		cands = RankManuals(cands, now)
	} else {
		cands = RankCases(cands, now)
	}
	if len(cands) > topK {
		cands = cands[:topK]
	}
	res := make([]Result, 0, len(cands))
	for _, c := range cands {
		res = append(res, Result{
			DocID: c.Chunk.DocID, Title: c.Chunk.Title, Category: c.Chunk.Category,
			Heading: c.Chunk.Heading, Content: c.Chunk.Content, Score: c.Final,
		})
	}
	return res, nil
}

// Router 依据阶段/意图返回案例与手册的 topK 配额 (casesTopK, manualsTopK)。
func Router(stage string, intents []string, leaves []model.LeafSpec) (int, int) {
	switch stage {
	case "rca":
		return 2, 1
	case "recommend":
		return 1, 2
	}
	if hasIntent(intents, "AskRootCause") {
		return 3, 0
	}
	if hasIntent(intents, "AskRecommendation") {
		return 0, 3
	}
	cases, manuals := 0, 0
	for _, code := range intents {
		if code == "" || code == "Unknown" {
			continue
		}
		if opIntent(code, leaves) {
			manuals++
		} else {
			cases++
		}
	}
	if cases == 0 && manuals == 0 {
		return 2, 1
	}
	if cases > 0 && manuals > 0 {
		if cases > 3 {
			cases = 3
		}
		if manuals > 3 {
			manuals = 3
		}
		return cases, manuals
	}
	if manuals > 0 {
		return 0, 3 // 纯操作/处置意图：全给手册
	}
	return 3, 0 // 纯查询意图：全给案例
}

func hasIntent(intents []string, code string) bool {
	for _, i := range intents {
		if i == code {
			return true
		}
	}
	return false
}

// opIntent 叶子规则关键词含操作类词视为操作/处置意图（走手册）。
func opIntent(code string, leaves []model.LeafSpec) bool {
	for _, l := range leaves {
		if l.Code != code {
			continue
		}
		for _, kw := range l.RuleKeywords {
			for _, w := range []string{"处置", "修复", "操作", "步骤", "命令", "更换", "升级"} {
				if strings.Contains(kw, w) {
					return true
				}
			}
		}
		return false
	}
	return false
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/retrieve/ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/retrieve/searcher.go backend/internal/retrieve/searcher_test.go
git commit -m "feat: retrieve新增Searcher混合检索编排与Router配额"
```

---

### Task 8: kb 服务层维护 kb_embeddings

**Files:**
- Modify: `backend/internal/kb/service.go`
- Test: `backend/internal/kb/service_test.go`

**Interfaces:**
- Consumes: `retrieve.Embedder`/`ErrNoEmbed`（Task 3）、`store.KBStore` 新方法（Task 1）
- Produces:
  - `Service` 新增字段 `embed retrieve.Embedder`
  - `func NewServiceWithEmbedder(st *store.Store, leaves func() []model.LeafSpec, embed retrieve.Embedder) *Service`；`NewService` 委托 `NewServiceWithEmbedder(..., nil)`
  - 内部 `func (s *Service) embedChunks(ctx context.Context, docID string, chunks []ChunkResult, ids []string, now time.Time) []model.KbEmbedding`（失败返回 nil，不阻断入库）

- [ ] **Step 1: 写失败测试**

`service_test.go` 追加（保留 `hasStr`/`storeMust` 辅助）：

```go
type fakeEmbed struct{}

func (fakeEmbed) Model() string { return "fake" }
func (fakeEmbed) Embed(_ context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i := range texts {
		out[i] = []float64{float64(i + 1), 1}
	}
	return out, nil
}

func TestUploadGeneratesEmbeddings(t *testing.T) {
	st := storeMust(t)
	svc := NewServiceWithEmbedder(st, nil, fakeEmbed{})
	doc, err := svc.Upload(context.Background(), UploadReq{
		SourceFile: "case.md", Data: []byte("GPU 掉卡排查正文"), Category: model.KBCase,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	emb, err := st.ListKbEmbeddings()
	if err != nil || len(emb) != 1 || len(emb[0].Embedding) != 2 {
		t.Fatalf("应生成1条向量: %v %+v", err, emb)
	}
	if emb[0].DocID != doc.ID || emb[0].Model != "fake" {
		t.Fatalf("向量元数据: %+v", emb[0])
	}
}

func TestUploadEmbedFailDoesNotBlock(t *testing.T) {
	st := storeMust(t)
	svc := NewServiceWithEmbedder(st, nil, errEmbed{})
	doc, err := svc.Upload(context.Background(), UploadReq{
		SourceFile: "case.md", Data: []byte("正文内容"), Category: model.KBCase,
	})
	if err != nil {
		t.Fatalf("embedding 失败不应阻断入库: %v", err)
	}
	emb, _ := st.ListKbEmbeddings()
	if len(emb) != 0 {
		t.Fatalf("embedding 失败应无向量: %+v", emb)
	}
	if doc.ID == "" {
		t.Fatalf("文档应已入库")
	}
}

type errEmbed struct{}

func (errEmbed) Model() string { return "fake" }
func (errEmbed) Embed(context.Context, []string) ([][]float64, error) {
	return nil, errors.New("boom")
}

func TestDeleteCascadesEmbeddings(t *testing.T) {
	st := storeMust(t)
	svc := NewServiceWithEmbedder(st, nil, fakeEmbed{})
	doc, _ := svc.Upload(context.Background(), UploadReq{SourceFile: "a.txt", Data: []byte("正文"), Category: model.KBCase})
	if err := svc.Delete(context.Background(), doc.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	emb, _ := st.ListKbEmbeddings()
	if len(emb) != 0 {
		t.Fatalf("删除后应无向量, got %+v", emb)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/kb/ -run 'TestUploadGeneratesEmbeddings|TestUploadEmbedFailDoesNotBlock|TestDeleteCascadesEmbeddings' -v`
Expected: FAIL——`undefined: NewServiceWithEmbedder`。

- [ ] **Step 3: 写实现**

`service.go` 修改 import（追加 `"aiops/internal/retrieve"`）与结构体/构造：

```go
type Service struct {
	st     *store.Store
	leaves func() []model.LeafSpec
	embed  retrieve.Embedder
}

func NewService(st *store.Store, leaves func() []model.LeafSpec) *Service {
	return NewServiceWithEmbedder(st, leaves, nil)
}

// NewServiceWithEmbedder 构造 Service；embed 可为 nil（不上向量）。
func NewServiceWithEmbedder(st *store.Store, leaves func() []model.LeafSpec, embed retrieve.Embedder) *Service {
	return &Service{st: st, leaves: leaves, embed: embed}
}
```

`Upload` 中 `insertDocAndChunks` 调用改为先生成 chunk id 与向量：

```go
	chunks := Chunk(content)
	ids := make([]string, len(chunks))
	for i := range chunks {
		ids[i] = model.NewID("kbchunk")
	}
	embs := s.embedChunks(ctx, doc.ID, chunks, ids, now)
	if err := s.insertDocAndChunks(doc, chunks, ids, embs, now); err != nil {
		return nil, err
	}
	return &doc, nil
```

`Seed` 中 `insertDocAndChunks` 调用同样改为（`ctx` 用 `context.Background()`）：

```go
		chunks := Chunk(d.Content)
		ids := make([]string, len(chunks))
		for i := range chunks {
			ids[i] = model.NewID("kbchunk")
		}
		embs := s.embedChunks(context.Background(), doc.ID, chunks, ids, now)
		if err := s.insertDocAndChunks(doc, chunks, ids, embs, now); err != nil {
			return err
		}
```

替换 `insertDocAndChunks` 并新增 `embedChunks`：

```go
func (s *Service) embedChunks(ctx context.Context, docID string, chunks []ChunkResult, ids []string, now time.Time) []model.KbEmbedding {
	if s.embed == nil {
		return nil
	}
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = truncateRunes(c.Content, 8000)
	}
	vecs, err := s.embed.Embed(ctx, texts)
	if err != nil {
		return nil
	}
	out := make([]model.KbEmbedding, 0, len(chunks))
	for i, v := range vecs {
		if len(v) == 0 {
			continue
		}
		out = append(out, model.KbEmbedding{ChunkID: ids[i], DocID: docID, Embedding: v, Model: s.embed.Model(), CreatedAt: now})
	}
	return out
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func (s *Service) insertDocAndChunks(doc model.KbDocument, chunks []ChunkResult, ids []string, embs []model.KbEmbedding, now time.Time) error {
	return s.st.WithKBTx(func(tx store.KBStore) error {
		if err := tx.InsertKbDocument(doc); err != nil {
			return err
		}
		for i, c := range chunks {
			if err := tx.InsertKbChunk(model.KbChunk{ID: ids[i], DocID: doc.ID, Seq: i, Heading: c.Heading, Content: c.Content, CreatedAt: now}); err != nil {
				return err
			}
		}
		for _, e := range embs {
			if err := tx.UpsertKbEmbedding(e); err != nil {
				return err
			}
		}
		return nil
	})
}
```

`Delete` 在删块后追加级联删向量：

```go
		if err := tx.DeleteKbChunks(id); err != nil {
			return err
		}
		if err := tx.DeleteKbEmbeddingsByDoc(id); err != nil {
			return err
		}
		return tx.DeleteKbDocument(id)
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/kb/ ./internal/store/ -v`
Expected: 全部 PASS，含既有上传/删除测试。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/kb/service.go backend/internal/kb/service_test.go
git commit -m "feat: kb上传生成向量与删除级联清理"
```

---

### Task 9: context 新增 search_case / search_manual 工具

**Files:**
- Modify: `backend/internal/context/tools.go`
- Test: `backend/internal/context/tools_test.go`

**Interfaces:**
- Consumes: `retrieve.Searcher`/`retrieve.Query`/`retrieve.Options`/`retrieve.Result`（Task 7）
- Produces:
  - `func NewSearchCaseTool(s retrieve.Searcher) Tool`（名称 `search_case`）
  - `func NewSearchManualTool(s retrieve.Searcher) Tool`（名称 `search_manual`）
  - 工具参数：`query`（必填，`strArg`）、`metric`、`device`、`category`（仅 manual 工具）、`top_k`（默认 3，`intArg`）

- [ ] **Step 1: 写失败测试**

`tools_test.go` 追加（需补充 import：`"aiops/internal/model"`、`"aiops/internal/retrieve"`）：

```go
type fakeSearcher struct{ res []retrieve.Result }

func (f fakeSearcher) SearchCases(_ context.Context, q retrieve.Query, o retrieve.Options) ([]retrieve.Result, error) {
	return f.res, nil
}
func (f fakeSearcher) SearchManuals(_ context.Context, q retrieve.Query, o retrieve.Options) ([]retrieve.Result, error) {
	return f.res, nil
}

func TestSearchCaseTool(t *testing.T) {
	svc := fakeSearcher{res: []retrieve.Result{{DocID: "d1", Title: "GPU掉卡案例", Category: model.KBCase, Content: "正文"}}}
	reg := NewRegistry()
	reg.Register(NewSearchCaseTool(svc))
	reg.Register(NewSearchManualTool(svc))
	if _, ok := reg.Lookup("search_case"); !ok {
		t.Fatalf("search_case missing")
	}
	if _, ok := reg.Lookup("search_manual"); !ok {
		t.Fatalf("search_manual missing")
	}
	res, err := reg.Execute(context.Background(), "search_case",
		map[string]any{"query": "GPU 掉卡", "metric": "gpu_util", "top_k": 5})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	outs, ok := res.([]retrieve.Result)
	if !ok || len(outs) != 1 || outs[0].DocID != "d1" {
		t.Fatalf("res = %#v", res)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/context/ -run 'TestSearchCaseTool' -v`
Expected: FAIL——`undefined: NewSearchCaseTool`。

- [ ] **Step 3: 写实现**

`tools.go` 追加 import `"aiops/internal/retrieve"` 与两个工厂（放在 `DefaultTools()` 之后）：

```go
// NewSearchCaseTool 检索相似历史运维案例（故障症状→根因→处置）。
func NewSearchCaseTool(s retrieve.Searcher) Tool {
	return Tool{
		Name: "search_case", Description: "检索相似历史运维案例（故障症状→根因→处置）",
		ParamSchema: map[string]string{"query": "问题或症状描述（必填）", "metric": "可选，指标名", "device": "可选，设备名", "top_k": "可选，返回条数（默认3）"},
		Execute: func(ctx context.Context, args map[string]any) (any, error) {
			if s == nil {
				return nil, errors.New("knowledge searcher unavailable")
			}
			return s.SearchCases(ctx, retrieve.Query{
				Text: strArg(args, "query"), Metric: strArg(args, "metric"), Device: strArg(args, "device"),
			}, retrieve.Options{TopK: intArg(args, "top_k", 3)})
		},
	}
}

// NewSearchManualTool 检索产品手册/操作手册（规格、告警含义、操作步骤）。
func NewSearchManualTool(s retrieve.Searcher) Tool {
	return Tool{
		Name: "search_manual", Description: "检索产品手册/操作手册（规格、告警含义、操作步骤）",
		ParamSchema: map[string]string{"query": "要查的问题（必填）", "category": "可选，manual/runbook", "metric": "可选，指标名", "top_k": "可选，返回条数（默认3）"},
		Execute: func(ctx context.Context, args map[string]any) (any, error) {
			if s == nil {
				return nil, errors.New("knowledge searcher unavailable")
			}
			return s.SearchManuals(ctx, retrieve.Query{
				Text: strArg(args, "query"), Metric: strArg(args, "metric"),
			}, retrieve.Options{TopK: intArg(args, "top_k", 3)})
		},
	}
}
```

`TestDefaultToolsRegistered` 期望 6 个工具不变（两个新工具独立注册，不在 DefaultTools 内）。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/context/ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/context/tools.go backend/internal/context/tools_test.go
git commit -m "feat: 新增search_case/search_manual知识检索工具"
```

---

### Task 10: context 组装器知识源改走 Searcher

**Files:**
- Modify: `backend/internal/context/assembler.go`
- Modify: `backend/internal/context/engine.go`
- Test: `backend/internal/context/assembler_test.go`（追加）

**Interfaces:**
- Consumes: `retrieve.Searcher`/`retrieve.Query`/`retrieve.Options`/`retrieve.Result`/`retrieve.Router`（Task 7）、`model.KbKnowledgeChunk`/`model.LeafSpec`（Task 1）
- Produces:
  - `Assembler` 新增字段 `search retrieve.Searcher`、`leaves func() []model.LeafSpec`
  - `func (a *Assembler) SetSearcher(s retrieve.Searcher)`
  - `func (a *Assembler) SetLeaves(f func() []model.LeafSpec)`
  - `func (e *Engine) SetSearcher(s retrieve.Searcher)`（委托给内部 asm）

- [ ] **Step 1: 写失败测试**

`assembler_test.go` 追加：

```go
type mockSearcher struct {
	cases   []retrieve.Result
	manuals []retrieve.Result
}

func (m mockSearcher) SearchCases(_ context.Context, q retrieve.Query, o retrieve.Options) ([]retrieve.Result, error) {
	out := m.cases
	if len(out) > o.TopK {
		out = out[:o.TopK]
	}
	return out, nil
}
func (m mockSearcher) SearchManuals(_ context.Context, q retrieve.Query, o retrieve.Options) ([]retrieve.Result, error) {
	out := m.manuals
	if len(out) > o.TopK {
		out = out[:o.TopK]
	}
	return out, nil
}

func TestAssembleRoutesKnowledgeByStage(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := NewStore(s)
	asm := NewAssembler(cs, 3)
	asm.SetSearcher(mockSearcher{
		cases:   []retrieve.Result{{DocID: "d1", Title: "GPU掉卡案例", Category: model.KBCase, Content: "掉卡根因与处置"}},
		manuals: []retrieve.Result{{DocID: "d2", Title: "交换机手册", Category: model.KBManual, Content: "光模块更换步骤"}},
	})
	// rca：案例2 + 手册1
	as, err := asm.Assemble(Query{Device: "db-01", Metric: "gpu_util", Stage: "rca"}, false, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !strings.Contains(as.User, "GPU掉卡案例") {
		t.Fatalf("rca 应含案例:\n%s", as.User)
	}
	// recommend：手册2 + 案例1
	as, err = asm.Assemble(Query{Device: "db-01", Metric: "gpu_util", Stage: "recommend"}, false, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !strings.Contains(as.User, "交换机手册") {
		t.Fatalf("recommend 应含手册:\n%s", as.User)
	}
}
```

（`assembler_test.go` 需补充 import：`"context"`、`"aiops/internal/retrieve"`。）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/context/ -run 'TestAssembleRoutesKnowledgeByStage' -v`
Expected: FAIL——`undefined: SetSearcher` 或组装不含知识。

- [ ] **Step 3: 写实现**

`assembler.go` 修改 import（追加 `"context"`、`"aiops/internal/retrieve"`）与结构体：

```go
type Assembler struct {
	cs     *Store
	sel    Selector
	topK   int
	search retrieve.Searcher
	leaves func() []model.LeafSpec
}

func (a *Assembler) SetSearcher(s retrieve.Searcher)      { a.search = s }
func (a *Assembler) SetLeaves(f func() []model.LeafSpec)  { a.leaves = f }
```

`candidates()` 中知识源替换为（`kcs, err := a.cs.ListKnowledgeChunks()` 之前插入分支；原 `ListKnowledgeChunks` 循环保留在 `else` 内）：

```go
	if a.search != nil {
		casesTopK, manualsTopK := retrieve.Router(q.Stage, q.Intents, a.leafList())
		if casesTopK > 0 {
			res, err := a.search.SearchCases(context.Background(), retrieve.Query{
				Text: qText(q), Metric: q.Metric, Device: q.Device, Intents: q.Intents,
			}, retrieve.Options{TopK: casesTopK})
			if err == nil {
				for _, r := range res {
					out = append(out, resultChunk(r))
				}
			}
		}
		if manualsTopK > 0 {
			res, err := a.search.SearchManuals(context.Background(), retrieve.Query{
				Text: qText(q), Metric: q.Metric, Device: q.Device, Intents: q.Intents,
			}, retrieve.Options{TopK: manualsTopK})
			if err == nil {
				for _, r := range res {
					out = append(out, resultChunk(r))
				}
			}
		}
	} else {
		kcs, err := a.cs.ListKnowledgeChunks()
		if err != nil {
			return nil, err
		}
		for _, kc := range kcs {
			out = append(out, Chunk{Source: SourceKnowledge,
				Content: knowledgeContent(kc),
				Metadata: map[string]string{
					"metrics":      strings.Join(kc.Metrics, ","),
					"device_types": strings.Join(kc.DeviceTypes, ","),
					"vendor":       strings.Join(kc.Vendor, ","),
					"model":        strings.Join(kc.Model, ","),
					"intents":      strings.Join(kc.Intents, ","),
					"doc_title":    kc.Title,
					"doc_id":       kc.DocID,
					"created_at":   kc.CreatedAt.Format(time.RFC3339),
				}})
		}
	}
```

`assembler.go` 追加辅助函数（文件末尾）：

```go
func (a *Assembler) leafList() []model.LeafSpec {
	if a.leaves != nil {
		if l := a.leaves(); len(l) > 0 {
			return l
		}
	}
	return nil
}

// qText 组装检索文本：设备 + 指标 + 关键词。
func qText(q Query) string {
	return strings.Join(append([]string{q.Device, q.Metric}, q.Keywords...), " ")
}

// resultChunk 将检索结果块转成组装用 Chunk（复用 knowledgeContent 回显格式）。
func resultChunk(r retrieve.Result) Chunk {
	kc := model.KbKnowledgeChunk{
		DocID: r.DocID, Title: r.Title, Category: r.Category,
		Heading: r.Heading, Content: r.Content,
	}
	return Chunk{
		Source:   SourceKnowledge,
		Content:  knowledgeContent(kc),
		Metadata: map[string]string{"doc_title": r.Title, "doc_id": r.DocID},
	}
}
```

`assembler.go` 追加 import（`"context"`、`"aiops/internal/model"`、`"aiops/internal/retrieve"`；若已引入则忽略）。

`engine.go` 追加（`engine.go` 属 `context` 包，import `"aiops/internal/retrieve"`）：

```go
// SetSearcher 注入混合检索器（供组装器知识源使用）。
func (e *Engine) SetSearcher(s retrieve.Searcher) {
	e.asm.SetSearcher(s)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/context/ -v`
Expected: 全部 PASS，含既有 `TestAssembleSections`/`TestAssembleKnowledgeByIntent`（未 SetSearcher 时走回退路径）。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/context/assembler.go backend/internal/context/engine.go backend/internal/context/assembler_test.go
git commit -m "feat: 组装器知识源改走Searcher混合检索并按阶段路由"
```

---

### Task 11: agent 注入 Searcher 并按意图路由

**Files:**
- Modify: `backend/internal/agent/agent.go`
- Test: `backend/internal/agent/agent_test.go`（追加）

**Interfaces:**
- Consumes: `retrieve.Searcher`（Task 7）、`ctxpkg.Assembler.SetSearcher`/`SetLeaves`（Task 10）
- Produces:
  - `Agent` 新增字段 `search retrieve.Searcher`
  - `func (a *Agent) SetSearcher(s retrieve.Searcher)`

- [ ] **Step 1: 写失败测试**

`agent_test.go` 追加（复用文件内既有 store/Agent 构造辅助；需补充 import：`"context"`、`"path/filepath"`、`"strings"`、`"aiops/internal/retrieve"`）：

```go
type agentFakeSearcher struct{ res []retrieve.Result }

func (f agentFakeSearcher) SearchCases(_ context.Context, q retrieve.Query, o retrieve.Options) ([]retrieve.Result, error) {
	return f.res, nil
}
func (f agentFakeSearcher) SearchManuals(_ context.Context, q retrieve.Query, o retrieve.Options) ([]retrieve.Result, error) {
	return f.res, nil
}

func TestAgentBuildAssembledUsesSearcher(t *testing.T) {
	st, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer st.Close()
	cs := ctxpkg.NewStore(st)
	ag := NewWithLeaves(st, nil, nil, nil, cs, 3, 3, nil)
	ag.SetSearcher(agentFakeSearcher{
		res: []retrieve.Result{{DocID: "d1", Title: "磁盘案例", Category: model.KBCase, Content: "磁盘扩容处置"}},
	})
	as := ag.buildAssembled("c1", "db-01", "disk_usage", []string{"AskRootCause"})
	if as == nil || !strings.Contains(as.User, "磁盘案例") {
		t.Fatalf("buildAssembled 应含案例知识:\n%v", as)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent/ -run 'TestAgentBuildAssembledUsesSearcher' -v`
Expected: FAIL——`undefined: SetSearcher`。

- [ ] **Step 3: 写实现**

`agent.go` 修改 import（追加 `"aiops/internal/retrieve"`）与结构体：

```go
type Agent struct {
	store    *store.Store
	llm      ctxpkg.Completer
	lightLLM ctxpkg.Completer
	maxSplit int
	eng      *pipeline.Engine
	ctxStore *ctxpkg.Store
	topK     int
	leaves   func() []model.LeafSpec
	search   retrieve.Searcher
}

// SetSearcher 注入混合检索器（供组装器知识源使用）。
func (a *Agent) SetSearcher(s retrieve.Searcher) { a.search = s }
```

`buildAssembled` 与 `kbReply` 中 `ctxpkg.NewAssembler(...)` 后追加：

```go
	asm := ctxpkg.NewAssembler(a.ctxStore, a.topK)
	asm.SetSearcher(a.search)
	asm.SetLeaves(a.leaves)
```

（`buildAssembled` 与 `kbReply` 两处均如此修改。）

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agent/ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/agent/agent.go backend/internal/agent/agent_test.go
git commit -m "feat: agent注入Searcher并按意图检索知识库"
```

---

### Task 12: main 装配与全量验证

**Files:**
- Modify: `backend/cmd/server/main.go`
- Modify: `backend/internal/pipeline/engine.go`
- Test: 无新增（回归验证）

**Interfaces:**
- Consumes: `retrieve.NewLLMEmbedder`（Task 3）、`retrieve.NewSearcher`（Task 7）、`kb.NewServiceWithEmbedder`（Task 8）、`ctxpkg.NewSearchCaseTool`/`NewSearchManualTool`（Task 9）、`ctxpkg.Engine.SetSearcher`（Task 10）

- [ ] **Step 1: pipeline.Engine 透传 Searcher**

`pipeline/engine.go` 修改 import（追加 `"aiops/internal/retrieve"`）与结构体（在 `topK` 后加字段）：

```go
type Engine struct {
	store    *store.Store
	stages   []Stage
	Publish  func(eventType string, data any)
	coord    *coordinator.Coordinator
	ctxStore *ctxpkg.Store
	tools    *ctxpkg.Registry
	react    bool
	topK     int
	searcher retrieve.Searcher
}

// SetSearcher 注入混合检索器（透传给每个 run 的上下文引擎）。
func (e *Engine) SetSearcher(s retrieve.Searcher) { e.searcher = s }
```

`Run()` 中 `st.Ctx = ctxpkg.NewEngine(...)` 之后追加：

```go
	if e.searcher != nil {
		st.Ctx.SetSearcher(e.searcher)
	}
```

- [ ] **Step 2: 写装配代码**

`main.go` 在 `intentsvc`/`kbsvc` 构造处修改：

```go
	embedder, _ := retrieve.NewLLMEmbedder(cfg) // 无 key 时 nil，检索回退
	searcher := retrieve.NewSearcher(st, embedder)

	eng := pipeline.BuildWithContext(st, lc, coordinator.New(), cs, reg, cfg.React, cfg.TopK)
	eng.Publish = h.Publish
	eng.SetSearcher(searcher)
	reg.Register(ctxpkg.NewSearchCaseTool(searcher))
	reg.Register(ctxpkg.NewSearchManualTool(searcher))
```

`kbsvc` 构造改为：

```go
	kbsvc := kb.NewServiceWithEmbedder(st, func() []model.LeafSpec {
		leaves, _ := intentsvc.ActiveLeaves(context.Background())
		return leaves
	}, embedder)
```

`ag` 构造后追加：

```go
	ag := agent.NewWithLeaves(st, lc, light, eng, cs, cfg.TopK, cfg.MaxSplit, func() []model.LeafSpec {
		leaves, _ := intentsvc.ActiveLeaves(context.Background())
		return leaves
	})
	ag.SetSearcher(searcher)
```

`main.go` 追加 import `"aiops/internal/retrieve"`。

- [ ] **Step 3: 构建验证**

Run: `go build ./...`
Expected: 编译通过。

- [ ] **Step 4: 全量测试**

Run: `go test ./...`
Expected: 全部 PASS（含无 key 回退路径的既有测试）。

- [ ] **Step 5: 提交**

```bash
git add backend/cmd/server/main.go backend/internal/pipeline/engine.go
git commit -m "feat: main装配embedder/searcher与两个知识检索工具"
```

---

## Self-Review 记录

- **Spec 覆盖**：向量通道（§5）→ Task 3/5；BM25（§6）→ Task 4；差异化融合（§7）→ Task 6/7；kb_embeddings 表（§4）→ Task 1；配置（§10）→ Task 2；两个工具（§8）→ Task 9；被动注入增强（§9）→ Task 10/11；main 装配（§12）→ Task 12。
- **依赖环**：retrieve 不 import context（Task 3-7 仅依赖 store/model/config）；context import retrieve（Task 9/10）；kb import retrieve（Task 8）；agent import retrieve（Task 11）；无环。
- **回退**：无 key 时 embedder nil → 向量通道为 0（searcher.search），组装器 Searcher 未注入时走原 Selector 路径（Task 10 else 分支），既有测试保持绿。