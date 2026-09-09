package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"aiops/internal/agent"
	"aiops/internal/api"
	"aiops/internal/config"
	ctxpkg "aiops/internal/context"
	"aiops/internal/coordinator"
	"aiops/internal/hub"
	"aiops/internal/intent"
	"aiops/internal/kb"
	"aiops/internal/llm"
	"aiops/internal/mockdata"
	"aiops/internal/model"
	"aiops/internal/pipeline"
	"aiops/internal/prompt"
	"aiops/internal/retrieve"
	"aiops/internal/simulator"
	"aiops/internal/store"
)

func main() {
	cfg := config.Load()

	if cfg.PhoenixBaseURL != "" {
		prompt.Configure(prompt.NewPhoenixFetcher(cfg.PhoenixBaseURL, cfg.PhoenixTag, cfg.PhoenixAPIKey),
			time.Duration(cfg.PhoenixTTL)*time.Second)
		log.Printf("phoenix prompt hub: %s (tag=%s, ttl=%ds)", cfg.PhoenixBaseURL, cfg.PhoenixTag, cfg.PhoenixTTL)
	}

	lc, err := llm.NewClient(cfg)
	if err != nil {
		log.Printf("llm unavailable (will use rule fallback): %v", err)
	}

	light, err := llm.NewLightClient(cfg)
	if err != nil {
		log.Printf("light llm unavailable (intent fallback to rules): %v", err)
	}

	mockdata.GenerateAll(42)

	st, err := store.New(cfg.DBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	h := hub.New()
	cs := ctxpkg.NewStore(st)
	reg := ctxpkg.NewRegistry()
	for _, t := range ctxpkg.DefaultTools() {
		reg.Register(t)
	}

	embedder, _ := retrieve.NewLLMEmbedder(cfg) // 无 key 时 nil，检索回退
	reranker, err := retrieve.NewReranker(cfg)
	if err != nil {
		if errors.Is(err, retrieve.ErrNoRerank) {
			log.Printf("rerank 不可用: %v", err)
		} else {
			log.Printf("rerank init 失败: %v", err)
		}
		reranker = nil
	}
	searcher := retrieve.NewSearcherWithConfig(st, embedder, reranker, cfg)

	eng := pipeline.BuildWithContext(st, lc, coordinator.New(), cs, reg, cfg.React, cfg.TopK)
	eng.Publish = h.Publish
	eng.SetSearcher(searcher)
	reg.Register(ctxpkg.NewSearchCaseTool(searcher))
	reg.Register(ctxpkg.NewSearchManualTool(searcher))

	intentsvc := intent.NewService(st, intent.NewMemCache())
	var enrichLC kb.Completer
	if lc != nil {
		enrichLC = llm.NewKbCompleter(lc)
	}
	kbsvc := kb.NewServiceWithEnrich(st, func() []model.LeafSpec {
		leaves, _ := intentsvc.ActiveLeaves(context.Background())
		return leaves
	}, embedder, enrichLC, cfg)
	var seeds []kb.SeedDoc
	for _, d := range mockdata.Docs() {
		seeds = append(seeds, kb.SeedDoc{
			Title: d.Title, Category: model.KBCase,
			DeviceTypes: d.DeviceTypes, Metrics: d.Metrics, Keywords: d.Keywords, Content: d.Body,
		})
	}
	if err := kbsvc.Seed(seeds); err != nil {
		log.Printf("kb seed: %v", err)
	}

	ag := agent.NewWithLeaves(st, lc, light, eng, cs, cfg.TopK, cfg.MaxSplit, func() []model.LeafSpec {
		leaves, _ := intentsvc.ActiveLeaves(context.Background())
		return leaves
	})
	ag.SetSearcher(searcher)
	sim := simulator.New(st, func(a model.Alert) {
		h.Publish("alert", a)
		go func() {
			_ = eng.Run(context.Background(), a, nil)
		}()
	})
	sim.Start(context.Background(), cfg.SimInterval)

	handler := api.NewServer(cfg, st, ag, eng, h, sim, intentsvc, kbsvc)
	log.Printf("aiops backend listening on %s", cfg.Port)
	if err := http.ListenAndServe(cfg.Port, handler); err != nil {
		log.Fatal(err)
	}
}
