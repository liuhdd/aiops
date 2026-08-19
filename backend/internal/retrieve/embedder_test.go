package retrieve

import (
	"context"
	"errors"
	"math"
	"testing"

	"aiops/internal/config"
)

func TestNewLLMEmbedderNoKey(t *testing.T) {
	e, err := NewLLMEmbedder(config.Config{})
	if !errors.Is(err, ErrNoEmbed) || e != nil {
		t.Fatalf("无 key 应 ErrNoEmbed, got %v %v", e, err)
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
