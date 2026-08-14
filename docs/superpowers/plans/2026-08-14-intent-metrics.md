# 意图识别评测指标 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 eval 体系新增三个意图分类聚合指标（正确率 Accuracy / 可信度 Precision / 召回率 Recall），逐配置输出到报告 summary，并扩充 chat 评测集覆盖"应争议/不应争议"正反例。

**Architecture:** 新增 `backend/internal/eval/intent.go` 实现 `IntentStats` 类型与 `intentMetrics` 聚合函数（对每个配置遍历 chat 任务，pass@k 语义判定 correct/judgedDisputed/shouldDisputed，得到 2×2 混淆矩阵三指标）；`report.go` 的 `Stats` 增 `Intent *IntentStats` 字段并在 `BuildReport`/`Summarize` 填充打印；`task.go` 追加 6 个 chat 任务；`runner.go` 的 `ensureAlertForChat` 支持新任务告警种子。复用 `grader.go` 现有的 `contains`/`containsAll` 与 IntentMatch 判定口径。

**Tech Stack:** Go 1.24，纯 `internal/eval` 包内改动。不引入新依赖。注释与 commit message 用中文。

## Global Constraints

- Go 模块 `aiops`，包路径以 `aiops/internal/...` 开头。
- 测试命令：`go test ./internal/eval/`；全量：`go test ./...`。
- `mockdata.GenerateAll(seed)` 进程内一次性生成，测试须先调用。
- 提交信息用 conventional prefixes（`feat:`/`fix:`/`test:`/`docs:`/`chore:`）。
- 分母为 0 时指标记 0，不 panic。
- 既有回归护栏 `TestRuleRegressionMainTasks`/`TestRuleRegressionGpuTempFails` 保持通过（新增 chat 任务不影响 pipeline 主任务）。

---

### Task 1: intent.go 三指标聚合 + 复用 IntentMatch 判定

**Files:**
- Create: `backend/internal/eval/intent.go`
- Modify: `backend/internal/eval/grader.go:149-166`（`gradeChat` 复用 `intentMatch`）
- Test: `backend/internal/eval/intent_test.go`

**Interfaces:**
- Consumes: `Task`/`Expected`/`TrialResult`/`ScoredTrial`/`Config`（同包）、`contains`/`containsAll`（`grader.go`）
- Produces:
  - `type IntentStats struct { Accuracy, Precision, Recall float64 }`（JSON tag `accuracy`/`precision`/`recall`）
  - `func intentMetrics(tasks []Task, configs []Config, scored map[string][]ScoredTrial) map[string]IntentStats`
  - 私有 `intentMatch(t Task, res *TrialResult) bool`、`allUnknown(res *TrialResult) bool`、`isShouldDisputed(t Task) bool`

- [ ] **Step 1: 写失败测试 `backend/internal/eval/intent_test.go`**

```go
package eval

import "testing"

func TestIntentMetricsFull(t *testing.T) {
	tasks := []Task{
		{ID: "c1", Object: ObjectChat, Expected: Expected{Intent: "AskRootCause"}},
		{ID: "c2", Object: ObjectChat, Expected: Expected{Intent: "Unknown"}},
		{ID: "c3", Object: ObjectChat, Expected: Expected{Intents: []string{"ListAlerts", "AskRootCause"}}},
		{ID: "p1", Object: ObjectPipeline, Expected: Expected{Intent: "Unknown"}}, // 非 chat，应忽略
	}
	configs := []Config{{Name: "rule"}}
	scored := map[string][]ScoredTrial{
		"c1|rule": {{TrialResult: &TrialResult{Intents: []string{"AskRootCause"}}}},
		"c2|rule": {{TrialResult: &TrialResult{Intents: []string{"Unknown"}}}},
		"c3|rule": {{TrialResult: &TrialResult{Intents: []string{"ListAlerts", "AskRootCause"}}}},
	}
	st := intentMetrics(tasks, configs, scored)["rule"]
	// total=3, correct=3 → accuracy=1.0；judgedDisputed=1(c2)，shouldDisputed=1(c2)，tp=1
	if st.Accuracy != 1.0 || st.Precision != 1.0 || st.Recall != 1.0 {
		t.Fatalf("intentMetrics = %+v, want 全 1.0", st)
	}
}

func TestIntentMetricsMisclassify(t *testing.T) {
	// c2 应争议(Unknown)但系统误判为 ListAlerts → recall=0, precision=0/0=0, accuracy=1/2
	tasks := []Task{
		{ID: "c1", Object: ObjectChat, Expected: Expected{Intent: "AskRootCause"}},
		{ID: "c2", Object: ObjectChat, Expected: Expected{Intent: "Unknown"}},
	}
	configs := []Config{{Name: "rule"}}
	scored := map[string][]ScoredTrial{
		"c1|rule": {{TrialResult: &TrialResult{Intents: []string{"AskRootCause"}}}},
		"c2|rule": {{TrialResult: &TrialResult{Intents: []string{"ListAlerts"}}}},
	}
	st := intentMetrics(tasks, configs, scored)["rule"]
	if st.Accuracy != 0.5 {
		t.Fatalf("accuracy = %v, want 0.5", st.Accuracy)
	}
	if st.Recall != 0 {
		t.Fatalf("recall = %v, want 0", st.Recall)
	}
}

func TestIntentMetricsZeroDenom(t *testing.T) {
	tasks := []Task{{ID: "c1", Object: ObjectChat, Expected: Expected{Intent: "AskRootCause"}}}
	configs := []Config{{Name: "rule"}}
	// 无任何 trial → total=0，全部 0
	st := intentMetrics(tasks, configs, map[string][]ScoredTrial{})["rule"]
	if st.Accuracy != 0 || st.Precision != 0 || st.Recall != 0 {
		t.Fatalf("无 trial 应全 0: %+v", st)
	}
	// 有 trial 但无 shouldDisputed → recall 分母 0，记 0
	scored := map[string][]ScoredTrial{
		"c1|rule": {{TrialResult: &TrialResult{Intents: []string{"AskRootCause"}}}},
	}
	st2 := intentMetrics(tasks, configs, scored)["rule"]
	if st2.Accuracy != 1.0 || st2.Recall != 0 {
		t.Fatalf("无争议任务 recall 应为 0: %+v", st2)
	}
}

func TestAllUnknown(t *testing.T) {
	if !allUnknown(&TrialResult{Intents: []string{"Unknown"}}) {
		t.Fatal("单 Unknown 应判为争议")
	}
	if !allUnknown(&TrialResult{Intents: []string{"Unknown", "Unknown"}}) {
		t.Fatal("全 Unknown 应判为争议")
	}
	if allUnknown(&TrialResult{Intents: []string{"Unknown", "ListAlerts"}}) {
		t.Fatal("含非 Unknown 不应判为争议")
	}
	if allUnknown(&TrialResult{}) {
		t.Fatal("空意图不应判为争议")
	}
	// Intents 空但 Intent 为 Unknown → 视为全 Unknown
	if !allUnknown(&TrialResult{Intent: "Unknown"}) {
		t.Fatal("Intent=Unknown 回退应判为争议")
	}
}

func TestIsShouldDisputed(t *testing.T) {
	if !isShouldDisputed(Task{Expected: Expected{Intent: "Unknown"}}) {
		t.Fatal("Intent=Unknown 应为应争议")
	}
	if isShouldDisputed(Task{Expected: Expected{Intent: "AskRootCause"}}) {
		t.Fatal("Intent=AskRootCause 不应为应争议")
	}
	if isShouldDisputed(Task{Expected: Expected{Intents: []string{"ListAlerts"}}}) {
		t.Fatal("Intents 非空任务不应为应争议")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/eval/ -run 'TestIntentMetrics|TestAllUnknown|TestIsShouldDisputed' -v`
Expected: 编译错误 `undefined: intentMetrics` 等。

- [ ] **Step 3: 写实现 `backend/internal/eval/intent.go`**

```go
package eval

// IntentStats 意图分类聚合指标（逐配置）。
type IntentStats struct {
	Accuracy  float64 `json:"accuracy"`  // 正确率：分类正确任务数 / chat 任务总数
	Precision float64 `json:"precision"` // 可信度：正确判争议 / 所有判争议
	Recall    float64 `json:"recall"`    // 召回率：正确判争议 / 所有应争议
}

// intentMetrics 对每个配置聚合 chat 任务的意图分类指标。
// 任务级判定取 pass@k 语义：任一 trial 命中即算。
func intentMetrics(tasks []Task, configs []Config, scored map[string][]ScoredTrial) map[string]IntentStats {
	out := make(map[string]IntentStats, len(configs))
	for _, cfg := range configs {
		var total, correct, judgedDisputed, shouldDisputed, tp float64
		for _, t := range tasks {
			if t.Object != ObjectChat {
				continue
			}
			trs := scored[t.ID+"|"+cfg.Name]
			if len(trs) == 0 {
				continue
			}
			total++
			corr, jd := false, false
			for i := range trs {
				res := trs[i].TrialResult
				if intentMatch(t, res) {
					corr = true
				}
				if allUnknown(res) {
					jd = true
				}
			}
			if corr {
				correct++
			}
			if jd {
				judgedDisputed++
			}
			if isShouldDisputed(t) {
				shouldDisputed++
				if jd {
					tp++
				}
			}
		}
		st := IntentStats{}
		if total > 0 {
			st.Accuracy = correct / total
		}
		if judgedDisputed > 0 {
			st.Precision = tp / judgedDisputed
		}
		if shouldDisputed > 0 {
			st.Recall = tp / shouldDisputed
		}
		out[cfg.Name] = st
	}
	return out
}

// intentMatch 判断 trial 的意图是否命中 ground-truth（与 gradeChat 同一判定口径）。
func intentMatch(t Task, res *TrialResult) bool {
	got := res.Intents
	if len(got) == 0 {
		got = []string{res.Intent}
	}
	if len(t.Expected.Intents) > 0 {
		return containsAll(got, t.Expected.Intents)
	}
	return contains(got, t.Expected.Intent)
}

// allUnknown 判断 trial 的意图是否全部为 Unknown（判为争议 = 输出引导语）。
func allUnknown(res *TrialResult) bool {
	got := res.Intents
	if len(got) == 0 {
		got = []string{res.Intent}
	}
	if len(got) == 0 {
		return false
	}
	for _, i := range got {
		if i != "Unknown" {
			return false
		}
	}
	return true
}

// isShouldDisputed ground-truth：Expected.Intent == "Unknown" 且无 Intents。
func isShouldDisputed(t Task) bool {
	return t.Expected.Intent == "Unknown" && len(t.Expected.Intents) == 0
}
```

**修改 `grader.go` 的 `gradeChat`，复用 `intentMatch`（去掉重复的命中判定，保留 `got` 供 detail）：**

```go
func gradeChat(t Task, res *TrialResult) []GraderResult {
	got := res.Intents
	if len(got) == 0 {
		got = []string{res.Intent}
	}
	ok := intentMatch(t, res)
	detail := "intents=" + strings.Join(got, ",")
	if ok {
		detail += " reply=" + truncate(res.Reply, 80)
	}
	out := []GraderResult{{
		Name: "IntentMatch", Pass: ok, Score: boolScore(ok), Detail: detail, Weight: 0.4,
	}}
	// 期望回复包含关键词（如根因/处置）时追加内容检查
	if len(t.Expected.RootCause) > 0 {
		contentOK := false
		for _, kw := range t.Expected.RootCause {
			if strings.Contains(res.Reply, kw) {
				contentOK = true
				break
			}
		}
		out = append(out, GraderResult{Name: "ReplyContent", Pass: contentOK,
			Score: boolScore(contentOK), Detail: "reply=" + truncate(res.Reply, 80), Weight: 0.6})
	}
	return out
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/eval/ -run 'TestIntentMetrics|TestAllUnknown|TestIsShouldDisputed|TestGradeChat' -v`
Expected: 全部 PASS（`TestGradeChatIntent`/`TestGradeChatMultiIntent` 行为不变）。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/eval/intent.go backend/internal/eval/grader.go backend/internal/eval/intent_test.go
git commit -m "feat: eval意图分类三指标聚合(正确率/可信度/召回率)"
```

---

### Task 2: report.go 集成指标组

**Files:**
- Modify: `backend/internal/eval/report.go:31-36,133-139,226-233`
- Test: `backend/internal/eval/report_test.go`

**Interfaces:**
- Consumes: `intentMetrics`/`IntentStats`（Task 1）
- Produces: `Stats` 新增字段 `Intent *IntentStats`（JSON `intent,omitempty`）；`BuildReport` 填充；`Summarize` 打印

- [ ] **Step 1: 写失败测试（追加到 `report_test.go`）**

```go
func TestBuildReportIntentStats(t *testing.T) {
	mockdata.GenerateAll(42)
	task, _ := TaskByID("chat-list-alerts")
	cfg := Config{Name: ConfigRule}
	res, err := RunTask(context.Background(), task, cfg, nil, 1)
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	sc := ScoredTrial{TrialResult: res, Scores: Grade(context.Background(), task, cfg, res, nil)}
	sc.Score = WeightedScore(sc.Scores)
	sc.Pass = sc.Score >= 0.6
	rep := BuildReport([]Task{task}, []Config{cfg}, map[string][]ScoredTrial{"chat-list-alerts|rule": {sc}})
	st := rep.Summary.ByConfig["rule"]
	if st.Intent == nil {
		t.Fatal("ByConfig 应带 Intent 指标组")
	}
	if st.Intent.Accuracy != 1.0 {
		t.Fatalf("accuracy = %v, want 1.0", st.Intent.Accuracy)
	}
	if !strings.Contains(rep.Summarize(), "intent") {
		t.Fatalf("摘要应打印 intent 行: %q", rep.Summarize())
	}
}
```

（`report_test.go` 现有 import 若缺 `context`/`strings`，按需补。）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/eval/ -run TestBuildReportIntentStats -v`
Expected: 编译错误 `st.Intent undefined`（`Stats` 无该字段）。

- [ ] **Step 3: 写实现（修改 `report.go`）**

`Stats` 结构体追加字段：

```go
type Stats struct {
	PassAtK   float64       `json:"pass_at_k"`
	MeanScore float64       `json:"mean_score"`
	Stddev    float64       `json:"stddev"`
	Saturated bool          `json:"saturated"`
	Intent    *IntentStats  `json:"intent,omitempty"` // chat 意图分类指标（仅 ByConfig 填充）
}
```

`BuildReport` 在第二遍汇总前计算一次意图指标，并在填充 `ByConfig` 时带上。在 `for name, a := range cfgAcc {` 循环之前插入：

```go
	intents := intentMetrics(tasks, configs, scored)
```

并把填充处改为（原 `rep.Summary.ByConfig[name] = Stats{...}`）：

```go
	for name, a := range cfgAcc {
		pk := a.sumPk / float64(a.n)
		ist := intents[name]
		rep.Summary.ByConfig[name] = Stats{
			PassAtK: pk, MeanScore: a.sumMean / float64(a.n),
			Stddev: a.sumSd / float64(a.n), Saturated: pk > 0.85,
			Intent: &ist,
		}
	}
```

`Summarize` 的 config 循环中追加意图指标行（`Summarize` 函数内）：

```go
	for cfg, st := range r.Summary.ByConfig {
		fmt.Fprintf(&b, "  [%s] pass@k=%.2f 平均分=%.2f\n", cfg, st.PassAtK, st.MeanScore)
		if st.Intent != nil {
			fmt.Fprintf(&b, "       intent: 正确率=%.2f 可信度=%.2f 召回率=%.2f\n",
				st.Intent.Accuracy, st.Intent.Precision, st.Intent.Recall)
		}
	}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/eval/ -run 'TestBuildReport' -v`
Expected: 全部 PASS（既有 `TestBuildReportJSON` 因 `intent,omitempty` 仍合法 JSON）。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/eval/report.go backend/internal/eval/report_test.go
git commit -m "feat: eval报告summary集成意图指标组"
```

---

### Task 3: 扩充 chat 评测集 + ensureAlertForChat 支持

**Files:**
- Modify: `backend/internal/eval/task.go:136-146`（`chat-irrelevant` 后追加 6 个任务）
- Modify: `backend/internal/eval/runner.go:162-190`（`ensureAlertForChat`）
- Test: `backend/internal/eval/task_test.go`

**Interfaces:**
- Consumes: `mkAlert`/`Expected`/`Task`（同包）
- Produces: 6 个新 chat 任务：`chat-ambiguous`/`chat-greeting`/`chat-weather-offtopic`/`chat-status-progress`/`chat-disk-reason-2`/`chat-multi-recommend`

- [ ] **Step 1: 写失败测试（追加到 `task_test.go`）**

```go
func TestChatTasksIntentCoverage(t *testing.T) {
	// 应争议任务集（Expected.Intent == "Unknown"）
	wantDisputed := map[string]bool{
		"chat-irrelevant":       true,
		"chat-ambiguous":        true,
		"chat-greeting":         true,
		"chat-weather-offtopic": true,
	}
	found := map[string]bool{}
	for _, tk := range DefaultTasks() {
		if tk.Object != ObjectChat {
			continue
		}
		if isShouldDisputed(tk) {
			found[tk.ID] = true
		}
	}
	if len(found) != len(wantDisputed) {
		t.Fatalf("应争议任务数 = %d, want %d: %v", len(found), len(wantDisputed), found)
	}
	for id := range wantDisputed {
		if !found[id] {
			t.Fatalf("缺应争议任务 %s", id)
		}
	}
}

func TestChatMultiRecommendExists(t *testing.T) {
	tk, ok := TaskByID("chat-multi-recommend")
	if !ok {
		t.Fatal("缺 chat-multi-recommend 任务")
	}
	if len(tk.Expected.Intents) != 2 {
		t.Fatalf("chat-multi-recommend 期望 2 个意图, got %v", tk.Expected.Intents)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/eval/ -run 'TestChatTasksIntentCoverage|TestChatMultiRecommendExists' -v`
Expected: FAIL——应争议任务数不符（缺 3 个）/ `TaskByID` 未找到。

- [ ] **Step 3: 写实现（修改 `task.go`）**

在 `DefaultTasks` 的 `chat-irrelevant` 任务块之后、`chat-multi` 之前插入：

```go
		{
			ID: "chat-ambiguous", Object: ObjectChat,
			ChatInput: "帮我查一下原因",
			Expected:  Expected{Intent: "Unknown"},
		},
		{
			ID: "chat-greeting", Object: ObjectChat,
			ChatInput: "在吗？",
			Expected:  Expected{Intent: "Unknown"},
		},
		{
			ID: "chat-weather-offtopic", Object: ObjectChat,
			ChatInput: "明天天气好吗",
			Expected:  Expected{Intent: "Unknown"},
		},
		{
			ID: "chat-status-progress", Object: ObjectChat,
			ChatInput: "现在排查进度如何",
			Expected:  Expected{Intent: "AskStatus"},
		},
		{
			ID: "chat-disk-reason-2", Object: ObjectChat,
			ChatInput: "db-01 为什么告警",
			Expected:  Expected{Intent: "AskRootCause", RootCause: []string{"磁盘空间不足"}},
		},
		{
			ID: "chat-multi-recommend", Object: ObjectChat,
			ChatInput: "alarm-1 什么原因？alarm-2 怎么处理？",
			Expected:  Expected{Intents: []string{"AskRootCause", "AskRecommendation"}},
			Configs:   []string{ConfigLLMSingle, ConfigLLMReact},
		},
```

**修改 `runner.go` 的 `ensureAlertForChat`：**

早返回条件补 `AskStatus`（`chat-status-progress` 不涉告警）：

```go
	if t.Expected.Intent == "Unknown" || t.Expected.Intent == "ListAlerts" || t.Expected.Intent == "AskStatus" {
		return model.Alert{}, nil
	}
```

scene map 追加 `chat-disk-reason-2`：

```go
	scene := map[string][4]string{
		"chat-root-cause-db":  {"db-01", "disk_usage", "critical", "磁盘使用率超过95%"},
		"chat-recommend-gpu":  {"compute-02", "gpu_util", "critical", "GPU掉卡，利用率归零"},
		"chat-inspect-switch": {"switch-core-01", "net_in", "critical", "核心交换机入口流量中断"},
		"chat-multi":          {"db-01", "disk_usage", "critical", "磁盘使用率超过95%"},
		"chat-disk-reason-2":  {"db-01", "disk_usage", "critical", "磁盘使用率超过95%"},
	}[t.ID]
```

在 `alertID := "eval-chat-" + t.ID` 之前，追加 chat-multi-recommend 双告警种子（该任务引用 alarm-1 与 alarm-2）：

```go
	if t.ID == "chat-multi-recommend" {
		now := time.Now()
		for _, id := range []string{"alarm-1", "alarm-2"} {
			a := model.Alert{ID: id, Device: "db-01", Metric: "disk_usage",
				Severity: "critical", Message: "磁盘使用率超过95%", Status: "active",
				FiredAt: now, CreatedAt: now}
			if err := st.SaveAlert(a); err != nil {
				return a, err
			}
		}
		return model.Alert{}, nil
	}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/eval/ -run 'TestChatTasks|TestChatMultiRecommend|TestValidateTasks|TestDefaultTasks|TestRunTaskChat|TestGradeChat|TestIntentMetrics|TestRuleRegression' -v`
Expected: 全部 PASS。注意 `TestRunTaskChatMultiRuleFallback` 依赖 `chat-multi`（未改动），`TestRuleRegressionMainTasks` 只覆盖 pipeline 主任务（不受新增 chat 任务影响）。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/eval/task.go backend/internal/eval/runner.go backend/internal/eval/task_test.go
git commit -m "feat: eval扩充chat评测集覆盖应争议/不应争议正反例"
```

---

### Task 4: 全量回归验证

**Files:**
- 无新增；验证全部包

- [ ] **Step 1: 全量单测**

Run: `go test ./...`
Expected: 全部 PASS，重点 `internal/eval` 全绿。

- [ ] **Step 2: 静态检查**

Run: `go vet ./internal/eval/`
Expected: 无告警。

- [ ] **Step 3: 无 LLM key 冒烟（CI 路径）**

Run: `LLM_API_KEY= go run ./cmd/eval ci --threshold 0.9`（在 `backend/` 目录）
Expected: 退出码 0，rule 路径 pass@k ≥ 0.9（新增 chat 任务不应拉低 pass@k 至 0.9 以下）。

- [ ] **Step 4: 提交（若产生文件改动）**

```bash
git status
# 无未提交改动则跳过
```

---

## 自检清单（执行前确认）

- **Spec 覆盖**：✅ 正确率/可信度/召回率定义与 2×2 混淆矩阵 → Task 1 `intentMetrics`；争议判定（系统侧全 Unknown / ground-truth Intent==Unknown）→ Task 1 `allUnknown`/`isShouldDisputed`；pass@k 语义 → Task 1 任一 trial 命中；分母 0 记 0 → Task 1 与 `TestIntentMetricsZeroDenom`；报告 summary 指标组（JSON `intent` + stdout）→ Task 2；6 个新 chat 任务 → Task 3；ensureAlertForChat 支持 → Task 3；回归护栏 → Task 4。
- **placeholder 扫描**：所有代码步骤给出完整实现，无 TBD/TODO。
- **类型一致性**：`IntentStats{Accuracy, Precision, Recall float64}` 在 Task 1 定义、Task 2 使用一致；`intentMetrics(tasks []Task, configs []Config, scored map[string][]ScoredTrial) map[string]IntentStats` 一致；`intentMatch`/`allUnknown`/`isShouldDisputed` 签名在 Task 1 定义、测试与 Task 3 测试引用一致；`Stats.Intent *IntentStats`（`json:"intent,omitempty"`）一致。
