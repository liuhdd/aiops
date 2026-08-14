package llm

import (
	"errors"
	"testing"

	"aiops/internal/config"
)

func TestNewClientNoAPIKey(t *testing.T) {
	cfg := config.LoadFromEnv(func(string) string { return "" })
	c, err := NewClient(cfg)
	if c != nil || !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("expected ErrNoAPIKey, got %v c=%+v", err, c)
	}
}

func TestParseJSONFenced(t *testing.T) {
	var m map[string]string
	raw := "```json\n{\"a\":\"b\"}\n```"
	if err := ParseJSON(raw, &m); err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
	if m["a"] != "b" {
		t.Fatalf("parsed = %+v", m)
	}
}

func TestParseJSONPlain(t *testing.T) {
	var m map[string]string
	if err := ParseJSON(`{"x":"y"}`, &m); err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
	if m["x"] != "y" {
		t.Fatalf("parsed = %+v", m)
	}
}

func TestNewLightClientNoKey(t *testing.T) {
	cfg := config.Config{LLMAPIKey: ""}
	c, err := NewLightClient(cfg)
	if err == nil || c != nil {
		t.Fatalf("无 key 应返回 nil+err, got c=%v err=%v", c, err)
	}
}

func TestNewLightClientDefaultsToMain(t *testing.T) {
	cfg := config.Config{
		LLMAPIKey: "sk-test", LLMBaseURL: "https://main.example.com/v1", LLMModel: "main-model",
	}
	c, err := NewLightClient(cfg)
	if err != nil {
		t.Fatalf("NewLightClient: %v", err)
	}
	if c == nil {
		t.Fatal("有 key 应返回非 nil client")
	}
}
