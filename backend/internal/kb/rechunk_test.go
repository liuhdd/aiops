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

// TestRechunkPreservesFrontmatterOnlyVersion 回归：仅 frontmatter 声明版本的文档，
// Upload 落库时正文已无 frontmatter，重切不得静默清空版本（Version/VersionMin/VersionMax 保持）。
func TestRechunkPreservesFrontmatterOnlyVersion(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	svc := NewService(st, nil)
	ctx := context.Background()

	raw := "---\nversion: \">=2.0\"\n---\n# 升级指南\n\n第一段正文，描述升级前的检查事项。\n\n第二段正文，描述升级后的验证步骤。\n"
	doc, err := svc.Upload(ctx, UploadReq{
		SourceFile: "fm.md", Data: []byte(raw), Category: model.KBManual,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if doc.Version != ">=2.0" || doc.VersionMin != "2.0.0" || doc.VersionMax != "" {
		t.Fatalf("上传落库版本错误, got %+v", doc)
	}
	stat, err := svc.RechunkDoc(ctx, doc.ID)
	if err != nil {
		t.Fatalf("RechunkDoc: %v", err)
	}
	if stat.Skipped {
		t.Fatalf("正常文档不应跳过, stat = %+v", stat)
	}
	got, err := st.GetKbDocument(doc.ID)
	if err != nil {
		t.Fatalf("GetKbDocument: %v", err)
	}
	if got.Version != ">=2.0" || got.VersionMin != "2.0.0" || got.VersionMax != "" {
		t.Fatalf("重切后文档版本应保留, got %+v", got)
	}
	chunks, err := st.ListKbChunksByDoc(doc.ID)
	if err != nil || len(chunks) == 0 {
		t.Fatalf("重切后应有块, n=%d err=%v", len(chunks), err)
	}
	for _, c := range chunks {
		if c.VersionMin != "2.0.0" || c.VersionMax != "" {
			t.Fatalf("块版本应继承文档级下界, chunk = %+v", c)
		}
	}
}

// TestRechunkTwiceStable 回归：同一文档连续重切两轮，版本与引用边保持稳定。
func TestRechunkTwiceStable(t *testing.T) {
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
	raw := "---\nversion: \">=2.0\"\n---\n# 主文档\n\n正文嵌入 ![[target]] 结束。\n\n第二段正文内容。\n"
	doc, err := svc.Upload(ctx, UploadReq{
		SourceFile: "main.md", Data: []byte(raw), Category: model.KBManual,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	snap := func() (model.KbDocument, []model.KbChunk, int) {
		d, err := st.GetKbDocument(doc.ID)
		if err != nil {
			t.Fatalf("GetKbDocument: %v", err)
		}
		chunks, err := st.ListKbChunksByDoc(doc.ID)
		if err != nil {
			t.Fatalf("ListKbChunksByDoc: %v", err)
		}
		edges := 0
		for _, c := range chunks {
			rows, err := st.ListKbChunkRefsByChunk(c.ID)
			if err != nil {
				t.Fatalf("ListKbChunkRefsByChunk: %v", err)
			}
			edges += len(rows)
		}
		return d, chunks, edges
	}
	if _, err := svc.RechunkDoc(ctx, doc.ID); err != nil {
		t.Fatalf("首轮 RechunkDoc: %v", err)
	}
	d1, c1, e1 := snap()
	if d1.VersionMin == "" {
		t.Fatalf("首轮后文档版本不应为空, got %+v", d1)
	}
	if e1 == 0 {
		t.Fatalf("首轮后引用边不应丢失（目标 %s）", target.ID)
	}
	second, err := svc.RechunkDoc(ctx, doc.ID)
	if err != nil {
		t.Fatalf("次轮 RechunkDoc: %v", err)
	}
	if second.Changed {
		t.Fatalf("次轮重切应无差异, stat = %+v", second)
	}
	d2, c2, e2 := snap()
	if d2.Version != d1.Version || d2.VersionMin != d1.VersionMin || d2.VersionMax != d1.VersionMax {
		t.Fatalf("两轮文档版本应一致, first=%+v second=%+v", d1, d2)
	}
	if len(c2) != len(c1) {
		t.Fatalf("两轮块数应一致, first=%d second=%d", len(c1), len(c2))
	}
	for i := range c1 {
		if c2[i].Seq != c1[i].Seq || c2[i].Heading != c1[i].Heading ||
			c2[i].HeadingPath != c1[i].HeadingPath || c2[i].Content != c1[i].Content ||
			c2[i].VersionMin != c1[i].VersionMin || c2[i].VersionMax != c1[i].VersionMax ||
			c2[i].IsFlow != c1[i].IsFlow {
			t.Fatalf("两轮块内容应一致, first=%+v second=%+v", c1[i], c2[i])
		}
	}
	if e2 != e1 {
		t.Fatalf("两轮引用边数应一致, first=%d second=%d", e1, e2)
	}
}
