package config

import "testing"

func TestRetrieveVariantDefaults(t *testing.T) {
	c := Default()
	if got := c.RetrieveSteps("pipeline")["rewrite"]; got != "passthrough" {
		t.Fatalf("pipeline rewrite=%q", got)
	}
	if got := c.RetrieveSteps("chat")["rewrite"]; got != "llm" {
		t.Fatalf("chat rewrite=%q", got)
	}
	if got := c.RetrieveSteps("pipeline")["fuse"]; got != "rrf" {
		t.Fatalf("fuse=%q", got)
	}
}

func TestRetrieveVariantEnvOverride(t *testing.T) {
	c := LoadFromEnv(func(k string) string {
		switch k {
		case "RETRIEVE_VARIANT":
			return "rerank=none,fuse=weighted"
		case "RETRIEVE_VARIANT_CHAT":
			return "rewrite=passthrough"
		}
		return ""
	})
	if c.RetrieveSteps("pipeline")["rerank"] != "none" || c.RetrieveSteps("pipeline")["fuse"] != "weighted" {
		t.Fatalf("base=%v", c.RetrieveSteps("pipeline"))
	}
	if c.RetrieveSteps("chat")["rewrite"] != "passthrough" {
		t.Fatalf("chat=%v", c.RetrieveSteps("chat"))
	}
}
