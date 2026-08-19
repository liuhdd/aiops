package retrieve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"aiops/internal/config"
)

var ErrNoEmbed = errors.New("embedding 不可用")

// Embedder 向量生成接口；Model 返回生成模型名（用于落库记录）。
type Embedder interface {
	Model() string
	Embed(ctx context.Context, texts []string) ([][]float64, error)
}

// LLMEmbedder 通过 OpenAI 兼容 embeddings REST 接口生成向量。
type LLMEmbedder struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
	dim     int
}

// NewLLMEmbedder 构造 LLMEmbedder；未配置 LLM_API_KEY 时返回 ErrNoEmbed。
func NewLLMEmbedder(cfg config.Config) (*LLMEmbedder, error) {
	if cfg.LLMAPIKey == "" {
		return nil, ErrNoEmbed
	}
	base := cfg.LLMEmbedBaseURL
	if base == "" {
		base = cfg.LLMBaseURL
	}
	model := cfg.LLMEmbedModel
	if model == "" {
		model = "text-embedding-3-small"
	}
	return &LLMEmbedder{
		baseURL: strings.TrimRight(base, "/"),
		apiKey:  cfg.LLMAPIKey,
		model:   model,
		client:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (e *LLMEmbedder) Model() string { return e.model }

// Embed 批量生成向量；返回的向量已归一化为单位向量。
func (e *LLMEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(map[string]any{"model": e.model, "input": texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embeddings status %d", resp.StatusCode)
	}
	var parsed struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if len(parsed.Data) == 0 {
		return nil, errors.New("embeddings 响应为空")
	}
	if e.dim == 0 {
		e.dim = len(parsed.Data[0].Embedding)
	}
	out := make([][]float64, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		if len(d.Embedding) != e.dim {
			return nil, fmt.Errorf("embeddings 维度不一致: %d != %d", len(d.Embedding), e.dim)
		}
		out = append(out, Normalize(d.Embedding))
	}
	return out, nil
}

// Normalize 将向量归一化为单位向量（原地修改）；零向量返回原样。
func Normalize(v []float64) []float64 {
	norm := 0.0
	for _, x := range v {
		norm += x * x
	}
	norm = math.Sqrt(norm)
	if norm < 1e-12 {
		return v
	}
	for i := range v {
		v[i] /= norm
	}
	return v
}
