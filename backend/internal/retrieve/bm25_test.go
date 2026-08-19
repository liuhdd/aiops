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
