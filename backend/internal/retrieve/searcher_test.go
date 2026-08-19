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
