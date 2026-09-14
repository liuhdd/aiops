package goldenset

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"aiops/internal/phoenix"
)

func TestRagReportRoundTrip(t *testing.T) {
	rep := RagReport{
		Meta: RagMeta{Timestamp: time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC), Enhance: true, TopK: 3,
			Variant: "rerank=none", VariantSpec: map[string]string{"rerank": "none"}},
		Items:   []ItemResult{{ID: "q1", Type: "fact", TopK: 3, RankedDocIDs: []string{"d1"}, Behavior: "hit", MRR: 1, NDCG: 1, Recall: 1, Precision: 0.33, Traces: []LinkTrace{{Kind: "recall", Impl: "hybrid", DurationMS: 3, Hit: true, Note: "n"}}}},
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
	if got.Meta.Variant != "rerank=none" || got.Meta.VariantSpec["rerank"] != "none" {
		t.Fatalf("meta variant = %+v", got.Meta)
	}
	if len(got.Items[0].Traces) != 1 || got.Items[0].Traces[0].Impl != "hybrid" || !got.Items[0].Traces[0].Hit {
		t.Fatalf("traces = %+v", got.Items[0].Traces)
	}
	if !strings.Contains(string(b), `"meta"`) || !strings.Contains(string(b), `"overall"`) || !strings.Contains(string(b), `"traces"`) {
		t.Fatalf("json = %s", b)
	}
}

type fakeWriter struct {
	runs               []phoenix.RunRequest
	evals              []phoenix.EvaluationRequest
	exp                []string
	lastExperimentName string
	lastExperimentMeta map[string]any
}

func (f *fakeWriter) ListExamples(datasetID, versionID string) ([]phoenix.Example, error) {
	return []phoenix.Example{
		{ID: "EX-q1", Metadata: map[string]any{"id": "q1"}},
		{ID: "EX-q2", Metadata: map[string]any{"id": "q2"}},
		{ID: "EX-q3", Metadata: map[string]any{"id": "q3"}},
	}, nil
}
func (f *fakeWriter) CreateExperiment(datasetID, name string, repetitions int, metadata map[string]any) (phoenix.Experiment, error) {
	f.exp = append(f.exp, name)
	f.lastExperimentName = name
	f.lastExperimentMeta = metadata
	return phoenix.Experiment{ID: "E1"}, nil
}
func (f *fakeWriter) LogRun(experimentID string, r phoenix.RunRequest) (string, error) {
	f.runs = append(f.runs, r)
	return fmt.Sprintf("R%d", len(f.runs)), nil
}
func (f *fakeWriter) LogEvaluation(e phoenix.EvaluationRequest) (string, error) {
	f.evals = append(f.evals, e)
	return "EV1", nil
}

// findEval 按 runID + 评测名查一条 evaluation。
func findEval(evals []phoenix.EvaluationRequest, runID, name string) (phoenix.EvaluationRequest, bool) {
	for _, e := range evals {
		if e.ExperimentRunID == runID && e.Name == name {
			return e, true
		}
	}
	return phoenix.EvaluationRequest{}, false
}

func TestPushRagExperiment(t *testing.T) {
	hit := ItemResult{ID: "q1", Type: "fact", TopK: 3, RankedDocIDs: []string{"d1", "d2"}, Behavior: "hit", MRR: 0.5, NDCG: 0.6, Recall: 0.7, Precision: 0.33}
	rejectOK := ItemResult{ID: "q2", Type: "reject", Behavior: "reject", RejectOK: true}
	rejectFail := ItemResult{ID: "q3", Type: "reject", Behavior: "reject", RejectOK: false}
	rep := RagReport{
		Meta:  RagMeta{Enhance: true, TopK: 3},
		Items: []ItemResult{hit, rejectOK, rejectFail},
	}
	w := &fakeWriter{}
	sum, err := PushRagExperiment(w, "DS", rep, "aiops-rag-golden/rag")
	if err != nil {
		t.Fatal(err)
	}
	if sum.Name != "aiops-rag-golden/rag" || sum.ID != "E1" {
		t.Fatalf("summary name/id = %+v", sum)
	}
	// 3 runs；q1=4 evals，q2/q3 各 4+RejectOK=5 → 共 14
	if sum.RunsWritten != 3 || sum.EvalsWritten != 14 || sum.Failures != 0 {
		t.Fatalf("summary counts = %+v", sum)
	}
	if len(w.exp) != 1 || w.exp[0] != "aiops-rag-golden/rag" {
		t.Fatalf("exp = %v", w.exp)
	}
	if len(w.runs) != 3 {
		t.Fatalf("runs = %+v", w.runs)
	}
	// run 与 example 按报告顺序一一对应
	for i, want := range []string{"EX-q1", "EX-q2", "EX-q3"} {
		if w.runs[i].DatasetExampleID != want {
			t.Fatalf("runs[%d].DatasetExampleID = %s, want %s", i, w.runs[i].DatasetExampleID, want)
		}
	}
	// hit item 的 run Output 携带 ranked_doc_ids
	out, ok := w.runs[0].Output.(map[string]any)
	if !ok {
		t.Fatalf("runs[0].Output 类型 = %T", w.runs[0].Output)
	}
	docs, ok := out["ranked_doc_ids"].([]string)
	if !ok || !reflect.DeepEqual(docs, hit.RankedDocIDs) {
		t.Fatalf("ranked_doc_ids = %#v, want %v", out["ranked_doc_ids"], hit.RankedDocIDs)
	}
	// hit item 的四项指标评测携带对应分值
	metrics := map[string]float64{"MRR": hit.MRR, "NDCG@K": hit.NDCG, "Recall@K": hit.Recall, "Precision@K": hit.Precision}
	for name, want := range metrics {
		e, ok := findEval(w.evals, "R1", name)
		if !ok {
			t.Fatalf("缺评测 %s: %+v", name, w.evals)
		}
		if e.Result == nil || e.Result.Score == nil || *e.Result.Score != want {
			t.Fatalf("%s score = %+v, want %v", name, e.Result, want)
		}
	}
	// hit item 不应写 RejectOK
	if _, ok := findEval(w.evals, "R1", "RejectOK"); ok {
		t.Fatalf("hit item 不应有 RejectOK: %+v", w.evals)
	}
	// reject 且 RejectOK=true → score 1 / PASS
	e, ok := findEval(w.evals, "R2", "RejectOK")
	if !ok {
		t.Fatalf("缺 RejectOK 评测: %+v", w.evals)
	}
	if e.Result == nil || e.Result.Score == nil || *e.Result.Score != 1 || e.Result.Label != "PASS" {
		t.Fatalf("RejectOK(true) = %+v", e.Result)
	}
	// reject 且 RejectOK=false → score 0 / FAIL
	e, ok = findEval(w.evals, "R3", "RejectOK")
	if !ok {
		t.Fatalf("缺 RejectOK 评测: %+v", w.evals)
	}
	if e.Result == nil || e.Result.Score == nil || *e.Result.Score != 0 || e.Result.Label != "FAIL" {
		t.Fatalf("RejectOK(false) = %+v", e.Result)
	}
}

func TestPushRagExperimentUsesGivenName(t *testing.T) {
	w := &fakeWriter{}
	_, err := PushRagExperiment(w, "DS", RagReport{Meta: RagMeta{Variant: "rerank=none"}}, "aiops-rag-golden/rerank=none")
	if err != nil {
		t.Fatal(err)
	}
	if w.lastExperimentName != "aiops-rag-golden/rerank=none" {
		t.Fatalf("name=%q", w.lastExperimentName)
	}
}

func TestPushRagExperimentCarriesVariantMeta(t *testing.T) {
	w := &fakeWriter{}
	if _, err := PushRagExperiment(w, "DS", RagReport{Meta: RagMeta{Variant: "fuse=weighted"}}, "p"); err != nil {
		t.Fatal(err)
	}
	if w.lastExperimentMeta["variant"] != "fuse=weighted" {
		t.Fatalf("metadata = %+v", w.lastExperimentMeta)
	}
}

func TestPushRagExperimentCountsMissingExampleAsFailure(t *testing.T) {
	rep := RagReport{
		Meta:  RagMeta{Enhance: false, TopK: 3},
		Items: []ItemResult{{ID: "q-missing", Type: "fact", TopK: 3, Behavior: "hit"}},
	}
	w := &fakeWriter{}
	sum, err := PushRagExperiment(w, "DS", rep, "p/rag")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.runs) != 0 || len(w.evals) != 0 {
		t.Fatalf("缺失 example 不应写 run/eval: runs=%d evals=%d", len(w.runs), len(w.evals))
	}
	if sum.RunsWritten != 0 || sum.EvalsWritten != 0 || sum.Failures != 1 {
		t.Fatalf("summary = %+v", sum)
	}
}
