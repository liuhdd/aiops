package retrieve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"aiops/internal/config"
)

func TestRerankerSuccess(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"index": 1, "relevance_score": 0.9}, {"index": 0, "relevance_score": 0.2}}})
	}))
	defer srv.Close()
	r, _ := NewReranker(config.Config{RerankBaseURL: srv.URL, RerankAPIKey: "k", RerankModel: "m"})
	scores, err := r.Rerank(context.Background(), "q", []string{"d0", "d1"})
	if err != nil || len(scores) != 2 || scores[1] != 0.9 {
		t.Fatalf("scores=%v err=%v hits=%d", scores, err, hits)
	}
}
func TestRerankerUnavailable(t *testing.T) {
	if _, err := NewReranker(config.Config{}); !errors.Is(err, ErrNoRerank) {
		t.Fatalf("want ErrNoRerank got %v", err)
	}
}
func TestRerankerHTTPFailureFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	r, _ := NewReranker(config.Config{RerankBaseURL: srv.URL, RerankAPIKey: "k", RerankModel: "m"})
	if _, err := r.Rerank(context.Background(), "q", []string{"d0"}); err == nil {
		t.Fatal("want err")
	}
}
