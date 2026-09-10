package config

import (
	"testing"
)

func TestDefaultIgnoresEnv(t *testing.T) {
	t.Setenv("KB_ENHANCE", "0")
	t.Setenv("KB_CDTA_THRESHOLD", "0.5")
	cfg := Default()
	if !cfg.KBEnhance {
		t.Fatalf("Default 应忽略环境变量，KBEnhance 应为 true，got %+v", cfg)
	}
	if cfg.KBCdtaThreshold != 0.85 {
		t.Fatalf("Default 阈值应为 0.85，got %v", cfg.KBCdtaThreshold)
	}
}

func TestDefaultEqualsEmptyEnv(t *testing.T) {
	want := LoadFromEnv(func(string) string { return "" })
	got := Default()
	if got != want {
		t.Fatalf("Default 应等于空环境 LoadFromEnv\ngot  %+v\nwant %+v", got, want)
	}
}
