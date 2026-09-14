package main

import "aiops/internal/goldenset"

type Metrics struct {
	MRR        float64 `json:"mrr"`
	NDCG       float64 `json:"ndcg"`
	Recall     float64 `json:"recall"`
	Precision  float64 `json:"precision"`
	RejectRate float64 `json:"reject_rate"`
}

type VariantReport struct {
	Name    string                 `json:"name"`
	Spec    map[string]string      `json:"spec"`
	Overall Metrics                `json:"overall"`
	ByType  map[string]Metrics     `json:"by_type,omitempty"`
	Items   []goldenset.ItemResult `json:"items"`
}

type CompareMeta struct {
	Timestamp string `json:"ts"`
	Golden    string `json:"golden"`
	Enhance   bool   `json:"enhance"`
	Baseline  string `json:"baseline"`
}

type ComparisonReport struct {
	Meta     CompareMeta        `json:"meta"`
	Variants []VariantReport    `json:"variants"`
	Deltas   map[string]Metrics `json:"deltas"`
}

// aggregate 计算 overall 与 by_type 指标；hit 题参与 MRR/NDCG/Recall/Precision，reject 题参与 RejectRate。
func aggregate(items []goldenset.ItemResult) (Metrics, map[string]Metrics) {
	type acc struct {
		n, hit, reject, rejectOK int
		mrr, ndcg, rec, prec     float64
	}
	aggs := map[string]*acc{}
	total := &acc{}
	for _, it := range items {
		a := aggs[it.Type]
		if a == nil {
			a = &acc{}
			aggs[it.Type] = a
		}
		a.n++
		total.n++
		if it.Behavior == "reject" {
			a.reject++
			total.reject++
			if it.RejectOK {
				a.rejectOK++
				total.rejectOK++
			}
			continue
		}
		a.hit++
		total.hit++
		a.mrr += it.MRR
		a.ndcg += it.NDCG
		a.rec += it.Recall
		a.prec += it.Precision
		total.mrr += it.MRR
		total.ndcg += it.NDCG
		total.rec += it.Recall
		total.prec += it.Precision
	}
	toMetrics := func(a *acc) Metrics {
		m := Metrics{}
		if a.hit > 0 {
			m.MRR = a.mrr / float64(a.hit)
			m.NDCG = a.ndcg / float64(a.hit)
			m.Recall = a.rec / float64(a.hit)
			m.Precision = a.prec / float64(a.hit)
		}
		if a.reject > 0 {
			m.RejectRate = float64(a.rejectOK) / float64(a.reject)
		}
		return m
	}
	byType := map[string]Metrics{}
	for k, a := range aggs {
		byType[k] = toMetrics(a)
	}
	return toMetrics(total), byType
}

func delta(base, v Metrics) Metrics {
	return Metrics{
		MRR: v.MRR - base.MRR, NDCG: v.NDCG - base.NDCG, Recall: v.Recall - base.Recall,
		Precision: v.Precision - base.Precision, RejectRate: v.RejectRate - base.RejectRate,
	}
}
