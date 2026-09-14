package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aiops/internal/config"
	"aiops/internal/goldenset"
	"aiops/internal/kb"
	"aiops/internal/llm"
	"aiops/internal/mockdata"
	"aiops/internal/model"
	"aiops/internal/phoenix"
	"aiops/internal/retrieve"
	"aiops/internal/store"
)

func main() {
	golden := flag.String("golden", "testdata/golden_set.jsonl", "golden set jsonl path")
	generate := flag.Bool("generate", false, "生成模式：按种子文档批量出题")
	enhance := flag.Bool("enhance", true, "是否启用富化")
	out := flag.String("o", "", "结构化报告输出路径（JSON）")
	variantsFlag := flag.String("variants", "", "变体串，如 baseline;rerank=none;fuse=weighted")
	variantsFile := flag.String("variants-file", "", "变体 JSON 文件")
	flag.Parse()

	if *generate {
		cfg := config.Load()
		if cfg.LLMAPIKey == "" {
			fmt.Fprintln(os.Stderr, "generate 模式需要 LLM_API_KEY")
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "generate 模式尚未实现：请基于种子文档人工校对 golden_set.jsonl")
		os.Exit(1)
	}

	// locate golden file: handle both "testdata/..." (cwd=backend) and "backend/testdata/..." (cwd=backend)
	goldenPath := resolveGoldenPath(*golden)

	items, err := goldenset.Load(goldenPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "标注集解析失败: %v\n", err)
		os.Exit(1)
	}
	if len(items) == 0 {
		fmt.Fprintln(os.Stderr, "标注集为空")
		os.Exit(1)
	}

	cfg := config.Load()
	if !*enhance {
		cfg.KBEnhance = false
	}
	dbPath := filepath.Join(os.TempDir(), "rageval.db")
	_ = os.Remove(dbPath)
	defer os.Remove(dbPath)
	s, err := store.New(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store.New 失败: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	mockdata.GenerateAll(42)
	// 构造种子文档并导入（幂等）
	var seeds []kb.SeedDoc
	for _, d := range mockdata.Docs() {
		seeds = append(seeds, kb.SeedDoc{
			Title:       d.Title,
			Category:    model.KBCase,
			DeviceTypes: d.DeviceTypes,
			Metrics:     d.Metrics,
			Keywords:    d.Keywords,
			Content:     d.Body,
		})
	}
	var embedder retrieve.Embedder
	if e, err := retrieve.NewLLMEmbedder(cfg); err == nil {
		embedder = e
	}
	var lc *llm.Client
	if c, err := llm.NewClient(cfg); err == nil {
		lc = c
	}
	var llmCompleter kb.Completer
	if lc != nil {
		llmCompleter = kb.AdaptCompleter(lc)
	}
	svc := kb.NewServiceWithEnrich(s, nil, embedder, llmCompleter, cfg)
	if err := svc.Seed(seeds); err != nil {
		fmt.Fprintf(os.Stderr, "kb seed 失败: %v\n", err)
		os.Exit(1)
	}

	// 构建映射：mock 概念 id -> Title，以及 Title -> 真实库 id 与真实 id 集合
	mockIDToTitle := map[string]string{}
	for _, d := range mockdata.Docs() {
		mockIDToTitle[d.ID] = d.Title
	}
	existing := map[string]bool{}
	titleToRealID := map[string]string{}
	if docs, err := s.ListKbDocuments(store.KbFilter{}); err == nil {
		for _, d := range docs {
			existing[d.ID] = true
			titleToRealID[d.Title] = d.ID
		}
	}
	// 解析函数：若 id 已在库内则保留；否则若为 mock 概念 id 且对应 Title 在库内，则映射为真实 id；否则原样返回（后续触发 I-1 校验）
	resolve := func(id string) string {
		if existing[id] {
			return id
		}
		if title, ok := mockIDToTitle[id]; ok {
			if rid, ok2 := titleToRealID[title]; ok2 {
				return rid
			}
		}
		return id
	}
	// 对每个 GoldenItem 的 DocIDs 与 DistractorDocIDs 进行解析，并收集经解析后仍不在库内的缺失 id（严格校验，exit 非零）
	missingSet := map[string]bool{}
	for idx := range items {
		resolved := make([]string, 0, len(items[idx].Expect.DocIDs))
		for _, did := range items[idx].Expect.DocIDs {
			rid := resolve(did)
			resolved = append(resolved, rid)
			if rid != "" && !existing[rid] {
				missingSet[rid] = true
			}
		}
		items[idx].Expect.DocIDs = resolved
		resolvedDist := make([]string, 0, len(items[idx].Expect.DistractorDocIDs))
		for _, did := range items[idx].Expect.DistractorDocIDs {
			rid := resolve(did)
			resolvedDist = append(resolvedDist, rid)
			if rid != "" && !existing[rid] {
				missingSet[rid] = true
			}
		}
		items[idx].Expect.DistractorDocIDs = resolvedDist
	}
	if len(missingSet) > 0 {
		var ids []string
		for id := range missingSet {
			ids = append(ids, id)
		}
		fmt.Fprintf(os.Stderr, "标注集引用了不存在的 doc_id: %s\n", strings.Join(ids, ", "))
		os.Exit(1)
	}

	// 变体解析：-variants-file 提供具名变体，-variants 为 ";" 分隔的规格串
	var fileVariants []retrieve.Variant
	if *variantsFile != "" {
		data, err := os.ReadFile(*variantsFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读变体文件失败: %v\n", err)
			os.Exit(1)
		}
		fileVariants, err = retrieve.ParseVariantsJSON(data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "解析变体文件失败: %v\n", err)
			os.Exit(1)
		}
	}
	variants, err := selectVariants(*variantsFlag, fileVariants)
	if err != nil {
		fmt.Fprintf(os.Stderr, "解析变体失败: %v\n", err)
		os.Exit(1)
	}

	var reranker retrieve.Reranker
	if rr, err := retrieve.NewReranker(cfg); err == nil {
		reranker = rr
	}
	var rewriteC retrieve.Completer
	if lc != nil {
		rewriteC = rewriteAdapter{c: lc}
	}

	var reports []VariantReport
	for _, v := range variants {
		searcher, err := retrieve.NewSearcherVariant(s, embedder, reranker, cfg, rewriteC, v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "构造变体 %s 失败: %v\n", v.Name, err)
			os.Exit(1)
		}
		itemResults := evaluateItems(searcher, items)
		overall, byType := aggregate(itemResults)
		reports = append(reports, VariantReport{Name: v.Name, Spec: v.Steps, Overall: overall, ByType: byType, Items: itemResults})
	}

	// 以 Name=="baseline" 定位基准并置于首位，避免依赖输入顺序
	reports = baselineFirst(reports)
	baseReport := reports[0]
	base := baseReport.Overall
	cmp := ComparisonReport{
		Meta: CompareMeta{
			Timestamp: time.Now().Format(time.RFC3339), Golden: goldenPath, Enhance: *enhance, Baseline: baseReport.Name,
		},
		Variants: reports,
		Deltas:   map[string]Metrics{},
	}
	for _, r := range reports[1:] {
		cmp.Deltas[r.Name] = delta(base, r.Overall)
	}
	printComparison(os.Stdout, cmp)
	if *out != "" {
		b, _ := json.MarshalIndent(cmp, "", "  ")
		if err := os.WriteFile(*out, b, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "写报告失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("报告已写入 %s\n", *out)
	}

	// Phoenix 逐变体回写：未配置 PHOENIX_BASE_URL 时零外呼，仅本地报告照常。
	if cfg.PhoenixBaseURL != "" {
		pc := phoenix.NewClient(cfg.PhoenixBaseURL, cfg.PhoenixAPIKey)
		dsID, err := findRagDataset(pc)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Phoenix 数据集解析失败，跳过回写: %v\n", err)
		} else {
			for _, r := range reports {
				rep := goldenset.RagReport{
					Meta:    goldenset.RagMeta{Timestamp: time.Now(), Enhance: *enhance, TopK: 3, Variant: r.Name, VariantSpec: r.Spec},
					Items:   r.Items,
					Overall: toOverall(r),
				}
				sum, err := goldenset.PushRagExperiment(pc, dsID, rep, goldenset.RagDatasetName+"/"+r.Name)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Phoenix 回写变体 %s: %v (%d failures)\n", r.Name, err, sum.Failures)
					continue
				}
				fmt.Printf("Phoenix 变体 %s → experiment %s\n", r.Name, sum.ID)
			}
		}
	}
}

// evaluateItems 在给定检索器上跑完 golden 题并按既有口径计算逐题指标。
func evaluateItems(searcher retrieve.Searcher, items []goldenset.Item) []goldenset.ItemResult {
	var itemResults []goldenset.ItemResult
	for _, it := range items {
		k := it.TopK
		if k <= 0 {
			k = 3
		}
		var traces []retrieve.LinkTrace
		ctx := retrieve.WithTraceSink(context.Background(), func(t []retrieve.LinkTrace) { traces = t })
		qStart := time.Now()
		res, err := searcher.SearchCases(ctx, retrieve.Query{Text: it.Question, History: it.History}, retrieve.Options{TopK: k})
		qEnd := time.Now()
		if err != nil {
			res = nil
		}
		ranked := make([]string, len(res))
		for i, r := range res {
			ranked[i] = r.DocID
		}
		ir := goldenset.ItemResult{
			ID: it.ID, Type: it.Type, Question: it.Question, TopK: k,
			RankedDocIDs: ranked, Behavior: it.Expect.Behavior,
			StartedAt: qStart, EndedAt: qEnd, Traces: convertTraces(traces),
		}
		if it.Expect.Behavior == "reject" {
			if len(ranked) == 0 {
				ir.RejectOK = true
			}
		} else {
			relevant := map[string]bool{}
			for _, id := range it.Expect.DocIDs {
				relevant[id] = true
			}
			mrr := ComputeMRR(ranked, relevant)
			ndcg := ComputeNDCG(ranked, relevant, k)
			rec := ComputeRecallAtK(ranked, relevant, k)
			prec := ComputePrecisionAtK(ranked, relevant, k)
			ir.MRR, ir.NDCG, ir.Recall, ir.Precision = mrr, ndcg, rec, prec
		}
		itemResults = append(itemResults, ir)
	}
	return itemResults
}

// rewriteAdapter 把双参 llm.Client 适配为单参 retrieve.Completer（system 置空）。
type rewriteAdapter struct{ c *llm.Client }

func (r rewriteAdapter) Complete(ctx context.Context, prompt string) (string, error) {
	return r.c.Complete(ctx, "", prompt)
}

func hasVariant(vs []retrieve.Variant, name string) bool {
	for _, v := range vs {
		if v.Name == name {
			return true
		}
	}
	return false
}

// selectVariants 解析变体选择：-variants 非空时按规格解析（可引用文件具名变体）；
// 仅给 -variants-file 时直接使用文件内全部变体；两者皆空时默认 baseline。
// baseline 始终作为对比基准前插（未显式列出时）。
func selectVariants(spec string, fileVariants []retrieve.Variant) ([]retrieve.Variant, error) {
	named := make(map[string]retrieve.Variant, len(fileVariants))
	for _, v := range fileVariants {
		named[v.Name] = v
	}
	var out []retrieve.Variant
	if strings.TrimSpace(spec) == "" && len(fileVariants) > 0 {
		out = append(out, fileVariants...)
	} else {
		vs, err := retrieve.ParseVariants(spec, named)
		if err != nil {
			return nil, err
		}
		out = vs
	}
	if !hasVariant(out, "baseline") {
		b, _ := retrieve.ParseVariantPatch("baseline", "", retrieve.BaselineVariant())
		out = append([]retrieve.Variant{b}, out...)
	}
	return out, nil
}

func printComparison(w io.Writer, c ComparisonReport) {
	fmt.Fprintf(w, "rageval: %s baseline=%s\n", c.Meta.Golden, c.Meta.Baseline)
	fmt.Fprintf(w, "%-24s %6s %8s %8s %10s %8s\n", "variant", "MRR", "NDCG", "Recall", "Precision", "Reject")
	for _, v := range c.Variants {
		m := v.Overall
		fmt.Fprintf(w, "%-24s %6.3f %8.3f %8.3f %10.3f %8.3f\n", v.Name, m.MRR, m.NDCG, m.Recall, m.Precision, m.RejectRate)
	}
	if len(c.Deltas) > 0 {
		fmt.Fprintf(w, "-- delta vs %s --\n", c.Meta.Baseline)
		// 按 variants 顺序输出（跳过基线），保证可复现，不依赖 map 遍历顺序。
		for _, v := range c.Variants {
			if v.Name == c.Meta.Baseline {
				continue
			}
			d, ok := c.Deltas[v.Name]
			if !ok {
				continue
			}
			fmt.Fprintf(w, "%-24s ΔMRR %+.3f ΔNDCG %+.3f ΔRecall %+.3f ΔPrec %+.3f ΔReject %+.3f\n", v.Name, d.MRR, d.NDCG, d.Recall, d.Precision, d.RejectRate)
		}
	}
}

// resolveGoldenPath 尝试解析标注集路径，兼容 backend/ 前缀与相对路径差异
func resolveGoldenPath(p string) string {
	if p == "" {
		return p
	}
	if _, err := os.Stat(p); err == nil {
		return p
	}
	// 去掉 backend/ 前缀重试（当 cwd=backend 时传入 backend/testdata/...）
	trimmed := strings.TrimPrefix(p, "backend/")
	if trimmed != p {
		if _, err := os.Stat(trimmed); err == nil {
			return trimmed
		}
	}
	// 尝试上层 backend 前缀（当 cwd=worktree root 时传入 testdata/...）
	alt := filepath.Join("backend", p)
	if _, err := os.Stat(alt); err == nil {
		return alt
	}
	// 尝试 backend/testdata 的绝对或相对兜底
	if filepath.Base(p) == "golden_set.jsonl" {
		candidates := []string{
			"backend/testdata/golden_set.jsonl",
			"testdata/golden_set.jsonl",
			filepath.Join("..", "backend", "testdata", "golden_set.jsonl"),
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
	}
	return p
}
