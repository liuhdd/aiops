package eval

import (
	"context"
	"testing"
	"time"

	"aiops/internal/model"
)

// ctxCaptureCompleter 记录评委收到的 ctx，用于验证超时是否传递到 LLM 评委
type ctxCaptureCompleter struct {
	captured context.Context
}

func (c *ctxCaptureCompleter) Complete(ctx context.Context, _, _ string) (string, error) {
	c.captured = ctx
	return `{"score":0.8,"verdict":"PASS"}`, nil
}

// 验证调用方传入的带超时 ctx 一路流到 judge.Complete（Fix3 round2：超时下沉到调用方）
func TestGradeJudgeReceivesDeadline(t *testing.T) {
	task := Task{ID: "disk-full-db-01", Object: ObjectPipeline,
		Expected: Expected{RootCause: []string{"磁盘空间不足"}}}
	cfg := Config{Name: ConfigLLMReact}
	res := &TrialResult{Outcome: RunOutcome{
		RCA:      model.RCA{RootCause: "磁盘空间不足"},
		Evidence: model.Evidence{Metrics: []model.MetricPoint{{Value: 95}}},
	}}
	judge := &ctxCaptureCompleter{}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	Grade(ctx, task, cfg, res, judge)
	if judge.captured == nil {
		t.Fatal("judge 未收到 ctx")
	}
	if _, ok := judge.captured.Deadline(); !ok {
		t.Fatal("judge 收到的 ctx 无 deadline：超时未传递到 LLM 评委")
	}
}

func TestGradePipelineDiskFull(t *testing.T) {
	task, _ := TaskByID("disk-full-db-01")
	cfg := Config{Name: ConfigLLMReact}
	res := &TrialResult{RunStatus: model.StatusCompleted, Outcome: RunOutcome{
		RCA: model.RCA{RootCause: "磁盘空间不足"},
	}}
	scores := Grade(context.Background(), task, cfg, res, nil)
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
	task, _ := TaskByID("chat-list-alerts")
	cfg := Config{Name: ConfigLLMReact}
	res := &TrialResult{Intents: []string{"ListAlerts"}}
	scores := Grade(context.Background(), task, cfg, res, nil)
	found := map[string]GraderResult{}
	for _, s := range scores {
		found[s.Name] = s
	}
	if !found["IntentMatch"].Pass {
		t.Fatalf("IntentMatch 未通过: %+v", found["IntentMatch"])
	}
}

func TestGradeChatMultiIntent(t *testing.T) {
	task, _ := TaskByID("chat-multi")
	if len(task.Expected.Intents) != 2 {
		t.Fatalf("chat-multi 应期望 2 个意图, got %v", task.Expected.Intents)
	}
	res := &TrialResult{Intents: []string{"ListAlerts", "AskRootCause"}, Reply: "多意图聚合回复"}
	scores := gradeChat(task, res)
	found := map[string]GraderResult{}
	for _, s := range scores {
		found[s.Name] = s
	}
	if !found["IntentMatch"].Pass {
		t.Fatalf("多意图全命中应通过: %+v", found["IntentMatch"])
	}
	// 缺一个意图 → 不通过
	res2 := &TrialResult{Intents: []string{"ListAlerts"}}
	found2 := map[string]GraderResult{}
	for _, s := range gradeChat(task, res2) {
		found2[s.Name] = s
	}
	if found2["IntentMatch"].Pass {
		t.Fatalf("缺意图不应通过: %+v", found2["IntentMatch"])
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

// ---- Fix4：L1 grader 直接单测（手工构造 TrialResult，不依赖流水线） ----

func TestEvidenceMatch(t *testing.T) {
	task := Task{ID: "t", Object: ObjectPipeline, Expected: Expected{Logs: []string{"no space"}}}
	// 命中期望日志 + 有指标 => 通过
	res := &TrialResult{Outcome: RunOutcome{Evidence: model.Evidence{
		Metrics: []model.MetricPoint{{Value: 95}},
		Logs:    []model.LogEntry{{Content: "no space left on device"}},
	}}}
	if g := evidenceMatch(task, res); !g.Pass || g.Score != 1.0 {
		t.Fatalf("evidenceMatch 应通过: %+v", g)
	}
	// 无指标 => 失败
	res2 := &TrialResult{Outcome: RunOutcome{Evidence: model.Evidence{
		Logs: []model.LogEntry{{Content: "no space left"}},
	}}}
	if g := evidenceMatch(task, res2); g.Pass {
		t.Fatalf("无指标应失败: %+v", g)
	}
	// 缺期望日志 => 失败
	res3 := &TrialResult{Outcome: RunOutcome{Evidence: model.Evidence{
		Metrics: []model.MetricPoint{{Value: 95}},
		Logs:    []model.LogEntry{{Content: "unrelated"}},
	}}}
	if g := evidenceMatch(task, res3); g.Pass {
		t.Fatalf("缺期望日志应失败: %+v", g)
	}
}

func TestRecommendMatch(t *testing.T) {
	task := Task{ID: "t", Object: ObjectPipeline, Expected: Expected{Runbook: "disk_usage"}}
	// actions 非空 + runbook 匹配 => 通过
	res := &TrialResult{Outcome: RunOutcome{Recommend: model.Recommendation{
		Actions: []string{"清理磁盘"}, RunbookRef: "disk_usage",
	}}}
	if g := recommendMatch(task, res); !g.Pass || g.Score != 1.0 {
		t.Fatalf("recommendMatch 应通过: %+v", g)
	}
	// runbook 不匹配 => 失败
	res2 := &TrialResult{Outcome: RunOutcome{Recommend: model.Recommendation{
		Actions: []string{"清理磁盘"}, RunbookRef: "gpu_util",
	}}}
	if g := recommendMatch(task, res2); g.Pass {
		t.Fatalf("runbook 不匹配应失败: %+v", g)
	}
	// actions 为空 => 失败
	res3 := &TrialResult{Outcome: RunOutcome{Recommend: model.Recommendation{RunbookRef: "disk_usage"}}}
	if g := recommendMatch(task, res3); g.Pass {
		t.Fatalf("空 actions 应失败: %+v", g)
	}
}

func TestEdgeRootCause(t *testing.T) {
	task := Task{ID: "gpu-temp-compute-02", Object: ObjectPipeline,
		Expected: Expected{RootCause: []string{"温度过高", "散热", "过温"}}}
	// 未识别根因时不通过。/ An unidentified root cause fails.
	res := &TrialResult{Outcome: RunOutcome{RCA: model.RCA{RootCause: "未知根因，需人工介入"}}}
	g := edgeRootCause(task, res)
	if g.Pass || g.Score != 0.1 {
		t.Fatalf("未知根因应 Pass=false/Score=0.1: %+v", g)
	}
	// 命中温度关键词 => Pass=true, Score=1.0
	res2 := &TrialResult{Outcome: RunOutcome{RCA: model.RCA{RootCause: "GPU温度过高导致热保护"}}}
	g2 := edgeRootCause(task, res2)
	if !g2.Pass || g2.Score != 1.0 {
		t.Fatalf("温度关键词应 Pass=true/Score=1.0: %+v", g2)
	}
}

func TestRootCauseMatchEmptyExpected(t *testing.T) {
	// 期望根因为空（防御）：不 panic，判失败
	task := Task{ID: "t", Object: ObjectPipeline, Expected: Expected{}}
	res := &TrialResult{Outcome: RunOutcome{RCA: model.RCA{RootCause: "任意根因"}}}
	if g := rootCauseMatch(task, res); g.Pass {
		t.Fatalf("空 Expected.RootCause 应判失败: %+v", g)
	}
}
