package main

import (
	"testing"

	"aiops/internal/goldenset"
)

func TestAggregateAndDelta(t *testing.T) {
	items := []goldenset.ItemResult{
		{ID: "q1", Type: "fact", Behavior: "hit", MRR: 1, NDCG: 1, Recall: 1, Precision: 1},
		{ID: "q2", Type: "fact", Behavior: "hit", MRR: 0, NDCG: 0, Recall: 0, Precision: 0},
		{ID: "q3", Type: "no_answer", Behavior: "reject", RejectOK: true},
	}
	overall, byType := aggregate(items)
	if overall.RejectRate != 1.0 {
		t.Fatalf("reject rate=%v", overall.RejectRate)
	}
	if byType["fact"].MRR != 0.5 {
		t.Fatalf("byType fact mrr=%v", byType["fact"].MRR)
	}
	d := delta(overall, Metrics{MRR: overall.MRR + 0.1})
	if d.MRR < 0.099 || d.MRR > 0.101 {
		t.Fatalf("delta=%v", d.MRR)
	}
}

func TestBaselineFirstSelectsBaseline(t *testing.T) {
	reports := []VariantReport{
		{Name: "fuse=weighted", Overall: Metrics{MRR: 0.9}},
		{Name: "baseline", Overall: Metrics{MRR: 0.2}},
		{Name: "rerank=none", Overall: Metrics{MRR: 0.2}},
	}
	got := baselineFirst(reports)
	if got[0].Name != "baseline" {
		t.Fatalf("first=%s", got[0].Name)
	}
	if got[1].Name != "fuse=weighted" || got[2].Name != "rerank=none" {
		t.Fatalf("order changed: %v", []string{got[0].Name, got[1].Name, got[2].Name})
	}
	if d := delta(got[0].Overall, got[1].Overall); d.MRR < 0.699 || d.MRR > 0.701 {
		t.Fatalf("delta vs baseline=%v", d.MRR)
	}
}
