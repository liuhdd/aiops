package prompt

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func resetRegistry(t *testing.T) {
	t.Cleanup(func() { Configure(nil, 0) })
}

func TestAllRegistryComplete(t *testing.T) {
	if len(All) != 14 {
		t.Fatalf("注册表应含 14 条, got %d", len(All))
	}
	for name, e := range All {
		if e.Name != name || (e.Role != "system" && e.Role != "user") || e.Default == "" {
			t.Fatalf("Entry %q 不合法: %+v", name, e)
		}
	}
}

func TestRenderUsesDefaultWhenNotConfigured(t *testing.T) {
	resetRegistry(t)
	got := Render("rca-gap-hint", map[string]string{"iteration": "2"})
	if got != "这是第2轮补充取证，请重点分析新增证据。" {
		t.Fatalf("Render = %q", got)
	}
}

func TestRenderUnknownNameReturnsEmpty(t *testing.T) {
	resetRegistry(t)
	if got := Render("no-such-prompt", nil); got != "" {
		t.Fatalf("未注册 name 应返回空串, got %q", got)
	}
}

func TestRenderPrefersPhoenix(t *testing.T) {
	resetRegistry(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"template":{"messages":[{"role":"SYSTEM","content":"PHOENIX 版 {{x}}"}]}}}`))
	}))
	defer srv.Close()
	Configure(NewPhoenixFetcher(srv.URL, "production", ""), time.Minute)
	got := Render("rca-gap-hint", map[string]string{"x": "变量"})
	if got != "PHOENIX 版 变量" {
		t.Fatalf("Render = %q", got)
	}
}

func TestRenderFallsBackOnFetchFailure(t *testing.T) {
	resetRegistry(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	Configure(NewPhoenixFetcher(srv.URL, "production", ""), time.Minute)
	got := Render("rca-gap-hint", map[string]string{"iteration": "3"})
	if got != "这是第3轮补充取证，请重点分析新增证据。" {
		t.Fatalf("失败应回退默认模板, got %q", got)
	}
}

func TestRenderStaleThenBackgroundRefresh(t *testing.T) {
	resetRegistry(t)
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"data":{"template":{"messages":[{"role":"SYSTEM","content":"V1"}]}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"template":{"messages":[{"role":"SYSTEM","content":"V2"}]}}}`))
	}))
	defer srv.Close()
	Configure(NewPhoenixFetcher(srv.URL, "production", ""), 30*time.Millisecond)
	if got := Render("rca-gap-hint", nil); got != "V1" {
		t.Fatalf("首次应取 V1, got %q", got)
	}
	time.Sleep(80 * time.Millisecond) // TTL 过期
	if got := Render("rca-gap-hint", nil); got != "V1" {
		t.Fatalf("过期后应先返回旧值 V1, got %q", got)
	}
	time.Sleep(80 * time.Millisecond) // 等后台刷新完成
	if got := Render("rca-gap-hint", nil); got != "V2" {
		t.Fatalf("后台刷新后应为 V2, got %q", got)
	}
}

func TestRenderSingleflightWithinTTL(t *testing.T) {
	resetRegistry(t)
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		_, _ = w.Write([]byte(`{"data":{"template":{"messages":[{"role":"SYSTEM","content":"V"}]}}}`))
	}))
	defer srv.Close()
	Configure(NewPhoenixFetcher(srv.URL, "production", ""), time.Minute)
	for i := 0; i < 10; i++ {
		if got := Render("rca-gap-hint", nil); got != "V" {
			t.Fatalf("got %q", got)
		}
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("TTL 内 10 次调用应只请求 1 次, got %d", got)
	}
}
