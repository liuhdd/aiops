package main

import "math"

func ComputeMRR(ranked []string, relevant map[string]bool) float64 {
	for i, id := range ranked {
		if relevant[id] {
			return 1.0 / float64(i+1)
		}
	}
	return 0
}
func ComputeRecallAtK(ranked []string, relevant map[string]bool, k int) float64 {
	if len(relevant) == 0 {
		return 0
	}
	if k > len(ranked) {
		k = len(ranked)
	}
	hit := 0
	for _, id := range ranked[:k] {
		if relevant[id] {
			hit++
		}
	}
	return float64(hit) / float64(len(relevant))
}
func ComputeNDCG(ranked []string, relevant map[string]bool, k int) float64 {
	if len(ranked) == 0 || len(relevant) == 0 {
		return 0
	}
	if k > len(ranked) {
		k = len(ranked)
	}
	dcg := 0.0
	for i := 0; i < k; i++ {
		rel := 0.0
		if relevant[ranked[i]] {
			rel = 1
		}
		dcg += rel / math.Log2(float64(i+2))
	}
	idcg := 0.0
	need := len(relevant)
	if need > k {
		need = k
	}
	for i := 0; i < need; i++ {
		idcg += 1 / math.Log2(float64(i+2))
	}
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}
func ComputeRejectRate(results map[string][]string, expects map[string]string) float64 {
	tot, ok := 0, 0
	for id, beh := range expects {
		if beh != "reject" {
			continue
		}
		tot++
		if len(results[id]) == 0 {
			ok++
		}
	}
	if tot == 0 {
		return 0
	}
	return float64(ok) / float64(tot)
}
