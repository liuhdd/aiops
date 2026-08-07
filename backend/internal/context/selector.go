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
	kwWeight         = 0.4
	tagWeight        = 0.4
	recencyWeight    = 0.2
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
	if strings.Contains(chunk.Metadata["metrics"], q.Metric) ||
		strings.Contains(chunk.Metadata["device_types"], q.Device) {
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
	return kwWeight*kwScore + tagWeight*tagScore + recencyWeight*recencyScore
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
