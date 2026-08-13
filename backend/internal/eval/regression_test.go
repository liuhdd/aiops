package eval

import (
	"context"
	"testing"

	"aiops/internal/mockdata"
)

// 回归护栏：rule 路径下 pipeline 主任务（5 个已知异常场景）pass@k 必须全过
func TestRuleRegressionMainTasks(t *testing.T) {
	mockdata.GenerateAll(42)
	ctx := context.Background()
	cfg := Config{Name: ConfigRule}
	mainIDs := []string{
		"cpu-hot-compute-01", "disk-full-db-01", "gpu-drop-compute-02",
		"mem-oom-web-01", "net-down-switch",
	}
	for _, id := range mainIDs {
		task, ok := TaskByID(id)
		if !ok {
			t.Fatalf("任务 %s 不存在", id)
		}
		res, err := RunTask(ctx, task, cfg, nil, 1)
		if err != nil {
			t.Fatalf("%s RunTask: %v", id, err)
		}
		scores := Grade(ctx, task, cfg, res, nil)
		st := ScoredTrial{TrialResult: res, Scores: scores}
		st.Score = WeightedScore(scores)
		st.Pass = st.Score >= 0.6
		if !st.Pass {
			t.Fatalf("%s rule 路径未通过: score=%.2f scores=%+v", id, st.Score, scores)
		}
	}
}

// 回归护栏：gpu-temp 是 L2 边界任务，规则路径无映射 → "未知根因"，EdgeRootCause 记 0.1，
// 总分应低于 0.6 阈值（配置对比的价值所在：LLM 路径才应通过）。
func TestRuleRegressionGpuTempFails(t *testing.T) {
	mockdata.GenerateAll(42)
	ctx := context.Background()
	cfg := Config{Name: ConfigRule}
	task, ok := TaskByID("gpu-temp-compute-02")
	if !ok {
		t.Fatalf("任务 gpu-temp-compute-02 不存在")
	}
	res, err := RunTask(ctx, task, cfg, nil, 1)
	if err != nil {
		t.Fatalf("gpu-temp-compute-02 RunTask: %v", err)
	}
	scores := Grade(ctx, task, cfg, res, nil)
	st := ScoredTrial{TrialResult: res, Scores: scores}
	st.Score = WeightedScore(scores)
	st.Pass = st.Score >= 0.6
	if st.Score >= 0.6 {
		t.Fatalf("gpu-temp-compute-02 rule 路径不应通过 0.6 阈值: score=%.2f scores=%+v", st.Score, scores)
	}
}
