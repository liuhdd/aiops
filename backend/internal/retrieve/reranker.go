package retrieve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"aiops/internal/config"
)

// ErrNoRerank 未配置 rerank 服务时返回。
var ErrNoRerank = errors.New("rerank 不可用")

// Reranker 精排接口：对候选文档按查询相关度打分，返回与 docs 等长分数。
type Reranker interface {
	Rerank(ctx context.Context, query string, docs []string) ([]float64, error)
}

// NewReranker 构造 rerank 客户端；未配置 RerankBaseURL 时返回 ErrNoRerank。
func NewReranker(cfg config.Config) (Reranker, error) {
	if strings.TrimSpace(cfg.RerankBaseURL) == "" {
		return nil, ErrNoRerank
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.RerankBaseURL), "/")
	model := cfg.RerankModel
	if model == "" {
		model = "rerank"
	}
	return &rerankClient{
		baseURL: base,
		apiKey:  cfg.RerankAPIKey,
		model:   model,
		client:  &http.Client{Timeout: 3 * time.Second},
	}, nil
}

type rerankClient struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// Rerank 调用 POST {base}/rerank，对 docs 逐一打分；返回与输入等长分数，未回传项为 0；非 2xx / JSON 异常返回 err。
func (c *rerankClient) Rerank(ctx context.Context, query string, docs []string) ([]float64, error) {
	if len(docs) == 0 {
		return []float64{}, nil
	}
	body, err := json.Marshal(map[string]any{
		"model":     c.model,
		"query":     query,
		"documents": docs,
		"top_n":     len(docs),
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/rerank", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("rerank status %d", resp.StatusCode)
	}
	var parsed struct {
		Results []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	scores := make([]float64, len(docs))
	for _, r := range parsed.Results {
		if r.Index >= 0 && r.Index < len(scores) {
			scores[r.Index] = r.RelevanceScore
		}
	}
	return scores, nil
}
