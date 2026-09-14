package main

import (
	"fmt"

	"aiops/internal/goldenset"
	"aiops/internal/phoenix"
	"aiops/internal/retrieve"
)

// convertTraces 把 retrieve 的链路轨迹转成报告用的本地类型。
func convertTraces(in []retrieve.LinkTrace) []goldenset.LinkTrace {
	out := make([]goldenset.LinkTrace, 0, len(in))
	for _, t := range in {
		out = append(out, goldenset.LinkTrace{Kind: t.Kind, Impl: t.Impl, DurationMS: t.DurationMS, Hit: t.Hit, Note: t.Note})
	}
	return out
}

// toOverall 由变体聚合指标与逐题命中/reject 计数组装 Phoenix 报告的总体指标。
func toOverall(r VariantReport) goldenset.Overall {
	o := goldenset.Overall{
		MRR: r.Overall.MRR, NDCG: r.Overall.NDCG, Recall: r.Overall.Recall, Precision: r.Overall.Precision,
	}
	for _, it := range r.Items {
		if it.Behavior == "reject" {
			o.RejectCount++
			if it.RejectOK {
				o.RejectOK++
			}
			continue
		}
		o.HitCount++
	}
	return o
}

// findRagDataset 按名查 RAG 标注集的数据集 id；未找到提示先 push。
func findRagDataset(pc *phoenix.Client) (string, error) {
	ds, err := pc.ListDatasets()
	if err != nil {
		return "", err
	}
	for _, d := range ds {
		if d.Name == goldenset.RagDatasetName {
			return d.ID, nil
		}
	}
	return "", fmt.Errorf("未找到数据集 %s（先执行 datasetctl push）", goldenset.RagDatasetName)
}
