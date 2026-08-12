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
		scores := Grade(task, cfg, res, nil)
		st := ScoredTrial{TrialResult: res, Scores: scores}
		st.Score = WeightedScore(scores)
		st.Pass = st.Score >= 0.6
		if !st.Pass {
			t.Fatalf("%s rule 路径未通过: score=%.2f scores=%+v", id, st.Score, scores)
		}
	}
}
