package goldenset

import (
	"encoding/json"
	"fmt"
	"time"

	"aiops/internal/phoenix"
)

type ItemResult struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	Question     string    `json:"question"`
	TopK         int       `json:"top_k"`
	RankedDocIDs []string  `json:"ranked_doc_ids"`
	Behavior     string    `json:"behavior"`
	MRR          float64   `json:"mrr"`
	NDCG         float64   `json:"ndcg"`
	Recall       float64   `json:"recall"`
	Precision    float64   `json:"precision"`
	RejectOK     bool      `json:"reject_ok"`
	StartedAt    time.Time `json:"started_at"`
	EndedAt      time.Time `json:"ended_at"`
}

type RagMeta struct {
	Timestamp time.Time `json:"ts"`
	Enhance   bool      `json:"enhance"`
	TopK      int       `json:"top_k"`
}

type Overall struct {
	HitCount    int     `json:"hit_count"`
	RejectCount int     `json:"reject_count"`
	RejectOK    int     `json:"reject_ok"`
	MRR         float64 `json:"mrr"`
	NDCG        float64 `json:"ndcg"`
	Recall      float64 `json:"recall"`
	Precision   float64 `json:"precision"`
}

type RagReport struct {
	Meta    RagMeta      `json:"meta"`
	Items   []ItemResult `json:"items"`
	Overall Overall      `json:"overall"`
}

func (r RagReport) ToJSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// PushRagExperiment 把 rageval 报告回写为单个 Phoenix 实验：每 item 一 run，
// 每项指标一条 evaluation（CODE），reject 题额外一条 RejectOK。
func PushRagExperiment(w phoenix.Writer, datasetID string, report RagReport, namePrefix string) error {
	exs, err := w.ListExamples(datasetID, "")
	if err != nil {
		return fmt.Errorf("列数据集失败: %w", err)
	}
	byID := map[string]string{}
	for _, e := range exs {
		if id, ok := e.Metadata["id"].(string); ok {
			byID[id] = e.ID
		}
	}
	exp, err := w.CreateExperiment(datasetID, namePrefix+"/rag", 1, map[string]any{
		"enhance": report.Meta.Enhance, "top_k": report.Meta.TopK, "ts": report.Meta.Timestamp.Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	var firstErr error
	for _, it := range report.Items {
		exID, ok := byID[it.ID]
		if !ok {
			continue
		}
		start, end := it.StartedAt, it.EndedAt
		if start.IsZero() {
			start = report.Meta.Timestamp
		}
		if end.IsZero() {
			end = report.Meta.Timestamp
		}
		runID, err := w.LogRun(exp.ID, phoenix.RunRequest{
			DatasetExampleID: exID, Output: map[string]any{"ranked_doc_ids": it.RankedDocIDs},
			RepetitionNumber: 1, StartTime: start, EndTime: end,
		})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		metrics := []struct {
			name string
			val  float64
		}{
			{"MRR", it.MRR}, {"NDCG@K", it.NDCG}, {"Recall@K", it.Recall}, {"Precision@K", it.Precision},
		}
		for _, m := range metrics {
			v := m.val
			if _, err := w.LogEvaluation(phoenix.EvaluationRequest{
				ExperimentRunID: runID, Name: m.name, AnnotatorKind: "CODE",
				StartTime: start, EndTime: end,
				Result: &phoenix.EvaluationResult{Score: &v, Explanation: it.Behavior},
			}); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if it.Behavior == "reject" {
			v := 0.0
			if it.RejectOK {
				v = 1.0
			}
			if _, err := w.LogEvaluation(phoenix.EvaluationRequest{
				ExperimentRunID: runID, Name: "RejectOK", AnnotatorKind: "CODE",
				StartTime: start, EndTime: end,
				Result: &phoenix.EvaluationResult{Score: &v, Label: passFail(it.RejectOK)},
			}); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func passFail(b bool) string {
	if b {
		return "PASS"
	}
	return "FAIL"
}
