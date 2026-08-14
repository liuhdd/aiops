package eval

import (
	"context"
	"strings"
	"testing"

	"aiops/internal/mockdata"
)

func TestRunTaskRuleDiskFull(t *testing.T) {
	mockdata.GenerateAll(42)
	ctx := context.Background()
	task, _ := TaskByID("disk-full-db-01")
	cfg := Config{Name: ConfigRule}
	res, err := RunTask(ctx, task, cfg, nil, 1)
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if res.RunStatus != "completed" {
		t.Fatalf("run status = %s, want completed", res.RunStatus)
	}
	if res.Outcome.RCA.RootCause == "" {
		t.Fatal("RCA 根因为空")
	}
	if len(res.Outcome.Stages) != 6 {
		t.Fatalf("stage 数 = %d, want 6", len(res.Outcome.Stages))
	}
}

func TestRunTaskChatIntent(t *testing.T) {
	mockdata.GenerateAll(42)
	ctx := context.Background()
	task, _ := TaskByID("chat-list-alerts")
	cfg := Config{Name: ConfigRule}
	res, err := RunTask(ctx, task, cfg, nil, 1)
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if res.Intent != "ListAlerts" {
		t.Fatalf("intent = %s, want ListAlerts", res.Intent)
	}
}

func TestRunTaskChatRecommendRule(t *testing.T) {
	mockdata.GenerateAll(42)
	ctx := context.Background()
	task, _ := TaskByID("chat-recommend-gpu")
	cfg := Config{Name: ConfigRule}
	res, err := RunTask(ctx, task, cfg, nil, 1)
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if !strings.Contains(res.Reply, "处置建议") {
		t.Fatalf("reply 不含处置建议: %q", res.Reply)
	}
}

func TestRunTaskChatMultiRuleFallback(t *testing.T) {
	mockdata.GenerateAll(42)
	task, _ := TaskByID("chat-multi")
	// rule 配置（无 LLM）下回退单意图规则解析
	res, err := RunTask(context.Background(), task, Config{Name: ConfigRule}, nil, 1)
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if len(res.Intents) == 0 {
		t.Fatal("res.Intents 不应为空")
	}
}

// Fix2：未知 chat 任务 ID（未命中 scene 表）仍应返回非 nil 的部分结果，失败 trial 不丢失。
func TestRunTaskChatUnknownIDReturnsPartial(t *testing.T) {
	mockdata.GenerateAll(42)
	task := Task{ID: "chat-unknown-scene", Object: ObjectChat, ChatInput: "为什么 db-01 磁盘满了？",
		Expected: Expected{Intent: "AskRootCause"}}
	res, err := RunTask(context.Background(), task, Config{Name: ConfigRule}, nil, 1)
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if res == nil {
		t.Fatal("未知 chat 任务应返回非 nil 的部分结果")
	}
	if res.RunStatus != "chat" {
		t.Fatalf("RunStatus = %q, want chat", res.RunStatus)
	}
}

// Fix2：失败 trial 仍可被评分并记为失败。
// runPipeline 出错时返回带 RunStatus=failed 的非 nil res；此处直接构造该状态，
// 验证 Grade 产生 StageComplete=false（呼应用户决策：failed trial 出现在报告中而非消失）。
func TestGradeFailedRunRecordedAsFail(t *testing.T) {
	task, _ := TaskByID("disk-full-db-01")
	cfg := Config{Name: ConfigRule}
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
