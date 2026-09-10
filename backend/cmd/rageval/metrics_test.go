package main

import (
	"math"
	"testing"
)

func TestComputeNDCG(t *testing.T) {
	ranked := []string{"d2", "d1", "d3"}
	relevant := map[string]bool{"d1": true}
	got := ComputeNDCG(ranked, relevant, 3)
	// 排名第 2 命中，归一化 DCG 应为 1/log2(3) / 理想 DCG
	if got <= 0.6 || got >= 0.65 {
		t.Fatalf("NDCG=%v not in [0.60,0.65)", got)
	}
}
func TestComputeMRR(t *testing.T) {
	got := ComputeMRR([]string{"d2", "d1"}, map[string]bool{"d1": true})
	if got != 0.5 {
		t.Fatalf("MRR=%v want 0.5", got)
	}
}
func TestComputeRecallAtK_Hit(t *testing.T) {
	got := ComputeRecallAtK([]string{"d1", "d2"}, map[string]bool{"d1": true, "d2": true}, 1)
	if got != 0.5 {
		t.Fatalf("Recall@1=%v want 0.5", got)
	}
}
func TestComputeRejectRate(t *testing.T) {
	results := map[string][]string{"q1": {}, "q2": {"d1"}}
	expects := map[string]string{"q1": "reject", "q2": "reject"}
	got := ComputeRejectRate(results, expects)
	if got != 0.5 {
		t.Fatalf("RejectRate=%v want 0.5", got)
	}
}
func TestComputePrecisionAtK(t *testing.T) {
	ranked := []string{"a", "b", "c"}
	relevant := map[string]bool{"a": true}
	if got := ComputePrecisionAtK(ranked, relevant, 3); math.Abs(got-1.0/3.0) > 1e-9 {
		t.Fatalf("precision = %v, want 1/3", got)
	}
	if got := ComputePrecisionAtK(ranked, relevant, 1); got != 1 {
		t.Fatalf("precision@1 = %v", got)
	}
}
