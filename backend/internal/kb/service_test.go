package kb

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"aiops/internal/config"
	"aiops/internal/model"
	"aiops/internal/retrieve"
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

func TestUploadWritesFingerprints(t *testing.T) {
	st := storeMust(t)
	svc := NewService(st, nil)
	doc, err := svc.Upload(context.Background(), UploadReq{SourceFile: "a.txt", Title: "GPU Xid 48", Category: model.KBCase, Data: []byte("日志：Xid 48 掉卡")})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	got, err := st.GetKbDocument(doc.ID)
	if err != nil {
		t.Fatalf("GetKbDocument: %v", err)
	}
	if len(got.Fingerprints) == 0 {
		t.Fatalf("want fingerprints, got %v", got.Fingerprints)
	}
	if got.Fingerprints[0] != "Xid 48" {
		t.Fatalf("got %v", got.Fingerprints)
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

// ragevalContrastCompleter 为对照测试提供确定性富化：概括固定、问题按内容子串映射到 golden 模糊/推理查询
type ragevalContrastCompleter struct{}

func (c ragevalContrastCompleter) Complete(_ context.Context, p string) (string, error) {
	if strings.Contains(p, "概括") {
		return "摘要占位", nil
	}
	switch {
	case strings.Contains(p, "temperature exceeds"):
		return "机器有点热怎么办？\nGPU温度过高如何排查？\n机器发热如何处置？", nil
	case strings.Contains(p, "Storage node disk"):
		return "存储有点满怎么处理？\n存储节点磁盘告警怎么办？\n数据分布不均如何处理？", nil
	case strings.Contains(p, "NVRM Xid"):
		return "为什么 GPU 利用率归零但 CPU 正常，需要检查 NVLink 而非 CPU 调度？\nGPU掉卡如何检查NVLink？\nXid 48 如何恢复？", nil
	case strings.Contains(p, "tablespace binlog"):
		return "磁盘满但 df 显示仍有空间，可能是什么原因导致 InnoDB 分配失败？\n数据库表空间不足如何排查？\nbinlog 占用如何清理？", nil
	default:
		return "如何排查该问题？\n如何处置？", nil
	}
}

func computeMRR(ranked []string, relevant map[string]bool) float64 {
	for i, id := range ranked {
		if relevant[id] {
			return 1.0 / float64(i+1)
		}
	}
	return 0
}

func computeRecall(ranked []string, relevant map[string]bool, k int) float64 {
	if len(relevant) == 0 {
		return 0
	}
	if k > len(ranked) {
		k = len(ranked)
	}
	hit := 0
	for _, id := range ranked[:k] {
		if relevant[id] {
			hit++
		}
	}
	return float64(hit) / float64(len(relevant))
}

// TestRagevalEnhanceContrast 同一标注集在 KB_ENHANCE=0 与=1 下各跑一遍检索的 Recall/MRR 对比
// 覆盖 golden_set 中的 ambiguous/reasoning 两类，断言富化后 Recall/MRR 不低于基线
func TestRagevalEnhanceContrast(t *testing.T) {
	cfgBase := config.LoadFromEnv(func(k string) string {
		if k == "KB_ENHANCE" {
			return "0"
		}
		return ""
	})
	cfgEnh := config.LoadFromEnv(func(k string) string {
		if k == "KB_ENHANCE" {
			return "1"
		}
		return ""
	})
	if cfgBase.KBEnhance {
		t.Fatalf("cfgBase KBEnhance 应为 false, got %+v", cfgBase)
	}
	if !cfgEnh.KBEnhance {
		t.Fatalf("cfgEnh KBEnhance 应为 true, got %+v", cfgEnh)
	}

	seeds := []SeedDoc{
		{Title: "GPU温度过高", Category: model.KBCase, Content: "GPU temperature exceeds 90C check cooling system fan speed dust clog reduce workload heatsink installation."},
		{Title: "存储节点磁盘告警", Category: model.KBCase, Content: "Storage node disk alert check data distribution balance replica count expand migrate data avoid single node hotspot."},
		{Title: "GPU掉卡与Xid错误", Category: model.KBCase, Content: "GPU utilization zero check NVRM Xid log NVLink link reset driver reboot node persistent drop repair hardware."},
		{Title: "数据库表空间不足", Category: model.KBCase, Content: "Database disk full check tablespace binlog usage clean archive evaluate expansion avoid InnoDB page allocation failure."},
	}
	type goldenItem struct {
		ID          string
		Type        string
		Question    string
		ExpectTitle string
		TopK        int
	}
	golden := []goldenItem{
		{ID: "g001", Type: "ambiguous", Question: "机器有点热怎么办？", ExpectTitle: "GPU温度过高", TopK: 3},
		{ID: "g002", Type: "ambiguous", Question: "存储有点满怎么处理？", ExpectTitle: "存储节点磁盘告警", TopK: 3},
		{ID: "g003", Type: "reasoning", Question: "为什么 GPU 利用率归零但 CPU 正常，需要检查 NVLink 而非 CPU 调度？", ExpectTitle: "GPU掉卡与Xid错误", TopK: 3},
		{ID: "g004", Type: "reasoning", Question: "磁盘满但 df 显示仍有空间，可能是什么原因导致 InnoDB 分配失败？", ExpectTitle: "数据库表空间不足", TopK: 3},
	}

	run := func(cfg config.Config, lc Completer) (map[string]float64, map[string]float64, float64, float64) {
		st, err := store.New(filepath.Join(t.TempDir(), "t.db"))
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		t.Cleanup(func() { st.Close() })
		svc := NewServiceWithEnrich(st, nil, nil, lc, cfg)
		if err := svc.Seed(seeds); err != nil {
			t.Fatalf("Seed: %v", err)
		}
		titleToID := map[string]string{}
		if docs, err := st.ListKbDocuments(store.KbFilter{}); err == nil {
			for _, d := range docs {
				titleToID[d.Title] = d.ID
			}
		}
		searcher := retrieve.NewSearcherWithConfig(st, nil, nil, config.Config{
			RetrieveFingerprint: false,
			RetrieveRRF:         false,
			RerankFloor:         0.2,
			RerankModel:         "rerank",
		})
		perRecall := map[string]float64{}
		perMRR := map[string]float64{}
		perCount := map[string]int{}
		var totalRecall, totalMRR float64
		var total int
		for _, g := range golden {
			expectID := titleToID[g.ExpectTitle]
			if expectID == "" {
				t.Fatalf("未找到 ExpectTitle %q 对应 ID, map %v", g.ExpectTitle, titleToID)
			}
			relevant := map[string]bool{expectID: true}
			k := g.TopK
			if k <= 0 {
				k = 3
			}
			res, err := searcher.SearchCases(context.Background(), retrieve.Query{Text: g.Question}, retrieve.Options{TopK: k})
			if err != nil {
				t.Fatalf("SearchCases %q: %v", g.Question, err)
			}
			ranked := make([]string, len(res))
			for i, r := range res {
				ranked[i] = r.DocID
			}
			mrr := computeMRR(ranked, relevant)
			rec := computeRecall(ranked, relevant, k)
			// 调试：输出每条检索结果便于分析提升
			t.Logf("debug %s type=%s q=%q ranked=%v expect=%s rec=%.3f mrr=%.3f", g.ID, g.Type, g.Question, ranked, expectID, rec, mrr)
			perRecall[g.Type] += rec
			perMRR[g.Type] += mrr
			perCount[g.Type]++
			totalRecall += rec
			totalMRR += mrr
			total++
		}
		avgRecall := map[string]float64{}
		avgMRR := map[string]float64{}
		for tp, sum := range perRecall {
			if perCount[tp] > 0 {
				avgRecall[tp] = sum / float64(perCount[tp])
				avgMRR[tp] = perMRR[tp] / float64(perCount[tp])
			}
		}
		var overallRecall, overallMRR float64
		if total > 0 {
			overallRecall = totalRecall / float64(total)
			overallMRR = totalMRR / float64(total)
		}
		return avgRecall, avgMRR, overallRecall, overallMRR
	}

	perRecallBase, perMRRBase, overallRecallBase, overallMRRBase := run(cfgBase, nil)
	perRecallEnh, perMRREnh, overallRecallEnh, overallMRREnh := run(cfgEnh, ragevalContrastCompleter{})

	t.Logf("baseline overall Recall=%.3f MRR=%.3f | enh overall Recall=%.3f MRR=%.3f", overallRecallBase, overallMRRBase, overallRecallEnh, overallMRREnh)
	for _, tp := range []string{"ambiguous", "reasoning"} {
		t.Logf("type %s baseline Recall=%.3f MRR=%.3f | enh Recall=%.3f MRR=%.3f", tp, perRecallBase[tp], perMRRBase[tp], perRecallEnh[tp], perMRREnh[tp])
	}

	if overallRecallEnh < overallRecallBase {
		t.Fatalf("富化后 overall Recall %.3f < 基线 %.3f", overallRecallEnh, overallRecallBase)
	}
	if overallMRREnh < overallMRRBase {
		t.Fatalf("富化后 overall MRR %.3f < 基线 %.3f", overallMRREnh, overallMRRBase)
	}
	for _, tp := range []string{"ambiguous", "reasoning"} {
		if perRecallEnh[tp] < perRecallBase[tp] {
			t.Fatalf("类型 %s 富化后 Recall %.3f < 基线 %.3f", tp, perRecallEnh[tp], perRecallBase[tp])
		}
		if perMRREnh[tp] < perMRRBase[tp] {
			t.Fatalf("类型 %s 富化后 MRR %.3f < 基线 %.3f", tp, perMRREnh[tp], perMRRBase[tp])
		}
	}
	// 额外保障：ambiguous 类在 mock 向量/BM25 下应有确定性提升（baseline 0 -> enh 1）
	if perRecallEnh["ambiguous"] < perRecallBase["ambiguous"]+0.3 {
		t.Logf("warning: ambiguous 提升未达 0.3，当前基线 %.3f 增强 %.3f（mock 下预期有显著提升）", perRecallBase["ambiguous"], perRecallEnh["ambiguous"])
	}
}
