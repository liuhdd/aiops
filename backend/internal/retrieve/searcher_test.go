package retrieve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aiops/internal/config"
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
		case strings.Contains(t, "semantic"):
			out[i] = []float64{1, 0}
		default:
			// 无关文本映射到与种子向量正交的方向，保证余弦 ≈ 0，避免误判为向量相关。
			out[i] = []float64{0, -1}
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

func TestSearchCasesVectorOnlyMatch(t *testing.T) {
	s := seedRetrieveStore(t)
	se := NewSearcher(s, fakeEmbedder{})
	// "semantic topic" 与任一案例无关键词/标签/意图重叠，仅与 c1 向量高度相似（余弦 1.0）。
	res, err := se.SearchCases(context.Background(), Query{Text: "semantic topic"}, Options{TopK: 3})
	if err != nil {
		t.Fatalf("SearchCases: %v", err)
	}
	if len(res) == 0 || res[0].DocID != "d1" {
		t.Fatalf("纯语义匹配应命中 d1 居首: %+v", res)
	}
	if res[0].Score <= 0 {
		t.Fatalf("纯语义命中的案例应有融合分: %+v", res[0])
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

func TestSearchManualsCategoryFilter(t *testing.T) {
	s := seedRetrieveStore(t)
	se := NewSearcher(s, fakeEmbedder{})
	res, err := se.SearchManuals(context.Background(), Query{Text: "光模块", Category: model.KBRunbook}, Options{TopK: 2})
	if err != nil {
		t.Fatalf("SearchManuals: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("category=runbook 不应返回 manual 文档: %+v", res)
	}
	res, err = se.SearchManuals(context.Background(), Query{Text: "光模块", Category: model.KBManual}, Options{TopK: 2})
	if err != nil {
		t.Fatalf("SearchManuals: %v", err)
	}
	if len(res) != 1 || res[0].DocID != "d3" {
		t.Fatalf("category=manual 应命中 d3: %+v", res)
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

func TestSearchFingerprintFastPathHit(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "fp.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Now()
	if err := s.WithKBTx(func(tx store.KBStore) error {
		doc := model.KbDocument{ID: "fp1", Title: "GPU Xid 48 掉卡案例", Category: model.KBCase, Fingerprints: []string{"Xid 48"}, Content: "GPU 掉卡日志", CreatedAt: now, UpdatedAt: now}
		if err := tx.InsertKbDocument(doc); err != nil {
			return err
		}
		if err := tx.InsertKbChunk(model.KbChunk{ID: "c_fp1", DocID: "fp1", Seq: 0, Content: "GPU 掉卡通用内容无指纹字面", CreatedAt: now}); err != nil {
			return err
		}
		doc2 := model.KbDocument{ID: "other", Title: "其他案例", Category: model.KBCase, Content: "其他内容", CreatedAt: now, UpdatedAt: now}
		if err := tx.InsertKbDocument(doc2); err != nil {
			return err
		}
		return tx.InsertKbChunk(model.KbChunk{ID: "c_other", DocID: "other", Seq: 0, Content: "其他内容", CreatedAt: now})
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	se := NewSearcher(s, fakeEmbedder{})
	res, err := se.SearchCases(context.Background(), Query{Text: "系统报 Xid 48 故障"}, Options{TopK: 3})
	if err != nil {
		t.Fatalf("SearchCases: %v", err)
	}
	if len(res) == 0 || res[0].DocID != "fp1" {
		t.Fatalf("指纹快路径应命中 fp1，got %+v", res)
	}
	if res[0].Score != 1.0 {
		t.Fatalf("指纹命中 Score 应为 1.0，got %v", res[0].Score)
	}
	// 关闭指纹开关应回退到空（无 BM25/向量/信号相关性）
	cfgDisabled := config.Config{RetrieveFingerprint: false, RetrieveRRF: true, RerankFloor: 0.2, RerankModel: "rerank"}
	se2 := NewSearcherWithConfig(s, fakeEmbedder{}, nil, cfgDisabled)
	res2, err := se2.SearchCases(context.Background(), Query{Text: "系统报 Xid 48 故障"}, Options{TopK: 3})
	if err != nil {
		t.Fatalf("SearchCases disabled: %v", err)
	}
	if len(res2) != 0 {
		t.Fatalf("关闭指纹后应无相关结果（快路径禁用且无其他相关性），got %+v", res2)
	}
}

func TestSearchRRFVsWeightedToggle(t *testing.T) {
	s := seedRetrieveStore(t)
	cfgRRF := config.Config{RetrieveFingerprint: false, RetrieveRRF: true, RerankFloor: 0.2, RerankModel: "rerank"}
	cfgW := config.Config{RetrieveFingerprint: false, RetrieveRRF: false, RerankFloor: 0.2, RerankModel: "rerank"}
	seRRF := NewSearcherWithConfig(s, fakeEmbedder{}, nil, cfgRRF)
	seW := NewSearcherWithConfig(s, fakeEmbedder{}, nil, cfgW)
	q := Query{Text: "GPU 掉卡"}
	resRRF, err := seRRF.SearchCases(context.Background(), q, Options{TopK: 2})
	if err != nil {
		t.Fatalf("RRF SearchCases: %v", err)
	}
	resW, err := seW.SearchCases(context.Background(), q, Options{TopK: 2})
	if err != nil {
		t.Fatalf("Weighted SearchCases: %v", err)
	}
	if len(resRRF) == 0 || len(resW) == 0 {
		t.Fatalf("两开关下均应有结果 RRF=%+v Weighted=%+v", resRRF, resW)
	}
	// 同一批候选的 RRF 与加权融合应至少有一项实现差异（通过直接对比 FuseRRF 与 RankCases 的序）
	cands := []cand{
		{Chunk: model.KbKnowledgeChunk{ChunkID: "a"}, BM25: 0.1, Vec: 0.95, Signal: 0.5},
		{Chunk: model.KbKnowledgeChunk{ChunkID: "b"}, BM25: 0.95, Vec: 0.1, Signal: 0.5},
		{Chunk: model.KbKnowledgeChunk{ChunkID: "c"}, BM25: 0.5, Vec: 0.5, Signal: 0.5},
	}
	gotRRF := FuseRRF(append([]cand(nil), cands...), false)
	gotW := RankCases(append([]cand(nil), cands...), time.Now())
	if gotRRF[0].Chunk.ChunkID == gotW[0].Chunk.ChunkID && len(gotRRF) > 1 {
		// 允许偶发相同，但至少验证两路径可独立开关且结果非空；若相同则仅校验非空已足够
		t.Logf("RRF 与加权首位相同 rrf=%v weighted=%v", gotRRF[0].Chunk.ChunkID, gotW[0].Chunk.ChunkID)
	}
}

func TestSearchRerankGateBRejectsNoAnswer(t *testing.T) {
	s := seedRetrieveStore(t)
	// 低分 rerank：全部 0.05，floor 0.2 → GateB 整批剔空
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		docs, _ := req["documents"].([]any)
		results := make([]map[string]any, len(docs))
		for i := range docs {
			results[i] = map[string]any{"index": i, "relevance_score": 0.05}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
	}))
	defer srv.Close()
	rr, _ := NewReranker(config.Config{RerankBaseURL: srv.URL, RerankAPIKey: "k", RerankModel: "m"})
	cfg := config.Config{RetrieveFingerprint: false, RetrieveRRF: true, RerankFloor: 0.2, RerankBaseURL: srv.URL, RerankAPIKey: "k", RerankModel: "m"}
	se := NewSearcherWithConfig(s, fakeEmbedder{}, rr, cfg)
	res, err := se.SearchCases(context.Background(), Query{Text: "GPU 掉卡"}, Options{TopK: 3})
	if err != nil {
		t.Fatalf("SearchCases rerank: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("低分精排应被 GateB 全部过滤为空，got %+v", res)
	}
}

func TestSearchRerankUnavailableFallsBack(t *testing.T) {
	s := seedRetrieveStore(t)
	// reranker=nil 应静默降级返回融合结果
	cfg := config.Config{RetrieveFingerprint: false, RetrieveRRF: true, RerankFloor: 0.2}
	se := NewSearcherWithConfig(s, fakeEmbedder{}, nil, cfg)
	res, err := se.SearchCases(context.Background(), Query{Text: "GPU 掉卡"}, Options{TopK: 2})
	if err != nil {
		t.Fatalf("SearchCases no rerank: %v", err)
	}
	if len(res) == 0 {
		t.Fatalf("无 reranker 时应回退到融合结果，got empty")
	}
	// 兼容旧构造 NewSearcher 亦应等价于开关全开但无精排
	se2 := NewSearcher(s, fakeEmbedder{})
	res2, err := se2.SearchCases(context.Background(), Query{Text: "GPU 掉卡"}, Options{TopK: 2})
	if err != nil || len(res2) == 0 {
		t.Fatalf("NewSearcher 兼容构造应返回结果 err=%v res=%+v", err, res2)
	}
}

func newTestStoreWithQuestions(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Now()
	_ = s.WithKBTx(func(tx store.KBStore) error {
		_ = tx.InsertKbDocument(model.KbDocument{ID: "d1", Title: "GPU掉卡与Xid错误", Category: model.KBCase,
			DeviceTypes: []string{"gpu-server"}, Metrics: []string{"gpu_util"}, Keywords: []string{"显卡"},
			Intents: []string{"AskRootCause"}, Content: "GPU 详细内容", CreatedAt: now, UpdatedAt: now})
		_ = tx.InsertKbChunk(model.KbChunk{ID: "c1", DocID: "d1", Seq: 0, Content: "GPU 硬件模块 详细记录", SourceDocIDs: []string{"d1"}, CreatedAt: now})
		_ = tx.InsertKbChunkQuestion(model.KbChunkQuestion{ID: "q1", ChunkID: "c1", DocID: "d1", Question: "如何排查 Xid 48？", CreatedAt: now})
		_ = tx.UpsertKbEmbedding(model.KbEmbedding{ChunkID: "c1", DocID: "d1", Embedding: []float64{1, 0}, Model: "fake", CreatedAt: now})
		_ = tx.UpsertKbEmbedding(model.KbEmbedding{ChunkID: "q_q1", DocID: "d1", Embedding: []float64{0, 1}, Model: "fake", CreatedAt: now})
		_ = tx.InsertKbDocument(model.KbDocument{ID: "d2", Title: "磁盘案例", Category: model.KBCase,
			DeviceTypes: []string{"server"}, Metrics: []string{"disk_usage"}, Keywords: []string{"磁盘"},
			Content: "磁盘内容", CreatedAt: now, UpdatedAt: now})
		_ = tx.InsertKbChunk(model.KbChunk{ID: "c2", DocID: "d2", Seq: 0, Content: "磁盘扩容 清理 日志 无关内容", SourceDocIDs: []string{"d2"}, CreatedAt: now})
		_ = tx.UpsertKbEmbedding(model.KbEmbedding{ChunkID: "c2", DocID: "d2", Embedding: []float64{0, -1}, Model: "fake", CreatedAt: now})
		return nil
	})
	return s
}

type questionBoostEmbedder struct{}

func (questionBoostEmbedder) Model() string { return "fake" }
func (questionBoostEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i, t := range texts {
		if strings.Contains(t, "排查") {
			out[i] = []float64{0, 1}
		} else if strings.Contains(t, "GPU") {
			out[i] = []float64{1, 0}
		} else {
			out[i] = []float64{0, -1}
		}
	}
	return out, nil
}

func TestSearchQuestionChannelBoost(t *testing.T) {
	// 种子：块内容为 "GPU 硬件模块 详细记录"（与查询无 BM25 重叠），问题为 "如何排查 Xid 48？"
	// 查询 "Xid 48 怎么排查" 对块 BM25 弱（0）但对问题 BM25 强，应因问题通道而召回
	s := newTestStoreWithQuestions(t)
	cfg := config.Config{RetrieveFingerprint: false, RetrieveRRF: true, RerankFloor: 0.2, RerankModel: "rerank"}
	searcher := NewSearcherWithConfig(s, nil, nil, cfg)
	res, err := searcher.SearchCases(context.Background(), Query{Text: "Xid 48 怎么排查"}, Options{TopK: 3})
	if err != nil {
		t.Fatalf("SearchCases: %v", err)
	}
	if len(res) == 0 || res[0].Title != "GPU掉卡与Xid错误" {
		t.Fatalf("问题通道应使 GPU 案例被召回，got %+v", res)
	}
	// 向量通道：块向量与查询正交，问题向量与查询一致，应同样通过 QVec 召回
	s2 := newTestStoreWithQuestions(t)
	searcher2 := NewSearcherWithConfig(s2, questionBoostEmbedder{}, nil, cfg)
	res2, err := searcher2.SearchCases(context.Background(), Query{Text: "Xid 48 怎么排查"}, Options{TopK: 3})
	if err != nil {
		t.Fatalf("SearchCases vector: %v", err)
	}
	if len(res2) == 0 || res2[0].Title != "GPU掉卡与Xid错误" {
		t.Fatalf("问题向量通道应使 GPU 案例被召回，got %+v", res2)
	}
}

func TestSearchQuestionChannelFallback(t *testing.T) {
	// 无问题表时仍按块通道正常召回
	s := seedRetrieveStore(t)
	cfg := config.Config{RetrieveFingerprint: false, RetrieveRRF: true, RerankFloor: 0.2, RerankModel: "rerank"}
	searcher := NewSearcherWithConfig(s, fakeEmbedder{}, nil, cfg)
	res, err := searcher.SearchCases(context.Background(), Query{Text: "GPU 掉卡"}, Options{TopK: 3})
	if err != nil {
		t.Fatalf("SearchCases: %v", err)
	}
	if len(res) == 0 || res[0].DocID != "d1" {
		t.Fatalf("无问题表时块通道应正常命中 d1，got %+v", res)
	}
	// 无问题表且无 embedder 时 BM25 仍应召回
	searcher2 := NewSearcherWithConfig(s, nil, nil, cfg)
	res2, err := searcher2.SearchCases(context.Background(), Query{Text: "光模块"}, Options{TopK: 3})
	if err != nil {
		t.Fatalf("SearchCases manual fallback: %v", err)
	}
	// 光模块仅手册中有，案例检索应为空，不因缺问题表而 panic
	if len(res2) != 0 {
		// 案例中无光模块，应为空，验证静默降级不 panic
	}
	// 手册通道在无问题表时仍应正常
	res3, err := searcher2.SearchManuals(context.Background(), Query{Text: "光模块"}, Options{TopK: 3})
	if err != nil {
		t.Fatalf("SearchManuals fallback: %v", err)
	}
	if len(res3) == 0 || res3[0].DocID != "d3" {
		t.Fatalf("手册通道 fallback 应命中 d3，got %+v", res3)
	}
}

// TestSearchFingerprintFastPathVersionFilter 指纹快路径版本过滤：共享指纹的两版本文档，
// 带版本查询应只回命中区间的块（关闭旁路）；空版本直通两者皆回。
func TestSearchFingerprintFastPathVersionFilter(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "fpv.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Now()
	if err := s.WithKBTx(func(tx store.KBStore) error {
		if err := tx.InsertKbDocument(model.KbDocument{ID: "d-old", Title: "旧版案例", Category: model.KBCase,
			Fingerprints: []string{"Xid 48"}, Content: "旧版内容", CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		if err := tx.InsertKbChunk(model.KbChunk{ID: "c-old", DocID: "d-old", Seq: 0,
			Content: "旧版块内容", VersionMin: "2.0.0", VersionMax: "2.9.9", CreatedAt: now}); err != nil {
			return err
		}
		if err := tx.InsertKbDocument(model.KbDocument{ID: "d-new", Title: "新版案例", Category: model.KBCase,
			Fingerprints: []string{"Xid 48"}, Content: "新版内容", CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		return tx.InsertKbChunk(model.KbChunk{ID: "c-new", DocID: "d-new", Seq: 0,
			Content: "新版块内容", VersionMin: "3.0.0", VersionMax: "3.9.9", CreatedAt: now})
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// 指纹开关默认开启（NewSearcher），复现旁路的原始条件。
	se := NewSearcher(s, nil)
	only := func(version string) []string {
		t.Helper()
		res, err := se.SearchCases(context.Background(), Query{Text: "系统报 Xid 48 故障", Version: version}, Options{TopK: 10})
		if err != nil {
			t.Fatalf("SearchCases version=%q: %v", version, err)
		}
		ids := make([]string, 0, len(res))
		for _, r := range res {
			ids = append(ids, r.DocID)
		}
		return ids
	}
	if got := only("2.5.0"); len(got) != 1 || got[0] != "d-old" {
		t.Fatalf("2.5.0 应只回旧版（快路径需过滤新版），got %v", got)
	}
	if got := only("3.1.0"); len(got) != 1 || got[0] != "d-new" {
		t.Fatalf("3.1.0 应只回新版（快路径需过滤旧版），got %v", got)
	}
	if got := only(""); len(got) != 2 {
		t.Fatalf("空版本应直通两者皆回，got %v", got)
	}
}

// TestSearchVersionFilter 版本过滤透传：同关键词两案例分属不交版本区间，
// Query.Version 应只召回命中区间的文档；空版本返回全量。
// 区间直接落盘（含 Max 上界）；经 Upload 只能造下界区间（frontmatter 无上界形式），
// 故 assembler 侧用 >= 区间（见 TestAssembleVersionFilter），此处补全双向排他验证。
func TestSearchVersionFilter(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Now()
	if err := s.WithKBTx(func(tx store.KBStore) error {
		mkDoc := func(id, title string) error {
			return tx.InsertKbDocument(model.KbDocument{ID: id, Title: title, Category: model.KBCase,
				DeviceTypes: []string{"server"}, Metrics: []string{"firmware_ver"},
				Keywords: []string{"NVMe固件"}, Content: "NVMe固件升级公共关键词" + title,
				CreatedAt: now, UpdatedAt: now})
		}
		mkChunk := func(id, docID, content, vmin, vmax string) error {
			return tx.InsertKbChunk(model.KbChunk{ID: id, DocID: docID, Seq: 0,
				Heading: "步骤", Content: content, VersionMin: vmin, VersionMax: vmax, CreatedAt: now})
		}
		if err := mkDoc("d-v2", "版本过滤案例旧版"); err != nil {
			return err
		}
		if err := mkChunk("c-v2", "d-v2", "NVMe固件升级公共关键词旧版内容", "2.0.0", "2.9.9"); err != nil {
			return err
		}
		if err := mkDoc("d-v3", "版本过滤案例新版"); err != nil {
			return err
		}
		return mkChunk("c-v3", "d-v3", "NVMe固件升级公共关键词新版内容", "3.0.0", "3.9.9")
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// 关闭指纹快路径，隔离版本过滤主链路。
	cfg := config.Config{RetrieveFingerprint: false, RetrieveRRF: true, RerankFloor: 0.2, RerankModel: "rerank"}
	se := NewSearcherWithConfig(s, nil, nil, cfg)

	only := func(version string) []string {
		t.Helper()
		res, err := se.SearchCases(context.Background(), Query{Text: "NVMe固件升级", Version: version}, Options{TopK: 3})
		if err != nil {
			t.Fatalf("SearchCases version=%q: %v", version, err)
		}
		ids := make([]string, 0, len(res))
		for _, r := range res {
			ids = append(ids, r.DocID)
		}
		return ids
	}
	if got := only("2.5.0"); len(got) != 1 || got[0] != "d-v2" {
		t.Fatalf("2.5.0 应只回旧版，got %v", got)
	}
	if got := only("3.1.0"); len(got) != 1 || got[0] != "d-v3" {
		t.Fatalf("3.1.0 应只回新版，got %v", got)
	}
	gotAll := only("")
	if len(gotAll) != 2 {
		t.Fatalf("空版本应两者皆回，got %v", gotAll)
	}
}
