package main

import (
	"context"
	"log"

	"aiops/internal/config"
	"aiops/internal/llm"
)

func main() {
	cfg := config.Load()
	client, err := llm.NewClient(cfg)
	if err != nil {
		log.Printf("llm unavailable (will use rule fallback): %v", err)
	}
	_ = client
	_ = context.Background()
	log.Printf("starting aiops backend on %s", cfg.Port)
}
