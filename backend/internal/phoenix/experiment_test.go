package phoenix

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCreateExperiment(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/datasets/DS1/experiments" {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"data":{"id":"E1","dataset_id":"DS1","dataset_version_id":"V1","name":"n"}}`)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	exp, err := c.CreateExperiment("DS1", "n", 3, map[string]any{"config": "rule"})
	if err != nil || exp.ID != "E1" || exp.DatasetVersionID != "V1" {
		t.Fatalf("CreateExperiment = (%+v,%v)", exp, err)
	}
	if body["name"] != "n" || body["repetitions"].(float64) != 3 {
		t.Fatalf("body = %v", body)
	}
}

func TestLogRun(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/experiments/E1/runs" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"data":{"id":"R1"}}`)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	start := time.Date(2026, 9, 10, 1, 0, 0, 0, time.UTC)
	id, err := c.LogRun("E1", RunRequest{
		DatasetExampleID: "EX1", Output: map[string]any{"x": 1},
		RepetitionNumber: 2, StartTime: start, EndTime: start.Add(time.Second), Error: "boom",
	})
	if err != nil || id != "R1" {
		t.Fatalf("LogRun = (%q,%v)", id, err)
	}
	if body["dataset_example_id"] != "EX1" || body["repetition_number"].(float64) != 2 || body["error"] != "boom" {
		t.Fatalf("body = %v", body)
	}
}

func TestLogEvaluation(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/experiment_evaluations" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"data":{"id":"EV1"}}`)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	score := 0.5
	id, err := c.LogEvaluation(EvaluationRequest{
		ExperimentRunID: "R1", Name: "RootCauseMatch", AnnotatorKind: "CODE",
		StartTime: time.Now(), EndTime: time.Now(),
		Result: &EvaluationResult{Score: &score, Label: "FAIL", Explanation: "x"},
	})
	if err != nil || id != "EV1" {
		t.Fatalf("LogEvaluation = (%q,%v)", id, err)
	}
	if body["annotator_kind"] != "CODE" || body["result"].(map[string]any)["score"].(float64) != 0.5 {
		t.Fatalf("body = %v", body)
	}
}
