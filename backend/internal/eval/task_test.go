package eval

import (
	"testing"
	"time"

	"aiops/internal/mockdata"
)

func TestDefaultTasksNotEmpty(t *testing.T) {
	tasks := DefaultTasks()
	if len(tasks) == 0 {
		t.Fatal("DefaultTasks() 返回空任务集")
	}
	ids := map[string]bool{}
	for _, tk := range tasks {
		if ids[tk.ID] {
			t.Fatalf("任务ID重复: %s", tk.ID)
		}
		ids[tk.ID] = true
	}
}

func TestTaskGroundTruthMatchesMockdata(t *testing.T) {
	mockdata.GenerateAll(42)
	now := time.Now()
	for _, tk := range DefaultTasks() {
		if tk.Object != "pipeline" || len(tk.Expected.Logs) == 0 {
			continue
		}
		// 期望日志关键字必须能在 mockdata 中检索到（防止任务漂移）
		logs, err := mockdata.SearchLogs(tk.Alert.Device, tk.Expected.Logs, now.Add(-3*time.Hour), now)
		if err != nil {
			t.Fatalf("task %s SearchLogs: %v", tk.ID, err)
		}
		if len(logs) == 0 {
			t.Fatalf("task %s 期望日志关键字 %v 在 mockdata 中无匹配", tk.ID, tk.Expected.Logs)
		}
	}
}

func TestTaskRunbookExists(t *testing.T) {
	for _, tk := range DefaultTasks() {
		if tk.Object != "pipeline" || tk.Expected.Runbook == "" {
			continue
		}
		if _, ok := mockdata.SearchRunbook(tk.Expected.Runbook); !ok {
			t.Fatalf("task %s runbook %q 不存在", tk.ID, tk.Expected.Runbook)
		}
	}
}

func TestTaskByID(t *testing.T) {
	if tk, ok := TaskByID("disk-full-db-01"); !ok {
		t.Fatal("TaskByID 未找到 disk-full-db-01")
	} else if tk.Expected.RootCause[0] == "" {
		t.Fatal("root_cause 为空")
	}
}

func TestValidateTasks(t *testing.T) {
	if err := ValidateTasks(); err != nil {
		t.Fatalf("ValidateTasks: %v", err)
	}
	// 无歧义：每个 pipeline 任务的告警 device/metric 组合唯一
	seen := map[string]bool{}
	for _, tk := range DefaultTasks() {
		if tk.Object == "pipeline" {
			key := tk.Alert.Device + "|" + tk.Alert.Metric
			if seen[key] {
				t.Fatalf("pipeline 任务 device+metric 重复: %s", key)
			}
			seen[key] = true
		}
	}
}

func TestChatTasksIntentCoverage(t *testing.T) {
	// 应争议任务集（Expected.Intent == "Unknown"）
	wantDisputed := map[string]bool{
		"chat-irrelevant":       true,
		"chat-ambiguous":        true,
		"chat-greeting":         true,
		"chat-weather-offtopic": true,
		"chat-reverse-thanks":   true,
		"chat-reverse-time":     true,
		"chat-reverse-meaning":  true,
		"chat-reverse-nonsense": true,
	}
	found := map[string]bool{}
	for _, tk := range DefaultTasks() {
		if tk.Object != ObjectChat {
			continue
		}
		if isShouldDisputed(tk) {
			found[tk.ID] = true
		}
	}
	if len(found) != len(wantDisputed) {
		t.Fatalf("应争议任务数 = %d, want %d: %v", len(found), len(wantDisputed), found)
	}
	for id := range wantDisputed {
		if !found[id] {
			t.Fatalf("缺应争议任务 %s", id)
		}
	}
}

func TestChatMultiRecommendExists(t *testing.T) {
	tk, ok := TaskByID("chat-multi-recommend")
	if !ok {
		t.Fatal("缺 chat-multi-recommend 任务")
	}
	if len(tk.Expected.Intents) != 2 {
		t.Fatalf("chat-multi-recommend 期望 2 个意图, got %v", tk.Expected.Intents)
	}
}

func TestNewPipelineTasks(t *testing.T) {
	for _, id := range []string{"cpu-hot-db-01", "mem-oom-compute-01", "disk-full-web-01", "disk-full-storage-01"} {
		tk, ok := TaskByID(id)
		if !ok {
			t.Fatalf("缺 pipeline 任务 %s", id)
		}
		if !tk.Extended {
			t.Fatalf("新题应为 Extended: %+v", tk)
		}
	}
}

func TestChatScenesCoverPositiveIntents(t *testing.T) {
	// 触发流水线的正例 chat 意图必须已播种告警场景，否则 inspect/根因/建议路径不会被实际执行。
	positive := map[string]bool{
		"AskRootCause":      true,
		"AskRecommendation": true,
		"InspectAlert":      true,
	}
	for _, tk := range DefaultTasks() {
		if tk.Object != ObjectChat || !positive[tk.Expected.Intent] {
			continue
		}
		if chatScenes[tk.ID] == ([4]string{}) {
			t.Fatalf("chat 任务 %s（intent=%s）缺 scene 播种", tk.ID, tk.Expected.Intent)
		}
	}
}

func TestNewChatTasks(t *testing.T) {
	single := map[string]string{
		"chat-root-cause-cpu":    "AskRootCause",
		"chat-recommend-disk":    "AskRecommendation",
		"chat-status-progress-2": "AskStatus",
		"chat-list-alerts-2":     "ListAlerts",
		"chat-inspect-db":        "InspectAlert",
		"chat-root-cause-gpu":    "AskRootCause",
		"chat-recommend-mem":     "AskRecommendation",
		"chat-root-cause-net":    "AskRootCause",
		"chat-recommend-storage": "AskRecommendation",
		"chat-status-db":         "AskStatus",
	}
	for id, want := range single {
		tk, ok := TaskByID(id)
		if !ok {
			t.Fatalf("缺 chat 任务 %s", id)
		}
		if tk.Expected.Intent != want || !tk.Extended {
			t.Fatalf("%s = %+v（want intent=%s, Extended）", id, tk.Expected, want)
		}
	}
	multi := map[string][]string{
		"chat-multi-rootcause-recommend": {"AskRootCause", "AskRecommendation"},
		"chat-multi-list-status":         {"ListAlerts", "AskStatus"},
		"chat-multi-three":               {"AskRootCause", "AskRecommendation", "AskStatus"},
	}
	for id, want := range multi {
		tk, ok := TaskByID(id)
		if !ok {
			t.Fatalf("缺 chat 任务 %s", id)
		}
		if len(tk.Expected.Intents) != len(want) {
			t.Fatalf("%s intents = %v, want %v", id, tk.Expected.Intents, want)
		}
		for i := range want {
			if tk.Expected.Intents[i] != want[i] {
				t.Fatalf("%s intents = %v, want %v", id, tk.Expected.Intents, want)
			}
		}
		if !tk.Extended {
			t.Fatalf("%s 多意图题应为 Extended: %+v", id, tk)
		}
		if len(tk.Configs) == 0 {
			t.Fatalf("%s 多意图题应限定 Configs", id)
		}
	}
}

func TestNewContextTasks(t *testing.T) {
	for _, id := range []string{"react-cpu-evidence", "react-disk-evidence", "react-mem-evidence", "react-net-evidence"} {
		tk, ok := TaskByID(id)
		if !ok {
			t.Fatalf("缺 context 任务 %s", id)
		}
		if tk.Object != ObjectContext || !tk.Extended {
			t.Fatalf("%s 应为 ObjectContext 且 Extended: %+v", id, tk)
		}
		if len(tk.Configs) != 1 || tk.Configs[0] != ConfigLLMReact {
			t.Fatalf("%s 应仅 llm-react: %v", id, tk.Configs)
		}
		if len(tk.Expected.ToolUse) != 1 || tk.Expected.ToolUse[0] != "write_scratchpad" {
			t.Fatalf("%s ToolUse = %v, want [write_scratchpad]", id, tk.Expected.ToolUse)
		}
	}
}
