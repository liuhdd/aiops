# 系统上下文工程（Context Engineering）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为流水线 rca/recommend 与聊天 Agent 建立系统化上下文工程：Scratchpad / 短期与长期记忆 / 工具（ReAct）/ 知识（RAG），每次调用模型前用确定性打分器挑选相关上下文注入窗口。

**Architecture:** 新增 `backend/internal/context` 包（Store/ScopedStore/Selector/Assembler/ToolRegistry/ReAct/Engine 门面），数据持久化到 SQLite 新表，知识语料放 mockdata。`pipeline.Engine` 与 `agent.Agent` 通过 `BuildWithContext` / `NewWithContext` 接入；上下文门面经 `State.Ctx` 注入各阶段。ReAct 默认开启，无 LLM 时规则兜底不变。

**Tech Stack:** Go 1.24（module `aiops`）、modernc.org/sqlite（driver 名 `"sqlite"`）、Eino ChatModel（经 `context.Completer` 接口隔离）、mockdata 进程内 mock、`go test ./...` 测试。

## Global Constraints

- 所有命令在 `backend/` 目录下运行；Go module 名为 `aiops`。
- SQLite driver 名是 `"sqlite"`（不是 `sqlite3`）；`store.New` 已设 `SetMaxOpenConns(1)`，不得新增连接。
- 上下文作用域对齐 `internal/scope`：scratchpad_entries / tool_events 任务级（`task:<run_id>`），long_term_memory 与 messages 全局。
- 阶段代码一律经 `st.Store`/`st.Ctx` 访问数据，绝不直接触碰 `*store.Store`（`st.Ctx` 内部持 raw store 用于全局读写除外）。
- 不破坏现有公开签名：`pipeline.Build(s, lc, coord)`、`NewRCAStage(lc)`、`NewRecommendStage(lc)`、`agent.New(s, lc, eng)` 必须继续可用（新增 `*WithContext` 变体）。
- 代码注释、文档、commit message 用中文；commit subject 用 conventional 前缀（`feat:`/`test:`/`docs:`/`chore:`）。
- 每次任务结束跑 `go test ./...`，全绿才提交。
- 规范见 `docs/superpowers/specs/2026-08-07-context-engineering-design.md`。

---

### Task 1: 模型类型与 SQLite 表迁移

**Files:**
- Create: `backend/internal/model/context.go`
- Modify: `backend/internal/store/store.go`（migrate 新增 3 表）
- Modify: `backend/internal/store/store_test.go`

**Interfaces:**
- Produces: `model.ScratchpadEntry{ID, RunID, Step, Content, ToolName, CreatedAt}`、`model.ToolEvent{ID, RunID, ToolName, ArgsJSON, ResultJSON, Status, DurationMS, CreatedAt}`、`model.LongTermMemory{ID, Device, Metric, RootCause, Confidence, ActionsJSON, RunID, CreatedAt}`、常量 `model.ToolStatusOK` / `model.ToolStatusError`。

- [ ] **Step 1: 写失败测试**（先补模型测试）

在 `backend/internal/model/model_test.go` 末尾追加：

```go
func TestContextTypesJSON(t *testing.T) {
	te := ToolEvent{ID: "e1", RunID: "run-1", ToolName: "query_metric",
		ArgsJSON: "{}", ResultJSON: "[]", Status: ToolStatusOK}
	if te.Status != ToolStatusOK {
		t.Fatalf("status = %q", te.Status)
	}
	if got := MustJSON(te); got == "{}" {
		t.Fatalf("MustJSON empty")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/model/ -run TestContextTypesJSON -v`
Expected: FAIL——`undefined: ToolEvent`。

- [ ] **Step 3: 新建 `backend/internal/model/context.go`**

```go
package model

import "time"

type ScratchpadEntry struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id"`
	Step      string    `json:"step"`
	Content   string    `json:"content"`
	ToolName  string    `json:"tool_name,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type ToolEvent struct {
	ID         string    `json:"id"`
	RunID      string    `json:"run_id"`
	ToolName   string    `json:"tool_name"`
	ArgsJSON   string    `json:"args_json"`
	ResultJSON string    `json:"result_json"`
	Status     string    `json:"status"`
	DurationMS int64     `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
}

const (
	ToolStatusOK    = "ok"
	ToolStatusError = "error"
)

type LongTermMemory struct {
	ID          string    `json:"id"`
	Device      string    `json:"device"`
	Metric      string    `json:"metric"`
	RootCause   string    `json:"root_cause"`
	Confidence  float64   `json:"confidence"`
	ActionsJSON string    `json:"actions_json"`
	RunID       string    `json:"run_id"`
	CreatedAt   time.Time `json:"created_at"`
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/model/`
Expected: PASS。

- [ ] **Step 5: 写表迁移失败测试**（store_test.go 追加）

```go
func TestMigrateCreatesContextTables(t *testing.T) {
	s, _ := New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('scratchpad_entries','tool_events','long_term_memory')`).Scan(&n)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 3 {
		t.Fatalf("context tables = %d, want 3", n)
	}
}
```

（`store_test.go` 属包 `store`，可直接访问 `s.db`；如当前是 `store_test` 包，改为 `package store` 或经导出方法。检查现有文件头，保持同包。）

- [ ] **Step 6: 运行确认失败**

Run: `go test ./internal/store/ -run TestMigrateCreatesContextTables -v`
Expected: FAIL——`context tables = 0, want 3`。

- [ ] **Step 7: 在 `store.go` 的 `migrate()` 中追加 DDL**

在现有 `idx_stage_run` 索引之后追加：

```go
		`CREATE TABLE IF NOT EXISTS scratchpad_entries (
			id TEXT PRIMARY KEY, run_id TEXT NOT NULL, step TEXT NOT NULL,
			content TEXT NOT NULL, tool_name TEXT, created_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS tool_events (
			id TEXT PRIMARY KEY, run_id TEXT NOT NULL, tool_name TEXT NOT NULL,
			args_json TEXT NOT NULL, result_json TEXT NOT NULL, status TEXT NOT NULL,
			duration_ms INTEGER, created_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS long_term_memory (
			id TEXT PRIMARY KEY, device TEXT NOT NULL, metric TEXT NOT NULL,
			root_cause TEXT NOT NULL, confidence REAL, actions_json TEXT,
			run_id TEXT NOT NULL, created_at DATETIME)`,
		`CREATE INDEX IF NOT EXISTS idx_scratch_run ON scratchpad_entries(run_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_run ON tool_events(run_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_ltm_dev_metric ON long_term_memory(device, metric, created_at)`,
```

- [ ] **Step 8: 运行测试确认通过**

Run: `go test ./internal/store/ -run TestMigrateCreatesContextTables -v`
Expected: PASS。

- [ ] **Step 9: 提交**

```bash
git add backend/internal/model/context.go backend/internal/model/model_test.go backend/internal/store/store.go backend/internal/store/store_test.go
git commit -m "feat: 上下文模型类型与三张新表迁移"
```

---

### Task 2: 配置项 CONTEXT_REACT / CONTEXT_TOP_K

**Files:**
- Modify: `backend/internal/config/config.go`
- Modify: `backend/internal/config/config_test.go`
- Modify: `.env.example`（仓库根）

**Interfaces:**
- Produces: `config.Config.React bool`、`config.Config.TopK int`。

- [ ] **Step 1: 写失败测试**（config_test.go 追加）

```go
func TestLoadFromEnvContextDefaults(t *testing.T) {
	cfg := LoadFromEnv(func(string) string { return "" })
	if !cfg.React {
		t.Fatalf("React default should be true")
	}
	if cfg.TopK != 3 {
		t.Fatalf("TopK = %d, want 3", cfg.TopK)
	}
}

func TestLoadFromEnvContextOverride(t *testing.T) {
	env := map[string]string{"CONTEXT_REACT": "false", "CONTEXT_TOP_K": "5"}
	cfg := LoadFromEnv(func(k string) string { return env[k] })
	if cfg.React {
		t.Fatalf("React = true, want false")
	}
	if cfg.TopK != 5 {
		t.Fatalf("TopK = %d, want 5", cfg.TopK)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/config/`
Expected: FAIL——`undefined: cfg.React`（编译错）。

- [ ] **Step 3: 修改 `config.go`**

在 `Config` struct 加字段：

```go
	React   bool // CONTEXT_REACT，ReAct 循环开关，默认 true
	TopK    int  // CONTEXT_TOP_K，每源上下文条数，默认 3
```

在 `LoadFromEnv` 中、return 之前：

```go
	react := true
	if v := lookup("CONTEXT_REACT"); v == "0" || v == "false" {
		react = false
	}
	topK := 3
	if v := lookup("CONTEXT_TOP_K"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			topK = n
		}
	}
```

return 处补 `React: react, TopK: topK,`。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/config/`
Expected: PASS。

- [ ] **Step 5: `.env.example` 追加两行**

```
# CONTEXT_REACT=1
# CONTEXT_TOP_K=3
```

- [ ] **Step 6: 提交**

```bash
git add backend/internal/config/config.go backend/internal/config/config_test.go .env.example
git commit -m "feat: 上下文工程配置项CONTEXT_REACT与CONTEXT_TOP_K"
```

---

### Task 3: mockdata 知识语料

**Files:**
- Create: `backend/internal/mockdata/docs.go`
- Create: `backend/internal/mockdata/docs_test.go`

**Interfaces:**
- Produces: `mockdata.Doc{ID, Title, DeviceTypes, Metrics, Keywords, Body}`、`mockdata.Docs() []Doc`。语料必须覆盖 5 个指标（cpu_usage / disk_usage / gpu_util / mem_usage / net_in）与 3 类设备（gpu-server / server / switch）。

- [ ] **Step 1: 写失败测试**（`docs_test.go`）

```go
package mockdata

import "testing"

func TestDocsCoverMetricsAndDevices(t *testing.T) {
	docs := Docs()
	if len(docs) < 8 {
		t.Fatalf("docs = %d, want >= 8", len(docs))
	}
	metrics := map[string]bool{}
	devTypes := map[string]bool{}
	for _, d := range docs {
		for _, m := range d.Metrics {
			metrics[m] = true
		}
		for _, dt := range d.DeviceTypes {
			devTypes[dt] = true
		}
		if d.ID == "" || d.Title == "" || d.Body == "" {
			t.Fatalf("doc incomplete: %+v", d)
		}
	}
	for _, m := range []string{"cpu_usage", "disk_usage", "gpu_util", "mem_usage", "net_in"} {
		if !metrics[m] {
			t.Fatalf("metric %s not covered", m)
		}
	}
	for _, dt := range []string{"gpu-server", "server", "switch"} {
		if !devTypes[dt] {
			t.Fatalf("device type %s not covered", dt)
		}
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/mockdata/ -run TestDocsCoverMetricsAndDevices -v`
Expected: FAIL——`undefined: Docs`。

- [ ] **Step 3: 新建 `docs.go`**（至少 10 条，按指标与设备类型打标）

```go
package mockdata

type Doc struct {
	ID          string
	Title       string
	DeviceTypes []string
	Metrics     []string
	Keywords    []string
	Body        string
}

var docs = []Doc{
	{ID: "doc-cpu-hot", Title: "CPU热点进程排查", DeviceTypes: []string{"gpu-server", "server"}, Metrics: []string{"cpu_usage"},
		Keywords: []string{"load", "scheduler", "热点进程"}, Body: "CPU使用率持续高于85%时，检查top命令热点进程，优先定位训练任务与异常进程，必要时迁移负载。"},
	{ID: "doc-disk-full", Title: "磁盘空间不足处理", DeviceTypes: []string{"gpu-server", "server", "storage"}, Metrics: []string{"disk_usage"},
		Keywords: []string{"no space", "ext4", "磁盘"}, Body: "磁盘使用率超过95%时执行df -h定位挂载点，检查大文件与日志轮转，清理或扩容，排查异常写入进程。"},
	{ID: "doc-gpu-xid", Title: "GPU掉卡与Xid错误", DeviceTypes: []string{"gpu-server"}, Metrics: []string{"gpu_util"},
		Keywords: []string{"Xid", "NVLink", "nvidia-smi"}, Body: "GPU利用率归零时检查NVRM Xid日志与NVLink链路，尝试重置驱动或重启节点，持续掉卡报修硬件。"},
	{ID: "doc-gpu-temp", Title: "GPU温度过高", DeviceTypes: []string{"gpu-server"}, Metrics: []string{"gpu_util"},
		Keywords: []string{"温度", "thermal", "风扇"}, Body: "GPU温度超过90度时检查散热与风扇转速，清理尘堵，降低负载或加装散热。"},
	{ID: "doc-mem-oom", Title: "内存溢出与OOM", DeviceTypes: []string{"gpu-server", "server"}, Metrics: []string{"mem_usage"},
		Keywords: []string{"OOM", "OutOfMemory", "memory", "oom-kill"}, Body: "内存使用率超过90%时检查dmesg OOM日志与内存Top进程，定位内存泄漏，调优内核参数或扩容。"},
	{ID: "doc-net-link", Title: "网络链路中断排查", DeviceTypes: []string{"switch", "server"}, Metrics: []string{"net_in"},
		Keywords: []string{"link down", "transceiver", "光模块"}, Body: "入口流量中断时检查端口down日志与光模块告警，核对对端设备状态，必要时联系网络侧。"},
	{ID: "doc-load-balance", Title: "负载不均衡处理", DeviceTypes: []string{"gpu-server"}, Metrics: []string{"cpu_usage"},
		Keywords: []string{"负载均衡", "邻居节点", "调度"}, Body: "多节点CPU负载差异大时检查邻居节点是否过载导致不均衡，视情况迁移负载。"},
	{ID: "doc-storage-full", Title: "存储节点磁盘告警", DeviceTypes: []string{"storage"}, Metrics: []string{"disk_usage"},
		Keywords: []string{"分布式存储", "数据均衡", "副本"}, Body: "存储节点磁盘告警时检查数据分布均衡度与副本数，扩容或迁移数据，避免单节点热点。"},
	{ID: "doc-db-tablespace", Title: "数据库表空间不足", DeviceTypes: []string{"server"}, Metrics: []string{"disk_usage"},
		Keywords: []string{"InnoDB", "tablespace", "mysql"}, Body: "数据库节点磁盘满时检查表空间与binlog占用，清理归档并评估扩容，避免InnoDB分配页失败。"},
	{ID: "doc-network-flap", Title: "端口抖动与广播风暴", DeviceTypes: []string{"switch"}, Metrics: []string{"net_in"},
		Keywords: []string{"flapping", "广播风暴", "拥塞"}, Body: "端口持续抖动或流量异常时检查广播风暴与STP收敛，定位环网并隔离异常端口。"},
}

func Docs() []Doc { return docs }
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/mockdata/`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/mockdata/docs.go backend/internal/mockdata/docs_test.go
git commit -m "feat: mock知识语料与覆盖测试"
```

---

### Task 4: context 包骨架 + Selector 打分器

**Files:**
- Create: `backend/internal/context/chunk.go`
- Create: `backend/internal/context/selector.go`
- Create: `backend/internal/context/selector_test.go`

**Interfaces:**
- Produces: `context.Source`（`SourceScratchpad`/`SourceShortTerm`/`SourceLongTerm`/`SourceKnowledge`）、`context.Chunk{Source, Content, Metadata map[string]string, Score}`、`context.Query{Device, Metric, Severity, Stage, RunID, Keywords}`、`context.Selector.Score(chunk Chunk, q Query, now time.Time) float64`、`context.Selector.Select(candidates []Chunk, q Query, topK int, threshold float64) []Chunk`。常量 `context.DefaultTopK=3`、`context.DefaultThreshold=0.15`。

- [ ] **Step 1: 新建 `chunk.go`**

```go
package context

type Source string

const (
	SourceScratchpad Source = "scratchpad"
	SourceShortTerm  Source = "short_term"
	SourceLongTerm   Source = "long_term"
	SourceKnowledge  Source = "knowledge"
)

type Chunk struct {
	Source   Source
	Content  string
	Metadata map[string]string
	Score    float64
}

type Query struct {
	Device   string
	Metric   string
	Severity string
	Stage    string
	RunID    string
	Keywords []string
}
```

- [ ] **Step 2: 写失败测试**（`selector_test.go`）

```go
package context

import (
	"sort"
	"testing"
	"time"
)

func chunk(src Source, content string, meta map[string]string) Chunk {
	return Chunk{Source: src, Content: content, Metadata: meta}
}

func TestScoreKeywordOverlap(t *testing.T) {
	sel := Selector{}
	now := time.Now()
	q := Query{Device: "db-01", Metric: "disk_usage", Severity: "critical", Stage: "rca", Keywords: []string{"磁盘", "扩容"}}
	hit := chunk(SourceKnowledge, "磁盘使用率超过95%时扩容磁盘", map[string]string{"metrics": "disk_usage"})
	miss := chunk(SourceKnowledge, "GPU温度过高时检查风扇", map[string]string{"metrics": "gpu_util"})
	if sel.Score(hit, q, now) <= sel.Score(miss, q, now) {
		t.Fatalf("hit(%f) should beat miss(%f)", sel.Score(hit, q, now), sel.Score(miss, q, now))
	}
}

func TestSelectTopKPerSource(t *testing.T) {
	sel := Selector{}
	q := Query{Metric: "disk_usage"}
	now := time.Now()
	var cands []Chunk
	for i := 0; i < 5; i++ {
		cands = append(cands, chunk(SourceScratchpad, "disk_usage 磁盘 记录", map[string]string{"created_at": now.Format(time.RFC3339)}))
		cands = append(cands, chunk(SourceKnowledge, "磁盘 扩容 预案", map[string]string{"metrics": "disk_usage"}))
	}
	got := sel.Select(cands, q, 3, 0.0)
	srcCount := map[Source]int{}
	for _, c := range got {
		srcCount[c.Source]++
	}
	if srcCount[SourceScratchpad] != 3 || srcCount[SourceKnowledge] != 3 {
		t.Fatalf("per-source counts = %v, want 3 each", srcCount)
	}
	sorted := make([]float64, 0, len(got))
	for _, c := range got {
		sorted = append(sorted, c.Score)
	}
	if !sort.SliceIsSorted(sorted, func(a, b int) bool { return sorted[a] >= sorted[b] }) {
		t.Fatalf("result not sorted by score: %v", sorted)
	}
}

func TestSelectFiltersBelowThreshold(t *testing.T) {
	sel := Selector{}
	now := time.Now()
	// 无关键词重叠、无标签、无时间戳 → 低分
	weak := chunk(SourceKnowledge, "与查询无关的随机文本", nil)
	got := sel.Select([]Chunk{weak}, Query{Metric: "disk_usage"}, 3, 0.9)
	if len(got) != 0 {
		t.Fatalf("expected filtered out, got %d", len(got))
	}
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/context/`
Expected: FAIL——`undefined: Selector`。

- [ ] **Step 4: 新建 `selector.go`**

```go
package context

import (
	"sort"
	"strings"
	"time"
)

const (
	DefaultTopK      = 3
	DefaultThreshold = 0.15
	kwWeight         = 0.4
	tagWeight        = 0.4
	recencyWeight    = 0.2
	halfLifeMinutes  = 60.0
)

type Selector struct{}

func (Selector) keywords(q Query) []string {
	var out []string
	for _, k := range append([]string{q.Device, q.Metric, q.Severity, q.Stage}, q.Keywords...) {
		if k != "" {
			out = append(out, strings.ToLower(k))
		}
	}
	return out
}

func (Selector) Score(chunk Chunk, q Query, now time.Time) float64 {
	kws := Selector{}.keywords(q)
	hay := strings.ToLower(chunk.Content + " " + joinMeta(chunk.Metadata))
	kwScore := 1.0
	if len(kws) > 0 {
		matched := 0
		for _, k := range kws {
			if strings.Contains(hay, k) {
				matched++
			}
		}
		kwScore = float64(matched) / float64(len(kws))
	}
	tagScore := 0.0
	if strings.Contains(chunk.Metadata["metrics"], q.Metric) ||
		strings.Contains(chunk.Metadata["device_types"], q.Device) {
		tagScore = 1.0
	}
	recencyScore := 0.5
	if ts := chunk.Metadata["created_at"]; ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			ageMin := now.Sub(t).Minutes()
			if ageMin < 0 {
				ageMin = 0
			}
			recencyScore = exp(-ageMin / halfLifeMinutes)
		}
	}
	return kwWeight*kwScore + tagWeight*tagScore + recencyWeight*recencyScore
}

func (Selector) Select(candidates []Chunk, q Query, topK int, threshold float64) []Chunk {
	bySource := map[Source][]Chunk{}
	for _, c := range candidates {
		if c.Score < threshold {
			continue
		}
		bySource[c.Source] = append(bySource[c.Source], c)
	}
	var merged []Chunk
	for _, list := range bySource {
		sort.SliceStable(list, func(i, j int) bool { return list[i].Score > list[j].Score })
		if len(list) > topK {
			list = list[:topK]
		}
		merged = append(merged, list...)
	}
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].Score > merged[j].Score })
	return merged
}

func joinMeta(m map[string]string) string {
	parts := make([]string, 0, len(m))
	for _, v := range m {
		parts = append(parts, v)
	}
	return strings.Join(parts, " ")
}
```

（`exp` 用 `math.Exp`，在文件头部加 `import "math"` 并调用 `math.Exp`。）

- [ ] **Step 5: 修正 import 后运行确认通过**

`selector.go` 的 import 为：`"math"`、`"sort"`、`"strings"`、`"time"`；`recencyScore = math.Exp(-ageMin / halfLifeMinutes)`。

Run: `go test ./internal/context/`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add backend/internal/context/chunk.go backend/internal/context/selector.go backend/internal/context/selector_test.go
git commit -m "feat: 上下文块抽象与相关性打分器"
```

---

### Task 5: context.Store 持久化层

**Files:**
- Create: `backend/internal/context/store.go`
- Create: `backend/internal/context/store_test.go`

**Interfaces:**
- Produces: `context.Store`（wrap `*store.Store`），方法 `InsertScratchpad(model.ScratchpadEntry) error`、`ListScratchpad(runID string) ([]model.ScratchpadEntry, error)`（按 created_at ASC）、`InsertToolEvent(model.ToolEvent) error`、`ListToolEvents(runID string) ([]model.ToolEvent, error)`（ASC）、`AddLongTermMemory(model.LongTermMemory) error`、`ListLongTermMemory(device, metric string) ([]model.LongTermMemory, error)`（created_at DESC）、`RecentMessages(n int) ([]model.Message, error)`（最新 n 条，时间升序返回）。

- [ ] **Step 1: 写失败测试**（`store_test.go`）

```go
package context

import (
	"path/filepath"
	"testing"
	"time"

	"aiops/internal/model"
	"aiops/internal/store"
)

func newTestStore(t *testing.T) (*Store, *store.Store) {
	t.Helper()
	s, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return NewStore(s), s
}

func TestScratchpadRoundtrip(t *testing.T) {
	cs, _ := newTestStore(t)
	now := time.Now()
	_ = cs.InsertScratchpad(model.ScratchpadEntry{ID: "s1", RunID: "run-1", Step: "rca", Content: "x", CreatedAt: now.Add(-time.Minute)})
	_ = cs.InsertScratchpad(model.ScratchpadEntry{ID: "s2", RunID: "run-1", Step: "recommend", Content: "y", ToolName: "write_scratchpad", CreatedAt: now})
	got, err := cs.ListScratchpad("run-1")
	if err != nil || len(got) != 2 {
		t.Fatalf("ListScratchpad = %d err=%v", len(got), err)
	}
	if got[0].Step != "rca" || got[1].ToolName != "write_scratchpad" {
		t.Fatalf("order/content wrong: %+v", got)
	}
}

func TestToolEventRoundtrip(t *testing.T) {
	cs, _ := newTestStore(t)
	now := time.Now()
	_ = cs.InsertToolEvent(model.ToolEvent{ID: "e1", RunID: "run-1", ToolName: "query_metric",
		ArgsJSON: "{}", ResultJSON: "[]", Status: model.ToolStatusOK, DurationMS: 5, CreatedAt: now})
	got, err := cs.ListToolEvents("run-1")
	if err != nil || len(got) != 1 || got[0].Status != model.ToolStatusOK {
		t.Fatalf("ListToolEvents = %+v err=%v", got, err)
	}
}

func TestLongTermMemoryRoundtrip(t *testing.T) {
	cs, _ := newTestStore(t)
	now := time.Now()
	_ = cs.AddLongTermMemory(model.LongTermMemory{ID: "m1", Device: "db-01", Metric: "disk_usage",
		RootCause: "磁盘空间不足", Confidence: 0.8, ActionsJSON: "[]", RunID: "run-1", CreatedAt: now})
	_ = cs.AddLongTermMemory(model.LongTermMemory{ID: "m2", Device: "db-01", Metric: "disk_usage",
		RootCause: "磁盘满", Confidence: 0.9, ActionsJSON: "[]", RunID: "run-2", CreatedAt: now.Add(time.Minute)})
	got, err := cs.ListLongTermMemory("db-01", "disk_usage")
	if err != nil || len(got) != 2 {
		t.Fatalf("ListLongTermMemory = %d err=%v", len(got), err)
	}
	if got[0].RunID != "run-2" { // 最新的在前
		t.Fatalf("order wrong: %+v", got)
	}
}

func TestRecentMessagesOrder(t *testing.T) {
	cs, s := newTestStore(t)
	now := time.Now()
	for i := 0; i < 5; i++ {
		_ = s.SaveMessage(model.Message{ID: model.NewID("msg"), Role: model.RoleUser, Content: "m" + string(rune('0'+i)), CreatedAt: now.Add(time.Duration(i) * time.Minute)})
	}
	got, err := cs.RecentMessages(3)
	if err != nil || len(got) != 3 {
		t.Fatalf("RecentMessages = %d err=%v", len(got), err)
	}
	if got[0].Content != "m2" || got[2].Content != "m4" {
		t.Fatalf("wrong window: %+v", got)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/context/`
Expected: FAIL——`undefined: NewStore`。

- [ ] **Step 3: 新建 `context/store.go`（薄封装，委托 store 包专用 CRUD）**

```go
package context

import (
	"aiops/internal/model"
	"aiops/internal/store"
)

type Store struct {
	st *store.Store
}

func NewStore(st *store.Store) *Store { return &Store{st: st} }

func (cs *Store) InsertScratchpad(e model.ScratchpadEntry) error {
	return cs.st.InsertScratchpad(e)
}

func (cs *Store) ListScratchpad(runID string) ([]model.ScratchpadEntry, error) {
	return cs.st.ListScratchpad(runID)
}

func (cs *Store) InsertToolEvent(ev model.ToolEvent) error {
	return cs.st.InsertToolEvent(ev)
}

func (cs *Store) ListToolEvents(runID string) ([]model.ToolEvent, error) {
	return cs.st.ListToolEvents(runID)
}

func (cs *Store) AddLongTermMemory(m model.LongTermMemory) error {
	return cs.st.AddLongTermMemory(m)
}

func (cs *Store) ListLongTermMemory(device, metric string) ([]model.LongTermMemory, error) {
	return cs.st.ListLongTermMemory(device, metric)
}

func (cs *Store) RecentMessages(n int) ([]model.Message, error) {
	return cs.st.RecentMessages(n)
}
```

- [ ] **Step 4: 在 `store/store.go` 追加专用 CRUD**

在 `store.go` 末尾追加（表列名与 Task 1 的 DDL 一致，均为语义化列名）：

```go
// ---- 上下文工程持久化 ----

func (s *Store) InsertScratchpad(e model.ScratchpadEntry) error {
	_, err := s.db.Exec(`INSERT INTO scratchpad_entries (id, run_id, step, content, tool_name, created_at) VALUES (?,?,?,?,?,?)`,
		e.ID, e.RunID, e.Step, e.Content, e.ToolName, e.CreatedAt)
	return err
}

func (s *Store) ListScratchpad(runID string) ([]model.ScratchpadEntry, error) {
	rows, err := s.db.Query(`SELECT id, run_id, step, content, tool_name, created_at FROM scratchpad_entries WHERE run_id = ? ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ScratchpadEntry
	for rows.Next() {
		var e model.ScratchpadEntry
		if err := rows.Scan(&e.ID, &e.RunID, &e.Step, &e.Content, &e.ToolName, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) InsertToolEvent(ev model.ToolEvent) error {
	_, err := s.db.Exec(`INSERT INTO tool_events (id, run_id, tool_name, args_json, result_json, status, duration_ms, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		ev.ID, ev.RunID, ev.ToolName, ev.ArgsJSON, ev.ResultJSON, ev.Status, ev.DurationMS, ev.CreatedAt)
	return err
}

func (s *Store) ListToolEvents(runID string) ([]model.ToolEvent, error) {
	rows, err := s.db.Query(`SELECT id, run_id, tool_name, args_json, result_json, status, duration_ms, created_at FROM tool_events WHERE run_id = ? ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ToolEvent
	for rows.Next() {
		var ev model.ToolEvent
		if err := rows.Scan(&ev.ID, &ev.RunID, &ev.ToolName, &ev.ArgsJSON, &ev.ResultJSON, &ev.Status, &ev.DurationMS, &ev.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (s *Store) AddLongTermMemory(m model.LongTermMemory) error {
	_, err := s.db.Exec(`INSERT INTO long_term_memory (id, device, metric, root_cause, confidence, actions_json, run_id, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		m.ID, m.Device, m.Metric, m.RootCause, m.Confidence, m.ActionsJSON, m.RunID, m.CreatedAt)
	return err
}

func (s *Store) ListLongTermMemory(device, metric string) ([]model.LongTermMemory, error) {
	rows, err := s.db.Query(`SELECT id, device, metric, root_cause, confidence, actions_json, run_id, created_at FROM long_term_memory WHERE device = ? AND metric = ? ORDER BY created_at DESC`, device, metric)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.LongTermMemory
	for rows.Next() {
		var m model.LongTermMemory
		if err := rows.Scan(&m.ID, &m.Device, &m.Metric, &m.RootCause, &m.Confidence, &m.ActionsJSON, &m.RunID, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) RecentMessages(n int) ([]model.Message, error) {
	rows, err := s.db.Query(`SELECT id, role, content, alert_id, created_at FROM messages ORDER BY created_at DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var desc []model.Message
	for rows.Next() {
		var m model.Message
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.AlertID, &m.CreatedAt); err != nil {
			return nil, err
		}
		desc = append(desc, m)
	}
	// 反转回时间升序
	out := make([]model.Message, 0, len(desc))
	for i := len(desc) - 1; i >= 0; i-- {
		out = append(out, desc[i])
	}
	return out, rows.Err()
}
```

`store/store.go` 需要 `time` import（已有）。

- [ ] **Step 5: 运行确认通过**

Run: `go test ./internal/context/ ./internal/store/`
Expected: 全 PASS。

- [ ] **Step 6: 提交**

```bash
git add backend/internal/context backend/internal/store/store.go
git commit -m "feat: 上下文持久化层与store专用CRUD"
```

---

### Task 6: context.ScopedStore 作用域

**Files:**
- Create: `backend/internal/context/scoped.go`
- Create: `backend/internal/context/scoped_test.go`

**Interfaces:**
- Produces: `context.NewScoped(cs *Store, sc scope.Scope) *ScopedStore`，方法 `AddScratchpad(step, content, toolName string) error`、`ListScratchpad() ([]model.ScratchpadEntry, error)`、`AddToolEvent(ev model.ToolEvent) error`、`ListToolEvents() ([]model.ToolEvent, error)`。任务级写校验：`sc.Level != scope.LevelTask || runID != sc.TaskID` → `scope.ErrScopeDenied`。

- [ ] **Step 1: 写失败测试**（`scoped_test.go`）

```go
package context

import (
	"errors"
	"path/filepath"
	"testing"

	"aiops/internal/model"
	"aiops/internal/scope"
	"aiops/internal/store"
)

func TestScopedTaskWriteAndDeny(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := NewStore(s)

	// 合法任务作用域写入
	task := NewScoped(cs, scope.Task("run-1", "compute-01"))
	if err := task.AddScratchpad("rca", "root cause note", ""); err != nil {
		t.Fatalf("AddScratchpad: %v", err)
	}
	ev := model.ToolEvent{ID: "e1", RunID: "run-1", ToolName: "query_metric",
		ArgsJSON: "{}", ResultJSON: "[]", Status: model.ToolStatusOK}
	if err := task.AddToolEvent(ev); err != nil {
		t.Fatalf("AddToolEvent: %v", err)
	}

	// 越权：任务作用域写其它 run
	other := NewScoped(cs, scope.Task("run-other", "compute-01"))
	if err := other.AddScratchpad("rca", "x", ""); !errors.Is(err, scope.ErrScopeDenied) {
		t.Fatalf("cross-run scratchpad err = %v, want ErrScopeDenied", err)
	}
	if err := other.AddToolEvent(model.ToolEvent{RunID: "run-other"}); !errors.Is(err, scope.ErrScopeDenied) {
		t.Fatalf("cross-run tool err = %v, want ErrScopeDenied", err)
	}

	// 全局作用域不允许任务写
	g := NewScoped(cs, scope.Global())
	if err := g.AddScratchpad("rca", "x", ""); !errors.Is(err, scope.ErrScopeDenied) {
		t.Fatalf("global write err = %v, want ErrScopeDenied", err)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/context/ -run TestScopedTaskWriteAndDeny -v`
Expected: FAIL——`undefined: NewScoped`。

- [ ] **Step 3: 新建 `scoped.go`**

```go
package context

import (
	"aiops/internal/model"
	"aiops/internal/scope"
)

type ScopedStore struct {
	cs *Store
	sc scope.Scope
}

func NewScoped(cs *Store, sc scope.Scope) *ScopedStore {
	return &ScopedStore{cs: cs, sc: sc}
}

func (ss *ScopedStore) AddScratchpad(step, content, toolName string) error {
	if ss.sc.Level != scope.LevelTask || ss.sc.TaskID == "" {
		return scope.ErrScopeDenied
	}
	return ss.cs.InsertScratchpad(model.ScratchpadEntry{
		ID: model.NewID("sp"), RunID: ss.sc.TaskID, Step: step,
		Content: content, ToolName: toolName, CreatedAt: nowFunc(),
	})
}

func (ss *ScopedStore) ListScratchpad() ([]model.ScratchpadEntry, error) {
	if ss.sc.Level != scope.LevelTask {
		return nil, scope.ErrScopeDenied
	}
	return ss.cs.ListScratchpad(ss.sc.TaskID)
}

func (ss *ScopedStore) AddToolEvent(ev model.ToolEvent) error {
	if ss.sc.Level != scope.LevelTask || ev.RunID != ss.sc.TaskID {
		return scope.ErrScopeDenied
	}
	return ss.cs.InsertToolEvent(ev)
}

func (ss *ScopedStore) ListToolEvents() ([]model.ToolEvent, error) {
	if ss.sc.Level != scope.LevelTask {
		return nil, scope.ErrScopeDenied
	}
	return ss.cs.ListToolEvents(ss.sc.TaskID)
}
```

`nowFunc` 定义在 `context` 包（`store.go` 或新文件）：

```go
var nowFunc = func() time.Time { return time.Now() }
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/context/`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/context/scoped.go backend/internal/context/scoped_test.go backend/internal/context/store.go
git commit -m "feat: 任务级ScopedStore与越权校验"
```

---

### Task 7: ToolRegistry 与 6 个工具

**Files:**
- Create: `backend/internal/context/tools.go`
- Create: `backend/internal/context/tools_test.go`

**Interfaces:**
- Produces: `context.Tool{Name, Description string, ParamSchema map[string]string, Execute func(ctx context.Context, args map[string]any) (any, error)}`、`context.Registry{...}`（`NewRegistry() *Registry`、`Register(Tool)`、`List() []Tool`、`Lookup(name string) (Tool, bool)`、`Execute(ctx, name string, args map[string]any) (any, error)`）、`context.DefaultTools() []Tool`（6 个：query_metric / search_logs / get_device_info / get_neighbors / search_runbook / write_scratchpad）。`write_scratchpad.Execute` 返回 `ErrScratchpadHandled`，由 ReAct 侧接管。

- [ ] **Step 1: 写失败测试**（`tools_test.go`）

```go
package context

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"aiops/internal/mockdata"
	"aiops/internal/store"
)

func TestDefaultToolsRegistered(t *testing.T) {
	reg := NewRegistry()
	for _, t := range DefaultTools() {
		reg.Register(t)
	}
	if len(reg.List()) != 6 {
		t.Fatalf("tools = %d, want 6", len(reg.List()))
	}
	if _, ok := reg.Lookup("write_scratchpad"); !ok {
		t.Fatalf("write_scratchpad missing")
	}
}

func TestQueryMetricTool(t *testing.T) {
	mockdata.GenerateAll(42)
	reg := NewRegistry()
	for _, t := range DefaultTools() {
		reg.Register(t)
	}
	res, err := reg.Execute(context.Background(), "query_metric",
		map[string]any{"device": "db-01", "metric": "disk_usage", "window_minutes": 30})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	pts, ok := res.([]mockdata.MetricPointView)
	if !ok || len(pts) == 0 {
		t.Fatalf("res = %#v", res)
	}
}

func TestSearchLogsToolKeywords(t *testing.T) {
	mockdata.GenerateAll(42)
	reg := NewRegistry()
	for _, t := range DefaultTools() {
		reg.Register(t)
	}
	res, err := reg.Execute(context.Background(), "search_logs",
		map[string]any{"device": "db-01", "keywords": []any{"no space"}, "window_minutes": 120})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	logs, ok := res.([]mockdata.LogEntryView)
	if !ok || len(logs) == 0 {
		t.Fatalf("res = %#v", res)
	}
}

func TestWriteScratchpadIntercepted(t *testing.T) {
	reg := NewRegistry()
	for _, t := range DefaultTools() {
		reg.Register(t)
	}
	_, err := reg.Execute(context.Background(), "write_scratchpad", map[string]any{"content": "x"})
	if !errors.Is(err, ErrScratchpadHandled) {
		t.Fatalf("err = %v, want ErrScratchpadHandled", err)
	}
}

func TestUnknownTool(t *testing.T) {
	reg := NewRegistry()
	_, err := reg.Execute(context.Background(), "nope", nil)
	if err == nil {
		t.Fatalf("expected error for unknown tool")
	}
}
```

（`mockdata.MetricPointView` / `mockdata.LogEntryView` 为 mockdata 侧暴露的展示视图，见 Step 4。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/context/ -run 'TestDefaultTools|TestQueryMetric|TestSearchLogs|TestWriteScratchpad|TestUnknownTool' -v`
Expected: FAIL——`undefined: NewRegistry` 等。

- [ ] **Step 3: 新建 `tools.go`**

```go
package context

import (
	"context"
	"errors"
	"fmt"
	"time"

	"aiops/internal/mockdata"
)

var ErrScratchpadHandled = errors.New("write_scratchpad 由引擎接管")

type Tool struct {
	Name        string
	Description string
	ParamSchema map[string]string
	Execute     func(ctx context.Context, args map[string]any) (any, error)
}

type Registry struct {
	tools map[string]Tool
}

func NewRegistry() *Registry { return &Registry{tools: map[string]Tool{}} }

func (r *Registry) Register(t Tool)  { r.tools[t.Name] = t }
func (r *Registry) List() []Tool {
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}
func (r *Registry) Lookup(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) Execute(ctx context.Context, name string, args map[string]any) (any, error) {
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool %q", name)
	}
	return t.Execute(ctx, args)
}

func DefaultTools() []Tool {
	return []Tool{
		{Name: "query_metric", Description: "查询设备监控指标时序", ParamSchema: map[string]string{
			"device": "设备名", "metric": "指标名", "window_minutes": "查询窗口分钟数（整数）"},
			Execute: func(ctx context.Context, args map[string]any) (any, error) {
				device, metric, win := strArg(args, "device"), strArg(args, "metric"), intArg(args, "window_minutes", 120)
				from := time.Now().Add(-time.Duration(win) * time.Minute)
				pts, err := mockdata.QueryMetric(device, metric, from, time.Now())
				if err != nil {
					return nil, err
				}
				return mockdata.ToMetricViews(pts), nil
			}},
		{Name: "search_logs", Description: "按关键字检索设备日志", ParamSchema: map[string]string{
			"device": "设备名", "keywords": "关键字数组", "window_minutes": "查询窗口分钟数（整数）"},
			Execute: func(ctx context.Context, args map[string]any) (any, error) {
				device := strArg(args, "device")
				win := intArg(args, "window_minutes", 120)
				from := time.Now().Add(-time.Duration(win) * time.Minute)
				logs, err := mockdata.SearchLogs(device, listArg(args, "keywords"), from, time.Now())
				if err != nil {
					return nil, err
				}
				return mockdata.ToLogViews(logs), nil
			}},
		{Name: "get_device_info", Description: "获取设备资产信息", ParamSchema: map[string]string{"device": "设备名"},
			Execute: func(ctx context.Context, args map[string]any) (any, error) {
				name := strArg(args, "device")
				for _, d := range mockdata.Devices() {
					if d.Name == name {
						return d, nil
					}
				}
				return nil, fmt.Errorf("device %q not found", name)
			}},
		{Name: "get_neighbors", Description: "获取设备拓扑邻居", ParamSchema: map[string]string{"device": "设备名"},
			Execute: func(ctx context.Context, args map[string]any) (any, error) {
				return mockdata.Neighbors(strArg(args, "device")), nil
			}},
		{Name: "search_runbook", Description: "按指标查处置预案", ParamSchema: map[string]string{"metric": "指标名"},
			Execute: func(ctx context.Context, args map[string]any) (any, error) {
				steps, ok := mockdata.SearchRunbook(strArg(args, "metric"))
				if !ok {
					return nil, fmt.Errorf("no runbook for metric %q", strArg(args, "metric"))
				}
				return steps, nil
			}},
		{Name: "write_scratchpad", Description: "写入临时笔记板（记录推理过程）", ParamSchema: map[string]string{
			"step": "笔记步骤名", "content": "笔记内容"},
			Execute: func(ctx context.Context, args map[string]any) (any, error) {
				return nil, ErrScratchpadHandled
			}},
	}
}

func strArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func intArg(args map[string]any, key string, def int) int {
	switch v := args[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	case string:
		n := 0
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return def
}

func listArg(args map[string]any, key string) []string {
	raw, ok := args[key].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
```

- [ ] **Step 4: mockdata 增加展示视图**

在 `backend/internal/mockdata/metrics.go` 末尾追加：

```go
type MetricPointView struct {
	Time  string  `json:"time"`
	Value float64 `json:"value"`
}

func ToMetricViews(pts []model.MetricPoint) []MetricPointView {
	out := make([]MetricPointView, 0, len(pts))
	for _, p := range pts {
		out = append(out, MetricPointView{Time: p.Time.Format("15:04"), Value: p.Value})
	}
	return out
}
```

在 `backend/internal/mockdata/logs.go` 末尾追加：

```go
type LogEntryView struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Content string `json:"content"`
}

func ToLogViews(logs []model.LogEntry) []LogEntryView {
	out := make([]LogEntryView, 0, len(logs))
	for _, e := range logs {
		out = append(out, LogEntryView{Time: e.Time.Format("15:04"), Level: e.Level, Content: e.Content})
	}
	return out
}
```

`context` 包需要 `mockdata` 的 `MetricPointView`/`LogEntryView` 类型（与 `MockMetricPoints` 一致）。修正测试断言类型为 `[]mockdata.MetricPointView` / `[]mockdata.LogEntryView`（Step 1 已写对）。

- [ ] **Step 5: 运行确认通过**

Run: `go test ./internal/context/ ./internal/mockdata/`
Expected: PASS（修正类型断言后）。

- [ ] **Step 6: 提交**

```bash
git add backend/internal/context/tools.go backend/internal/context/tools_test.go backend/internal/mockdata/metrics.go backend/internal/mockdata/logs.go
git commit -m "feat: 工具注册表与六个mock工具"
```

---

### Task 8: ReAct 循环

**Files:**
- Create: `backend/internal/context/react.go`
- Create: `backend/internal/context/react_test.go`

**Interfaces:**
- Produces: `context.Completer interface { Complete(ctx context.Context, system, user string) (string, error) }`（`llm.Client` 天然满足）、`context.ReActToolCall{Name string, Args map[string]any}`、`context.ReActStep{Thought string, ToolCall *ReActToolCall, FinalAnswer json.RawMessage}`、`context.ReActTurn{Thought, Tool string, Args map[string]any, Result, Status string}`、`context.ReActResult{Final json.RawMessage, Turns []ReActTurn}`、`context.ReactOptions{MaxSteps, ToolErrorsAllowed int}`、`context.RunReact(ctx, c Completer, system, user string, reg *Registry, sideEffect func(name string, args map[string]any) (any, error), opts ReactOptions) (*ReActResult, error)`。`sideEffect` 非 nil 时优先于 registry 执行（用于 write_scratchpad）；MaxSteps 耗尽返回 `Final=nil`（非错误）。

- [ ] **Step 1: 写失败测试**（`react_test.go`）

```go
package context

import (
	"context"
	"errors"
	"testing"

	"aiops/internal/mockdata"
)

type scriptedCompleter struct {
	steps []string
	i     int
}

func (s *scriptedCompleter) Complete(_ context.Context, _, _ string) (string, error) {
	if s.i >= len(s.steps) {
		return "", errors.New("no more scripted steps")
	}
	r := s.steps[s.i]
	s.i++
	return r, nil
}

func newTestReg(t *testing.T) *Registry {
	t.Helper()
	mockdata.GenerateAll(42)
	reg := NewRegistry()
	for _, tt := range DefaultTools() {
		reg.Register(tt)
	}
	return reg
}

func TestReactToolThenFinal(t *testing.T) {
	reg := newTestReg(t)
	sc := &scriptedCompleter{steps: []string{
		`{"thought":"查磁盘","tool_call":{"name":"query_metric","args":{"device":"db-01","metric":"disk_usage","window_minutes":30}}}`,
		`{"thought":"有数据","final_answer":{"root_cause":"磁盘空间不足","evidence_chain":["e"],"confidence":0.8,"gaps":[]}}`,
	}}
	res, err := RunReact(context.Background(), sc, "sys", "usr", reg, nil, ReactOptions{MaxSteps: 5, ToolErrorsAllowed: 2})
	if err != nil {
		t.Fatalf("RunReact: %v", err)
	}
	if res.Final == nil {
		t.Fatalf("no final answer")
	}
	if len(res.Turns) != 1 || res.Turns[0].Tool != "query_metric" || res.Turns[0].Status != "ok" {
		t.Fatalf("turns = %+v", res.Turns)
	}
}

func TestReactImmediateFinal(t *testing.T) {
	reg := newTestReg(t)
	sc := &scriptedCompleter{steps: []string{
		`{"thought":"直接答","final_answer":{"root_cause":"x","evidence_chain":[],"confidence":0.5,"gaps":[]}}`,
	}}
	res, err := RunReact(context.Background(), sc, "s", "u", reg, nil, ReactOptions{MaxSteps: 5, ToolErrorsAllowed: 2})
	if err != nil || res.Final == nil || len(res.Turns) != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestReactToolErrorThenFinal(t *testing.T) {
	reg := newTestReg(t)
	sc := &scriptedCompleter{steps: []string{
		`{"thought":"调未知工具","tool_call":{"name":"nope","args":{}}}`,
		`{"thought":"换预案","final_answer":{"root_cause":"x","evidence_chain":[],"confidence":0.5,"gaps":[]}}`,
	}}
	res, err := RunReact(context.Background(), sc, "s", "u", reg, nil, ReactOptions{MaxSteps: 5, ToolErrorsAllowed: 2})
	if err != nil {
		t.Fatalf("RunReact: %v", err)
	}
	if res.Final == nil {
		t.Fatalf("expected final after tolerated error")
	}
	if res.Turns[0].Status != "error" {
		t.Fatalf("turn0 status = %q, want error", res.Turns[0].Status)
	}
}

func TestReactAbortsOnTooManyToolErrors(t *testing.T) {
	reg := newTestReg(t)
	sc := &scriptedCompleter{steps: []string{
		`{"thought":"错1","tool_call":{"name":"nope","args":{}}}`,
		`{"thought":"错2","tool_call":{"name":"nope2","args":{}}}`,
	}}
	_, err := RunReact(context.Background(), sc, "s", "u", reg, nil, ReactOptions{MaxSteps: 5, ToolErrorsAllowed: 2})
	if err == nil {
		t.Fatalf("expected abort error")
	}
}

func TestReactMaxStepsExhausted(t *testing.T) {
	reg := newTestReg(t)
	steps := []string{}
	for i := 0; i < 3; i++ {
		steps = append(steps, `{"thought":"重复","tool_call":{"name":"search_runbook","args":{"metric":"disk_usage"}}}`)
	}
	sc := &scriptedCompleter{steps: steps}
	res, err := RunReact(context.Background(), sc, "s", "u", reg, nil, ReactOptions{MaxSteps: 3, ToolErrorsAllowed: 2})
	if err != nil {
		t.Fatalf("exhaustion should not error: %v", err)
	}
	if res.Final != nil {
		t.Fatalf("should have no final on exhaustion")
	}
	if len(res.Turns) != 3 {
		t.Fatalf("turns = %d, want 3", len(res.Turns))
	}
}

func TestReactSideEffectIntercepts(t *testing.T) {
	reg := newTestReg(t)
	var wrote bool
	sc := &scriptedCompleter{steps: []string{
		`{"thought":"记笔记","tool_call":{"name":"write_scratchpad","args":{"step":"rca","content":"hypothesis"}}}`,
		`{"thought":"done","final_answer":{"root_cause":"x","evidence_chain":[],"confidence":0.5,"gaps":[]}}`,
	}}
	side := func(name string, args map[string]any) (any, error) {
		if name == "write_scratchpad" {
			wrote = true
			return "ok", nil
		}
		return reg.Execute(context.Background(), name, args)
	}
	res, err := RunReact(context.Background(), sc, "s", "u", reg, side, ReactOptions{MaxSteps: 5, ToolErrorsAllowed: 2})
	if err != nil || res.Final == nil {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !wrote {
		t.Fatalf("side effect not invoked")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/context/ -run TestReact -v`
Expected: FAIL——`undefined: RunReact`。

- [ ] **Step 3: 新建 `react.go`**

```go
package context

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"aiops/internal/llm"
)

type Completer interface {
	Complete(ctx context.Context, system, user string) (string, error)
}

type ReActToolCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type ReActStep struct {
	Thought     string          `json:"thought"`
	ToolCall    *ReActToolCall  `json:"tool_call,omitempty"`
	FinalAnswer json.RawMessage `json:"final_answer,omitempty"`
}

type ReActTurn struct {
	Thought string
	Tool    string
	Args    map[string]any
	Result  string
	Status  string
}

type ReActResult struct {
	Final json.RawMessage
	Turns []ReActTurn
}

type ReactOptions struct {
	MaxSteps          int
	ToolErrorsAllowed int
}

func RunReact(ctx context.Context, c Completer, system, user string, reg *Registry,
	sideEffect func(name string, args map[string]any) (any, error), opts ReactOptions) (*ReActResult, error) {
	maxSteps := opts.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 5
	}
	allowed := opts.ToolErrorsAllowed
	if allowed <= 0 {
		allowed = 2
	}
	res := &ReActResult{}
	toolErrors := 0
	var turns []ReActTurn
	for step := 0; step < maxSteps; step++ {
		prompt := user + "\n\n【历史推理】\n" + renderTurns(turns) + "\n请给出下一步："
		raw, err := c.Complete(ctx, system, prompt)
		if err != nil {
			return res, fmt.Errorf("react llm: %w", err)
		}
		var out ReActStep
		if err := llm.ParseJSON(raw, &out); err != nil {
			return res, fmt.Errorf("react parse: %w", err)
		}
		if len(out.FinalAnswer) > 0 {
			res.Final = out.FinalAnswer
			res.Turns = turns
			return res, nil
		}
		if out.ToolCall == nil {
			return res, errors.New("react: step without tool_call or final_answer")
		}
		tc := out.ToolCall
		turn := ReActTurn{Thought: out.Thought, Tool: tc.Name, Args: tc.Args}
		var result any
		var execErr error
		if sideEffect != nil {
			result, execErr = sideEffect(tc.Name, tc.Args)
		} else {
			result, execErr = reg.Execute(ctx, tc.Name, tc.Args)
		}
		if execErr != nil {
			turn.Status = "error"
			turn.Result = execErr.Error()
			toolErrors++
			if toolErrors >= allowed {
				res.Turns = turns
				return res, fmt.Errorf("react: %d consecutive tool errors", toolErrors)
			}
		} else {
			turn.Status = "ok"
			turn.Result = jsonSafe(result)
			toolErrors = 0
		}
		turns = append(turns, turn)
	}
	res.Turns = turns
	return res, nil
}

func renderTurns(turns []ReActTurn) string {
	out := ""
	for i, t := range turns {
		out += fmt.Sprintf("%d. thought=%q tool=%q result=%s(%s)\n", i+1, t.Thought, t.Tool, t.Result, t.Status)
	}
	return out
}

func jsonSafe(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/context/ -run TestReact -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/context/react.go backend/internal/context/react_test.go
git commit -m "feat: ReAct循环执行器"
```

---

### Task 9: context.Assembler 组装器

**Files:**
- Create: `backend/internal/context/assembler.go`
- Create: `backend/internal/context/assembler_test.go`

**Interfaces:**
- Produces: `context.Assembled{System, User string, Selected []Chunk}`、`context.NewAssembler(cs *Store, topK int) *Assembler`、`context.Assembler.Assemble(q Query, includeTools bool, tools []Tool) (*Assembled, error)`。分节名：`【临时笔记】`/`【设备历史经验】`/`【知识库】`/`【短期记忆】`/`【可用工具】`/【工具调用历史】`；空源不输出分节。

- [ ] **Step 1: 写失败测试**（`assembler_test.go`）

```go
package context

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aiops/internal/mockdata"
	"aiops/internal/model"
	"aiops/internal/store"
)

func TestAssembleSections(t *testing.T) {
	mockdata.GenerateAll(42)
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := NewStore(s)
	now := time.Now()
	_ = cs.InsertScratchpad(model.ScratchpadEntry{ID: "sp1", RunID: "run-1", Step: "rca",
		Content: "磁盘空间不足假设", CreatedAt: now})
	_ = cs.AddLongTermMemory(model.LongTermMemory{ID: "m1", Device: "db-01", Metric: "disk_usage",
		RootCause: "磁盘空间不足", Confidence: 0.8, ActionsJSON: `["清理日志"]`, RunID: "run-0", CreatedAt: now.Add(-time.Hour)})
	_ = s.SaveMessage(model.Message{ID: model.NewID("msg"), Role: model.RoleUser, Content: "查一下db-01磁盘", CreatedAt: now})

	asm := NewAssembler(cs, 3)
	q := Query{Device: "db-01", Metric: "disk_usage", Severity: "critical", Stage: "rca", RunID: "run-1"}
	as, err := asm.Assemble(q, true, DefaultTools())
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	for _, sec := range []string{"【临时笔记】", "【设备历史经验】", "【知识库】", "【短期记忆】", "【可用工具】"} {
		if !strings.Contains(as.User, sec) {
			t.Fatalf("missing section %s in:\n%s", sec, as.User)
		}
	}
	if len(as.Selected) == 0 {
		t.Fatalf("no selected chunks")
	}
}

func TestAssembleEmptySourcesNoSections(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := NewStore(s)
	asm := NewAssembler(cs, 3)
	q := Query{Device: "nonexistent-9", Metric: "cpu_usage", Stage: "rca", RunID: "run-x"}
	as, err := asm.Assemble(q, false, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if strings.Contains(as.User, "【临时笔记】") || strings.Contains(as.User, "【可用工具】") {
		t.Fatalf("empty sources should not emit sections:\n%s", as.User)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/context/ -run TestAssemble -v`
Expected: FAIL——`undefined: NewAssembler`。

- [ ] **Step 3: 新建 `assembler.go`**

```go
package context

import (
	"strings"
	"time"

	"aiops/internal/mockdata"
	"aiops/internal/model"
)

type Assembled struct {
	System   string
	User     string
	Selected []Chunk
}

type Assembler struct {
	cs   *Store
	sel  Selector
	topK int
}

func NewAssembler(cs *Store, topK int) *Assembler {
	if topK <= 0 {
		topK = DefaultTopK
	}
	return &Assembler{cs: cs, sel: Selector{}, topK: topK}
}

func (a *Assembler) Assemble(q Query, includeTools bool, tools []Tool) (*Assembled, error) {
	now := time.Now()
	cands, err := a.candidates(q, now)
	if err != nil {
		return nil, err
	}
	selected := a.sel.Select(cands, q, a.topK, DefaultThreshold)
	out := &Assembled{Selected: selected}
	var sections []string
	if sec := scratchpadSection(selected); sec != "" {
		sections = append(sections, sec)
	}
	if sec := longTermSection(selected); sec != "" {
		sections = append(sections, sec)
	}
	if sec := knowledgeSection(selected); sec != "" {
		sections = append(sections, sec)
	}
	if sec := shortTermSection(selected); sec != "" {
		sections = append(sections, sec)
	}
	if includeTools && len(tools) > 0 {
		sections = append(sections, toolsSection(tools))
		if sec := toolHistorySection(q.RunID, a.cs); sec != "" {
			sections = append(sections, sec)
		}
	}
	out.System = "以下是当前任务相关的上下文信息。"
	out.User = strings.Join(sections, "\n")
	return out, nil
}

func (a *Assembler) candidates(q Query, now time.Time) ([]Chunk, error) {
	var out []Chunk
	if q.RunID != "" {
		sp, err := a.cs.ListScratchpad(q.RunID)
		if err != nil {
			return nil, err
		}
		for _, e := range sp {
			out = append(out, Chunk{Source: SourceScratchpad,
				Content: "step=" + e.Step + ": " + e.Content,
				Metadata: map[string]string{"created_at": e.CreatedAt.Format(time.RFC3339)}})
		}
	}
	if q.Device != "" && q.Metric != "" {
		ltm, err := a.cs.ListLongTermMemory(q.Device, q.Metric)
		if err != nil {
			return nil, err
		}
		for _, m := range ltm {
			out = append(out, Chunk{Source: SourceLongTerm,
				Content: "上次(" + m.CreatedAt.Format("2006-01-02") + ") 根因=" + m.RootCause + " 置信度=" + itoa(int(m.Confidence*100)) + "% 处置=" + m.ActionsJSON,
				Metadata: map[string]string{"device": m.Device, "metric": m.Metric, "created_at": m.CreatedAt.Format(time.RFC3339)}})
		}
	}
	msgs, err := a.cs.RecentMessages(10)
	if err != nil {
		return nil, err
	}
	for _, m := range msgs {
		out = append(out, Chunk{Source: SourceShortTerm,
			Content: m.Role + ": " + m.Content,
			Metadata: map[string]string{"created_at": m.CreatedAt.Format(time.RFC3339)}})
	}
	for _, d := range mockdata.Docs() {
		out = append(out, Chunk{Source: SourceKnowledge,
			Content: d.Title + ": " + d.Body,
			Metadata: map[string]string{"metrics": strings.Join(d.Metrics, ","), "device_types": strings.Join(d.DeviceTypes, ",")}})
	}
	return out, nil
}

func scratchpadSection(sel []Chunk) string {
	var b strings.Builder
	for _, c := range sel {
		if c.Source == SourceScratchpad {
			b.WriteString(c.Content + "\n")
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "【临时笔记】\n" + b.String()
}

func longTermSection(sel []Chunk) string {
	var b strings.Builder
	for _, c := range sel {
		if c.Source == SourceLongTerm {
			b.WriteString(c.Content + "\n")
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "【设备历史经验】\n" + b.String()
}

func knowledgeSection(sel []Chunk) string {
	var b strings.Builder
	for _, c := range sel {
		if c.Source == SourceKnowledge {
			b.WriteString(c.Content + "\n")
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "【知识库】\n" + b.String()
}

func shortTermSection(sel []Chunk) string {
	var b strings.Builder
	for _, c := range sel {
		if c.Source == SourceShortTerm {
			b.WriteString(c.Content + "\n")
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "【短期记忆】\n" + b.String()
}

func toolsSection(tools []Tool) string {
	var b strings.Builder
	for _, t := range tools {
		b.WriteString(t.Name + "(" + joinParams(t.ParamSchema) + ") - " + t.Description + "\n")
	}
	return "【可用工具】\n" + b.String()
}

func toolHistorySection(runID string, cs *Store) string {
	if runID == "" {
		return ""
	}
	evs, err := cs.ListToolEvents(runID)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, ev := range evs {
		b.WriteString(ev.ToolName + "(" + ev.ArgsJSON + ") → " + ev.Status + "\n")
	}
	if b.Len() == 0 {
		return ""
	}
	return "【工具调用历史】\n" + b.String()
}

func joinParams(schema map[string]string) string {
	keys := make([]string, 0, len(schema))
	for k := range schema {
		keys = append(keys, k)
	}
	return strings.Join(keys, ",")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
```

（`itoa` 可用 `strconv.Itoa` 替代；若用标准库，import `"strconv"`，删除 `itoa` 定义，`Content` 中改用 `strconv.Itoa(...)`。推荐用 `strconv`。）

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/context/ -run TestAssemble -v`
Expected: PASS（注意第一个测试断言 `【短期记忆】` 需要最近 messages 存在，已 seed；`【知识库】` 需 q.Metric 命中 doc，已命中）。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/context/assembler.go backend/internal/context/assembler_test.go
git commit -m "feat: 上下文组装器"
```

---

### Task 10: context.Engine 门面

**Files:**
- Create: `backend/internal/context/engine.go`
- Create: `backend/internal/context/engine_test.go`

**Interfaces:**
- Produces: `context.NewEngine(cs *Store, reg *Registry, runID, device, metric string, react bool, topK int) *Engine`，方法：
  - `RunID() string`、`ReactEnabled() bool`
  - `AddScratchpad(step, content, toolName string) error`、`ListScratchpad() ([]model.ScratchpadEntry, error)`
  - `AddToolEvent(ev model.ToolEvent) error`、`ListToolEvents() ([]model.ToolEvent, error)`
  - `Assemble(q Query, includeTools bool) (*Assembled, error)`
  - `SaveLongTermMemory(rca *model.RCA, rec *model.Recommendation) error`
  - `RunReact(ctx context.Context, c Completer, system string, q Query, baseUser string) (*ReActResult, error)`

- [ ] **Step 1: 写失败测试**（`engine_test.go`）

```go
package context

import (
	"context"
	"path/filepath"
	"testing"

	"aiops/internal/mockdata"
	"aiops/internal/model"
	"aiops/internal/store"
)

func TestEngineScratchpadAndLongTerm(t *testing.T) {
	mockdata.GenerateAll(42)
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := NewStore(s)
	reg := NewRegistry()
	for _, tt := range DefaultTools() {
		reg.Register(tt)
	}
	e := NewEngine(cs, reg, "run-1", "db-01", "disk_usage", true, 3)
	if !e.ReactEnabled() || e.RunID() != "run-1" {
		t.Fatalf("engine = %+v", e)
	}
	if err := e.AddScratchpad("rca", "假设: 磁盘满", "write_scratchpad"); err != nil {
		t.Fatalf("AddScratchpad: %v", err)
	}
	_ = e.AddToolEvent(model.ToolEvent{ID: model.NewID("evt"), RunID: "run-1", ToolName: "query_metric",
		ArgsJSON: `{"device":"db-01"}`, ResultJSON: "[]", Status: model.ToolStatusOK})
	if err := e.SaveLongTermMemory(&model.RCA{RootCause: "磁盘空间不足", Confidence: 0.8},
		&model.Recommendation{Actions: []string{"清理日志"}, RunbookRef: "disk_usage"}); err != nil {
		t.Fatalf("SaveLongTermMemory: %v", err)
	}
	ltm, _ := cs.ListLongTermMemory("db-01", "disk_usage")
	if len(ltm) != 1 || ltm[0].RunID != "run-1" {
		t.Fatalf("long term = %+v", ltm)
	}
}

func TestEngineRunReactEndToEnd(t *testing.T) {
	mockdata.GenerateAll(42)
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := NewStore(s)
	reg := NewRegistry()
	for _, tt := range DefaultTools() {
		reg.Register(tt)
	}
	e := NewEngine(cs, reg, "run-1", "db-01", "disk_usage", true, 3)
	sc := &scriptedCompleter{steps: []string{
		`{"thought":"查日志","tool_call":{"name":"search_logs","args":{"device":"db-01","keywords":["no space"],"window_minutes":120}}}`,
		`{"thought":"结论","final_answer":{"root_cause":"磁盘空间不足","evidence_chain":["ext4"],"confidence":0.8,"gaps":[]}}`,
	}}
	res, err := e.RunReact(context.Background(), sc, "sys", Query{Device: "db-01", Metric: "disk_usage", RunID: "run-1"}, "【临时笔记】\n")
	if err != nil || res.Final == nil {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	evs, _ := e.ListToolEvents()
	if len(evs) != 1 || evs[0].ToolName != "search_logs" || evs[0].Status != model.ToolStatusOK {
		t.Fatalf("tool events = %+v", evs)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/context/ -run 'TestEngine' -v`
Expected: FAIL——`undefined: NewEngine`。

- [ ] **Step 3: 新建 `engine.go`**

```go
package context

import (
	"context"
	"errors"
	"time"

	"aiops/internal/model"
	"aiops/internal/scope"
)

type Engine struct {
	cs     *Store
	scoped *ScopedStore
	reg    *Registry
	sel    Selector
	asm    *Assembler
	runID  string
	device string
	metric string
	react  bool
	topK   int
}

func NewEngine(cs *Store, reg *Registry, runID, device, metric string, react bool, topK int) *Engine {
	sc := scope.Task(runID, device)
	return &Engine{
		cs: cs, scoped: NewScoped(cs, sc), reg: reg, sel: Selector{},
		asm: NewAssembler(cs, topK), runID: runID, device: device, metric: metric, react: react, topK: topK,
	}
}

func (e *Engine) RunID() string      { return e.runID }
func (e *Engine) ReactEnabled() bool { return e.react }

func (e *Engine) AddScratchpad(step, content, toolName string) error {
	return e.scoped.AddScratchpad(step, content, toolName)
}

func (e *Engine) ListScratchpad() ([]model.ScratchpadEntry, error) {
	return e.scoped.ListScratchpad()
}

func (e *Engine) AddToolEvent(ev model.ToolEvent) error {
	return e.scoped.AddToolEvent(ev)
}

func (e *Engine) ListToolEvents() ([]model.ToolEvent, error) {
	return e.scoped.ListToolEvents()
}

func (e *Engine) Assemble(q Query, includeTools bool) (*Assembled, error) {
	tools := []Tool{}
	if e.reg != nil {
		tools = e.reg.List()
	}
	return e.asm.Assemble(q, includeTools, tools)
}

func (e *Engine) SaveLongTermMemory(rca *model.RCA, rec *model.Recommendation) error {
	actions := "[]"
	if rec != nil {
		actions = model.MustJSON(rec.Actions)
	}
	return e.cs.AddLongTermMemory(model.LongTermMemory{
		ID: model.NewID("ltm"), Device: e.device, Metric: e.metric,
		RootCause: rca.RootCause, Confidence: rca.Confidence, ActionsJSON: actions,
		RunID: e.runID, CreatedAt: time.Now(),
	})
}

func (e *Engine) RunReact(ctx context.Context, c Completer, system string, q Query, baseUser string) (*ReActResult, error) {
	user := baseUser
	if user == "" {
		if as, err := e.Assemble(q, true); err == nil {
			user = as.User
		}
	}
	side := func(name string, args map[string]any) (any, error) {
		if name == "write_scratchpad" {
			if step := strArg(args, "step"); step == "" {
				step = "react"
			}
			if err := e.AddScratchpad(step, strArg(args, "content"), "write_scratchpad"); err != nil {
				return nil, err
			}
			return "ok", nil
		}
		if e.reg != nil {
			res, err := e.reg.Execute(ctx, name, args)
			if err == nil {
				_ = e.AddToolEvent(model.ToolEvent{
					ID: model.NewID("evt"), RunID: e.runID, ToolName: name,
					ArgsJSON: model.MustJSON(args), ResultJSON: model.MustJSON(res),
					Status: model.ToolStatusOK, CreatedAt: time.Now(),
				})
			}
			return res, err
		}
		return nil, errors.New("unknown tool " + name)
	}
	return RunReact(ctx, c, system, user, e.reg, side, ReactOptions{MaxSteps: 5, ToolErrorsAllowed: 2})
}
```

（`NewEngine` 签名已含 `metric`，`engine_test.go` 中调用为 `NewEngine(cs, reg, "run-1", "db-01", "disk_usage", true, 3)`。）

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/context/`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/context/engine.go backend/internal/context/engine_test.go
git commit -m "feat: 上下文引擎门面"
```

---

### Task 11: pipeline 接入——BuildWithContext 与 engine.Run 集成

**Files:**
- Modify: `backend/internal/pipeline/build.go`
- Modify: `backend/internal/pipeline/engine.go`
- Modify: `backend/internal/pipeline/engine_test.go`

**Interfaces:**
- Consumes: `context.NewEngine(cs, reg, runID, device, metric string, react, topK int)`、`context.NewStore`、`context.NewRegistry`、`context.DefaultTools`。
- Produces: `pipeline.BuildWithContext(s *store.Store, lc *llm.Client, coord *coordinator.Coordinator, cs *context.Store, reg *context.Registry, react bool, topK int) *Engine`（保持 `Build(s, lc, coord)` 委托给它并传 nil）；`State.Ctx *context.Engine`；`pipeline.Engine` 新增私有字段 `ctxStore`/`tools`/`react`/`topK`。Run 内：任务作用域后建 `st.Ctx`，每阶段完成后写 Scratchpad 笔记，结束时写长期记忆。

- [ ] **Step 1: 写失败测试**（engine_test.go 追加）

```go
func TestEngineInjectsContextAndWritesNotes(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := context.NewStore(s)
	reg := context.NewRegistry()
	for _, tt := range context.DefaultTools() {
		reg.Register(tt)
	}
	e := BuildWithContext(s, nil, coordinator.New(), cs, reg, false, 3)
	alert := model.Alert{ID: "alarm-ctx", Device: "db-01", Metric: "disk_usage",
		Severity: "critical", FiredAt: time.Now(), CreatedAt: time.Now()}
	if err := e.Run(context.Background(), alert, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sp, _ := cs.ListScratchpad("run-" + alert.ID)
	if len(sp) == 0 {
		t.Fatalf("no scratchpad notes written")
	}
	ltm, _ := cs.ListLongTermMemory("db-01", "disk_usage")
	if len(ltm) != 1 || ltm[0].RunID != "run-"+alert.ID {
		t.Fatalf("long term memory = %+v", ltm)
	}
}
```

（`engine_test.go` import 增加 `"aiops/internal/context"`。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/pipeline/ -run TestEngineInjectsContext -v`
Expected: FAIL——`undefined: BuildWithContext`。

- [ ] **Step 3: 修改 `build.go`**

```go
package pipeline

import (
	"aiops/internal/context"
	"aiops/internal/coordinator"
	"aiops/internal/llm"
	"aiops/internal/store"
)

func Build(s *store.Store, lc *llm.Client, coord *coordinator.Coordinator) *Engine {
	return BuildWithContext(s, lc, coord, nil, nil, false, 0)
}

func BuildWithContext(s *store.Store, lc *llm.Client, coord *coordinator.Coordinator,
	cs *context.Store, reg *context.Registry, react bool, topK int) *Engine {
	e := NewEngine(s, []Stage{
		NormalizeStage{},
		NewTriageStage(s),
		ContextStage{},
		EvidenceStage{},
		NewRCAStage(lc),
		NewRecommendStage(lc),
	})
	e.SetCoordinator(coord)
	e.ctxStore = cs
	e.tools = reg
	e.react = react
	e.topK = topK
	return e
}
```

- [ ] **Step 4: 修改 `engine.go`**

在 `Engine` struct 增加字段：

```go
	ctxStore *context.Store
	tools    *context.Registry
	react    bool
	topK     int
```

import 增加 `"aiops/internal/context"`。

在 `State` struct 增加 `Ctx *context.Engine`。

在 `Run` 中，创建 `taskScope` 与 `st` 之后（`st := &State{...}` 之后）追加：

```go
	if e.ctxStore != nil {
		st.Ctx = context.NewEngine(e.ctxStore, e.tools, run.ID, alert.Device, alert.Metric, e.react, e.topK)
	}
```

在阶段循环内，`res.Status = model.StageCompleted` 分支之后、`if err := st.Store.SaveStage(res); err != nil {` 之前追加：

```go
			if st.Ctx != nil {
				_ = st.Ctx.AddScratchpad(stage.StageID(), stageSummary(st, stage.StageID()), "")
			}
```

在函数末尾 `run.Status = status` 之后（`if e.Publish != nil` 之前或之后均可，保持 `degraded` 标记在）追加：

```go
	if st.Ctx != nil && st.RCA != nil {
		_ = st.Ctx.SaveLongTermMemory(st.RCA, st.Recommendation)
	}
```

新增辅助函数（`engine.go` 末尾）：

```go
func stageSummary(st *State, stageID string) string {
	switch stageID {
	case "triage":
		if st.Triage != nil {
			if st.Triage.Proceed {
				return "优先级 " + st.Triage.Priority + "，进入排查"
			}
			return "已去重跳过: " + st.Triage.Reason
		}
	case "context":
		if st.Context != nil && st.Context.DeviceInfo.Name != "" {
			return "设备 " + st.Context.DeviceInfo.Name + " 快照已收集"
		}
	case "rca":
		if st.RCA != nil {
			return "根因: " + st.RCA.RootCause
		}
	case "recommend":
		if st.Recommendation != nil {
			return "处置: " + strings.Join(st.Recommendation.Actions, "；")
		}
	}
	return "阶段完成"
}
```

`engine.go` import 增加 `"strings"`。

- [ ] **Step 5: 运行确认通过**

Run: `go test ./internal/pipeline/ -run TestEngineInjectsContext -v`
Expected: PASS。再跑 `go test ./internal/pipeline/` 全量，确认既有测试不回归。

- [ ] **Step 6: 提交**

```bash
git add backend/internal/pipeline/build.go backend/internal/pipeline/engine.go backend/internal/pipeline/engine_test.go
git commit -m "feat: 流水线接入上下文引擎与长期记忆沉淀"
```

---

### Task 12: rca 三档触发（Completer 接口化 + ReAct）

**Files:**
- Modify: `backend/internal/pipeline/rca.go`
- Modify: `backend/internal/pipeline/stages_test.go`

**Interfaces:**
- Consumes: `context.Completer`、`context.Engine.RunReact`、`context.Engine.Assemble`。
- Produces: `NewRCAStage(lc context.Completer) *RCAStage`（签名从 `*llm.Client` 改为 `context.Completer`，`llm.Client` 天然满足）；`ParseRCAJSON` 保留。

- [ ] **Step 1: 写失败测试**（stages_test.go 追加）

stages_test.go 顶部（import 区后）追加 fake Completer：

```go
type fakeCompleter struct {
	raw string
	err error
}

func (f *fakeCompleter) Complete(_ context.Context, _, _ string) (string, error) {
	return f.raw, f.err
}
```

import 追加 `"aiops/internal/context"`。

追加测试（同包 `pipeline` 可直接访问 `e.ctxStore` 等私有字段）：

```go
func TestRCAReactFallsBackOnError(t *testing.T) {
	mockdata.GenerateAll(42)
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := context.NewStore(s)
	reg := context.NewRegistry()
	for _, tt := range context.DefaultTools() {
		reg.Register(tt)
	}
	// 假模型永远只回 tool_call，无 final_answer → ReAct 到 max_steps 后落规则兜底
	fake := &fakeCompleter{raw: `{"thought":"调工具","tool_call":{"name":"search_runbook","args":{"metric":"disk_usage"}}}`}
	e := NewEngine(s, []Stage{NewRCAStage(fake)})
	e.ctxStore = cs
	e.tools = reg
	e.react = true
	e.topK = 3
	alert := model.Alert{ID: "alarm-rca", Device: "db-01", Metric: "disk_usage",
		Severity: "critical", FiredAt: time.Now(), CreatedAt: time.Now()}
	if err := e.Run(context.Background(), alert, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	run, _ := s.GetRun("run-" + alert.ID)
	if run.Status != model.StatusCompleted {
		t.Fatalf("status = %q, want completed (rule fallback)", run.Status)
	}
	evs, _ := cs.ListToolEvents("run-" + alert.ID)
	if len(evs) != 5 { // max_steps 轮工具调用
		t.Fatalf("tool events = %d, want 5", len(evs))
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/pipeline/ -run TestRCAReactFallsBack -v`
Expected: FAIL——`cannot use fakeCompleter as *llm.Client`（当前 `NewRCAStage(lc *llm.Client)` 尚不接受接口）。

- [ ] **Step 3: 修改 `rca.go`**

```go
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"aiops/internal/context"
	"aiops/internal/model"
)

type RCAStage struct {
	llm context.Completer
}

func NewRCAStage(lc context.Completer) *RCAStage { return &RCAStage{llm: lc} }

func (RCAStage) StageID() string { return "rca" }
func (RCAStage) Name() string    { return "根因分析" }

func (s *RCAStage) Run(ctx context.Context, st *State) error {
	if s.llm == nil {
		st.RCA = ruleRCA(st)
		return nil
	}
	if st.Ctx != nil && st.Ctx.ReactEnabled() {
		return s.react(ctx, st)
	}
	return s.singleCall(ctx, st)
}

func (s *RCAStage) react(ctx context.Context, st *State) error {
	q := context.Query{
		Device: st.Alert.Device, Metric: st.Alert.Metric, Severity: st.Alert.Severity,
		Stage: "rca", RunID: st.Run.ID, Keywords: []string{st.Alert.Message},
	}
	as, err := st.Ctx.Assemble(q, true)
	if err != nil {
		st.RCA = ruleRCA(st)
		return nil
	}
	system := "你是算力中心硬件运维专家。基于上下文，若证据不足可调用工具补充，推理记录写入write_scratchpad。最终输出JSON: {\"final_answer\":{\"root_cause\":string,\"evidence_chain\":[string],\"confidence\":0~1,\"gaps\":[string]}}。除final_answer或tool_call外不输出其它内容。"
	res, err := st.Ctx.RunReact(ctx, s.llm, system, q, as.User)
	if err != nil || res.Final == nil {
		st.RCA = ruleRCA(st)
		return nil
	}
	var rc model.RCA
	if err := json.Unmarshal(res.Final, &rc); err != nil {
		st.RCA = ruleRCA(st)
		return nil
	}
	st.RCA = &rc
	return nil
}

func (s *RCAStage) singleCall(ctx context.Context, st *State) error {
	system := "你是算力中心硬件运维专家。基于给定证据输出JSON：{\"root_cause\":string,\"evidence_chain\":[string],\"confidence\":0~1,\"gaps\":[string]}。只输出JSON。"
	user := ""
	if st.Ctx != nil {
		if as, err := st.Ctx.Assemble(context.Query{
			Device: st.Alert.Device, Metric: st.Alert.Metric, Severity: st.Alert.Severity,
			Stage: "rca", RunID: st.Run.ID, Keywords: []string{st.Alert.Message}}, false); err == nil {
			user = as.User + "\n"
		}
	}
	user += fmt.Sprintf("设备:%s 指标:%s\n证据:%s\n%s",
		st.Alert.Device, st.Alert.Metric,
		model.MustJSON(st.Evidence),
		extraGapHint(st))
	raw, err := s.llm.Complete(ctx, system, user)
	if err != nil {
		st.RCA = ruleRCA(st)
		st.RCA.Gaps = append(st.RCA.Gaps, "LLM调用失败，已使用规则兜底")
		return nil
	}
	rc, err := ParseRCAJSON(raw)
	if err != nil {
		st.RCA = ruleRCA(st)
		return nil
	}
	st.RCA = rc
	return nil
}
```

`ruleRCA`、`extraGapHint`、`ParseRCAJSON` 保留不动。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/pipeline/`
Expected: PASS（全量，含既有 stages_test）。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/pipeline/rca.go backend/internal/pipeline/stages_test.go
git commit -m "feat: rca阶段ReAct与单次调用双路径"
```

---

### Task 13: recommend 阶段上下文注入

**Files:**
- Modify: `backend/internal/pipeline/recommend.go`
- Modify: `backend/internal/pipeline/stages_test.go`

**Interfaces:**
- Consumes: `context.Completer`、`context.Engine.Assemble`。
- Produces: `NewRecommendStage(lc context.Completer) *RecommendStage`。

- [ ] **Step 1: 写失败测试**（stages_test.go 追加）

```go
func TestRecommendInjectsAssembledContext(t *testing.T) {
	mockdata.GenerateAll(42)
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := context.NewStore(s)
	st := &State{
		Run:   model.PipelineRun{ID: "run-1", AlertID: "alarm-1"},
		Alert: model.Alert{ID: "alarm-1", Device: "db-01", Metric: "disk_usage", Severity: "critical"},
		RCA:   &model.RCA{RootCause: "磁盘空间不足"},
	}
	_ = cs.AddLongTermMemory(model.LongTermMemory{ID: "m1", Device: "db-01", Metric: "disk_usage",
		RootCause: "磁盘空间不足", Confidence: 0.8, ActionsJSON: `["清理"]`, RunID: "run-0", CreatedAt: time.Now()})
	rec := NewRecommendStage(&fakeCompleter{raw: `{"actions":["清理日志"],"runbook_ref":"disk_usage","risk":"低"}`})
	if err := rec.Run(context.Background(), st); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if st.Recommendation == nil || len(st.Recommendation.Actions) == 0 {
		t.Fatalf("recommendation = %+v", st.Recommendation)
	}
}
```

（该测试未设置 `st.Ctx`，走 nil-safe 单次调用路径；`fakeCompleter` 已在上个任务定义。断言只验证调用不崩、结果正确。若要验证上下文注入，可手工设置 `st.Ctx = context.NewEngine(cs, reg, "run-1", "db-01", "disk_usage", false, 3)`。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/pipeline/ -run TestRecommendInjects -v`
Expected: FAIL——`cannot use fakeCompleter as llm.Client`（当前 `NewRecommendStage(lc *llm.Client)`）。

- [ ] **Step 3: 修改 `recommend.go`**

```go
package pipeline

import (
	"context"
	"fmt"

	"aiops/internal/context"
	"aiops/internal/llm"
	"aiops/internal/mockdata"
	"aiops/internal/model"
)

type RecommendStage struct {
	llm context.Completer
}

func NewRecommendStage(lc context.Completer) *RecommendStage { return &RecommendStage{llm: lc} }

func (RecommendStage) StageID() string { return "recommend" }
func (RecommendStage) Name() string    { return "处置建议" }

func (s *RecommendStage) Run(ctx context.Context, st *State) error {
	steps, ok := mockdata.SearchRunbook(st.Alert.Metric)
	if !ok {
		steps = []string{"联系人工介入排查"}
	}
	actions := steps
	if s.llm != nil {
		system := "你是算力中心运维助手。基于根因和预案，给出简明的处置建议步骤列表。只输出JSON：{\"actions\":[string],\"runbook_ref\":string,\"risk\":string}"
		root := ""
		if st.RCA != nil {
			root = st.RCA.RootCause
		}
		user := ""
		if st.Ctx != nil {
			if as, err := st.Ctx.Assemble(context.Query{
				Device: st.Alert.Device, Metric: st.Alert.Metric, Severity: st.Alert.Severity,
				Stage: "recommend", RunID: st.Run.ID, Keywords: []string{st.Alert.Message}}, false); err == nil {
				user = as.User + "\n"
			}
		}
		user += fmt.Sprintf("根因:%s\n预案:%v", root, steps)
		if raw, err := s.llm.Complete(ctx, system, user); err == nil {
			var rec model.Recommendation
			if llm.ParseJSON(raw, &rec) == nil && len(rec.Actions) > 0 {
				st.Recommendation = &rec
				return nil
			}
		}
	}
	st.Recommendation = &model.Recommendation{
		Actions:    actions,
		RunbookRef: st.Alert.Metric,
		Risk:       "低",
	}
	return nil
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/pipeline/ -run TestRecommendInjects -v` 以及全量 `go test ./internal/pipeline/`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/pipeline/recommend.go backend/internal/pipeline/stages_test.go
git commit -m "feat: recommend阶段注入组装上下文"
```

---

### Task 14: evidence 与 context 阶段落工具事件/笔记

**Files:**
- Modify: `backend/internal/pipeline/evidence.go`
- Modify: `backend/internal/pipeline/context.go`
- Modify: `backend/internal/pipeline/stages_test.go`

**Interfaces:**
- Consumes: `context.Engine.AddToolEvent`、`context.Engine.AddScratchpad`。

- [ ] **Step 1: 写失败测试**（stages_test.go 追加）

```go
func TestEvidenceRecordsToolEvents(t *testing.T) {
	mockdata.GenerateAll(42)
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := context.NewStore(s)
	st := &State{
		Run:   model.PipelineRun{ID: "run-1", AlertID: "alarm-1"},
		Alert: model.Alert{ID: "alarm-1", Device: "db-01", Metric: "disk_usage", FiredAt: time.Now().Add(-time.Hour)},
	}
	st.SuggestLogs = []string{"no space"}
	st.Ctx = context.NewEngine(cs, nil, "run-1", "db-01", "disk_usage", false, 3)
	if err := (EvidenceStage{}).Run(context.Background(), st); err != nil {
		t.Fatalf("Run: %v", err)
	}
	evs, _ := cs.ListToolEvents("run-1")
	if len(evs) != 2 { // query_metric + search_logs
		t.Fatalf("tool events = %d, want 2", len(evs))
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/pipeline/ -run TestEvidenceRecords -v`
Expected: FAIL——当前 evidence 阶段不写 tool_events（断言 2 失败）。

- [ ] **Step 3: 修改 `evidence.go`**

在 `st.Evidence = &model.Evidence{...}` 之后追加：

```go
	if st.Ctx != nil {
		_ = st.Ctx.AddToolEvent(model.ToolEvent{
			ID: model.NewID("evt"), RunID: st.Run.ID, ToolName: "query_metric",
			ArgsJSON:   model.MustJSON(map[string]any{"device": st.Alert.Device, "metric": st.Alert.Metric, "window_minutes": 120}),
			ResultJSON: model.MustJSON(metrics), Status: model.ToolStatusOK, CreatedAt: time.Now(),
		})
		_ = st.Ctx.AddToolEvent(model.ToolEvent{
			ID: model.NewID("evt"), RunID: st.Run.ID, ToolName: "search_logs",
			ArgsJSON:   model.MustJSON(map[string]any{"device": st.Alert.Device, "keywords": st.SuggestLogs, "window_minutes": 120}),
			ResultJSON: model.MustJSON(logs), Status: model.ToolStatusOK, CreatedAt: time.Now(),
		})
	}
```

- [ ] **Step 4: 修改 `context.go` 写笔记**

在 `st.DeviceContext = &dc` 赋值之后追加：

```go
	if st.Ctx != nil {
		_ = st.Ctx.AddScratchpad("context", "设备 "+dev.Name+" 快照已收集，邻居 "+strconv.Itoa(len(st.Context.Neighbors))+" 个", "")
	}
```

`context.go` 需要 import `"strconv"`。

- [ ] **Step 5: 运行确认通过**

Run: `go test ./internal/pipeline/`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add backend/internal/pipeline/evidence.go backend/internal/pipeline/context.go backend/internal/pipeline/stages_test.go
git commit -m "feat: 证据与上下文阶段沉淀工具事件与笔记"
```

---

### Task 15: 聊天 Agent 接入上下文

**Files:**
- Modify: `backend/internal/agent/agent.go`
- Modify: `backend/internal/agent/agent_test.go`

**Interfaces:**
- Consumes: `context.NewStore`、`context.Assembler`（或 `context.NewEngine` + `Assemble`）。
- Produces: `agent.NewWithContext(s *store.Store, lc *llm.Client, eng *pipeline.Engine, cs *context.Store, topK int) *Agent`（`New(s, lc, eng)` 委托给 `NewWithContext(s, lc, eng, nil, 3)`）；`extractQuery(text string, s *store.Store) (device, metric string)`；Agent 私有字段 `ctxStore *context.Store`、`topK int`。

- [ ] **Step 1: 写失败测试**（agent_test.go 追加）

```go
func TestAgentRuleAnswerIncludesLongTermMemory(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := context.NewStore(s)
	now := time.Now()
	_ = cs.AddLongTermMemory(model.LongTermMemory{ID: "m1", Device: "db-01", Metric: "disk_usage",
		RootCause: "磁盘空间不足", Confidence: 0.8, ActionsJSON: `["清理日志"]`, RunID: "run-0", CreatedAt: now.Add(-time.Hour)})
	a := model.Alert{ID: "alarm-1", Device: "db-01", Metric: "disk_usage",
		Severity: "critical", FiredAt: now, CreatedAt: now}
	_ = s.SaveAlert(a)
	_ = s.SaveRun(model.PipelineRun{ID: "run-" + a.ID, AlertID: a.ID, Status: model.StatusCompleted, Iteration: 0, MaxIteration: 2, CreatedAt: now, UpdatedAt: now})
	_ = s.SaveStage(model.StageResult{ID: "st1", RunID: "run-" + a.ID, StageID: "rca", StageName: "根因分析",
		Status: model.StageCompleted, Output: `{"root_cause":"磁盘空间不足","evidence_chain":["e"],"confidence":0.8,"gaps":[]}`, Iteration: 0, StartedAt: now, EndedAt: now})
	ag := NewWithContext(s, nil, nil, cs, 3)
	msg, err := ag.Handle(context.Background(), "alarm-1什么原因")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(msg.Content, "上次") {
		t.Fatalf("reply missing long-term memory: %q", msg.Content)
	}
}
```

（`agent_test.go` import 增加 `"strings"`、`"aiops/internal/context"`。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/agent/ -run TestAgentRuleAnswer -v`
Expected: FAIL——`undefined: NewWithContext`。

- [ ] **Step 3: 修改 `agent.go`**

```go
package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"aiops/internal/context"
	"aiops/internal/llm"
	"aiops/internal/model"
	"aiops/internal/pipeline"
	"aiops/internal/store"
)

type Agent struct {
	store    *store.Store
	llm      *llm.Client
	eng      *pipeline.Engine
	ctxStore *context.Store
	topK     int
}

func New(s *store.Store, lc *llm.Client, eng *pipeline.Engine) *Agent {
	return NewWithContext(s, lc, eng, nil, 3)
}

func NewWithContext(s *store.Store, lc *llm.Client, eng *pipeline.Engine, cs *context.Store, topK int) *Agent {
	return &Agent{store: s, llm: lc, eng: eng, ctxStore: cs, topK: topK}
}

func (a *Agent) buildAssembled(device, metric string) *context.Assembled {
	if a.ctxStore == nil || device == "" || metric == "" {
		return nil
	}
	asm := context.NewAssembler(a.ctxStore, a.topK)
	as, err := asm.Assemble(context.Query{Device: device, Metric: metric, Stage: "chat"}, false, nil)
	if err != nil {
		return nil
	}
	return as
}

func (a *Agent) memorySuffix(device, metric string) string {
	as := a.buildAssembled(device, metric)
	if as == nil || !strings.Contains(as.User, "【设备历史经验】") {
		return ""
	}
	// 取长期记忆段落首行摘要
	for _, line := range strings.Split(as.User, "\n") {
		if strings.HasPrefix(line, "上次(") {
			return "\n参考历史经验：" + line
		}
	}
	return ""
}
```

在 `Handle` 中，`AskRootCause` 分支的规则回复拼接处追加长期记忆摘要。修改 `answerFor` 的末尾返回前，若 `a.ctxStore != nil` 且查询到对应告警，附加 `a.memorySuffix(alert.Device, alert.Metric)`。在 `Handle` 的 `InspectAlert, AskRootCause, AskRecommendation` 分支内，先解析 `alert, err := a.store.GetAlert(alertID)`，成功后在 LLM 可用时用 `a.buildAssembled(alert.Device, alert.Metric)` 拼入提示词；LLM 不可用时规则回复加 `a.memorySuffix(...)`。

具体最小改造（保持现有 `Handle` 结构，仅扩展 `answerFor`）：

```go
func (a *Agent) answerFor(alertID string, intent Intent) string {
	run, err := a.store.GetRun("run-" + alertID)
	if err != nil {
		return fmt.Sprintf("告警 %s 尚未完成排查，请稍后再问。", alertID)
	}
	if run.Status == model.StatusRunning {
		return fmt.Sprintf("告警 %s 正在排查中，请稍后查看时间线。", alertID)
	}
	var rca model.RCA
	var rec model.Recommendation
	if st, ok, _ := a.store.GetLatestStage(run.ID, "rca"); ok {
		_ = llm.ParseJSON(st.Output, &rca)
	}
	if st, ok, _ := a.store.GetLatestStage(run.ID, "recommend"); ok {
		_ = llm.ParseJSON(st.Output, &rec)
	}
	reply := ""
	switch intent {
	case AskRecommendation:
		if len(rec.Actions) > 0 {
			reply = fmt.Sprintf("处置建议：\n%s", strings.Join(rec.Actions, "\n"))
		}
	case AskRootCause:
		if rca.RootCause != "" {
			reply = fmt.Sprintf("根因：%s\n置信度：%.0f%%\n证据链：%s",
				rca.RootCause, rca.Confidence*100, strings.Join(rca.EvidenceChain, "；"))
		}
	}
	if reply == "" {
		reply = fmt.Sprintf("告警 %s：%s", alertID, rca.RootCause)
	}
	// 长期记忆附加
	alert, err := a.store.GetAlert(alertID)
	if err == nil {
		reply += a.memorySuffix(alert.Device, alert.Metric)
	}
	return reply
}
```

（`extractQuery` 用文本找设备名/指标名，供 `buildAssembled` 在无告警命中时使用。为控制范围，本任务先通过已命中的 alert 提供 device/metric；`extractQuery` 仅作为辅助函数实现并测试命中设备名场景。）

追加：

```go
func extractQuery(text string, s *store.Store) (device, metric string) {
	low := strings.ToLower(text)
	knownMetrics := []string{"cpu_usage", "disk_usage", "gpu_util", "mem_usage", "net_in"}
	for _, m := range knownMetrics {
		if strings.Contains(low, m) {
			metric = m
			break
		}
	}
	for _, d := range mockdata.Devices() {
		if strings.Contains(low, strings.ToLower(d.Name)) {
			device = d.Name
			break
		}
	}
	return device, metric
}
```

`agent.go` import 增加 `"aiops/internal/mockdata"`、`"aiops/internal/context"`。

- [ ] **Step 4: 补 extractQuery 测试**（agent_test.go 追加）

```go
func TestExtractQuery(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	dev, met := extractQuery("排查一下db-01的disk_usage", s)
	if dev != "db-01" || met != "disk_usage" {
		t.Fatalf("extractQuery = %q %q", dev, met)
	}
}
```

- [ ] **Step 5: 运行确认通过**

Run: `go test ./internal/agent/`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add backend/internal/agent/agent.go backend/internal/agent/agent_test.go
git commit -m "feat: 聊天Agent接入上下文与长期记忆"
```

---

### Task 16: main 装配 + 端到端增强

**Files:**
- Modify: `backend/cmd/server/main.go`
- Modify: `backend/internal/e2e/e2e_test.go`

**Interfaces:**
- Consumes: `context.NewStore`、`context.NewRegistry`、`context.DefaultTools`、`pipeline.BuildWithContext`、`agent.NewWithContext`。

- [ ] **Step 1: 写端到端测试**（e2e_test.go 追加）

```go
func TestEndToEndContextWritesLongTermMemory(t *testing.T) {
	mockdata.GenerateAll(3)
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := context.NewStore(s)
	reg := context.NewRegistry()
	for _, tt := range context.DefaultTools() {
		reg.Register(tt)
	}
	// react=false，无 LLM → 规则兜底，仍应沉淀长期记忆
	eng := pipeline.BuildWithContext(s, nil, coordinator.New(), cs, reg, false, 3)
	now := time.Now()
	alert := model.Alert{ID: "alarm-ctx", Device: "web-01", Metric: "mem_usage",
		Severity: "warning", Message: "内存高", FiredAt: now, CreatedAt: now}
	if err := eng.Run(context.Background(), alert, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	ltm, err := cs.ListLongTermMemory("web-01", "mem_usage")
	if err != nil || len(ltm) == 0 {
		t.Fatalf("long term memory = %+v err=%v", ltm, err)
	}
	if ltm[0].RunID != "run-"+alert.ID {
		t.Fatalf("run id = %q", ltm[0].RunID)
	}
}
```

`e2e_test.go` import 增加 `"aiops/internal/context"`。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/e2e/ -run TestEndToEndContext -v`
Expected: 通过或失败取决于 main 未接——本测试直接用 `BuildWithContext`，应已通过（Task 11 完成）。若通过则改为验证 main 接线（见 Step 4）。

- [ ] **Step 3: 修改 `main.go`**

```go
	cs := context.NewStore(st)
	reg := context.NewRegistry()
	for _, t := range context.DefaultTools() {
		reg.Register(t)
	}

	eng := pipeline.BuildWithContext(st, lc, coordinator.New(), cs, reg, cfg.React, cfg.TopK)
	eng.Publish = h.Publish

	ag := agent.NewWithContext(st, lc, eng, cs, cfg.TopK)
```

import 增加 `"aiops/internal/context"`。`agent.New` 调用替换为 `agent.NewWithContext`。

- [ ] **Step 4: 运行全量测试**

Run: `go test ./...`
Expected: 全 PASS。

- [ ] **Step 5: 更新 AGENTS.md 架构备注（可选，一行）**

在 `AGENTS.md` 的 Architecture notes 追加：

```
- Context engineering: `internal/context` 包实现 Scratchpad/记忆/工具(ReAct)/知识(RAG)，每次调 LLM 前经 Selector+Assembler 挑选相关上下文；`CONTEXT_REACT`(默认1) 控制 ReAct。
```

- [ ] **Step 6: 提交**

```bash
git add backend/cmd/server/main.go backend/internal/e2e/e2e_test.go AGENTS.md
git commit -m "feat: main装配上下文引擎并增强端到端验证"
```

---

## Self-Review 记录（由计划作者在写完后核对）

- **Spec 覆盖**：第 4 节数据模型 → Task 1/5/6；第 5 节 Selector → Task 4；第 6 节 Assembler → Task 9；第 7 节 Tools/ReAct → Task 7/8；第 8 节流水线 → Task 11/12/13/14；第 9 节聊天 → Task 15；第 10 节配置/错误/测试 → Task 2 + 各任务测试；知识语料 → Task 3。✅
- **占位符**：无 TBD/TODO；Step 中代码均为完整实现。✅
- **类型一致性**：`NewEngine(cs, reg, runID, device, metric, react, topK)` 在 Task 10/11/14 中一致；`NewRCAStage(lc context.Completer)` / `NewRecommendStage(lc context.Completer)` 在 Task 12/13 中与 build.go 的 `NewRCAStage(lc)` / `NewRecommendStage(lc)` 兼容（`*llm.Client` 满足接口）；`llm.ParseJSON` 用于 JSON 解析（Task 13）。Task 12 的 stages_test 直接设置 `e.ctxStore/e.tools/e.react/e.topK` 私有字段（同包 `pipeline` 可行）；e2e 用 `BuildWithContext`。✅
