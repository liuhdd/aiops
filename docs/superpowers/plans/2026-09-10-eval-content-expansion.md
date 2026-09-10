# 评测题集扩容实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 agent 评测任务集新增 25 条（4 pipeline + 17 chat + 4 context）手工校准题，为 RAG golden set 新增 25 条题，并给新题加 CI 豁免开关。

**Architecture:** 先给 `Task` 加 `Extended` 字段并让 `cmd/eval ci` 默认跳过 Extended（避免 20+ 新题拉爆 CI 耗时/token）；为新增 pipeline 题扩展 `mockdata` 的确定性异常场景与日志；再向 `DefaultTasks()` / `golden_set.jsonl` 追加题。数据集上传/实验回写见配套计划 `2026-09-10-phoenix-eval-datasets.md`。

**Tech Stack:** Go 1.24；纯数据与既有测试框架，无新依赖。

**Spec:** `docs/superpowers/specs/2026-09-10-phoenix-eval-datasets-design.md`

## Global Constraints

- 所有命令在 `backend/` 目录执行；测试 `go test ./...`、静态检查 `go vet ./...`。
- 新增 agent 题一律 `Extended: true`；新增 RAG 题直接进文件（`rageval` 无 CI 门禁，成本低）。
- pipeline 题必须满足 `ValidateTasks()`（device+metric 唯一）；`Expected.Logs` 必须能在 `mockdata` 中检索到；`Expected.Runbook` 必须存在。
- chat 题的 `askRootCause`/`askRecommendation` 类（触发流水线）需在 `ensureAlertForChat` 的 `scene` 表补设备场景；纯意图题（Unknown/ListAlerts/AskStatus）无需。
- `golden_set.jsonl` 的 `doc_ids` 必须引用 `mockdata.Docs()` 中存在的 mock 概念 id。
- 代码注释、commit message 用中文，commit 用 conventional 前缀。

---

### Task 1: `Task.Extended` 字段 + `cmd/eval ci` 豁免开关

**Files:**
- Modify: `backend/internal/eval/task.go`（`Task` 加字段）
- Modify: `backend/internal/eval/phoenix.go`（`taskExample` metadata 带 `extended`）
- Modify: `backend/cmd/eval/main.go`（`selectCITasks` + `ci --all`）
- Test: `backend/cmd/eval/main_test.go`（追加）

**Interfaces:**
- Consumes: `eval.DefaultTasks()`、`eval.Task`。
- Produces:
  - `Task.Extended bool`
  - `func selectCITasks(all bool) []eval.Task`（cmd/eval 包内）

- [ ] **Step 1: 写失败测试**

追加到 `backend/cmd/eval/main_test.go`（import 追加 `"aiops/internal/eval"`）：

```go
func TestSelectCITasksSkipsExtended(t *testing.T) {
	all := eval.DefaultTasks()
	if len(all) == 0 {
		t.Fatal("任务集为空")
	}
	// 人为标记一条为 Extended（通过任务集副本无法改，改为直接测过滤函数对真实扩展题的判断）
	kept := selectCITasks(false)
	keptAll := selectCITasks(true)
	if len(keptAll) != len(all) {
		t.Fatalf("--all 应保留全部: got %d want %d", len(keptAll), len(all))
	}
	if len(kept) > len(keptAll) {
		t.Fatalf("默认不应多于 --all")
	}
	for _, tk := range kept {
		if tk.Extended {
			t.Fatalf("默认 CI 不应含 Extended 任务 %s", tk.ID)
		}
	}
}
```

- [ ] **Step 2: 确认测试失败**

Run: `go test ./cmd/eval/ -run TestSelectCITasks -v`
Expected: FAIL，`undefined: selectCITasks`。

- [ ] **Step 3: 实现**

`backend/internal/eval/task.go`：`Task` 结构体追加字段（放在 `ChatInput` 之后）：

```go
	Extended  bool        // true=仅 eval run 全量跑，cmd/eval ci 默认跳过（CI 耗时/token 控制）
```

`backend/internal/eval/phoenix.go`：`taskExample` 的 `Metadata` 增加 `extended`：

```go
		Metadata: map[string]any{"task_id": t.ID, "object": t.Object, "configs": t.Configs, "extended": t.Extended},
```

`backend/cmd/eval/main.go`：
- `cmdCI` 的 flagset 增加 `all := fs.Bool("all", false, "包含 Extended 任务（默认跳过）")`。
- 在 `cmdCI` 开头（`mockdata.GenerateAll(42)` 之后）加 `tasks := selectCITasks(*all)`，把循环里的 `for _, t := range eval.DefaultTasks()` 改为 `for _, t := range tasks`，末尾 `eval.BuildReport(eval.DefaultTasks(), ...)` 改为 `eval.BuildReport(tasks, ...)`。
- 追加：

```go
// selectCITasks 返回 CI 门禁任务集：默认跳过 Extended，all=true 时全量。
func selectCITasks(all bool) []eval.Task {
	var out []eval.Task
	for _, t := range eval.DefaultTasks() {
		if t.Extended && !all {
			continue
		}
		out = append(out, t)
	}
	return out
}
```

- [ ] **Step 4: 确认通过**

Run: `go build ./... && go test ./cmd/eval/ ./internal/eval/ -v`
Expected: PASS（此时尚无 Extended 题，`selectCITasks(false)==selectCITasks(true)`）。

- [ ] **Step 5: Commit**

```bash
git add internal/eval/task.go internal/eval/phoenix.go cmd/eval/main.go cmd/eval/main_test.go
git commit -m "feat: Task.Extended 与 cmd/eval ci 豁免开关"
```

---

### Task 2: mockdata 新增确定性异常场景与日志

**Files:**
- Modify: `backend/internal/mockdata/metrics.go`（`scenario` 新增 4 个 case）
- Modify: `backend/internal/mockdata/logs.go`（新增 db-01/compute-01/web-01 日志 + storage-01 条目）
- Test: `backend/internal/mockdata/metrics_logs_test.go`（追加）

**Interfaces:**
- Consumes: 既有 `scenario`、`generateLogs`。
- Produces: `db-01|cpu_usage`、`compute-01|mem_usage`、`web-01|disk_usage`、`storage-01|disk_usage` 的确定性异常曲线与可检索日志。

- [ ] **Step 1: 写失败测试**

追加到 `backend/internal/mockdata/metrics_logs_test.go`：

```go
func TestNewAnomalyScenarios(t *testing.T) {
	cases := []struct {
		dev, metric string
		min         float64
	}{
		{"db-01", "cpu_usage", 85},
		{"compute-01", "mem_usage", 90},
		{"web-01", "disk_usage", 95},
		{"storage-01", "disk_usage", 95},
	}
	for _, c := range cases {
		if got := scenario(c.dev, c.metric, 0); got < c.min {
			t.Fatalf("%s/%s = %v, want >= %v", c.dev, c.metric, got, c.min)
		}
	}
}

func TestNewAnomalyLogs(t *testing.T) {
	GenerateAll(42)
	now := time.Now()
	checks := []struct {
		dev string
		kws []string
	}{
		{"db-01", []string{"load", "cpu"}},
		{"compute-01", []string{"oom", "OutOfMemory", "memory"}},
		{"web-01", []string{"no space", "error"}},
		{"storage-01", []string{"no space", "error"}},
	}
	for _, c := range checks {
		logs, err := SearchLogs(c.dev, c.kws, now.Add(-3*time.Hour), now)
		if err != nil || len(logs) == 0 {
			t.Fatalf("%s 关键字 %v 无匹配: %v", c.dev, c.kws, err)
		}
	}
}
```

- [ ] **Step 2: 确认测试失败**

Run: `go test ./internal/mockdata/ -run TestNewAnomaly -v`
Expected: FAIL（新场景返回默认 50，新日志无匹配）。

- [ ] **Step 3: 实现 metrics.go**

在 `scenario` 的 `switch` 中、`default:` 之前插入：

```go
	case device == "db-01" && metric == "cpu_usage":
		if m <= 30 {
			return 90 + jitter(m)
		}
		return 55 + jitter(m)
	case device == "compute-01" && metric == "mem_usage":
		if m <= 20 {
			return 94
		}
		return 70 + jitter(m)
	case device == "web-01" && metric == "disk_usage":
		return 96
	case device == "storage-01" && metric == "disk_usage":
		return 96
```

- [ ] **Step 4: 实现 logs.go**

在 `generateLogs` 的 `entries` map 中：
- `"db-01"` 列表追加 `{20, "WARN", "load average 20.1, cpu usage saturated"},`
- `"compute-01"` 列表追加 `{18, "ERROR", "kernel: OutOfMemory: memory cgroup oom-kill process 2222"},`
- `"web-01"` 列表追加 `{30, "ERROR", "ext4-fs error (device sda1): no space left on device"},`
- 新增 `"storage-01"` 键：

```go
		"storage-01": {
			{40, "ERROR", "ext4-fs error (device sdb1): no space left on device"},
			{20, "WARN", "df: /data usage 96%"},
		},
```

- [ ] **Step 5: 确认通过**

Run: `go test ./internal/mockdata/ -v`
Expected: PASS（含原有用例；新增日志不影响既有查询）。

- [ ] **Step 6: Commit**

```bash
git add internal/mockdata/metrics.go internal/mockdata/logs.go internal/mockdata/metrics_logs_test.go
git commit -m "test: mockdata 新增 cpu/mem/disk 异常场景与日志"
```

---

### Task 3: 新增 4 条 pipeline 任务

**Files:**
- Modify: `backend/internal/eval/task.go`（`DefaultTasks()` 追加）
- Test: `backend/internal/eval/task_test.go`（追加断言）

**Interfaces:**
- Consumes: `mkAlert`、`Task`/`Expected`、Task 1 的 `Extended`。
- Produces: 任务 `cpu-hot-db-01`、`mem-oom-compute-01`、`disk-full-web-01`、`disk-full-storage-01`。

- [ ] **Step 1: 写失败测试**

追加到 `backend/internal/eval/task_test.go`：

```go
func TestNewPipelineTasks(t *testing.T) {
	ids := map[string]bool{}
	for _, tk := range DefaultTasks() {
		ids[tk.ID] = true
	}
	for _, id := range []string{"cpu-hot-db-01", "mem-oom-compute-01", "disk-full-web-01", "disk-full-storage-01"} {
		if !ids[id] {
			t.Fatalf("缺 pipeline 任务 %s", id)
		}
	}
	tk, _ := TaskByID("mem-oom-compute-01")
	if !tk.Extended {
		t.Fatalf("新题应为 Extended: %+v", tk)
	}
}
```

- [ ] **Step 2: 确认测试失败**

Run: `go test ./internal/eval/ -run TestNewPipelineTasks -v`
Expected: FAIL，`缺 pipeline 任务 cpu-hot-db-01`。

- [ ] **Step 3: 实现**

在 `DefaultTasks()` 的 `tasks := []Task{...}` 中、`gpu-temp-compute-02` 之后插入：

```go
		{
			ID: "cpu-hot-db-01", Object: ObjectPipeline,
			Alert: mkAlert("db-01", "cpu_usage", "warning", "CPU使用率持续高于90%"),
			Expected: Expected{
				RootCause: []string{"CPU热点进程", "负载饱和"},
				Logs:      []string{"cpu"},
				NoGaps:    true, Runbook: "cpu_usage",
			},
			Extended: true,
		},
		{
			ID: "mem-oom-compute-01", Object: ObjectPipeline,
			Alert: mkAlert("compute-01", "mem_usage", "warning", "内存使用率持续高于90%"),
			Expected: Expected{
				RootCause: []string{"内存泄漏", "OOM"},
				Logs:      []string{"OutOfMemory"},
				NoGaps:    true, Runbook: "mem_usage",
			},
			Extended: true,
		},
		{
			ID: "disk-full-web-01", Object: ObjectPipeline,
			Alert: mkAlert("web-01", "disk_usage", "critical", "磁盘使用率超过95%"),
			Expected: Expected{
				RootCause: []string{"磁盘空间不足"},
				Logs:      []string{"no space"},
				NoGaps:    true, Runbook: "disk_usage",
			},
			Extended: true,
		},
		{
			ID: "disk-full-storage-01", Object: ObjectPipeline,
			Alert: mkAlert("storage-01", "disk_usage", "critical", "磁盘使用率超过95%"),
			Expected: Expected{
				RootCause: []string{"磁盘空间不足"},
				Logs:      []string{"no space"},
				NoGaps:    true, Runbook: "disk_usage",
			},
			Extended: true,
		},
```

- [ ] **Step 4: 确认通过**

Run: `go test ./internal/eval/ -run 'TestNewPipelineTasks|TestValidateTasks|TestTaskGroundTruthMatchesMockdata|TestTaskRunbookExists' -v`
Expected: PASS（新增题 device+metric 唯一、日志可检索、runbook 存在）。

- [ ] **Step 5: Commit**

```bash
git add internal/eval/task.go internal/eval/task_test.go
git commit -m "test: 新增 4 条 pipeline 评测任务（cpu/mem/disk 边界）"
```

---

### Task 4: 新增 17 条 chat 任务 + 场景补全

**Files:**
- Modify: `backend/internal/eval/task.go`（`DefaultTasks()` 追加）
- Modify: `backend/internal/eval/runner.go`（`ensureAlertForChat` 场景表补全 + 空场景守卫）
- Test: `backend/internal/eval/task_test.go`（更新 `wantDisputed` + 追加单/多意图断言）

**Interfaces:**
- Consumes: `Task`/`Expected`、`Extended`。
- Produces: 17 条 chat 任务（见 Step 3 列表）。

- [ ] **Step 1: 更新应争议任务测试（先失败）**

修改 `backend/internal/eval/task_test.go` 的 `TestChatTasksIntentCoverage`，把 `wantDisputed` 扩为：

```go
	wantDisputed := map[string]bool{
		"chat-irrelevant":       true,
		"chat-ambiguous":        true,
		"chat-greeting":         true,
		"chat-weather-offtopic": true,
		"chat-reverse-thanks":   true,
		"chat-reverse-time":     true,
		"chat-reverse-meaning":  true,
		"chat-reverse-nonsense": true,
	}
```

并追加：

```go
func TestNewChatTasks(t *testing.T) {
	single := map[string]string{
		"chat-root-cause-cpu":    "AskRootCause",
		"chat-recommend-disk":    "AskRecommendation",
		"chat-status-progress-2": "AskStatus",
		"chat-list-alerts-2":     "ListAlerts",
		"chat-inspect-db":        "InspectAlert",
		"chat-root-cause-gpu":    "AskRootCause",
		"chat-recommend-mem":     "AskRecommendation",
		"chat-root-cause-net":    "AskRootCause",
		"chat-recommend-storage": "AskRecommendation",
		"chat-status-db":         "AskStatus",
	}
	for id, want := range single {
		tk, ok := TaskByID(id)
		if !ok {
			t.Fatalf("缺 chat 任务 %s", id)
		}
		if tk.Expected.Intent != want || !tk.Extended {
			t.Fatalf("%s = %+v（want intent=%s, Extended）", id, tk.Expected, want)
		}
	}
	multi := map[string][]string{
		"chat-multi-rootcause-recommend": {"AskRootCause", "AskRecommendation"},
		"chat-multi-list-status":         {"ListAlerts", "AskStatus"},
		"chat-multi-three":               {"AskRootCause", "AskRecommendation", "AskStatus"},
	}
	for id, want := range multi {
		tk, ok := TaskByID(id)
		if !ok {
			t.Fatalf("缺 chat 任务 %s", id)
		}
		if len(tk.Expected.Intents) != len(want) {
			t.Fatalf("%s intents = %v, want %v", id, tk.Expected.Intents, want)
		}
		if len(tk.Configs) == 0 {
			t.Fatalf("%s 多意图题应限定 Configs", id)
		}
	}
}
```

- [ ] **Step 2: 确认测试失败**

Run: `go test ./internal/eval/ -run 'TestChatTasksIntentCoverage|TestNewChatTasks' -v`
Expected: FAIL（缺任务/应争议数不符）。

- [ ] **Step 3: 实现任务**

在 `DefaultTasks()` 的 `tasks` 切片中、`chat-multi` 相关任务之后追加：

```go
		// ---- chat 单意图（规则可命中） ----
		{ID: "chat-root-cause-cpu", Object: ObjectChat, ChatInput: "compute-01 的 CPU 为什么这么高？",
			Expected: Expected{Intent: "AskRootCause"}, Extended: true},
		{ID: "chat-recommend-disk", Object: ObjectChat, ChatInput: "db-01 磁盘满了怎么办？",
			Expected: Expected{Intent: "AskRecommendation"}, Extended: true},
		{ID: "chat-status-progress-2", Object: ObjectChat, ChatInput: "当前排查状态怎么样",
			Expected: Expected{Intent: "AskStatus"}, Extended: true},
		{ID: "chat-list-alerts-2", Object: ObjectChat, ChatInput: "给我看看所有告警",
			Expected: Expected{Intent: "ListAlerts"}, Extended: true},
		{ID: "chat-inspect-db", Object: ObjectChat, ChatInput: "排查一下 db-01",
			Expected: Expected{Intent: "InspectAlert"}, Extended: true},
		{ID: "chat-root-cause-gpu", Object: ObjectChat, ChatInput: "compute-02 GPU 掉卡是什么原因？",
			Expected: Expected{Intent: "AskRootCause"}, Extended: true},
		{ID: "chat-recommend-mem", Object: ObjectChat, ChatInput: "web-01 内存告警怎么处理？",
			Expected: Expected{Intent: "AskRecommendation"}, Extended: true},
		{ID: "chat-root-cause-net", Object: ObjectChat, ChatInput: "switch-core-01 网络为什么中断了",
			Expected: Expected{Intent: "AskRootCause"}, Extended: true},
		{ID: "chat-recommend-storage", Object: ObjectChat, ChatInput: "storage-01 磁盘告警有什么处置建议",
			Expected: Expected{Intent: "AskRecommendation"}, Extended: true},
		{ID: "chat-status-db", Object: ObjectChat, ChatInput: "db-01 的排查状态",
			Expected: Expected{Intent: "AskStatus"}, Extended: true},
		// ---- chat 反例（期望 Unknown） ----
		{ID: "chat-reverse-thanks", Object: ObjectChat, ChatInput: "谢谢",
			Expected: Expected{Intent: "Unknown"}, Extended: true},
		{ID: "chat-reverse-time", Object: ObjectChat, ChatInput: "现在几点了",
			Expected: Expected{Intent: "Unknown"}, Extended: true},
		{ID: "chat-reverse-meaning", Object: ObjectChat, ChatInput: "帮我写一首诗",
			Expected: Expected{Intent: "Unknown"}, Extended: true},
		{ID: "chat-reverse-nonsense", Object: ObjectChat, ChatInput: "随便聊聊",
			Expected: Expected{Intent: "Unknown"}, Extended: true},
		// ---- chat 多意图（仅 LLM 配置） ----
		{ID: "chat-multi-rootcause-recommend", Object: ObjectChat, ChatInput: "db-01 磁盘为什么满？该怎么处理？",
			Expected: Expected{Intents: []string{"AskRootCause", "AskRecommendation"}},
			Configs:  []string{ConfigLLMSingle, ConfigLLMReact}, Extended: true},
		{ID: "chat-multi-list-status", Object: ObjectChat, ChatInput: "有哪些告警？排查进度如何？",
			Expected: Expected{Intents: []string{"ListAlerts", "AskStatus"}},
			Configs:  []string{ConfigLLMSingle, ConfigLLMReact}, Extended: true},
		{ID: "chat-multi-three", Object: ObjectChat, ChatInput: "compute-02 GPU 掉卡是什么原因？怎么处理？现在进度如何？",
			Expected: Expected{Intents: []string{"AskRootCause", "AskRecommendation", "AskStatus"}},
			Configs:  []string{ConfigLLMSingle, ConfigLLMReact}, Extended: true},
```

- [ ] **Step 4: 补全 `ensureAlertForChat` 场景**

在 `backend/internal/eval/runner.go` 的 `scene := map[string][4]string{...}` 中追加：

```go
		"chat-root-cause-cpu":            {"compute-01", "cpu_usage", "warning", "CPU使用率持续高于85%"},
		"chat-recommend-disk":            {"db-01", "disk_usage", "critical", "磁盘使用率超过95%"},
		"chat-root-cause-gpu":            {"compute-02", "gpu_util", "critical", "GPU掉卡，利用率归零"},
		"chat-recommend-mem":             {"web-01", "mem_usage", "warning", "内存使用率持续高于90%"},
		"chat-root-cause-net":            {"switch-core-01", "net_in", "critical", "核心交换机入口流量中断"},
		"chat-recommend-storage":         {"storage-01", "disk_usage", "critical", "磁盘使用率超过95%"},
		"chat-multi-rootcause-recommend": {"db-01", "disk_usage", "critical", "磁盘使用率超过95%"},
		"chat-multi-three":               {"compute-02", "gpu_util", "critical", "GPU掉卡，利用率归零"},
```

并在 `chat-multi-recommend` 的 special-case 块（`if t.ID == "chat-multi-recommend" { ... }`）**之后**、`alertID := "eval-chat-" + t.ID` 之前加空场景守卫（放在 special-case 之后是为了不误伤 `chat-multi-recommend`——它不在 scene 表中）：

```go
	if scene == ([4]string{}) {
		return model.Alert{}, nil
	}
```

- [ ] **Step 5: 确认通过**

Run: `go build ./... && go test ./internal/eval/ -v`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
git add internal/eval/task.go internal/eval/runner.go internal/eval/task_test.go
git commit -m "test: 新增 17 条 chat 评测任务（单/多意图与反例）"
```

---

### Task 5: 新增 4 条 context（ReAct）任务

**Files:**
- Modify: `backend/internal/eval/task.go`（`DefaultTasks()` 追加）
- Test: `backend/internal/eval/task_test.go`（追加）

**Interfaces:**
- Consumes: `mkAlert`、`Extended`。
- Produces: `react-cpu-evidence`、`react-disk-evidence`、`react-mem-evidence`、`react-net-evidence`。

- [ ] **Step 1: 写失败测试**

追加到 `backend/internal/eval/task_test.go`：

```go
func TestNewContextTasks(t *testing.T) {
	for _, id := range []string{"react-cpu-evidence", "react-disk-evidence", "react-mem-evidence", "react-net-evidence"} {
		tk, ok := TaskByID(id)
		if !ok {
			t.Fatalf("缺 context 任务 %s", id)
		}
		if tk.Object != ObjectContext || !tk.Extended {
			t.Fatalf("%s 应为 ObjectContext 且 Extended: %+v", id, tk)
		}
		if len(tk.Configs) != 1 || tk.Configs[0] != ConfigLLMReact {
			t.Fatalf("%s 应仅 llm-react: %v", id, tk.Configs)
		}
	}
}
```

- [ ] **Step 2: 确认测试失败**

Run: `go test ./internal/eval/ -run TestNewContextTasks -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

在 `DefaultTasks()` 的 `react-gpu-evidence` 之后追加：

```go
		{ID: "react-cpu-evidence", Object: ObjectContext,
			Alert:     mkAlert("compute-01", "cpu_usage", "warning", "CPU使用率持续高于85%"),
			Expected:  Expected{ToolUse: []string{"write_scratchpad"}},
			Configs:   []string{ConfigLLMReact}, Extended: true},
		{ID: "react-disk-evidence", Object: ObjectContext,
			Alert:     mkAlert("db-01", "disk_usage", "critical", "磁盘使用率超过95%"),
			Expected:  Expected{ToolUse: []string{"write_scratchpad"}},
			Configs:   []string{ConfigLLMReact}, Extended: true},
		{ID: "react-mem-evidence", Object: ObjectContext,
			Alert:     mkAlert("web-01", "mem_usage", "warning", "内存使用率持续高于90%"),
			Expected:  Expected{ToolUse: []string{"write_scratchpad"}},
			Configs:   []string{ConfigLLMReact}, Extended: true},
		{ID: "react-net-evidence", Object: ObjectContext,
			Alert:     mkAlert("switch-core-01", "net_in", "critical", "核心交换机入口流量中断"),
			Expected:  Expected{ToolUse: []string{"write_scratchpad"}},
			Configs:   []string{ConfigLLMReact}, Extended: true},
```

- [ ] **Step 4: 确认通过**

Run: `go test ./internal/eval/ -run 'TestNewContextTasks|TestValidateTasks' -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/eval/task.go internal/eval/task_test.go
git commit -m "test: 新增 4 条 context（ReAct）评测任务"
```

---

### Task 6: 新增 25 条 RAG golden 题

**Files:**
- Modify: `backend/testdata/golden_set.jsonl`（追加 q033–q057）
- Test: `backend/internal/goldenset/shipped_test.go`（新增，校验受控资产）

**Interfaces:**
- Consumes: `goldenset.Load`、`mockdata.Docs()`。
- Produces: 扩容后的 `golden_set.jsonl`（共 57 条）。

- [ ] **Step 1: 写失败测试**

创建 `backend/internal/goldenset/shipped_test.go`：

```go
package goldenset

import (
	"path/filepath"
	"testing"

	"aiops/internal/mockdata"
)

func TestShippedGoldenSetLoads(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "golden_set.jsonl")
	items, err := Load(path)
	if err != nil {
		t.Fatalf("加载受控 golden set 失败: %v", err)
	}
	if len(items) < 57 {
		t.Fatalf("golden set 应 >=57 条, got %d", len(items))
	}
	ids := map[string]bool{}
	known := map[string]bool{}
	for _, d := range mockdata.Docs() {
		known[d.ID] = true
	}
	for _, it := range items {
		if ids[it.ID] {
			t.Fatalf("golden id 重复: %s", it.ID)
		}
		ids[it.ID] = true
		for _, did := range append(append([]string{}, it.Expect.DocIDs...), it.Expect.DistractorDocIDs...) {
			if !known[did] {
				t.Fatalf("%s 引用未知 doc_id %s", it.ID, did)
			}
		}
	}
}
```

- [ ] **Step 2: 确认测试失败**

Run: `go test ./internal/goldenset/ -run TestShippedGoldenSetLoads -v`
Expected: FAIL，`应 >=57 条, got 32`。

- [ ] **Step 3: 追加题目**

在 `backend/testdata/golden_set.jsonl` 末尾追加以下 25 行（每行一条，勿留空行）：

```jsonl
{"id":"q033","type":"fact","question":"GPU 温度超过多少度需要检查散热与风扇转速？","expect":{"doc_ids":["doc-gpu-temp"],"distractor_doc_ids":[],"behavior":"hit"},"top_k":3}
{"id":"q034","type":"fact","question":"内存使用率超过90%时首先应查看哪个日志？","expect":{"doc_ids":["doc-mem-oom"],"distractor_doc_ids":[],"behavior":"hit"},"top_k":3}
{"id":"q035","type":"fact","question":"网络入口流量中断时如何检查光模块告警？","expect":{"doc_ids":["doc-net-link"],"distractor_doc_ids":[],"behavior":"hit"},"top_k":3}
{"id":"q036","type":"fact","question":"数据库磁盘满且 InnoDB 分配页失败该如何处置？","expect":{"doc_ids":["doc-db-tablespace"],"distractor_doc_ids":[],"behavior":"hit"},"top_k":3}
{"id":"q037","type":"fact","question":"存储节点磁盘告警时如何检查数据分布均衡度与副本？","expect":{"doc_ids":["doc-storage-full"],"distractor_doc_ids":[],"behavior":"hit"},"top_k":3}
{"id":"q038","type":"fact","question":"多节点 CPU 负载差异大时如何排查邻居节点？","expect":{"doc_ids":["doc-load-balance"],"distractor_doc_ids":[],"behavior":"hit"},"top_k":3}
{"id":"q039","type":"multi_doc","question":"GPU 温度过高且交换机端口抖动，应如何分别处置？","expect":{"doc_ids":["doc-gpu-temp","doc-network-flap"],"distractor_doc_ids":[],"behavior":"hit"},"top_k":5}
{"id":"q040","type":"multi_doc","question":"数据库表空间不足与存储节点副本不足如何联动处理？","expect":{"doc_ids":["doc-db-tablespace","doc-storage-full"],"distractor_doc_ids":[],"behavior":"hit"},"top_k":5}
{"id":"q041","type":"multi_doc","question":"CPU 热点与内存 OOM 同时出现，应如何排查？","expect":{"doc_ids":["doc-cpu-hot","doc-mem-oom"],"distractor_doc_ids":[],"behavior":"hit"},"top_k":5}
{"id":"q042","type":"multi_doc","question":"GPU 掉卡与网络广播风暴并发时的排查顺序是什么？","expect":{"doc_ids":["doc-gpu-xid","doc-network-flap"],"distractor_doc_ids":[],"behavior":"hit"},"top_k":5}
{"id":"q043","type":"negative","question":"磁盘空间充足时不需要执行哪些清理步骤？","expect":{"doc_ids":["doc-disk-full"],"distractor_doc_ids":[],"behavior":"hit"},"top_k":3}
{"id":"q044","type":"negative","question":"网络链路正常时不需要检查哪些项目？","expect":{"doc_ids":["doc-net-link"],"distractor_doc_ids":[],"behavior":"hit"},"top_k":3}
{"id":"q045","type":"negative","question":"CPU 负载正常时不需要执行哪些扩容操作？","expect":{"doc_ids":["doc-load-balance"],"distractor_doc_ids":[],"behavior":"hit"},"top_k":3}
{"id":"q046","type":"ambiguous","question":"数据库有点慢怎么处理？","expect":{"doc_ids":["doc-db-tablespace"],"distractor_doc_ids":["doc-cpu-hot"],"behavior":"hit"},"top_k":3}
{"id":"q047","type":"ambiguous","question":"显卡风扇声音很大怎么办？","expect":{"doc_ids":["doc-gpu-temp"],"distractor_doc_ids":["doc-gpu-xid"],"behavior":"hit"},"top_k":3}
{"id":"q048","type":"ambiguous","question":"入口流量好像不太对","expect":{"doc_ids":["doc-net-link"],"distractor_doc_ids":["doc-network-flap"],"behavior":"hit"},"top_k":3}
{"id":"q049","type":"reasoning","question":"为什么 GPU 掉卡要先查 NVRM Xid 而不是先扩容 CPU？","expect":{"doc_ids":["doc-gpu-xid"],"distractor_doc_ids":["doc-cpu-hot"],"behavior":"hit"},"top_k":3}
{"id":"q050","type":"reasoning","question":"为什么内存缓慢增长比瞬时峰值更可能是内存泄漏？","expect":{"doc_ids":["doc-mem-oom"],"distractor_doc_ids":[],"behavior":"hit"},"top_k":3}
{"id":"q051","type":"reasoning","question":"为什么分布式存储副本数不足会表现为单节点磁盘热点？","expect":{"doc_ids":["doc-storage-full"],"distractor_doc_ids":[],"behavior":"hit"},"top_k":3}
{"id":"q052","type":"no_answer","question":"如何更换笔记本电脑电池？","expect":{"doc_ids":[],"distractor_doc_ids":[],"behavior":"reject"},"top_k":3}
{"id":"q053","type":"no_answer","question":"怎么配置 Nginx 的 HTTPS 证书？","expect":{"doc_ids":[],"distractor_doc_ids":[],"behavior":"reject"},"top_k":3}
{"id":"q054","type":"cross_object","question":"用 GPU 温度散热方案处理数据库表空间不足是否合适？","expect":{"doc_ids":["doc-db-tablespace"],"distractor_doc_ids":["doc-gpu-temp"],"behavior":"hit"},"top_k":3}
{"id":"q055","type":"cross_object","question":"将网络链路排查步骤用于 CPU 负载不均衡是否有效？","expect":{"doc_ids":[],"distractor_doc_ids":["doc-cpu-hot","doc-net-link"],"behavior":"reject"},"top_k":3}
{"id":"q056","type":"misleading_premise","question":"为什么网络链路中断一定需要更换 CPU？","expect":{"doc_ids":["doc-net-link"],"distractor_doc_ids":[],"behavior":"hit"},"top_k":3}
{"id":"q057","type":"misleading_premise","question":"存储节点磁盘告警是不是必须立刻删除所有数据？","expect":{"doc_ids":["doc-storage-full"],"distractor_doc_ids":[],"behavior":"hit"},"top_k":3}
```

- [ ] **Step 4: 确认通过**

Run: `go test ./internal/goldenset/ -v`
Expected: PASS。

Run: `go run ./cmd/rageval -golden testdata/golden_set.jsonl`
Expected: 正常输出各类型指标（`loaded ... (57 items)`），无「引用了不存在的 doc_id」错误。

- [ ] **Step 5: Commit**

```bash
git add testdata/golden_set.jsonl internal/goldenset/shipped_test.go
git commit -m "test: RAG golden set 新增 25 条评测题"
```

---

### Task 7: 全量回归

**Files:** 无新增。

- [ ] **Step 1: 全量测试与静态检查**

Run（`backend/`）: `go build ./... && go vet ./... && go test ./...`
Expected: 全绿；`eval.DefaultTasks()` 共 44 条（19 原有 + 25 新增），除 `chat-*` 反例数由 4 增至 8 外，既有断言全部通过。

- [ ] **Step 2: CLI 冒烟（rule 全量，含新题）**

Run（`backend/`，`LLM_API_KEY=`）:
```bash
LLM_API_KEY= go run ./cmd/eval run --trials 1 --configs rule --report /tmp/eval-content.json
```
Expected: 正常完成；新 pipeline/chat/context 题出现在报告中（context 题仅 llm-react，rule 下不计）。

- [ ] **Step 3: CI 豁免验证**

Run（`backend/`，`LLM_API_KEY=`）:
```bash
LLM_API_KEY= go run ./cmd/eval ci --threshold 0.9
```
Expected: 默认跳过 Extended 新题；退出码与改动前一致（不因新题失败）。

- [ ] **Step 4: Commit**

若 Step 1-3 需要微调（如任务文案），修正后：
```bash
git add -A
git commit -m "test: 评测题集扩容回归修正"
```

---

## 收尾

- [ ] 新题全量上传 Phoenix：回到管线计划，执行 `datasetctl push --dataset all`，确认 `aiops-agents-eval` 含 44 条、`aiops-rag-golden` 含 57 条。
- [ ] 若某条新 pipeline 题在 rule 路径意外失败，逐题对照 `mockdata`：或修正 `Expected`，或保留为 LLM-only（`Configs: [llm-single, llm-react]`）。
- [ ] RAG 新题若某条长期检索不到期望文档，可将其 `behavior` 调整为 `reject` 或在后续检索调优 spec 中处理；本计划不阻塞。
