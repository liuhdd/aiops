package context

import (
	"path/filepath"
	"testing"
	"time"

	"aiops/internal/model"
	"aiops/internal/store"
)

func newTestStore(t *testing.T) (*Store, *store.Store) {
	t.Helper()
	s, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return NewStore(s), s
}

func TestScratchpadRoundtrip(t *testing.T) {
	cs, _ := newTestStore(t)
	now := time.Now()
	_ = cs.InsertScratchpad(model.ScratchpadEntry{ID: "s1", RunID: "run-1", Step: "rca", Content: "x", CreatedAt: now.Add(-time.Minute)})
	_ = cs.InsertScratchpad(model.ScratchpadEntry{ID: "s2", RunID: "run-1", Step: "recommend", Content: "y", ToolName: "write_scratchpad", CreatedAt: now})
	got, err := cs.ListScratchpad("run-1")
	if err != nil || len(got) != 2 {
		t.Fatalf("ListScratchpad = %d err=%v", len(got), err)
	}
	if got[0].Step != "rca" || got[1].ToolName != "write_scratchpad" {
		t.Fatalf("order/content wrong: %+v", got)
	}
}

func TestToolEventRoundtrip(t *testing.T) {
	cs, _ := newTestStore(t)
	now := time.Now()
	_ = cs.InsertToolEvent(model.ToolEvent{ID: "e1", RunID: "run-1", ToolName: "query_metric",
		ArgsJSON: "{}", ResultJSON: "[]", Status: model.ToolStatusOK, DurationMS: 5, CreatedAt: now})
	got, err := cs.ListToolEvents("run-1")
	if err != nil || len(got) != 1 || got[0].Status != model.ToolStatusOK {
		t.Fatalf("ListToolEvents = %+v err=%v", got, err)
	}
}

func TestLongTermMemoryRoundtrip(t *testing.T) {
	cs, _ := newTestStore(t)
	now := time.Now()
	_ = cs.AddLongTermMemory(model.LongTermMemory{ID: "m1", Device: "db-01", Metric: "disk_usage",
		RootCause: "磁盘空间不足", Confidence: 0.8, ActionsJSON: "[]", RunID: "run-1", CreatedAt: now})
	_ = cs.AddLongTermMemory(model.LongTermMemory{ID: "m2", Device: "db-01", Metric: "disk_usage",
		RootCause: "磁盘满", Confidence: 0.9, ActionsJSON: "[]", RunID: "run-2", CreatedAt: now.Add(time.Minute)})
	got, err := cs.ListLongTermMemory("db-01", "disk_usage")
	if err != nil || len(got) != 2 {
		t.Fatalf("ListLongTermMemory = %d err=%v", len(got), err)
	}
	if got[0].RunID != "run-2" { // 最新的在前
		t.Fatalf("order wrong: %+v", got)
	}
}

func TestRecentMessagesOrder(t *testing.T) {
	cs, s := newTestStore(t)
	now := time.Now()
	for i := 0; i < 5; i++ {
		_ = s.SaveMessage(model.Message{ID: model.NewID("msg"), Role: model.RoleUser, Content: "m" + string(rune('0'+i)), CreatedAt: now.Add(time.Duration(i) * time.Minute)})
	}
	got, err := cs.RecentMessages("", 3)
	if err != nil || len(got) != 3 {
		t.Fatalf("RecentMessages = %d err=%v", len(got), err)
	}
	if got[0].Content != "m2" || got[2].Content != "m4" {
		t.Fatalf("wrong window: %+v", got)
	}
}

func TestRecentMessagesByConversationFilter(t *testing.T) {
	cs, s := newTestStore(t)
	now := time.Now()
	for i := 0; i < 3; i++ {
		_ = s.SaveMessage(model.Message{ID: model.NewID("msg"), Role: model.RoleUser, Content: "a" + string(rune('0'+i)), ConversationID: "c1", CreatedAt: now.Add(time.Duration(i) * time.Minute)})
		_ = s.SaveMessage(model.Message{ID: model.NewID("msg"), Role: model.RoleUser, Content: "b" + string(rune('0'+i)), ConversationID: "c2", CreatedAt: now.Add(time.Duration(i) * time.Minute)})
	}
	got, err := cs.RecentMessages("c1", 2)
	if err != nil || len(got) != 2 || got[0].Content != "a1" || got[1].Content != "a2" {
		t.Fatalf("RecentMessages(c1,2): %v %+v", err, got)
	}
}
