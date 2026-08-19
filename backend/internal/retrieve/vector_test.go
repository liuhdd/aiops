package retrieve

import (
	"math"
	"testing"
	"time"

	"aiops/internal/model"
)

func TestCosine(t *testing.T) {
	if got := Cosine([]float64{1, 0}, []float64{1, 0}); math.Abs(got-1) > 1e-9 {
		t.Fatalf("cos = %v, want 1", got)
	}
	if got := Cosine([]float64{1, 0}, []float64{0, 1}); math.Abs(got) > 1e-9 {
		t.Fatalf("cos = %v, want 0", got)
	}
	if got := Cosine([]float64{1}, []float64{1, 0}); got != 0 {
		t.Fatalf("维度不一致应为0, got %v", got)
	}
	if got := Cosine([]float64{2, 0}, []float64{3, 0}); math.Abs(got-1) > 1e-9 {
		t.Fatalf("非单位向量同向应 1, got %v", got)
	}
	if got := Cosine([]float64{2, 0}, []float64{0, 3}); math.Abs(got) > 1e-9 {
		t.Fatalf("非单位向量正交应 0, got %v", got)
	}
	if got := Cosine([]float64{0, 0}, []float64{1, 0}); got != 0 {
		t.Fatalf("零向量应 0, got %v", got)
	}
}

func TestLabelScore(t *testing.T) {
	kc := model.KbKnowledgeChunk{DeviceTypes: []string{"server"}, Metrics: []string{"disk_usage"},
		Keywords: []string{"扩容"}, Intents: []string{"AskRootCause"}}
	q := Query{Metric: "disk_usage", Device: "server", Text: "磁盘扩容", Intents: []string{"AskRootCause"}}
	if s := LabelScore(kc, q); s <= 0 {
		t.Fatalf("label score = %v, want > 0", s)
	}
	if s := LabelScore(kc, Query{Metric: "gpu_util"}); s != 0 {
		t.Fatalf("不相关应 0, got %v", s)
	}
}

func TestFieldScore(t *testing.T) {
	kc := model.KbKnowledgeChunk{Vendor: []string{"H3C"}, Model: []string{"S5560"},
		DeviceTypes: []string{"switch"}, Metrics: []string{"net_in"}}
	if s := FieldScore(kc, Query{Vendor: "H3C", Metric: "net_in"}); s < 2 {
		t.Fatalf("field score = %v, want >= 2", s)
	}
	if s := FieldScore(kc, Query{Vendor: "Dell"}); s != 0.1 {
		t.Fatalf("有字段全不命中应 0.1, got %v", s)
	}
	if s := FieldScore(kc, Query{}); s != 0 {
		t.Fatalf("无字段应 0, got %v", s)
	}
}

func TestRecencyScore(t *testing.T) {
	now := time.Now()
	old := now.Add(-120 * time.Minute)
	if s := RecencyScore(old, now); s <= 0 || s >= 1 {
		t.Fatalf("recency = %v, want (0,1)", s)
	}
	if s := RecencyScore(now, now); s != 1 {
		t.Fatalf("recency now = %v, want 1", s)
	}
}
