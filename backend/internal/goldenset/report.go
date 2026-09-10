package goldenset

import (
	"encoding/json"
	"time"
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
