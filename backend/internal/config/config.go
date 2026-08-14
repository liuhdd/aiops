package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	LLMAPIKey       string
	LLMBaseURL      string
	LLMModel        string
	DBPath          string
	SimInterval     time.Duration
	Port            string
	React           bool   // CONTEXT_REACT，ReAct 循环开关，默认 true
	TopK            int    // CONTEXT_TOP_K，每源上下文条数，默认 3
	LLMLightBaseURL string // LLM_LIGHT_BASE_URL，轻量意图模型 BaseURL，默认同 LLM_BASE_URL
	LLMLightModel   string // LLM_LIGHT_MODEL，轻量意图模型，默认同 LLM_MODEL
	MaxSplit        int    // INTENT_MAX_SPLIT，单条输入多意图拆分上限，默认 3
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
	react := true
	if v := lookup("CONTEXT_REACT"); v == "0" || v == "false" {
		react = false
	}
	topK := 3
	if v := lookup("CONTEXT_TOP_K"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			topK = n
		}
	}
	lightBase := lookup("LLM_LIGHT_BASE_URL")
	if lightBase == "" {
		lightBase = base
	}
	lightModel := lookup("LLM_LIGHT_MODEL")
	if lightModel == "" {
		lightModel = model
	}
	maxSplit := 3
	if v := lookup("INTENT_MAX_SPLIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxSplit = n
		}
	}
	return Config{
		LLMAPIKey:       lookup("LLM_API_KEY"),
		LLMBaseURL:      base,
		LLMModel:        model,
		DBPath:          db,
		SimInterval:     time.Duration(interval) * time.Second,
		Port:            port,
		React:           react,
		TopK:            topK,
		LLMLightBaseURL: lightBase,
		LLMLightModel:   lightModel,
		MaxSplit:        maxSplit,
	}
}
