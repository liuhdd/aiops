package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"aiops/internal/agent"
	"aiops/internal/config"
	"aiops/internal/hub"
	"aiops/internal/intent"
	"aiops/internal/kb"
	"aiops/internal/model"
	"aiops/internal/pipeline"
	"aiops/internal/simulator"
	"aiops/internal/store"
)

type Server struct {
	cfg   config.Config
	st    *store.Store
	ag    *agent.Agent
	eng   *pipeline.Engine
	hub   *hub.Hub
	sim   *simulator.Simulator
	svc   *intent.Service
	kbsvc *kb.Service
	mux   *http.ServeMux
}

func NewServer(cfg config.Config, s *store.Store, ag *agent.Agent, eng *pipeline.Engine, h *hub.Hub, sim *simulator.Simulator, svc *intent.Service, kbsvc *kb.Service) http.Handler {
	srv := &Server{cfg: cfg, st: s, ag: ag, eng: eng, hub: h, sim: sim, svc: svc, kbsvc: kbsvc, mux: http.NewServeMux()}
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
	s.mux.HandleFunc("GET /api/intents/tree", s.handleIntentTree)
	s.mux.HandleFunc("GET /api/intents/leaves", s.handleIntentLeaves)
	s.mux.HandleFunc("POST /api/intents", s.handleIntentCreate)
	s.mux.HandleFunc("PATCH /api/intents/{id}", s.handleIntentUpdate)
	s.mux.HandleFunc("DELETE /api/intents", s.handleIntentDelete)
	s.mux.HandleFunc("POST /api/intents/status", s.handleIntentStatus)
	s.mux.HandleFunc("GET /api/kb/docs", s.handleKbList)
	s.mux.HandleFunc("GET /api/kb/docs/{id}", s.handleKbGet)
	s.mux.HandleFunc("POST /api/kb/upload", s.handleKbUpload)
	s.mux.HandleFunc("PATCH /api/kb/docs/{id}", s.handleKbUpdate)
	s.mux.HandleFunc("DELETE /api/kb/docs/{id}", s.handleKbDelete)
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
	msgs, err := s.st.ListMessages("")
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

func operator(r *http.Request) string {
	op := r.Header.Get("X-Operator")
	if op == "" {
		op = "admin"
	}
	return op
}

func intentStatus(err error) int {
	switch {
	case errors.Is(err, intent.ErrDuplicateCode):
		return http.StatusConflict
	case errors.Is(err, intent.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, intent.ErrSubtreeIncomplete):
		return http.StatusBadRequest
	default:
		return http.StatusBadRequest
	}
}

func (s *Server) handleIntentTree(w http.ResponseWriter, r *http.Request) {
	tree, err := s.svc.Tree(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, tree)
}

func (s *Server) handleIntentLeaves(w http.ResponseWriter, r *http.Request) {
	leaves, err := s.svc.ActiveLeaves(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, leaves)
}

func (s *Server) handleIntentCreate(w http.ResponseWriter, r *http.Request) {
	var req intent.CreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}
	n, err := s.svc.Create(r.Context(), operator(r), req)
	if err != nil {
		writeJSON(w, intentStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleIntentUpdate(w http.ResponseWriter, r *http.Request) {
	var req intent.UpdateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}
	n, err := s.svc.Update(r.Context(), operator(r), r.PathValue("id"), req)
	if err != nil {
		writeJSON(w, intentStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleIntentDelete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}
	if err := s.svc.Delete(r.Context(), operator(r), body.IDs); err != nil {
		writeJSON(w, intentStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleIntentStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs     []string `json:"ids"`
		Enabled bool     `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}
	if err := s.svc.SetEnabled(r.Context(), operator(r), body.IDs, body.Enabled); err != nil {
		writeJSON(w, intentStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleKbList(w http.ResponseWriter, r *http.Request) {
	f := store.KbFilter{
		Category:   r.URL.Query().Get("category"),
		DeviceType: r.URL.Query().Get("device_type"),
		Vendor:     r.URL.Query().Get("vendor"),
		Model:      r.URL.Query().Get("model"),
		Intent:     r.URL.Query().Get("intent"),
		Q:          r.URL.Query().Get("q"),
	}
	list, err := s.kbsvc.List(r.Context(), f)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleKbGet(w http.ResponseWriter, r *http.Request) {
	detail, err := s.kbsvc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, kbStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleKbUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无法解析上传表单"})
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 file 字段"})
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	req := kb.UploadReq{
		Title:       r.FormValue("title"),
		Category:    model.KBCategory(r.FormValue("category")),
		SourceFile:  hdr.Filename,
		Data:        data,
		DeviceTypes: splitCSV(r.FormValue("device_types")),
		Vendor:      splitCSV(r.FormValue("vendor")),
		Model:       splitCSV(r.FormValue("model")),
		Metrics:     splitCSV(r.FormValue("metrics")),
		Keywords:    splitCSV(r.FormValue("keywords")),
		Intents:     splitCSV(r.FormValue("intents")),
	}
	doc, err := s.kbsvc.Upload(r.Context(), req)
	if err != nil {
		writeJSON(w, kbStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (s *Server) handleKbUpdate(w http.ResponseWriter, r *http.Request) {
	var req kb.UpdateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}
	doc, err := s.kbsvc.Update(r.Context(), r.PathValue("id"), req)
	if err != nil {
		writeJSON(w, kbStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (s *Server) handleKbDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.kbsvc.Delete(r.Context(), r.PathValue("id")); err != nil {
		writeJSON(w, kbStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func splitCSV(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func kbStatus(err error) int {
	switch {
	case errors.Is(err, kb.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, kb.ErrBadFile), errors.Is(err, kb.ErrEmptyContent), errors.Is(err, kb.ErrBadCategory):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
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
