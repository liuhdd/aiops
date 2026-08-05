package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"aiops/internal/agent"
	"aiops/internal/config"
	"aiops/internal/hub"
	"aiops/internal/model"
	"aiops/internal/pipeline"
	"aiops/internal/simulator"
	"aiops/internal/store"
)

type Server struct {
	cfg config.Config
	st  *store.Store
	ag  *agent.Agent
	eng *pipeline.Engine
	hub *hub.Hub
	sim *simulator.Simulator
	mux *http.ServeMux
}

func NewServer(cfg config.Config, s *store.Store, ag *agent.Agent, eng *pipeline.Engine, h *hub.Hub, sim *simulator.Simulator) http.Handler {
	srv := &Server{cfg: cfg, st: s, ag: ag, eng: eng, hub: h, sim: sim, mux: http.NewServeMux()}
	srv.routes()
	return withCORS(srv.mux)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	s.mux.HandleFunc("GET /api/alerts", s.handleListAlerts)
	s.mux.HandleFunc("GET /api/alerts/{id}", s.handleGetAlert)
	s.mux.HandleFunc("POST /api/alerts/{id}/inspect", s.handleInspect)
	s.mux.HandleFunc("GET /api/messages", s.handleListMessages)
	s.mux.HandleFunc("POST /api/chat", s.handleChat)
	s.mux.HandleFunc("GET /ws", s.handleWS)
}

func (s *Server) handleListAlerts(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListAlerts()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetAlert(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	al, err := s.st.GetAlert(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "alert not found"})
		return
	}
	run, _ := s.st.GetRun("run-" + id)
	var stages []model.StageResult
	if run.ID != "" {
		stages, _ = s.st.ListStages(run.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"alert": al, "run": run, "stages": stages})
}

func (s *Server) handleInspect(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	al, err := s.st.GetAlert(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "alert not found"})
		return
	}
	go func() {
		_ = s.eng.Run(context.Background(), al, nil)
	}()
	writeJSON(w, http.StatusOK, map[string]string{"status": "started", "alert_id": id})
}

func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	msgs, err := s.st.ListMessages()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, msgs)
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}
	msg, err := s.ag.Handle(r.Context(), body.Text)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.hub.Publish("message", msg)
	writeJSON(w, http.StatusOK, msg)
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	s.hub.HandleWS(w, r)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if strings.Contains(r.URL.Path, "/ws") {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
