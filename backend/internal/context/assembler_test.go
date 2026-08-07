package context

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aiops/internal/mockdata"
	"aiops/internal/model"
	"aiops/internal/store"
)

func TestAssembleSections(t *testing.T) {
	mockdata.GenerateAll(42)
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := NewStore(s)
	now := time.Now()
	_ = cs.InsertScratchpad(model.ScratchpadEntry{ID: "sp1", RunID: "run-1", Step: "rca",
		Content: "磁盘空间不足假设", CreatedAt: now})
	_ = cs.AddLongTermMemory(model.LongTermMemory{ID: "m1", Device: "db-01", Metric: "disk_usage",
		RootCause: "磁盘空间不足", Confidence: 0.8, ActionsJSON: `["清理日志"]`, RunID: "run-0", CreatedAt: now.Add(-time.Hour)})
	_ = s.SaveMessage(model.Message{ID: model.NewID("msg"), Role: model.RoleUser, Content: "查一下db-01磁盘", CreatedAt: now})

	asm := NewAssembler(cs, 3)
	q := Query{Device: "db-01", Metric: "disk_usage", Severity: "critical", Stage: "rca", RunID: "run-1"}
	as, err := asm.Assemble(q, true, DefaultTools())
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	for _, sec := range []string{"【临时笔记】", "【设备历史经验】", "【知识库】", "【短期记忆】", "【可用工具】"} {
		if !strings.Contains(as.User, sec) {
			t.Fatalf("missing section %s in:\n%s", sec, as.User)
		}
	}
	if len(as.Selected) == 0 {
		t.Fatalf("no selected chunks")
	}
}

func TestAssembleEmptySourcesNoSections(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := NewStore(s)
	asm := NewAssembler(cs, 3)
	q := Query{Device: "nonexistent-9", Metric: "cpu_usage", Stage: "rca", RunID: "run-x"}
	as, err := asm.Assemble(q, false, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if strings.Contains(as.User, "【临时笔记】") || strings.Contains(as.User, "【可用工具】") {
		t.Fatalf("empty sources should not emit sections:\n%s", as.User)
	}
}
