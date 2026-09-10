package eval

import (
	"fmt"
	"time"

	"aiops/internal/phoenix"
)

// AgentDatasetName Phoenix 上 agent 评测数据集名称。
const AgentDatasetName = "aiops-agents-eval"

// AgentDatasetExamples 把 DefaultTasks() 映射为 Phoenix 数据集样本。
func AgentDatasetExamples() []phoenix.Example {
	tasks := DefaultTasks()
	out := make([]phoenix.Example, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, taskExample(t))
	}
	return out
}

func taskExample(t Task) phoenix.Example {
	input := map[string]any{"object": t.Object}
	if t.Object == ObjectChat {
		input["chat_input"] = t.ChatInput
	} else {
		input["alert"] = map[string]any{
			"device": t.Alert.Device, "metric": t.Alert.Metric,
			"severity": t.Alert.Severity, "message": t.Alert.Message,
		}
	}
	output := map[string]any{}
	if len(t.Expected.RootCause) > 0 {
		output["root_cause_keywords"] = t.Expected.RootCause
	}
	if len(t.Expected.Logs) > 0 {
		output["log_keywords"] = t.Expected.Logs
	}
	if t.Expected.NoGaps {
		output["no_gaps"] = true
	}
	if t.Expected.Runbook != "" {
		output["runbook"] = t.Expected.Runbook
	}
	if t.Expected.Intent != "" {
		output["intent"] = t.Expected.Intent
	}
	if len(t.Expected.Intents) > 0 {
		output["intents"] = t.Expected.Intents
	}
	if len(t.Expected.ToolUse) > 0 {
		output["tool_use"] = t.Expected.ToolUse
	}
	return phoenix.Example{
		ID: t.ID, Input: input, Output: output,
		Metadata: map[string]any{"task_id": t.ID, "object": t.Object, "configs": t.Configs},
		Splits:   []string{t.Object},
	}
}

// PushAgentExperiment 把评测报告回写为 Phoenix 实验：每 config 一实验，每 trial 一 run，
// 每非 NA grader 一条 evaluation，另加一条 WeightedScore 总体评测。
// 回写按项继续，返回各实验汇总与首个错误。
func PushAgentExperiment(w phoenix.Writer, datasetID string, report Report, cfgFilter []string, namePrefix string) ([]phoenix.ExperimentSummary, error) {
	exs, err := w.ListExamples(datasetID, "")
	if err != nil {
		return nil, fmt.Errorf("列数据集失败: %w", err)
	}
	byTask := map[string]string{}
	for _, e := range exs {
		if id, ok := e.Metadata["task_id"].(string); ok {
			byTask[id] = e.ID
		}
	}
	var summaries []phoenix.ExperimentSummary
	var firstErr error
	for _, cfgName := range report.Meta.Configs {
		if len(cfgFilter) > 0 && !contains(cfgFilter, cfgName) {
			continue
		}
		type trialRef struct {
			taskID string
			tr     ScoredTrial
		}
		var refs []trialRef
		for _, tr := range report.Tasks {
			cs, ok := tr.Configs[cfgName]
			if !ok {
				continue
			}
			for _, sc := range cs.Trials {
				refs = append(refs, trialRef{tr.ID, sc})
			}
		}
		if len(refs) == 0 {
			continue
		}
		maxRep := 1
		for _, r := range refs {
			if r.tr.Trial > maxRep {
				maxRep = r.tr.Trial
			}
		}
		exp, err := w.CreateExperiment(datasetID, namePrefix+"/"+cfgName, maxRep, map[string]any{
			"config": cfgName, "ts": report.Meta.Timestamp.Format(time.RFC3339), "llm_enabled": report.Meta.LLMEnabled,
		})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		sum := phoenix.ExperimentSummary{Name: namePrefix + "/" + cfgName, ID: exp.ID}
		for _, r := range refs {
			exID, ok := byTask[r.taskID]
			if !ok {
				sum.Failures++
				continue
			}
			sc := r.tr
			start, end := sc.StartedAt, sc.EndedAt
			if start.IsZero() {
				start = report.Meta.Timestamp
			}
			if end.IsZero() {
				end = report.Meta.Timestamp
			}
			runID, err := w.LogRun(exp.ID, phoenix.RunRequest{
				DatasetExampleID: exID,
				Output:           runOutput(sc.TrialResult),
				RepetitionNumber: sc.Trial,
				StartTime:        start,
				EndTime:          end,
				Error:            sc.ErrMsg,
			})
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				sum.Failures++
				continue
			}
			sum.RunsWritten++
			for _, g := range sc.Scores {
				if g.NA {
					continue
				}
				kind := "CODE"
				if g.Name == "JudgeAccuracy" || g.Name == "JudgeGroundedness" {
					kind = "LLM"
				}
				score := g.Score
				if _, err := w.LogEvaluation(phoenix.EvaluationRequest{
					ExperimentRunID: runID, Name: g.Name, AnnotatorKind: kind,
					StartTime: start, EndTime: end,
					Result:   &phoenix.EvaluationResult{Score: &score, Label: passLabel(g.Pass), Explanation: g.Detail},
					Metadata: map[string]any{"weight": g.Weight},
				}); err != nil {
					if firstErr == nil {
						firstErr = err
					}
					sum.Failures++
				} else {
					sum.EvalsWritten++
				}
			}
			score := sc.Score
			if _, err := w.LogEvaluation(phoenix.EvaluationRequest{
				ExperimentRunID: runID, Name: "WeightedScore", AnnotatorKind: "CODE",
				StartTime: start, EndTime: end,
				Result: &phoenix.EvaluationResult{Score: &score, Label: passLabel(sc.Pass), Explanation: sc.RunStatus},
			}); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				sum.Failures++
			} else {
				sum.EvalsWritten++
			}
		}
		summaries = append(summaries, sum)
	}
	return summaries, firstErr
}

func runOutput(res *TrialResult) map[string]any {
	if res == nil {
		return map[string]any{}
	}
	// 展开为可序列化 map，避免 Phoenix 收到 Go 结构体字段名。
	return map[string]any{
		"run_status": res.RunStatus,
		"rca":        res.Outcome.RCA,
		"recommend":  res.Outcome.Recommend,
		"reply":      res.Reply,
		"intent":     res.Intent,
		"intents":    res.Intents,
		"err_msg":    res.ErrMsg,
	}
}

func passLabel(pass bool) string {
	if pass {
		return "PASS"
	}
	return "FAIL"
}
