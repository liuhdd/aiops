package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"aiops/internal/agent"
	"aiops/internal/config"
	"aiops/internal/hub"
	"aiops/internal/intent"
	"aiops/internal/store"
)

func newIntentServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	t.Cleanup(func() { st.Close() })
	ag := agent.New(st, nil, nil)
	svc := intent.NewService(st, intent.NewMemCache())
	srv := NewServer(config.Config{}, st, ag, nil, hub.New(), nil, svc)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts, st
}

func TestIntentTreeEndpoint(t *testing.T) {
	ts, _ := newIntentServer(t)
	res, err := http.Get(ts.URL + "/api/intents/tree")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	var tree []json.RawMessage
	if err := json.NewDecoder(res.Body).Decode(&tree); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tree) != 1 {
		t.Fatalf("tree len = %d, want 1", len(tree))
	}
}

func TestIntentStatusIntegrityError(t *testing.T) {
	ts, _ := newIntentServer(t)
	body := strings.NewReader(`{"ids":["grp-builtin"],"enabled":false}`)
	req, _ := http.NewRequest("POST", ts.URL+"/api/intents/status", body)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("完整性失败应 400, got %d", res.StatusCode)
	}
	var e map[string]string
	_ = json.NewDecoder(res.Body).Decode(&e)
	if !strings.Contains(e["error"], "未包含") {
		t.Fatalf("错误信息应提示漏了谁, got %q", e["error"])
	}
}
