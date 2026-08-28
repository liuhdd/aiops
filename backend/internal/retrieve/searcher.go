package retrieve

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"aiops/internal/config"
	"aiops/internal/model"
	"aiops/internal/store"
)

// Options 检索选项。
type Options struct {
	TopK int
}

// Result 检索结果块（供工具与组装器消费）。
type Result struct {
	DocID        string
	Title        string
	Category     model.KBCategory
	Heading      string
	Content      string
	Score        float64
	Intents      []string // 所属文档的意图码，供组装器/agent 按意图过滤
	SourceDocIDs []string // 多文档出处（CDTA 合并后的 source_doc_ids）
}

// Searcher 混合检索器接口：案例/手册两路检索入口。
type Searcher interface {
	SearchCases(ctx context.Context, q Query, opts Options) ([]Result, error)
	SearchManuals(ctx context.Context, q Query, opts Options) ([]Result, error)
}

// searcher 混合检索引擎：BM25 + 向量 + 信号，按类型差异化融合。
type searcher struct {
	st       *store.Store
	embedder Embedder
	reranker Reranker
	cfg      config.Config
	mu       sync.Mutex // 保护 idx/docCount 的懒加载
	idx      *BM25Index
	docCount int
}

// NewSearcherWithConfig 构造混合检索器；reranker 可为 nil（精排跳过）。
func NewSearcherWithConfig(st *store.Store, embedder Embedder, rr Reranker, cfg config.Config) Searcher {
	if cfg.RerankModel == "" {
		cfg.RerankModel = "rerank"
	}
	if cfg.RerankFloor == 0 {
		cfg.RerankFloor = 0.2
	}
	return &searcher{st: st, embedder: embedder, reranker: rr, cfg: cfg}
}

// NewSearcher 构造混合检索器；embedder 可为 nil（向量通道回退）。兼容旧构造，默认开关全开但无精排。
func NewSearcher(st *store.Store, embedder Embedder) Searcher {
	return NewSearcherWithConfig(st, embedder, nil, config.Config{
		RetrieveFingerprint: true,
		RetrieveRRF:         true,
		RerankModel:         "rerank",
		RerankFloor:         0.2,
	})
}

// isFingerprintEnabled 指纹快路径是否启用。
func (s *searcher) isFingerprintEnabled() bool {
	return s.cfg.RetrieveFingerprint
}

// isRRFEnabled RRF 融合是否启用。
func (s *searcher) isRRFEnabled() bool {
	return s.cfg.RetrieveRRF
}

// effectiveRerankFloor 返回生效的精排门限（已在构造时归一）。
func (s *searcher) effectiveRerankFloor() float64 {
	return s.cfg.RerankFloor
}

// ensureIndex 知识库文档数变化时重建 BM25 索引（并发安全：check-then-act 加锁）。
func (s *searcher) ensureIndex() error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
func (s *searcher) SearchCases(ctx context.Context, q Query, opts Options) ([]Result, error) {
	return s.search(ctx, q, model.KBCase, false, opts)
}

// SearchManuals 检索产品/操作手册（category IN manual/runbook），关键词为主。
func (s *searcher) SearchManuals(ctx context.Context, q Query, opts Options) ([]Result, error) {
	return s.search(ctx, q, "", true, opts)
}

func (s *searcher) search(ctx context.Context, q Query, cat model.KBCategory, isManual bool, opts Options) ([]Result, error) {
	topK := opts.TopK
	if topK <= 0 {
		topK = 3
	}
	// 指纹快路径（无需 BM25 索引，失败静默降级为 miss）
	if s.isFingerprintEnabled() {
		if res, hit, err := s.fingerprintFastPath(q, cat, isManual, topK); hit {
			return res, err
		}
	}
	if err := s.ensureIndex(); err != nil {
		return nil, err
	}
	cands := s.dualRecall(ctx, q, isManual, cat)
	if s.isRRFEnabled() {
		cands = FuseRRF(cands, isManual)
	} else {
		if isManual {
			cands = RankManuals(cands, time.Now())
		} else {
			cands = RankCases(cands, time.Now())
		}
	}
	if !hasRelevance(cands) {
		return []Result{}, nil // 无相关候选：返回空（spec: 库空/无候选返回空数组）
	}
	cands = s.applyRerank(ctx, q.Text, cands)
	if s.reranker != nil {
		n := len(cands)
		if n > 20 {
			n = 20
		}
		if n > 0 {
			head := gateB(cands[:n], s.effectiveRerankFloor())
			cands = append(head, cands[n:]...)
		}
	}
	if len(cands) > topK {
		cands = cands[:topK]
	}
	res := make([]Result, 0, len(cands))
	for _, c := range cands {
		res = append(res, Result{
			DocID: c.Chunk.DocID, Title: c.Chunk.Title, Category: c.Chunk.Category,
			Heading: c.Chunk.Heading, Content: c.Chunk.Content, Score: c.Final,
			Intents: c.Chunk.Intents, SourceDocIDs: c.Chunk.SourceDocIDs,
		})
	}
	return res, nil
}

// fingerprintFastPath 指纹快路径：命中返回 (results,true,nil)，未命中 (nil,false,nil)。
func (s *searcher) fingerprintFastPath(q Query, cat model.KBCategory, isManual bool, topK int) ([]Result, bool, error) {
	fps := extractFingerprints(q.Text)
	if len(fps) == 0 {
		return nil, false, nil
	}
	docs, err := s.st.ListKbDocumentsByFingerprints(fps)
	if err != nil {
		return nil, false, nil // 失败静默降级为未命中
	}
	// 按 cat/isManual 过滤
	var filtered []model.KbDocSummary
	for _, d := range docs {
		if isManual {
			if d.Category != model.KBManual && d.Category != model.KBRunbook {
				continue
			}
			if q.Category != "" && d.Category != q.Category {
				continue
			}
		} else {
			if d.Category != cat {
				continue
			}
		}
		filtered = append(filtered, d)
	}
	if len(filtered) == 0 {
		return nil, false, nil
	}
	if topK <= 0 {
		topK = 3
	}
	var res []Result
	for _, d := range filtered {
		chunks, err := s.st.ListKbChunksByDoc(d.ID)
		if err != nil {
			continue // 单文档加载失败静默跳过
		}
		for _, c := range chunks {
			res = append(res, Result{
				DocID:        d.ID,
				Title:        d.Title,
				Category:     d.Category,
				Heading:      c.Heading,
				Content:      c.Content,
				Score:        1.0,
				Intents:      d.Intents,
				SourceDocIDs: c.SourceDocIDs,
			})
			if len(res) >= topK {
				break
			}
		}
		if len(res) >= topK {
			break
		}
	}
	if len(res) == 0 {
		return nil, false, nil
	}
	return res, true, nil
}

// dualRecall 双路召回：BM25 + 向量 + 信号 + 时效，返回未排序候选供融合阶段处理。
// 扩展问题通道：拉问题向量与问题文本，对每块计算 QVec/QBM25 取 max 后与块通道取 max 再进 RRF。
func (s *searcher) dualRecall(ctx context.Context, q Query, isManual bool, cat model.KBCategory) []cand {
	kcs, err := s.st.ListKnowledgeChunks()
	if err != nil {
		return nil
	}
	embeds := map[string][]float64{}
	qEmbeds := map[string][]float64{} // questionID -> vec（去掉 q_ 前缀）
	if s.embedder != nil {
		if evs, err := s.st.ListKbEmbeddings(); err == nil {
			for _, e := range evs {
				if strings.HasPrefix(e.ChunkID, "q_") {
					qid := strings.TrimPrefix(e.ChunkID, "q_")
					qEmbeds[qid] = e.Embedding
				} else {
					embeds[e.ChunkID] = e.Embedding
				}
			}
		}
	}
	// idx 可能为 nil（如空库），此时 BM25 分数为 0
	bmScores := map[string]float64{}
	if s.idx != nil {
		bmScores = s.idx.Score(q.Text)
	}
	var qVec []float64
	if s.embedder != nil && q.Text != "" {
		if vs, err := s.embedder.Embed(ctx, []string{q.Text}); err == nil && len(vs) > 0 {
			qVec = vs[0]
		}
	}
	// 拉问题文本，按 chunkID 聚合
	questionsByChunk := map[string][]model.KbChunkQuestion{}
	if qs, err := s.st.ListKbChunkQuestions(); err == nil {
		for _, qq := range qs {
			questionsByChunk[qq.ChunkID] = append(questionsByChunk[qq.ChunkID], qq)
		}
	}
	// 计算 QBM25：对问题文本建临时 BM25 索引，按 chunk 取 max
	qBM25Max := map[string]float64{}
	if len(questionsByChunk) > 0 && strings.TrimSpace(q.Text) != "" {
		var qKcs []model.KbKnowledgeChunk
		var qIds []string
		for _, qs := range questionsByChunk {
			for _, qq := range qs {
				qKcs = append(qKcs, model.KbKnowledgeChunk{ChunkID: qq.ID, Content: qq.Question})
				qIds = append(qIds, qq.ID)
			}
		}
		if len(qKcs) > 0 {
			qIdx := BuildBM25(qKcs, qIds)
			qScores := qIdx.Score(q.Text)
			for chunkID, qs := range questionsByChunk {
				max := 0.0
				for _, qq := range qs {
					if sc := qScores[qq.ID]; sc > max {
						max = sc
					}
				}
				if max > 0 {
					qBM25Max[chunkID] = max
				}
			}
		}
	}
	// 计算 QVec：对每块取所属问题中与 qVec 的最大余弦
	qVecMax := map[string]float64{}
	if qVec != nil && len(qEmbeds) > 0 {
		for chunkID, qs := range questionsByChunk {
			max := 0.0
			first := true
			for _, qq := range qs {
				if qv, ok := qEmbeds[qq.ID]; ok {
					sc := Cosine(qv, qVec)
					if first || sc > max {
						max = sc
						first = false
					}
				}
			}
			if !first {
				qVecMax[chunkID] = max
			}
		}
	}
	now := time.Now()
	var cands []cand
	for _, kc := range kcs {
		if isManual {
			if kc.Category != model.KBManual && kc.Category != model.KBRunbook {
				continue
			}
			if q.Category != "" && kc.Category != q.Category {
				continue
			}
		} else if kc.Category != cat {
			continue
		}
		c := cand{Chunk: kc}
		c.BM25 = bmScores[kc.ChunkID]
		if v, ok := qBM25Max[kc.ChunkID]; ok && v > c.BM25 {
			c.BM25 = v
		}
		if v, ok := embeds[kc.ChunkID]; ok && qVec != nil {
			c.Vec = Cosine(v, qVec)
		}
		if qv, ok := qVecMax[kc.ChunkID]; ok && qv > c.Vec {
			c.Vec = qv
		}
		if isManual {
			c.Signal = FieldScore(kc, q)
		} else {
			c.Signal = LabelScore(kc, q)
			c.Recency = RecencyScore(kc.CreatedAt, now)
		}
		cands = append(cands, c)
	}
	return cands
}

// applyRerank 精排：取前 20 调 rerank，按分重排，失败原样返回。
func (s *searcher) applyRerank(ctx context.Context, qText string, cands []cand) []cand {
	if s.reranker == nil || len(cands) == 0 {
		return cands
	}
	n := len(cands)
	if n > 20 {
		n = 20
	}
	docs := make([]string, n)
	for i := 0; i < n; i++ {
		docs[i] = cands[i].Chunk.Content
	}
	scores, err := s.reranker.Rerank(ctx, qText, docs)
	if err != nil || len(scores) != n {
		return cands // 失败静默降级
	}
	for i := 0; i < n; i++ {
		cands[i].Final = scores[i]
	}
	// 仅对前 n 个按精排分重排
	sort.SliceStable(cands[:n], func(a, b int) bool { return cands[a].Final > cands[b].Final })
	return cands
}

// gateB 精排分低于 floor 的剔除（无 rerank 时透传，由调用方确保仅在精排后调用）。
func gateB(cands []cand, floor float64) []cand {
	if floor <= 0 {
		return cands
	}
	out := cands[:0]
	for _, c := range cands {
		if c.Final >= floor {
			out = append(out, c)
		}
	}
	return out
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

// vectorRelevanceFloor 向量通道的相关性门限：原始余弦 ≥ 该值视为语义相关。
// 排名阶段向量分经 minmax 归一化后单候选会塌缩为 0，故相关性判定须用归一化前的原始余弦。
const vectorRelevanceFloor = 0.5

// hasRelevance 是否存在相关候选：关键词(BM25)、信号或纯语义向量(原始余弦≥门限)任一命中即相关。
func hasRelevance(cands []cand) bool {
	for _, c := range cands {
		if c.BM25 > 0 || c.Signal > 0 || c.Vec >= vectorRelevanceFloor {
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
