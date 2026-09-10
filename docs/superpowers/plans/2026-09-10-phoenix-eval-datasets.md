# Phoenix 评测数据集与实验回写（管线部分）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 agent 评测任务集与 RAG golden set 上传为 Phoenix Datasets，并把本地 `cmd/eval` / `cmd/rageval` 报告作为 Phoenix Experiment + runs + evaluations 回写。

**Architecture:** 新增 `internal/phoenix`（纯 REST 客户端，零 internal 依赖）承担 datasets/experiments 六个端点；新增 `internal/goldenset` 承载 RAG 标注集解析与报告类型；`internal/eval` 增加数据集构建与实验回写；新增 `cmd/datasetctl`（push/list/experiment）；`cmd/rageval` 增加 `-o` 结构化报告；`cmd/eval` 给 trial 打时间戳。

**Tech Stack:** Go 1.24、标准库 `net/http`/`encoding/json`、`time`；零新增第三方依赖。

**Spec:** `docs/superpowers/specs/2026-09-10-phoenix-eval-datasets-design.md`（评测题扩容见配套计划 `2026-09-10-eval-content-expansion.md`）

## Global Constraints

- 所有命令在 `backend/` 目录执行；测试命令 `go test ./...`、静态检查 `go vet ./...`（仓库无 golangci-lint）。
- `internal/phoenix` 禁止 import 任何 `aiops/internal/*` 包；配置经参数注入。
- 零新增第三方依赖（只用标准库）。
- 代码注释、commit message 用中文，commit 用 conventional 前缀（`feat:`/`fix:`/`refactor:`/`test:`/`docs:`/`chore:`）。
- 复用 `PHOENIX_BASE_URL` / `PHOENIX_API_KEY`，不新增 env。
- 运行时 `cmd/server` 不引入本计划任何代码；`PHOENIX_BASE_URL` 为空时服务/测试行为零变化。
- 未指定时，`cmd/datasetctl` 的 golden 路径默认 `testdata/golden_set.jsonl`（相对 backend cwd）。
- Phoenix REST 事实（已核实）：`POST /v1/datasets/upload?sync=true`、`GET /v1/datasets`、`GET /v1/datasets/{id}/examples`、`POST /v1/datasets/{id}/experiments`、`POST /v1/experiments/{id}/runs`、`POST /v1/experiment_evaluations`。
- 本计划不新增评测题（题集扩容见配套计划）。

---

### Task 0: 合并 phoenix 分支并创建隔离 worktree

**Files:** 无代码改动（git 操作）。

- [ ] **Step 1: 确认 phoenix 分支全绿**

Run（`backend/`）: `git -C .. log --oneline -1 feat/phoenix-prompt-management && go test ./...`
Expected: 分支最新提交存在；`go test ./...` 全绿。若失败，停止并报告，不继续。

- [ ] **Step 2: 合并到 main**

```bash
git checkout main
git merge --no-ff feat/phoenix-prompt-management -m "merge: Phoenix Prompt Hub 提示词管理"
go test ./...            # 在 backend/ 下
go vet ./...
```
Expected: 合并成功且全绿。

- [ ] **Step 3: 基于 main 建 worktree 与分支**

```bash
git worktree add .worktrees/phoenix-eval-datasets -b feat/phoenix-eval-datasets main
```
Expected: worktree 创建成功。后续全部改动在 `.worktrees/phoenix-eval-datasets/` 内进行。

- [ ] **Step 4: Commit**

无（合并提交已产生）。

---

### Task 1: `internal/phoenix` 客户端 — 数据集上传与查询

**Files:**
- Create: `backend/internal/phoenix/client.go`
- Create: `backend/internal/phoenix/client_test.go`

**Interfaces:**
- Consumes: 无（仅标准库）。
- Produces（后续任务依赖的精确签名）:
  - `type Client struct { BaseURL, APIKey string; HTTP *http.Client }`
  - `func NewClient(baseURL, apiKey string) *Client`
  - `type Example struct { ID string; Input, Output, Metadata map[string]any; Splits []string }`
  - `type UploadResult struct { DatasetID, VersionID string; NumCreated, NumUpdated int }`
  - `type Dataset struct { ID, Name string }`
  - `type Uploader interface { UploadDataset(action, name, description string, exs []Example) (UploadResult, error) }`
  - `func (c *Client) UploadDataset(action, name, description string, exs []Example) (UploadResult, error)`
  - `func (c *Client) ListDatasets() ([]Dataset, error)`
  - `func (c *Client) ListExamples(datasetID, versionID string) ([]Example, error)`（versionID 空=最新）
  - `type HTTPError struct { StatusCode int; Body string }`；`func (e *HTTPError) Error() string`
  - `func IsConflict(err error) bool`

- [ ] **Step 1: 写失败测试**

创建 `backend/internal/phoenix/client_test.go`：

```go
package phoenix

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUploadDatasetPostsParallelArrays(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/datasets/upload" || r.URL.Query().Get("sync") != "true" {
			t.Errorf("unexpected: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = io.WriteString(w, `{"data":{"dataset_id":"DS1","version_id":"V1","num_created_examples":2,"num_updated_examples":0}}`)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	res, err := c.UploadDataset("create", "ds", "desc", []Example{
		{ID: "a", Input: map[string]any{"q": 1}, Output: map[string]any{"a": 2}, Metadata: map[string]any{"m": 1}, Splits: []string{"s1"}},
		{ID: "b", Input: map[string]any{"q": 2}, Output: map[string]any{"a": 3}, Metadata: map[string]any{"m": 2}, Splits: []string{"s2"}},
	})
	if err != nil {
		t.Fatalf("UploadDataset: %v", err)
	}
	if res.DatasetID != "DS1" || res.VersionID != "V1" || res.NumCreated != 2 {
		t.Fatalf("res = %+v", res)
	}
	if got["action"] != "create" || got["name"] != "ds" {
		t.Fatalf("body = %v", got)
	}
	if ids := got["example_ids"].([]any); ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("example_ids = %v", ids)
	}
	if ins := got["inputs"].([]any); ins[0].(map[string]any)["q"].(float64) != 1 {
		t.Fatalf("inputs = %v", ins)
	}
	if sp := got["splits"].([]any); sp[0].([]any)[0] != "s1" {
		t.Fatalf("splits = %v", sp)
	}
}

func TestUploadDataset409IsConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, "exists")
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	_, err := c.UploadDataset("create", "ds", "", []Example{{ID: "a", Input: map[string]any{}, Output: map[string]any{}}})
	if !IsConflict(err) {
		t.Fatalf("应可判别 409, got %v", err)
	}
	if !strings.Contains(err.Error(), "409") {
		t.Fatalf("err 应含状态码, got %v", err)
	}
}

func TestListDatasetsAndExamples(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/datasets":
			_, _ = io.WriteString(w, `{"data":[{"id":"DS1","name":"ds"}]}`)
		case "/v1/datasets/DS1/examples":
			_, _ = io.WriteString(w, `{"data":{"dataset_id":"DS1","version_id":"V1","examples":[{"id":"EX1","node_id":"N1","input":{"q":1},"output":{"a":2},"metadata":{"task_id":"t1"}}]}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	ds, err := c.ListDatasets()
	if err != nil || len(ds) != 1 || ds[0].Name != "ds" || ds[0].ID != "DS1" {
		t.Fatalf("ListDatasets = (%v,%v)", ds, err)
	}
	exs, err := c.ListExamples("DS1", "")
	if err != nil || len(exs) != 1 || exs[0].ID != "EX1" || exs[0].Metadata["task_id"] != "t1" {
		t.Fatalf("ListExamples = (%v,%v)", exs, err)
	}
}

func TestSendsBearerWhenAPIKeySet(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "sk-x")
	if _, err := c.ListDatasets(); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer sk-x" {
		t.Fatalf("auth = %q", auth)
	}
}
```

- [ ] **Step 2: 确认测试失败**

Run: `go test ./internal/phoenix/ -v`
Expected: FAIL，`undefined: NewClient` 等。

- [ ] **Step 3: 实现 client.go**

创建 `backend/internal/phoenix/client.go`：

```go
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
	ID       string         // 上传时为稳定公开 id（example_ids）；ListExamples 返回时为 dataset_example_id
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
```

- [ ] **Step 4: 确认测试通过**

Run: `go test ./internal/phoenix/ -v && go vet ./internal/phoenix/`
Expected: 全部 PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/phoenix/client.go internal/phoenix/client_test.go
git commit -m "feat: Phoenix REST 客户端（数据集上传/查询）"
```

---

### Task 2: `internal/phoenix` — 实验端点

**Files:**
- Create: `backend/internal/phoenix/experiment.go`
- Create: `backend/internal/phoenix/experiment_test.go`

**Interfaces:**
- Consumes: Task 1 的 `Client.do`。
- Produces:
  - `type Experiment struct { ID, DatasetID, DatasetVersionID, Name string }`
  - `type RunRequest struct { DatasetExampleID string; Output any; RepetitionNumber int; StartTime, EndTime time.Time; TraceID, Error string }`
  - `type EvaluationResult struct { Label string; Score *float64; Explanation string }`
  - `type EvaluationRequest struct { ExperimentRunID, Name, AnnotatorKind string; StartTime, EndTime time.Time; Result *EvaluationResult; Error string; Metadata map[string]any }`
  - `type Writer interface { ListExamples(...); CreateExperiment(...); LogRun(...); LogEvaluation(...) }`（Task 8/11 依赖）
  - `func (c *Client) CreateExperiment(datasetID, name string, repetitions int, metadata map[string]any) (Experiment, error)`
  - `func (c *Client) LogRun(experimentID string, r RunRequest) (string, error)`
  - `func (c *Client) LogEvaluation(e EvaluationRequest) (string, error)`

- [ ] **Step 1: 写失败测试**

创建 `backend/internal/phoenix/experiment_test.go`：

```go
package phoenix

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCreateExperiment(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/datasets/DS1/experiments" {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"data":{"id":"E1","dataset_id":"DS1","dataset_version_id":"V1","name":"n"}}`)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	exp, err := c.CreateExperiment("DS1", "n", 3, map[string]any{"config": "rule"})
	if err != nil || exp.ID != "E1" || exp.DatasetVersionID != "V1" {
		t.Fatalf("CreateExperiment = (%+v,%v)", exp, err)
	}
	if body["name"] != "n" || body["repetitions"].(float64) != 3 {
		t.Fatalf("body = %v", body)
	}
}

func TestLogRun(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/experiments/E1/runs" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"data":{"id":"R1"}}`)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	start := time.Date(2026, 9, 10, 1, 0, 0, 0, time.UTC)
	id, err := c.LogRun("E1", RunRequest{
		DatasetExampleID: "EX1", Output: map[string]any{"x": 1},
		RepetitionNumber: 2, StartTime: start, EndTime: start.Add(time.Second), Error: "boom",
	})
	if err != nil || id != "R1" {
		t.Fatalf("LogRun = (%q,%v)", id, err)
	}
	if body["dataset_example_id"] != "EX1" || body["repetition_number"].(float64) != 2 || body["error"] != "boom" {
		t.Fatalf("body = %v", body)
	}
}

func TestLogEvaluation(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/experiment_evaluations" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"data":{"id":"EV1"}}`)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	score := 0.5
	id, err := c.LogEvaluation(EvaluationRequest{
		ExperimentRunID: "R1", Name: "RootCauseMatch", AnnotatorKind: "CODE",
		StartTime: time.Now(), EndTime: time.Now(),
		Result: &EvaluationResult{Score: &score, Label: "FAIL", Explanation: "x"},
	})
	if err != nil || id != "EV1" {
		t.Fatalf("LogEvaluation = (%q,%v)", id, err)
	}
	if body["annotator_kind"] != "CODE" || body["result"].(map[string]any)["score"].(float64) != 0.5 {
		t.Fatalf("body = %v", body)
	}
}
```

- [ ] **Step 2: 确认测试失败**

Run: `go test ./internal/phoenix/ -run 'TestCreateExperiment|TestLogRun|TestLogEvaluation' -v`
Expected: FAIL，`undefined: CreateExperiment` 等。

- [ ] **Step 3: 实现 experiment.go**

创建 `backend/internal/phoenix/experiment.go`：

```go
package phoenix

import (
	"net/http"
	"net/url"
	"time"
)

type Experiment struct {
	ID, DatasetID, DatasetVersionID, Name string
}

// RunRequest 一条实验 run（对应一个 dataset example 的一次执行）。
type RunRequest struct {
	DatasetExampleID string    `json:"dataset_example_id"`
	Output           any       `json:"output"`
	RepetitionNumber int       `json:"repetition_number"`
	StartTime        time.Time `json:"start_time"`
	EndTime          time.Time `json:"end_time"`
	TraceID          string    `json:"trace_id,omitempty"`
	Error            string    `json:"error,omitempty"`
}

type EvaluationResult struct {
	Label       string   `json:"label,omitempty"`
	Score       *float64 `json:"score,omitempty"`
	Explanation string   `json:"explanation,omitempty"`
}

type EvaluationRequest struct {
	ExperimentRunID string              `json:"experiment_run_id"`
	Name            string              `json:"name"`
	AnnotatorKind   string              `json:"annotator_kind"` // LLM | CODE | HUMAN
	StartTime       time.Time           `json:"start_time"`
	EndTime         time.Time           `json:"end_time"`
	Result          *EvaluationResult   `json:"result,omitempty"`
	Error           string              `json:"error,omitempty"`
	Metadata        map[string]any      `json:"metadata,omitempty"`
	TraceID         string              `json:"trace_id,omitempty"`
}

// Writer 实验回写所需的最小客户端接口（*Client 实现），便于 fake 注入测试。
type Writer interface {
	ListExamples(datasetID, versionID string) ([]Example, error)
	CreateExperiment(datasetID, name string, repetitions int, metadata map[string]any) (Experiment, error)
	LogRun(experimentID string, r RunRequest) (string, error)
	LogEvaluation(e EvaluationRequest) (string, error)
}

func (c *Client) CreateExperiment(datasetID, name string, repetitions int, metadata map[string]any) (Experiment, error) {
	body := map[string]any{"name": name, "repetitions": repetitions}
	if metadata != nil {
		body["metadata"] = metadata
	}
	var out struct {
		Data struct {
			ID               string `json:"id"`
			DatasetID        string `json:"dataset_id"`
			DatasetVersionID string `json:"dataset_version_id"`
			Name             string `json:"name"`
		} `json:"data"`
	}
	path := "/v1/datasets/" + url.PathEscape(datasetID) + "/experiments"
	if err := c.do(http.MethodPost, path, body, &out, uploadTimeout); err != nil {
		return Experiment{}, err
	}
	return Experiment{
		ID: out.Data.ID, DatasetID: out.Data.DatasetID,
		DatasetVersionID: out.Data.DatasetVersionID, Name: out.Data.Name,
	}, nil
}

func (c *Client) LogRun(experimentID string, r RunRequest) (string, error) {
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	path := "/v1/experiments/" + url.PathEscape(experimentID) + "/runs"
	if err := c.do(http.MethodPost, path, r, &out, uploadTimeout); err != nil {
		return "", err
	}
	if out.Data.ID == "" {
		return "", &HTTPError{StatusCode: http.StatusOK, Body: "log run 响应缺 id"}
	}
	return out.Data.ID, nil
}

func (c *Client) LogEvaluation(e EvaluationRequest) (string, error) {
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.do(http.MethodPost, "/v1/experiment_evaluations", e, &out, uploadTimeout); err != nil {
		return "", err
	}
	if out.Data.ID == "" {
		return "", &HTTPError{StatusCode: http.StatusOK, Body: "log evaluation 响应缺 id"}
	}
	return out.Data.ID, nil
}
```

- [ ] **Step 4: 确认测试通过**

Run: `go test ./internal/phoenix/ -v && go vet ./internal/phoenix/`
Expected: 全部 PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/phoenix/experiment.go internal/phoenix/experiment_test.go
git commit -m "feat: Phoenix REST 客户端（实验与评分端点）"
```

---

### Task 3: `internal/goldenset` 抽取

**Files:**
- Create: `backend/internal/goldenset/goldenset.go`
- Create: `backend/internal/goldenset/goldenset_test.go`

**Interfaces:**
- Consumes: Task 1 的 `phoenix.Example`。
- Produces:
  - `type Item struct { ID, Type, Question string; Expect Expect; TopK int }`
  - `type Expect struct { DocIDs, DistractorDocIDs []string; Behavior string }`
  - `func Load(path string) ([]Item, error)`
  - `const RagDatasetName = "aiops-rag-golden"`
  - `func ToPhoenixExamples(items []Item) []phoenix.Example`

- [ ] **Step 1: 写失败测试**

创建 `backend/internal/goldenset/goldenset_test.go`：

```go
package goldenset

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFixture(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "g.jsonl")
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadNormalizesDefaults(t *testing.T) {
	p := writeFixture(t,
		`{"id":"q1","type":"fact","question":"Q","expect":{"doc_ids":["d1"]}}`,
		`{"id":"q2","type":"no_answer","question":"Q2","expect":{},"top_k":5}`,
	)
	items, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("len = %d", len(items))
	}
	if items[0].TopK != 3 || items[0].Expect.Behavior != "hit" {
		t.Fatalf("q1 = %+v", items[0])
	}
	if items[0].Expect.DistractorDocIDs == nil {
		t.Fatal("distractor 应归一为空切片而非 nil")
	}
	if items[1].TopK != 5 || items[1].Expect.Behavior != "reject" || len(items[1].Expect.DocIDs) != 0 {
		t.Fatalf("q2 = %+v", items[1])
	}
}

func TestToPhoenixExamples(t *testing.T) {
	items := []Item{{ID: "q1", Type: "fact", Question: "Q", TopK: 3, Expect: Expect{DocIDs: []string{"d1"}, Behavior: "hit", DistractorDocIDs: []string{}}}}
	exs := ToPhoenixExamples(items)
	if len(exs) != 1 || exs[0].ID != "q1" {
		t.Fatalf("exs = %+v", exs)
	}
	if exs[0].Input["question"] != "Q" || exs[0].Input["top_k"] != 3 {
		t.Fatalf("input = %v", exs[0].Input)
	}
	if exs[0].Output["behavior"] != "hit" {
		t.Fatalf("output = %v", exs[0].Output)
	}
	if exs[0].Metadata["type"] != "fact" || exs[0].Splits[0] != "fact" {
		t.Fatalf("meta/splits = %v/%v", exs[0].Metadata, exs[0].Splits)
	}
}
```

- [ ] **Step 2: 确认测试失败**

Run: `go test ./internal/goldenset/ -v`
Expected: FAIL，`undefined: Load`。

- [ ] **Step 3: 实现 goldenset.go（从 `cmd/rageval/main.go` 迁出 GoldenItem）**

创建 `backend/internal/goldenset/goldenset.go`：

```go
// Package goldenset 承载检索质量标注集（golden set）的解析、归一与 Phoenix 数据集映射。
package goldenset

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"

	"aiops/internal/phoenix"
)

type Expect struct {
	DocIDs           []string `json:"doc_ids"`
	DistractorDocIDs []string `json:"distractor_doc_ids"`
	Behavior         string   `json:"behavior"`
}

type Item struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Question string `json:"question"`
	Expect   Expect `json:"expect"`
	TopK     int    `json:"top_k"`
}

// Load 读取 JSONL 标注集，逐行归一（TopK 缺省 3、behavior 缺省按 doc_ids 推断、切片非 nil）。
func Load(path string) ([]Item, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var items []Item
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var it Item
		if err := json.Unmarshal([]byte(line), &it); err != nil {
			return nil, err
		}
		normalize(&it)
		items = append(items, it)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func normalize(it *Item) {
	if it.TopK == 0 {
		it.TopK = 3
	}
	if it.Expect.DocIDs == nil {
		it.Expect.DocIDs = []string{}
	}
	if it.Expect.DistractorDocIDs == nil {
		it.Expect.DistractorDocIDs = []string{}
	}
	if it.Expect.Behavior == "" {
		if len(it.Expect.DocIDs) > 0 {
			it.Expect.Behavior = "hit"
		} else {
			it.Expect.Behavior = "reject"
		}
	}
}

// RagDatasetName Phoenix 上检索数据集的名称。
const RagDatasetName = "aiops-rag-golden"

// ToPhoenixExamples 把标注集映射为 Phoenix 数据集样本（output 存作者原始 id）。
func ToPhoenixExamples(items []Item) []phoenix.Example {
	out := make([]phoenix.Example, 0, len(items))
	for _, it := range items {
		out = append(out, phoenix.Example{
			ID:     it.ID,
			Input:  map[string]any{"question": it.Question, "top_k": it.TopK},
			Output: map[string]any{"doc_ids": it.Expect.DocIDs, "distractor_doc_ids": it.Expect.DistractorDocIDs, "behavior": it.Expect.Behavior},
			Metadata: map[string]any{"id": it.ID, "type": it.Type},
			Splits:   []string{it.Type},
		})
	}
	return out
}
```

- [ ] **Step 4: 确认测试通过**

Run: `go test ./internal/goldenset/ -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/goldenset/goldenset.go internal/goldenset/goldenset_test.go
git commit -m "feat: 抽取 internal/goldenset 标注集解析与 Phoenix 映射"
```

---

### Task 4: `cmd/rageval` 改用 `internal/goldenset`

**Files:**
- Modify: `backend/cmd/rageval/main.go`（删除本地 `GoldenItem` 定义与解析，改 import `internal/goldenset`）
- Modify: `backend/cmd/rageval/metrics_test.go`（如引用了 `GoldenItem` 则同步；先确认）

**Interfaces:**
- Consumes: Task 3 的 `goldenset.Item`、`goldenset.Expect`、`goldenset.Load`。
- Produces: `cmd/rageval` 行为不变（后续 Task 10 在其上加 `-o`）。

- [ ] **Step 1: 确认现有引用**

Run: `grep -rn "GoldenItem" backend/cmd/rageval`
Expected: 记录全部引用点（预期仅 `main.go`；若 `metrics_test.go` 引用，一并替换为 `goldenset.Item`）。

- [ ] **Step 2: 改 main.go**

删除 `cmd/rageval/main.go` 中 `type GoldenItem struct {...}` 定义（22–32 行），把 import 块加入 `"aiops/internal/goldenset"`，并把：
- `var items []GoldenItem` → `var items []goldenset.Item`
- 逐行解析块（`json.Unmarshal` + 归一逻辑）替换为：
```go
	var items []goldenset.Item
	items, err = goldenset.Load(goldenPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "标注集解析失败: %v\n", err)
		os.Exit(1)
	}
```
- `it.Expect.Behavior`/`it.Expect.DocIDs` 等字段访问不变（结构体字段同名）。
- 删除不再使用的 import（如 `bufio`、`encoding/json` 若他处不再使用；以 `go build` 报错为准逐个处理）。

- [ ] **Step 3: 确认通过**

Run: `go build ./... && go test ./cmd/rageval/ -v`
Expected: 全部 PASS（行为与迁移前一致）。

- [ ] **Step 4: Commit**

```bash
git add cmd/rageval/main.go cmd/rageval/metrics_test.go
git commit -m "refactor: rageval 复用 internal/goldenset"
```

---

### Task 5: `eval.AgentDatasetExamples` 数据集构建

**Files:**
- Create: `backend/internal/eval/phoenix.go`
- Create: `backend/internal/eval/phoenix_test.go`

**Interfaces:**
- Consumes: Task 1 的 `phoenix.Example`；本包 `DefaultTasks()`、`Task`、`Expected`。
- Produces:
  - `const AgentDatasetName = "aiops-agents-eval"`
  - `func AgentDatasetExamples() []phoenix.Example`

- [ ] **Step 1: 写失败测试**

创建 `backend/internal/eval/phoenix_test.go`：

```go
package eval

import "testing"

func TestAgentDatasetExamplesCoverAllTasks(t *testing.T) {
	tasks := DefaultTasks()
	exs := AgentDatasetExamples()
	if len(exs) != len(tasks) {
		t.Fatalf("examples=%d tasks=%d", len(exs), len(tasks))
	}
	byID := map[string]bool{}
	for _, e := range exs {
		byID[e.ID] = true
		if e.Metadata["task_id"] != e.ID {
			t.Fatalf("metadata.task_id 应与 example id 一致: %+v", e)
		}
		if len(e.Splits) != 1 {
			t.Fatalf("splits 应为单元素: %+v", e)
		}
	}
	for _, task := range tasks {
		if !byID[task.ID] {
			t.Fatalf("缺任务 %s", task.ID)
		}
	}
}

func TestAgentDatasetExampleShapes(t *testing.T) {
	byID := map[string]int{}
	exs := AgentDatasetExamples()
	for i, e := range exs {
		byID[e.ID] = i
	}
	pipe := exs[byID["disk-full-db-01"]]
	alert, ok := pipe.Input["alert"].(map[string]any)
	if !ok || alert["device"] != "db-01" || alert["metric"] != "disk_usage" {
		t.Fatalf("pipeline input = %v", pipe.Input)
	}
	if pipe.Output["runbook"] != "disk_usage" {
		t.Fatalf("pipeline output = %v", pipe.Output)
	}

	chat := exs[byID["chat-list-alerts"]]
	if chat.Input["chat_input"] != "有哪些告警" || chat.Input["object"] != ObjectChat {
		t.Fatalf("chat input = %v", chat.Input)
	}
	if chat.Output["intent"] != "ListAlerts" {
		t.Fatalf("chat output = %v", chat.Output)
	}
}
```

- [ ] **Step 2: 确认测试失败**

Run: `go test ./internal/eval/ -run TestAgentDataset -v`
Expected: FAIL，`undefined: AgentDatasetExamples`。

- [ ] **Step 3: 实现**

创建 `backend/internal/eval/phoenix.go`：

```go
package eval

import "aiops/internal/phoenix"

// AgentDatasetName Phoenix 上 agent 评测数据集名称。
const AgentDatasetName = "aiops-agents-eval"

// AgentDatasetExamples 把 DefaultTasks() 映射为 Phoenix 数据集样本。
func AgentDatasetExamples() []phoenix.Example {
	tasks := DefaultTasks()
	out := make([]phoenix.Example, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, taskExample(t))
	}
	return out
}

func taskExample(t Task) phoenix.Example {
	input := map[string]any{"object": t.Object}
	if t.Object == ObjectChat {
		input["chat_input"] = t.ChatInput
	} else {
		input["alert"] = map[string]any{
			"device": t.Alert.Device, "metric": t.Alert.Metric,
			"severity": t.Alert.Severity, "message": t.Alert.Message,
		}
	}
	output := map[string]any{}
	if len(t.Expected.RootCause) > 0 {
		output["root_cause_keywords"] = t.Expected.RootCause
	}
	if len(t.Expected.Logs) > 0 {
		output["log_keywords"] = t.Expected.Logs
	}
	if t.Expected.NoGaps {
		output["no_gaps"] = true
	}
	if t.Expected.Runbook != "" {
		output["runbook"] = t.Expected.Runbook
	}
	if t.Expected.Intent != "" {
		output["intent"] = t.Expected.Intent
	}
	if len(t.Expected.Intents) > 0 {
		output["intents"] = t.Expected.Intents
	}
	if len(t.Expected.ToolUse) > 0 {
		output["tool_use"] = t.Expected.ToolUse
	}
	return phoenix.Example{
		ID: t.ID, Input: input, Output: output,
		Metadata: map[string]any{"task_id": t.ID, "object": t.Object, "configs": t.Configs},
		Splits:   []string{t.Object},
	}
}
```

- [ ] **Step 4: 确认测试通过**

Run: `go test ./internal/eval/ -run TestAgentDataset -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/eval/phoenix.go internal/eval/phoenix_test.go
git commit -m "feat: eval 数据集样本构建（AgentDatasetExamples）"
```

---

### Task 6: `cmd/datasetctl` push / list

**Files:**
- Create: `backend/cmd/datasetctl/main.go`
- Create: `backend/cmd/datasetctl/main_test.go`

**Interfaces:**
- Consumes: Task 1 `phoenix.Client`/`Uploader`/`IsConflict`、Task 3 `goldenset.Load`/`ToPhoenixExamples`/`RagDatasetName`、Task 5 `eval.AgentDatasetExamples`/`AgentDatasetName`、`config.Load()` 的 `PhoenixBaseURL`/`PhoenixAPIKey`。
- Produces:
  - CLI `datasetctl push [--dataset agents|rag|all] [--golden path]`
  - CLI `datasetctl list`
  - `func pushDataset(u phoenix.Uploader, name string, exs []phoenix.Example) (phoenix.UploadResult, error)`（后续 Task 9 复用其幂等语义）

- [ ] **Step 1: 写失败测试**

创建 `backend/cmd/datasetctl/main_test.go`：

```go
package main

import (
	"errors"
	"testing"

	"aiops/internal/phoenix"
)

type fakeUploader struct {
	calls []string
	fail  error
}

func (f *fakeUploader) UploadDataset(action, name, description string, exs []phoenix.Example) (phoenix.UploadResult, error) {
	f.calls = append(f.calls, action)
	if action == "create" && f.fail != nil {
		return phoenix.UploadResult{}, f.fail
	}
	return phoenix.UploadResult{DatasetID: "DS", VersionID: "V", NumCreated: len(exs)}, nil
}

func TestPushDatasetRetriesWithUpdateOnConflict(t *testing.T) {
	u := &fakeUploader{fail: &phoenix.HTTPError{StatusCode: 409, Body: "exists"}}
	_, err := pushDataset(u, "ds", []phoenix.Example{{ID: "a"}})
	if err != nil {
		t.Fatalf("应回退 update 成功, got %v", err)
	}
	if len(u.calls) != 2 || u.calls[0] != "create" || u.calls[1] != "update" {
		t.Fatalf("calls = %v", u.calls)
	}
}

func TestPushDatasetPropagatesNonConflict(t *testing.T) {
	u := &fakeUploader{fail: errors.New("boom")}
	if _, err := pushDataset(u, "ds", []phoenix.Example{{ID: "a"}}); err == nil {
		t.Fatal("非 409 错误应上抛")
	}
	if len(u.calls) != 1 {
		t.Fatalf("不应重试, calls = %v", u.calls)
	}
}
```

- [ ] **Step 2: 确认测试失败**

Run: `go test ./cmd/datasetctl/ -v`
Expected: FAIL，`undefined: pushDataset`。

- [ ] **Step 3: 实现 main.go**

创建 `backend/cmd/datasetctl/main.go`：

```go
// datasetctl 把本地评测资产灌入 Phoenix 并回写实验结果。
// 用法：
//
//	datasetctl push [--dataset agents|rag|all] [--golden testdata/golden_set.jsonl]
//	datasetctl list
//	datasetctl experiment --dataset agents|rag --report <json> [--name <prefix>] [--config a,b]
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"aiops/internal/config"
	"aiops/internal/eval"
	"aiops/internal/goldenset"
	"aiops/internal/phoenix"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cfg := config.Load()
	if cfg.PhoenixBaseURL == "" {
		log.Fatal("PHOENIX_BASE_URL 未配置")
	}
	c := phoenix.NewClient(cfg.PhoenixBaseURL, cfg.PhoenixAPIKey)
	switch os.Args[1] {
	case "push":
		cmdPush(c, os.Args[2:])
	case "list":
		cmdList(c)
	case "experiment":
		cmdExperiment(c, os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `用法:
  datasetctl push [--dataset agents|rag|all] [--golden testdata/golden_set.jsonl]
  datasetctl list
  datasetctl experiment --dataset agents|rag --report <json> [--name <prefix>] [--config a,b]`)
}

func cmdPush(c *phoenix.Client, args []string) {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	which := fs.String("dataset", "all", "agents|rag|all")
	goldenPath := fs.String("golden", "testdata/golden_set.jsonl", "RAG 标注集路径")
	fs.Parse(args)
	if *which == "agents" || *which == "all" {
		res, err := pushDataset(c, eval.AgentDatasetName, eval.AgentDatasetExamples())
		if err != nil {
			log.Fatalf("push %s 失败: %v", eval.AgentDatasetName, err)
		}
		fmt.Printf("pushed %s: dataset_id=%s created=%d updated=%d\n",
			eval.AgentDatasetName, res.DatasetID, res.NumCreated, res.NumUpdated)
	}
	if *which == "rag" || *which == "all" {
		items, err := goldenset.Load(*goldenPath)
		if err != nil {
			log.Fatalf("读取标注集失败: %v", err)
		}
		res, err := pushDataset(c, goldenset.RagDatasetName, goldenset.ToPhoenixExamples(items))
		if err != nil {
			log.Fatalf("push %s 失败: %v", goldenset.RagDatasetName, err)
		}
		fmt.Printf("pushed %s: dataset_id=%s created=%d updated=%d\n",
			goldenset.RagDatasetName, res.DatasetID, res.NumCreated, res.NumUpdated)
	}
}

func cmdList(c *phoenix.Client) {
	ds, err := c.ListDatasets()
	if err != nil {
		log.Fatal(err)
	}
	for _, d := range ds {
		fmt.Printf("%s\t%s\n", d.Name, d.ID)
	}
}

// pushDataset 幂等上传：先 create，遇 409 退 update（依赖 example_ids 原地 upsert）。
func pushDataset(u phoenix.Uploader, name string, exs []phoenix.Example) (phoenix.UploadResult, error) {
	res, err := u.UploadDataset("create", name, "", exs)
	if phoenix.IsConflict(err) {
		return u.UploadDataset("update", name, "", exs)
	}
	return res, err
}
```

（`cmdExperiment` 在 Task 9 填充；本任务先加一个占位分支会破坏编译，故此刻 `main.go` 先不引用 `cmdExperiment`——把 `case "experiment":` 暂时留到 Task 9 添加。即本任务的 `main()` switch 只含 `push`/`list`/default。）

- [ ] **Step 4: 确认测试通过**

Run: `go test ./cmd/datasetctl/ -v && go build ./...`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add cmd/datasetctl/main.go cmd/datasetctl/main_test.go
git commit -m "feat: datasetctl push/list（幂等上传评测数据集）"
```

---

### Task 7: `TrialResult` 时间戳 + `cmd/eval` 接线

**Files:**
- Modify: `backend/internal/eval/runner.go`（`TrialResult` 加字段）
- Modify: `backend/cmd/eval/main.go`（`cmdRun`/`cmdCI` 打点）
- Test: `backend/internal/eval/phoenix_test.go`（追加序列化断言）

**Interfaces:**
- Consumes: 无。
- Produces: `TrialResult.StartedAt`、`TrialResult.EndedAt time.Time`（JSON `started_at`/`ended_at`）。

- [ ] **Step 1: 写失败测试**

追加到 `backend/internal/eval/phoenix_test.go`：

```go
import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTrialResultSerializesTimestamps(t *testing.T) {
	start := time.Date(2026, 9, 10, 1, 2, 3, 0, time.UTC)
	tr := TrialResult{Trial: 1, Config: "rule", StartedAt: start, EndedAt: start.Add(time.Second)}
	b, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"started_at"`) || !strings.Contains(s, `"ended_at"`) {
		t.Fatalf("缺少时间戳字段: %s", s)
	}
}
```

（若文件顶部已有 `import "testing"`，合并 import 块。）

- [ ] **Step 2: 确认测试失败**

Run: `go test ./internal/eval/ -run TestTrialResultSerializesTimestamps -v`
Expected: FAIL，`unknown field StartedAt`。

- [ ] **Step 3: 实现字段**

`backend/internal/eval/runner.go` 中 `TrialResult` 结构体追加（放在 `ErrMsg` 之后）：

```go
	StartedAt time.Time `json:"started_at,omitempty"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
```

并在文件 import 块确保有 `"time"`。

`backend/cmd/eval/main.go`：在 `cmdRun` 与 `cmdCI` 的每个 trial 循环内，把
```go
res, err := eval.RunTask(trialCtx, t, c, lc, trial)
```
改为在 `RunTask` 前记录 `start := time.Now()`，在 `Grade` 完成后写回：
```go
res, err := eval.RunTask(trialCtx, t, c, lc, trial)
...
scores := eval.Grade(trialCtx, t, c, res, lc)
if res != nil {
	res.StartedAt = start
	res.EndedAt = time.Now()
}
```
（`cmdRun` 与 `cmdCI` 两处均如此；`start` 声明在 `res, err :=` 之前一行。）

- [ ] **Step 4: 确认通过**

Run: `go build ./... && go test ./internal/eval/ ./cmd/eval/ -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/eval/runner.go internal/eval/phoenix_test.go cmd/eval/main.go
git commit -m "feat: trial 记录起止时间供 Phoenix 实验回写"
```

---

### Task 8: `eval.PushAgentExperiment` 回写

**Files:**
- Modify: `backend/internal/eval/phoenix.go`（追加）
- Modify: `backend/internal/eval/phoenix_test.go`（追加 fake Writer 测试）

**Interfaces:**
- Consumes: Task 2 `phoenix.Writer`/`RunRequest`/`EvaluationRequest`/`EvaluationResult`/`Experiment`、Task 5 常量、本包 `Report`/`ScoredTrial`/`GraderResult`。
- Produces:
  - `func PushAgentExperiment(w phoenix.Writer, datasetID string, report Report, cfgFilter []string, namePrefix string) error`
  - 每 config 一 experiment，命名 `<namePrefix>/<config>`。

- [ ] **Step 1: 写失败测试**

追加到 `backend/internal/eval/phoenix_test.go`：

```go
type fakeWriter struct {
	runs        []phoenix.RunRequest
	evals       []phoenix.EvaluationRequest
	experiments []string
	exID        string
}

func (f *fakeWriter) ListExamples(datasetID, versionID string) ([]phoenix.Example, error) {
	return []phoenix.Example{
		{ID: "EX-t1", Metadata: map[string]any{"task_id": "t1"}},
		{ID: "EX-t2", Metadata: map[string]any{"task_id": "t2"}},
	}, nil
}
func (f *fakeWriter) CreateExperiment(datasetID, name string, repetitions int, metadata map[string]any) (phoenix.Experiment, error) {
	f.experiments = append(f.experiments, name)
	return phoenix.Experiment{ID: "E-" + name}, nil
}
func (f *fakeWriter) LogRun(experimentID string, r phoenix.RunRequest) (string, error) {
	f.runs = append(f.runs, r)
	return "R", nil
}
func (f *fakeWriter) LogEvaluation(e phoenix.EvaluationRequest) (string, error) {
	f.evals = append(f.evals, e)
	return "EV", nil
}

func TestPushAgentExperimentFullMapping(t *testing.T) {
	rep := Report{
		Meta: Meta{Configs: []string{"rule"}, LLMEnabled: false},
		Tasks: []TaskReport{
			{ID: "t1", Configs: map[string]ConfigStats{
				"rule": {Trials: []ScoredTrial{{TrialResult: &TrialResult{Config: "rule", Trial: 1, RunStatus: "completed", Outcome: RunOutcome{RCA: model.RCA{RootCause: "x"}}},
					Scores: []GraderResult{
						{Name: "RootCauseMatch", Pass: true, Score: 1, Weight: 0.3, Detail: "ok"},
						{Name: "JudgeAccuracy", NA: true},
					}, Pass: true, Score: 1}}},
			}},
		},
	}
	w := &fakeWriter{}
	if err := PushAgentExperiment(w, "DS", rep, nil, "aiops-agents-eval"); err != nil {
		t.Fatal(err)
	}
	if len(w.experiments) != 1 || w.experiments[0] != "aiops-agents-eval/rule" {
		t.Fatalf("experiments = %v", w.experiments)
	}
	if len(w.runs) != 1 || w.runs[0].DatasetExampleID != "EX-t1" || w.runs[0].RepetitionNumber != 1 {
		t.Fatalf("runs = %+v", w.runs)
	}
	// 1 个 grader(NA 跳过) + 1 个 WeightedScore = 2
	if len(w.evals) != 2 {
		t.Fatalf("evals = %+v", w.evals)
	}
	if w.evals[0].Name != "RootCauseMatch" || w.evals[0].AnnotatorKind != "CODE" {
		t.Fatalf("eval0 = %+v", w.evals[0])
	}
	if w.evals[1].Name != "WeightedScore" {
		t.Fatalf("eval1 = %+v", w.evals[1])
	}
}

func TestPushAgentExperimentJudgeUsesLLMKind(t *testing.T) {
	rep := Report{
		Meta: Meta{Configs: []string{"llm-single"}},
		Tasks: []TaskReport{
			{ID: "t1", Configs: map[string]ConfigStats{
				"llm-single": {Trials: []ScoredTrial{{TrialResult: &TrialResult{Config: "llm-single", Trial: 1},
					Scores: []GraderResult{{Name: "JudgeAccuracy", Pass: true, Score: 0.9, Weight: 0.2}}, Pass: true, Score: 0.9}}},
			}},
		},
	}
	w := &fakeWriter{}
	if err := PushAgentExperiment(w, "DS", rep, nil, "p"); err != nil {
		t.Fatal(err)
	}
	if w.evals[0].AnnotatorKind != "LLM" {
		t.Fatalf("judge 应用 LLM kind: %+v", w.evals[0])
	}
}
```

（import 追加 `"aiops/internal/model"` 与 `"aiops/internal/phoenix"`。）

- [ ] **Step 2: 确认测试失败**

Run: `go test ./internal/eval/ -run TestPushAgentExperiment -v`
Expected: FAIL，`undefined: PushAgentExperiment`。

- [ ] **Step 3: 实现**

追加到 `backend/internal/eval/phoenix.go`：

```go
import (
	"fmt"
	"time"

	"aiops/internal/phoenix"
)

// PushAgentExperiment 把评测报告回写为 Phoenix 实验：每 config 一实验，每 trial 一 run，
// 每非 NA grader 一条 evaluation，另加一条 WeightedScore 总体评测。
func PushAgentExperiment(w phoenix.Writer, datasetID string, report Report, cfgFilter []string, namePrefix string) error {
	exs, err := w.ListExamples(datasetID, "")
	if err != nil {
		return fmt.Errorf("列数据集失败: %w", err)
	}
	byTask := map[string]string{}
	for _, e := range exs {
		if id, ok := e.Metadata["task_id"].(string); ok {
			byTask[id] = e.ID
		}
	}
	var firstErr error
	for _, cfgName := range report.Meta.Configs {
		if len(cfgFilter) > 0 && !contains(cfgFilter, cfgName) {
			continue
		}
		type trialRef struct {
			taskID string
			tr     ScoredTrial
		}
		var refs []trialRef
		for _, tr := range report.Tasks {
			cs, ok := tr.Configs[cfgName]
			if !ok {
				continue
			}
			for _, sc := range cs.Trials {
				refs = append(refs, trialRef{tr.ID, sc})
			}
		}
		if len(refs) == 0 {
			continue
		}
		maxRep := 1
		for _, r := range refs {
			if r.tr.Trial > maxRep {
				maxRep = r.tr.Trial
			}
		}
		exp, err := w.CreateExperiment(datasetID, namePrefix+"/"+cfgName, maxRep, map[string]any{
			"config": cfgName, "ts": report.Meta.Timestamp.Format(time.RFC3339), "llm_enabled": report.Meta.LLMEnabled,
		})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, r := range refs {
			exID, ok := byTask[r.taskID]
			if !ok {
				continue
			}
			sc := r.tr
			start, end := sc.StartedAt, sc.EndedAt
			if start.IsZero() {
				start = report.Meta.Timestamp
			}
			if end.IsZero() {
				end = report.Meta.Timestamp
			}
			runID, err := w.LogRun(exp.ID, phoenix.RunRequest{
				DatasetExampleID: exID,
				Output:           runOutput(sc.TrialResult),
				RepetitionNumber: sc.Trial,
				StartTime:        start,
				EndTime:          end,
				Error:            sc.ErrMsg,
			})
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			for _, g := range sc.Scores {
				if g.NA {
					continue
				}
				kind := "CODE"
				if g.Name == "JudgeAccuracy" || g.Name == "JudgeGroundedness" {
					kind = "LLM"
				}
				score := g.Score
				if _, err := w.LogEvaluation(phoenix.EvaluationRequest{
					ExperimentRunID: runID, Name: g.Name, AnnotatorKind: kind,
					StartTime: start, EndTime: end,
					Result:   &phoenix.EvaluationResult{Score: &score, Label: passLabel(g.Pass), Explanation: g.Detail},
					Metadata: map[string]any{"weight": g.Weight},
				}); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			score := sc.Score
			if _, err := w.LogEvaluation(phoenix.EvaluationRequest{
				ExperimentRunID: runID, Name: "WeightedScore", AnnotatorKind: "CODE",
				StartTime: start, EndTime: end,
				Result: &phoenix.EvaluationResult{Score: &score, Label: passLabel(sc.Pass), Explanation: sc.RunStatus},
			}); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func runOutput(res *TrialResult) map[string]any {
	// mustJSON 把嵌套结构转为可序列化 map，避免 Phoenix 收到 Go 结构体字段名。
	return map[string]any{
		"run_status": res.RunStatus,
		"rca":        res.Outcome.RCA,
		"recommend":  res.Outcome.Recommend,
		"reply":      res.Reply,
		"intent":     res.Intent,
		"intents":    res.Intents,
		"err_msg":    res.ErrMsg,
	}
}

func passLabel(pass bool) string {
	if pass {
		return "PASS"
	}
	return "FAIL"
}
```

注意：`phoenix.go` 原 import 只有 `"aiops/internal/phoenix"`；本步合并为上面的 import 块。`contains` 已在本包 `grader.go` 定义，直接复用。

- [ ] **Step 4: 确认通过**

Run: `go build ./... && go test ./internal/eval/ -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/eval/phoenix.go internal/eval/phoenix_test.go
git commit -m "feat: 评测报告回写 Phoenix 实验（runs + evaluations）"
```

---

### Task 9: `datasetctl experiment --dataset agents`

**Files:**
- Modify: `backend/cmd/datasetctl/main.go`（加 `experiment` 子命令 + `findDataset`）
- Modify: `backend/cmd/datasetctl/main_test.go`（加 experiment 参数校验测试）

**Interfaces:**
- Consumes: Task 8 `eval.PushAgentExperiment`、Task 2 `phoenix.Writer`、本包 `pushDataset`。
- Produces: CLI `datasetctl experiment --dataset agents --report <json> [--name <prefix>] [--config a,b]`。

- [ ] **Step 1: 写失败测试**

追加到 `backend/cmd/datasetctl/main_test.go`：

```go
func TestExperimentRequiresDatasetAndReport(t *testing.T) {
	if err := validateExperimentArgs("", "x.json"); err == nil {
		t.Fatal("缺 dataset 应报错")
	}
	if err := validateExperimentArgs("agents", ""); err == nil {
		t.Fatal("缺 report 应报错")
	}
	if err := validateExperimentArgs("agents", "x.json"); err != nil {
		t.Fatalf("合法参数不应报错: %v", err)
	}
}
```

- [ ] **Step 2: 确认测试失败**

Run: `go test ./cmd/datasetctl/ -run TestExperimentRequires -v`
Expected: FAIL，`undefined: validateExperimentArgs`。

- [ ] **Step 3: 实现**

`backend/cmd/datasetctl/main.go`：import 追加 `"encoding/json"`、`"strings"`；`main()` switch 增加 `case "experiment": cmdExperiment(c, os.Args[2:])`；追加：

```go
func validateExperimentArgs(dataset, report string) error {
	if dataset != "agents" && dataset != "rag" {
		return fmt.Errorf("--dataset 必填且为 agents|rag")
	}
	if report == "" {
		return fmt.Errorf("--report 必填")
	}
	return nil
}

func cmdExperiment(c *phoenix.Client, args []string) {
	fs := flag.NewFlagSet("experiment", flag.ContinueOnError)
	dataset := fs.String("dataset", "", "agents|rag")
	reportPath := fs.String("report", "", "报告 JSON 路径")
	name := fs.String("name", "", "实验名前缀")
	configs := fs.String("config", "", "逗号分隔，仅 agents；空=全部")
	fs.Parse(args)
	if err := validateExperimentArgs(*dataset, *reportPath); err != nil {
		log.Fatal(err)
	}
	b, err := os.ReadFile(*reportPath)
	if err != nil {
		log.Fatalf("读报告失败: %v", err)
	}
	switch *dataset {
	case "agents":
		var rep eval.Report
		if err := json.Unmarshal(b, &rep); err != nil {
			log.Fatalf("报告解析失败: %v", err)
		}
		dsID, err := findDataset(c, eval.AgentDatasetName)
		if err != nil {
			log.Fatal(err)
		}
		prefix := *name
		if prefix == "" {
			prefix = eval.AgentDatasetName
		}
		var filter []string
		if *configs != "" {
			filter = strings.Split(*configs, ",")
		}
		if err := eval.PushAgentExperiment(c, dsID, rep, filter, prefix); err != nil {
			log.Fatalf("回写失败: %v", err)
		}
		fmt.Printf("experiment 已回写: %s（%d configs）\n", prefix, len(rep.Meta.Configs))
	case "rag":
		log.Fatal("rag 回写将在后续任务提供")
	}
}

// findDataset 按名称查数据集 id。
func findDataset(c *phoenix.Client, name string) (string, error) {
	ds, err := c.ListDatasets()
	if err != nil {
		return "", err
	}
	for _, d := range ds {
		if d.Name == name {
			return d.ID, nil
		}
	}
	return "", fmt.Errorf("未找到数据集 %s（先执行 datasetctl push）", name)
}
```

（Task 11 会把 `rag` 分支替换为真实实现。）

- [ ] **Step 4: 确认通过**

Run: `go build ./... && go test ./cmd/datasetctl/ -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add cmd/datasetctl/main.go cmd/datasetctl/main_test.go
git commit -m "feat: datasetctl experiment --dataset agents 回写实验"
```

---

### Task 10: `goldenset.RagReport` + `cmd/rageval -o`

**Files:**
- Create: `backend/internal/goldenset/report.go`
- Create: `backend/internal/goldenset/report_test.go`
- Modify: `backend/cmd/rageval/main.go`（计算 Precision@K、逐项计时、`-o` 落盘）
- Create/Modify: `backend/cmd/rageval/metrics.go`（`ComputePrecisionAtK`）+ `metrics_test.go`

**Interfaces:**
- Consumes: 无外部包（`goldenset` 内自洽）。
- Produces:
  - `type ItemResult struct { ID, Type, Question string; TopK int; RankedDocIDs []string; Behavior string; MRR, NDCG, Recall, Precision float64; RejectOK bool; StartedAt, EndedAt time.Time }`
  - `type RagMeta struct { Timestamp time.Time; Enhance bool; TopK int }`
  - `type Overall struct { HitCount, RejectCount, RejectOK int; MRR, NDCG, Recall, Precision float64 }`
  - `type RagReport struct { Meta RagMeta; Items []ItemResult; Overall Overall }`
  - `func (r RagReport) ToJSON() ([]byte, error)`
  - `cmd/rageval` flag `-o <path>`

- [ ] **Step 1: 写失败测试**

创建 `backend/internal/goldenset/report_test.go`：

```go
package goldenset

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRagReportRoundTrip(t *testing.T) {
	rep := RagReport{
		Meta:  RagMeta{Timestamp: time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC), Enhance: true, TopK: 3},
		Items: []ItemResult{{ID: "q1", Type: "fact", TopK: 3, RankedDocIDs: []string{"d1"}, Behavior: "hit", MRR: 1, NDCG: 1, Recall: 1, Precision: 0.33}},
		Overall: Overall{HitCount: 1, MRR: 1, NDCG: 1, Recall: 1, Precision: 0.33},
	}
	b, err := rep.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	var got RagReport
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Items[0].ID != "q1" || got.Overall.HitCount != 1 {
		t.Fatalf("got = %+v", got)
	}
	if !strings.Contains(string(b), `"meta"`) || !strings.Contains(string(b), `"overall"`) {
		t.Fatalf("json = %s", b)
	}
}
```

- [ ] **Step 2: 确认测试失败**

Run: `go test ./internal/goldenset/ -run TestRagReport -v`
Expected: FAIL，`undefined: RagReport`。

- [ ] **Step 3: 实现 report.go**

创建 `backend/internal/goldenset/report.go`：

```go
package goldenset

import (
	"encoding/json"
	"time"
)

type ItemResult struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	Question     string    `json:"question"`
	TopK         int       `json:"top_k"`
	RankedDocIDs []string  `json:"ranked_doc_ids"`
	Behavior     string    `json:"behavior"`
	MRR          float64   `json:"mrr"`
	NDCG         float64   `json:"ndcg"`
	Recall       float64   `json:"recall"`
	Precision    float64   `json:"precision"`
	RejectOK     bool      `json:"reject_ok"`
	StartedAt    time.Time `json:"started_at"`
	EndedAt      time.Time `json:"ended_at"`
}

type RagMeta struct {
	Timestamp time.Time `json:"ts"`
	Enhance   bool      `json:"enhance"`
	TopK      int       `json:"top_k"`
}

type Overall struct {
	HitCount    int     `json:"hit_count"`
	RejectCount int     `json:"reject_count"`
	RejectOK    int     `json:"reject_ok"`
	MRR         float64 `json:"mrr"`
	NDCG        float64 `json:"ndcg"`
	Recall      float64 `json:"recall"`
	Precision   float64 `json:"precision"`
}

type RagReport struct {
	Meta    RagMeta     `json:"meta"`
	Items   []ItemResult `json:"items"`
	Overall Overall     `json:"overall"`
}

func (r RagReport) ToJSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}
```

- [ ] **Step 4: 确认 report 测试通过**

Run: `go test ./internal/goldenset/ -run TestRagReport -v`
Expected: PASS。

- [ ] **Step 5: 加 ComputePrecisionAtK（写失败测试）**

追加到 `backend/cmd/rageval/metrics_test.go`：

```go
func TestComputePrecisionAtK(t *testing.T) {
	ranked := []string{"a", "b", "c"}
	relevant := map[string]bool{"a": true}
	if got := ComputePrecisionAtK(ranked, relevant, 3); math.Abs(got-1.0/3.0) > 1e-9 {
		t.Fatalf("precision = %v, want 1/3", got)
	}
	if got := ComputePrecisionAtK(ranked, relevant, 1); got != 1 {
		t.Fatalf("precision@1 = %v", got)
	}
}
```

（import 追加 `"math"`。）

Run: `go test ./cmd/rageval/ -run TestComputePrecisionAtK -v`
Expected: FAIL，`undefined: ComputePrecisionAtK`。

- [ ] **Step 6: 实现 ComputePrecisionAtK**

追加到 `backend/cmd/rageval/metrics.go`：

```go
// ComputePrecisionAtK 命中相关文档数 / K（K<=0 或结果为空时返回 0）。
func ComputePrecisionAtK(ranked []string, relevant map[string]bool, k int) float64 {
	if k <= 0 {
		return 0
	}
	hits := 0
	for i, id := range ranked {
		if i >= k {
			break
		}
		if relevant[id] {
			hits++
		}
	}
	return float64(hits) / float64(k)
}
```

Run: `go test ./cmd/rageval/ -run TestComputePrecisionAtK -v`
Expected: PASS。

- [ ] **Step 7: `cmd/rageval` 计算 Precision/计时并落盘**

在 `cmd/rageval/main.go`：

1. import 追加 `"aiops/internal/goldenset"`、`"time"`（若未有）。
2. flag 增加：`out := flag.String("o", "", "结构化报告输出路径（JSON）")`。
3. `agg` 结构体（循环前的本地类型）追加字段 `SumPrecision float64`。
4. 声明 per-item 收集器（在 `resultsMap := ...` 附近）：
```go
	var itemResults []goldenset.ItemResult
```
5. 循环体改为（保留既有检索/打印，新增计时、precision 与 per-item 收集）：
```go
	for _, it := range items {
		k := it.TopK
		if k <= 0 {
			k = 3
		}
		ctx := context.Background()
		qStart := time.Now()
		res, err := searcher.SearchCases(ctx, retrieve.Query{Text: it.Question}, retrieve.Options{TopK: k})
		qEnd := time.Now()
		if err != nil {
			res = nil
		}
		ranked := make([]string, len(res))
		for i, r := range res {
			ranked[i] = r.DocID
		}
		resultsMap[it.ID] = ranked
		expectsMap[it.ID] = it.Expect.Behavior

		ag := perType[it.Type]
		if ag == nil {
			ag = &agg{}
			perType[it.Type] = ag
		}
		ag.Count++

		ir := goldenset.ItemResult{
			ID: it.ID, Type: it.Type, Question: it.Question, TopK: k,
			RankedDocIDs: ranked, Behavior: it.Expect.Behavior,
			StartedAt: qStart, EndedAt: qEnd,
		}
		if it.Expect.Behavior == "reject" {
			ag.RejectCount++
			overallRejectTotal++
			if len(ranked) == 0 {
				ag.RejectOK++
				overallRejectOK++
				ir.RejectOK = true
			}
		} else {
			ag.HitCount++
			overallHit.Count++
			relevant := map[string]bool{}
			for _, id := range it.Expect.DocIDs {
				relevant[id] = true
			}
			mrr := ComputeMRR(ranked, relevant)
			ndcg := ComputeNDCG(ranked, relevant, k)
			rec := ComputeRecallAtK(ranked, relevant, k)
			prec := ComputePrecisionAtK(ranked, relevant, k)
			ag.SumMRR += mrr
			ag.SumNDCG += ndcg
			ag.SumRecall += rec
			ag.SumPrecision += prec
			overallHit.SumMRR += mrr
			overallHit.SumNDCG += ndcg
			overallHit.SumRecall += rec
			overallHit.SumPrecision += prec
			ir.MRR, ir.NDCG, ir.Recall, ir.Precision = mrr, ndcg, rec, prec
		}
		itemResults = append(itemResults, ir)
	}
```
（`ag.SumNDCG`/`SumRecall` 的字段名以现有 `agg` 定义为准；若现名为 `SumNDCG`/`SumRecall` 则保持不变，仅新增 `SumPrecision`。）

6. 在既有 `overallAvgMRR` 等字符串计算处并列记录 float（供报告用）：
```go
	var overallMRRf, overallNDCGf, overallRecf, overallPrecf float64
	if overallHit.Count > 0 {
		overallMRRf = overallHit.SumMRR / float64(overallHit.Count)
		overallNDCGf = overallHit.SumNDCG / float64(overallHit.Count)
		overallRecf = overallHit.SumRecall / float64(overallHit.Count)
		overallPrecf = overallHit.SumPrecision / float64(overallHit.Count)
	}
```

7. 函数末尾（打印之后）落盘：
```go
	if *out != "" {
		rep := goldenset.RagReport{
			Meta:  goldenset.RagMeta{Timestamp: time.Now(), Enhance: *enhance, TopK: 3},
			Items: itemResults,
			Overall: goldenset.Overall{
				HitCount: overallHit.Count, RejectCount: overallRejectTotal, RejectOK: overallRejectOK,
				MRR: overallMRRf, NDCG: overallNDCGf, Recall: overallRecf, Precision: overallPrecf,
			},
		}
		b, _ := rep.ToJSON()
		if err := os.WriteFile(*out, b, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "写报告失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("报告已写入 %s\n", *out)
	}
```
（保留既有 `overallAvgMRR` 等字符串打印逻辑不动。）

- [ ] **Step 8: 确认通过**

Run: `go build ./... && go test ./cmd/rageval/ ./internal/goldenset/ -v`
Expected: PASS。

- [ ] **Step 9: Commit**

```bash
git add internal/goldenset/report.go internal/goldenset/report_test.go cmd/rageval/main.go cmd/rageval/metrics.go cmd/rageval/metrics_test.go
git commit -m "feat: rageval 结构化报告（Precision@K + -o JSON）"
```

---

### Task 11: `goldenset.PushRagExperiment` + `datasetctl experiment --dataset rag`

**Files:**
- Modify: `backend/internal/goldenset/report.go`（追加回写）
- Modify: `backend/internal/goldenset/report_test.go`（fake Writer 测试）
- Modify: `backend/cmd/datasetctl/main.go`（替换 rag 分支）

**Interfaces:**
- Consumes: Task 2 `phoenix.Writer`/`RunRequest`/`EvaluationRequest`、Task 10 `RagReport`。
- Produces:
  - `func PushRagExperiment(w phoenix.Writer, datasetID string, report RagReport, namePrefix string) error`

- [ ] **Step 1: 写失败测试**

追加到 `backend/internal/goldenset/report_test.go`：

```go
type fakeWriter struct {
	runs  []phoenix.RunRequest
	evals []phoenix.EvaluationRequest
	exp   []string
}

func (f *fakeWriter) ListExamples(datasetID, versionID string) ([]phoenix.Example, error) {
	return []phoenix.Example{{ID: "EX-q1", Metadata: map[string]any{"id": "q1"}}}, nil
}
func (f *fakeWriter) CreateExperiment(datasetID, name string, repetitions int, metadata map[string]any) (phoenix.Experiment, error) {
	f.exp = append(f.exp, name)
	return phoenix.Experiment{ID: "E1"}, nil
}
func (f *fakeWriter) LogRun(experimentID string, r phoenix.RunRequest) (string, error) {
	f.runs = append(f.runs, r)
	return "R1", nil
}
func (f *fakeWriter) LogEvaluation(e phoenix.EvaluationRequest) (string, error) {
	f.evals = append(f.evals, e)
	return "EV1", nil
}

func TestPushRagExperiment(t *testing.T) {
	rep := RagReport{
		Meta:  RagMeta{Enhance: true, TopK: 3},
		Items: []ItemResult{{ID: "q1", Type: "fact", TopK: 3, RankedDocIDs: []string{"d1"}, Behavior: "hit", MRR: 1, NDCG: 1, Recall: 1, Precision: 0.33}},
	}
	w := &fakeWriter{}
	if err := PushRagExperiment(w, "DS", rep, "aiops-rag-golden"); err != nil {
		t.Fatal(err)
	}
	if len(w.exp) != 1 || w.exp[0] != "aiops-rag-golden/rag" {
		t.Fatalf("exp = %v", w.exp)
	}
	if len(w.runs) != 1 || w.runs[0].DatasetExampleID != "EX-q1" {
		t.Fatalf("runs = %+v", w.runs)
	}
	names := map[string]bool{}
	for _, e := range w.evals {
		names[e.Name] = true
	}
	for _, want := range []string{"MRR", "NDCG@K", "Recall@K", "Precision@K"} {
		if !names[want] {
			t.Fatalf("缺评测 %s: %+v", want, w.evals)
		}
	}
}
```

（import 追加 `"aiops/internal/phoenix"`。）

- [ ] **Step 2: 确认测试失败**

Run: `go test ./internal/goldenset/ -run TestPushRagExperiment -v`
Expected: FAIL，`undefined: PushRagExperiment`。

- [ ] **Step 3: 实现**

追加到 `backend/internal/goldenset/report.go`（import 追加 `"fmt"`、`"aiops/internal/phoenix"`）：

```go
// PushRagExperiment 把 rageval 报告回写为单个 Phoenix 实验：每 item 一 run，
// 每项指标一条 evaluation（CODE），reject 题额外一条 RejectOK。
func PushRagExperiment(w phoenix.Writer, datasetID string, report RagReport, namePrefix string) error {
	exs, err := w.ListExamples(datasetID, "")
	if err != nil {
		return fmt.Errorf("列数据集失败: %w", err)
	}
	byID := map[string]string{}
	for _, e := range exs {
		if id, ok := e.Metadata["id"].(string); ok {
			byID[id] = e.ID
		}
	}
	exp, err := w.CreateExperiment(datasetID, namePrefix+"/rag", 1, map[string]any{
		"enhance": report.Meta.Enhance, "top_k": report.Meta.TopK, "ts": report.Meta.Timestamp.Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	var firstErr error
	for _, it := range report.Items {
		exID, ok := byID[it.ID]
		if !ok {
			continue
		}
		start, end := it.StartedAt, it.EndedAt
		if start.IsZero() {
			start = report.Meta.Timestamp
		}
		if end.IsZero() {
			end = report.Meta.Timestamp
		}
		runID, err := w.LogRun(exp.ID, phoenix.RunRequest{
			DatasetExampleID: exID, Output: map[string]any{"ranked_doc_ids": it.RankedDocIDs},
			RepetitionNumber: 1, StartTime: start, EndTime: end,
		})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		metrics := []struct {
			name string
			val  float64
		}{
			{"MRR", it.MRR}, {"NDCG@K", it.NDCG}, {"Recall@K", it.Recall}, {"Precision@K", it.Precision},
		}
		for _, m := range metrics {
			v := m.val
			if _, err := w.LogEvaluation(phoenix.EvaluationRequest{
				ExperimentRunID: runID, Name: m.name, AnnotatorKind: "CODE",
				StartTime: start, EndTime: end,
				Result: &phoenix.EvaluationResult{Score: &v, Explanation: it.Behavior},
			}); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if it.Behavior == "reject" {
			v := 0.0
			if it.RejectOK {
				v = 1.0
			}
			if _, err := w.LogEvaluation(phoenix.EvaluationRequest{
				ExperimentRunID: runID, Name: "RejectOK", AnnotatorKind: "CODE",
				StartTime: start, EndTime: end,
				Result: &phoenix.EvaluationResult{Score: &v, Label: passFail(it.RejectOK)},
			}); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func passFail(b bool) string {
	if b {
		return "PASS"
	}
	return "FAIL"
}
```

- [ ] **Step 4: 替换 datasetctl rag 分支**

`backend/cmd/datasetctl/main.go` 中把 `case "rag": log.Fatal("rag 回写将在后续任务提供")` 替换为：

```go
	case "rag":
		var rep goldenset.RagReport
		if err := json.Unmarshal(b, &rep); err != nil {
			log.Fatalf("报告解析失败: %v", err)
		}
		dsID, err := findDataset(c, goldenset.RagDatasetName)
		if err != nil {
			log.Fatal(err)
		}
		prefix := *name
		if prefix == "" {
			prefix = goldenset.RagDatasetName
		}
		if err := goldenset.PushRagExperiment(c, dsID, rep, prefix); err != nil {
			log.Fatalf("回写失败: %v", err)
		}
		fmt.Printf("experiment 已回写: %s/rag（%d items）\n", prefix, len(rep.Items))
```

- [ ] **Step 5: 确认通过**

Run: `go build ./... && go test ./internal/goldenset/ ./cmd/datasetctl/ -v`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
git add internal/goldenset/report.go internal/goldenset/report_test.go cmd/datasetctl/main.go
git commit -m "feat: RAG 评测报告回写 Phoenix 实验"
```

---

### Task 12: 文档与全量回归

**Files:**
- Modify: `README.md`（环境变量表下方说明 datasetctl 用法；若 Phoenix 分组已有则只追加命令说明）
- Modify: `AGENTS.md`（「其余核心模块」补充 `internal/phoenix`、`internal/goldenset`、`cmd/datasetctl`）

**Interfaces:**
- Consumes: 全部前序任务。
- Produces: 文档同步。

- [ ] **Step 1: 更新 README**

在 README 的 Phoenix 说明处追加：

```markdown
### Phoenix 评测数据集

先 `datasetctl push` 上传数据集，本地评测后 `datasetctl experiment` 回写实验：

```bash
cd backend
go run ./cmd/promptctl push                              # 提示词（既有）
go run ./cmd/datasetctl push --dataset all               # 数据集：agents + rag
go run ./cmd/eval run --report eval-report.json
go run ./cmd/datasetctl experiment --dataset agents --report eval-report.json
go run ./cmd/rageval -o rageval-report.json
go run ./cmd/datasetctl experiment --dataset rag --report rageval-report.json
```
```

- [ ] **Step 2: 更新 AGENTS.md**

「其余核心模块」追加一行：

```markdown
- Phoenix 评测：`internal/phoenix`（datasets/experiments 轻量 REST 客户端）、`internal/goldenset`（RAG 标注集解析/报告/Phoenix 映射）、`cmd/datasetctl`（push/list/experiment）；`cmd/rageval -o` 出结构化报告。
```

- [ ] **Step 3: 全量回归**

Run（`backend/`）: `go build ./... && go vet ./... && go test ./...`
Expected: 全绿。

Run（`backend/`，`PHOENIX_BASE_URL` 未设）: `go run ./cmd/server` 启动日志无异常，`curl -s localhost:8080/healthz` 正常；确认无外呼（无 Phoenix 相关日志）。启动后 Ctrl-C。

- [ ] **Step 4: Commit**

```bash
git add ../README.md ../AGENTS.md
git commit -m "docs: 补充 datasetctl 与 Phoenix 评测数据集说明"
```

---

## 收尾

- [ ] 对照 spec §5 自查六个端点均有契约测试；§7/§8 映射有单测覆盖。
- [ ] `PHOENIX_BASE_URL` 为空时 `go test ./...` 全绿、server 零外呼。
- [ ] 冒烟（Phoenix 可用时，compose 起 phoenix）：`datasetctl push` → `datasetctl list` 可见两数据集 → `eval run --report` → `datasetctl experiment --dataset agents` → Phoenix UI 见实验与评分；记录结果。若 Phoenix 不可用，标注「待补验」，不阻塞。
- [ ] 评测题扩容完成后（配套计划），重跑 `datasetctl push` 使数据集包含新题。
