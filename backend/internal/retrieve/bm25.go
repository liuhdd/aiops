package retrieve

import (
	"math"
	"strings"

	"aiops/internal/model"
)

// Tokenize 切词：含 CJK 的 token 按单字拆，其余按空格分词并小写。
func Tokenize(s string) []string {
	var out []string
	for _, tok := range strings.Fields(s) {
		if hasCJK(tok) {
			for _, r := range tok {
				out = append(out, string(r))
			}
		} else {
			out = append(out, strings.ToLower(tok))
		}
	}
	return out
}

func hasCJK(s string) bool {
	for _, r := range s {
		if r >= 0x4e00 && r <= 0x9fff {
			return true
		}
	}
	return false
}

// BM25Index 内存倒排索引；每个知识块视为一个文档。
type BM25Index struct {
	k1, b  float64
	avgLen float64
	docLen []float64
	df     map[string]int
	tf     map[string]map[int]int
	ids    []string
}

// BuildBM25 从知识块构建索引；ids 与 chunks 一一对应。
func BuildBM25(chunks []model.KbKnowledgeChunk, ids []string) *BM25Index {
	// 防御：ids 不足时截断 chunks，保证索引内 chunk 下标都能映射到合法 id。
	if len(ids) < len(chunks) {
		chunks = chunks[:len(ids)]
	}
	ix := &BM25Index{k1: 1.5, b: 0.75, df: map[string]int{}, tf: map[string]map[int]int{}, ids: ids}
	totalLen := 0.0
	for i, c := range chunks {
		toks := Tokenize(c.Content)
		ix.docLen = append(ix.docLen, float64(len(toks)))
		totalLen += float64(len(toks))
		seen := map[string]bool{}
		for _, t := range toks {
			if ix.tf[t] == nil {
				ix.tf[t] = map[int]int{}
			}
			ix.tf[t][i]++
			if !seen[t] {
				seen[t] = true
				ix.df[t]++
			}
		}
	}
	if len(chunks) > 0 {
		ix.avgLen = totalLen / float64(len(chunks))
	}
	return ix
}

// Score 返回 chunkID → BM25 分数；查询无命中返回空 map。
func (ix *BM25Index) Score(query string) map[string]float64 {
	out := map[string]float64{}
	n := len(ix.docLen)
	if n == 0 {
		return out
	}
	for _, t := range Tokenize(query) {
		df := ix.df[t]
		if df == 0 {
			continue
		}
		idf := math.Log(1 + (float64(n)-float64(df)+0.5)/(float64(df)+0.5))
		for idx, tf := range ix.tf[t] {
			tfF := float64(tf)
			denom := tfF + ix.k1*(1-ix.b+ix.b*ix.docLen[idx]/ix.avgLen)
			out[ix.ids[idx]] += idf * tfF * (ix.k1 + 1) / denom
		}
	}
	return out
}
