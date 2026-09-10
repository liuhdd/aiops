package goldenset

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"aiops/internal/phoenix"
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

type fakeWriter struct {
	runs  []phoenix.RunRequest
	evals []phoenix.EvaluationRequest
	exp   []string
}

func (f *fakeWriter) ListExamples(datasetID, versionID string) ([]phoenix.Example, error) {
	return []phoenix.Example{{ID: "EX-q1", Metadata: map[string]any{"id": "q1"}}}, nil
}
func (f *fakeWriter) CreateExperiment(datasetID, name string, repetitions int, metadata map[string]any) (phoenix.Experiment, error) {
	f.exp = append(f.exp, name)
	return phoenix.Experiment{ID: "E1"}, nil
}
func (f *fakeWriter) LogRun(experimentID string, r phoenix.RunRequest) (string, error) {
	f.runs = append(f.runs, r)
	return "R1", nil
}
func (f *fakeWriter) LogEvaluation(e phoenix.EvaluationRequest) (string, error) {
	f.evals = append(f.evals, e)
	return "EV1", nil
}

func TestPushRagExperiment(t *testing.T) {
	rep := RagReport{
		Meta:  RagMeta{Enhance: true, TopK: 3},
		Items: []ItemResult{{ID: "q1", Type: "fact", TopK: 3, RankedDocIDs: []string{"d1"}, Behavior: "hit", MRR: 1, NDCG: 1, Recall: 1, Precision: 0.33}},
	}
	w := &fakeWriter{}
	if err := PushRagExperiment(w, "DS", rep, "aiops-rag-golden"); err != nil {
		t.Fatal(err)
	}
	if len(w.exp) != 1 || w.exp[0] != "aiops-rag-golden/rag" {
		t.Fatalf("exp = %v", w.exp)
	}
	if len(w.runs) != 1 || w.runs[0].DatasetExampleID != "EX-q1" {
		t.Fatalf("runs = %+v", w.runs)
	}
	names := map[string]bool{}
	for _, e := range w.evals {
		names[e.Name] = true
	}
	for _, want := range []string{"MRR", "NDCG@K", "Recall@K", "Precision@K"} {
		if !names[want] {
			t.Fatalf("缺评测 %s: %+v", want, w.evals)
		}
	}
}
