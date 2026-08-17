package context

import (
	"math"
	"sort"
	"strings"
	"time"
)

const (
	DefaultTopK      = 3
	DefaultThreshold = 0.15
	kwWeight         = 0.3
	tagWeight        = 0.3
	intentWeight     = 0.25
	recencyWeight    = 0.15
	halfLifeMinutes  = 60.0
)

type Selector struct{}

func (Selector) keywords(q Query) []string {
	var out []string
	for _, k := range append([]string{q.Device, q.Metric, q.Severity, q.Stage}, q.Keywords...) {
		if k != "" {
			out = append(out, strings.ToLower(k))
		}
	}
	return out
}

func (Selector) Score(chunk Chunk, q Query, now time.Time) float64 {
	kws := Selector{}.keywords(q)
	hay := strings.ToLower(chunk.Content + " " + joinMeta(chunk.Metadata))
	kwScore := 1.0
	if len(kws) > 0 {
		matched := 0
		for _, k := range kws {
			if strings.Contains(hay, k) {
				matched++
			}
		}
		kwScore = float64(matched) / float64(len(kws))
	}
	tagScore := 0.0
	if (q.Metric != "" && strings.Contains(chunk.Metadata["metrics"], q.Metric)) ||
		(q.Device != "" && strings.Contains(chunk.Metadata["device_types"], q.Device)) {
		tagScore = 1.0
	}
	recencyScore := 0.5
	if ts := chunk.Metadata["created_at"]; ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			ageMin := now.Sub(t).Minutes()
			if ageMin < 0 {
				ageMin = 0
			}
			recencyScore = math.Exp(-ageMin / halfLifeMinutes)
		}
	}
	intentScore := 0.0
	if len(q.Intents) > 0 && hitsIntent(q.Intents, chunk.Metadata["intents"]) {
		intentScore = 1.0
	}
	return kwWeight*kwScore + tagWeight*tagScore + intentWeight*intentScore + recencyWeight*recencyScore
}

// MatchesIntent 判断块文档 intents 元数据（逗号分隔）是否包含指定意图码（精确 token 匹配）。
func MatchesIntent(meta, code string) bool {
	return hitsIntent([]string{code}, meta)
}

// hitsIntent 查询意图与块文档 intents 元数据（逗号分隔）是否有交集。
func hitsIntent(query []string, meta string) bool {
	if meta == "" {
		return false
	}
	tokens := map[string]bool{}
	for _, t := range strings.Split(meta, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tokens[t] = true
		}
	}
	for _, q := range query {
		if tokens[q] {
			return true
		}
	}
	return false
}

func (Selector) Select(candidates []Chunk, q Query, topK int, threshold float64) []Chunk {
	bySource := map[Source][]Chunk{}
	for _, c := range candidates {
		if c.Score < threshold {
			continue
		}
		bySource[c.Source] = append(bySource[c.Source], c)
	}
	var merged []Chunk
	for _, list := range bySource {
		sort.SliceStable(list, func(i, j int) bool { return list[i].Score > list[j].Score })
		if len(list) > topK {
			list = list[:topK]
		}
		merged = append(merged, list...)
	}
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].Score > merged[j].Score })
	return merged
}

func joinMeta(m map[string]string) string {
	parts := make([]string, 0, len(m))
	for _, v := range m {
		parts = append(parts, v)
	}
	return strings.Join(parts, " ")
}
