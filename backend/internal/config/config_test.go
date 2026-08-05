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
