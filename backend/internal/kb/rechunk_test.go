package kb

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aiops/internal/model"
	"aiops/internal/store"
)

// insertLegacyDoc 直写旧管线形态的文档行与块（无版本/标题路径/引用/流程标记）。
func insertLegacyDoc(t *testing.T, st *store.Store, title, content string) string {
	t.Helper()
	docID := model.NewID("kbdoc")
	now := time.Now()
	err := st.WithKBTx(func(tx store.KBStore) error {
		if err := tx.InsertKbDocument(model.KbDocument{
			ID: docID, Title: title, Category: model.KBManual,
			Content: content, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		return tx.InsertKbChunk(model.KbChunk{
			ID: model.NewID("kbchunk"), DocID: docID, Seq: 0,
			Heading: "旧块", Content: content, CreatedAt: now,
		})
	})
	if err != nil {
		t.Fatalf("直写旧文档: %v", err)
	}
	return docID
}

func TestRechunkUpgradesLegacyDoc(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	svc := NewService(st, nil)
	ctx := context.Background()

	target, err := svc.Upload(ctx, UploadReq{
		SourceFile: "target.md", Data: []byte("# 目标\n目标正文"),
		Category: model.KBManual,
	})
	if err != nil {
		t.Fatalf("Upload target: %v", err)
	}
	legacyContent := "# 升级\n@version >=2.0 起可用\n\n步骤如下：\n\n1. 第一步\n2. 第二步\n\n详见 ![[target]] 。\n"
	legacyID := insertLegacyDoc(t, st, "待升级", legacyContent)

	stat, err := svc.RechunkDoc(ctx, legacyID)
	if err != nil {
		t.Fatalf("RechunkDoc: %v", err)
	}
	if stat.Skipped || stat.OldChunks != 1 || stat.NewChunks < 1 {
		t.Fatalf("stat = %+v", stat)
	}
	if !stat.Changed {
		t.Fatalf("旧单块→新管线应有差异, stat = %+v", stat)
	}
	doc, err := st.GetKbDocument(legacyID)
	if err != nil {
		t.Fatalf("GetKbDocument: %v", err)
	}
	if doc.VersionMin == "" {
		t.Fatalf("文档版本下界应回填, got %+v", doc)
	}
	if doc.Fingerprints == nil {
		t.Fatalf("文档指纹应回填")
	}
	chunks, err := st.ListKbChunksByDoc(legacyID)
	if err != nil || len(chunks) != stat.NewChunks {
		t.Fatalf("chunks = %d %+v err=%v", len(chunks), stat, err)
	}
	flow := 0
	for _, c := range chunks {
		if c.HeadingPath == "" {
			t.Fatalf("标题路径应回填, chunk = %+v", c)
		}
		if c.VersionMin == "" {
			t.Fatalf("块版本应继承文档级, chunk = %+v", c)
		}
		if c.IsFlow {
			flow++
		}
	}
	if flow == 0 {
		t.Fatalf("有序步骤节应产出流程块")
	}
	// 引用边：embed 应解析到目标文档。
	found := false
	for _, c := range chunks {
		rows, err := st.ListKbChunkRefsByChunk(c.ID)
		if err != nil {
			t.Fatalf("ListKbChunkRefsByChunk: %v", err)
		}
		for _, r := range rows {
			if r.TargetDocID == target.ID && r.Resolved {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("embed 引用边应解析到目标文档 %s", target.ID)
	}
}

func TestRechunkPreservesEmbedEdges(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	svc := NewService(st, nil)
	ctx := context.Background()

	target, err := svc.Upload(ctx, UploadReq{
		SourceFile: "target.md", Data: []byte("# 目标\n目标正文"),
		Category: model.KBManual,
	})
	if err != nil {
		t.Fatalf("Upload target: %v", err)
	}
	doc, err := svc.Upload(ctx, UploadReq{
		SourceFile: "a.md", Data: []byte("# 主文档\n正文嵌入 ![[target]] 结束。\n"),
		Category: model.KBManual,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	countEdges := func() int {
		n := 0
		chunks, _ := st.ListKbChunksByDoc(doc.ID)
		for _, c := range chunks {
			rows, _ := st.ListKbChunkRefsByChunk(c.ID)
			n += len(rows)
		}
		return n
	}
	before := countEdges()
	if before == 0 {
		t.Fatalf("上传时应已建引用边")
	}
	if _, err := svc.RechunkDoc(ctx, doc.ID); err != nil {
		t.Fatalf("RechunkDoc: %v", err)
	}
	if got := countEdges(); got != before {
		t.Fatalf("重切前后边数应一致 before=%d got=%d", before, got)
	}
	// 目标仍可解析（已内联正文重切不丢 embed 引用）。
	chunks, _ := st.ListKbChunksByDoc(doc.ID)
	resolved := false
	for _, c := range chunks {
		rows, _ := st.ListKbChunkRefsByChunk(c.ID)
		for _, r := range rows {
			if r.TargetDocID == target.ID && r.Resolved {
				resolved = true
			}
		}
	}
	if !resolved {
		t.Fatalf("重切后 embed 边应仍解析到 %s", target.ID)
	}
}

func TestPreviewRechunkReadOnly(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	svc := NewService(st, nil)
	ctx := context.Background()

	doc, err := svc.Upload(ctx, UploadReq{
		SourceFile: "a.md", Data: []byte("# 标题\n第一段。\n\n第二段。\n"),
		Category: model.KBManual,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	snap := func() string {
		chunks, _ := st.ListKbChunksByDoc(doc.ID)
		var b strings.Builder
		for _, c := range chunks {
			b.WriteString(c.ID + "|" + c.Content + "\n")
		}
		d, _ := st.GetKbDocument(doc.ID)
		b.WriteString(d.Content + d.UpdatedAt.String())
		return b.String()
	}
	before := snap()
	stat, err := svc.PreviewRechunk(ctx, doc.ID)
	if err != nil {
		t.Fatalf("PreviewRechunk: %v", err)
	}
	if stat.Skipped {
		t.Fatalf("正常文档不应跳过")
	}
	if got := snap(); got != before {
		t.Fatalf("dry-run 不应写库")
	}
}

func TestRechunkNotFound(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	if _, err := svc.RechunkDoc(ctx, "kbdoc_missing"); err != ErrNotFound {
		t.Fatalf("RechunkDoc 应返回 ErrNotFound, got %v", err)
	}
	if _, err := svc.PreviewRechunk(ctx, "kbdoc_missing"); err != ErrNotFound {
		t.Fatalf("PreviewRechunk 应返回 ErrNotFound, got %v", err)
	}
}

func TestRechunkSkipsEmptyContent(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	svc := NewService(st, nil)
	ctx := context.Background()

	docID := model.NewID("kbdoc")
	now := time.Now()
	if err := st.WithKBTx(func(tx store.KBStore) error {
		return tx.InsertKbDocument(model.KbDocument{
			ID: docID, Title: "空文档", Category: model.KBManual,
			CreatedAt: now, UpdatedAt: now,
		})
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	stat, err := svc.RechunkDoc(ctx, docID)
	if err != nil {
		t.Fatalf("RechunkDoc: %v", err)
	}
	if !stat.Skipped {
		t.Fatalf("空内容应跳过, stat = %+v", stat)
	}
}
