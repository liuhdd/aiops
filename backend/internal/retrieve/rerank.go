package retrieve

import (
	"sort"
	"time"

	"aiops/internal/model"
)

const (
	caseVecWeight   = 0.45
	caseLabelWeight = 0.30
	caseBM25Weight  = 0.15
	caseRecencyW    = 0.10

	manualBM25Weight  = 0.50
	manualFieldWeight = 0.30
	manualVecWeight   = 0.20
)

// cand 融合候选：各通道原始分 + 融合分。
type cand struct {
	Chunk   model.KbKnowledgeChunk
	BM25    float64
	Vec     float64
	Signal  float64
	Recency float64
	Final   float64
}

// RankCases 运维案例融合排序（语义为主）。输入候选原地排序后返回。
func RankCases(cands []cand, now time.Time) []cand {
	bm := minmax(get(cands, func(c cand) float64 { return c.BM25 }))
	vec := minmax(get(cands, func(c cand) float64 { return c.Vec }))
	sig := minmax(get(cands, func(c cand) float64 { return c.Signal }))
	rec := minmax(get(cands, func(c cand) float64 { return c.Recency }))
	for i := range cands {
		cands[i].Final = caseVecWeight*vec[i] + caseLabelWeight*sig[i] +
			caseBM25Weight*bm[i] + caseRecencyW*rec[i]
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Final > cands[j].Final })
	return cands
}

// RankManuals 手册融合排序（关键词为主，无时效）。输入候选原地排序后返回。
func RankManuals(cands []cand, now time.Time) []cand {
	bm := minmax(get(cands, func(c cand) float64 { return c.BM25 }))
	vec := minmax(get(cands, func(c cand) float64 { return c.Vec }))
	sig := minmax(get(cands, func(c cand) float64 { return c.Signal }))
	for i := range cands {
		cands[i].Final = manualBM25Weight*bm[i] + manualFieldWeight*sig[i] + manualVecWeight*vec[i]
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Final > cands[j].Final })
	return cands
}

func get(cands []cand, f func(cand) float64) []float64 {
	out := make([]float64, len(cands))
	for i, c := range cands {
		out[i] = f(c)
	}
	return out
}

// minmax 归一化到 [0,1]；最大值=最小值（含全 0）时全部置 0。
func minmax(vals []float64) []float64 {
	if len(vals) == 0 {
		return vals
	}
	lo, hi := vals[0], vals[0]
	for _, v := range vals[1:] {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	if hi-lo < 1e-12 {
		for i := range vals {
			vals[i] = 0
		}
		return vals
	}
	for i := range vals {
		vals[i] = (vals[i] - lo) / (hi - lo)
	}
	return vals
}
