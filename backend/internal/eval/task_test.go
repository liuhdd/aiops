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
