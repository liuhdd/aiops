# 知识库（Knowledge Base）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增硬件运维知识库：上传 txt/md/docx 文档，解析→自动打标→分块入库；管理页支持按分类/设备类型/厂商/型号/意图/关键词筛选；RAG 检索改为从数据库读知识（取代 `mockdata.Docs()`），并新增"意图命中"加权信号，实现"提问→意图→对应文档库"检索闭环。

**Architecture:** 新增 `internal/kb` 包（parser/chunk/tagger/service 四文件）负责导入与 CRUD；`store` 新增 `kb_documents`/`kb_chunks` 两表与 `KBStore` 事务接口；`context` 的 Selector 增加意图加权、Assembler 知识源改读 DB；`pipeline`/`agent` 注入意图 code 到 Query；`api` 增加 `/api/kb/*` 路由；前端新增知识库管理页。

**Tech Stack:** Go 1.24 + SQLite（`modernc.org/sqlite`，driver 名 `"sqlite"`）+ React 18/Vite/TS + Vitest（jsdom）。docx 解析用标准库 `archive/zip` + `encoding/xml`，不引入第三方依赖。注释与 commit message 用中文。

## Global Constraints

- Go 模块 `aiops`，包路径 `aiops/internal/...`。SQLite 单写：`store.New` 已设 `SetMaxOpenConns(1)`，测试用 `filepath.Join(t.TempDir(), "*.db")`，用完 `s.Close()`。
- `store.New` 的 `migrate()` 幂等建表（`IF NOT EXISTS`）。种子导入由 `kb.Service.Seed` 在表空时执行（main 启动调用）。
- 标签/意图字段均为 JSON 数组字符串列，空数组存 `[]`；用 `model.MustJSON` 序列化、`json.Unmarshal` 反序列化。
- 提交信息用 conventional prefixes（`feat:`/`test:`/`docs:`/`chore:`）。
- 测试命令：后端 `go test ./...`；前端 `npx vitest run`；构建 `go build ./...`、`npm run build`。
- 现有 `TestRuleRegression*` 与 `mockdata` 相关测试必须保持通过（`mockdata.Docs()` 仅保留供种子导入，检索路径不再引用）。

---

### Task 1: model 新增知识库类型

**Files:**
- Create: `backend/internal/model/kb.go`
- Test: `backend/internal/model/kb_test.go`

**Interfaces:**
- Produces:
  - `type KBCategory string`；常量 `KBCase="case"` / `KBManual="manual"` / `KBRunbook="runbook"` / `KBOther="other"`
  - `func IsKBCategory(c KBCategory) bool`
  - `type KbDocument struct{ ID,Title string; Category KBCategory; DeviceTypes,Vendor,Model,Metrics,Keywords,Intents []string; SourceFile,Content string; CreatedAt,UpdatedAt time.Time }`（JSON tag 用 snake_case）
  - `type KbChunk struct{ ID,DocID string; Seq int; Heading,Content string; CreatedAt time.Time }`
  - `type KbDocSummary struct{ KbDocument; ChunkCount int \`json:"chunk_count"\` }`
  - `type KbKnowledgeChunk struct{ DocID,Title string; Category KBCategory; DeviceTypes,Vendor,Model,Metrics,Keywords,Intents []string; Heading,Content string; CreatedAt time.Time }`（RAG 联查视图，无 JSON tag）
- Consumes: 无

- [ ] **Step 1: 写失败测试（新建 `kb_test.go`）**

```go
package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestKBCategory(t *testing.T) {
	if !IsKBCategory(KBCase) || !IsKBCategory(KBManual) || !IsKBCategory(KBRunbook) || !IsKBCategory(KBOther) {
		t.Fatal("内置分类应合法")
	}
	if IsKBCategory("bogus") {
		t.Fatal("非法分类不应合法")
	}
}

func TestKbDocumentJSON(t *testing.T) {
	d := KbDocument{ID: "d1", Title: "交换机手册", Category: KBManual,
		DeviceTypes: []string{"switch"}, Vendor: []string{"H3C"}, Model: []string{"S5560"},
		Metrics: []string{"net_in"}, Keywords: []string{"光模块"}, Intents: []string{"AskRootCause"},
		SourceFile: "s5560.md", Content: "正文"}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out KbDocument
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Category != KBManual || len(out.Vendor) != 1 || out.Vendor[0] != "H3C" {
		t.Fatalf("roundtrip = %+v", out)
	}
}

func TestKbDocSummary(t *testing.T) {
	s := KbDocSummary{KbDocument: KbDocument{ID: "d1", Title: "t"}, ChunkCount: 3}
	b, _ := json.Marshal(s)
	if !strings.Contains(string(b), `"chunk_count":3`) {
		t.Fatalf("summary json = %s", b)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/model/ -run 'TestKBCategory|TestKbDocumentJSON|TestKbDocSummary' -v`
Expected: FAIL——`undefined: KBCategory`。

- [ ] **Step 3: 写实现（新建 `kb.go`）**

```go
package model

import "time"

type KBCategory string

const (
	KBCase    KBCategory = "case"
	KBManual  KBCategory = "manual"
	KBRunbook KBCategory = "runbook"
	KBOther   KBCategory = "other"
)

func IsKBCategory(c KBCategory) bool {
	switch c {
	case KBCase, KBManual, KBRunbook, KBOther:
		return true
	}
	return false
}

type KbDocument struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Category    KBCategory `json:"category"`
	DeviceTypes []string   `json:"device_types"`
	Vendor      []string   `json:"vendor"`
	Model       []string   `json:"model"`
	Metrics     []string   `json:"metrics"`
	Keywords    []string   `json:"keywords"`
	Intents     []string   `json:"intents"`
	SourceFile  string     `json:"source_file"`
	Content     string     `json:"content"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type KbChunk struct {
	ID        string    `json:"id"`
	DocID     string    `json:"doc_id"`
	Seq       int       `json:"seq"`
	Heading   string    `json:"heading"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// KbDocSummary 列表视图：文档 + 块数。
type KbDocSummary struct {
	KbDocument
	ChunkCount int `json:"chunk_count"`
}

// KbKnowledgeChunk 检索视图：块 + 所属文档元数据（RAG 联查返回）。
type KbKnowledgeChunk struct {
	DocID       string
	Title       string
	Category    KBCategory
	DeviceTypes []string
	Vendor      []string
	Model       []string
	Metrics     []string
	Keywords    []string
	Intents     []string
	Heading     string
	Content     string
	CreatedAt   time.Time
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/model/ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/model/kb.go backend/internal/model/kb_test.go
git commit -m "feat: model新增知识库文档/块/分类类型"
```

---

### Task 2: store 知识库表 + CRUD + 检索联查

**Files:**
- Modify: `backend/internal/store/store.go`（`migrate()` 追加建表）
- Create: `backend/internal/store/kb.go`
- Test: `backend/internal/store/kb_test.go`

**Interfaces:**
- Consumes: `model.KbDocument`/`model.KbChunk`/`model.KbDocSummary`/`model.KbKnowledgeChunk`（Task 1）
- Produces:
  - `type KbFilter struct{ Category, DeviceType, Vendor, Model, Intent, Q string }`
  - `type KBStore interface{ InsertKbDocument(d model.KbDocument) error; InsertKbChunk(c model.KbChunk) error; GetKbDocument(id string) (model.KbDocument, error); ListKbChunksByDoc(docID string) ([]model.KbChunk, error); DeleteKbChunks(docID string) error; DeleteKbDocument(id string) error }`
  - `func (s *Store) ListKbDocuments(f KbFilter) ([]model.KbDocSummary, error)`
  - `func (s *Store) GetKbDocument(id string) (model.KbDocument, error)`
  - `func (s *Store) ListKbChunksByDoc(docID string) ([]model.KbChunk, error)`
  - `func (s *Store) ListKnowledgeChunks() ([]model.KbKnowledgeChunk, error)`
  - `func (s *Store) CountKbDocuments() (int, error)`
  - `func (s *Store) WithKBTx(fn func(tx KBStore) error) error`
- 依赖：`dbExec` 接口（`store/intent.go` 已定义，复用）

- [ ] **Step 1: 写失败测试（新建 `kb_test.go`）**

```go
package store

import (
	"strings"
	"testing"
	"time"

	"aiops/internal/model"
)

func mkDoc(id, title string, category model.KBCategory) model.KbDocument {
	return model.KbDocument{ID: id, Title: title, Category: category,
		DeviceTypes: []string{"switch"}, Vendor: []string{"H3C"}, Model: []string{"S5560"},
		Metrics: []string{"net_in"}, Keywords: []string{"光模块"}, Intents: []string{"AskRootCause"},
		SourceFile: "s5560.md", Content: "交换机链路排查正文", CreatedAt: time.Now(), UpdatedAt: time.Now()}
}

func TestKbDocRoundTrip(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	d := mkDoc("d1", "交换机手册", model.KBManual)
	d.CreatedAt, d.UpdatedAt = now, now
	if err := s.WithKBTx(func(tx KBStore) error {
		if err := tx.InsertKbDocument(d); err != nil {
			return err
		}
		return tx.InsertKbChunk(model.KbChunk{ID: "c1", DocID: "d1", Seq: 0, Heading: "", Content: "正文", CreatedAt: now})
	}); err != nil {
		t.Fatalf("tx insert: %v", err)
	}
	got, err := s.GetKbDocument("d1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "交换机手册" || len(got.Vendor) != 1 || got.Vendor[0] != "H3C" || len(got.Intents) != 1 {
		t.Fatalf("roundtrip = %+v", got)
	}
	chunks, err := s.ListKbChunksByDoc("d1")
	if err != nil || len(chunks) != 1 || chunks[0].Seq != 0 {
		t.Fatalf("chunks: %v %+v", err, chunks)
	}
}

func TestKbListFilters(t *testing.T) {
	s := newTestStore(t)
	_ = s.WithKBTx(func(tx KBStore) error {
		_ = tx.InsertKbDocument(mkDoc("d1", "交换机手册", model.KBManual))
		_ = tx.InsertKbDocument(model.KbDocument{ID: "d2", Title: "GPU故障案例", Category: model.KBCase,
			DeviceTypes: []string{"gpu-server"}, Vendor: []string{"NVIDIA"}, Model: []string{"H800"},
			Metrics: []string{"gpu_util"}, Intents: []string{"AskRootCause"}, Content: "GPU掉卡排查", CreatedAt: time.Now(), UpdatedAt: time.Now()})
		return nil
	})
	if list, err := s.ListKbDocuments(KbFilter{Category: string(model.KBManual)}); err != nil || len(list) != 1 || list[0].ID != "d1" {
		t.Fatalf("by category: %v %+v", err, list)
	}
	if list, err := s.ListKbDocuments(KbFilter{DeviceType: "gpu-server"}); err != nil || len(list) != 1 || list[0].ID != "d2" {
		t.Fatalf("by device_type: %v %+v", err, list)
	}
	if list, err := s.ListKbDocuments(KbFilter{Vendor: "H3C"}); err != nil || len(list) != 1 || list[0].ID != "d1" {
		t.Fatalf("by vendor: %v %+v", err, list)
	}
	if list, err := s.ListKbDocuments(KbFilter{Model: "H800"}); err != nil || len(list) != 1 || list[0].ID != "d2" {
		t.Fatalf("by model: %v %+v", err, list)
	}
	if list, err := s.ListKbDocuments(KbFilter{Intent: "AskRootCause"}); err != nil || len(list) != 2 {
		t.Fatalf("by intent: %v %+v", err, list)
	}
	if list, err := s.ListKbDocuments(KbFilter{Q: "掉卡"}); err != nil || len(list) != 1 || list[0].ID != "d2" {
		t.Fatalf("by q: %v %+v", err, list)
	}
	if list, err := s.ListKbDocuments(KbFilter{Category: string(model.KBCase), Vendor: "H3C"}); err != nil || len(list) != 0 {
		t.Fatalf("组合应无命中: %v %+v", err, list)
	}
	if list, err := s.ListKbDocuments(KbFilter{}); err != nil || len(list) != 2 {
		t.Fatalf("无筛选应全量: %v len=%d", err, len(list))
	}
}

func TestKbDeleteCascadesChunks(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	_ = s.WithKBTx(func(tx KBStore) error {
		_ = tx.InsertKbDocument(mkDoc("d1", "t", model.KBOther))
		return tx.InsertKbChunk(model.KbChunk{ID: "c1", DocID: "d1", Seq: 0, Content: "x", CreatedAt: now})
	})
	if err := s.WithKBTx(func(tx KBStore) error {
		if err := tx.DeleteKbChunks("d1"); err != nil {
			return err
		}
		return tx.DeleteKbDocument("d1")
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetKbDocument("d1"); err == nil || !strings.Contains(err.Error(), "no rows") {
		t.Fatalf("删除后应无文档, got %v", err)
	}
	chunks, _ := s.ListKbChunksByDoc("d1")
	if len(chunks) != 0 {
		t.Fatalf("块应级联删除, got %+v", chunks)
	}
}

func TestKbKnowledgeChunksJoin(t *testing.T) {
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
	if kcs[0].Title != "交换机手册" || kcs[0].Category != model.KBManual || kcs[0].Heading != "排查步骤" || len(kcs[0].Vendor) != 1 {
		t.Fatalf("join = %+v", kcs[0])
	}
}

func TestKbCount(t *testing.T) {
	s := newTestStore(t)
	_ = s.WithKBTx(func(tx KBStore) error {
		_ = tx.InsertKbDocument(mkDoc("d1", "t", model.KBOther))
		return tx.InsertKbDocument(mkDoc("d2", "t2", model.KBOther))
	})
	n, err := s.CountKbDocuments()
	if err != nil || n != 2 {
		t.Fatalf("Count = %d %v", n, err)
	}
}

func TestKbTablesCreated(t *testing.T) {
	s := newTestStore(t)
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('kb_documents','kb_chunks')`).Scan(&n)
	if err != nil || n != 2 {
		t.Fatalf("kb tables = %d %v, want 2", n, err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/store/ -run 'TestKbDocRoundTrip|TestKbListFilters|TestKbDeleteCascadesChunks|TestKbKnowledgeChunksJoin|TestKbCount|TestKbTablesCreated' -v`
Expected: FAIL——`undefined: KBStore`/`WithKBTx` 等。

- [ ] **Step 3: 写实现（修改 `store.go` + 新建 `kb.go`）**

`store.go` 的 `migrate()` 在 `intent_audit` 建表后追加：

```go
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS kb_documents (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		category TEXT NOT NULL,
		device_types TEXT NOT NULL DEFAULT '[]',
		vendor TEXT NOT NULL DEFAULT '[]',
		model TEXT NOT NULL DEFAULT '[]',
		metrics TEXT NOT NULL DEFAULT '[]',
		keywords TEXT NOT NULL DEFAULT '[]',
		intents TEXT NOT NULL DEFAULT '[]',
		source_file TEXT NOT NULL DEFAULT '',
		content TEXT NOT NULL,
		created_at DATETIME,
		updated_at DATETIME)`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_kb_docs_category ON kb_documents(category)`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS kb_chunks (
		id TEXT PRIMARY KEY,
		doc_id TEXT NOT NULL,
		seq INTEGER NOT NULL,
		heading TEXT NOT NULL DEFAULT '',
		content TEXT NOT NULL,
		created_at DATETIME)`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_kb_chunks_doc ON kb_chunks(doc_id, seq)`); err != nil {
		return err
	}
```

新建 `kb.go`：

```go
package store

import (
	"encoding/json"
	"strings"

	"aiops/internal/model"
)

// KbFilter 知识库列表筛选（空值忽略）。
type KbFilter struct {
	Category   string
	DeviceType string
	Vendor     string
	Model      string
	Intent     string
	Q          string
}

// KBStore 知识库读写接口（*Store 与事务内 repo 均实现）。
type KBStore interface {
	InsertKbDocument(d model.KbDocument) error
	InsertKbChunk(c model.KbChunk) error
	GetKbDocument(id string) (model.KbDocument, error)
	ListKbChunksByDoc(docID string) ([]model.KbChunk, error)
	DeleteKbChunks(docID string) error
	DeleteKbDocument(id string) error
}

type kbRepo struct{ db dbExec }

const kbDocCols = `id, title, category, device_types, vendor, model, metrics, keywords, intents, source_file, content, created_at, updated_at`

func parseArr(s string) []string {
	var out []string
	if s != "" {
		_ = json.Unmarshal([]byte(s), &out)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func scanKbDoc(scan func(dest ...any) error) (model.KbDocument, error) {
	var d model.KbDocument
	var dt, vd, md, mtr, kw, in string
	if err := scan(&d.ID, &d.Title, &d.Category, &dt, &vd, &md, &mtr, &kw, &in, &d.SourceFile, &d.Content, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return d, err
	}
	d.DeviceTypes = parseArr(dt)
	d.Vendor = parseArr(vd)
	d.Model = parseArr(md)
	d.Metrics = parseArr(mtr)
	d.Keywords = parseArr(kw)
	d.Intents = parseArr(in)
	return d, nil
}

func (r *kbRepo) InsertKbDocument(d model.KbDocument) error {
	_, err := r.db.Exec(`INSERT INTO kb_documents (id, title, category, device_types, vendor, model, metrics, keywords, intents, source_file, content, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			title=excluded.title, category=excluded.category, device_types=excluded.device_types,
			vendor=excluded.vendor, model=excluded.model, metrics=excluded.metrics,
			keywords=excluded.keywords, intents=excluded.intents, source_file=excluded.source_file,
			content=excluded.content, updated_at=excluded.updated_at`,
		d.ID, d.Title, string(d.Category), model.MustJSON(d.DeviceTypes), model.MustJSON(d.Vendor),
		model.MustJSON(d.Model), model.MustJSON(d.Metrics), model.MustJSON(d.Keywords),
		model.MustJSON(d.Intents), d.SourceFile, d.Content, d.CreatedAt, d.UpdatedAt)
	return err
}

func (r *kbRepo) InsertKbChunk(c model.KbChunk) error {
	_, err := r.db.Exec(`INSERT INTO kb_chunks (id, doc_id, seq, heading, content, created_at) VALUES (?,?,?,?,?,?)`,
		c.ID, c.DocID, c.Seq, c.Heading, c.Content, c.CreatedAt)
	return err
}

func (r *kbRepo) GetKbDocument(id string) (model.KbDocument, error) {
	return scanKbDoc(r.db.QueryRow(`SELECT `+kbDocCols+` FROM kb_documents WHERE id = ?`, id).Scan)
}

func (r *kbRepo) ListKbChunksByDoc(docID string) ([]model.KbChunk, error) {
	rows, err := r.db.Query(`SELECT id, doc_id, seq, heading, content, created_at FROM kb_chunks WHERE doc_id = ? ORDER BY seq ASC`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.KbChunk
	for rows.Next() {
		var c model.KbChunk
		if err := rows.Scan(&c.ID, &c.DocID, &c.Seq, &c.Heading, &c.Content, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *kbRepo) DeleteKbChunks(docID string) error {
	_, err := r.db.Exec(`DELETE FROM kb_chunks WHERE doc_id = ?`, docID)
	return err
}

func (r *kbRepo) DeleteKbDocument(id string) error {
	_, err := r.db.Exec(`DELETE FROM kb_documents WHERE id = ?`, id)
	return err
}

// ---- *Store 委托与事务 ----

func (s *Store) ListKbDocuments(f KbFilter) ([]model.KbDocSummary, error) {
	var conds []string
	var args []any
	if f.Category != "" {
		conds = append(conds, "d.category = ?")
		args = append(args, f.Category)
	}
	if f.DeviceType != "" {
		conds = append(conds, `d.device_types LIKE '%'||?||'%'`)
		args = append(args, `"`+f.DeviceType+`"`)
	}
	if f.Vendor != "" {
		conds = append(conds, `d.vendor LIKE '%'||?||'%'`)
		args = append(args, `"`+f.Vendor+`"`)
	}
	if f.Model != "" {
		conds = append(conds, `d.model LIKE '%'||?||'%'`)
		args = append(args, `"`+f.Model+`"`)
	}
	if f.Intent != "" {
		conds = append(conds, `d.intents LIKE '%'||?||'%'`)
		args = append(args, `"`+f.Intent+`"`)
	}
	if f.Q != "" {
		conds = append(conds, `(d.title LIKE '%'||?||'%' OR d.content LIKE '%'||?||'%')`)
		args = append(args, f.Q, f.Q)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	query := `SELECT d.id, d.title, d.category, d.device_types, d.vendor, d.model, d.metrics, d.keywords, d.intents, d.source_file, d.content, d.created_at, d.updated_at,
		(SELECT count(*) FROM kb_chunks c WHERE c.doc_id = d.id) AS chunk_count
		FROM kb_documents d` + where + ` ORDER BY d.updated_at DESC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.KbDocSummary
	for rows.Next() {
		// 注意：rows.Scan 每行只能调用一次，chunk_count 须与文档列一并扫描。
		var s model.KbDocSummary
		var dt, vd, md, mtr, kw, in string
		if err := rows.Scan(&s.ID, &s.Title, &s.Category, &dt, &vd, &md, &mtr, &kw, &in,
			&s.SourceFile, &s.Content, &s.CreatedAt, &s.UpdatedAt, &s.ChunkCount); err != nil {
			return nil, err
		}
		s.DeviceTypes = parseArr(dt)
		s.Vendor = parseArr(vd)
		s.Model = parseArr(md)
		s.Metrics = parseArr(mtr)
		s.Keywords = parseArr(kw)
		s.Intents = parseArr(in)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (s *Store) GetKbDocument(id string) (model.KbDocument, error) {
	return (&kbRepo{db: s.db}).GetKbDocument(id)
}

func (s *Store) ListKbChunksByDoc(docID string) ([]model.KbChunk, error) {
	return (&kbRepo{db: s.db}).ListKbChunksByDoc(docID)
}

func (s *Store) ListKnowledgeChunks() ([]model.KbKnowledgeChunk, error) {
	rows, err := s.db.Query(`SELECT c.doc_id, d.title, d.category, d.device_types, d.vendor, d.model, d.metrics, d.keywords, d.intents, c.heading, c.content, d.created_at
		FROM kb_chunks c JOIN kb_documents d ON d.id = c.doc_id
		ORDER BY d.created_at DESC, c.seq ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.KbKnowledgeChunk
	for rows.Next() {
		var k model.KbKnowledgeChunk
		var dt, vd, md, mtr, kw, in string
		if err := rows.Scan(&k.DocID, &k.Title, &k.Category, &dt, &vd, &md, &mtr, &kw, &in, &k.Heading, &k.Content, &k.CreatedAt); err != nil {
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

func (s *Store) CountKbDocuments() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM kb_documents`).Scan(&n)
	return n, err
}

// WithKBTx 在单事务内执行知识库写操作；fn 返回错误则回滚。
func (s *Store) WithKBTx(fn func(tx KBStore) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(&kbRepo{db: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/store/ -v`
Expected: 全部 PASS，含既有测试。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/store/store.go backend/internal/store/kb.go backend/internal/store/kb_test.go
git commit -m "feat: store新增知识库两表/CRUD/级联删除/RAG联查"
```

---

### Task 3: kb 文件解析（txt/md/docx）

**Files:**
- Create: `backend/internal/kb/parser.go`
- Test: `backend/internal/kb/parser_test.go`

**Interfaces:**
- Produces:
  - `var ErrBadFile = errors.New("不支持的文件类型")`
  - `var ErrEmptyContent = errors.New("文档内容为空")`
  - `func ParseDoc(filename string, data []byte) (string, error)`
- Consumes: 无

- [ ] **Step 1: 写失败测试（新建 `parser_test.go`）**

```go
package kb

import (
	"archive/zip"
	"bytes"
	"errors"
	"testing"
)

func TestParseTxtAndMd(t *testing.T) {
	if got, err := ParseDoc("a.txt", []byte("hello")); err != nil || got != "hello" {
		t.Fatalf("txt = %q %v", got, err)
	}
	if got, err := ParseDoc("b.md", []byte("# 标题\n正文")); err != nil || got != "# 标题\n正文" {
		t.Fatalf("md = %q %v", got, err)
	}
}

func TestParseDocx(t *testing.T) {
	data := makeDocx(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>第一段</w:t></w:r></w:p><w:p><w:r><w:t>第二段</w:t></w:r></w:p></w:body></w:document>`)
	got, err := ParseDoc("doc.docx", data)
	if err != nil {
		t.Fatalf("docx parse: %v", err)
	}
	if !bytes.Contains([]byte(got), []byte("第一段")) || !bytes.Contains([]byte(got), []byte("第二段")) {
		t.Fatalf("docx text = %q", got)
	}
}

func TestParseBadFile(t *testing.T) {
	if _, err := ParseDoc("a.pdf", []byte("x")); !errors.Is(err, ErrBadFile) {
		t.Fatalf("pdf 应 ErrBadFile, got %v", err)
	}
	if _, err := ParseDoc("a.exe", nil); !errors.Is(err, ErrBadFile) {
		t.Fatalf("exe 应 ErrBadFile, got %v", err)
	}
	if _, err := ParseDoc("noext", []byte("x")); !errors.Is(err, ErrBadFile) {
		t.Fatalf("无扩展名应 ErrBadFile, got %v", err)
	}
}

func TestParseEmptyContent(t *testing.T) {
	if _, err := ParseDoc("a.txt", []byte("")); !errors.Is(err, ErrEmptyContent) {
		t.Fatalf("空内容应 ErrEmptyContent, got %v", err)
	}
	if _, err := ParseDoc("a.txt", []byte("   \n\t ")); !errors.Is(err, ErrEmptyContent) {
		t.Fatalf("空白内容应 ErrEmptyContent, got %v", err)
	}
}

// makeDocx 用手写 zip 生成最小 docx 字节。
func makeDocx(documentXML string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("word/document.xml")
	_, _ = w.Write([]byte(documentXML))
	_ = zw.Close()
	return buf.Bytes()
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/kb/ -run 'TestParseTxtAndMd|TestParseDocx|TestParseBadFile|TestParseEmptyContent' -v`
Expected: FAIL——`undefined: ParseDoc`。

- [ ] **Step 3: 写实现（新建 `parser.go`）**

```go
package kb

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"path/filepath"
	"strings"
)

var (
	ErrBadFile      = errors.New("不支持的文件类型")
	ErrEmptyContent = errors.New("文档内容为空")
)

// ParseDoc 按扩展名解析为纯文本；非法类型/空内容返回哨兵错误。
func ParseDoc(filename string, data []byte) (string, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	var content string
	var err error
	switch ext {
	case ".txt", ".md":
		content = string(data)
	case ".docx":
		content, err = parseDocx(data)
	default:
		return "", ErrBadFile
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(content) == "" {
		return "", ErrEmptyContent
	}
	return content, nil
}

// parseDocx 抽取 word/document.xml 中所有 w:t 文本，按 w:p 段落换行。
func parseDocx(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		raw, rerr := io.ReadAll(rc)
		rc.Close()
		if rerr != nil {
			return "", rerr
		}
		var doc struct {
			Body struct {
				Paras []struct {
					Runs []struct {
						Text string `xml:"t"`
					} `xml:"r"`
				} `xml:"p"`
			} `xml:"body"`
		}
		if err := xml.Unmarshal(raw, &doc); err != nil {
			return "", err
		}
		var b strings.Builder
		for _, p := range doc.Body.Paras {
			for _, r := range p.Runs {
				b.WriteString(r.Text)
			}
			b.WriteString("\n")
		}
		return b.String(), nil
	}
	return "", errors.New("docx: 缺少 word/document.xml")
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/kb/ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/kb/parser.go backend/internal/kb/parser_test.go
git commit -m "feat: kb文件解析支持txt/md/docx"
```

---

### Task 4: kb 文本切块

**Files:**
- Create: `backend/internal/kb/chunk.go`
- Test: `backend/internal/kb/chunk_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `const DefaultChunkSize = 2000`
  - `type ChunkResult struct{ Heading, Content string }`
  - `func Chunk(content string) []ChunkResult`（空内容返回 nil；优先按 Markdown 标题切，组超长按固定大小二次切）

- [ ] **Step 1: 写失败测试（新建 `chunk_test.go`）**

```go
package kb

import (
	"strings"
	"testing"
)

func TestChunkByHeadings(t *testing.T) {
	content := "# 第一章\n第一段\n# 第二章\n第二段\n## 子节\n第三段"
	got := Chunk(content)
	if len(got) != 3 {
		t.Fatalf("chunks = %d, want 3", len(got))
	}
	if got[0].Heading != "第一章" || !strings.Contains(got[0].Content, "第一段") {
		t.Fatalf("chunk0 = %+v", got[0])
	}
	if got[1].Heading != "第二章" || !strings.Contains(got[1].Content, "第二段") {
		t.Fatalf("chunk1 = %+v", got[1])
	}
	if got[2].Heading != "子节" || !strings.Contains(got[2].Content, "第三段") {
		t.Fatalf("chunk2 = %+v", got[2])
	}
}

func TestChunkNoHeadingsSingle(t *testing.T) {
	content := "一段没有标题的正文内容，仅一行。"
	got := Chunk(content)
	if len(got) != 1 || got[0].Heading != "" || got[0].Content != content {
		t.Fatalf("chunks = %+v", got)
	}
}

func TestChunkNoHeadingsLongSplitsBySize(t *testing.T) {
	content := strings.Repeat("字", 4500)
	got := Chunk(content)
	if len(got) != 3 {
		t.Fatalf("4500字应切3块, got %d", len(got))
	}
	for _, c := range got {
		if len([]rune(c.Content)) > DefaultChunkSize {
			t.Fatalf("块超限: %d", len([]rune(c.Content)))
		}
	}
}

func TestChunkLongHeadingGroupSplits(t *testing.T) {
	content := "# 标题\n" + strings.Repeat("长", 4500)
	got := Chunk(content)
	if len(got) < 2 {
		t.Fatalf("超长标题组应多块, got %d", len(got))
	}
	if got[0].Heading != "标题" {
		t.Fatalf("首块应保留标题, got %+v", got[0])
	}
}

func TestChunkEmpty(t *testing.T) {
	if got := Chunk(""); got != nil {
		t.Fatalf("空内容应返回 nil, got %+v", got)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/kb/ -run 'TestChunkByHeadings|TestChunkNoHeadingsSingle|TestChunkNoHeadingsLongSplitsBySize|TestChunkLongHeadingGroupSplits|TestChunkEmpty' -v`
Expected: FAIL——`undefined: Chunk`/`DefaultChunkSize`。

- [ ] **Step 3: 写实现（新建 `chunk.go`）**

```go
package kb

import (
	"regexp"
	"strings"
)

// DefaultChunkSize 无标题结构时的固定块大小（rune 数）。
const DefaultChunkSize = 2000

// ChunkResult 一个知识块（heading 可为空）。
type ChunkResult struct {
	Heading string
	Content string
}

var headingRe = regexp.MustCompile(`^#{1,4}\s(.*)$`)

func headingOf(line string) (string, bool) {
	m := headingRe.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
}

func splitBySize(content string, size int) []ChunkResult {
	if size <= 0 {
		size = DefaultChunkSize
	}
	runes := []rune(content)
	var out []ChunkResult
	for start := 0; start < len(runes); start += size {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, ChunkResult{Content: string(runes[start:end])})
	}
	return out
}

// Chunk 优先按 Markdown 标题切块；无标题或组超长时按固定大小二次切。空内容返回 nil。
func Chunk(content string) []ChunkResult {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	var groups []ChunkResult
	cur := ChunkResult{}
	flush := func() {
		if strings.TrimSpace(cur.Content) != "" {
			groups = append(groups, cur)
		}
		cur = ChunkResult{}
	}
	for _, line := range strings.Split(content, "\n") {
		if h, ok := headingOf(line); ok {
			flush()
			cur.Heading = h
			continue
		}
		if cur.Content != "" {
			cur.Content += "\n"
		}
		cur.Content += line
	}
	flush()
	var out []ChunkResult
	for _, g := range groups {
		if len([]rune(g.Content)) <= DefaultChunkSize {
			out = append(out, g)
			continue
		}
		for i, piece := range splitBySize(g.Content, DefaultChunkSize) {
			p := ChunkResult{Content: piece}
			if i == 0 {
				p.Heading = g.Heading
			}
			out = append(out, p)
		}
	}
	return out
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/kb/ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/kb/chunk.go backend/internal/kb/chunk_test.go
git commit -m "feat: kb文本按标题/固定大小切块"
```

---

### Task 5: kb 自动打标（device_types/metrics/keywords/意图建议）

**Files:**
- Create: `backend/internal/kb/tagger.go`
- Test: `backend/internal/kb/tagger_test.go`

**Interfaces:**
- Consumes: `model.LeafSpec`（Task 1）
- Produces:
  - `type TagResult struct{ DeviceTypes, Metrics, Keywords, Intents []string }`
  - `func Tag(content string, leaves []model.LeafSpec) TagResult`

- [ ] **Step 1: 写失败测试（新建 `tagger_test.go`）**

```go
package kb

import (
	"testing"

	"aiops/internal/model"
)

func TestTagDeviceTypesAndMetrics(t *testing.T) {
	content := "gpu-server 掉卡，检查 gpu_util 与 NVLink 链路，必要时扩容。"
	tags := Tag(content, nil)
	if !contains(tags.DeviceTypes, "gpu-server") {
		t.Fatalf("应命中 gpu-server, got %v", tags.DeviceTypes)
	}
	if !contains(tags.Metrics, "gpu_util") {
		t.Fatalf("应命中 gpu_util, got %v", tags.Metrics)
	}
	if !contains(tags.Keywords, "NVLink") || !contains(tags.Keywords, "扩容") || !contains(tags.Keywords, "掉卡") {
		t.Fatalf("关键词应命中 NVLink/扩容/掉卡, got %v", tags.Keywords)
	}
}

func TestTagNoHit(t *testing.T) {
	content := "与运维无关的普通文本。"
	tags := Tag(content, nil)
	if len(tags.DeviceTypes) != 0 || len(tags.Metrics) != 0 || len(tags.Keywords) != 0 {
		t.Fatalf("不应命中, got %+v", tags)
	}
}

func TestTagIntentSuggestion(t *testing.T) {
	content := "如何排查磁盘空间不足的根因？请给出处置建议。"
	leaves := []model.LeafSpec{
		{Code: "AskRootCause", Name: "AskRootCause", Description: "询问根因", RuleKeywords: []string{"根因", "为什么"}},
		{Code: "AskRecommendation", Name: "AskRecommendation", Description: "询问处置建议", RuleKeywords: []string{"建议", "怎么办"}},
		{Code: "Unknown", Name: "Unknown", Description: "无关", RuleKeywords: []string{}},
	}
	tags := Tag(content, leaves)
	if !contains(tags.Intents, "AskRootCause") || !contains(tags.Intents, "AskRecommendation") {
		t.Fatalf("应建议绑定两意图, got %v", tags.Intents)
	}
}

func TestTagIntentByDescriptionAndName(t *testing.T) {
	content := "文档介绍散热与风扇转速。"
	leaves := []model.LeafSpec{{Code: "Cooling", Name: "散热排查", Description: "散热与风扇", RuleKeywords: []string{}}}
	tags := Tag(content, leaves)
	if !contains(tags.Intents, "Cooling") {
		t.Fatalf("应按 name/description 命中, got %v", tags.Intents)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/kb/ -run 'TestTagDeviceTypesAndMetrics|TestTagNoHit|TestTagIntentSuggestion|TestTagIntentByDescriptionAndName' -v`
Expected: FAIL——`undefined: Tag`/`TagResult`。

- [ ] **Step 3: 写实现（新建 `tagger.go`）**

```go
package kb

import (
	"strings"

	"aiops/internal/model"
)

// 打标词表（与 mockdata 设备 Role 集合保持一致，独立维护避免包依赖）。
var deviceTypeVocab = []string{"gpu-server", "server", "switch", "storage"}
var metricVocab = []string{"cpu_usage", "disk_usage", "gpu_util", "mem_usage", "net_in"}
var keywordVocab = []string{"OOM", "Xid", "NVLink", "扩容", "掉卡", "热插拔", "固件", "光模块", "散热", "日志轮转", "STP", "广播风暴", "tablespace", "InnoDB"}

// TagResult 自动打标结果。
type TagResult struct {
	DeviceTypes []string
	Metrics     []string
	Keywords    []string
	Intents     []string
}

// Tag 扫描正文命中词表；leaves 非空时按其 RuleKeywords/Name/Description 建议绑定意图（跳过 Unknown）。
func Tag(content string, leaves []model.LeafSpec) TagResult {
	low := strings.ToLower(content)
	var out TagResult
	for _, v := range deviceTypeVocab {
		if strings.Contains(low, strings.ToLower(v)) {
			out.DeviceTypes = append(out.DeviceTypes, v)
		}
	}
	for _, v := range metricVocab {
		if strings.Contains(low, strings.ToLower(v)) {
			out.Metrics = append(out.Metrics, v)
		}
	}
	for _, v := range keywordVocab {
		if strings.Contains(low, strings.ToLower(v)) {
			out.Keywords = append(out.Keywords, v)
		}
	}
	for _, l := range leaves {
		if l.Code == "" || l.Code == "Unknown" {
			continue
		}
		if intentHit(l, low) {
			out.Intents = append(out.Intents, l.Code)
		}
	}
	return out
}

func intentHit(l model.LeafSpec, low string) bool {
	for _, kw := range l.RuleKeywords {
		if kw != "" && strings.Contains(low, strings.ToLower(kw)) {
			return true
		}
	}
	if l.Name != "" && strings.Contains(low, strings.ToLower(l.Name)) {
		return true
	}
	return l.Description != "" && strings.Contains(low, strings.ToLower(l.Description))
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/kb/ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/kb/tagger.go backend/internal/kb/tagger_test.go
git commit -m "feat: kb自动打标device_types/metrics/keywords/意图建议"
```

---

### Task 6: kb 服务层（Seed / Upload / CRUD / 列表筛选）

**Files:**
- Create: `backend/internal/kb/service.go`
- Test: `backend/internal/kb/service_test.go`

**Interfaces:**
- Consumes: `ParseDoc`/`ErrBadFile`/`ErrEmptyContent`（Task 3）、`Chunk`/`ChunkResult`（Task 4）、`Tag`（Task 5）、`store.KBStore`/`store.WithKBTx`/`store.KbFilter`/`store.Store`（Task 2）、`model.*`（Task 1）
- Produces:
  - `var ErrNotFound = errors.New("文档不存在")`；`var ErrBadCategory = errors.New("非法分类")`
  - `type UploadReq struct{ Title string; Category model.KBCategory; SourceFile string; Data []byte; DeviceTypes,Vendor,Model,Metrics,Keywords,Intents []string }`
  - `type UpdateReq struct{ Title string; Category model.KBCategory; DeviceTypes,Vendor,Model,Metrics,Keywords,Intents []string }`
  - `type SeedDoc struct{ Title string; Category model.KBCategory; DeviceTypes,Metrics,Keywords []string; Content string }`
  - `type DocDetail struct{ model.KbDocument; Chunks []model.KbChunk \`json:"chunks"\` }`
  - `type Service struct{ st *store.Store; leaves func() []model.LeafSpec }`
  - `func NewService(st *store.Store, leaves func() []model.LeafSpec) *Service`（leaves 可为 nil）
  - `func (s *Service) Seed(docs []SeedDoc) error`（表空才导入）
  - `func (s *Service) Upload(ctx context.Context, req UploadReq) (*model.KbDocument, error)`
  - `func (s *Service) List(ctx context.Context, f store.KbFilter) ([]model.KbDocSummary, error)`
  - `func (s *Service) Get(ctx context.Context, id string) (*DocDetail, error)`
  - `func (s *Service) Update(ctx context.Context, id string, req UpdateReq) (*model.KbDocument, error)`
  - `func (s *Service) Delete(ctx context.Context, id string) error`

- [ ] **Step 1: 写失败测试（新建 `service_test.go`）**

```go
package kb

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"aiops/internal/model"
	"aiops/internal/store"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return NewService(st, nil)
}

func TestSeedOnlyWhenEmpty(t *testing.T) {
	svc := newTestService(t)
	docs := []SeedDoc{
		{Title: "CPU热点排查", Category: model.KBCase, DeviceTypes: []string{"server"}, Metrics: []string{"cpu_usage"}, Keywords: []string{"热点进程"}, Content: "# 排查\n检查top热点进程"},
		{Title: "磁盘清理", Category: model.KBCase, DeviceTypes: []string{"storage"}, Metrics: []string{"disk_usage"}, Content: "df -h 定位挂载点"},
	}
	if err := svc.Seed(docs); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if err := svc.Seed(docs); err != nil {
		t.Fatalf("Seed2: %v", err)
	}
	list, err := svc.List(context.Background(), store.KbFilter{})
	if err != nil || len(list) != 2 {
		t.Fatalf("seed 后应 2 条, got %v %+v", err, list)
	}
	if list[0].ChunkCount == 0 {
		t.Fatalf("chunk_count 应为正, got %+v", list[0])
	}
}

func TestUploadAndFields(t *testing.T) {
	svc := newTestService(t)
	doc, err := svc.Upload(context.Background(), UploadReq{
		SourceFile: "s5560.md", Data: []byte("# 光模块\n检查光模块告警，涉及 switch 设备。"),
		Category: model.KBManual, Vendor: []string{"H3C"}, Model: []string{"S5560"},
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if doc.ID == "" || doc.Title != "s5560" || doc.Category != model.KBManual {
		t.Fatalf("doc = %+v", doc)
	}
	if !hasStr(doc.DeviceTypes, "switch") {
		t.Fatalf("应自动命中 switch, got %v", doc.DeviceTypes)
	}
	if !hasStr(doc.Keywords, "光模块") {
		t.Fatalf("应自动命中 光模块, got %v", doc.Keywords)
	}
	if !hasStr(doc.Vendor, "H3C") || !hasStr(doc.Model, "S5560") {
		t.Fatalf("vendor/model 人工字段, got %+v", doc)
	}
	detail, err := svc.Get(context.Background(), doc.ID)
	if err != nil || len(detail.Chunks) == 0 {
		t.Fatalf("Get: %v %+v", err, detail)
	}
}

func TestUploadOverrideTags(t *testing.T) {
	svc := newTestService(t)
	doc, err := svc.Upload(context.Background(), UploadReq{
		SourceFile: "doc.md", Data: []byte("交换机运维"),
		Category: model.KBCase, DeviceTypes: []string{"switch"}, Keywords: []string{"自定义"},
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if !hasStr(doc.Keywords, "自定义") || len(doc.DeviceTypes) != 1 {
		t.Fatalf("覆盖未生效: %+v", doc)
	}
}

func TestUploadErrors(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.Upload(context.Background(), UploadReq{SourceFile: "a.pdf", Data: []byte("x")}); !errors.Is(err, ErrBadFile) {
		t.Fatalf("非法类型应 ErrBadFile, got %v", err)
	}
	if _, err := svc.Upload(context.Background(), UploadReq{SourceFile: "a.txt", Data: []byte("  ")}); !errors.Is(err, ErrEmptyContent) {
		t.Fatalf("空内容应 ErrEmptyContent, got %v", err)
	}
	if _, err := svc.Upload(context.Background(), UploadReq{SourceFile: "a.txt", Data: []byte("x"), Category: model.KBCategory("bogus")}); !errors.Is(err, ErrBadCategory) {
		t.Fatalf("非法分类应 ErrBadCategory, got %v", err)
	}
}

func TestUpdateAndDelete(t *testing.T) {
	svc := newTestService(t)
	doc, _ := svc.Upload(context.Background(), UploadReq{SourceFile: "a.txt", Data: []byte("正文"), Category: model.KBCase})
	upd, err := svc.Update(context.Background(), doc.ID, UpdateReq{
		Title: "改名", Category: model.KBManual, DeviceTypes: []string{"server"}, Vendor: []string{"Dell"}, Model: []string{"R750"}, Intents: []string{"AskRootCause"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if upd.Title != "改名" || upd.Category != model.KBManual || upd.Content != "正文" {
		t.Fatalf("update = %+v", upd)
	}
	if err := svc.Delete(context.Background(), doc.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get(context.Background(), doc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后应 ErrNotFound, got %v", err)
	}
	if _, err := svc.Update(context.Background(), "nope", UpdateReq{Title: "x", Category: model.KBCase}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("更新不存在应 ErrNotFound, got %v", err)
	}
	if err := svc.Delete(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除不存在应 ErrNotFound, got %v", err)
	}
}

func TestUploadTitleFallbackAndIntentSuggestion(t *testing.T) {
	svc := NewService(storeMust(t), func() []model.LeafSpec {
		return []model.LeafSpec{{Code: "AskRootCause", Name: "AskRootCause", Description: "询问根因", RuleKeywords: []string{"根因"}}}
	})
	doc, err := svc.Upload(context.Background(), UploadReq{SourceFile: "my-guide.md", Data: []byte("排查磁盘根因正文")})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if doc.Title != "my-guide" {
		t.Fatalf("title fallback = %q", doc.Title)
	}
	if !hasStr(doc.Intents, "AskRootCause") {
		t.Fatalf("应建议绑定 AskRootCause, got %v", doc.Intents)
	}
}

func hasStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func storeMust(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/kb/ -run 'TestSeedOnlyWhenEmpty|TestUploadAndFields|TestUploadOverrideTags|TestUploadErrors|TestUpdateAndDelete|TestUploadTitleFallbackAndIntentSuggestion' -v`
Expected: FAIL——`undefined: Service`/`NewService` 等。

- [ ] **Step 3: 写实现（新建 `service.go`）**

```go
package kb

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"aiops/internal/model"
	"aiops/internal/store"
)

var (
	ErrNotFound    = errors.New("文档不存在")
	ErrBadCategory = errors.New("非法分类")
)

// UploadReq 上传请求；标签字段非空时覆盖自动打标结果。
type UploadReq struct {
	Title       string
	Category    model.KBCategory
	SourceFile  string
	Data        []byte
	DeviceTypes []string
	Vendor      []string
	Model       []string
	Metrics     []string
	Keywords    []string
	Intents     []string
}

// UpdateReq 全量覆盖元数据（不动 content）。
type UpdateReq struct {
	Title       string
	Category    model.KBCategory
	DeviceTypes []string
	Vendor      []string
	Model       []string
	Metrics     []string
	Keywords    []string
	Intents     []string
}

// SeedDoc 种子文档（mockdata 导入视图）。
type SeedDoc struct {
	Title       string
	Category    model.KBCategory
	DeviceTypes []string
	Metrics     []string
	Keywords    []string
	Content     string
}

// DocDetail 详情视图：文档 + 全部块。
type DocDetail struct {
	model.KbDocument
	Chunks []model.KbChunk `json:"chunks"`
}

type Service struct {
	st     *store.Store
	leaves func() []model.LeafSpec
}

func NewService(st *store.Store, leaves func() []model.LeafSpec) *Service {
	return &Service{st: st, leaves: leaves}
}

// Seed 表空时导入种子文档（幂等）。
func (s *Service) Seed(docs []SeedDoc) error {
	n, err := s.st.CountKbDocuments()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	now := time.Now()
	for _, d := range docs {
		if strings.TrimSpace(d.Content) == "" {
			continue
		}
		cat := d.Category
		if cat == "" || !model.IsKBCategory(cat) {
			cat = model.KBOther
		}
		doc := model.KbDocument{
			ID: model.NewID("kbdoc"), Title: d.Title, Category: cat,
			DeviceTypes: d.DeviceTypes, Metrics: d.Metrics, Keywords: d.Keywords,
			Content: d.Content, CreatedAt: now, UpdatedAt: now,
		}
		if err := s.insertDocAndChunks(doc, Chunk(d.Content), now); err != nil {
			return err
		}
	}
	return nil
}

// Upload 解析→打标（合并覆盖）→切块→事务入库。
func (s *Service) Upload(ctx context.Context, req UploadReq) (*model.KbDocument, error) {
	content, err := ParseDoc(req.SourceFile, req.Data)
	if err != nil {
		return nil, err
	}
	cat := req.Category
	if cat != "" && !model.IsKBCategory(cat) {
		return nil, ErrBadCategory
	}
	if cat == "" {
		cat = model.KBOther
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		base := filepath.Base(req.SourceFile)
		title = strings.TrimSuffix(base, filepath.Ext(base))
	}
	var leaves []model.LeafSpec
	if s.leaves != nil {
		leaves = s.leaves()
	}
	tags := Tag(content, leaves)
	merge := func(auto, override []string) []string {
		if len(override) > 0 {
			return override
		}
		return auto
	}
	now := time.Now()
	doc := model.KbDocument{
		ID: model.NewID("kbdoc"), Title: title, Category: cat,
		DeviceTypes: merge(tags.DeviceTypes, req.DeviceTypes),
		Vendor:      req.Vendor,
		Model:       req.Model,
		Metrics:     merge(tags.Metrics, req.Metrics),
		Keywords:    merge(tags.Keywords, req.Keywords),
		Intents:     merge(tags.Intents, req.Intents),
		SourceFile:  req.SourceFile,
		Content:     content,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.insertDocAndChunks(doc, Chunk(content), now); err != nil {
		return nil, err
	}
	return &doc, nil
}

func (s *Service) insertDocAndChunks(doc model.KbDocument, chunks []ChunkResult, now time.Time) error {
	return s.st.WithKBTx(func(tx store.KBStore) error {
		if err := tx.InsertKbDocument(doc); err != nil {
			return err
		}
		for i, c := range chunks {
			if err := tx.InsertKbChunk(model.KbChunk{
				ID: model.NewID("kbchunk"), DocID: doc.ID, Seq: i, Heading: c.Heading, Content: c.Content, CreatedAt: now,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) List(ctx context.Context, f store.KbFilter) ([]model.KbDocSummary, error) {
	return s.st.ListKbDocuments(f)
}

func (s *Service) Get(ctx context.Context, id string) (*DocDetail, error) {
	doc, err := s.st.GetKbDocument(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	chunks, err := s.st.ListKbChunksByDoc(id)
	if err != nil {
		return nil, err
	}
	return &DocDetail{KbDocument: doc, Chunks: chunks}, nil
}

func (s *Service) Update(ctx context.Context, id string, req UpdateReq) (*model.KbDocument, error) {
	cur, err := s.st.GetKbDocument(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !model.IsKBCategory(req.Category) {
		return nil, ErrBadCategory
	}
	cur.Title = req.Title
	cur.Category = req.Category
	cur.DeviceTypes = req.DeviceTypes
	cur.Vendor = req.Vendor
	cur.Model = req.Model
	cur.Metrics = req.Metrics
	cur.Keywords = req.Keywords
	cur.Intents = req.Intents
	cur.UpdatedAt = time.Now()
	if err := s.st.WithKBTx(func(tx store.KBStore) error {
		return tx.InsertKbDocument(cur)
	}); err != nil {
		return nil, err
	}
	return &cur, nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	return s.st.WithKBTx(func(tx store.KBStore) error {
		if _, err := tx.GetKbDocument(id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if err := tx.DeleteKbChunks(id); err != nil {
			return err
		}
		return tx.DeleteKbDocument(id)
	})
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/kb/ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/kb/service.go backend/internal/kb/service_test.go
git commit -m "feat: kb服务层Seed/Upload/CRUD/列表筛选"
```

---

### Task 7: context 意图加权 + Assembler 知识源改读 DB

**Files:**
- Modify: `backend/internal/context/chunk.go`（`Query` 加 `Intents`）
- Modify: `backend/internal/context/selector.go`（权重与意图信号）
- Modify: `backend/internal/context/assembler.go`（`candidates()` 改读 `ListKnowledgeChunks`，删 `mockdata` 引用）
- Modify: `backend/internal/context/store.go`（透传 `ListKnowledgeChunks`）
- Test: `backend/internal/context/selector_test.go`（追加）、`backend/internal/context/assembler_test.go`（更新）

**Interfaces:**
- Consumes: `model.KbKnowledgeChunk`（Task 1）、`store.ListKnowledgeChunks`（Task 2）
- Produces:
  - `Query` 新增字段 `Intents []string`
  - `Selector.Score` 新增意图命中信号；权重更新为 `kwWeight=0.3, tagWeight=0.3, intentWeight=0.25, recencyWeight=0.15`
  - `func (cs *Store) ListKnowledgeChunks() ([]model.KbKnowledgeChunk, error)`（context.Store 透传）

- [ ] **Step 1: 写失败测试**

`selector_test.go` 追加：

```go
func TestScoreIntentBoost(t *testing.T) {
	sel := Selector{}
	now := time.Now()
	base := func(intents string) Chunk {
		return chunk(SourceKnowledge, "磁盘扩容预案正文", map[string]string{
			"metrics": "disk_usage", "intents": intents, "created_at": now.Format(time.RFC3339),
		})
	}
	hit := base("AskRootCause,AskRecommendation")
	miss := base("OtherIntent")
	q := Query{Metric: "disk_usage", Intents: []string{"AskRootCause"}}
	if sel.Score(hit, q, now) <= sel.Score(miss, q, now) {
		t.Fatalf("意图命中应加分: hit=%f miss=%f", sel.Score(hit, q, now), sel.Score(miss, q, now))
	}
	q2 := Query{Metric: "disk_usage"}
	if sel.Score(hit, q2, now) != sel.Score(miss, q2, now) {
		t.Fatalf("无意图查询应同分: %f vs %f", sel.Score(hit, q2, now), sel.Score(miss, q2, now))
	}
}
```

`assembler_test.go` 追加 `seedKbDoc` 辅助并在 `TestAssembleSections` 开头调用（保留原断言），另追加意图用例：

```go
func seedKbDoc(t *testing.T, s *store.Store) {
	t.Helper()
	now := time.Now()
	if err := s.WithKBTx(func(tx store.KBStore) error {
		if err := tx.InsertKbDocument(model.KbDocument{
			ID: "kbdoc-1", Title: "磁盘扩容预案", Category: model.KBCase,
			DeviceTypes: []string{"server"}, Metrics: []string{"disk_usage"},
			Keywords: []string{"扩容", "清理"}, Intents: []string{"AskRecommendation"},
			Content: "磁盘使用率超过95%时扩容或清理日志。", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		return tx.InsertKbChunk(model.KbChunk{ID: "kbchunk-1", DocID: "kbdoc-1", Seq: 0,
			Heading: "", Content: "磁盘使用率超过95%时扩容或清理日志。", CreatedAt: now})
	}); err != nil {
		t.Fatalf("seed kb: %v", err)
	}
}

func TestAssembleKnowledgeByIntent(t *testing.T) {
	mockdata.GenerateAll(42)
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	seedKbDoc(t, s)
	cs := NewStore(s)
	asm := NewAssembler(cs, 3)
	as, err := asm.Assemble(Query{Device: "db-01", Metric: "disk_usage", Intents: []string{"AskRecommendation"}}, false, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	found := false
	for _, c := range as.Selected {
		if c.Source == SourceKnowledge && strings.Contains(c.Content, "磁盘扩容预案") {
			found = true
		}
	}
	if !found {
		t.Fatalf("应命中意图对应知识块:\n%s", as.User)
	}
}
```

在 `TestAssembleSections` 内 `cs := NewStore(s)` 前调用 `seedKbDoc(t, s)`，并断言 `as.User` 含"磁盘扩容预案"。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/context/ -run 'TestScoreIntentBoost|TestAssembleSections|TestAssembleKnowledgeByIntent' -v`
Expected: FAIL——`Query` 无 `Intents` 字段、`Score` 未加分等。

- [ ] **Step 3: 写实现**

`chunk.go` 的 `Query` 追加：

```go
type Query struct {
	Device   string
	Metric   string
	Severity string
	Stage    string
	RunID    string
	Keywords []string
	Intents  []string
}
```

`selector.go` 常量改为：

```go
const (
	DefaultTopK      = 3
	DefaultThreshold = 0.15
	kwWeight         = 0.3
	tagWeight        = 0.3
	intentWeight     = 0.25
	recencyWeight    = 0.15
	halfLifeMinutes  = 60.0
)
```

`Score` 在 `recencyScore` 计算后追加并改返回值：

```go
	intentScore := 0.0
	if len(q.Intents) > 0 && hitsIntent(q.Intents, chunk.Metadata["intents"]) {
		intentScore = 1.0
	}
	return kwWeight*kwScore + tagWeight*tagScore + intentWeight*intentScore + recencyWeight*recencyScore
```

`selector.go` 追加辅助函数：

```go
// hitsIntent 查询意图与块文档 intents 元数据（逗号分隔）是否有交集。
func hitsIntent(query []string, meta string) bool {
	if meta == "" {
		return false
	}
	tokens := map[string]bool{}
	for _, t := range strings.Split(meta, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tokens[t] = true
		}
	}
	for _, q := range query {
		if tokens[q] {
			return true
		}
	}
	return false
}
```

`assembler.go` 的 `candidates()` 中 `for _, d := range mockdata.Docs() { ... }` 整段替换为：

```go
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
```

`assembler.go` 追加辅助函数并 import `aiops/internal/model`（移除不再用的 `mockdata` import 若不再被引用）：

```go
func knowledgeContent(kc model.KbKnowledgeChunk) string {
	head := "[" + categoryName(kc.Category) + "] " + kc.Title
	if kc.Heading != "" {
		head += " §" + kc.Heading
	}
	return head + ": " + kc.Content
}

func categoryName(c model.KBCategory) string {
	switch c {
	case model.KBCase:
		return "运维案例"
	case model.KBManual:
		return "产品手册"
	case model.KBRunbook:
		return "操作手册"
	default:
		return "其他"
	}
}
```

`context/store.go` 追加：

```go
func (cs *Store) ListKnowledgeChunks() ([]model.KbKnowledgeChunk, error) {
	return cs.st.ListKnowledgeChunks()
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/context/ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/context/chunk.go backend/internal/context/selector.go backend/internal/context/selector_test.go backend/internal/context/assembler.go backend/internal/context/assembler_test.go backend/internal/context/store.go
git commit -m "feat: 检索新增意图加权，Assembler知识源改读知识库DB"
```

---

### Task 8: pipeline 按阶段注入意图 code

**Files:**
- Modify: `backend/internal/pipeline/rca.go`（两处 `ctxpkg.Query`）
- Modify: `backend/internal/pipeline/recommend.go`（一处 `ctxpkg.Query`）
- Test: 无新增（Query 字段为编译期保证，冒烟由 Task 7 context 测试覆盖）

**Interfaces:**
- Consumes: `ctxpkg.Query{..., Intents}`（Task 7）
- Produces: 无（行为变化：rca 注入 `["AskRootCause"]`，recommend 注入 `["AskRecommendation"]`）

- [ ] **Step 1: 写失败测试**

本任务无新增测试（改动为数据字段补齐，编译期由 `go test ./internal/pipeline/` 保证不回归）。直接进入实现。

- [ ] **Step 2: 运行测试确认当前基线**

Run: `go test ./internal/pipeline/ -v`
Expected: 全部 PASS（基线）。

- [ ] **Step 3: 写实现**

`rca.go` 第一处（`react` 内）：

```go
	q := ctxpkg.Query{
		Device: st.Alert.Device, Metric: st.Alert.Metric, Severity: st.Alert.Severity,
		Stage: "rca", RunID: st.Run.ID, Keywords: []string{st.Alert.Message},
		Intents: []string{"AskRootCause"},
	}
```

`rca.go` 第二处（`singleCall` 内联）：

```go
		if as, err := st.Ctx.Assemble(ctxpkg.Query{
			Device: st.Alert.Device, Metric: st.Alert.Metric, Severity: st.Alert.Severity,
			Stage: "rca", RunID: st.Run.ID, Keywords: []string{st.Alert.Message},
			Intents: []string{"AskRootCause"}}, false); err == nil {
```

`recommend.go` 内联：

```go
			if as, err := st.Ctx.Assemble(ctxpkg.Query{
				Device: st.Alert.Device, Metric: st.Alert.Metric, Severity: st.Alert.Severity,
				Stage: "recommend", RunID: st.Run.ID, Keywords: []string{st.Alert.Message},
				Intents: []string{"AskRecommendation"}}, false); err == nil {
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/pipeline/ -v && go build ./...`
Expected: 全部 PASS，构建成功。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/pipeline/rca.go backend/internal/pipeline/recommend.go
git commit -m "feat: 流水线rca/recommend按阶段注入意图code"
```

---

### Task 9: agent 识别意图后注入 Intents + KB 意图检索回复

**Files:**
- Modify: `backend/internal/agent/agent.go`
- Test: `backend/internal/agent/agent_test.go`（追加）

**Interfaces:**
- Consumes: `ctxpkg.Query{..., Intents}`（Task 7）、`ctxpkg.NewAssembler`/`ctxpkg.SourceKnowledge`（已有）
- Produces:
  - `func (a *Agent) buildAssembled(device, metric string, intents []string) *ctxpkg.Assembled`（签名变更）
  - `func isSystemIntent(code Intent) bool`
  - `func (a *Agent) kbReply(ctx context.Context, code string) (string, bool)`
- 行为：`memorySuffix` 传 `nil`；`answerWithLLM` 传 `[]string{intent}`；`answerSingle`/`answerOneStatus` 的 default 分支：非内置意图先试 `kbReply`，失败回退占位语。

- [ ] **Step 1: 写失败测试（追加 `agent_test.go`）**

```go
func TestAgentKBIntentReturnsKnowledge(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := ctxpkg.NewStore(s)
	_ = s.WithKBTx(func(tx store.KBStore) error {
		now := time.Now()
		if err := tx.InsertKbDocument(model.KbDocument{
			ID: "kbdoc-1", Title: "磁盘清理手册", Category: model.KBRunbook,
			Metrics: []string{"disk_usage"}, Intents: []string{"DiskCleanup"},
			Content: "清理日志与归档。", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		return tx.InsertKbChunk(model.KbChunk{ID: "c1", DocID: "kbdoc-1", Seq: 0,
			Content: "清理日志与归档。", CreatedAt: now})
	})
	ag := NewWithLeaves(s, nil, nil, nil, cs, 3, 3, func() []model.LeafSpec {
		return []model.LeafSpec{{Code: "DiskCleanup", Name: "DiskCleanup", Description: "磁盘清理", RuleKeywords: []string{"清理磁盘"}}}
	})
	msg, _ := ag.Handle(context.Background(), "怎么清理磁盘")
	if !strings.Contains(msg.Content, "磁盘清理手册") {
		t.Fatalf("KB 意图应返回知识, got %q", msg.Content)
	}
}

func TestAgentKBIntentFallsBackToPlaceholder(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := ctxpkg.NewStore(s)
	ag := NewWithLeaves(s, nil, nil, nil, cs, 3, 3, func() []model.LeafSpec {
		return []model.LeafSpec{{Code: "MCP1", Name: "MCP1", Description: "mcp", RuleKeywords: []string{"mcp命令"}}}
	})
	msg, _ := ag.Handle(context.Background(), "执行 mcp命令")
	if !strings.Contains(msg.Content, "尚未实现") {
		t.Fatalf("无命中知识应回退占位语, got %q", msg.Content)
	}
}
```

> 注意：`agent_test.go` 顶部需已有 `ctxpkg`（`aiops/internal/context`）与 `store` import；若无则补。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent/ -run 'TestAgentKBIntentReturnsKnowledge|TestAgentKBIntentFallsBackToPlaceholder' -v`
Expected: FAIL——KB 意图命中后仍返回占位语。

- [ ] **Step 3: 写实现（修改 `agent.go`）**

`buildAssembled` 改为：

```go
func (a *Agent) buildAssembled(device, metric string, intents []string) *ctxpkg.Assembled {
	if a.ctxStore == nil || device == "" || metric == "" {
		return nil
	}
	asm := ctxpkg.NewAssembler(a.ctxStore, a.topK)
	as, err := asm.Assemble(ctxpkg.Query{Device: device, Metric: metric, Stage: "chat", Intents: intents}, false, nil)
	if err != nil {
		return nil
	}
	return as
}
```

`memorySuffix` 调用改 `a.buildAssembled(device, metric, nil)`；`answerWithLLM` 调用改 `a.buildAssembled(alert.Device, alert.Metric, []string{intent})`。

`answerSingle` 的 default 分支改为：

```go
	default:
		if !isSystemIntent(intent) {
			if content, ok := a.kbReply(ctx, intent); ok {
				return content
			}
		}
		return a.placeholderReply(intent)
```

`answerOneStatus` 的 default 分支改为：

```go
	default:
		if !isSystemIntent(intent) {
			if content, ok := a.kbReply(ctx, intent); ok {
				return content, "success"
			}
			return a.placeholderReply(intent), statusFor(intent)
		}
		return a.placeholderReply(intent), statusFor(intent)
```

追加辅助函数（`placeholderReply` 附近）：

```go
// isSystemIntent 内置 SYSTEM 意图（种子 6 个）。
func isSystemIntent(code Intent) bool {
	switch code {
	case Unknown, ListAlerts, InspectAlert, AskRootCause, AskRecommendation, AskStatus:
		return true
	}
	return false
}

// kbReply 按意图 code 检索知识库并返回命中文档行；无命中返回 ok=false。
func (a *Agent) kbReply(ctx context.Context, code string) (string, bool) {
	if a.ctxStore == nil {
		return "", false
	}
	asm := ctxpkg.NewAssembler(a.ctxStore, a.topK)
	as, err := asm.Assemble(ctxpkg.Query{Stage: "chat", Intents: []string{code}}, false, nil)
	if err != nil {
		return "", false
	}
	var lines []string
	for _, c := range as.Selected {
		if c.Source == ctxpkg.SourceKnowledge {
			lines = append(lines, "- "+c.Content)
		}
	}
	if len(lines) == 0 {
		return "", false
	}
	return "知识库相关文档：\n" + strings.Join(lines, "\n"), true
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agent/ -v`
Expected: 全部 PASS（含既有护栏）。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/agent/agent.go backend/internal/agent/agent_test.go
git commit -m "feat: agent按识别意图检索知识库，KB意图返回知识"
```

---

### Task 10: API 路由 + main 装配与种子导入

**Files:**
- Modify: `backend/internal/api/server.go`（`Server` 加 `kbsvc`；`NewServer` 签名；`routes()`；handler + `splitCSV`/`kbStatus`）
- Modify: `backend/internal/api/server_test.go`、`backend/internal/api/intent_test.go`（`NewServer` 调用点加参）
- Modify: `backend/cmd/server/main.go`（构造 `kb.Service`、`Seed`、注入）
- Test: `backend/internal/api/server_test.go`（追加）

**Interfaces:**
- Consumes: `kb.Service`/`kb.UploadReq`/`kb.UpdateReq`/`kb.DocDetail`/`kb.Err*`（Task 6）、`store.KbFilter`（Task 2）、`model.KBCategory`（Task 1）、`intent.Service.ActiveLeaves`（已有）
- Produces:
  - `func NewServer(cfg config.Config, s *store.Store, ag *agent.Agent, eng *pipeline.Engine, h *hub.Hub, sim *simulator.Simulator, svc *intent.Service, kbsvc *kb.Service) http.Handler`
  - 路由：`GET /api/kb/docs`、`GET /api/kb/docs/{id}`、`POST /api/kb/upload`、`PATCH /api/kb/docs/{id}`、`DELETE /api/kb/docs/{id}`

- [ ] **Step 1: 写失败测试（追加 `server_test.go`）**

先给既有 `NewServer(...)` 调用点追加 `kb.NewService(s, nil)` 参数（`server_test.go` 两处、`intent_test.go` 一处），再追加：

```go
func newKBServer(t *testing.T) *httptest.Server {
	t.Helper()
	st, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	t.Cleanup(func() { st.Close() })
	ag := agent.New(st, nil, nil)
	svc := intent.NewService(st, intent.NewMemCache())
	kbsvc := kb.NewService(st, nil)
	srv := NewServer(config.Config{}, st, ag, nil, hub.New(), nil, svc, kbsvc)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts
}

func TestKBListEmptyAndUpload(t *testing.T) {
	ts := newKBServer(t)
	res, err := http.Get(ts.URL + "/api/kb/docs")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	var list []json.RawMessage
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("空库应空列表, got %d", len(list))
	}

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, _ := mw.CreateFormFile("file", "手册.md")
	_, _ = fw.Write([]byte("# 光模块\n检查光模块告警，涉及 switch 设备。"))
	_ = mw.WriteField("category", "manual")
	_ = mw.WriteField("vendor", "H3C")
	_ = mw.WriteField("model", "S5560")
	_ = mw.WriteField("intents", "AskRootCause")
	_ = mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/kb/upload", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	ures, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer ures.Body.Close()
	if ures.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d body=%s", ures.StatusCode, readBody(ures))
	}
	var doc model.KbDocument
	if err := json.NewDecoder(ures.Body).Decode(&doc); err != nil {
		t.Fatalf("decode doc: %v", err)
	}
	if doc.Category != model.KBManual || len(doc.DeviceTypes) == 0 || len(doc.Intents) == 0 {
		t.Fatalf("doc = %+v", doc)
	}
	res2, _ := http.Get(ts.URL + "/api/kb/docs?vendor=H3C")
	defer res2.Body.Close()
	var list2 []model.KbDocSummary
	if err := json.NewDecoder(res2.Body).Decode(&list2); err != nil {
		t.Fatalf("decode list2: %v", err)
	}
	if len(list2) != 1 || list2[0].ChunkCount == 0 {
		t.Fatalf("filtered list = %+v", list2)
	}
	res3, _ := http.Get(ts.URL + "/api/kb/docs/" + doc.ID)
	defer res3.Body.Close()
	var detail kb.DocDetail
	if err := json.NewDecoder(res3.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if len(detail.Chunks) == 0 {
		t.Fatalf("detail chunks empty")
	}
	dreq, _ := http.NewRequest("DELETE", ts.URL+"/api/kb/docs/"+doc.ID, nil)
	dres, _ := http.DefaultClient.Do(dreq)
	defer dres.Body.Close()
	if dres.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d", dres.StatusCode)
	}
}

func TestKBUploadBadFile(t *testing.T) {
	ts := newKBServer(t)
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, _ := mw.CreateFormFile("file", "a.pdf")
	_, _ = fw.Write([]byte("x"))
	_ = mw.WriteField("category", "manual")
	_ = mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/kb/upload", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, _ := http.DefaultClient.Do(req)
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("非法类型应 400, got %d", res.StatusCode)
	}
}

func readBody(res *http.Response) string {
	b, _ := io.ReadAll(res.Body)
	return string(b)
}
```

> import 需补：`bytes`、`mime/multipart`、`io`、`aiops/internal/kb`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/api/ -run 'TestKBListEmptyAndUpload|TestKBUploadBadFile|TestHealthzAndAlerts|TestChatEndpoint' -v`
Expected: FAIL——`undefined: kb`/签名不匹配。

- [ ] **Step 3: 写实现**

`server.go`：
- `Server` struct 增字段 `kbsvc *kb.Service`；`NewServer` 增参数并赋值。
- `routes()` 追加：

```go
	s.mux.HandleFunc("GET /api/kb/docs", s.handleKbList)
	s.mux.HandleFunc("GET /api/kb/docs/{id}", s.handleKbGet)
	s.mux.HandleFunc("POST /api/kb/upload", s.handleKbUpload)
	s.mux.HandleFunc("PATCH /api/kb/docs/{id}", s.handleKbUpdate)
	s.mux.HandleFunc("DELETE /api/kb/docs/{id}", s.handleKbDelete)
```

- handlers：

```go
func (s *Server) handleKbList(w http.ResponseWriter, r *http.Request) {
	f := store.KbFilter{
		Category:   r.URL.Query().Get("category"),
		DeviceType: r.URL.Query().Get("device_type"),
		Vendor:     r.URL.Query().Get("vendor"),
		Model:      r.URL.Query().Get("model"),
		Intent:     r.URL.Query().Get("intent"),
		Q:          r.URL.Query().Get("q"),
	}
	list, err := s.kbsvc.List(r.Context(), f)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleKbGet(w http.ResponseWriter, r *http.Request) {
	detail, err := s.kbsvc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, kbStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleKbUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无法解析上传表单"})
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 file 字段"})
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	req := kb.UploadReq{
		Title:       r.FormValue("title"),
		Category:    model.KBCategory(r.FormValue("category")),
		SourceFile:  hdr.Filename,
		Data:        data,
		DeviceTypes: splitCSV(r.FormValue("device_types")),
		Vendor:      splitCSV(r.FormValue("vendor")),
		Model:       splitCSV(r.FormValue("model")),
		Metrics:     splitCSV(r.FormValue("metrics")),
		Keywords:    splitCSV(r.FormValue("keywords")),
		Intents:     splitCSV(r.FormValue("intents")),
	}
	doc, err := s.kbsvc.Upload(r.Context(), req)
	if err != nil {
		writeJSON(w, kbStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (s *Server) handleKbUpdate(w http.ResponseWriter, r *http.Request) {
	var req kb.UpdateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}
	doc, err := s.kbsvc.Update(r.Context(), r.PathValue("id"), req)
	if err != nil {
		writeJSON(w, kbStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (s *Server) handleKbDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.kbsvc.Delete(r.Context(), r.PathValue("id")); err != nil {
		writeJSON(w, kbStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func splitCSV(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func kbStatus(err error) int {
	switch {
	case errors.Is(err, kb.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, kb.ErrBadFile), errors.Is(err, kb.ErrEmptyContent), errors.Is(err, kb.ErrBadCategory):
		return http.StatusBadRequest
	default:
		return http.StatusBadRequest
	}
}
```

- `server.go` import 追加：`"io"`、`"aiops/internal/kb"`。

`main.go` 装配（调整顺序：先 `intentsvc`，再 `kbsvc`（闭包引用 `intentsvc`），再 `ag`，再 `sim`）：

```go
	intentsvc := intent.NewService(st, intent.NewMemCache())
	kbsvc := kb.NewService(st, func() []model.LeafSpec {
		leaves, _ := intentsvc.ActiveLeaves(context.Background())
		return leaves
	})
	var seeds []kb.SeedDoc
	for _, d := range mockdata.Docs() {
		seeds = append(seeds, kb.SeedDoc{
			Title: d.Title, Category: model.KBCase,
			DeviceTypes: d.DeviceTypes, Metrics: d.Metrics, Keywords: d.Keywords, Content: d.Body,
		})
	}
	if err := kbsvc.Seed(seeds); err != nil {
		log.Printf("kb seed: %v", err)
	}

	ag := agent.NewWithLeaves(st, lc, light, eng, cs, cfg.TopK, cfg.MaxSplit, func() []model.LeafSpec {
		leaves, _ := intentsvc.ActiveLeaves(context.Background())
		return leaves
	})
	sim := simulator.New(st, func(a model.Alert) {
		h.Publish("alert", a)
		go func() {
			_ = eng.Run(context.Background(), a, nil)
		}()
	})
	sim.Start(context.Background(), cfg.SimInterval)

	handler := api.NewServer(cfg, st, ag, eng, h, sim, intentsvc, kbsvc)
```

删除原 `svc := intent.NewService(...)` 与重复闭包。`main.go` import 追加 `"aiops/internal/kb"`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/api/ -v && go build ./... && go test ./...`
Expected: 全部 PASS，全量构建通过。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/api/server.go backend/internal/api/server_test.go backend/internal/api/intent_test.go backend/cmd/server/main.go
git commit -m "feat: 知识库API路由/上传端点/main种子导入装配"
```

---

### Task 11: 前端知识库管理页

**Files:**
- Modify: `frontend/src/types.ts`（KB 类型）
- Modify: `frontend/src/api/client.ts`（KB API）
- Modify: `frontend/src/App.tsx`（第三个 tab）
- Create: `frontend/src/components/KnowledgeBase.tsx`
- Test: `frontend/src/components/KnowledgeBase.test.tsx`

**Interfaces:**
- Consumes: `fetchIntentLeaves`（已有，`/api/intents/leaves` 返回 `IntentNode[]`——需确认；若返回 `LeafSpec` 形状以实际为准，本页仅取 `{code,name}`）
- Produces:
  - `export type KbCategory = 'case' | 'manual' | 'runbook' | 'other'`
  - `export interface KbDoc` / `KbChunk` / `KbDocSummary extends KbDoc { chunk_count: number }` / `KbDocDetail extends KbDoc { chunks: KbChunk[] }` / `KbDocUpsert`
  - `export interface KbFilter { category?: string; device_type?: string; vendor?: string; model?: string; intent?: string; q?: string }`
  - `fetchKbDocs(f: KbFilter): Promise<KbDocSummary[]>`
  - `fetchKbDoc(id: string): Promise<KbDocDetail>`
  - `uploadKbDoc(form: FormData): Promise<KbDoc>`
  - `updateKbDoc(id: string, body: KbDocUpsert): Promise<KbDoc>`
  - `deleteKbDoc(id: string): Promise<{ status: string }>`
  - `export interface IntentLeaf { code: string; name: string; description: string; rule_keywords: string[]; path: string }`（若已有同名类型则复用）
  - `fetchIntentLeaves(): Promise<IntentLeaf[]>`（补到 `client.ts`，后端 `GET /api/intents/leaves` 已有）

- [ ] **Step 1: 写失败测试（新建 `KnowledgeBase.test.tsx`）**

```tsx
import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import KnowledgeBase from './KnowledgeBase'
import { fetchKbDocs, uploadKbDoc, deleteKbDoc } from '../api/client'
import type { KbDocSummary } from '../types'

vi.mock('../api/client', () => ({
  fetchKbDocs: vi.fn(),
  fetchKbDoc: vi.fn(),
  uploadKbDoc: vi.fn(),
  updateKbDoc: vi.fn(),
  deleteKbDoc: vi.fn(),
  fetchIntentLeaves: vi.fn().mockResolvedValue([]),
}))

const doc = (id: string, title: string, category: KbDocSummary['category']): KbDocSummary => ({
  id, title, category, device_types: ['switch'], vendor: ['H3C'], model: ['S5560'],
  metrics: [], keywords: ['光模块'], intents: [], source_file: '', content: '',
  created_at: '', updated_at: '', chunk_count: 2,
})

describe('KnowledgeBase', () => {
  it('渲染文档列表', async () => {
    vi.mocked(fetchKbDocs).mockResolvedValue([doc('d1', '交换机手册', 'manual')])
    render(<KnowledgeBase />)
    expect(await screen.findByText('交换机手册')).toBeTruthy()
    expect(screen.getByText(/2 块/)).toBeTruthy()
  })

  it('筛选变更调用 fetchKbDocs', async () => {
    vi.mocked(fetchKbDocs).mockResolvedValue([])
    render(<KnowledgeBase />)
    const q = await screen.findByPlaceholderText('关键词')
    fireEvent.change(q, { target: { value: '光模块' } })
    await waitFor(() => {
      expect(fetchKbDocs).toHaveBeenCalledWith(expect.objectContaining({ q: '光模块' }))
    })
  })

  it('上传成功后刷新列表', async () => {
    vi.mocked(fetchKbDocs).mockResolvedValue([])
    vi.mocked(uploadKbDoc).mockResolvedValue({} as never)
    render(<KnowledgeBase />)
    const file = new File(['x'], 'a.md', { type: 'text/markdown' })
    const input = await screen.findByLabelText('上传文件')
    fireEvent.change(input, { target: { files: [file] } })
    fireEvent.click(screen.getByText('上传'))
    await waitFor(() => expect(uploadKbDoc).toHaveBeenCalled())
  })

  it('删除后调用 deleteKbDoc 并刷新', async () => {
    vi.mocked(fetchKbDocs).mockResolvedValue([doc('d1', '交换机手册', 'manual')])
    vi.mocked(deleteKbDoc).mockResolvedValue({ status: 'ok' })
    render(<KnowledgeBase />)
    await screen.findByText('交换机手册')
    fireEvent.click(screen.getByText('删除'))
    await waitFor(() => expect(deleteKbDoc).toHaveBeenCalledWith('d1'))
  })
})
```

- [ ] **Step 2: 运行测试确认失败**

Run: `npx vitest run src/components/KnowledgeBase.test.tsx`
Expected: FAIL——`Cannot find module './KnowledgeBase'`。

- [ ] **Step 3: 写实现**

`types.ts` 追加：

```ts
export type KbCategory = 'case' | 'manual' | 'runbook' | 'other'

export interface KbDoc {
  id: string
  title: string
  category: KbCategory
  device_types: string[]
  vendor: string[]
  model: string[]
  metrics: string[]
  keywords: string[]
  intents: string[]
  source_file: string
  content: string
  created_at: string
  updated_at: string
}

export interface KbChunk {
  id: string
  doc_id: string
  seq: number
  heading: string
  content: string
  created_at: string
}

export interface KbDocSummary extends KbDoc {
  chunk_count: number
}

export interface KbDocDetail extends KbDoc {
  chunks: KbChunk[]
}

export interface KbDocUpsert {
  title: string
  category: KbCategory
  device_types: string[]
  vendor: string[]
  model: string[]
  metrics: string[]
  keywords: string[]
  intents: string[]
}

export interface KbFilter {
  category?: string
  device_type?: string
  vendor?: string
  model?: string
  intent?: string
  q?: string
}
```

`client.ts` 追加（顶部 import 补 `KbDoc, KbDocDetail, KbDocSummary, KbDocUpsert, KbFilter`；`IntentLeaf` 从 `../types` 引入）：

```ts
export const fetchKbDocs = (f: KbFilter = {}) => {
  const qs = new URLSearchParams()
  if (f.category) qs.set('category', f.category)
  if (f.device_type) qs.set('device_type', f.device_type)
  if (f.vendor) qs.set('vendor', f.vendor)
  if (f.model) qs.set('model', f.model)
  if (f.intent) qs.set('intent', f.intent)
  if (f.q) qs.set('q', f.q)
  const s = qs.toString()
  return req<KbDocSummary[]>(`/kb/docs${s ? `?${s}` : ''}`)
}
export const fetchKbDoc = (id: string) => req<KbDocDetail>(`/kb/docs/${id}`)
export const uploadKbDoc = (form: FormData) =>
  req<KbDoc>('/kb/upload', { method: 'POST', body: form })
export const updateKbDoc = (id: string, body: KbDocUpsert) =>
  req<KbDoc>(`/kb/docs/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
export const deleteKbDoc = (id: string) =>
  req<{ status: string }>(`/kb/docs/${id}`, { method: 'DELETE' })
export const fetchIntentLeaves = () => req<IntentLeaf[]>('/intents/leaves')
```

`types.ts` 追加（`IntentLeaf`，若已有则复用/改在 `client.ts` 内联类型）：

```ts
export interface IntentLeaf {
  code: string
  name: string
  description: string
  rule_keywords: string[]
  path: string
}
```

`App.tsx`：`view` 状态加 `'kb'`；nav 加第三个按钮；`view === 'kb'` 时渲染 `<KnowledgeBase />`（复用 intents 分支的布局）。

新建 `KnowledgeBase.tsx`：

```tsx
import { useCallback, useEffect, useState } from 'react'
import {
  fetchKbDocs, fetchKbDoc, uploadKbDoc, updateKbDoc, deleteKbDoc, fetchIntentLeaves,
} from '../api/client'
import type { KbDocSummary, KbDocDetail, KbDocUpsert, KbFilter, IntentLeaf } from '../types'

const CATEGORIES: { value: string; label: string }[] = [
  { value: '', label: '全部分类' },
  { value: 'case', label: '运维案例' },
  { value: 'manual', label: '产品手册' },
  { value: 'runbook', label: '操作手册' },
  { value: 'other', label: '其他' },
]

function tags(list: string[] | undefined): string {
  return (list ?? []).join(', ')
}

export default function KnowledgeBase() {
  const [docs, setDocs] = useState<KbDocSummary[]>([])
  const [detail, setDetail] = useState<KbDocDetail | null>(null)
  const [intents, setIntents] = useState<IntentLeaf[]>([])
  const [error, setError] = useState('')
  const [filter, setFilter] = useState<KbFilter>({})
  const [uploading, setUploading] = useState(false)
  const [upload, setUpload] = useState({ file: null as File | null, category: '', title: '', vendor: '', model: '', intent: '' })

  const reload = useCallback(async () => {
    try {
      setDocs(await fetchKbDocs(filter))
      setError('')
    } catch (e) {
      setError((e as Error).message)
    }
  }, [filter])

  useEffect(() => { reload() }, [reload])

  useEffect(() => {
    fetchIntentLeaves().then(setIntents).catch(() => {})
  }, [])

  const onUpload = async () => {
    if (!upload.file) { setError('请选择文件'); return }
    const form = new FormData()
    form.append('file', upload.file)
    if (upload.category) form.append('category', upload.category)
    if (upload.title) form.append('title', upload.title)
    if (upload.vendor) form.append('vendor', upload.vendor)
    if (upload.model) form.append('model', upload.model)
    if (upload.intent) form.append('intents', upload.intent)
    setUploading(true)
    try {
      await uploadKbDoc(form)
      setUpload({ file: null, category: '', title: '', vendor: '', model: '', intent: '' })
      setUploading(false)
      reload()
    } catch (e) {
      setUploading(false)
      setError((e as Error).message)
    }
  }

  const onOpen = async (id: string) => {
    try {
      setDetail(await fetchKbDoc(id))
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const onDelete = async (id: string) => {
    try {
      await deleteKbDoc(id)
      setDetail(null)
      reload()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const onSaveMeta = async (id: string, body: KbDocUpsert) => {
    try {
      await updateKbDoc(id, body)
      setDetail(await fetchKbDoc(id))
      reload()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div style={{ padding: 16 }}>
      <h2>知识库</h2>
      {error && <div style={{ color: '#d32f2f', padding: 8 }}>{error}</div>}

      <div style={{ marginBottom: 16, display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        <select aria-label="筛选分类" value={filter.category ?? ''}
          onChange={(e) => setFilter({ ...filter, category: e.target.value || undefined })}>
          {CATEGORIES.map((c) => <option key={c.value} value={c.value}>{c.label}</option>)}
        </select>
        <input placeholder="厂商" aria-label="厂商"
          onChange={(e) => setFilter({ ...filter, vendor: e.target.value || undefined })} />
        <input placeholder="型号" aria-label="型号"
          onChange={(e) => setFilter({ ...filter, model: e.target.value || undefined })} />
        <input placeholder="关键词" aria-label="关键词"
          onChange={(e) => setFilter({ ...filter, q: e.target.value || undefined })} />
      </div>

      <div style={{ marginBottom: 16, padding: 12, border: '1px solid #ddd', borderRadius: 8 }}>
        <div>上传文档（.txt / .md / .docx）</div>
        <input type="file" accept=".txt,.md,.docx" aria-label="上传文件"
          onChange={(e) => setUpload({ ...upload, file: e.target.files?.[0] ?? null })} />
        <select aria-label="上传分类" value={upload.category}
          onChange={(e) => setUpload({ ...upload, category: e.target.value })}>
          <option value="">默认：其他</option>
          <option value="case">运维案例</option>
          <option value="manual">产品手册</option>
          <option value="runbook">操作手册</option>
        </select>
        <input placeholder="标题（留空用文件名）" aria-label="标题"
          value={upload.title} onChange={(e) => setUpload({ ...upload, title: e.target.value })} />
        <input placeholder="厂商（逗号分隔）" aria-label="上传厂商"
          value={upload.vendor} onChange={(e) => setUpload({ ...upload, vendor: e.target.value })} />
        <input placeholder="型号（逗号分隔）" aria-label="上传型号"
          value={upload.model} onChange={(e) => setUpload({ ...upload, model: e.target.value })} />
        <select aria-label="绑定意图" value={upload.intent}
          onChange={(e) => setUpload({ ...upload, intent: e.target.value })}>
          <option value="">不绑定意图</option>
          {intents.map((i) => <option key={i.code} value={i.code}>{i.name || i.code}</option>)}
        </select>
        <button onClick={onUpload} disabled={uploading}>{uploading ? '上传中…' : '上传'}</button>
      </div>

      {detail && (
        <div style={{ marginBottom: 16, padding: 12, border: '1px solid #ccc', borderRadius: 8 }}>
          <h3>{detail.title}</h3>
          <button onClick={() => setDetail(null)}>关闭</button>
          <button onClick={() => onDelete(detail.id)}>删除文档</button>
          <button onClick={() => onSaveMeta(detail.id, {
            title: detail.title, category: detail.category,
            device_types: detail.device_types, vendor: detail.vendor, model: detail.model,
            metrics: detail.metrics, keywords: detail.keywords, intents: detail.intents,
          })}>保存元数据</button>
          <div>标签：{tags(detail.device_types)} / {tags(detail.vendor)} / {tags(detail.model)} / {tags(detail.intents)}</div>
          {detail.chunks.map((c) => (
            <div key={c.id} style={{ marginTop: 8, padding: 8, background: '#f6f6f6' }}>
              {c.heading && <strong>{c.heading}</strong>}
              <div>{c.content}</div>
            </div>
          ))}
        </div>
      )}

      <table style={{ width: '100%', borderCollapse: 'collapse' }}>
        <thead>
          <tr>
            <th>标题</th><th>分类</th><th>设备类型</th><th>厂商</th><th>型号</th><th>块数</th><th>来源</th><th></th>
          </tr>
        </thead>
        <tbody>
          {docs.map((d) => (
            <tr key={d.id} style={{ borderTop: '1px solid #eee' }}>
              <td>{d.title}</td>
              <td>{d.category}</td>
              <td>{tags(d.device_types)}</td>
              <td>{tags(d.vendor)}</td>
              <td>{tags(d.model)}</td>
              <td>{d.chunk_count} 块</td>
              <td>{d.source_file}</td>
              <td>
                <button onClick={() => onOpen(d.id)}>详情</button>
                <button onClick={() => onDelete(d.id)}>删除</button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
```

> 说明：`onSaveMeta` 为演示级元数据保存（回传当前值）；完整编辑弹层（改分类/标签）可后续补，当前满足"上传/列表/筛选/详情/删除"闭环。若 `fetchIntentLeaves` 返回 `IntentNode[]`，用 `{ code: n.code, name: n.name }` 映射。

- [ ] **Step 4: 运行测试确认通过**

Run: `npx vitest run src/components/KnowledgeBase.test.tsx && npm run build`
Expected: 全部 PASS，`tsc -b && vite build` 成功。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/types.ts frontend/src/api/client.ts frontend/src/App.tsx frontend/src/components/KnowledgeBase.tsx frontend/src/components/KnowledgeBase.test.tsx
git commit -m "feat: 前端知识库管理页（列表筛选/上传/详情/删除）"
```

---

## 最终验证

全量验证顺序（全部通过才算完成）：

```bash
cd backend && go build ./... && go test ./...
cd ../frontend && npx vitest run && npm run build
```

同时自查：`mockdata.Docs()` 不再被检索路径引用（仅 `main.go` 种子导入与 `mockdata/docs_test.go` 使用）；`agent`/`pipeline` 的 Query 均注入意图；`AGENTS.md` 无需改动（知识库作为新子系统，架构注释可选补充）。