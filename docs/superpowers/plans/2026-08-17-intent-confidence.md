# 意图识别置信度打分 + description/examples 字段 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为意图识别加入置信度打分环节（规则固定分 + LLM 自评分，<0.4 落回 Unknown），并为每条意图叶子补充 examples 字段与 path 派生，二者均可在意图树管理界面修改。

**Architecture:** 在现有意图树（`intent_nodes` 表）上新增 `examples` 列；`LeafSpec` 增加 `Examples`/`Path` 供识别消费。识别链路从「硬分类」升级为 `ScoredIntent{Intent, Confidence}`：规则命中给固定分（实体关键词 0.9 / 主题词 0.6 / 无命中 0），LLM 分类让模型返回 0~1 置信度并注入 description+examples+path 做 few-shot，低置信度拦截为 Unknown。置信度不对外展示。

**Tech Stack:** Go 1.x（`modernc.org/sqlite`）、React 18 + Vite + TypeScript、Vitest。

## Global Constraints

- SQLite driver 名 `"sqlite"`（非 `sqlite3`）；`store.New` 已设 `SetMaxOpenConns(1)`，勿增连接。
- 注释、文档、commit message 用中文；commit subject 用 conventional 前缀（`feat:`/`fix:`/`test:`/`docs:`/`chore:`）。
- 后端测试：`go test ./...`（单包 `go test ./internal/agent/` 等）。前端：`npx vitest run`；构建 `npm run build`。
- 所有命令从各自目录运行：后端 `backend/`，前端 `frontend/`。
- LLM 不可用（`c == nil` 或 `ctxpkg.IsNilCompleter(c)`）必须回退规则路径，服务仍可运行。
- 不修改 pipeline 六阶段及其顺序；不修改意图树 CRUD/审计/软删除语义。
- `mockdata.GenerateAll(42)` 一次性；涉及 metrics/logs 的测试须先调用。

---

### Task 1: examples 字段持久化（model + store + seed）

**Files:**
- Modify: `backend/internal/model/intent.go`
- Modify: `backend/internal/store/store.go`
- Modify: `backend/internal/store/intent.go`
- Test: `backend/internal/store/intent_test.go`

**Interfaces:**
- Consumes: `model.IntentNode`、`model.LeafSpec` 现有字段。
- Produces:
  - `model.IntentNode.Examples []string`（json tag `examples`）
  - `model.LeafSpec.Examples []string`（tag `examples`）、`model.LeafSpec.Path string`（tag `path`）
  - `intent_nodes` 表新增 `examples TEXT NOT NULL DEFAULT '[]'` 列；`IntentStore` 的 `InsertIntentNode`/`UpdateIntentNode` 落库 examples；`scanIntentNode` 读回 examples（空 → `[]string{}`）。

- [ ] **Step 1: 写失败测试**

在 `backend/internal/store/intent_test.go` 追加：

```go
func TestIntentExamplesRoundTrip(t *testing.T) {
	s := newTestStore(t)
	n := model.IntentNode{ID: "n2", ParentID: "grp-builtin", Name: "带示例", Kind: model.KindKB,
		Code: "MyKB2", BindTarget: "kb-2", RuleKeywords: []string{"手册"},
		Examples: []string{"怎么查手册", "手册在哪"}, Enabled: true, SortOrder: 10}
	if err := s.WithIntentTx(func(tx IntentStore) error {
		return tx.InsertIntentNode(n)
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, _ := s.ListIntentNodes()
	for _, x := range got {
		if x.ID == "n2" {
			if len(x.Examples) != 2 || x.Examples[0] != "怎么查手册" {
				t.Fatalf("examples roundtrip 失败: %+v", x.Examples)
			}
			return
		}
	}
	t.Fatal("未找到插入节点")
}
```

并在 `TestSeedIntents` 末尾追加断言：

```go
if len(byID["leaf-AskRootCause"].Examples) == 0 {
	t.Fatal("AskRootCause 种子应有示例")
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/store/ -run 'TestIntentExamplesRoundTrip|TestSeedIntents' -v`（在 `backend/` 下）
Expected: 编译错误 `n.Examples undefined` / `byID[...].Examples undefined`。

- [ ] **Step 3: 实现 model 字段**

`backend/internal/model/intent.go`：在 `IntentNode` 的 `Description` 后新增字段；`LeafSpec` 新增 `Examples` 与 `Path`：

```go
type IntentNode struct {
	ID           string     `json:"id"`
	ParentID     string     `json:"parent_id"`
	Name         string     `json:"name"`
	Kind         IntentKind `json:"kind"`
	Code         string     `json:"code"`
	BindTarget   string     `json:"bind_target"`
	RuleKeywords []string   `json:"rule_keywords"`
	Description  string     `json:"description"`
	Examples     []string   `json:"examples"`
	Enabled      bool       `json:"enabled"`
	Deleted      bool       `json:"deleted"`
	SortOrder    int        `json:"sort_order"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type LeafSpec struct {
	Code         string   `json:"code"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Examples     []string `json:"examples"`
	RuleKeywords []string `json:"rule_keywords"`
	Path         string   `json:"path"`
}
```

- [ ] **Step 4: 实现 store 列与读写**

`backend/internal/store/store.go` 的 `migrate()`：
1. `intent_nodes` 的 `CREATE TABLE IF NOT EXISTS` 中，`description TEXT NOT NULL DEFAULT ''` 后加一行 `examples TEXT NOT NULL DEFAULT '[]',`。
2. 在 `idx_intent_parent` 索引创建之后（`intent_audit` 建表之前）加：
   ```go
   if err := ensureColumn(s.db, "intent_nodes", "examples", "examples TEXT NOT NULL DEFAULT '[]'"); err != nil {
       return err
   }
   ```

`backend/internal/store/intent.go`：
- `intentCols` 改为 `id, parent_id, name, kind, code, bind_target, rule_keywords, description, examples, enabled, deleted, sort_order, created_at, updated_at`。
- `scanIntentNode` 增加 `var ex string` 扫描，并在 `kw` 解析后同样解析 `ex` 到 `n.Examples`，nil 时置 `[]string{}`：

```go
var kw, ex string
if err := scan(&n.ID, &n.ParentID, &n.Name, &n.Kind, &n.Code, &n.BindTarget,
	&kw, &n.Description, &ex, &enabled, &deleted, &n.SortOrder, &n.CreatedAt, &n.UpdatedAt); err != nil {
	return n, err
}
n.Enabled = enabled != 0
n.Deleted = deleted != 0
if kw != "" {
	_ = json.Unmarshal([]byte(kw), &n.RuleKeywords)
}
if n.RuleKeywords == nil {
	n.RuleKeywords = []string{}
}
if ex != "" {
	_ = json.Unmarshal([]byte(ex), &n.Examples)
}
if n.Examples == nil {
	n.Examples = []string{}
}
```

- `InsertIntentNode`/`UpdateIntentNode`：`ex, _ := json.Marshal(n.Examples)`，INSERT/UPDATE 语句的列清单在 `description` 后加 `examples`，值占位符在 `n.Description` 后加 `string(ex)`。

- `seedIntents`：seeds 结构体加 `ex []string` 字段，6 个种子补示例（对应 spec）：

```go
seeds := []struct {
	code, desc string
	kw, ex     []string
	order      int
}{
	{"AskRootCause", "询问根因", []string{"什么原因", "为什么", "根因"}, []string{"alarm-123 什么原因", "这台机器为什么告警"}, 0},
	{"AskRecommendation", "询问处置建议", []string{"怎么处理", "怎么办", "建议", "处置"}, []string{"alarm-123 怎么处理", "磁盘满了怎么办"}, 1},
	{"AskStatus", "询问状态/进度", []string{"状态", "进度"}, []string{"排查进度如何", "现在什么状态"}, 2},
	{"InspectAlert", "排查某个告警", []string{}, []string{"查一下 compute-01", "看看 switch-core-01"}, 3},
	{"ListAlerts", "查看告警列表", []string{"列表", "有哪些告警", "告警"}, []string{"有哪些告警", "列出所有告警"}, 4},
	{"Unknown", "与告警运维无关", []string{}, []string{}, 5},
}
```

循环内 `ex, _ := json.Marshal(sd.ex)`，INSERT 语句列清单加 `examples`，值加 `string(ex)`。

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/store/ -run 'TestIntentExamplesRoundTrip|TestSeedIntents' -v`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
git add backend/internal/model/intent.go backend/internal/store/store.go backend/internal/store/intent.go backend/internal/store/intent_test.go
git commit -m "feat: 意图节点新增examples字段持久化"
```

---

### Task 2: 服务层 examples 透传 + Path 派生

**Files:**
- Modify: `backend/internal/intent/service.go`
- Test: `backend/internal/intent/service_test.go`

**Interfaces:**
- Consumes: Task 1 的 `model.IntentNode.Examples`、`model.LeafSpec.Examples/Path`。
- Produces:
  - `CreateReq.Examples []string`、`UpdateReq.Examples []string`（json `examples`），`Create`/`Update` 落库。
  - `ActiveLeavesFrom(nodes)` 派生的 `LeafSpec.Path`：沿 `parent_id` 链向上收集分组 `Name`，倒序后以 `/` 连接（含顶层分组名）。

- [ ] **Step 1: 写失败测试**

`backend/internal/intent/service_test.go` 追加：

```go
func TestCreatePersistsExamples(t *testing.T) {
	svc := newTestService(t)
	n, err := svc.Create(context.Background(), "t", CreateReq{ParentID: "grp-builtin", Name: "带示例", Kind: model.KindKB, Code: "ex1", Examples: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(n.Examples) != 2 || n.Examples[0] != "a" {
		t.Fatalf("examples = %+v", n.Examples)
	}
	leaves, _ := svc.ActiveLeaves(context.Background())
	for _, l := range leaves {
		if l.Code == "ex1" && (len(l.Examples) != 2 || l.Examples[1] != "b") {
			t.Fatalf("leaf examples = %+v", l.Examples)
		}
	}
}

func TestActiveLeavesPath(t *testing.T) {
	svc := newTestService(t)
	g, err := svc.Create(context.Background(), "t", CreateReq{Name: "告警排查", Kind: model.KindGroup})
	if err != nil {
		t.Fatalf("Create 分组: %v", err)
	}
	if _, err := svc.Create(context.Background(), "t", CreateReq{ParentID: g.ID, Name: "查手册", Kind: model.KindKB, Code: "kbx"}); err != nil {
		t.Fatalf("Create 叶子: %v", err)
	}
	leaves, _ := svc.ActiveLeaves(context.Background())
	found := false
	for _, l := range leaves {
		if l.Code == "kbx" {
			if l.Path != "告警排查" {
				t.Fatalf("Path = %q, want 告警排查", l.Path)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("未找到 kbx 叶子")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/intent/ -run 'TestCreatePersistsExamples|TestActiveLeavesPath' -v`
Expected: `CreateReq.Examples undefined` 编译错误。

- [ ] **Step 3: 实现 examples 透传**

`backend/internal/intent/service.go`：
- `CreateReq`/`UpdateReq` 各加 `Examples []string \`json:"examples"\``。
- `Create` 构造 `n := model.IntentNode{..., Examples: req.Examples, ...}`。
- `Update` 在 `n.Description = req.Description` 之后加 `n.Examples = req.Examples`。
- `normalize` 的 `KindGroup` 分支加 `n.Examples = []string{}`。

- [ ] **Step 4: 实现 Path 派生**

`ActiveLeavesFrom` 内，构造 `LeafSpec` 时补 `Examples` 与 `Path`：

```go
out = append(out, model.LeafSpec{
	Code: n.Code, Name: n.Name, Description: n.Description,
	Examples: n.Examples, RuleKeywords: n.RuleKeywords, Path: nodePath(n, byID),
})
```

并在同文件新增辅助函数：

```go
func nodePath(n model.IntentNode, byID map[string]model.IntentNode) string {
	var names []string
	for {
		if n.Name != "" {
			names = append(names, n.Name)
		}
		if n.ParentID == "" {
			break
		}
		p, ok := byID[n.ParentID]
		if !ok {
			break
		}
		n = p
	}
	// 倒序：顶层 → 叶子
	for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
		names[i], names[j] = names[j], names[i]
	}
	return strings.Join(names, "/")
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/intent/ -v`
Expected: PASS（含既有用例）。

- [ ] **Step 6: Commit**

```bash
git add backend/internal/intent/service.go backend/internal/intent/service_test.go
git commit -m "feat: 意图服务透传examples并派生叶子path"
```

---

### Task 3: 规则打分 RuleScore + ScoredIntent

**Files:**
- Modify: `backend/internal/agent/intent.go`
- Test: `backend/internal/agent/agent_test.go`

**Interfaces:**
- Consumes: `model.LeafSpec`（含 Examples/Path）、`mockdata.Devices()`、`alarmRe`。
- Produces:
  - `type ScoredIntent struct { Intent Intent; Confidence float64 }`
  - `RuleScore(leaves []model.LeafSpec, text string) ScoredIntent`：保留优先级顺序的选择语义；命中实体关键词 0.9、主题词/复合规则 0.6、无命中 Unknown(0)。
  - `RuleMatch(leaves, text) Intent = RuleScore(leaves, text).Intent`（签名不变，向后兼容）。
  - `isEntityWord(kw string) bool`：kw 命中设备名/指标名/alarmRe 之一。

- [ ] **Step 1: 写失败测试**

`backend/internal/agent/agent_test.go` 追加：

```go
func TestRuleScore(t *testing.T) {
	cases := []struct {
		text string
		want Intent
		conf float64
	}{
		{"有哪些告警", ListAlerts, 0.6},
		{"查一下alarm-123是什么原因", AskRootCause, 0.6},
		{"你好", Unknown, 0},
	}
	for _, c := range cases {
		sc := RuleScore(nil, c.text)
		if sc.Intent != c.want || sc.Confidence != c.conf {
			t.Fatalf("RuleScore(%q) = (%v,%v), want (%v,%v)", c.text, sc.Intent, sc.Confidence, c.want, c.conf)
		}
	}
	leaves := []model.LeafSpec{{Code: "MyKB", Name: "MyKB", RuleKeywords: []string{"db-01"}}}
	sc := RuleScore(leaves, "查一下db-01的问题")
	if sc.Intent != "MyKB" || sc.Confidence != 0.9 {
		t.Fatalf("实体命中应 0.9, got (%v,%v)", sc.Intent, sc.Confidence)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent/ -run TestRuleScore -v`
Expected: `undefined: RuleScore` 编译错误。

- [ ] **Step 3: 实现 RuleScore 与 ScoredIntent**

`backend/internal/agent/intent.go`：在 `RuleMatch` 附近替换/新增（`RuleMatch` 改为委托 `RuleScore`）：

```go
// ScoredIntent 带置信度的意图识别结果。
type ScoredIntent struct {
	Intent     Intent
	Confidence float64 // 0~1
}

// entityWords 实体词表（设备名 + 已知指标名）。
var entityWords = func() map[string]bool {
	m := make(map[string]bool)
	for _, d := range mockdata.Devices() {
		m[strings.ToLower(d.Name)] = true
	}
	for _, k := range []string{"cpu_usage", "disk_usage", "gpu_util", "mem_usage", "net_in"} {
		m[k] = true
	}
	return m
}()

// isEntityWord 判断关键词是否为实体名（设备/指标/alarm id）。
func isEntityWord(kw string) bool {
	if entityWords[strings.ToLower(kw)] {
		return true
	}
	return alarmRe.MatchString(kw)
}

// RuleScore 规则打分：按 leaves 顺序匹配，命中实体关键词 0.9、主题词/复合规则 0.6，无命中 Unknown(0)。
// 选择语义与 RuleMatch 一致（同分时先出现者胜），仅附加置信度。
func RuleScore(leaves []model.LeafSpec, text string) ScoredIntent {
	if len(leaves) == 0 {
		leaves = SeedLeaves
	}
	low := strings.ToLower(text)
	best := ScoredIntent{Intent: Unknown, Confidence: 0}
	for _, l := range leaves {
		if l.Code == Unknown {
			continue
		}
		if l.Code == InspectAlert {
			if inspectAlertMatch(low) && best.Confidence < 0.6 {
				best = ScoredIntent{Intent: InspectAlert, Confidence: 0.6}
			}
			continue
		}
		for _, kw := range l.RuleKeywords {
			if kw == "" || !strings.Contains(low, strings.ToLower(kw)) {
				continue
			}
			conf := 0.6
			if isEntityWord(kw) {
				conf = 0.9
			}
			if conf > best.Confidence {
				best = ScoredIntent{Intent: l.Code, Confidence: conf}
			}
		}
		for _, phrase := range []string{l.Description, l.Path} {
			if phrase == "" {
				continue
			}
			if strings.Contains(low, strings.ToLower(phrase)) && best.Confidence < 0.6 {
				best = ScoredIntent{Intent: l.Code, Confidence: 0.6}
			}
		}
	}
	if best.Confidence < 0.4 {
		return ScoredIntent{Intent: Unknown, Confidence: 0}
	}
	return best
}

// RuleMatch 规则优先：等价 RuleScore(...).Intent（向后兼容）。
func RuleMatch(leaves []model.LeafSpec, text string) Intent {
	return RuleScore(leaves, text).Intent
}
```

注意：`intent.go` 需新增 import `aiops/internal/mockdata`（`agent.go` 已用，同包不冲突）。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agent/ -run TestRuleScore -v`，再 `go test ./internal/agent/ -run TestParseIntent -v` 确认既有规则识别不回归。
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add backend/internal/agent/intent.go backend/internal/agent/agent_test.go
git commit -m "feat: 意图规则识别新增RuleScore置信度打分"
```

---

### Task 4: LLM 置信度 + few-shot（ClassifyWithLight / ParseIntentWithLight）

**Files:**
- Modify: `backend/internal/agent/intent.go`
- Test: `backend/internal/agent/agent_test.go`

**Interfaces:**
- Consumes: Task 3 的 `ScoredIntent`/`RuleScore`。
- Produces:
  - `ClassifyWithLight(ctx, leaves, text, c) (Intent, float64)`：输出 `{"intent":"X","confidence":0~1}`，失败/非法 → `(Unknown, 0)`。
  - `ParseIntentWithLight(ctx, leaves, text, c) (Intent, float64)`：规则分 ≥0.4 用之，否则 LLM；LLM 分 <0.4 → `(Unknown, conf)`。

- [ ] **Step 1: 更新失败测试**

将 `agent_test.go` 的 `TestClassifyWithLight`、`TestParseIntentWithLight` 改为匹配新签名：

```go
func TestClassifyWithLight(t *testing.T) {
	c := &scriptedLLM{raw: `{"intent":"AskRootCause","confidence":0.9}`}
	got, conf := ClassifyWithLight(context.Background(), nil, "为什么磁盘满了", c)
	if got != AskRootCause || conf != 0.9 {
		t.Fatalf("ClassifyWithLight = (%v,%v), want (AskRootCause,0.9)", got, conf)
	}
	bad := &scriptedLLM{raw: `{"intent":"Nope","confidence":0.9}`}
	if got, _ := ClassifyWithLight(context.Background(), nil, "x", bad); got != Unknown {
		t.Fatalf("非法意图应为 Unknown, got %v", got)
	}
	fail := &scriptedLLM{err: errors.New("boom")}
	if got, _ := ClassifyWithLight(context.Background(), nil, "x", fail); got != Unknown {
		t.Fatalf("LLM 出错应为 Unknown, got %v", got)
	}
}

func TestParseIntentWithLight(t *testing.T) {
	c := &scriptedLLM{raw: `{"intent":"Nope","confidence":0.9}`}
	if got, _ := ParseIntentWithLight(context.Background(), nil, "alarm-1什么原因", c); got != AskRootCause {
		t.Fatalf("规则命中应为 AskRootCause, got %v", got)
	}
	if got, _ := ParseIntentWithLight(context.Background(), nil, "帮我看看今天天气", &scriptedLLM{raw: `{"intent":"ListAlerts","confidence":0.9}`}); got != ListAlerts {
		t.Fatalf("LLM 分类应为 ListAlerts, got %v", got)
	}
	if got, _ := ParseIntentWithLight(context.Background(), nil, "今天天气如何", nil); got != Unknown {
		t.Fatalf("nil completer 应回退 Unknown, got %v", got)
	}
	// LLM 低置信度 → 拦截为 Unknown
	if got, conf := ParseIntentWithLight(context.Background(), nil, "帮我看看今天天气", &scriptedLLM{raw: `{"intent":"ListAlerts","confidence":0.2}`}); got != Unknown || conf != 0.2 {
		t.Fatalf("低置信度应拦截为 Unknown, got (%v,%v)", got, conf)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent/ -run 'TestClassifyWithLight|TestParseIntentWithLight' -v`
Expected: `assignment mismatch` / 旧签名编译错误。

- [ ] **Step 3: 实现 ClassifyWithLight 置信度 + few-shot**

`backend/internal/agent/intent.go` 替换 `ClassifyWithLight`：

```go
// ClassifyWithLight 轻量 LLM 分类并自评分：闭集 = leaves 的 code+description+examples+path；
// 返回 (Intent, confidence)；失败/非法映射 (Unknown, 0)。
func ClassifyWithLight(ctx context.Context, leaves []model.LeafSpec, text string, c ctxpkg.Completer) (Intent, float64) {
	if len(leaves) == 0 {
		leaves = SeedLeaves
	}
	var parts []string
	for _, l := range leaves {
		desc := l.Description
		if desc == "" {
			desc = l.Code
		}
		s := l.Code + "(" + desc
		if len(l.Examples) > 0 {
			s += "，示例：" + strings.Join(l.Examples, "、")
		}
		if l.Path != "" {
			s += "，路径：" + l.Path
		}
		parts = append(parts, s+")")
	}
	system := "你是意图分类器。判断用户请求属于哪个意图，只输出 JSON：{\"intent\":\"<意图名>\",\"confidence\":<0到1之间的小数>}。意图名必须是 " + strings.Join(parts, "、") + "。无法确定时返回 {\"intent\":\"Unknown\",\"confidence\":0}。"
	raw, err := c.Complete(ctx, system, text)
	if err != nil || strings.TrimSpace(raw) == "" {
		return Unknown, 0
	}
	var out struct {
		Intent     string  `json:"intent"`
		Confidence float64 `json:"confidence"`
	}
	if err := llm.ParseJSON(raw, &out); err != nil {
		return Unknown, 0
	}
	for _, l := range leaves {
		if out.Intent == l.Code {
			if out.Confidence < 0 || out.Confidence > 1 {
				return Unknown, 0
			}
			return l.Code, out.Confidence
		}
	}
	return Unknown, 0
}
```

替换 `ParseIntentWithLight`：

```go
// ParseIntentWithLight 规则优先；规则分 <0.4 用轻量 LLM 分类；LLM 分 <0.4 落回 Unknown。
func ParseIntentWithLight(ctx context.Context, leaves []model.LeafSpec, text string, c ctxpkg.Completer) (Intent, float64) {
	sc := RuleScore(leaves, text)
	if sc.Confidence >= 0.4 {
		return sc.Intent, sc.Confidence
	}
	if c == nil || ctxpkg.IsNilCompleter(c) {
		return Unknown, 0
	}
	intent, conf := ClassifyWithLight(ctx, leaves, text, c)
	if conf < 0.4 {
		return Unknown, conf
	}
	return intent, conf
}
```

同时更新 `SeedLeaves` 每条加 `Examples`（与 store 种子一致，供 nil-provider 的 few-shot 使用）：

```go
{Code: AskRootCause, ..., Examples: []string{"alarm-123 什么原因", "这台机器为什么告警"}},
{Code: AskRecommendation, ..., Examples: []string{"alarm-123 怎么处理", "磁盘满了怎么办"}},
{Code: AskStatus, ..., Examples: []string{"排查进度如何", "现在什么状态"}},
{Code: InspectAlert, ..., Examples: []string{"查一下 compute-01", "看看 switch-core-01"}},
{Code: ListAlerts, ..., Examples: []string{"有哪些告警", "列出所有告警"}},
{Code: Unknown, ..., Examples: []string{}},
```

- [ ] **Step 4: 修复 agent.go 调用点**

`backend/internal/agent/agent.go` 的 `answerOneStatus`：

```go
intent, _ := ParseIntentWithLight(ctx, a.activeLeaves(), sub, a.lightLLM)
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/agent/ -v`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
git add backend/internal/agent/intent.go backend/internal/agent/agent.go backend/internal/agent/agent_test.go
git commit -m "feat: LLM意图分类返回置信度并注入description/examples/path做few-shot"
```

---

### Task 5: ParseIntentSet 返回 []ScoredIntent + eval 适配

**Files:**
- Modify: `backend/internal/agent/intent.go`
- Modify: `backend/internal/eval/helpers.go`
- Test: `backend/internal/agent/agent_test.go`、`backend/internal/eval/`（回归）

**Interfaces:**
- Consumes: Task 4 的 `ParseIntentWithLight (Intent, float64)`。
- Produces:
  - `ParseIntentSet(ctx, leaves, text, c, maxSplit) []ScoredIntent`。
  - `eval.helpers.intentNames(its []agent.ScoredIntent) []string`。

- [ ] **Step 1: 更新失败测试**

`agent_test.go` 的 `TestParseIntentSet` 改为：

```go
func TestParseIntentSet(t *testing.T) {
	c := &scriptedLLM{raw: `{"rewritten":"有哪些告警？alarm-1什么原因？","sub_questions":["有哪些告警","alarm-1什么原因"]}`}
	got := ParseIntentSet(context.Background(), nil, "有哪些告警？alarm-1什么原因？", c, 3)
	if len(got) != 2 || got[0].Intent != ListAlerts || got[1].Intent != AskRootCause {
		t.Fatalf("ParseIntentSet = %v", got)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent/ -run TestParseIntentSet -v`
Expected: 类型不匹配编译错误（`[]ScoredIntent` vs `[]Intent`）。

- [ ] **Step 3: 实现 ParseIntentSet 返回 []ScoredIntent**

`backend/internal/agent/intent.go` 替换 `ParseIntentSet`：

```go
// ParseIntentSet 解析一条输入的全部意图：重写拆分 → 逐子问题 ParseIntentWithLight，截断到 maxSplit。
func ParseIntentSet(ctx context.Context, leaves []model.LeafSpec, text string, c ctxpkg.Completer, maxSplit int) []ScoredIntent {
	if c == nil || ctxpkg.IsNilCompleter(c) {
		return []ScoredIntent{RuleScore(leaves, text)}
	}
	_, subs, err := RewriteAndSplit(ctx, text, c)
	if err != nil || len(subs) == 0 {
		return []ScoredIntent{RuleScore(leaves, text)}
	}
	if maxSplit <= 0 {
		maxSplit = 3
	}
	if len(subs) > maxSplit {
		subs = subs[:maxSplit]
	}
	out := make([]ScoredIntent, 0, len(subs))
	for _, s := range subs {
		intent, conf := ParseIntentWithLight(ctx, leaves, s, c)
		out = append(out, ScoredIntent{Intent: intent, Confidence: conf})
	}
	return out
}
```

- [ ] **Step 4: 适配 eval 调用点**

`backend/internal/eval/helpers.go` 改 `intentNames`：

```go
// intentNames 将子意图结果集转为字符串名（与运行时同一命名）。
func intentNames(its []agent.ScoredIntent) []string {
	out := make([]string, 0, len(its))
	for _, i := range its {
		out = append(out, agent.IntentName(i.Intent))
	}
	return out
}
```

`runner.go:155` 调用 `agent.ParseIntentSet(ctx, nil, t.ChatInput, light, 3)` 返回值类型已变，但 `intentNames` 签名同步变化后无需改动该行。

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/agent/ ./internal/eval/ -v`
Expected: PASS（含 `TestRunTaskChatMultiRuleFallback` 等回归）。

- [ ] **Step 6: Commit**

```bash
git add backend/internal/agent/intent.go backend/internal/agent/agent_test.go backend/internal/eval/helpers.go
git commit -m "feat: ParseIntentSet返回带置信度的结果并适配eval"
```

---

### Task 6: 前端 types + IntentEditor examples

**Files:**
- Modify: `frontend/src/types.ts`
- Modify: `frontend/src/components/IntentEditor.tsx`
- Modify: `frontend/src/components/IntentTree.test.tsx`
- Test: `frontend/src/components/IntentEditor.test.tsx`（新建）

**Interfaces:**
- Consumes: 后端已返回/接受 `examples` 字段（`IntentNode`/`IntentUpsert`）。
- Produces: `IntentNode.examples: string[]`、`IntentUpsert.examples: string[]`；`IntentEditor` 提交时携带 `examples`。

- [ ] **Step 1: 更新类型与测试夹具（失败）**

`frontend/src/types.ts`：`IntentNode` 与 `IntentUpsert` 各加 `examples: string[]`。

`frontend/src/components/IntentTree.test.tsx` 的 `leaf`/`group` 夹具各加 `examples: []`（满足 `tsc`）：

```ts
description: '', examples: [], enabled: true, ...
```

新建 `frontend/src/components/IntentEditor.test.tsx`：

```tsx
import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import IntentEditor from './IntentEditor'

describe('IntentEditor', () => {
  it('提交时解析示例并携带 examples', () => {
    const onSubmit = vi.fn()
    render(<IntentEditor groups={[]} onSubmit={onSubmit} onCancel={vi.fn()} />)
    fireEvent.change(screen.getByLabelText(/类型/), { target: { value: 'KB' } })
    fireEvent.change(screen.getByLabelText(/示例/), { target: { value: '怎么查手册, 手册在哪' } })
    fireEvent.click(screen.getByText('保存'))
    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ examples: ['怎么查手册', '手册在哪'] }),
    )
  })
})
```

- [ ] **Step 2: 运行测试确认失败**

Run: `npx vitest run src/components/IntentEditor.test.tsx`（在 `frontend/` 下）
Expected: 找不到「示例」输入框 / `examples` 未提交 → FAIL。

- [ ] **Step 3: 实现 IntentEditor**

`frontend/src/components/IntentEditor.tsx`：加状态与输入框，并在 `submit` 里携带：

```tsx
const [ex, setEx] = useState((initial?.examples ?? []).join(', '))
```

```tsx
const examples = ex.split(/[,，\n]/).map((s) => s.trim()).filter(Boolean)
```

```tsx
onSubmit({
  parent_id: parent, name, kind, code, bind_target: bind,
  rule_keywords: kw.split(/[,，]/).map((s) => s.trim()).filter(Boolean),
  description: desc, examples, sort_order: Number(sort) || 0,
})
```

在 `description` 输入框之后加：

```tsx
{kind !== 'GROUP' && <label>示例(逗号分隔) <input style={input} value={ex} onChange={(e) => setEx(e.target.value)} /></label>}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `npx vitest run`，再 `npm run build`
Expected: 全部 PASS，构建通过。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/types.ts frontend/src/components/IntentEditor.tsx frontend/src/components/IntentTree.test.tsx frontend/src/components/IntentEditor.test.tsx
git commit -m "feat: 意图编辑界面支持编辑examples"
```

---

### Task 7: 全量回归验证

**Files:** 无（验证 + 可能的修复提交）

**Interfaces:** 无新增。

- [ ] **Step 1: 后端全量测试**

Run: `go test ./...`（在 `backend/` 下）
Expected: PASS。

- [ ] **Step 2: 前端全量测试 + 构建**

Run: `npx vitest run && npm run build`（在 `frontend/` 下）
Expected: PASS。

- [ ] **Step 3: 修复任何回归后提交**

如无回归则跳过；如有，修复后：

```bash
git add -A
git commit -m "fix: 意图置信度改动回归修复"
```
