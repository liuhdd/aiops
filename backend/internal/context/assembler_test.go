package context

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aiops/internal/mockdata"
	"aiops/internal/model"
	"aiops/internal/retrieve"
	"aiops/internal/store"
)

func seedKbDoc(t *testing.T, s *store.Store) {
	t.Helper()
	now := time.Now()
	if err := s.WithKBTx(func(tx store.KBStore) error {
		if err := tx.InsertKbDocument(model.KbDocument{
			ID: "kbdoc-1", Title: "磁盘扩容预案", Category: model.KBCase,
			DeviceTypes: []string{"server"}, Metrics: []string{"disk_usage"},
			Keywords: []string{"扩容", "清理"}, Intents: []string{"AskRecommendation"},
			Content: "磁盘使用率超过95%时扩容或清理日志。", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		return tx.InsertKbChunk(model.KbChunk{ID: "kbchunk-1", DocID: "kbdoc-1", Seq: 0,
			Heading: "", Content: "磁盘使用率超过95%时扩容或清理日志。", CreatedAt: now})
	}); err != nil {
		t.Fatalf("seed kb: %v", err)
	}
}

func TestAssembleSections(t *testing.T) {
	mockdata.GenerateAll(42)
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	seedKbDoc(t, s)
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
	if !strings.Contains(as.User, "磁盘扩容预案") {
		t.Fatalf("知识库部分应含磁盘扩容预案:\n%s", as.User)
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

func TestAssembleKnowledgeByIntent(t *testing.T) {
	mockdata.GenerateAll(42)
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	seedKbDoc(t, s)
	cs := NewStore(s)
	asm := NewAssembler(cs, 3)
	as, err := asm.Assemble(Query{Device: "db-01", Metric: "disk_usage", Intents: []string{"AskRecommendation"}}, false, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	found := false
	for _, c := range as.Selected {
		if c.Source == SourceKnowledge && strings.Contains(c.Content, "磁盘扩容预案") {
			found = true
		}
	}
	if !found {
		t.Fatalf("应命中意图对应知识块:\n%s", as.User)
	}
}

type mockSearcher struct {
	cases   []retrieve.Result
	manuals []retrieve.Result

	lastCasesOpts   retrieve.Options
	lastManualsOpts retrieve.Options
}

func (m *mockSearcher) SearchCases(_ context.Context, q retrieve.Query, o retrieve.Options) ([]retrieve.Result, error) {
	m.lastCasesOpts = o
	out := m.cases
	if len(out) > o.TopK {
		out = out[:o.TopK]
	}
	return out, nil
}
func (m *mockSearcher) SearchManuals(_ context.Context, q retrieve.Query, o retrieve.Options) ([]retrieve.Result, error) {
	m.lastManualsOpts = o
	out := m.manuals
	if len(out) > o.TopK {
		out = out[:o.TopK]
	}
	return out, nil
}

func TestAssembleRoutesKnowledgeByStage(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := NewStore(s)
	asm := NewAssembler(cs, 3)
	mock := &mockSearcher{
		cases: []retrieve.Result{
			{DocID: "d1", Title: "GPU掉卡案例1", Category: model.KBCase, Content: "掉卡根因与处置"},
			{DocID: "d2", Title: "GPU掉卡案例2", Category: model.KBCase, Content: "掉卡根因与处置"},
			{DocID: "d3", Title: "GPU掉卡案例3", Category: model.KBCase, Content: "掉卡根因与处置"},
		},
		manuals: []retrieve.Result{
			{DocID: "m1", Title: "交换机手册1", Category: model.KBManual, Content: "光模块更换步骤"},
			{DocID: "m2", Title: "交换机手册2", Category: model.KBManual, Content: "光模块更换步骤"},
			{DocID: "m3", Title: "交换机手册3", Category: model.KBManual, Content: "光模块更换步骤"},
		},
	}
	asm.SetSearcher(mock)
	// rca：案例2 + 手册1
	as, err := asm.Assemble(Query{Device: "db-01", Metric: "gpu_util", Stage: "rca"}, false, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !strings.Contains(as.User, "GPU掉卡案例1") {
		t.Fatalf("rca 应含案例:\n%s", as.User)
	}
	if mock.lastCasesOpts.TopK != 2 || mock.lastManualsOpts.TopK != 1 {
		t.Fatalf("rca 配额应为 cases=2 manuals=1, got %+v %+v", mock.lastCasesOpts, mock.lastManualsOpts)
	}
	if strings.Contains(as.User, "GPU掉卡案例3") {
		t.Fatalf("rca 案例 topK=2 不应出现第3个案例:\n%s", as.User)
	}
	// recommend：手册2 + 案例1
	as, err = asm.Assemble(Query{Device: "db-01", Metric: "gpu_util", Stage: "recommend"}, false, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !strings.Contains(as.User, "交换机手册1") {
		t.Fatalf("recommend 应含手册:\n%s", as.User)
	}
	if mock.lastCasesOpts.TopK != 1 || mock.lastManualsOpts.TopK != 2 {
		t.Fatalf("recommend 配额应为 cases=1 manuals=2, got %+v %+v", mock.lastCasesOpts, mock.lastManualsOpts)
	}
	if strings.Contains(as.User, "交换机手册3") {
		t.Fatalf("recommend 手册 topK=2 不应出现第3个手册:\n%s", as.User)
	}
}
