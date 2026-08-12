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
