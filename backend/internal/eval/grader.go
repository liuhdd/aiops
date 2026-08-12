package eval

import (
	"context"
	"strings"

	"aiops/internal/agent"
	ctxpkg "aiops/internal/context"
	"aiops/internal/model"
)

// GraderResult 单个评分器的输出
type GraderResult struct {
	Name   string
	Pass   bool
	Score  float64 // 0~1
	Detail string
	NA     bool // 不适用（如无 LLM key 时 LLM 评委）
	Weight float64
}

// ScoredTrial 带评分的 trial
type ScoredTrial struct {
	*TrialResult
	Scores []GraderResult
	Pass   bool
	Score  float64
}

// Grade 对一次 trial 应用全部评分器（judge 为 nil 或 typed-nil 时跳过 LLM 评委）
func Grade(t Task, cfg Config, res *TrialResult, judge ctxpkg.Completer) []GraderResult {
	var out []GraderResult
	switch t.Object {
	case ObjectPipeline:
		out = gradePipeline(t, res)
	case ObjectChat:
		out = gradeChat(t, res)
	case ObjectContext:
		out = gradeContext(t, res)
	}
	// L2 Model-based：LLM 评委（可选）
	if judge != nil && !ctxpkg.IsNilCompleter(judge) {
		out = append(out, gradeWithJudge(t, res, judge)...)
	}
	return out
}

func gradePipeline(t Task, res *TrialResult) []GraderResult {
	out := []GraderResult{
		stageComplete(t, res),
		rootCauseMatch(t, res),
		evidenceMatch(t, res),
		recommendMatch(t, res),
	}
	if t.ID == "gpu-temp-compute-02" {
		out = append(out, edgeRootCause(t, res))
	}
	return out
}

func stageComplete(t Task, res *TrialResult) GraderResult {
	ok := res.RunStatus == model.StatusCompleted
	detail := "run status=" + res.RunStatus
	if ok {
		detail = "6 阶段全部 completed"
	}
	return GraderResult{Name: "StageComplete", Pass: ok, Score: boolScore(ok), Detail: detail, Weight: 0.25}
}

func rootCauseMatch(t Task, res *TrialResult) GraderResult {
	rc := res.Outcome.RCA.RootCause
	ok := strings.Contains(rc, t.Expected.RootCause[0])
	if !ok && len(t.Expected.RootCause) > 1 {
		for _, kw := range t.Expected.RootCause[1:] {
			if strings.Contains(rc, kw) {
				ok = true
				break
			}
		}
	}
	return GraderResult{Name: "RootCauseMatch", Pass: ok, Score: boolScore(ok),
		Detail: "root_cause=" + rc, Weight: 0.3}
}

func evidenceMatch(t Task, res *TrialResult) GraderResult {
	ok := true
	var msgs []string
	if len(res.Outcome.Evidence.Metrics) == 0 {
		ok = false
		msgs = append(msgs, "无指标")
	}
	logOK := false
	for _, kw := range t.Expected.Logs {
		for _, log := range res.Outcome.Evidence.Logs {
			if strings.Contains(log.Content, kw) {
				logOK = true
				break
			}
		}
		if logOK {
			break
		}
	}
	if !logOK {
		ok = false
		msgs = append(msgs, "缺期望日志")
	}
	if t.Expected.NoGaps && len(res.Outcome.Evidence.Gaps) > 0 {
		ok = false
		msgs = append(msgs, "证据有 gaps")
	}
	return GraderResult{Name: "EvidenceMatch", Pass: ok, Score: boolScore(ok),
		Detail: strings.Join(msgs, "; "), Weight: 0.2}
}

func recommendMatch(t Task, res *TrialResult) GraderResult {
	ok := true
	if t.Expected.Runbook != "" && res.Outcome.Recommend.RunbookRef != t.Expected.Runbook {
		ok = false
	}
	if len(res.Outcome.Recommend.Actions) == 0 {
		ok = false
	}
	return GraderResult{Name: "RecommendMatch", Pass: ok, Score: boolScore(ok),
		Detail: "runbook=" + res.Outcome.Recommend.RunbookRef, Weight: 0.15}
}

// edgeRootCause gpu-temp 边界任务：规则路径判"未知根因"记低分，LLM 期望命中温度关键词
func edgeRootCause(t Task, res *TrialResult) GraderResult {
	rc := res.Outcome.RCA.RootCause
	if strings.Contains(rc, "未知根因") {
		return GraderResult{Name: "EdgeRootCause", Pass: false, Score: 0.1,
			Detail: "规则路径未知根因（边界任务允许低分）", Weight: 0.1}
	}
	for _, kw := range t.Expected.RootCause {
		if strings.Contains(rc, kw) {
			return GraderResult{Name: "EdgeRootCause", Pass: true, Score: 1,
				Detail: "LLM 推理出根因: " + rc, Weight: 0.1}
		}
	}
	return GraderResult{Name: "EdgeRootCause", Pass: false, Score: 0.2,
		Detail: "根因不符: " + rc, Weight: 0.1}
}

func gradeChat(t Task, res *TrialResult) []GraderResult {
	intent := agent.ParseIntent(t.ChatInput)
	ok := intentNameStr(intent) == t.Expected.Intent
	detail := "intent=" + intentNameStr(intent)
	if ok {
		detail += " reply=" + truncate(res.Reply, 80)
	}
	out := []GraderResult{{
		Name: "IntentMatch", Pass: ok, Score: boolScore(ok), Detail: detail, Weight: 0.4,
	}}
	// 期望回复包含关键词（如根因/处置）时追加内容检查
	if len(t.Expected.RootCause) > 0 {
		contentOK := false
		for _, kw := range t.Expected.RootCause {
			if strings.Contains(res.Reply, kw) {
				contentOK = true
				break
			}
		}
		out = append(out, GraderResult{Name: "ReplyContent", Pass: contentOK,
			Score: boolScore(contentOK), Detail: "reply=" + truncate(res.Reply, 80), Weight: 0.6})
	}
	return out
}

func gradeContext(t Task, res *TrialResult) []GraderResult {
	ok := true
	var got []string
	for _, ev := range res.Outcome.ToolEvents {
		got = append(got, ev.ToolName)
	}
	for _, want := range t.Expected.ToolUse {
		if !contains(got, want) {
			ok = false
		}
	}
	if len(res.Outcome.Scratchpad) == 0 {
		ok = false
	}
	return []GraderResult{{
		Name: "ToolUseCheck", Pass: ok, Score: boolScore(ok),
		Detail: "tools=" + strings.Join(got, ","), Weight: 0.6,
	}}
}

// gradeWithJudge L2 Model-based 评分器（LLM 评委，按维度拆分）
func gradeWithJudge(t Task, res *TrialResult, judge ctxpkg.Completer) []GraderResult {
	if t.Object != ObjectPipeline || res.Outcome.RCA.RootCause == "" {
		return nil
	}
	dimensions := []struct{ name, rubric string }{
		{"JudgeAccuracy", "评估根因判定是否准确：根因表述是否与证据链自洽、是否命中真实异常类型。"},
		{"JudgeGroundedness", "评估根因是否有证据支持：证据链与日志/指标是否支撑结论，是否出现幻觉式编造。"},
	}
	var out []GraderResult
	for _, d := range dimensions {
		system := "你是评测评委。只输出 JSON: {\"score\":0~1,\"verdict\":\"PASS|FAIL|INSUFFICIENT_INFO\"}。若信息不足判断必须返回 INSUFFICIENT_INFO，不要猜测。"
		user := "评测任务: " + t.ID + "\n证据:" + mustJSON(res.Outcome.Evidence) +
			"\nRCA:" + mustJSON(res.Outcome.RCA) + "\n评分维度: " + d.rubric
		raw, err := judge.Complete(context.Background(), system, user)
		if err != nil || strings.TrimSpace(raw) == "" {
			out = append(out, GraderResult{Name: d.name, NA: true, Weight: 0.2})
			continue
		}
		var v struct {
			Score   float64 `json:"score"`
			Verdict string  `json:"verdict"`
		}
		if unmarshalJSON(raw, &v) != nil || v.Verdict == "" {
			out = append(out, GraderResult{Name: d.name, NA: true, Weight: 0.2})
			continue
		}
		if v.Verdict == "INSUFFICIENT_INFO" {
			out = append(out, GraderResult{Name: d.name, NA: true, Weight: 0.2})
			continue
		}
		out = append(out, GraderResult{Name: d.name, Pass: v.Verdict == "PASS" && v.Score >= 0.5,
			Score: v.Score, Detail: v.Verdict, Weight: 0.2})
	}
	return out
}

// WeightedScore 各评分器按权重求和（NA 项剔除后权重归一化）
func WeightedScore(scores []GraderResult) float64 {
	var sum, wsum float64
	for _, s := range scores {
		if s.NA {
			continue
		}
		if s.Weight == 0 {
			s.Weight = 1
		}
		sum += s.Score * s.Weight
		wsum += s.Weight
	}
	if wsum == 0 {
		return 0
	}
	return sum / wsum
}

func boolScore(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
