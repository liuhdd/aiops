package eval

import (
	"context"
	"errors"
	"testing"

	"aiops/internal/llm"
)

func TestRunTaskRequiresLLM(t *testing.T) {
	for _, id := range []string{"disk-full-db-01", "chat-list-alerts", "react-gpu-evidence"} {
		t.Run(id, func(t *testing.T) {
			task, ok := TaskByID(id)
			if !ok {
				t.Fatalf("任务 %s 不存在", id)
			}
			res, err := RunTask(context.Background(), task, Config{Name: ConfigLLMReact}, nil, 1)
			if !errors.Is(err, llm.ErrNoAPIKey) {
				t.Fatalf("缺模型应明确报错，res=%+v err=%v", res, err)
			}
		})
	}
}

// Fix2：失败 trial 仍可被评分并记为失败。
// runPipeline 出错时返回带 RunStatus=failed 的非 nil res；此处直接构造该状态，
// 验证 Grade 产生 StageComplete=false（呼应用户决策：failed trial 出现在报告中而非消失）。
func TestGradeFailedRunRecordedAsFail(t *testing.T) {
	task, _ := TaskByID("disk-full-db-01")
	cfg := Config{Name: ConfigLLMReact}
	res := &TrialResult{Trial: 1, Config: cfg.Name, RunStatus: "failed", ErrMsg: "模拟引擎错误"}
	scores := Grade(context.Background(), task, cfg, res, nil)
	st := ScoredTrial{TrialResult: res, Scores: scores}
	st.Score = WeightedScore(scores)
	st.Pass = st.Score >= 0.6
	for _, s := range scores {
		if s.Name == "StageComplete" && s.Pass {
			t.Fatalf("failed trial 的 StageComplete 不应通过: %+v", s)
		}
	}
	if st.Pass {
		t.Fatalf("failed trial 不应标记为通过: score=%.2f", st.Score)
	}
}
