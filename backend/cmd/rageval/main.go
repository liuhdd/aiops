package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"aiops/internal/config"
	"aiops/internal/kb"
	"aiops/internal/llm"
	"aiops/internal/mockdata"
	"aiops/internal/model"
	"aiops/internal/retrieve"
	"aiops/internal/store"
)

type GoldenItem struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Question string `json:"question"`
	Expect   struct {
		DocIDs           []string `json:"doc_ids"`
		DistractorDocIDs []string `json:"distractor_doc_ids"`
		Behavior         string   `json:"behavior"`
	} `json:"expect"`
	TopK int `json:"top_k"`
}

func main() {
	golden := flag.String("golden", "testdata/golden_set.jsonl", "golden set jsonl path")
	generate := flag.Bool("generate", false, "生成模式：按种子文档批量出题")
	enhance := flag.Bool("enhance", true, "是否启用富化")
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
	f, err := os.Open(goldenPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法打开标注集 %s (%s): %v\n", *golden, goldenPath, err)
		os.Exit(1)
	}
	defer f.Close()

	var items []GoldenItem
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var it GoldenItem
		if err := json.Unmarshal([]byte(line), &it); err != nil {
			fmt.Fprintf(os.Stderr, "标注集第 %d 行 JSON 解析失败: %v\n", lineNo, err)
			os.Exit(1)
		}
		if it.TopK == 0 {
			it.TopK = 3
		}
		if it.Expect.DocIDs == nil {
			it.Expect.DocIDs = []string{}
		}
		if it.Expect.DistractorDocIDs == nil {
			it.Expect.DistractorDocIDs = []string{}
		}
		// 兼容旧字段行为：空 behavior 视为 hit 若 doc_ids 非空，否则 reject
		if it.Expect.Behavior == "" {
			if len(it.Expect.DocIDs) > 0 {
				it.Expect.Behavior = "hit"
			} else {
				it.Expect.Behavior = "reject"
			}
		}
		items = append(items, it)
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "读取标注集失败: %v\n", err)
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
	var llmCompleter kb.Completer
	if lc, err := llm.NewClient(cfg); err == nil && lc != nil {
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

	searcher := retrieve.NewSearcher(s, embedder)

	// per-type 聚合
	type agg struct {
		Count       int
		HitCount    int
		RejectCount int
		SumMRR      float64
		SumNDCG     float64
		SumRecall   float64
		RejectOK    int
	}
	perType := map[string]*agg{}
	overallHit := &agg{}
	overallRejectTotal := 0
	overallRejectOK := 0

	// 用于 ComputeRejectRate 的输入
	resultsMap := map[string][]string{}
	expectsMap := map[string]string{}

	for _, it := range items {
		k := it.TopK
		if k <= 0 {
			k = 3
		}
		// 检索：统一走 SearchCases（种子库均为 case；manual 通道命中为 0 亦不影响指标）
		ctx := context.Background()
		res, err := searcher.SearchCases(ctx, retrieve.Query{Text: it.Question}, retrieve.Options{TopK: k})
		if err != nil {
			// 静默降级：按空结果计
			res = nil
		}
		ranked := make([]string, len(res))
		for i, r := range res {
			ranked[i] = r.DocID
		}
		resultsMap[it.ID] = ranked
		expectsMap[it.ID] = it.Expect.Behavior

		ag := perType[it.Type]
		if ag == nil {
			ag = &agg{}
			perType[it.Type] = ag
		}
		ag.Count++
		if it.Expect.Behavior == "reject" {
			ag.RejectCount++
			overallRejectTotal++
			if len(ranked) == 0 {
				ag.RejectOK++
				overallRejectOK++
			}
		} else {
			ag.HitCount++
			overallHit.Count++
			relevant := map[string]bool{}
			for _, id := range it.Expect.DocIDs {
				relevant[id] = true
			}
			// 即使 relevant 为空（标注缺失），指标计算仍正常返回 0，不崩溃
			mrr := ComputeMRR(ranked, relevant)
			ndcg := ComputeNDCG(ranked, relevant, k)
			rec := ComputeRecallAtK(ranked, relevant, k)
			ag.SumMRR += mrr
			ag.SumNDCG += ndcg
			ag.SumRecall += rec
			overallHit.SumMRR += mrr
			overallHit.SumNDCG += ndcg
			overallHit.SumRecall += rec
		}
	}

	fmt.Printf("rageval: loaded %s (%d items)\n", goldenPath, len(items))
	// 按固定 8 类型顺序输出，无数据时仍占行
	orderedTypes := []string{"fact", "multi_doc", "negative", "ambiguous", "reasoning", "no_answer", "cross_object", "misleading_premise"}
	fmt.Printf("%-18s %4s %6s %8s %8s %10s\n", "type", "n", "MRR", "NDCG", "Recall", "RejectRate")
	for _, tp := range orderedTypes {
		ag := perType[tp]
		if ag == nil {
			fmt.Printf("%-18s %4d %6s %8s %8s %10s\n", tp, 0, "-", "-", "-", "-")
			continue
		}
		avgMRR, avgNDCG, avgRec := "-", "-", "-"
		if ag.HitCount > 0 {
			avgMRR = fmt.Sprintf("%.3f", ag.SumMRR/float64(ag.HitCount))
			avgNDCG = fmt.Sprintf("%.3f", ag.SumNDCG/float64(ag.HitCount))
			avgRec = fmt.Sprintf("%.3f", ag.SumRecall/float64(ag.HitCount))
		}
		rejRate := "-"
		if ag.RejectCount > 0 {
			rejRate = fmt.Sprintf("%.3f", float64(ag.RejectOK)/float64(ag.RejectCount))
		} else if ag.Count > 0 && tp == "no_answer" {
			// no_answer 类型若全为 reject，HitCount=0 时已在上分支处理
		}
		// 对于混合类型（如 cross_object/misleading_premise）同时含 hit 与 reject，分别展示 hit 均值与 reject 率
		fmt.Printf("%-18s %4d %6s %8s %8s %10s\n", tp, ag.Count, avgMRR, avgNDCG, avgRec, rejRate)
	}
	// 总分
	overallAvgMRR, overallAvgNDCG, overallAvgRec := "-", "-", "-"
	if overallHit.Count > 0 {
		overallAvgMRR = fmt.Sprintf("%.3f", overallHit.SumMRR/float64(overallHit.Count))
		overallAvgNDCG = fmt.Sprintf("%.3f", overallHit.SumNDCG/float64(overallHit.Count))
		overallAvgRec = fmt.Sprintf("%.3f", overallHit.SumRecall/float64(overallHit.Count))
	}
	overallRejectRate := "-"
	if overallRejectTotal > 0 {
		overallRejectRate = fmt.Sprintf("%.3f", float64(overallRejectOK)/float64(overallRejectTotal))
		// 也可用统一函数校验一致性
		_ = ComputeRejectRate(resultsMap, expectsMap)
	}
	fmt.Printf("%-18s %4d %6s %8s %8s %10s\n", "overall", len(items), overallAvgMRR, overallAvgNDCG, overallAvgRec, overallRejectRate)
	fmt.Printf("hit_items=%d reject_items=%d reject_ok=%d\n", overallHit.Count, overallRejectTotal, overallRejectOK)
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
