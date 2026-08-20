package kb

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"aiops/internal/model"
	"aiops/internal/store"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return NewService(st, nil)
}

func TestSeedOnlyWhenEmpty(t *testing.T) {
	svc := newTestService(t)
	docs := []SeedDoc{
		{Title: "CPU热点排查", Category: model.KBCase, DeviceTypes: []string{"server"}, Metrics: []string{"cpu_usage"}, Keywords: []string{"热点进程"}, Content: "# 排查\n检查top热点进程"},
		{Title: "磁盘清理", Category: model.KBCase, DeviceTypes: []string{"storage"}, Metrics: []string{"disk_usage"}, Content: "df -h 定位挂载点"},
	}
	if err := svc.Seed(docs); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if err := svc.Seed(docs); err != nil {
		t.Fatalf("Seed2: %v", err)
	}
	list, err := svc.List(context.Background(), store.KbFilter{})
	if err != nil || len(list) != 2 {
		t.Fatalf("seed 后应 2 条, got %v %+v", err, list)
	}
	if list[0].ChunkCount == 0 {
		t.Fatalf("chunk_count 应为正, got %+v", list[0])
	}
}

func TestUploadAndFields(t *testing.T) {
	svc := newTestService(t)
	doc, err := svc.Upload(context.Background(), UploadReq{
		SourceFile: "s5560.md", Data: []byte("# 光模块\n检查光模块告警，涉及 switch 设备。"),
		Category: model.KBManual, Vendor: []string{"H3C"}, Model: []string{"S5560"},
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if doc.ID == "" || doc.Title != "s5560" || doc.Category != model.KBManual {
		t.Fatalf("doc = %+v", doc)
	}
	if !hasStr(doc.DeviceTypes, "switch") {
		t.Fatalf("应自动命中 switch, got %v", doc.DeviceTypes)
	}
	if !hasStr(doc.Keywords, "光模块") {
		t.Fatalf("应自动命中 光模块, got %v", doc.Keywords)
	}
	if !hasStr(doc.Vendor, "H3C") || !hasStr(doc.Model, "S5560") {
		t.Fatalf("vendor/model 人工字段, got %+v", doc)
	}
	detail, err := svc.Get(context.Background(), doc.ID)
	if err != nil || len(detail.Chunks) == 0 {
		t.Fatalf("Get: %v %+v", err, detail)
	}
}

func TestUploadOverrideTags(t *testing.T) {
	svc := newTestService(t)
	doc, err := svc.Upload(context.Background(), UploadReq{
		SourceFile: "doc.md", Data: []byte("交换机运维"),
		Category: model.KBCase, DeviceTypes: []string{"switch"}, Keywords: []string{"自定义"},
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if !hasStr(doc.Keywords, "自定义") || len(doc.DeviceTypes) != 1 {
		t.Fatalf("覆盖未生效: %+v", doc)
	}
}

func TestUploadErrors(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.Upload(context.Background(), UploadReq{SourceFile: "a.pdf", Data: []byte("x")}); !errors.Is(err, ErrBadFile) {
		t.Fatalf("非法类型应 ErrBadFile, got %v", err)
	}
	if _, err := svc.Upload(context.Background(), UploadReq{SourceFile: "a.txt", Data: []byte("  ")}); !errors.Is(err, ErrEmptyContent) {
		t.Fatalf("空内容应 ErrEmptyContent, got %v", err)
	}
	if _, err := svc.Upload(context.Background(), UploadReq{SourceFile: "a.txt", Data: []byte("x"), Category: model.KBCategory("bogus")}); !errors.Is(err, ErrBadCategory) {
		t.Fatalf("非法分类应 ErrBadCategory, got %v", err)
	}
}

func TestUpdateAndDelete(t *testing.T) {
	svc := newTestService(t)
	doc, _ := svc.Upload(context.Background(), UploadReq{SourceFile: "a.txt", Data: []byte("正文"), Category: model.KBCase})
	upd, err := svc.Update(context.Background(), doc.ID, UpdateReq{
		Title: "改名", Category: model.KBManual, DeviceTypes: []string{"server"}, Vendor: []string{"Dell"}, Model: []string{"R750"}, Intents: []string{"AskRootCause"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if upd.Title != "改名" || upd.Category != model.KBManual || upd.Content != "正文" {
		t.Fatalf("update = %+v", upd)
	}
	if err := svc.Delete(context.Background(), doc.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get(context.Background(), doc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后应 ErrNotFound, got %v", err)
	}
	if _, err := svc.Update(context.Background(), "nope", UpdateReq{Title: "x", Category: model.KBCase}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("更新不存在应 ErrNotFound, got %v", err)
	}
	if err := svc.Delete(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除不存在应 ErrNotFound, got %v", err)
	}
}

func TestUploadTitleFallbackAndIntentSuggestion(t *testing.T) {
	svc := NewService(storeMust(t), func() []model.LeafSpec {
		return []model.LeafSpec{{Code: "AskRootCause", Name: "AskRootCause", Description: "询问根因", RuleKeywords: []string{"根因"}}}
	})
	doc, err := svc.Upload(context.Background(), UploadReq{SourceFile: "my-guide.md", Data: []byte("排查磁盘根因正文")})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if doc.Title != "my-guide" {
		t.Fatalf("title fallback = %q", doc.Title)
	}
	if !hasStr(doc.Intents, "AskRootCause") {
		t.Fatalf("应建议绑定 AskRootCause, got %v", doc.Intents)
	}
}

type fakeEmbed struct{}

func (fakeEmbed) Model() string { return "fake" }
func (fakeEmbed) Embed(_ context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i := range texts {
		out[i] = []float64{float64(i + 1), 1}
	}
	return out, nil
}

func TestUploadGeneratesEmbeddings(t *testing.T) {
	st := storeMust(t)
	svc := NewServiceWithEmbedder(st, nil, fakeEmbed{})
	doc, err := svc.Upload(context.Background(), UploadReq{
		SourceFile: "case.md", Data: []byte("GPU 掉卡排查正文"), Category: model.KBCase,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	emb, err := st.ListKbEmbeddings()
	if err != nil || len(emb) != 1 || len(emb[0].Embedding) != 2 {
		t.Fatalf("应生成1条向量: %v %+v", err, emb)
	}
	if emb[0].DocID != doc.ID || emb[0].Model != "fake" {
		t.Fatalf("向量元数据: %+v", emb[0])
	}
}

func TestUploadEmbedFailDoesNotBlock(t *testing.T) {
	st := storeMust(t)
	svc := NewServiceWithEmbedder(st, nil, errEmbed{})
	doc, err := svc.Upload(context.Background(), UploadReq{
		SourceFile: "case.md", Data: []byte("正文内容"), Category: model.KBCase,
	})
	if err != nil {
		t.Fatalf("embedding 失败不应阻断入库: %v", err)
	}
	emb, _ := st.ListKbEmbeddings()
	if len(emb) != 0 {
		t.Fatalf("embedding 失败应无向量: %+v", emb)
	}
	if doc.ID == "" {
		t.Fatalf("文档应已入库")
	}
}

type errEmbed struct{}

func (errEmbed) Model() string { return "fake" }
func (errEmbed) Embed(context.Context, []string) ([][]float64, error) {
	return nil, errors.New("boom")
}

func TestDeleteCascadesEmbeddings(t *testing.T) {
	st := storeMust(t)
	svc := NewServiceWithEmbedder(st, nil, fakeEmbed{})
	doc, _ := svc.Upload(context.Background(), UploadReq{SourceFile: "a.txt", Data: []byte("正文"), Category: model.KBCase})
	if err := svc.Delete(context.Background(), doc.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	emb, _ := st.ListKbEmbeddings()
	if len(emb) != 0 {
		t.Fatalf("删除后应无向量, got %+v", emb)
	}
}

func hasStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func storeMust(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}
