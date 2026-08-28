package kb

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"aiops/internal/config"
	"aiops/internal/model"
	"aiops/internal/store"
)

type fakeEnrichCompleter struct {
	summary   string
	questions string
}

func (f fakeEnrichCompleter) Complete(_ context.Context, p string) (string, error) {
	if strings.Contains(p, "概括") {
		return f.summary, nil
	}
	return f.questions, nil
}

type fakeEmbedder struct {
	vec []float64
}

func (f *fakeEmbedder) Model() string { return "fake" }
func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i := range texts {
		// 返回固定向量，模拟相似度 1
		if f.vec != nil {
			cp := make([]float64, len(f.vec))
			copy(cp, f.vec)
			out[i] = cp
		} else {
			out[i] = []float64{1, 0}
		}
	}
	return out, nil
}

func newTestStoreEnrich(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestUploadWithEnrich(t *testing.T) {
	s := newTestStoreEnrich(t)
	lc := fakeEnrichCompleter{summary: "摘要句", questions: "如何排查？\n如何恢复？"}
	svc := NewServiceWithEnrich(s, nil, &fakeEmbedder{vec: []float64{1, 0}}, lc, config.Load())
	doc, err := svc.Upload(context.Background(), UploadReq{SourceFile: "a.md", Data: []byte("# 标题\nGPU 掉卡 Xid 48 内容"), Title: "GPU 掉卡"})
	if err != nil {
		t.Fatal(err)
	}
	chunks, _ := s.ListKbChunksByDoc(doc.ID)
	if len(chunks) == 0 || chunks[0].Summary != "摘要句" {
		t.Fatalf("want summary 摘要句 got %+v", chunks)
	}
	qs, _ := s.ListKbChunkQuestionsByChunk(chunks[0].ID)
	if len(qs) != 2 {
		t.Fatalf("questions %v", qs)
	}
	embs, _ := s.ListKbEmbeddings()
	// 至少包含块向量 + 2 问题向量
	if len(embs) < len(chunks)+2 {
		t.Fatalf("embeddings %v len=%d want >=%d", embs, len(embs), len(chunks)+2)
	}
	// 验证问题向量以 q_ 前缀存储
	foundQ := 0
	for _, e := range embs {
		if strings.HasPrefix(e.ChunkID, "q_") {
			foundQ++
		}
	}
	if foundQ < 2 {
		t.Fatalf("q embeddings not found %v", embs)
	}
}

func TestUploadCDTAMerge(t *testing.T) {
	s := newTestStoreEnrich(t)
	lc := fakeEnrichCompleter{summary: "s", questions: "q1"}
	svc := NewServiceWithEnrich(s, nil, &fakeEmbedder{vec: []float64{1, 0}}, lc, config.Load())
	if err := svc.Seed([]SeedDoc{{Title: "t1", Content: "GPU Xid 48 掉卡详细内容块A", Category: model.KBOther}}); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	// 记录合并前块数
	kcsBefore, _ := s.ListKnowledgeChunks()
	before := len(kcsBefore)
	if before == 0 {
		t.Fatalf("seed 后应有块")
	}
	// 获取首个文档ID用于后续校验 source_doc_ids
	var firstDocID string
	if docs, _ := s.ListKbDocuments(store.KbFilter{}); len(docs) > 0 {
		firstDocID = docs[0].ID
	}
	_, err := svc.Upload(context.Background(), UploadReq{SourceFile: "b.md", Data: []byte("GPU Xid 48 掉卡详细内容块A 变体"), Title: "t2"})
	if err != nil {
		t.Fatal(err)
	}
	kcsAfter, _ := s.ListKnowledgeChunks()
	after := len(kcsAfter)
	// 余弦 1 > 0.85 应合并，块数不应增加（或仅微增但 source_doc_ids 含两文档）
	if after != before {
		// 若块数增加则认为未合并，失败
		t.Fatalf("CDTA 未合并: before %d after %d", before, after)
	}
	// 检查 source_doc_ids 是否包含两个文档
	// 取任意块，其 SourceDocIDs 应含 firstDocID 和新上传文档ID
	// 找到合并后的块
	found := false
	for _, kc := range kcsAfter {
		// 通过 ListKbChunksByDoc 获取详细 SourceDocIDs（ListKnowledgeChunks 未包含该字段，需查块表）
		_ = kc
	}
	// 直接查所有块的 source_doc_ids
	// 由于 ListKnowledgeChunks 不返回 SourceDocIDs，改查底层
	// 遍历所有文档的块
	docs, _ := s.ListKbDocuments(store.KbFilter{})
	for _, d := range docs {
		chunks, _ := s.ListKbChunksByDoc(d.ID)
		for _, ch := range chunks {
			if len(ch.SourceDocIDs) >= 2 {
				// 至少包含 firstDocID 和另一个
				hasFirst := false
				for _, id := range ch.SourceDocIDs {
					if id == firstDocID {
						hasFirst = true
					}
				}
				if hasFirst {
					found = true
					// 内容应包含合并分隔符
					if !strings.Contains(ch.Content, "---") {
						t.Fatalf("合并后 content 未拼接, got %q", ch.Content)
					}
				}
			}
		}
	}
	if !found {
		t.Fatalf("source_doc_ids 未合并含两文档，before %d after %d first %s docs %v", before, after, firstDocID, docs)
	}
}

func TestUploadFallbackNoLLM(t *testing.T) {
	s := newTestStoreEnrich(t)
	svc := NewService(s, nil) // lc=nil
	doc, err := svc.Upload(context.Background(), UploadReq{SourceFile: "a.md", Data: []byte("内容"), Title: "t"})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	chunks, _ := s.ListKbChunksByDoc(doc.ID)
	if len(chunks) == 0 {
		t.Fatalf("no chunks")
	}
	if chunks[0].Summary != "" {
		t.Fatalf("should fallback, got summary %q", chunks[0].Summary)
	}
	qs, _ := s.ListKbChunkQuestionsByChunk(chunks[0].ID)
	if len(qs) != 0 {
		t.Fatalf("should no questions, got %v", qs)
	}
}
