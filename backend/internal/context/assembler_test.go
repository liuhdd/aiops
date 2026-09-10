package context

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aiops/internal/kb"
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

	lastCasesQuery   retrieve.Query
	lastManualsQuery retrieve.Query
	lastCasesOpts    retrieve.Options
	lastManualsOpts  retrieve.Options
}

func (m *mockSearcher) SearchCases(_ context.Context, q retrieve.Query, o retrieve.Options) ([]retrieve.Result, error) {
	m.lastCasesQuery = q
	m.lastCasesOpts = o
	out := m.cases
	if len(out) > o.TopK {
		out = out[:o.TopK]
	}
	return out, nil
}
func (m *mockSearcher) SearchManuals(_ context.Context, q retrieve.Query, o retrieve.Options) ([]retrieve.Result, error) {
	m.lastManualsQuery = q
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

func TestResultChunkSourceDocIDs(t *testing.T) {
	r := retrieve.Result{DocID: "d1", Title: "GPU掉卡", Category: model.KBCase, Heading: "排查", Content: "Xid 48 排查", SourceDocIDs: []string{"d1", "d2"}, Intents: []string{"AskRootCause"}}
	ch := resultChunk(r)
	if got := ch.Metadata["source_doc_ids"]; got != "d1,d2" {
		t.Fatalf("source_doc_ids Metadata = %q want %q", got, "d1,d2")
	}
	if !strings.Contains(ch.Content, "来源：d1,d2") {
		t.Fatalf("Content 应含多源出处，got %q", ch.Content)
	}
	// 单源不应追加来源
	r2 := retrieve.Result{DocID: "d1", Title: "t", Category: model.KBCase, Content: "c", SourceDocIDs: []string{"d1"}}
	ch2 := resultChunk(r2)
	if strings.Contains(ch2.Content, "来源") {
		t.Fatalf("单源不应追加来源，got %q", ch2.Content)
	}
	if got := ch2.Metadata["source_doc_ids"]; got != "d1" {
		t.Fatalf("单源 Metadata = %q want d1", got)
	}
	// 空源静默
	r3 := retrieve.Result{DocID: "d1", Title: "t", Category: model.KBCase, Content: "c"}
	ch3 := resultChunk(r3)
	if got := ch3.Metadata["source_doc_ids"]; got != "" {
		t.Fatalf("空源 Metadata = %q want empty", got)
	}
}

func TestKnowledgeContentSourceDocIDs(t *testing.T) {
	kc := model.KbKnowledgeChunk{Title: "手册", Category: model.KBManual, Content: "内容", SourceDocIDs: []string{"a", "b"}}
	got := knowledgeContent(kc)
	if !strings.Contains(got, "来源：a,b") {
		t.Fatalf("knowledgeContent 多源应含来源，got %q", got)
	}
	kc2 := model.KbKnowledgeChunk{Title: "t", Category: model.KBCase, Content: "c", SourceDocIDs: []string{"a"}}
	got2 := knowledgeContent(kc2)
	if strings.Contains(got2, "来源") {
		t.Fatalf("单源不应含来源，got %q", got2)
	}
	kc3 := model.KbKnowledgeChunk{Title: "t", Category: model.KBCase, Content: "c"}
	got3 := knowledgeContent(kc3)
	if strings.Contains(got3, "来源") {
		t.Fatalf("空源不应含来源，got %q", got3)
	}
}

func TestAssembleSystemIsKnowledgeIntro(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := NewStore(s)
	as, err := NewAssembler(cs, 3).Assemble(Query{Stage: "chat"}, false, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	for _, kw := range []string{"标注来源", "无相关信息时", "以下是当前任务相关的上下文信息"} {
		if !strings.Contains(as.System, kw) {
			t.Fatalf("System 应含 %q, got %q", kw, as.System)
		}
	}
}

func TestAssembleSourceDocIDsPropagation(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := NewStore(s)
	asm := NewAssembler(cs, 3)
	mock := &mockSearcher{
		cases: []retrieve.Result{
			{DocID: "d1", Title: "GPU掉卡", Category: model.KBCase, Content: "排查步骤", SourceDocIDs: []string{"d1", "d2"}},
		},
	}
	asm.SetSearcher(mock)
	as, err := asm.Assemble(Query{Device: "db-01", Metric: "gpu_util", Stage: "rca"}, false, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !strings.Contains(as.User, "来源：d1,d2") {
		t.Fatalf("组装后知识库段应含多源出处，got %s", as.User)
	}
	found := false
	for _, c := range as.Selected {
		if c.Source == SourceKnowledge && c.Metadata["source_doc_ids"] == "d1,d2" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Selected 中应透传 source_doc_ids，got %+v", as.Selected)
	}
}

// TestAssembleVersionFilter 版本过滤透传：两篇同关键词文档以 >= 形式标注版本区间，
// Query.Version 应过滤回退扫描结果并透传给 searcher。
// 注意：裸版本为精确值语义（Min=Max），故不用裸版本造区间数据；>=2.0 与 >=3.0
// 在 3.1.0 处重叠（皆命中），排他方向只用 2.5.0 验证（见任务报告偏差说明）。
func TestAssembleVersionFilter(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	svc := kb.NewService(s, nil)
	upload := func(title, file, ver string) {
		t.Helper()
		data := "---\nversion: " + ver + "\n---\n# 固件升级\n## 步骤\nNVMe固件升级公共关键词，检查链路后升级。\n"
		if _, err := svc.Upload(context.Background(), kb.UploadReq{
			Title: title, Category: model.KBManual, SourceFile: file, Data: []byte(data),
		}); err != nil {
			t.Fatalf("upload %s: %v", title, err)
		}
	}
	upload("版本过滤手册旧版", "fw-v2.md", ">=2.0")
	upload("版本过滤手册新版", "fw-v3.md", ">=3.0")

	cs := NewStore(s)
	asm := NewAssembler(cs, 5)

	// 2.5.0 仅命中旧版区间（新版 Min=3.0.0 排除），回显带版本与标题路径。
	as, err := asm.Assemble(Query{Text: "NVMe固件升级", Version: "2.5.0"}, false, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !strings.Contains(as.User, "版本过滤手册旧版") {
		t.Fatalf("2.5.0 应含旧版，got:\n%s", as.User)
	}
	if strings.Contains(as.User, "版本过滤手册新版") {
		t.Fatalf("2.5.0 不应含新版，got:\n%s", as.User)
	}
	if !strings.Contains(as.User, "[文档版本 V2.0.0]") {
		t.Fatalf("回显应带文档版本，got:\n%s", as.User)
	}
	if !strings.Contains(as.User, "固件升级 > 步骤") {
		t.Fatalf("回显应带标题路径，got:\n%s", as.User)
	}

	// 空版本两者皆含。
	asAll, err := asm.Assemble(Query{Text: "NVMe固件升级", Version: ""}, false, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !strings.Contains(asAll.User, "版本过滤手册旧版") || !strings.Contains(asAll.User, "版本过滤手册新版") {
		t.Fatalf("空版本应两者皆含，got:\n%s", asAll.User)
	}

	// 3.1.0 落在两区间重叠处，两者皆含（不过滤）。
	asBoth, err := asm.Assemble(Query{Text: "NVMe固件升级", Version: "3.1.0"}, false, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !strings.Contains(asBoth.User, "版本过滤手册旧版") || !strings.Contains(asBoth.User, "版本过滤手册新版") {
		t.Fatalf("3.1.0 两区间重叠应两者皆含，got:\n%s", asBoth.User)
	}

	// searcher 路径：Version 应透传到两路检索 Query。
	mock := &mockSearcher{}
	asm.SetSearcher(mock)
	if _, err := asm.Assemble(Query{Text: "NVMe固件升级", Version: "2.5.0"}, false, nil); err != nil {
		t.Fatalf("Assemble with searcher: %v", err)
	}
	if mock.lastCasesQuery.Version != "2.5.0" || mock.lastManualsQuery.Version != "2.5.0" {
		t.Fatalf("Version 未透传给 searcher，got cases=%q manuals=%q",
			mock.lastCasesQuery.Version, mock.lastManualsQuery.Version)
	}
}
