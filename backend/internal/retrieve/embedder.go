package retrieve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// DefaultEmbedBatch 单次 embedding 请求默认最大条数；部分推理服务（如火山方舟
// doubao-embedding-vision）限制每请求最多 10 条输入。
const DefaultEmbedBatch = 10

// DefaultEmbedRetry 单批请求失败（429/5xx/网络错误）默认重试次数。
const DefaultEmbedRetry = 5

// maxEmbedBackoff 单次重试退避上限。
const maxEmbedBackoff = 15 * time.Second

// LLMEmbedder 通过 OpenAI 兼容 embeddings REST 接口生成向量。
type LLMEmbedder struct {
	baseURL string
	apiKey  string
	model   string
	batch   int
	retries int
	sleep   func(context.Context, time.Duration)
	client  *http.Client
}

// NewLLMEmbedder 构造 LLMEmbedder；embedding key 取 LLM_EMBED_API_KEY，
// 未配置时回退 LLM_API_KEY；两者皆空返回 ErrNoEmbed。
func NewLLMEmbedder(cfg config.Config) (*LLMEmbedder, error) {
	key := cfg.LLMEmbedAPIKey
	if key == "" {
		key = cfg.LLMAPIKey
	}
	if key == "" {
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
	batch := cfg.LLMEmbedBatch
	if batch <= 0 {
		batch = DefaultEmbedBatch
	}
	retries := cfg.LLMEmbedRetry
	if cfg.LLMEmbedRetry < 0 {
		retries = DefaultEmbedRetry
	}
	return &LLMEmbedder{
		baseURL: strings.TrimRight(base, "/"),
		apiKey:  key,
		model:   model,
		batch:   batch,
		retries: retries,
		sleep:   sleepCtx,
		client:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// sleepCtx 可中断的退避等待。
func sleepCtx(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func (e *LLMEmbedder) Model() string { return e.model }

// Embed 批量生成向量；返回的向量已归一化为单位向量。按 batch 分批请求，
// 跨批结果按输入顺序拼接。
func (e *LLMEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	batch := e.batch
	if batch <= 0 {
		batch = DefaultEmbedBatch
	}
	out := make([][]float64, 0, len(texts))
	for start := 0; start < len(texts); start += batch {
		end := start + batch
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := e.embedBatch(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// embedBatch 生成单批向量；429/5xx/网络错误按指数退避重试。
func (e *LLMEmbedder) embedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	body, err := json.Marshal(map[string]any{"model": e.model, "input": texts})
	if err != nil {
		return nil, err
	}
	retries := e.retries
	if retries < 0 {
		retries = DefaultEmbedRetry
	}
	for attempt := 0; ; attempt++ {
		resp, err := e.post(ctx, body)
		if err != nil {
			if attempt >= retries {
				return nil, err
			}
			e.backoff(ctx, attempt)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			vecs, err := parseEmbeddings(resp.Body)
			resp.Body.Close()
			return vecs, err
		}
		status := resp.StatusCode
		resp.Body.Close()
		if !retryableStatus(status) || attempt >= retries {
			return nil, fmt.Errorf("embeddings status %d", status)
		}
		e.backoff(ctx, attempt)
	}
}

// post 发送一次 embeddings 请求。
func (e *LLMEmbedder) post(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	return e.client.Do(req)
}

// retryableStatus 限流与服务端错误可重试。
func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// backoff 指数退避：1s、2s、4s……上限 maxEmbedBackoff。
func (e *LLMEmbedder) backoff(ctx context.Context, attempt int) {
	d := time.Second << uint(attempt)
	if d > maxEmbedBackoff || d <= 0 {
		d = maxEmbedBackoff
	}
	if e.sleep != nil {
		e.sleep(ctx, d)
		return
	}
	sleepCtx(ctx, d)
}

// parseEmbeddings 解析响应并归一化。
func parseEmbeddings(r io.Reader) ([][]float64, error) {
	var parsed struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(r).Decode(&parsed); err != nil {
		return nil, err
	}
	if len(parsed.Data) == 0 {
		return nil, errors.New("embeddings 响应为空")
	}
	// 每批以首条维度为基准校验，避免在结构体上缓存可变状态引发并发竞态。
	dim := len(parsed.Data[0].Embedding)
	out := make([][]float64, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		if len(d.Embedding) != dim {
			return nil, fmt.Errorf("embeddings 维度不一致: %d != %d", len(d.Embedding), dim)
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
