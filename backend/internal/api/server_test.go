package api

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"aiops/internal/agent"
	"aiops/internal/config"
	"aiops/internal/hub"
	"aiops/internal/intent"
	"aiops/internal/kb"
	"aiops/internal/model"
	"aiops/internal/store"
)

func TestHealthzAndAlerts(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	_ = s.SaveAlert(model.Alert{ID: "alarm-1", Device: "d", Metric: "m",
		Severity: "warning", Message: "x", Status: "active"})
	h := hub.New()
	ag := agent.New(s, nil, nil)
	srv := NewServer(config.Config{}, s, ag, nil, h, nil, intent.NewService(s, intent.NewMemCache()), kb.NewService(s, nil))

	req := httptest.NewRequest("GET", "/healthz", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("healthz code = %d", rr.Code)
	}

	req = httptest.NewRequest("GET", "/api/alerts", nil)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("alerts code = %d", rr.Code)
	}
	var list []model.Alert
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list) != 1 || list[0].ID != "alarm-1" {
		t.Fatalf("list = %+v", list)
	}
}

func TestChatEndpoint(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	h := hub.New()
	ag := agent.New(s, nil, nil)
	srv := NewServer(config.Config{}, s, ag, nil, h, nil, intent.NewService(s, intent.NewMemCache()), kb.NewService(s, nil))

	body := strings.NewReader(`{"text":"有哪些告警"}`)
	req := httptest.NewRequest("POST", "/api/chat", body)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("chat code = %d body=%s", rr.Code, rr.Body.String())
	}
	var m model.Message
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Role != model.RoleAssistant {
		t.Fatalf("role = %q", m.Role)
	}
}

func newKBServer(t *testing.T) *httptest.Server {
	t.Helper()
	st, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	t.Cleanup(func() { st.Close() })
	ag := agent.New(st, nil, nil)
	svc := intent.NewService(st, intent.NewMemCache())
	kbsvc := kb.NewService(st, nil)
	srv := NewServer(config.Config{}, st, ag, nil, hub.New(), nil, svc, kbsvc)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts
}

func TestKBListEmptyAndUpload(t *testing.T) {
	ts := newKBServer(t)
	res, err := http.Get(ts.URL + "/api/kb/docs")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	var list []json.RawMessage
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("空库应空列表, got %d", len(list))
	}

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, _ := mw.CreateFormFile("file", "手册.md")
	_, _ = fw.Write([]byte("# 光模块\n检查光模块告警，涉及 switch 设备。"))
	_ = mw.WriteField("category", "manual")
	_ = mw.WriteField("vendor", "H3C")
	_ = mw.WriteField("model", "S5560")
	_ = mw.WriteField("intents", "AskRootCause")
	_ = mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/kb/upload", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	ures, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer ures.Body.Close()
	if ures.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d body=%s", ures.StatusCode, readBody(ures))
	}
	var doc model.KbDocument
	if err := json.NewDecoder(ures.Body).Decode(&doc); err != nil {
		t.Fatalf("decode doc: %v", err)
	}
	if doc.Category != model.KBManual || len(doc.DeviceTypes) == 0 || len(doc.Intents) == 0 {
		t.Fatalf("doc = %+v", doc)
	}
	res2, _ := http.Get(ts.URL + "/api/kb/docs?vendor=H3C")
	defer res2.Body.Close()
	var list2 []model.KbDocSummary
	if err := json.NewDecoder(res2.Body).Decode(&list2); err != nil {
		t.Fatalf("decode list2: %v", err)
	}
	if len(list2) != 1 || list2[0].ChunkCount == 0 {
		t.Fatalf("filtered list = %+v", list2)
	}
	res3, _ := http.Get(ts.URL + "/api/kb/docs/" + doc.ID)
	defer res3.Body.Close()
	var detail kb.DocDetail
	if err := json.NewDecoder(res3.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if len(detail.Chunks) == 0 {
		t.Fatalf("detail chunks empty")
	}
	dreq, _ := http.NewRequest("DELETE", ts.URL+"/api/kb/docs/"+doc.ID, nil)
	dres, _ := http.DefaultClient.Do(dreq)
	defer dres.Body.Close()
	if dres.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d", dres.StatusCode)
	}
}

func TestKBUploadBadFile(t *testing.T) {
	ts := newKBServer(t)
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, _ := mw.CreateFormFile("file", "a.pdf")
	_, _ = fw.Write([]byte("x"))
	_ = mw.WriteField("category", "manual")
	_ = mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/kb/upload", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, _ := http.DefaultClient.Do(req)
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("非法类型应 400, got %d", res.StatusCode)
	}
}

func readBody(res *http.Response) string {
	b, _ := io.ReadAll(res.Body)
	return string(b)
}
