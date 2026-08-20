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

// Searcher 混合检索器接口：案例/手册两路检索入口。
type Searcher interface {
	SearchCases(ctx context.Context, q Query, opts Options) ([]Result, error)
	SearchManuals(ctx context.Context, q Query, opts Options) ([]Result, error)
}

// searcher 混合检索引擎：BM25 + 向量 + 信号，按类型差异化融合。
type searcher struct {
	st       *store.Store
	embedder Embedder
	idx      *BM25Index
	docCount int
}

// NewSearcher 构造混合检索器；embedder 可为 nil（向量通道回退）。
func NewSearcher(st *store.Store, embedder Embedder) *searcher {
	return &searcher{st: st, embedder: embedder}
}

// ensureIndex 知识库文档数变化时重建 BM25 索引。
func (s *searcher) ensureIndex() error {
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
	if !hasRelevance(cands) {
		return []Result{}, nil // 无相关候选：返回空（spec: 库空/无候选返回空数组）
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

// hasRelevance 是否存在相关候选：关键词(BM25)或信号命中即相关；
// 向量分经 minmax 归一化后单候选会塌缩为 0，故不参与相关性判定。
func hasRelevance(cands []cand) bool {
	for _, c := range cands {
		if c.BM25 > 0 || c.Signal > 0 {
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
