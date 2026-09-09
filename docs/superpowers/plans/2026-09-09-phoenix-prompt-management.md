# Phoenix Prompt Hub 提示词管理 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把全项目 14 条 LLM 提示词托管到 Phoenix Prompt Hub：运行时按 name+tag 拉取 + 缓存，Phoenix 未配置/失败时回退内嵌默认模板，调用点签名基本不变。

**Architecture:** `internal/prompt` 包升级为提示词注册中心——`phoenix.go`（轻量 REST 客户端，无官方 Go SDK）+ `registry.go`（14 条 Entry 注册表、`{{var}}` 渲染、TTL 缓存 + singleflight + 降级）；7 条内联提示词收编进 prompt 包；`cmd/promptctl` 离线灌库；docker-compose 加 phoenix 服务。

**Tech Stack:** Go 1.24、标准库 `net/http`/`encoding/json`、`golang.org/x/sync/singleflight`（已有依赖，零新增）。

**Spec:** `docs/superpowers/specs/2026-09-09-phoenix-prompt-management-design.md`

## Global Constraints

- `internal/prompt` 禁止 import 任何 `aiops/internal/*` 包；配置经 `Configure` 参数注入。
- 零新增第三方依赖（只用标准库 + 已有的 `golang.org/x/sync`）。
- Phoenix 端点（已核实官方 REST API v1）：拉取 `GET /v1/prompts/{prompt_identifier}/tags/{tag_name}`（operationId `getPromptVersionByTagName`）；新建 `POST /v1/prompts`；列表 `GET /v1/prompts`。
- 默认模板文本必须与现有文案逐字一致（`iteration==0` 分支文案除外，该文案留在 Go 侧）。
- 代码注释、commit message 用中文，commit 用 conventional 前缀。
- 所有命令在 `backend/` 目录执行（除 compose/README 步骤）；测试命令：`go test ./internal/prompt/`、全量 `go test ./...`。
- 测试建库惯用法（如需）：`store.New(filepath.Join(t.TempDir(), "t.db"))` + `mockdata.GenerateAll(seed)`；本计划测试不触库。
- registry 是包级全局状态，prompt 包内测试每个用例结束必须 `Configure(nil, 0)` 复位。

---

### Task 1: PhoenixFetcher — 轻量 REST 客户端

**Files:**
- Create: `backend/internal/prompt/phoenix.go`
- Test: `backend/internal/prompt/phoenix_test.go`

**Interfaces:**
- Consumes: 无（仅标准库）。
- Produces（后续任务依赖的精确签名）:
  - `type PhoenixFetcher struct { BaseURL, Tag, APIKey string; HTTP *http.Client }`
  - `func NewPhoenixFetcher(baseURL, tag, apiKey string) *PhoenixFetcher`
  - `func (f *PhoenixFetcher) Fetch(name string) (string, bool)` — 按 name+tag 拉取，返回第一条消息 content；任何失败 `(, false)`
  - `func (f *PhoenixFetcher) Push(name, role, content, modelName string) error` — 新建 prompt（含首个版本），供 promptctl 用
  - `func (f *PhoenixFetcher) List() ([]PromptSummary, error)`；`type PromptSummary struct { Name, ID string }`

- [ ] **Step 1: 写失败测试**

创建 `backend/internal/prompt/phoenix_test.go`：

```go
package prompt

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchByTagSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/prompts/rca-system/tags/production" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"data":{"id":"v1","template":{"messages":[{"role":"SYSTEM","content":"你是算力中心硬件运维专家"}]}}}`)
	}))
	defer srv.Close()
	f := NewPhoenixFetcher(srv.URL, "production", "")
	got, ok := f.Fetch("rca-system")
	if !ok || got != "你是算力中心硬件运维专家" {
		t.Fatalf("Fetch = (%q, %v), 期望第一条消息 content", got, ok)
	}
}

func TestFetchSendsBearerWhenAPIKeySet(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"data":{"template":{"messages":[{"role":"USER","content":"x"}]}}}`)
	}))
	defer srv.Close()
	f := NewPhoenixFetcher(srv.URL, "production", "sk-test")
	if _, ok := f.Fetch("a"); !ok {
		t.Fatal("Fetch 应成功")
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
}

func TestFetchFailsOnBadStatusOrBody(t *testing.T) {
	cases := []http.HandlerFunc{
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
		func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `not-json`) },
		func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"data":{"template":{"messages":[]}}}`) },
		func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"data":{"template":{"messages":[{"role":"SYSTEM","content":""}]}}}`) },
	}
	for i, h := range cases {
		srv := httptest.NewServer(h)
		f := NewPhoenixFetcher(srv.URL, "production", "")
		if _, ok := f.Fetch("a"); ok {
			t.Fatalf("case %d: 坏响应应返回 ok=false", i)
		}
		srv.Close()
	}
}

func TestFetchFailsOnTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()
	f := NewPhoenixFetcher(srv.URL, "production", "")
	f.HTTP = &http.Client{Timeout: 10 * time.Millisecond}
	if _, ok := f.Fetch("a"); ok {
		t.Fatal("超时应返回 ok=false")
	}
}

func TestPushCreatesPromptWithTemplate(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/prompts" {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"data":{"id":"p-1"}}`)
	}))
	defer srv.Close()
	f := NewPhoenixFetcher(srv.URL, "production", "")
	if err := f.Push("rca-system", "system", "你好 {{x}}", "deepseek-chat"); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if body["name"] != "rca-system" {
		t.Fatalf("name = %v", body["name"])
	}
	ver := body["version"].(map[string]any)
	if ver["model_name"] != "deepseek-chat" || ver["template_format"] != "FSTRING" {
		t.Fatalf("version = %v", ver)
	}
	msgs := ver["template"].(map[string]any)["messages"].([]any)
	m0 := msgs[0].(map[string]any)
	if m0["role"] != "SYSTEM" || m0["content"] != "你好 {{x}}" {
		t.Fatalf("messages[0] = %v", m0)
	}
}

func TestListReturnsSummaries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"name":"rca-system","id":"p-1"},{"name":"kb-summarize","id":"p-2"}]}`)
	}))
	defer srv.Close()
	f := NewPhoenixFetcher(srv.URL, "production", "")
	ps, err := f.List()
	if err != nil || len(ps) != 2 || ps[1].Name != "kb-summarize" {
		t.Fatalf("List = (%v, %v)", ps, err)
	}
}
```

- [ ] **Step 2: 确认测试失败**

Run: `go test ./internal/prompt/ -run TestFetch -v && go test ./internal/prompt/ -run TestPush -v && go test ./internal/prompt/ -run TestList -v`
Expected: FAIL，`undefined: NewPhoenixFetcher`

- [ ] **Step 3: 实现 phoenix.go**

创建 `backend/internal/prompt/phoenix.go`：

```go
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
	ModelProvider   string       `json:"model_provider"`
	ModelName       string       `json:"model_name"`
	TemplateType    string       `json:"template_type"`
	TemplateFormat  string       `json:"template_format"`
	Template        pushTemplate `json:"template"`
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
```

- [ ] **Step 4: 确认测试通过**

Run: `go test ./internal/prompt/ -v`
Expected: 全部 PASS（含原有 prompt_test.go）

- [ ] **Step 5: Commit**

```bash
git add internal/prompt/phoenix.go internal/prompt/phoenix_test.go
git commit -m "feat: Phoenix Prompt Hub 轻量 REST 客户端（Fetch/Push/List）"
```

---

### Task 2: Registry — 14 条注册表 + 渲染 + 缓存降级

**Files:**
- Create: `backend/internal/prompt/registry.go`
- Test: `backend/internal/prompt/registry_test.go`

**Interfaces:**
- Consumes: Task 1 的 `PhoenixFetcher.Fetch`。
- Produces（后续任务依赖的精确签名）:
  - `type Entry struct { Name, Role, Default string }`（Role 为 `"system"`/`"user"`，push 时用）
  - `var All = map[string]Entry` — 14 条（key = Entry.Name = Phoenix prompt 标识）
  - `func Configure(f *PhoenixFetcher, d time.Duration)` — 注入拉取器；`f == nil` 即关闭拉取并清缓存
  - `func Render(name string, vars map[string]string) string` — 生效模板（Phoenix 优先/默认兜底）+ `{{var}}` 替换；未注册 name 返回空串

- [ ] **Step 1: 写失败测试**

创建 `backend/internal/prompt/registry_test.go`：

```go
package prompt

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func resetRegistry(t *testing.T) {
	t.Cleanup(func() { Configure(nil, 0) })
}

func TestAllRegistryComplete(t *testing.T) {
	if len(All) != 14 {
		t.Fatalf("注册表应含 14 条, got %d", len(All))
	}
	for name, e := range All {
		if e.Name != name || (e.Role != "system" && e.Role != "user") || e.Default == "" {
			t.Fatalf("Entry %q 不合法: %+v", name, e)
		}
	}
}

func TestRenderUsesDefaultWhenNotConfigured(t *testing.T) {
	resetRegistry(t)
	got := Render("rca-gap-hint", map[string]string{"iteration": "2"})
	if got != "这是第2轮补充取证，请重点分析新增证据。" {
		t.Fatalf("Render = %q", got)
	}
}

func TestRenderUnknownNameReturnsEmpty(t *testing.T) {
	resetRegistry(t)
	if got := Render("no-such-prompt", nil); got != "" {
		t.Fatalf("未注册 name 应返回空串, got %q", got)
	}
}

func TestRenderPrefersPhoenix(t *testing.T) {
	resetRegistry(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"template":{"messages":[{"role":"SYSTEM","content":"PHOENIX 版 {{x}}"}]}}}`))
	}))
	defer srv.Close()
	Configure(NewPhoenixFetcher(srv.URL, "production", ""), time.Minute)
	got := Render("rca-gap-hint", map[string]string{"x": "变量"})
	if got != "PHOENIX 版 变量" {
		t.Fatalf("Render = %q", got)
	}
}

func TestRenderFallsBackOnFetchFailure(t *testing.T) {
	resetRegistry(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	Configure(NewPhoenixFetcher(srv.URL, "production", ""), time.Minute)
	got := Render("rca-gap-hint", map[string]string{"iteration": "3"})
	if got != "这是第3轮补充取证，请重点分析新增证据。" {
		t.Fatalf("失败应回退默认模板, got %q", got)
	}
}

func TestRenderStaleThenBackgroundRefresh(t *testing.T) {
	resetRegistry(t)
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"data":{"template":{"messages":[{"role":"SYSTEM","content":"V1"}]}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"template":{"messages":[{"role":"SYSTEM","content":"V2"}]}}}`))
	}))
	defer srv.Close()
	Configure(NewPhoenixFetcher(srv.URL, "production", ""), 30*time.Millisecond)
	if got := Render("rca-gap-hint", nil); got != "V1" {
		t.Fatalf("首次应取 V1, got %q", got)
	}
	time.Sleep(80 * time.Millisecond) // TTL 过期
	if got := Render("rca-gap-hint", nil); got != "V1" {
		t.Fatalf("过期后应先返回旧值 V1, got %q", got)
	}
	time.Sleep(80 * time.Millisecond) // 等后台刷新完成
	if got := Render("rca-gap-hint", nil); got != "V2" {
		t.Fatalf("后台刷新后应为 V2, got %q", got)
	}
}

func TestRenderSingleflightWithinTTL(t *testing.T) {
	resetRegistry(t)
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		_, _ = w.Write([]byte(`{"data":{"template":{"messages":[{"role":"SYSTEM","content":"V"}]}}}`))
	}))
	defer srv.Close()
	Configure(NewPhoenixFetcher(srv.URL, "production", ""), time.Minute)
	for i := 0; i < 10; i++ {
		if got := Render("rca-gap-hint", nil); got != "V" {
			t.Fatalf("got %q", got)
		}
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("TTL 内 10 次调用应只请求 1 次, got %d", got)
	}
}
```

- [ ] **Step 2: 确认测试失败**

Run: `go test ./internal/prompt/ -run TestRender -v`
Expected: FAIL，`undefined: Configure` / `undefined: Render`

- [ ] **Step 3: 实现 registry.go**

创建 `backend/internal/prompt/registry.go`（Default 文本 = 现有 13 处文案逐字转写，动态部分改 `{{var}}`）：

```go
package prompt

import (
	"log"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Entry 一条提示词注册信息：name 为 Phoenix prompt 标识；Default 为内嵌默认模板（{{var}} 占位）；
// Role 为 "system"/"user"（push 灌库时作为消息角色）。
type Entry struct {
	Name    string
	Role    string
	Default string
}

// All 全部提示词注册表。Default 与代码中原文案逐字一致（见 spec §4 变量映射）。
var All = map[string]Entry{
	"chat-answer-system": {"chat-answer-system", "system",
		"【核心规则】必须严格基于【相关上下文】与已有排查结论回答；上下文中没有的信息回答\"暂无相关信息\"，禁止使用自身知识补充，禁止编造。" +
			"你是算力中心硬件运维助手，结论需注明依据来源（上下文段落或已有根因）。" +
			"【输出要求】分点回答，每点不超过3句，语言简洁专业。"},
	"chat-answer-user": {"chat-answer-user", "user",
		"告警 {{alert_id}}：设备={{device}} 指标={{metric}}\n已有根因：{{rca}}\n已有处置：{{actions}}\n\n【相关上下文】\n{{context}}\n\n用户问题：{{question}}"},
	"knowledge-intro": {"knowledge-intro", "system",
		"以下是当前任务相关的上下文信息。回答时优先依据【知识库】【设备历史经验】等分段内容，引用结论时标注来源文档；分段中无相关信息时直接说明，不要引用不存在的段落。"},
	"rca-system": {"rca-system", "system",
		"你是算力中心硬件运维专家。基于给定证据输出JSON：{\"root_cause\":string,\"evidence_chain\":[string],\"confidence\":0~1,\"gaps\":[string]}。证据中不存在的内容必须写入 gaps，禁止编造 evidence_chain。只输出JSON。"},
	"recommend-system": {"recommend-system", "system",
		"你是算力中心运维助手。基于根因和预案，给出简明的处置建议步骤列表。步骤须与 runbook_ref 对应，无对应预案时 risk 填\"无预案参考\"。只输出JSON：{\"actions\":[string],\"runbook_ref\":string,\"risk\":string}"},
	"query-rewrite": {"query-rewrite", "user",
		"将下述查询结合对话历史改写为独立检索句，仅输出改写句。设备名、告警ID、指标名等实体词必须原样保留。\n历史：{{history}}\n查询：{{query}}"},
	"intent-rewrite-split": {"intent-rewrite-split", "system",
		"你是 AIOps 运维助手的问题理解模块。把用户提问重写为规范、完整的中文问题，并拆分为相互独立的子问题。只输出 JSON：{\"rewritten\":\"<重写后的完整问题>\",\"sub_questions\":[\"<子问题1>\",\"<子问题2>\"]}。若只有一个意图，sub_questions 只含一项。子问题必须完整自包含（补全指代、告警ID、设备名等）。"},
	"intent-classify": {"intent-classify", "system",
		"你是意图分类器。判断用户请求属于哪个意图，只输出 JSON：{\"intent\":\"<意图名>\",\"confidence\":<0到1之间的小数>}。意图名必须是 {{candidates}}。无法确定时返回 {\"intent\":\"Unknown\",\"confidence\":0}。"},
	"rca-react-system": {"rca-react-system", "system",
		"你是算力中心硬件运维专家。基于上下文，若证据不足可调用工具补充，推理记录写入write_scratchpad。最终输出JSON: {\"final_answer\":{\"root_cause\":string,\"evidence_chain\":[string],\"confidence\":0~1,\"gaps\":[string]}}。除final_answer或tool_call外不输出其它内容。"},
	"rca-gap-hint": {"rca-gap-hint", "user",
		"这是第{{iteration}}轮补充取证，请重点分析新增证据。"},
	"eval-judge-system": {"eval-judge-system", "system",
		"你是评测评委。只输出 JSON: {\"score\":0~1,\"verdict\":\"PASS|FAIL|INSUFFICIENT_INFO\"}。若信息不足判断必须返回 INSUFFICIENT_INFO，不要猜测。"},
	"eval-judge-user": {"eval-judge-user", "user",
		"评测任务: {{task_id}}\n证据:{{evidence}}\nRCA:{{rca}}\n评分维度: {{rubric}}"},
	"kb-summarize": {"kb-summarize", "user",
		"用1-2句中文概括下列运维知识的核心处置/根因，不要扩写：\n{{content}}"},
	"kb-gen-questions": {"kb-gen-questions", "user",
		"基于下列运维知识，列出它能回答的2-3个用户提问，运维口吻，每行一问，不要编号：\n{{content}}"},
}

const (
	defaultTTL   = 60 * time.Second
	negativeTTL  = time.Minute // 拉取失败的负缓存窗口，期间不再请求 Phoenix
	warnInterval = time.Minute // 同名降级 warn 日志去重窗口
)

var (
	mu      sync.RWMutex
	fetcher *PhoenixFetcher // nil = 未配置，全走默认模板
	ttl     = defaultTTL
	cache   = map[string]cacheItem{}
	group   singleflight.Group
	warnAt  = map[string]time.Time{}
)

type cacheItem struct {
	text    string // 空 = 负缓存（拉取失败）
	expires time.Time
}

// Configure 注入 Phoenix 拉取器；f 为 nil 即关闭拉取并清缓存（测试用它复位）。
// d <= 0 时用默认 TTL。未调用本函数时 Render 永远走默认模板。
func Configure(f *PhoenixFetcher, d time.Duration) {
	mu.Lock()
	defer mu.Unlock()
	fetcher = f
	if d > 0 {
		ttl = d
	}
	cache = map[string]cacheItem{}
	warnAt = map[string]time.Time{}
}

// Render 取 name 对应生效模板并渲染 {{var}}；未注册 name 返回空串。
func Render(name string, vars map[string]string) string {
	e, ok := All[name]
	if !ok {
		return ""
	}
	return render(template(name), vars)
}

// template 返回生效模板：Phoenix 缓存优先，默认模板兜底。
// 首次同步惰性拉取；TTL 内命中缓存；过期且有旧值时后台刷新、先返回旧值。
func template(name string) string {
	e, ok := All[name]
	if !ok {
		return ""
	}
	mu.RLock()
	f := fetcher
	c, hit := cache[name]
	mu.RUnlock()
	if f == nil {
		return e.Default
	}
	now := time.Now()
	if hit && c.text != "" && now.Before(c.expires) {
		return c.text
	}
	if hit && c.text != "" { // 有旧值但已过期：后台刷新，先返回旧值
		go refresh(name)
		return c.text
	}
	refresh(name) // 首次或负缓存窗口内：同步尝试
	mu.RLock()
	c = cache[name]
	mu.RUnlock()
	if c.text != "" {
		return c.text
	}
	return e.Default
}

// refresh 拉取 name 并写缓存；成功写正缓存（TTL），失败写负缓存（negativeTTL）并记去重 warn。
// singleflight 合并同 name 并发拉取。
func refresh(name string) {
	e, ok := All[name]
	if !ok {
		return
	}
	group.Do(name, func() (any, error) {
		mu.RLock()
		c, hit := cache[name]
		mu.RUnlock()
		if hit && time.Now().Before(c.expires) {
			return nil, nil // 其他 goroutine 已刷新（含负缓存窗口内）
		}
		text, ok2 := fetcher.Fetch(name)
		mu.Lock()
		if ok2 {
			cache[name] = cacheItem{text: text, expires: time.Now().Add(ttl)}
		} else {
			cache[name] = cacheItem{text: "", expires: time.Now().Add(negativeTTL)}
			if t, seen := warnAt[name]; !seen || time.Since(t) > warnInterval {
				warnAt[name] = time.Now()
				log.Printf("[prompt] %s（%s）拉取失败，回退默认模板", name, e.Name)
			}
		}
		mu.Unlock()
		return nil, nil
	})
}

// render {{var}} 替换；模板中未出现的变量原样保留占位符，便于发现模板/变量不匹配。
func render(tpl string, vars map[string]string) string {
	if len(vars) == 0 {
		return tpl
	}
	pairs := make([]string, 0, len(vars)*2)
	for k, v := range vars {
		pairs = append(pairs, "{{"+k+"}}", v)
	}
	return strings.NewReplacer(pairs...).Replace(tpl)
}
```

- [ ] **Step 4: 确认测试通过**

Run: `go test ./internal/prompt/ -v`
Expected: 全部 PASS；`go vet ./...` 无告警

- [ ] **Step 5: Commit**

```bash
git add internal/prompt/registry.go internal/prompt/registry_test.go
git commit -m "feat: 提示词注册中心（14 条注册表 + Phoenix 拉取缓存降级）"
```

---

### Task 3: 集中 6 条常量改函数，接入 Registry

**Files:**
- Modify: `backend/internal/prompt/prompt.go`（整文件重写，常量 → 函数）
- Modify: `backend/internal/prompt/prompt_test.go`（5 处常量引用加 `()`）
- Modify: `backend/internal/agent/agent.go:401`、`backend/internal/context/assembler.go:65`、`backend/internal/pipeline/rca.go:68`、`backend/internal/pipeline/recommend.go:30`、`backend/internal/retrieve/query_rewrite.go:41`（各加 `()`）

**Interfaces:**
- Consumes: Task 2 的 `Render(name, vars)`。
- Produces（后续任务与既有调用方依赖）:
  - `func ChatAnswerSystem() string`
  - `func ChatAnswerUser(alertID, device, metric, rca, actions, contextText, question string) string`
  - `func KnowledgeIntro() string`
  - `func RCASystem() string`
  - `func RecommendSystem() string`
  - `func QueryRewriteInstruction(history, q string) string`

- [ ] **Step 1: 改测试（先失败）**

`prompt_test.go` 中 `ChatAnswerSystem`/`KnowledgeIntro`/`RCASystem`/`RecommendSystem` 常量改为函数调用。以 TestChatAnswerSystemContainsCoreRules 为例（其余 4 处同理加 `()`）：

```go
func TestChatAnswerSystemContainsCoreRules(t *testing.T) {
	for _, kw := range []string{"暂无相关信息", "禁止编造", "禁止使用自身知识补充", "算力中心硬件运维助手", "分点回答"} {
		if !strings.Contains(ChatAnswerSystem(), kw) {
			t.Fatalf("ChatAnswerSystem 应含 %q: %s", kw, ChatAnswerSystem())
		}
	}
}
```

`TestChatAnswerUserKeepsStructure`、`TestQueryRewriteInstructionKeepsEntities` 已是函数调用，不需改。新增一条「默认模板 == 注册表 Default」一致性断言（防 Task 2 的注册表文本与本包函数漂移）：

```go
func TestAccessorsUseRegistry(t *testing.T) {
	resetRegistry(t)
	if ChatAnswerSystem() != All["chat-answer-system"].Default {
		t.Fatal("ChatAnswerSystem 未走注册表默认模板")
	}
}
```

- [ ] **Step 2: 确认测试失败**

Run: `go test ./internal/prompt/ -v && go build ./...`
Expected: FAIL，`ChatAnswerSystem()` 调用无法编译（当前是常量）

- [ ] **Step 3: 重写 prompt.go**

整文件替换为（常量文本已移入 Task 2 的 `All`，本文件只剩访问函数）：

```go
// Package prompt 集中管理全项目提示词：内嵌默认模板 + Phoenix Prompt Hub 运行时拉取
// （spec 2026-09-09-phoenix-prompt-management-design.md）。
// 本包禁止 import 任何 aiops/internal/* 包，供 agent/context/pipeline/retrieve/eval/kb 单向引用。
package prompt

// ChatAnswerSystem answerWithLLM 系统提示（Phoenix name: chat-answer-system）。
func ChatAnswerSystem() string { return Render("chat-answer-system", nil) }

// KnowledgeIntro 组装结果 system 提示：知识使用规则（Phoenix name: knowledge-intro）。
func KnowledgeIntro() string { return Render("knowledge-intro", nil) }

// RCASystem rca 非 ReAct 分支系统提示（Phoenix name: rca-system）。
func RCASystem() string { return Render("rca-system", nil) }

// RecommendSystem 处置建议系统提示（Phoenix name: recommend-system）。
func RecommendSystem() string { return Render("recommend-system", nil) }

// ChatAnswerUser answerWithLLM 用户消息（Phoenix name: chat-answer-user）。
func ChatAnswerUser(alertID, device, metric, rca, actions, contextText, question string) string {
	return Render("chat-answer-user", map[string]string{
		"alert_id": alertID, "device": device, "metric": metric, "rca": rca,
		"actions": actions, "context": contextText, "question": question,
	})
}

// QueryRewriteInstruction 检索改写提示（Phoenix name: query-rewrite）。
func QueryRewriteInstruction(history, q string) string {
	return Render("query-rewrite", map[string]string{"history": history, "query": q})
}
```

同时更新 5 个调用点（均为加 `()`，纯机械改动）：

- `agent/agent.go:401`：`system := prompt.ChatAnswerSystem` → `system := prompt.ChatAnswerSystem()`
- `context/assembler.go:65`：`out.System = prompt.KnowledgeIntro` → `out.System = prompt.KnowledgeIntro()`
- `pipeline/rca.go:68`：`system := prompt.RCASystem` → `system := prompt.RCASystem()`
- `pipeline/recommend.go:30`：`system := prompt.RecommendSystem` → `system := prompt.RecommendSystem()`
- `retrieve/query_rewrite.go:41`：`prompt.QueryRewriteInstruction(...)` 已是函数调用，无需改（确认即可）

- [ ] **Step 4: 确认通过**

Run: `go build ./... && go test ./internal/prompt/ ./internal/agent/ ./internal/context/ ./internal/pipeline/ ./internal/retrieve/ -v`
Expected: 全部 PASS（现有测试验证默认模板 == 原文案，未接 Phoenix 行为不变）

- [ ] **Step 5: Commit**

```bash
git add internal/prompt/prompt.go internal/prompt/prompt_test.go internal/agent/agent.go internal/context/assembler.go internal/pipeline/rca.go internal/pipeline/recommend.go internal/retrieve/query_rewrite.go
git commit -m "refactor: 集中提示词常量改走注册中心函数（默认文案不变）"
```

---

### Task 4: 收编 agent 意图 2 条

**Files:**
- Modify: `backend/internal/prompt/prompt.go`（追加 2 个函数）
- Modify: `backend/internal/agent/intent.go:119,158`

**Interfaces:**
- Consumes: `Render`。
- Produces:
  - `func IntentRewriteSplitSystem() string`
  - `func IntentClassifySystem(candidates string) string` — candidates 为动态拼装的候选列表，由调用方 `strings.Join(parts, "、")` 传入

- [ ] **Step 1: 写失败测试**

追加到 `prompt_test.go`：

```go
func TestIntentClassifySystemRendersCandidates(t *testing.T) {
	resetRegistry(t)
	got := IntentClassifySystem("AskRootCause、AskAdvice")
	for _, kw := range []string{"意图分类器", "AskRootCause、AskAdvice", "Unknown"} {
		if !strings.Contains(got, kw) {
			t.Fatalf("IntentClassifySystem 应含 %q: %s", kw, got)
		}
	}
}
```

- [ ] **Step 2: 确认失败**

Run: `go test ./internal/prompt/ -run TestIntentClassify -v`
Expected: FAIL，`undefined: IntentClassifySystem`

- [ ] **Step 3: 实现**

追加到 `prompt.go`：

```go
// IntentRewriteSplitSystem 轻量 LLM 问题重写与多意图拆分系统提示（Phoenix name: intent-rewrite-split）。
func IntentRewriteSplitSystem() string { return Render("intent-rewrite-split", nil) }

// IntentClassifySystem 轻量 LLM 意图分类系统提示（Phoenix name: intent-classify）；
// candidates 为闭集候选列表（code+描述拼装），由 agent 侧生成。
func IntentClassifySystem(candidates string) string {
	return Render("intent-classify", map[string]string{"candidates": candidates})
}
```

改 `agent/intent.go`：

- 119 行：`system := "你是 AIOps 运维助手的问题理解模块。..."` 整段替换为 `system := prompt.IntentRewriteSplitSystem()`（确认文件已 import `"aiops/internal/prompt"`，无则添加）
- 158 行：`system := "你是意图分类器。..."` 整段替换为 `system := prompt.IntentClassifySystem(strings.Join(parts, "、"))`（`parts` 拼装逻辑原样保留）

- [ ] **Step 4: 确认通过**

Run: `go build ./... && go test ./internal/prompt/ ./internal/agent/ -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/prompt/prompt.go internal/prompt/prompt_test.go internal/agent/intent.go
git commit -m "refactor: 意图改写/分类提示词收编进 prompt 包"
```

---

### Task 5: 收编 RCA ReAct 与回溯 gap hint 2 条

**Files:**
- Modify: `backend/internal/prompt/prompt.go`（追加 2 个函数）
- Modify: `backend/internal/pipeline/rca.go:46,97-102`

**Interfaces:**
- Consumes: `Render`。
- Produces:
  - `func RCAReactSystem() string`
  - `func RCAGapHint(iteration int) string` — `iteration==0` 分支文案保留在 Go 侧（另一语义分支，不上 Phoenix，spec §4）

- [ ] **Step 1: 写失败测试**

追加到 `prompt_test.go`：

```go
func TestRCAGapHintBranches(t *testing.T) {
	resetRegistry(t)
	if got := RCAGapHint(0); got != "请基于当前证据给出根因结论。" {
		t.Fatalf("iteration=0 应走 Go 侧文案, got %q", got)
	}
	if got := RCAGapHint(2); got != "这是第2轮补充取证，请重点分析新增证据。" {
		t.Fatalf("iteration=2 应走模板, got %q", got)
	}
}
```

- [ ] **Step 2: 确认失败**

Run: `go test ./internal/prompt/ -run TestRCAGapHint -v`
Expected: FAIL，`undefined: RCAGapHint`

- [ ] **Step 3: 实现**

追加到 `prompt.go`：

```go
// RCAReactSystem RCA ReAct 分支系统提示（Phoenix name: rca-react-system）。
func RCAReactSystem() string { return Render("rca-react-system", nil) }

// RCAGapHint RCA 回溯轮次提示（Phoenix name: rca-gap-hint）。
// iteration==0 是「请给结论」语义分支，文案保留在 Go 侧，不上 Phoenix（spec §4）。
func RCAGapHint(iteration int) string {
	if iteration == 0 {
		return "请基于当前证据给出根因结论。"
	}
	return Render("rca-gap-hint", map[string]string{"iteration": fmt.Sprint(iteration)})
}
```

（`prompt.go` 需重新 `import "fmt"`。）

改 `pipeline/rca.go`：

- 46 行：`system := "你是算力中心硬件运维专家。基于上下文，..."` 整段替换为 `system := prompt.RCAReactSystem()`
- 78-81 行 user 拼接中的 `extraGapHint(st)` 改为 `prompt.RCAGapHint(st.Iteration)`
- 删除 `extraGapHint` 函数（97-102 行）

- [ ] **Step 4: 确认通过**

Run: `go build ./... && go test ./internal/prompt/ ./internal/pipeline/ -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/prompt/prompt.go internal/prompt/prompt_test.go internal/pipeline/rca.go
git commit -m "refactor: RCA ReAct 与回溯 gap hint 提示词收编进 prompt 包"
```

---

### Task 6: 收编 eval 评委 2 条

**Files:**
- Modify: `backend/internal/prompt/prompt.go`（追加 2 个函数）
- Modify: `backend/internal/eval/grader.go:216-218`

**Interfaces:**
- Consumes: `Render`。
- Produces:
  - `func EvalJudgeSystem() string`
  - `func EvalJudgeUser(taskID, evidence, rca, rubric string) string`

- [ ] **Step 1: 写失败测试**

追加到 `prompt_test.go`：

```go
func TestEvalJudgeUserRenders(t *testing.T) {
	resetRegistry(t)
	got := EvalJudgeUser("t-1", "EV", "RCA", "根因是否准确")
	for _, kw := range []string{"评测任务: t-1", "证据:EV", "RCA:RCA", "评分维度: 根因是否准确"} {
		if !strings.Contains(got, kw) {
			t.Fatalf("EvalJudgeUser 应含 %q: %s", kw, got)
		}
	}
}
```

- [ ] **Step 2: 确认失败**

Run: `go test ./internal/prompt/ -run TestEvalJudgeUser -v`
Expected: FAIL，`undefined: EvalJudgeUser`

- [ ] **Step 3: 实现**

追加到 `prompt.go`：

```go
// EvalJudgeSystem 评测 LLM 评委系统提示（Phoenix name: eval-judge-system）。
func EvalJudgeSystem() string { return Render("eval-judge-system", nil) }

// EvalJudgeUser 评测 LLM 评委用户消息（Phoenix name: eval-judge-user）。
func EvalJudgeUser(taskID, evidence, rca, rubric string) string {
	return Render("eval-judge-user", map[string]string{
		"task_id": taskID, "evidence": evidence, "rca": rca, "rubric": rubric,
	})
}
```

改 `eval/grader.go` 216-218 行：

```go
system := prompt.EvalJudgeSystem()
user := prompt.EvalJudgeUser(t.ID, mustJSON(res.Outcome.Evidence), mustJSON(res.Outcome.RCA), d.rubric)
```

（确认文件已 import `"aiops/internal/prompt"`，无则添加。）

- [ ] **Step 4: 确认通过**

Run: `go build ./... && go test ./internal/prompt/ ./internal/eval/ -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/prompt/prompt.go internal/prompt/prompt_test.go internal/eval/grader.go
git commit -m "refactor: 评测评委提示词收编进 prompt 包"
```

---

### Task 7: 收编 KB 富化 2 条

**Files:**
- Modify: `backend/internal/prompt/prompt.go`（追加 2 个函数）
- Modify: `backend/internal/kb/enhance.go:37,56`

**Interfaces:**
- Consumes: `Render`。
- Produces:
  - `func KBSummarizePrompt(content string) string`
  - `func KBGenQuestionsPrompt(content string) string`

注意：`kb/enhance.go` 现有局部变量名 `prompt` 与包名冲突，import 必须用别名 `aiprompt "aiops/internal/prompt"`。

- [ ] **Step 1: 写失败测试**

追加到 `prompt_test.go`：

```go
func TestKBPromptsRender(t *testing.T) {
	resetRegistry(t)
	if got := KBSummarizePrompt("某设备处置手册"); !strings.Contains(got, "1-2句") || !strings.Contains(got, "某设备处置手册") {
		t.Fatalf("KBSummarizePrompt = %q", got)
	}
	if got := KBGenQuestionsPrompt("某设备处置手册"); !strings.Contains(got, "2-3个") || !strings.Contains(got, "某设备处置手册") {
		t.Fatalf("KBGenQuestionsPrompt = %q", got)
	}
}
```

- [ ] **Step 2: 确认失败**

Run: `go test ./internal/prompt/ -run TestKBPrompts -v`
Expected: FAIL，`undefined: KBSummarizePrompt`

- [ ] **Step 3: 实现**

追加到 `prompt.go`：

```go
// KBSummarizePrompt 知识库富化摘要提示（Phoenix name: kb-summarize）。
func KBSummarizePrompt(content string) string {
	return Render("kb-summarize", map[string]string{"content": content})
}

// KBGenQuestionsPrompt 知识库富化出题提示（Phoenix name: kb-gen-questions）。
func KBGenQuestionsPrompt(content string) string {
	return Render("kb-gen-questions", map[string]string{"content": content})
}
```

改 `kb/enhance.go`：

- import 块添加：`aiprompt "aiops/internal/prompt"`
- 37 行：`prompt := "用1-2句中文概括...` 与 38 行 `out, err := lc.Complete(ctx2, prompt)` 改为：

```go
p := aiprompt.KBSummarizePrompt(chunk)
out, err := lc.Complete(ctx2, p)
```

- 56 行：`prompt := "基于下列运维知识，...` 与 57 行 `out, err := lc.Complete(ctx2, prompt)` 改为：

```go
p := aiprompt.KBGenQuestionsPrompt(chunk)
out, err := lc.Complete(ctx2, p)
```

- [ ] **Step 4: 确认通过**

Run: `go build ./... && go test ./internal/prompt/ ./internal/kb/ -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/prompt/prompt.go internal/prompt/prompt_test.go internal/kb/enhance.go
git commit -m "refactor: 知识库富化提示词收编进 prompt 包"
```

---

### Task 8: 配置项 + server 接线 + README/.env.example

**Files:**
- Modify: `backend/internal/config/config.go`（结构体 4 字段 + `LoadFromEnv` 4 段读取 + 返回值 4 行）
- Modify: `backend/cmd/server/main.go`（cfg 加载后接线 `prompt.Configure`）
- Modify: `README.md`（环境变量表格新增 Phoenix 分组）
- Modify: `.env.example`（追加 4 行）
- Test: `backend/internal/config/config_test.go`（如已有该文件则追加用例；没有则新建）

**Interfaces:**
- Consumes: Task 1/2 的 `prompt.NewPhoenixFetcher` / `prompt.Configure`。
- Produces:
  - `config.Config` 新字段：`PhoenixBaseURL string`、`PhoenixTag string`、`PhoenixTTL int`、`PhoenixAPIKey string`
  - main 启动时：`PHOENIX_BASE_URL` 非空 → `prompt.Configure(...)`；为空 → 不调用（全走默认模板）

- [ ] **Step 1: 写失败测试**

在 `backend/internal/config/config_test.go` 追加（无该文件则新建，package config）：

```go
func TestPhoenixConfig(t *testing.T) {
	cfg := LoadFromEnv(func(k string) string {
		m := map[string]string{
			"PHOENIX_BASE_URL":  "http://localhost:6006",
			"PHOENIX_PROMPT_TAG": "staging",
			"PHOENIX_PROMPT_TTL": "30",
			"PHOENIX_API_KEY":   "sk-p",
		}
		return m[k]
	})
	if cfg.PhoenixBaseURL != "http://localhost:6006" || cfg.PhoenixTag != "staging" ||
		cfg.PhoenixTTL != 30 || cfg.PhoenixAPIKey != "sk-p" {
		t.Fatalf("Phoenix 配置读取错误: %+v", cfg)
	}
}

func TestPhoenixConfigDefaults(t *testing.T) {
	cfg := Default()
	if cfg.PhoenixBaseURL != "" {
		t.Fatal("PHOENIX_BASE_URL 默认应为空（关闭拉取）")
	}
	if cfg.PhoenixTag != "production" || cfg.PhoenixTTL != 60 {
		t.Fatalf("默认 tag/ttl 错误: %s/%d", cfg.PhoenixTag, cfg.PhoenixTTL)
	}
}
```

（若已有 config_test.go 且已有同名 helper，按现有文件风格融入。）

- [ ] **Step 2: 确认失败**

Run: `go test ./internal/config/ -run TestPhoenix -v`
Expected: FAIL，`cfg.PhoenixBaseURL undefined`

- [ ] **Step 3: 实现 config**

`config.go` 结构体追加（`KBCdtaThreshold` 之后）：

```go
	PhoenixBaseURL string  // PHOENIX_BASE_URL，为空即关闭 Phoenix 提示词拉取
	PhoenixTag     string  // PHOENIX_PROMPT_TAG，拉取 tag，默认 production
	PhoenixTTL     int     // PHOENIX_PROMPT_TTL，缓存刷新周期秒数，默认 60
	PhoenixAPIKey  string  // PHOENIX_API_KEY，非空时带 Bearer 头
```

`LoadFromEnv` 在 `kbCdtaThreshold` 段之后追加：

```go
	phoenixBase := lookup("PHOENIX_BASE_URL")
	phoenixTag := lookup("PHOENIX_PROMPT_TAG")
	if phoenixTag == "" {
		phoenixTag = "production"
	}
	phoenixTTL := 60
	if v := lookup("PHOENIX_PROMPT_TTL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			phoenixTTL = n
		}
	}
	phoenixAPIKey := lookup("PHOENIX_API_KEY")
```

返回值结构体追加：

```go
		PhoenixBaseURL:      phoenixBase,
		PhoenixTag:          phoenixTag,
		PhoenixTTL:          phoenixTTL,
		PhoenixAPIKey:       phoenixAPIKey,
```

- [ ] **Step 4: main.go 接线**

`cmd/server/main.go`：`cfg := config.Load()` 之后、`llm.NewClient` 之前插入：

```go
	if cfg.PhoenixBaseURL != "" {
		prompt.Configure(prompt.NewPhoenixFetcher(cfg.PhoenixBaseURL, cfg.PhoenixTag, cfg.PhoenixAPIKey),
			time.Duration(cfg.PhoenixTTL)*time.Second)
		log.Printf("phoenix prompt hub: %s (tag=%s, ttl=%ds)", cfg.PhoenixBaseURL, cfg.PhoenixTag, cfg.PhoenixTTL)
	}
```

import 块添加 `"time"` 与 `"aiops/internal/prompt"`。

- [ ] **Step 5: README 与 .env.example**

README 环境变量表格在「检索」分组之后新增一组（保持表格既有列格式）：

```markdown
| `PHOENIX_BASE_URL` | 空（关闭拉取） | Phoenix 服务地址，如 `http://localhost:6006` |
| `PHOENIX_PROMPT_TAG` | `production` | 提示词拉取 tag |
| `PHOENIX_PROMPT_TTL` | `60` | 提示词缓存刷新周期（秒） |
| `PHOENIX_API_KEY` | 空 | Phoenix 鉴权 key，非空时带 Bearer 头 |
```

`.env.example` 末尾追加：

```
# PHOENIX_BASE_URL=http://localhost:6006
# PHOENIX_PROMPT_TAG=production
# PHOENIX_PROMPT_TTL=60
# PHOENIX_API_KEY=
```

- [ ] **Step 6: 确认通过**

Run: `go test ./internal/config/ -run TestPhoenix -v && go build ./... && go test ./...`
Expected: 全部 PASS

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go cmd/server/main.go ../README.md ../.env.example
git commit -m "feat: Phoenix 提示词拉取配置项与 server 接线"
```

---

### Task 9: cmd/promptctl 灌库与核对命令

**Files:**
- Create: `backend/cmd/promptctl/main.go`

**Interfaces:**
- Consumes: Task 1 `PhoenixFetcher.Push/List`、Task 2 `prompt.All`（Name/Role/Default）、Task 8 `config` 的 Phoenix 字段。
- Produces: CLI `promptctl push`（14 条默认模板灌库）/ `promptctl list`（核对远端）。无导出 API。

- [ ] **Step 1: 实现 main.go**

```go
// promptctl 本地默认提示词与 Phoenix Prompt Hub 的灌库/核对工具。
// 用法：promptctl push  把 14 条默认模板灌到 Phoenix（已存在的 name 新建版本）
//
//	promptctl list  列出远端 prompt 核对灌库结果
package main

import (
	"fmt"
	"log"
	"os"
	"sort"

	"aiops/internal/config"
	"aiops/internal/prompt"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("用法：promptctl push|list")
	}
	cfg := config.Load()
	if cfg.PhoenixBaseURL == "" {
		log.Fatal("PHOENIX_BASE_URL 未配置")
	}
	f := prompt.NewPhoenixFetcher(cfg.PhoenixBaseURL, cfg.PhoenixTag, cfg.PhoenixAPIKey)
	switch os.Args[1] {
	case "push":
		names := make([]string, 0, len(prompt.All))
		for n := range prompt.All {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			e := prompt.All[n]
			if err := f.Push(n, e.Role, e.Default, cfg.LLMModel); err != nil {
				log.Printf("push %s 失败: %v", n, err)
				continue
			}
			fmt.Println("pushed:", n)
		}
	case "list":
		ps, err := f.List()
		if err != nil {
			log.Fatal(err)
		}
		for _, p := range ps {
			fmt.Printf("%s\t%s\n", p.Name, p.ID)
		}
	default:
		log.Fatalf("未知子命令 %q", os.Args[1])
	}
}
```

- [ ] **Step 2: 确认通过**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: 全部 PASS（push/list 的 HTTP 行为已在 Task 1 的 Push/List 测试覆盖；本命令为薄封装）

- [ ] **Step 3: 冒烟验证（Phoenix 可用时）**

Run: `go run ./cmd/promptctl list`（连用户已部署的 Phoenix，需 `.env` 配 `PHOENIX_BASE_URL`）
Expected: 列出远端 prompt（空库输出为空）；`go run ./cmd/promptctl push` 后再 `list` 可见 14 条。若 Phoenix 不可用，本步骤记录「待 Phoenix 可用后补验」，不阻塞。

- [ ] **Step 4: Commit**

```bash
git add cmd/promptctl/main.go
git commit -m "feat: promptctl 灌库与核对命令（push/list）"
```

---

### Task 10: docker-compose 增加 phoenix 服务

**Files:**
- Modify: `docker-compose.yml`

**Interfaces:**
- Consumes: Task 8 的 `PHOENIX_BASE_URL` 约定。
- Produces: 本地 `phoenix` 服务（6006 端口），backend 默认指向它。

- [ ] **Step 1: 修改 docker-compose.yml**

在 `services:` 下新增 `phoenix`，并给 backend 加环境变量与依赖：

```yaml
services:
  backend:
    build:
      context: ./backend
    env_file: .env
    environment:
      - DB_PATH=/data/aiops.db
      - PHOENIX_BASE_URL=http://phoenix:6006
    volumes:
      - ./data:/data
    ports:
      - "8080:8080"
    depends_on:
      - phoenix
    restart: unless-stopped

  phoenix:
    image: arizephoenix/phoenix:latest
    ports:
      - "6006:6006"
    volumes:
      - ./data/phoenix:/mnt/data
    restart: unless-stopped

  frontend:
    build:
      context: ./frontend
    ports:
      - "3000:80"
    depends_on:
      - backend
    restart: unless-stopped
```

- [ ] **Step 2: 校验**

Run（仓库根目录）: `docker compose config --quiet`
Expected: 无输出（配置合法）。若本机无 docker daemon，记录「待有 daemon 时补验」，不阻塞。

- [ ] **Step 3: Commit**

```bash
git add docker-compose.yml
git commit -m "chore: compose 增加 phoenix 服务并接入 backend"
```

---

## 收尾

- [ ] 全量回归：`cd backend && go test ./... && go vet ./...`；如改到前端依赖面（本计划不应触及），`cd frontend && npm run build` 兜底
- [ ] 对照 spec 自查：14 条注册表齐全、降级链三条路径各有测试、README 表格含 4 个新变量、`.env.example` 同步
- [ ] 更新 AGENTS.md「其余核心模块」小节补一行 `internal/prompt`（Phoenix 提示词注册中心）说明
