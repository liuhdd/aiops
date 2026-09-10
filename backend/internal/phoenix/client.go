// Package phoenix 是 Arize Phoenix 的轻量 REST 客户端（Phoenix 无官方 Go SDK），
// 覆盖 Datasets 与 Experiments 端点，供 cmd/datasetctl 灌库与实验回写使用。
// 本包禁止 import 任何 aiops/internal/* 包（配置经参数注入）。
package phoenix

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	uploadTimeout = 10 * time.Second
	listTimeout   = 2 * time.Second
	maxBody       = 4 << 20
)

// Client Phoenix REST 客户端。
type Client struct {
	BaseURL string // 如 http://localhost:6006
	APIKey  string // 非空时带 Authorization: Bearer
	HTTP    *http.Client
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{BaseURL: baseURL, APIKey: apiKey, HTTP: &http.Client{Timeout: uploadTimeout}}
}

// HTTPError 非 2xx 响应。
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("phoenix: status %d: %s", e.StatusCode, e.Body)
}

// IsConflict 判断错误是否为 HTTP 409。
func IsConflict(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && he.StatusCode == http.StatusConflict
}

// Example 一条数据集样本。
type Example struct {
	ID       string // 上传时为稳定公开 id（example_ids）；ListExamples 返回时为 dataset_example_id
	Input    map[string]any
	Output   map[string]any
	Metadata map[string]any
	Splits   []string
}

type UploadResult struct {
	DatasetID  string
	VersionID  string
	NumCreated int
	NumUpdated int
}

type Dataset struct{ ID, Name string }

// Uploader 上传数据集所需的最小接口（便于 fake 注入）。
type Uploader interface {
	UploadDataset(action, name, description string, exs []Example) (UploadResult, error)
}

type uploadRequest struct {
	Action      string           `json:"action"`
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Inputs      []map[string]any `json:"inputs"`
	Outputs     []map[string]any `json:"outputs"`
	Metadata    []map[string]any `json:"metadata"`
	Splits      [][]string       `json:"splits,omitempty"`
	ExampleIDs  []string         `json:"example_ids,omitempty"`
}

// UploadDataset action=create|append|update；exs 的字段按 example 平行成数组。
func (c *Client) UploadDataset(action, name, description string, exs []Example) (UploadResult, error) {
	req := uploadRequest{Action: action, Name: name, Description: description}
	for _, e := range exs {
		req.Inputs = append(req.Inputs, nonNil(e.Input))
		req.Outputs = append(req.Outputs, nonNil(e.Output))
		req.Metadata = append(req.Metadata, nonNil(e.Metadata))
		req.Splits = append(req.Splits, e.Splits)
		req.ExampleIDs = append(req.ExampleIDs, e.ID)
	}
	var out struct {
		Data struct {
			DatasetID  string `json:"dataset_id"`
			VersionID  string `json:"version_id"`
			NumCreated int    `json:"num_created_examples"`
			NumUpdated int    `json:"num_updated_examples"`
		} `json:"data"`
	}
	if err := c.do(http.MethodPost, "/v1/datasets/upload?sync=true", req, &out, uploadTimeout); err != nil {
		return UploadResult{}, err
	}
	return UploadResult{
		DatasetID: out.Data.DatasetID, VersionID: out.Data.VersionID,
		NumCreated: out.Data.NumCreated, NumUpdated: out.Data.NumUpdated,
	}, nil
}

func (c *Client) ListDatasets() ([]Dataset, error) {
	var out struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := c.do(http.MethodGet, "/v1/datasets", nil, &out, listTimeout); err != nil {
		return nil, err
	}
	ds := make([]Dataset, 0, len(out.Data))
	for _, d := range out.Data {
		ds = append(ds, Dataset{ID: d.ID, Name: d.Name})
	}
	return ds, nil
}

func (c *Client) ListExamples(datasetID, versionID string) ([]Example, error) {
	path := "/v1/datasets/" + url.PathEscape(datasetID) + "/examples"
	if versionID != "" {
		path += "?version_id=" + url.QueryEscape(versionID)
	}
	var out struct {
		Data struct {
			Examples []struct {
				ID       string         `json:"id"`
				Input    map[string]any `json:"input"`
				Output   map[string]any `json:"output"`
				Metadata map[string]any `json:"metadata"`
			} `json:"examples"`
		} `json:"data"`
	}
	if err := c.do(http.MethodGet, path, nil, &out, listTimeout); err != nil {
		return nil, err
	}
	exs := make([]Example, 0, len(out.Data.Examples))
	for _, e := range out.Data.Examples {
		exs = append(exs, Example{ID: e.ID, Input: e.Input, Output: e.Output, Metadata: e.Metadata})
	}
	return exs, nil
}

// do 发请求并把 JSON 响应解到 out；非 2xx 返回 *HTTPError。
func (c *Client) do(method, path string, body, out any, timeout time.Duration) error {
	var rdr io.Reader
	if body != nil {
		bs, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(bs)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	bs, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{StatusCode: resp.StatusCode, Body: string(bs)}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(bs, out)
}

func nonNil(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
