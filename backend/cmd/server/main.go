package main

import (
	"log"

	"aiops/internal/config"
)

func main() {
	cfg := config.Load()
	log.Printf("starting aiops backend on %s", cfg.Port)
	_ = cfg
}
