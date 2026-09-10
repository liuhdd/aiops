package goldenset

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRagReportRoundTrip(t *testing.T) {
	rep := RagReport{
		Meta:    RagMeta{Timestamp: time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC), Enhance: true, TopK: 3},
		Items:   []ItemResult{{ID: "q1", Type: "fact", TopK: 3, RankedDocIDs: []string{"d1"}, Behavior: "hit", MRR: 1, NDCG: 1, Recall: 1, Precision: 0.33}},
		Overall: Overall{HitCount: 1, MRR: 1, NDCG: 1, Recall: 1, Precision: 0.33},
	}
	b, err := rep.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	var got RagReport
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Items[0].ID != "q1" || got.Overall.HitCount != 1 {
		t.Fatalf("got = %+v", got)
	}
	if !strings.Contains(string(b), `"meta"`) || !strings.Contains(string(b), `"overall"`) {
		t.Fatalf("json = %s", b)
	}
}
