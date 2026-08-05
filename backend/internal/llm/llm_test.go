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
