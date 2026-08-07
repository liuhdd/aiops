package context

import (
	"sort"
	"testing"
	"time"
)

func chunk(src Source, content string, meta map[string]string) Chunk {
	return Chunk{Source: src, Content: content, Metadata: meta}
}

func TestScoreKeywordOverlap(t *testing.T) {
	sel := Selector{}
	now := time.Now()
	q := Query{Device: "db-01", Metric: "disk_usage", Severity: "critical", Stage: "rca", Keywords: []string{"磁盘", "扩容"}}
	hit := chunk(SourceKnowledge, "磁盘使用率超过95%时扩容磁盘", map[string]string{"metrics": "disk_usage"})
	miss := chunk(SourceKnowledge, "GPU温度过高时检查风扇", map[string]string{"metrics": "gpu_util"})
	if sel.Score(hit, q, now) <= sel.Score(miss, q, now) {
		t.Fatalf("hit(%f) should beat miss(%f)", sel.Score(hit, q, now), sel.Score(miss, q, now))
	}
}

func TestSelectTopKPerSource(t *testing.T) {
	sel := Selector{}
	q := Query{Metric: "disk_usage"}
	now := time.Now()
	var cands []Chunk
	for i := 0; i < 5; i++ {
		sp := chunk(SourceScratchpad, "disk_usage 磁盘 记录", map[string]string{"created_at": now.Format(time.RFC3339)})
		sp.Score = 0.9 - float64(i)*0.2
		cands = append(cands, sp)
		kn := chunk(SourceKnowledge, "磁盘 扩容 预案", map[string]string{"metrics": "disk_usage"})
		kn.Score = 0.8 - float64(i)*0.2
		cands = append(cands, kn)
	}
	got := sel.Select(cands, q, 3, 0.0)
	srcCount := map[Source]int{}
	for _, c := range got {
		srcCount[c.Source]++
	}
	if srcCount[SourceScratchpad] != 3 || srcCount[SourceKnowledge] != 3 {
		t.Fatalf("per-source counts = %v, want 3 each", srcCount)
	}
	sorted := make([]float64, 0, len(got))
	for _, c := range got {
		sorted = append(sorted, c.Score)
	}
	if !sort.SliceIsSorted(sorted, func(a, b int) bool { return sorted[a] > sorted[b] }) {
		t.Fatalf("result not sorted by score: %v", sorted)
	}
}

func TestScoreRecencyDecay(t *testing.T) {
	sel := Selector{}
	now := time.Now()
	q := Query{Metric: "disk_usage"}
	// 关键词/标签完全一致，仅 created_at 不同：新的应严格高于旧的
	fresh := chunk(SourceScratchpad, "disk_usage 磁盘 记录", map[string]string{
		"metrics":    "disk_usage",
		"created_at": now.Format(time.RFC3339),
	})
	stale := chunk(SourceScratchpad, "disk_usage 磁盘 记录", map[string]string{
		"metrics":    "disk_usage",
		"created_at": now.Add(-2 * time.Hour).Format(time.RFC3339),
	})
	if sel.Score(fresh, q, now) <= sel.Score(stale, q, now) {
		t.Fatalf("fresh(%f) should beat stale(%f)", sel.Score(fresh, q, now), sel.Score(stale, q, now))
	}
}

func TestSelectFiltersBelowThreshold(t *testing.T) {
	sel := Selector{}
	// 无关键词重叠、无标签、无时间戳 → 低分
	weak := chunk(SourceKnowledge, "与查询无关的随机文本", nil)
	got := sel.Select([]Chunk{weak}, Query{Metric: "disk_usage"}, 3, 0.9)
	if len(got) != 0 {
		t.Fatalf("expected filtered out, got %d", len(got))
	}
}
