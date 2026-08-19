package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"aiops/internal/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAlertRoundTrip(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	a := model.Alert{ID: "alarm-1", Device: "compute-01", Metric: "cpu_usage",
		Severity: "warning", Message: "cpu high", Status: "active", FiredAt: now, CreatedAt: now}
	if err := s.SaveAlert(a); err != nil {
		t.Fatalf("SaveAlert: %v", err)
	}
	got, err := s.GetAlert("alarm-1")
	if err != nil {
		t.Fatalf("GetAlert: %v", err)
	}
	if got.Device != "compute-01" || got.Metric != "cpu_usage" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	list, err := s.ListAlerts()
	if err != nil || len(list) != 1 {
		t.Fatalf("ListAlerts: %v len=%d", err, len(list))
	}
}

func TestRunAndStages(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	r := model.PipelineRun{ID: "run-1", AlertID: "alarm-1", Status: model.StatusRunning,
		Iteration: 0, MaxIteration: 2, CreatedAt: now, UpdatedAt: now}
	if err := s.SaveRun(r); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	st := model.StageResult{ID: "st-1", RunID: "run-1", StageID: "normalize", StageName: "接收规范化",
		Status: model.StageCompleted, Iteration: 0, Output: "{}", StartedAt: now, EndedAt: now}
	if err := s.SaveStage(st); err != nil {
		t.Fatalf("SaveStage: %v", err)
	}
	if err := s.UpdateRunStatus("run-1", model.StatusCompleted); err != nil {
		t.Fatalf("UpdateRunStatus: %v", err)
	}
	gotRun, err := s.GetRun("run-1")
	if err != nil || gotRun.Status != model.StatusCompleted {
		t.Fatalf("GetRun: %v %+v", err, gotRun)
	}
	stages, err := s.ListStages("run-1")
	if err != nil || len(stages) != 1 {
		t.Fatalf("ListStages: %v len=%d", err, len(stages))
	}
	latest, ok, err := s.GetLatestStage("run-1", "normalize")
	if err != nil || !ok || latest.ID != "st-1" {
		t.Fatalf("GetLatestStage: %v ok=%v %+v", err, ok, latest)
	}
}

func TestMessages(t *testing.T) {
	s := newTestStore(t)
	m := model.Message{ID: "msg-1", Role: model.RoleUser, Content: "hello", CreatedAt: time.Now()}
	if err := s.SaveMessage(m); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	msgs, err := s.ListMessages("")
	if err != nil || len(msgs) != 1 || msgs[0].Content != "hello" {
		t.Fatalf("ListMessages: %v %+v", err, msgs)
	}
}

func TestListMessagesEmptyReturnsEmptySlice(t *testing.T) {
	s := newTestStore(t)
	msgs, err := s.ListMessages("")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if msgs == nil {
		t.Fatal("空表时 ListMessages 应返回非 nil 切片（否则 JSON 序列化为 null，前端 .length 崩溃）")
	}
}

func TestAlertScopeColumnWritten(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	a := model.Alert{ID: "alarm-s", Device: "compute-01", Metric: "cpu_usage",
		Severity: "warning", Status: "active", FiredAt: now, CreatedAt: now}
	if err := s.SaveAlert(a); err != nil {
		t.Fatalf("SaveAlert: %v", err)
	}
	var scopeVal string
	if err := s.db.QueryRow(`SELECT scope FROM alerts WHERE id = ?`, a.ID).Scan(&scopeVal); err != nil {
		t.Fatalf("query scope: %v", err)
	}
	if scopeVal != "device:compute-01" {
		t.Fatalf("scope = %q, want device:compute-01", scopeVal)
	}
}

func TestRunAndStageScopeColumns(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	r := model.PipelineRun{ID: "run-s", AlertID: "alarm-s", Status: model.StatusRunning,
		Iteration: 0, MaxIteration: 2, CreatedAt: now, UpdatedAt: now}
	if err := s.SaveRun(r); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	st := model.StageResult{ID: "st-s", RunID: "run-s", StageID: "rca", StageName: "根因分析",
		Status: model.StageCompleted, Output: "{}", StartedAt: now, EndedAt: now}
	if err := s.SaveStage(st); err != nil {
		t.Fatalf("SaveStage: %v", err)
	}
	var runScope, stageScope string
	if err := s.db.QueryRow(`SELECT scope FROM pipeline_runs WHERE id = ?`, r.ID).Scan(&runScope); err != nil {
		t.Fatalf("run scope: %v", err)
	}
	if err := s.db.QueryRow(`SELECT scope FROM stage_results WHERE id = ?`, st.ID).Scan(&stageScope); err != nil {
		t.Fatalf("stage scope: %v", err)
	}
	if runScope != "task:run-s" || stageScope != "task:run-s" {
		t.Fatalf("run=%q stage=%q, want task:run-s", runScope, stageScope)
	}
}

func TestDeviceContextRoundTrip(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	dc := model.DeviceContext{DeviceID: "compute-01", StateJSON: `{"cpu":85}`,
		UpdatedBy: "run-1", UpdatedAt: now}
	if err := s.SaveDeviceContext(dc); err != nil {
		t.Fatalf("SaveDeviceContext: %v", err)
	}
	got, err := s.GetDeviceContext("compute-01")
	if err != nil {
		t.Fatalf("GetDeviceContext: %v", err)
	}
	if got.DeviceID != "compute-01" || got.StateJSON != `{"cpu":85}` || got.UpdatedBy != "run-1" {
		t.Fatalf("roundtrip = %+v", got)
	}
	// UPSERT updates state
	dc2 := model.DeviceContext{DeviceID: "compute-01", StateJSON: `{"cpu":95}`, UpdatedBy: "run-2", UpdatedAt: now}
	if err := s.SaveDeviceContext(dc2); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, _ = s.GetDeviceContext("compute-01")
	if got.StateJSON != `{"cpu":95}` {
		t.Fatalf("upsert state = %q", got.StateJSON)
	}
}

func TestListAlertsByDevice(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	_ = s.SaveAlert(model.Alert{ID: "alarm-1", Device: "compute-01", Metric: "cpu_usage", Status: "active", FiredAt: now, CreatedAt: now})
	_ = s.SaveAlert(model.Alert{ID: "alarm-2", Device: "db-01", Metric: "disk_usage", Status: "active", FiredAt: now, CreatedAt: now})
	list, err := s.ListAlertsByDevice("compute-01")
	if err != nil || len(list) != 1 || list[0].ID != "alarm-1" {
		t.Fatalf("ListAlertsByDevice: %v %+v", err, list)
	}
	all, _ := s.ListAlerts()
	if len(all) != 2 {
		t.Fatalf("ListAlerts len = %d, want 2", len(all))
	}
}

func TestMigrateCreatesContextTables(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('scratchpad_entries','tool_events','long_term_memory')`).Scan(&n)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 3 {
		t.Fatalf("context tables = %d, want 3", n)
	}
}

func TestConversationsCRUD(t *testing.T) {
	s := newTestStore(t)
	conv, err := s.CreateConversation("")
	if err != nil || conv.ID == "" {
		t.Fatalf("CreateConversation: %v %+v", err, conv)
	}
	if err := s.SetConversationTitle(conv.ID, "会话甲"); err != nil {
		t.Fatalf("SetConversationTitle: %v", err)
	}
	got, err := s.GetConversation(conv.ID)
	if err != nil || got.Title != "会话甲" {
		t.Fatalf("GetConversation: %v %+v", err, got)
	}
	if _, err := s.GetConversation("nope"); err != ErrConversationNotFound {
		t.Fatalf("不存在会话应返回 ErrConversationNotFound, got %v", err)
	}
	// 活跃排序：B 创建后 Touch，应排 A 前
	a, _ := s.CreateConversation("A")
	b, _ := s.CreateConversation("B")
	if err := s.TouchConversation(b.ID); err != nil {
		t.Fatalf("TouchConversation: %v", err)
	}
	list, err := s.ListConversations()
	if err != nil || len(list) != 3 {
		t.Fatalf("ListConversations: %v len=%d", err, len(list))
	}
	if list[0].ID != b.ID || list[1].ID != a.ID {
		t.Fatalf("活跃排序错误: %+v", list)
	}
}

func TestListMessagesByConversation(t *testing.T) {
	s := newTestStore(t)
	c1, _ := s.CreateConversation("")
	c2, _ := s.CreateConversation("")
	now := time.Now()
	_ = s.SaveMessage(model.Message{ID: "m1", Role: model.RoleUser, Content: "in1", ConversationID: c1.ID, CreatedAt: now})
	_ = s.SaveMessage(model.Message{ID: "m2", Role: model.RoleUser, Content: "in2", ConversationID: c1.ID, CreatedAt: now.Add(time.Minute)})
	_ = s.SaveMessage(model.Message{ID: "m3", Role: model.RoleUser, Content: "other", ConversationID: c2.ID, CreatedAt: now})
	msgs, err := s.ListMessages(c1.ID)
	if err != nil || len(msgs) != 2 || msgs[0].ID != "m1" || msgs[1].ID != "m2" {
		t.Fatalf("ListMessages(c1): %v %+v", err, msgs)
	}
	all, _ := s.ListMessages("")
	if len(all) != 3 {
		t.Fatalf("ListMessages(\"\") 应返回全部, got %d", len(all))
	}
}

func TestRecentMessagesByConversation(t *testing.T) {
	s := newTestStore(t)
	c1, _ := s.CreateConversation("")
	c2, _ := s.CreateConversation("")
	now := time.Now()
	for i := 0; i < 3; i++ {
		_ = s.SaveMessage(model.Message{ID: model.NewID("msg"), Role: model.RoleUser, Content: "a" + string(rune('0'+i)), ConversationID: c1.ID, CreatedAt: now.Add(time.Duration(i) * time.Minute)})
		_ = s.SaveMessage(model.Message{ID: model.NewID("msg"), Role: model.RoleUser, Content: "b" + string(rune('0'+i)), ConversationID: c2.ID, CreatedAt: now.Add(time.Duration(i) * time.Minute)})
	}
	got, err := s.RecentMessages(c1.ID, 2)
	if err != nil || len(got) != 2 || got[0].Content != "a1" || got[1].Content != "a2" {
		t.Fatalf("RecentMessages(c1,2): %v %+v", err, got)
	}
	// 空会话 → 空非 nil 切片
	empty, _ := s.RecentMessages("nope", 5)
	if empty == nil {
		t.Fatal("空会话 RecentMessages 应返回非 nil 切片")
	}
}

func TestMigrateBackfillsLegacyMessages(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE messages (id TEXT PRIMARY KEY, role TEXT, content TEXT, alert_id TEXT, created_at DATETIME)`); err != nil {
		t.Fatalf("create legacy messages: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO messages (id, role, content, alert_id, created_at) VALUES ('old-1','user','hi',NULL,?)`, time.Now()); err != nil {
		t.Fatalf("insert legacy: %v", err)
	}
	_ = db.Close()
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("migrate legacy db: %v", err)
	}
	defer s.Close()
	convs, _ := s.ListConversations()
	if len(convs) != 1 || convs[0].Title != "历史对话" {
		t.Fatalf("回填应创建「历史对话」, got %+v", convs)
	}
	msgs, _ := s.ListMessages(convs[0].ID)
	if len(msgs) != 1 || msgs[0].ID != "old-1" {
		t.Fatalf("旧消息应回填进历史对话, got %+v", msgs)
	}
}
