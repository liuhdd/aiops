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
	srv := NewServer(config.Config{}, s, ag, nil, h, nil)

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
	srv := NewServer(config.Config{}, s, ag, nil, h, nil)

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
