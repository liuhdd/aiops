package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"aiops/internal/config"
	"aiops/internal/kb"
	"aiops/internal/llm"
	"aiops/internal/retrieve"
	"aiops/internal/store"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "只统计差异，不写库")
	dbPath := flag.String("db", "", "DB 路径，空则用配置 DB_PATH")
	flag.Parse()

	cfg := config.Load()
	path := *dbPath
	if path == "" {
		path = cfg.DBPath
	}
	st, err := store.New(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open db:", err)
		os.Exit(1)
	}
	defer st.Close()

	// 与线上一致：无 key 时 embed/enrich 为 nil，自动走规则回退。
	var embedder retrieve.Embedder
	if e, err := retrieve.NewLLMEmbedder(cfg); err == nil {
		embedder = e
	}
	var lc kb.Completer
	if c, err := llm.NewClient(cfg); err == nil && c != nil {
		lc = kb.AdaptCompleter(c)
	}
	svc := kb.NewServiceWithEnrich(st, nil, embedder, lc, cfg)

	docs, err := st.ListKbDocuments(store.KbFilter{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "list docs:", err)
		os.Exit(1)
	}
	ctx := context.Background()
	fail := 0
	done := 0
	for _, d := range docs {
		var stat *kb.RechunkStat
		if *dryRun {
			stat, err = svc.PreviewRechunk(ctx, d.ID)
		} else {
			stat, err = svc.RechunkDoc(ctx, d.ID)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "doc %s: 回填失败: %v\n", d.Title, err)
			fail++
			continue
		}
		if stat.Skipped {
			fmt.Printf("doc %s: 跳过（空内容）\n", stat.Title)
			continue
		}
		fmt.Printf("doc %s: chunks 旧%d→新%d, flow 旧%d→新%d, unresolved 旧%d→新%d, inbound=%d\n",
			stat.Title, stat.OldChunks, stat.NewChunks, stat.OldFlow, stat.NewFlow,
			stat.OldUnresolved, stat.NewUnresolved, stat.InboundEdges)
		done++
	}
	mode := "回填完成"
	if *dryRun {
		mode = "试运行完成（未写库）"
	}
	fmt.Printf("%s：共%d篇，成功%d篇，失败%d篇\n", mode, len(docs), done, fail)
	if fail > 0 {
		os.Exit(1)
	}
}
