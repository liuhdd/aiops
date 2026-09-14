package retrieve

import (
	"context"
	"testing"

	"aiops/internal/config"
)

func TestProfileCtxRoundTrip(t *testing.T) {
	ctx := WithProfile(context.Background(), "chat")
	if got := ProfileFromCtx(ctx); got != "chat" {
		t.Fatalf("profile=%q", got)
	}
	if got := ProfileFromCtx(context.Background()); got != "pipeline" {
		t.Fatalf("default=%q", got)
	}
}

func TestHistoryCtxRoundTrip(t *testing.T) {
	ctx := WithHistory(context.Background(), []string{"a", "b"})
	got := HistoryFromCtx(ctx)
	if len(got) != 2 || got[1] != "b" {
		t.Fatalf("history=%v", got)
	}
}

func TestProfileOfPrefersOption(t *testing.T) {
	ctx := WithProfile(context.Background(), "chat")
	if got := ProfileOf(ctx, Options{Profile: "pipeline"}); got != "pipeline" {
		t.Fatalf("got=%q", got)
	}
	if got := ProfileOf(ctx, Options{}); got != "chat" {
		t.Fatalf("got=%q", got)
	}
}

// TestSearcherProfilesRewriteOnlyForChat 固化默认行为：pipeline 有 history 也不改写，
// chat 有 history 则经 rewrite=llm 触发改写。
func TestSearcherProfilesRewriteOnlyForChat(t *testing.T) {
	s := seedRetrieveStore(t)
	fc := &rewriteFake{out: "GPU 掉卡"}
	se, err := NewSearcherProfiles(s, fakeEmbedder{}, nil, config.Default(), fc, nil)
	if err != nil {
		t.Fatalf("NewSearcherProfiles: %v", err)
	}
	if _, err := se.SearchCases(context.Background(),
		Query{Text: "它怎么处理", History: []string{"上一句"}}, Options{TopK: 1, Profile: "pipeline"}); err != nil {
		t.Fatalf("pipeline search: %v", err)
	}
	if fc.called {
		t.Fatalf("pipeline 不应触发改写")
	}
	if _, err := se.SearchCases(context.Background(),
		Query{Text: "它怎么处理", History: []string{"上一句"}}, Options{TopK: 1, Profile: "chat"}); err != nil {
		t.Fatalf("chat search: %v", err)
	}
	if !fc.called {
		t.Fatalf("chat 应触发改写")
	}
}

// TestSearcherProfilesCtxProfileSelectsChain 校验无 Options.Profile 时回退 ctx。
func TestSearcherProfilesCtxProfileSelectsChain(t *testing.T) {
	s := seedRetrieveStore(t)
	fc := &rewriteFake{out: "GPU 掉卡"}
	se, err := NewSearcherProfiles(s, fakeEmbedder{}, nil, config.Default(), fc, nil)
	if err != nil {
		t.Fatalf("NewSearcherProfiles: %v", err)
	}
	ctx := WithProfile(context.Background(), "chat")
	if _, err := se.SearchCases(ctx, Query{Text: "它怎么处理", History: []string{"h"}}, Options{TopK: 1}); err != nil {
		t.Fatalf("ctx chat search: %v", err)
	}
	if !fc.called {
		t.Fatalf("ctx=chat 应选 chat 链并触发改写")
	}
}
