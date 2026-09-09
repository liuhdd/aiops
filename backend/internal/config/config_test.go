package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadFromEnvDefaults(t *testing.T) {
	env := map[string]string{
		"LLM_API_KEY": "sk-test",
	}
	cfg := LoadFromEnv(func(k string) string { return env[k] })
	if cfg.LLMAPIKey != "sk-test" {
		t.Fatalf("LLMAPIKey = %q, want sk-test", cfg.LLMAPIKey)
	}
	if cfg.LLMBaseURL != "https://api.deepseek.com/v1" {
		t.Fatalf("LLMBaseURL = %q, want default", cfg.LLMBaseURL)
	}
	if cfg.LLMModel != "deepseek-chat" {
		t.Fatalf("LLMModel = %q, want deepseek-chat", cfg.LLMModel)
	}
	if cfg.SimInterval != 20*time.Second {
		t.Fatalf("SimInterval = %v, want 20s", cfg.SimInterval)
	}
	if cfg.Port != ":8080" {
		t.Fatalf("Port = %q, want :8080", cfg.Port)
	}
}

func TestLoadFromEnvSimInterval(t *testing.T) {
	env := map[string]string{"SIM_INTERVAL_SECONDS": "7"}
	cfg := LoadFromEnv(func(k string) string { return env[k] })
	if cfg.SimInterval != 7*time.Second {
		t.Fatalf("SimInterval = %v, want 7s", cfg.SimInterval)
	}
}

func TestLoadFromEnvContextDefaults(t *testing.T) {
	cfg := LoadFromEnv(func(string) string { return "" })
	if !cfg.React {
		t.Fatalf("React default should be true")
	}
	if cfg.TopK != 3 {
		t.Fatalf("TopK = %d, want 3", cfg.TopK)
	}
}

func TestLoadFromEnvContextOverride(t *testing.T) {
	env := map[string]string{"CONTEXT_REACT": "false", "CONTEXT_TOP_K": "5"}
	cfg := LoadFromEnv(func(k string) string { return env[k] })
	if cfg.React {
		t.Fatalf("React = true, want false")
	}
	if cfg.TopK != 5 {
		t.Fatalf("TopK = %d, want 5", cfg.TopK)
	}
}

func TestLoad(t *testing.T) {
	os.Setenv("LLM_API_KEY", "sk-test")
	defer os.Unsetenv("LLM_API_KEY")
	os.Setenv("DB_PATH", filepath.Join(t.TempDir(), "test.db"))
	defer os.Unsetenv("DB_PATH")
	cfg := Load()
	if cfg.LLMAPIKey != "sk-test" || cfg.DBPath == "" {
		t.Fatalf("Load() failed: %+v", cfg)
	}
}

func TestLoadFromEnvLightIntentDefaults(t *testing.T) {
	env := map[string]string{"LLM_BASE_URL": "https://main.example.com/v1", "LLM_MODEL": "main-model"}
	cfg := LoadFromEnv(func(k string) string { return env[k] })
	if cfg.LLMLightBaseURL != "https://main.example.com/v1" {
		t.Fatalf("LLMLightBaseURL = %q, want 同 LLM_BASE_URL", cfg.LLMLightBaseURL)
	}
	if cfg.LLMLightModel != "main-model" {
		t.Fatalf("LLMLightModel = %q, want 同 LLM_MODEL", cfg.LLMLightModel)
	}
	if cfg.MaxSplit != 3 {
		t.Fatalf("MaxSplit = %d, want 3", cfg.MaxSplit)
	}
}

func TestLoadFromEnvLightIntentOverride(t *testing.T) {
	env := map[string]string{
		"LLM_BASE_URL":       "https://main.example.com/v1",
		"LLM_MODEL":          "main-model",
		"LLM_LIGHT_BASE_URL": "https://light.example.com/v1",
		"LLM_LIGHT_MODEL":    "light-model",
		"INTENT_MAX_SPLIT":   "5",
	}
	cfg := LoadFromEnv(func(k string) string { return env[k] })
	if cfg.LLMLightBaseURL != "https://light.example.com/v1" {
		t.Fatalf("LLMLightBaseURL = %q, want light url", cfg.LLMLightBaseURL)
	}
	if cfg.LLMLightModel != "light-model" {
		t.Fatalf("LLMLightModel = %q, want light-model", cfg.LLMLightModel)
	}
	if cfg.MaxSplit != 5 {
		t.Fatalf("MaxSplit = %d, want 5", cfg.MaxSplit)
	}
}

func TestLoadFromEnvEmbedDefaults(t *testing.T) {
	env := map[string]string{"LLM_BASE_URL": "https://main.example.com/v1", "LLM_MODEL": "main-model"}
	cfg := LoadFromEnv(func(k string) string { return env[k] })
	if cfg.LLMEmbedBaseURL != "https://main.example.com/v1" {
		t.Fatalf("LLMEmbedBaseURL = %q, want 同 LLM_BASE_URL", cfg.LLMEmbedBaseURL)
	}
	if cfg.LLMEmbedModel != "text-embedding-3-small" {
		t.Fatalf("LLMEmbedModel = %q, want text-embedding-3-small", cfg.LLMEmbedModel)
	}
}

func TestLoadFromEnvEmbedOverride(t *testing.T) {
	env := map[string]string{
		"LLM_BASE_URL":       "https://main.example.com/v1",
		"LLM_EMBED_MODEL":    "text-embedding-3-large",
		"LLM_EMBED_BASE_URL": "https://embed.example.com/v1",
	}
	cfg := LoadFromEnv(func(k string) string { return env[k] })
	if cfg.LLMEmbedBaseURL != "https://embed.example.com/v1" {
		t.Fatalf("LLMEmbedBaseURL = %q, want override", cfg.LLMEmbedBaseURL)
	}
	if cfg.LLMEmbedModel != "text-embedding-3-large" {
		t.Fatalf("LLMEmbedModel = %q, want override", cfg.LLMEmbedModel)
	}
}

func TestConfigRetrieveFlags(t *testing.T) {
	cfg := LoadFromEnv(func(k string) string {
		m := map[string]string{"RETRIEVE_FINGERPRINT": "0", "RETRIEVE_RRF": "0", "RERANK_BASE_URL": "https://r.example", "RERANK_API_KEY": "k", "RERANK_MODEL": "m", "RERANK_FLOOR": "0.35"}
		return m[k]
	})
	if cfg.RetrieveFingerprint || cfg.RetrieveRRF {
		t.Fatal("flags")
	}
	if cfg.RerankBaseURL != "https://r.example" || cfg.RerankModel != "m" || cfg.RerankFloor != 0.35 {
		t.Fatalf("%+v", cfg)
	}
}
func TestConfigDefaults(t *testing.T) {
	cfg := LoadFromEnv(func(string) string { return "" })
	if !cfg.RetrieveFingerprint || !cfg.RetrieveRRF || cfg.RerankFloor != 0.2 {
		t.Fatalf("%+v", cfg)
	}
}
func TestPhoenixConfig(t *testing.T) {
	cfg := LoadFromEnv(func(k string) string {
		m := map[string]string{
			"PHOENIX_BASE_URL":   "http://localhost:6006",
			"PHOENIX_PROMPT_TAG": "staging",
			"PHOENIX_PROMPT_TTL": "30",
			"PHOENIX_API_KEY":    "sk-p",
		}
		return m[k]
	})
	if cfg.PhoenixBaseURL != "http://localhost:6006" || cfg.PhoenixTag != "staging" ||
		cfg.PhoenixTTL != 30 || cfg.PhoenixAPIKey != "sk-p" {
		t.Fatalf("Phoenix 配置读取错误: %+v", cfg)
	}
}

func TestPhoenixConfigDefaults(t *testing.T) {
	cfg := LoadFromEnv(func(string) string { return "" })
	if cfg.PhoenixBaseURL != "" {
		t.Fatal("PHOENIX_BASE_URL 默认应为空（关闭拉取）")
	}
	if cfg.PhoenixTag != "production" || cfg.PhoenixTTL != 60 {
		t.Fatalf("默认 tag/ttl 错误: %s/%d", cfg.PhoenixTag, cfg.PhoenixTTL)
	}
}

func TestConfigKbEnhance(t *testing.T) {
	cfg := LoadFromEnv(func(k string) string {
		m := map[string]string{"KB_ENHANCE": "0", "KB_ENHANCE_SUMMARY": "false", "KB_CDTA_THRESHOLD": "0.9"}
		return m[k]
	})
	if cfg.KBEnhance || cfg.KBEnhanceSummary {
		t.Fatalf("want false got %+v", cfg)
	}
	if cfg.KBCdtaThreshold != 0.9 {
		t.Fatalf("threshold %v", cfg.KBCdtaThreshold)
	}
	if !cfg.KBEnhanceQuestions || !cfg.KBEnhanceCDTA {
		t.Fatalf("default true")
	}
}
