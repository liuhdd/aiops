package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	openai "github.com/cloudwego/eino-ext/components/model/openai"

	"aiops/internal/config"
)

var ErrNoAPIKey = errors.New("LLM_API_KEY is not set")

type Client struct {
	model model.ChatModel
}

func NewClient(cfg config.Config) (*Client, error) {
	if cfg.LLMAPIKey == "" {
		return nil, ErrNoAPIKey
	}
	cm, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL: cfg.LLMBaseURL,
		APIKey:  cfg.LLMAPIKey,
		Model:   cfg.LLMModel,
	})
	if err != nil {
		return nil, fmt.Errorf("new chat model: %w", err)
	}
	return &Client{model: cm}, nil
}

// NewLightClient 构造轻量意图模型客户端；未配置 LLM_API_KEY 时返回 ErrNoAPIKey。
func NewLightClient(cfg config.Config) (*Client, error) {
	if cfg.LLMAPIKey == "" {
		return nil, ErrNoAPIKey
	}
	cm, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL: cfg.LLMLightBaseURL,
		APIKey:  cfg.LLMAPIKey,
		Model:   cfg.LLMLightModel,
	})
	if err != nil {
		return nil, fmt.Errorf("new light chat model: %w", err)
	}
	return &Client{model: cm}, nil
}

func (c *Client) Complete(ctx context.Context, system, user string) (string, error) {
	msg, err := c.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(system),
		schema.UserMessage(user),
	})
	if err != nil {
		return "", err
	}
	return msg.Content, nil
}

func ParseJSON(raw string, v any) error {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	return json.Unmarshal([]byte(s), v)
}
