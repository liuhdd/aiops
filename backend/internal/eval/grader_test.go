package eval

import (
	"context"
	"testing"

	"aiops/internal/mockdata"
)

func TestGradePipelineRuleDiskFull(t *testing.T) {
	mockdata.GenerateAll(42)
	task, _ := TaskByID("disk-full-db-01")
	cfg := Config{Name: ConfigRule}
	res, err := RunTask(context.Background(), task, cfg, nil, 1)
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	scores := Grade(task, cfg, res, nil)
	// rule 路径确定性：RootCauseMatch 应通过
	found := map[string]GraderResult{}
	for _, s := range scores {
		found[s.Name] = s
	}
	if !found["RootCauseMatch"].Pass {
		t.Fatalf("RootCauseMatch 未通过: %+v", found["RootCauseMatch"])
	}
	if !found["StageComplete"].Pass {
		t.Fatalf("StageComplete 未通过: %+v", found["StageComplete"])
	}
}

func TestGradeChatIntent(t *testing.T) {
	mockdata.GenerateAll(42)
	task, _ := TaskByID("chat-list-alerts")
	cfg := Config{Name: ConfigRule}
	res, err := RunTask(context.Background(), task, cfg, nil, 1)
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	scores := Grade(task, cfg, res, nil)
	found := map[string]GraderResult{}
	for _, s := range scores {
		found[s.Name] = s
	}
	if !found["IntentMatch"].Pass {
		t.Fatalf("IntentMatch 未通过: %+v", found["IntentMatch"])
	}
}

func TestWeightedScoreNA(t *testing.T) {
	scores := []GraderResult{
		{Name: "a", Pass: true, Score: 1, Weight: 0.5},
		{Name: "b", Pass: false, Score: 0, Weight: 0.5, NA: true}, // NA 不参与
	}
	if s := WeightedScore(scores); s != 1.0 {
		t.Fatalf("WeightedScore = %v, want 1.0", s)
	}
}
