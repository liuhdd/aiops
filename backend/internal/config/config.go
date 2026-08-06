package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	LLMAPIKey   string
	LLMBaseURL  string
	LLMModel    string
	DBPath      string
	SimInterval time.Duration
	Port        string
}

func Load() Config {
	return LoadFromEnv(os.Getenv)
}

func LoadFromEnv(lookup func(string) string) Config {
	base := lookup("LLM_BASE_URL")
	if base == "" {
		base = "https://api.deepseek.com/v1"
	}
	model := lookup("LLM_MODEL")
	if model == "" {
		model = "deepseek-chat"
	}
	port := lookup("PORT")
	if port == "" {
		port = ":8080"
	}
	db := lookup("DB_PATH")
	if db == "" {
		db = "aiops.db"
	}
	interval := 20
	if v := lookup("SIM_INTERVAL_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			interval = n
		}
	}
	return Config{
		LLMAPIKey:   lookup("LLM_API_KEY"),
		LLMBaseURL:  base,
		LLMModel:    model,
		DBPath:      db,
		SimInterval: time.Duration(interval) * time.Second,
		Port:        port,
	}
}
