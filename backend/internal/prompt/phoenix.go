package prompt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// PhoenixFetcher Phoenix Prompt Hub 轻量 REST 客户端（Phoenix 无官方 Go SDK，对接 /v1 REST API）。
// 运行时只用 Fetch（GET）；Push/List 供 cmd/promptctl 离线灌库与核对使用。
type PhoenixFetcher struct {
	BaseURL string // 如 http://localhost:6006
	Tag     string // 拉取使用的 tag，默认 production
	APIKey  string // 非空时带 Authorization: Bearer
	HTTP    *http.Client
}

const PhoenixTimeout = 2 * time.Second

func NewPhoenixFetcher(baseURL, tag, apiKey string) *PhoenixFetcher {
	if tag == "" {
		tag = "production"
	}
	return &PhoenixFetcher{BaseURL: baseURL, Tag: tag, APIKey: apiKey, HTTP: &http.Client{Timeout: PhoenixTimeout}}
}

// getPromptResponseBody 对应 Phoenix REST API GetPromptResponseBody（只取用到的字段）。
type getPromptResponseBody struct {
	Data struct {
		Template struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		} `json:"template"`
	} `json:"data"`
}

// Fetch 按 name+tag 拉取 prompt 版本，返回第一条消息 content；任何失败返回 (, false)。
// 端点：GET /v1/prompts/{prompt_identifier}/tags/{tag_name}（getPromptVersionByTagName）。
func (f *PhoenixFetcher) Fetch(name string) (string, bool) {
	endpoint := fmt.Sprintf("%s/v1/prompts/%s/tags/%s", f.BaseURL, url.PathEscape(name), url.PathEscape(f.Tag))
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("Accept", "application/json")
	f.setAuth(req)
	resp, err := f.HTTP.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", false
	}
	var parsed getPromptResponseBody
	if json.Unmarshal(body, &parsed) != nil || len(parsed.Data.Template.Messages) == 0 {
		return "", false
	}
	if c := parsed.Data.Template.Messages[0].Content; c != "" {
		return c, true
	}
	return "", false
}

// pushRequestBody 对应 Phoenix REST API 新建 prompt 请求体。
type pushRequestBody struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Version     pushVersionBody `json:"version"`
}

type pushVersionBody struct {
	ModelProvider  string       `json:"model_provider"`
	ModelName      string       `json:"model_name"`
	TemplateType   string       `json:"template_type"`
	TemplateFormat string       `json:"template_format"`
	Template       pushTemplate `json:"template"`
}

type pushTemplate struct {
	Messages []pushMessage `json:"messages"`
}

type pushMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Push 新建 prompt（含首个版本）；已存在的 name 由 Phoenix 侧新建版本。
func (f *PhoenixFetcher) Push(name, role, content, modelName string) error {
	payload := pushRequestBody{
		Name: name,
		Version: pushVersionBody{
			ModelProvider:  "OPENAI",
			ModelName:      modelName,
			TemplateType:   "STRING",
			TemplateFormat: "FSTRING",
			Template:       pushTemplate{Messages: []pushMessage{{Role: toUpperRole(role), Content: content}}},
		},
	}
	bs, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, f.BaseURL+"/v1/prompts", bytes.NewReader(bs))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	f.setAuth(req)
	resp, err := f.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("push %s: status %d", name, resp.StatusCode)
	}
	return nil
}

// PromptSummary prompt 列表项（list 子命令核对用）。
type PromptSummary struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// List 列出远端全部 prompt。
func (f *PhoenixFetcher) List() ([]PromptSummary, error) {
	req, err := http.NewRequest(http.MethodGet, f.BaseURL+"/v1/prompts", nil)
	if err != nil {
		return nil, err
	}
	f.setAuth(req)
	resp, err := f.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list: status %d", resp.StatusCode)
	}
	var out struct {
		Data []PromptSummary `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (f *PhoenixFetcher) setAuth(req *http.Request) {
	if f.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+f.APIKey)
	}
}

// toUpperRole 把 "system"/"user" 归一为 Phoenix 的 "SYSTEM"/"USER"。
func toUpperRole(role string) string {
	if role == "user" {
		return "USER"
	}
	return "SYSTEM"
}
