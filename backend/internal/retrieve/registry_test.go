package retrieve

import (
	"testing"

	"aiops/internal/config"
)

func TestBuildChainSelectsImplByVariant(t *testing.T) {
	env := newSearchEnv(nil, nil, nil, config.Default())
	v := BaselineVariant()
	v.Steps["rerank"] = "none"
	v.Steps["fuse"] = "weighted"
	ch, err := BuildChain(v, Deps{Env: env})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, l := range ch.links {
		got[l.link.Kind()] = l.impl
	}
	if got["rerank"] != "none" || got["fuse"] != "weighted" || got["recall"] != "hybrid" {
		t.Fatalf("got=%v", got)
	}
}

func TestBuildChainRejectsUnknownImpl(t *testing.T) {
	v := BaselineVariant()
	v.Steps["recall"] = "no-such-impl"
	if _, err := BuildChain(v, Deps{Env: newSearchEnv(nil, nil, nil, config.Default())}); err == nil {
		t.Fatal("未知实现应报错")
	}
}

func TestBuildChainSkipsNone(t *testing.T) {
	v := BaselineVariant()
	v.Steps["fingerprint"] = "none"
	ch, _ := BuildChain(v, Deps{Env: newSearchEnv(nil, nil, nil, config.Default())})
	for _, l := range ch.links {
		if l.link.Kind() == "fingerprint" {
			t.Fatal("none 应跳过该环节")
		}
	}
}
