package config

import (
	"os"
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
	return Config{
		LLMAPIKey:   lookup("LLM_API_KEY"),
		LLMBaseURL:  base,
		LLMModel:    model,
		DBPath:      db,
		SimInterval: 20 * time.Second,
		Port:        port,
	}
}
