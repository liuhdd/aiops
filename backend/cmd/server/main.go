package main

import (
	"context"
	"log"
	"net/http"

	"aiops/internal/agent"
	"aiops/internal/api"
	"aiops/internal/config"
	"aiops/internal/hub"
	"aiops/internal/llm"
	"aiops/internal/mockdata"
	"aiops/internal/model"
	"aiops/internal/pipeline"
	"aiops/internal/simulator"
	"aiops/internal/store"
)

func main() {
	cfg := config.Load()

	lc, err := llm.NewClient(cfg)
	if err != nil {
		log.Printf("llm unavailable (will use rule fallback): %v", err)
	}

	mockdata.GenerateAll(42)

	st, err := store.New(cfg.DBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	eng := pipeline.Build(st, lc)
	h := hub.New()

	ag := agent.New(st, lc, eng)
	sim := simulator.New(st, func(a model.Alert) {
		go func() {
			_ = eng.Run(context.Background(), a, nil)
		}()
	})
	sim.Start(context.Background(), cfg.SimInterval)

	handler := api.NewServer(cfg, st, ag, eng, h, sim)
	log.Printf("aiops backend listening on %s", cfg.Port)
	if err := http.ListenAndServe(cfg.Port, handler); err != nil {
		log.Fatal(err)
	}
}
