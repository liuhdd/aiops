package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aiops/internal/model"

	_ "modernc.org/sqlite"
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

func TestKbListFilterLiteralWildcards(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	_ = s.WithKBTx(func(tx KBStore) error {
		_ = tx.InsertKbDocument(model.KbDocument{ID: "d1", Title: "100%故障排查", Category: model.KBManual,
			Vendor: []string{"H3C"}, Content: "含百分号内容", CreatedAt: now, UpdatedAt: now})
		_ = tx.InsertKbDocument(mkDoc("d2", "100kw普通文档", model.KBManual))
		return nil
	})
	// q 含 % 应字面匹配：未转义时 '%100%%' 会把 % 当通配符，命中 d1/d2 两个。
	list, err := s.ListKbDocuments(KbFilter{Q: "100%"})
	if err != nil || len(list) != 1 || list[0].ID != "d1" {
		t.Fatalf("Q 含 %% 应字面匹配: %v %+v", err, list)
	}
	// 下划线同理：S5_60 不匹配 S5560。
	list2, err := s.ListKbDocuments(KbFilter{Model: "S5_60"})
	if err != nil || len(list2) != 0 {
		t.Fatalf("Model 含 _ 应字面匹配: %v %+v", err, list2)
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
func TestKbDocumentFingerprintRoundTrip(t *testing.T) {
	s := newTestStore(t) // 复用既有 helper
	d := mkDoc("dfp", "标题含 Xid 48", model.KBCase)
	d.Fingerprints = []string{"Xid 48", "ALERT-GPU-001"}
	if err := s.WithKBTx(func(tx KBStore) error { return tx.InsertKbDocument(d) }); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetKbDocument("dfp")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Fingerprints) != 2 || got.Fingerprints[0] != "Xid 48" {
		t.Fatalf("fingerprints=%v", got.Fingerprints)
	}
	by, err := s.ListKbDocumentsByFingerprints([]string{"ALERT-GPU-001"})
	if err != nil || len(by) != 1 || by[0].ID != "dfp" {
		t.Fatalf("byFp err=%v by=%+v", err, by)
	}
	by2, _ := s.ListKbDocumentsByFingerprints([]string{"NOT-EXISTS"})
	if len(by2) != 0 {
		t.Fatalf("want empty, got %v", by2)
	}
}
func TestKbDocumentFingerprintLegacyCompatible(t *testing.T) {
	// 用老 schema 建库（不含 fingerprints 列）→ 用新 migrate 打开应自动补列且旧行读回空数组
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")
	db, _ := sql.Open("sqlite", path)
	_, _ = db.Exec(`CREATE TABLE kb_documents (id TEXT PRIMARY KEY, title TEXT NOT NULL, category TEXT NOT NULL, device_types TEXT, vendor TEXT, model TEXT, metrics TEXT, keywords TEXT, intents TEXT, source_file TEXT, content TEXT, created_at DATETIME, updated_at DATETIME)`)
	db.Close()
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err := s.GetKbDocument("missing")
	_ = d
	_ = err // 仅断言 New 不报错
}
func TestKbChunkEnrichColumns(t *testing.T) {
	s := newTestStore(t)
	c := model.KbChunk{ID: "c1", DocID: "d1", Seq: 0, Heading: "h", Content: "增强后", Summary: "摘要", SourceDocIDs: []string{"d1", "d2"}, CreatedAt: time.Now()}
	// 通过事务插入文档与块
	_ = s.WithKBTx(func(tx KBStore) error {
		_ = tx.InsertKbDocument(model.KbDocument{ID: "d1", Title: "t", Category: model.KBOther, Content: "x", CreatedAt: time.Now(), UpdatedAt: time.Now()})
		return tx.InsertKbChunk(c)
	})
	got, _ := s.ListKbChunksByDoc("d1")
	if len(got) != 1 || got[0].Summary != "摘要" || len(got[0].SourceDocIDs) != 2 {
		t.Fatalf("%+v", got)
	}
	// 历史库兼容：无列旧库自动补列
}
func TestKbChunkQuestionsCRUD(t *testing.T) {
	s := newTestStore(t)
	q := model.KbChunkQuestion{ID: "q1", ChunkID: "c1", DocID: "d1", Question: "如何排查 Xid 48？", CreatedAt: time.Now()}
	_ = s.InsertKbChunkQuestion(q)
	list, _ := s.ListKbChunkQuestionsByChunk("c1")
	if len(list) != 1 || list[0].Question != q.Question {
		t.Fatalf("%+v", list)
	}
}

func TestKbChunkNewCols(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	refs := []model.ChunkRef{{Raw: "[[a.md]]", Type: "wikilink", Target: "a.md", Resolved: true}}
	_ = s.WithKBTx(func(tx KBStore) error {
		_ = tx.InsertKbDocument(model.KbDocument{ID: "d1", Title: "t", Category: model.KBOther,
			Version: "3.1.0", VersionMin: "3.0.0", VersionMax: "3.5.0",
			Content: "x", CreatedAt: now, UpdatedAt: now})
		return tx.InsertKbChunk(model.KbChunk{ID: "c1", DocID: "d1", Seq: 0, Heading: "h", Content: "x",
			VersionMin: "3.0.0", VersionMax: "3.5.0", HeadingPath: "标题 > 子标题",
			SourceRefs: refs, IsFlow: true, CreatedAt: now})
	})
	got, err := s.GetKbChunk("c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.VersionMin != "3.0.0" || got.VersionMax != "3.5.0" || got.HeadingPath != "标题 > 子标题" || !got.IsFlow {
		t.Fatalf("新列读回: %+v", got)
	}
	if len(got.SourceRefs) != 1 || got.SourceRefs[0].Target != "a.md" || !got.SourceRefs[0].Resolved {
		t.Fatalf("source_refs=%+v", got.SourceRefs)
	}
	gd, err := s.GetKbDocument("d1")
	if err != nil || gd.Version != "3.1.0" || gd.VersionMin != "3.0.0" || gd.VersionMax != "3.5.0" {
		t.Fatalf("doc 版本列: %+v %v", gd, err)
	}
	// UpdateKbChunk 全列更新：改内容后新列不丢失。
	got.Content, got.Summary = "new", "s"
	if err := s.UpdateKbChunk(got); err != nil {
		t.Fatal(err)
	}
	got2, err := s.GetKbChunk("c1")
	if err != nil || got2.Content != "new" || got2.VersionMin != "3.0.0" || !got2.IsFlow || len(got2.SourceRefs) != 1 {
		t.Fatalf("更新后: %+v %v", got2, err)
	}
}

func TestKbChunkNewColsLegacyCompat(t *testing.T) {
	// 用老 schema 建库（不含版本/引用新列）→ 新 migrate 打开应自动补列，旧行读回新字段为零值。
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE kb_documents (id TEXT PRIMARY KEY, title TEXT NOT NULL, category TEXT NOT NULL, device_types TEXT, vendor TEXT, model TEXT, metrics TEXT, keywords TEXT, intents TEXT, source_file TEXT, content TEXT, created_at DATETIME, updated_at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE kb_chunks (id TEXT PRIMARY KEY, doc_id TEXT NOT NULL, seq INTEGER NOT NULL, heading TEXT NOT NULL DEFAULT '', content TEXT NOT NULL, created_at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO kb_chunks (id, doc_id, seq, heading, content, created_at) VALUES ('c1','d1',0,'h','x',datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// 新列应已补齐。
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM pragma_table_info('kb_chunks') WHERE name IN ('version_min','version_max','heading_path','source_refs','is_flow')`).Scan(&n); err != nil || n != 5 {
		t.Fatalf("kb_chunks 新列数 = %d %v, want 5", n, err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM pragma_table_info('kb_documents') WHERE name IN ('version','version_min','version_max')`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("kb_documents 新列数 = %d %v, want 3", n, err)
	}
	got, err := s.GetKbChunk("c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.VersionMin != "" || got.HeadingPath != "" || got.IsFlow || len(got.SourceRefs) != 0 {
		t.Fatalf("旧行新字段应为零值: %+v", got)
	}
}
