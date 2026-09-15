package retrieve

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aiops/internal/config"
)

func TestNewLLMEmbedderNoKey(t *testing.T) {
	e, err := NewLLMEmbedder(config.Config{})
	if !errors.Is(err, ErrNoEmbed) || e != nil {
		t.Fatalf("无 key 应 ErrNoEmbed, got %v %v", e, err)
	}
}

func TestNewLLMEmbedderAPIKeyPreference(t *testing.T) {
	e, err := NewLLMEmbedder(config.Config{LLMAPIKey: "main", LLMEmbedAPIKey: "embed"})
	if err != nil || e == nil || e.apiKey != "embed" {
		t.Fatalf("应优先 LLM_EMBED_API_KEY: %v %v", e, err)
	}
	e, err = NewLLMEmbedder(config.Config{LLMAPIKey: "main"})
	if err != nil || e == nil || e.apiKey != "main" {
		t.Fatalf("应回退 LLM_API_KEY: %v %v", e, err)
	}
}

func TestLLMEmbedderBatches(t *testing.T) {
	var mu sync.Mutex
	var calls, maxN int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		calls++
		if len(req.Input) > maxN {
			maxN = len(req.Input)
		}
		mu.Unlock()
		data := make([]map[string]any, len(req.Input))
		for i := range req.Input {
			data[i] = map[string]any{"embedding": []float64{float64(i), 1}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()

	e, err := NewLLMEmbedder(config.Config{LLMEmbedAPIKey: "k", LLMEmbedBaseURL: srv.URL, LLMEmbedBatch: 10})
	if err != nil {
		t.Fatal(err)
	}
	vecs, err := e.Embed(context.Background(), make([]string, 25))
	if err != nil || len(vecs) != 25 {
		t.Fatalf("vecs=%d err=%v", len(vecs), err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 3 || maxN != 10 {
		t.Fatalf("calls=%d maxN=%d, want 3/10", calls, maxN)
	}
}

func TestLLMEmbedderRetriesOn429(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"embedding": []float64{1, 0}}}})
	}))
	defer srv.Close()

	e, err := NewLLMEmbedder(config.Config{LLMEmbedAPIKey: "k", LLMEmbedBaseURL: srv.URL, LLMEmbedRetry: 3})
	if err != nil {
		t.Fatal(err)
	}
	e.sleep = func(context.Context, time.Duration) {}
	vecs, err := e.Embed(context.Background(), []string{"a"})
	if err != nil || len(vecs) != 1 || calls.Load() != 3 {
		t.Fatalf("vecs=%d calls=%d err=%v", len(vecs), calls.Load(), err)
	}
}

func TestLLMEmbedderNoRetryOn400(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	e, err := NewLLMEmbedder(config.Config{LLMEmbedAPIKey: "k", LLMEmbedBaseURL: srv.URL, LLMEmbedRetry: 3})
	if err != nil {
		t.Fatal(err)
	}
	e.sleep = func(context.Context, time.Duration) {}
	if _, err := e.Embed(context.Background(), []string{"a"}); err == nil {
		t.Fatal("400 应直接失败")
	}
	if calls.Load() != 1 {
		t.Fatalf("400 不应重试: calls=%d", calls.Load())
	}
}

func TestNormalize(t *testing.T) {
	v := Normalize([]float64{3, 4})
	if math.Abs(math.Sqrt(v[0]*v[0]+v[1]*v[1])-1) > 1e-9 {
		t.Fatalf("非单位向量: %v", v)
	}
	z := Normalize([]float64{0, 0})
	if z[0] != 0 || z[1] != 0 {
		t.Fatalf("零向量应原样: %v", z)
	}
}

type fakeCompleter struct{}

func (fakeCompleter) Model() string { return "test" }
func (fakeCompleter) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i := range texts {
		out[i] = []float64{float64(i + 1), 1}
	}
	return out, nil
}

func TestEmbedderContract(t *testing.T) {
	var e Embedder = fakeCompleter{}
	if e.Model() != "test" {
		t.Fatalf("model = %q", e.Model())
	}
	vecs, err := e.Embed(context.Background(), []string{"a", "b"})
	if err != nil || len(vecs) != 2 || len(vecs[0]) != 2 {
		t.Fatalf("embed = %v %v", vecs, err)
	}
}
