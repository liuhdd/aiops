package eval

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAgentDatasetExamplesCoverAllTasks(t *testing.T) {
	tasks := DefaultTasks()
	exs := AgentDatasetExamples()
	if len(exs) != len(tasks) {
		t.Fatalf("examples=%d tasks=%d", len(exs), len(tasks))
	}
	byID := map[string]bool{}
	for _, e := range exs {
		byID[e.ID] = true
		if e.Metadata["task_id"] != e.ID {
			t.Fatalf("metadata.task_id 应与 example id 一致: %+v", e)
		}
		if len(e.Splits) != 1 {
			t.Fatalf("splits 应为单元素: %+v", e)
		}
	}
	for _, task := range tasks {
		if !byID[task.ID] {
			t.Fatalf("缺任务 %s", task.ID)
		}
	}
}

func TestAgentDatasetExampleShapes(t *testing.T) {
	byID := map[string]int{}
	exs := AgentDatasetExamples()
	for i, e := range exs {
		byID[e.ID] = i
	}
	pipe := exs[byID["disk-full-db-01"]]
	alert, ok := pipe.Input["alert"].(map[string]any)
	if !ok || alert["device"] != "db-01" || alert["metric"] != "disk_usage" {
		t.Fatalf("pipeline input = %v", pipe.Input)
	}
	if pipe.Output["runbook"] != "disk_usage" {
		t.Fatalf("pipeline output = %v", pipe.Output)
	}

	chat := exs[byID["chat-list-alerts"]]
	if chat.Input["chat_input"] != "有哪些告警" || chat.Input["object"] != ObjectChat {
		t.Fatalf("chat input = %v", chat.Input)
	}
	if chat.Output["intent"] != "ListAlerts" {
		t.Fatalf("chat output = %v", chat.Output)
	}
}

func TestTrialResultSerializesTimestamps(t *testing.T) {
	start := time.Date(2026, 9, 10, 1, 2, 3, 0, time.UTC)
	tr := TrialResult{Trial: 1, Config: "rule", StartedAt: start, EndedAt: start.Add(time.Second)}
	b, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"started_at"`) || !strings.Contains(s, `"ended_at"`) {
		t.Fatalf("缺少时间戳字段: %s", s)
	}
}
