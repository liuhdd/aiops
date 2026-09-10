package eval

import "aiops/internal/phoenix"

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
