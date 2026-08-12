package eval

import (
	"context"
	"encoding/json"
	"testing"

	"aiops/internal/mockdata"
)

func TestPassAtK(t *testing.T) {
	// 3 trial，2 通过 => pass@k=1.0（至少一次成功）
	tr := []ScoredTrial{
		{TrialResult: &TrialResult{}, Pass: true, Score: 0.9},
		{TrialResult: &TrialResult{}, Pass: false, Score: 0.3},
		{TrialResult: &TrialResult{}, Pass: true, Score: 0.8},
	}
	if got := passAtK(tr); got != 1.0 {
		t.Fatalf("passAtK = %v, want 1.0", got)
	}
	// 全失败 => 0
	tr2 := []ScoredTrial{{TrialResult: &TrialResult{}, Pass: false}}
	if got := passAtK(tr2); got != 0 {
		t.Fatalf("passAtK = %v, want 0", got)
	}
}

func TestMeanStddev(t *testing.T) {
	scores := []float64{0.8, 0.9, 1.0}
	m, sd := meanStddev(scores)
	if m < 0.899 || m > 0.901 {
		t.Fatalf("mean = %v", m)
	}
	if sd < 0.08 || sd > 0.082 {
		t.Fatalf("stddev = %v", sd)
	}
}

func TestBuildReportJSON(t *testing.T) {
	mockdata.GenerateAll(42)
	task, _ := TaskByID("disk-full-db-01")
	cfg := Config{Name: ConfigRule}
	res, err := RunTask(context.Background(), task, cfg, nil, 1)
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	sc := ScoredTrial{TrialResult: res, Scores: Grade(task, cfg, res, nil)}
	sc.Score = WeightedScore(sc.Scores)
	sc.Pass = sc.Score >= 0.6
	rep := BuildReport([]Task{task}, []Config{cfg},
		map[string][]ScoredTrial{"disk-full-db-01|rule": {sc}})
	if rep.Summary.Overall.PassAtK < 0.9 {
		t.Fatalf("overall pass_at_k = %v, want >=0.9", rep.Summary.Overall.PassAtK)
	}
	b, err := rep.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("报告不是合法 JSON: %v", err)
	}
	if m["summary"] == nil {
		t.Fatal("报告缺 summary 字段")
	}
}
