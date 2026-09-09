package prompt

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchByTagSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/prompts/rca-system/tags/production" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"data":{"id":"v1","template":{"messages":[{"role":"SYSTEM","content":"你是算力中心硬件运维专家"}]}}}`)
	}))
	defer srv.Close()
	f := NewPhoenixFetcher(srv.URL, "production", "")
	got, ok := f.Fetch("rca-system")
	if !ok || got != "你是算力中心硬件运维专家" {
		t.Fatalf("Fetch = (%q, %v), 期望第一条消息 content", got, ok)
	}
}

func TestFetchSendsBearerWhenAPIKeySet(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"data":{"template":{"messages":[{"role":"USER","content":"x"}]}}}`)
	}))
	defer srv.Close()
	f := NewPhoenixFetcher(srv.URL, "production", "sk-test")
	if _, ok := f.Fetch("a"); !ok {
		t.Fatal("Fetch 应成功")
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
}

func TestFetchFailsOnBadStatusOrBody(t *testing.T) {
	cases := []http.HandlerFunc{
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
		func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `not-json`) },
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"data":{"template":{"messages":[]}}}`)
		},
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"data":{"template":{"messages":[{"role":"SYSTEM","content":""}]}}}`)
		},
	}
	for i, h := range cases {
		srv := httptest.NewServer(h)
		f := NewPhoenixFetcher(srv.URL, "production", "")
		if _, ok := f.Fetch("a"); ok {
			t.Fatalf("case %d: 坏响应应返回 ok=false", i)
		}
		srv.Close()
	}
}

func TestFetchFailsOnTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()
	f := NewPhoenixFetcher(srv.URL, "production", "")
	f.HTTP = &http.Client{Timeout: 10 * time.Millisecond}
	if _, ok := f.Fetch("a"); ok {
		t.Fatal("超时应返回 ok=false")
	}
}

func TestPushCreatesPromptWithTemplate(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/prompts" {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"data":{"id":"p-1"}}`)
	}))
	defer srv.Close()
	f := NewPhoenixFetcher(srv.URL, "production", "")
	if err := f.Push("rca-system", "system", "你好 {{x}}", "deepseek-chat"); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if body["name"] != "rca-system" {
		t.Fatalf("name = %v", body["name"])
	}
	ver := body["version"].(map[string]any)
	if ver["model_name"] != "deepseek-chat" || ver["template_format"] != "FSTRING" {
		t.Fatalf("version = %v", ver)
	}
	msgs := ver["template"].(map[string]any)["messages"].([]any)
	m0 := msgs[0].(map[string]any)
	if m0["role"] != "SYSTEM" || m0["content"] != "你好 {{x}}" {
		t.Fatalf("messages[0] = %v", m0)
	}
}

func TestListReturnsSummaries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"name":"rca-system","id":"p-1"},{"name":"kb-summarize","id":"p-2"}]}`)
	}))
	defer srv.Close()
	f := NewPhoenixFetcher(srv.URL, "production", "")
	ps, err := f.List()
	if err != nil || len(ps) != 2 || ps[1].Name != "kb-summarize" {
		t.Fatalf("List = (%v, %v)", ps, err)
	}
}
