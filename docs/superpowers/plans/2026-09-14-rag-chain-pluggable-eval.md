# RAG 查询链路可插拔与变体矩阵评测 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把查询侧检索链路拆成可注册的 `Link` 管道，并让 `rageval` 一次运行多变体、横向对比收益。

**Architecture:** `searcher` 退化为「持有 Chain 的 Searcher」；`Chain` 按固定 Kind 顺序执行 Link；`QueryCtx` 承载环节间中间态；`Variant`（Kind→实现名+参数）经注册表装配 Chain。默认变体逐位复刻现行为。

**Tech Stack:** Go（无新依赖）、SQLite（modernc）、React/Vite（本期不动）、Phoenix REST（可选）。

## Global Constraints

- 后端模块 `aiops`，命令在 `backend/` 目录执行。
- 测试：`go test ./...`；单包 `go test ./internal/retrieve/`；单用例 `go test ./internal/retrieve/ -run TestName -v`。
- 静态检查仅 `go vet ./...`（无 golangci-lint）。
- 端到端评测：`make eval-ci`（阈值 0.9，默认跳过 Extended）。
- 注释/文档/commit 用中文；commit 前缀 `feat:`/`fix:`/`test:`/`docs:`/`chore:`。
- SQLite driver 名为 `"sqlite"`；`store.New` 已 `SetMaxOpenConns(1)`，不得新增连接。
- 默认变体必须与重构前逐位一致；既有 `searcher_test.go`、`internal/e2e/e2e_test.go`、`internal/eval/regression_test.go` 全绿。
- `Searcher` 公开接口 `SearchCases/SearchManuals` 不变。

---

### Task 1: Candidate 导出 + Variant 规格解析

**Files:**
- Modify: `backend/internal/retrieve/rerank.go`
- Modify: `backend/internal/retrieve/searcher.go`（仅类型引用，不改逻辑）
- Modify: `backend/internal/retrieve/rerank_test.go`、`backend/internal/retrieve/searcher_test.go`（`cand`→`Candidate`）
- Create: `backend/internal/retrieve/variant.go`
- Test: `backend/internal/retrieve/variant_test.go`

**Interfaces:**
- Consumes: 无。
- Produces:
  - `type Candidate struct { Chunk model.KbKnowledgeChunk; BM25, Vec, Signal, Recency, Final float64 }`
  - `type Variant struct { Name string; Steps map[string]string; Params map[string]map[string]string }`
  - `var CanonicalKinds []string`
  - `func BaselineVariant() Variant`
  - `func ParseVariantPatch(name, spec string, base Variant) (Variant, error)`
  - `func ParseVariants(spec string, named map[string]Variant) ([]Variant, error)`
  - `func ParseVariantsJSON(data []byte) ([]Variant, error)`

- [ ] **Step 1: 写失败测试**

创建 `backend/internal/retrieve/variant_test.go`：

```go
package retrieve

import "testing"

func TestBaselineVariantCoversAllKinds(t *testing.T) {
	v := BaselineVariant()
	if v.Name != "baseline" {
		t.Fatalf("name=%q", v.Name)
	}
	for _, k := range CanonicalKinds {
		if _, ok := v.Steps[k]; !ok {
			t.Fatalf("baseline 缺少 kind %q", k)
		}
	}
}

func TestParseVariantPatchStepsAndParams(t *testing.T) {
	got, err := ParseVariantPatch("x", "rerank=none,fuse=weighted,gate.floor=0.15", BaselineVariant())
	if err != nil {
		t.Fatal(err)
	}
	if got.Steps["rerank"] != "none" || got.Steps["fuse"] != "weighted" {
		t.Fatalf("steps=%v", got.Steps)
	}
	if got.Params["gate"]["floor"] != "0.15" {
		t.Fatalf("params=%v", got.Params)
	}
	if got.Steps["recall"] != "hybrid" { // 未覆盖项继承 base
		t.Fatalf("未覆盖项被清空: %v", got.Steps)
	}
}

func TestParseVariantPatchUnknownKind(t *testing.T) {
	if _, err := ParseVariantPatch("x", "nope=on", BaselineVariant()); err == nil {
		t.Fatal("未知 kind 应报错")
	}
}

func TestParseVariantsSplitsAndAutoNames(t *testing.T) {
	vs, err := ParseVariants("baseline;rerank=none;fuse=weighted", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 3 || vs[0].Name != "baseline" || vs[1].Name != "rerank=none" || vs[2].Name != "fuse=weighted" {
		t.Fatalf("variants=%+v", vs)
	}
	if vs[1].Steps["rerank"] != "none" {
		t.Fatalf("patch 未生效: %+v", vs[1])
	}
}

func TestParseVariantsJSON(t *testing.T) {
	data := []byte(`[{"name":"no-rerank","steps":{"rerank":"none"},"params":{"gate":{"floor":"0.15"}}}]`)
	vs, err := ParseVariantsJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Name != "no-rerank" || vs[0].Steps["rerank"] != "none" || vs[0].Params["gate"]["floor"] != "0.15" {
		t.Fatalf("variants=%+v", vs)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/retrieve/ -run TestParseVariant -v`
Expected: FAIL，`undefined: ParseVariantPatch` / `undefined: BaselineVariant`。

- [ ] **Step 3: 重命名 `cand` → `Candidate`**

在 `backend/internal/retrieve/rerank.go` 将 `type cand struct` 改为导出 `type Candidate struct`，并把该文件内全部 `cand` 引用（`[]cand`、`cand{`、`func(c cand)`、`func(cands []cand`）替换为 `Candidate`。
在 `backend/internal/retrieve/searcher.go` 把 `dualRecall`/`applyRerank`/`gateB`/`hasRelevance` 签名与局部 `cand{` 改为 `Candidate`。
在 `backend/internal/retrieve/rerank_test.go`、`backend/internal/retrieve/searcher_test.go` 同步 `cand`→`Candidate`（含 `mkCand` 返回类型）。
不改任何控制流。

- [ ] **Step 4: 实现 variant.go**

创建 `backend/internal/retrieve/variant.go`：

```go
package retrieve

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// CanonicalKinds 链路环节规范顺序；变体按 Kind 覆盖，装配固定按此序。
var CanonicalKinds = []string{"fingerprint", "rewrite", "recall", "fuse", "relevance", "rerank", "gate"}

// Variant 一组「Kind → 实现名(+参数)」选择；Name 进报告与 Phoenix experiment 名。
type Variant struct {
	Name   string
	Steps  map[string]string
	Params map[string]map[string]string
}

func canonicalKind(k string) bool {
	for _, c := range CanonicalKinds {
		if c == k {
			return true
		}
	}
	return false
}

// BaselineVariant 返回与重构前行为一致的默认变体。
func BaselineVariant() Variant {
	return Variant{
		Name: "baseline",
		Steps: map[string]string{
			"fingerprint": "exact", "rewrite": "passthrough", "recall": "hybrid",
			"fuse": "rrf", "relevance": "default", "rerank": "openai", "gate": "floor",
		},
	}
}

// ParseVariantPatch 解析 "rerank=none,fuse=weighted,gate.floor=0.15" 并叠加到 base（不改 base）。
func ParseVariantPatch(name, spec string, base Variant) (Variant, error) {
	out := cloneVariant(base)
	if name != "" {
		out.Name = name
	}
	for _, tok := range strings.Split(spec, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		key, val, ok := strings.Cut(tok, "=")
		if !ok || val == "" {
			return Variant{}, fmt.Errorf("非法变体项 %q（应为 kind=impl 或 kind.knob=value）", tok)
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if k, knob, hasKnob := strings.Cut(key, "."); hasKnob {
			if !canonicalKind(k) {
				return Variant{}, fmt.Errorf("未知环节 %q", k)
			}
			if out.Params == nil {
				out.Params = map[string]map[string]string{}
			}
			if out.Params[k] == nil {
				out.Params[k] = map[string]string{}
			}
			out.Params[k][knob] = val
			continue
		}
		if !canonicalKind(key) {
			return Variant{}, fmt.Errorf("未知环节 %q", key)
		}
		out.Steps[key] = val
	}
	return out, nil
}

// ParseVariants 解析 ";" 分隔的变体串；含 "=" 的条目 = 在 baseline 上打补丁并自动命名。
// 不含 "=" 的条目按 named 查表；缺失时报错。
func ParseVariants(spec string, named map[string]Variant) ([]Variant, error) {
	base := BaselineVariant()
	var out []Variant
	for _, entry := range strings.Split(spec, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "=") {
			v, err := ParseVariantPatch(entry, entry, base)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
			continue
		}
		if v, ok := named[entry]; ok {
			out = append(out, cloneVariant(v))
			continue
		}
		if entry == "baseline" {
			out = append(out, cloneVariant(base))
			continue
		}
		return nil, fmt.Errorf("未知具名变体 %q", entry)
	}
	if len(out) == 0 {
		out = []Variant{cloneVariant(base)}
	}
	return out, nil
}

// ParseVariantsJSON 解析 -variants-file JSON 数组；未覆盖项继承 baseline。
func ParseVariantsJSON(data []byte) ([]Variant, error) {
	var raw []Variant
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make([]Variant, 0, len(raw))
	for _, v := range raw {
		if v.Name == "" {
			return nil, fmt.Errorf("变体缺少 name")
		}
		merged := cloneVariant(BaselineVariant())
		merged.Name = v.Name
		for k, impl := range v.Steps {
			if !canonicalKind(k) {
				return nil, fmt.Errorf("变体 %q 未知环节 %q", v.Name, k)
			}
			merged.Steps[k] = impl
		}
		if len(v.Params) > 0 {
			merged.Params = v.Params
		}
		out = append(out, merged)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func cloneVariant(v Variant) Variant {
	steps := make(map[string]string, len(v.Steps))
	for k, s := range v.Steps {
		steps[k] = s
	}
	params := map[string]map[string]string{}
	for k, m := range v.Params {
		cp := map[string]string{}
		for kk, vv := range m {
			cp[kk] = vv
		}
		params[k] = cp
	}
	return Variant{Name: v.Name, Steps: steps, Params: params}
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/retrieve/ -v`
Expected: PASS（含既有 `searcher_test.go`/`rerank_test.go`）。

- [ ] **Step 6: 提交**

```bash
git add backend/internal/retrieve/
git commit -m "feat: 导出 Candidate 并新增检索变体规格解析"
```

---

### Task 2: 链路核心（Link/QueryCtx/Chain）+ searchEnv 拆分 + 7 内置 Link

**Files:**
- Create: `backend/internal/retrieve/link.go`
- Create: `backend/internal/retrieve/chain.go`
- Create: `backend/internal/retrieve/link_fingerprint.go`
- Create: `backend/internal/retrieve/link_rewrite.go`
- Create: `backend/internal/retrieve/link_recall.go`
- Create: `backend/internal/retrieve/link_fuse.go`
- Create: `backend/internal/retrieve/link_relevance.go`
- Create: `backend/internal/retrieve/link_rerank.go`
- Create: `backend/internal/retrieve/link_gate.go`
- Modify: `backend/internal/retrieve/searcher.go`（拆出 `searchEnv`，`search()` 改为跑 Chain）
- Modify: `backend/internal/retrieve/query_rewrite.go`（保持不变，被 rewrite Link 调用）
- Test: `backend/internal/retrieve/chain_test.go`

**Interfaces:**
- Consumes: `Candidate`、`Variant`（Task 1）。
- Produces:
  - `type QueryCtx struct { Raw Query; Text string; Options Options; Category model.KBCategory; IsManual bool; ShortCircuit bool; Fallback []Result; Cands []Candidate; Results []Result; RerankApplied bool; Trace []LinkTrace }`
  - `type LinkTrace struct { Kind, Impl string; DurationMS int64; Hit bool; Note string }`
  - `type Link interface { Kind() string; Run(ctx context.Context, qc *QueryCtx) error }`
  - `type Chain struct { links []chainLink }`；`func (c *Chain) Run(ctx context.Context, qc *QueryCtx) error`
  - `type searchEnv struct { ... }`（持有 store/embedder/reranker/cfg/BM25 索引态）
  - `func newSearchEnv(st *store.Store, embedder Embedder, rr Reranker, cfg config.Config) *searchEnv`
  - `func baselineChain(env *searchEnv) *Chain`
  - `func effectiveTopK(opts Options) int`

- [ ] **Step 1: 写失败测试**

创建 `backend/internal/retrieve/chain_test.go`：

```go
package retrieve

import (
	"context"
	"testing"
)

type stubLink struct {
	kind   string
	called bool
	run    func(*QueryCtx)
}

func (s *stubLink) Kind() string { return s.kind }
func (s *stubLink) Run(_ context.Context, qc *QueryCtx) error {
	s.called = true
	if s.run != nil {
		s.run(qc)
	}
	return nil
}

func TestChainRunsLinksInOrderAndStopsOnShortCircuit(t *testing.T) {
	a := &stubLink{kind: "recall"}
	b := &stubLink{kind: "fuse", run: func(qc *QueryCtx) { qc.ShortCircuit = true }}
	c := &stubLink{kind: "rerank"}
	ch := &Chain{links: []chainLink{{impl: "a", link: a}, {impl: "b", link: b}, {impl: "c", link: c}}}
	if err := ch.Run(context.Background(), &QueryCtx{}); err != nil {
		t.Fatal(err)
	}
	if !a.called || !b.called || c.called {
		t.Fatalf("短路失败 a=%v b=%v c=%v", a.called, b.called, c.called)
	}
}

func TestEffectiveTopK(t *testing.T) {
	if effectiveTopK(Options{}) != 3 || effectiveTopK(Options{TopK: 5}) != 5 {
		t.Fatal("effectiveTopK 错误")
	}
}

func TestBaselineChainHasAllKinds(t *testing.T) {
	ch := baselineChain(newSearchEnv(nil, nil, nil, configDefaultForTest()))
	got := map[string]bool{}
	for _, l := range ch.links {
		got[l.link.Kind()] = true
	}
	for _, k := range CanonicalKinds {
		if !got[k] {
			t.Fatalf("baseline 缺少 %q", k)
		}
	}
}
```

在文件末尾追加测试辅助（仅测试用）：

```go
func configDefaultForTest() (c config.Config) { return config.Default() }
```

并在 import 块加入 `"aiops/internal/config"`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/retrieve/ -run 'TestChain|TestEffectiveTopK|TestBaselineChain' -v`
Expected: FAIL，`undefined: Chain` / `BaselineChain` 等。

- [ ] **Step 3: 实现核心类型 `link.go`**

```go
package retrieve

import (
	"context"

	"aiops/internal/model"
)

// QueryCtx 链路环节间共享的受控中间态；每环节只读写自己的段。
type QueryCtx struct {
	Raw      Query
	Text     string
	Options  Options
	Category model.KBCategory
	IsManual bool

	ShortCircuit  bool
	Fallback      []Result
	Cands         []Candidate
	Results       []Result
	RerankApplied bool
	Trace         []LinkTrace
}

// LinkTrace 单环节运行痕迹，供评测报告逐环节对比。
type LinkTrace struct {
	Kind       string
	Impl       string
	DurationMS int64
	Hit        bool
	Note       string
}

// Link 链路环节；Kind 标识槽位。
type Link interface {
	Kind() string
	Run(ctx context.Context, qc *QueryCtx) error
}

func effectiveTopK(opts Options) int {
	if opts.TopK > 0 {
		return opts.TopK
	}
	return 3
}
```

- [ ] **Step 4: 实现 `chain.go`**

```go
package retrieve

import (
	"context"
	"time"
)

type chainLink struct {
	impl string
	link Link
}

// Chain 按固定顺序装配的 Link 管道；ShortCircuit 命中即停止后续。
type Chain struct {
	links []chainLink
}

// Run 依序执行 Link 并记录 LinkTrace；Link 返回错误则中断并向上传递。
func (c *Chain) Run(ctx context.Context, qc *QueryCtx) error {
	for _, cl := range c.links {
		if qc.ShortCircuit {
			return nil
		}
		start := time.Now()
		err := cl.link.Run(ctx, qc)
		qc.Trace = append(qc.Trace, LinkTrace{
			Kind: cl.link.Kind(), Impl: cl.impl,
			DurationMS: time.Since(start).Milliseconds(),
		})
		if err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 5: 拆分 `searchEnv` 并移入既有方法**

在 `searcher.go`：
1. 新增 `searchEnv`：

```go
type searchEnv struct {
	st         *store.Store
	embedder   Embedder
	reranker   Reranker
	cfg        config.Config
	now        func() time.Time
	mu         sync.Mutex
	idx        *BM25Index
	docCount   int
	idxVersion string
}

func newSearchEnv(st *store.Store, embedder Embedder, rr Reranker, cfg config.Config) *searchEnv {
	if cfg.RerankModel == "" {
		cfg.RerankModel = "rerank"
	}
	if cfg.RerankFloor == 0 {
		cfg.RerankFloor = 0.2
	}
	return &searchEnv{st: st, embedder: embedder, reranker: rr, cfg: cfg, now: time.Now}
}
```

2. 将 `searcher` 改为 `{ env *searchEnv; defaultChain *Chain }`；删除 `isFingerprintEnabled`/`isRRFEnabled`/`effectiveRerankFloor`。
3. 把 `ensureIndex`、`fingerprintFastPath`、`dualRecall`、`applyRerank` 四个方法**原样搬移**到 receiver `*searchEnv`（`s`→`e`，`s.st`→`e.st`、`s.embedder`→`e.embedder`、`s.reranker`→`e.reranker`、`s.cfg`→`e.cfg`、`s.mu/idx/docCount/idxVersion`→`e.*`）。
4. `searcher.search` 重写为：

```go
func (s *searcher) search(ctx context.Context, q Query, cat model.KBCategory, isManual bool, opts Options) ([]Result, error) {
	qc := &QueryCtx{Raw: q, Text: q.Text, Options: opts, Category: cat, IsManual: isManual}
	if err := s.defaultChain.Run(ctx, qc); err != nil {
		return nil, err
	}
	if qc.Results == nil {
		return []Result{}, nil
	}
	return qc.Results, nil
}
```

5. 构造函数：

```go
func NewSearcherWithConfig(st *store.Store, embedder Embedder, rr Reranker, cfg config.Config) Searcher {
	env := newSearchEnv(st, embedder, rr, cfg)
	return &searcher{env: env, defaultChain: baselineChain(env)}
}
```

- [ ] **Step 6: 实现 7 个 Link**

`link_fingerprint.go`：

```go
package retrieve

import "context"

type fingerprintLink struct {
	env  *searchEnv
	impl string // exact | off
}

func (l *fingerprintLink) Kind() string { return "fingerprint" }

func (l *fingerprintLink) Run(_ context.Context, qc *QueryCtx) error {
	if l.impl == "off" {
		return nil
	}
	res, hit, err := l.env.fingerprintFastPath(qc.Raw, qc.Category, qc.IsManual, effectiveTopK(qc.Options))
	if err != nil {
		return nil // 快路径失败静默降级为未命中
	}
	if hit {
		qc.Fallback = res
		qc.Results = res
		qc.ShortCircuit = true
	}
	return nil
}
```

`link_rewrite.go`：

```go
package retrieve

import "context"

type rewriteLink struct {
	env       *searchEnv
	completer Completer
	impl      string // passthrough | llm
}

func (l *rewriteLink) Kind() string { return "rewrite" }

func (l *rewriteLink) Run(ctx context.Context, qc *QueryCtx) error {
	if l.impl == "llm" && len(qc.Raw.History) > 0 && !IsNilCompleter(l.completer) {
		if out, err := RewriteQuery(ctx, l.completer, qc.Raw.History, qc.Text); err == nil && out != "" {
			qc.Text = out
		}
	}
	return nil
}
```

`link_recall.go`：

```go
package retrieve

import "context"

type recallLink struct {
	env  *searchEnv
	impl string // hybrid
}

func (l *recallLink) Kind() string { return "recall" }

func (l *recallLink) Run(ctx context.Context, qc *QueryCtx) error {
	if err := l.env.ensureIndex(qc.Raw.Version); err != nil {
		return err
	}
	q := qc.Raw
	q.Text = qc.Text
	qc.Cands = l.env.dualRecall(ctx, q, qc.IsManual, qc.Category)
	return nil
}
```

`link_fuse.go`：

```go
package retrieve

import "context"

type fuseLink struct {
	env  *searchEnv
	impl string // rrf | weighted
}

func (l *fuseLink) Kind() string { return "fuse" }

func (l *fuseLink) Run(_ context.Context, qc *QueryCtx) error {
	if l.impl == "weighted" {
		if qc.IsManual {
			qc.Cands = RankManuals(qc.Cands, l.env.now())
		} else {
			qc.Cands = RankCases(qc.Cands, l.env.now())
		}
		return nil
	}
	qc.Cands = FuseRRF(qc.Cands, qc.IsManual)
	return nil
}
```

`link_relevance.go`：

```go
package retrieve

import "context"

type relevanceLink struct{}

func (l *relevanceLink) Kind() string { return "relevance" }

func (l *relevanceLink) Run(_ context.Context, qc *QueryCtx) error {
	if !hasRelevance(qc.Cands) {
		qc.Results = []Result{}
		qc.ShortCircuit = true
	}
	return nil
}
```

`link_rerank.go`：

```go
package retrieve

import "context"

type rerankLink struct {
	env  *searchEnv
	impl string // openai | none
}

func (l *rerankLink) Kind() string { return "rerank" }

func (l *rerankLink) Run(ctx context.Context, qc *QueryCtx) error {
	if l.impl == "none" || l.env.reranker == nil {
		return nil
	}
	qc.Cands = l.env.applyRerank(ctx, qc.Text, qc.Cands)
	qc.RerankApplied = true
	return nil
}
```

`link_gate.go`：

```go
package retrieve

import "context"

type gateLink struct {
	env  *searchEnv
	impl string // floor
}

func (l *gateLink) Kind() string { return "gate" }

func (l *gateLink) Run(_ context.Context, qc *QueryCtx) error {
	if qc.RerankApplied {
		n := len(qc.Cands)
		if n > 20 {
			n = 20
		}
		if n > 0 {
			head := gateB(qc.Cands[:n], l.env.cfg.RerankFloor)
			qc.Cands = append(head, qc.Cands[n:]...)
		}
	}
	if topK := effectiveTopK(qc.Options); len(qc.Cands) > topK {
		qc.Cands = qc.Cands[:topK]
	}
	qc.Results = make([]Result, 0, len(qc.Cands))
	for _, c := range qc.Cands {
		qc.Results = append(qc.Results, Result{
			DocID: c.Chunk.DocID, Title: c.Chunk.Title, Category: c.Chunk.Category,
			Heading: c.Chunk.Heading, Content: c.Chunk.Content, Score: c.Chunk.Final,
			Intents: c.Chunk.Intents, SourceDocIDs: c.Chunk.SourceDocIDs,
		})
	}
	return nil
}
```

`baselineChain` 放在 `chain.go`：

```go
func baselineChain(env *searchEnv) *Chain {
	cfg := env.cfg
	fp, fuse, rr := "exact", "rrf", "openai"
	if !cfg.RetrieveFingerprint {
		fp = "off"
	}
	if !cfg.RetrieveRRF {
		fuse = "weighted"
	}
	if env.reranker == nil {
		rr = "none"
	}
	return &Chain{links: []chainLink{
		{impl: fp, link: &fingerprintLink{env: env, impl: fp}},
		{impl: "passthrough", link: &rewriteLink{env: env, impl: "passthrough"}},
		{impl: "hybrid", link: &recallLink{env: env, impl: "hybrid"}},
		{impl: fuse, link: &fuseLink{env: env, impl: fuse}},
		{impl: "default", link: &relevanceLink{}},
		{impl: rr, link: &rerankLink{env: env, impl: rr}},
		{impl: "floor", link: &gateLink{env: env, impl: "floor"}},
	}}
}
```

`gateB`/`hasRelevance`/`vectorRelevanceFloor` 保留在 `searcher.go`（被 gate/relevance Link 调用）；`Candidate` 的 `Final` 字段保持原语义。`Result.Score` 仍取 `c.Chunk.Final`（与旧 `c.Final` 相同字段）。

- [ ] **Step 7: 运行测试确认通过**

Run: `go test ./internal/retrieve/ -v`
Expected: PASS（新老用例全绿）。

- [ ] **Step 8: 提交**

```bash
git add backend/internal/retrieve/
git commit -m "feat: 检索链路拆为 Link 管道并保持默认行为"
```

---

### Task 3: 注册表 + BuildChain(Variant) + 配置映射

**Files:**
- Create: `backend/internal/retrieve/registry.go`
- Modify: `backend/internal/retrieve/chain.go`
- Modify: `backend/internal/retrieve/searcher.go`
- Modify: `backend/internal/config/config.go`
- Test: `backend/internal/retrieve/registry_test.go`
- Test: `backend/internal/config/config_test.go`（若不存在则新建）

**Interfaces:**
- Consumes: `Variant`、`Chain`、`searchEnv`、7 个 Link（Task 2）。
- Produces:
  - `type Deps struct { Env *searchEnv; Completer Completer }`
  - `type LinkFactory func(Deps) Link`
  - `func RegisterLink(kind, name string, f LinkFactory)`（panic on duplicate）
  - `func BuildChain(v Variant, deps Deps) (*Chain, error)`
  - `func NewSearcherVariant(st *store.Store, embedder Embedder, rr Reranker, cfg config.Config, completer Completer, v Variant) (Searcher, error)`
  - config: `func (c Config) RetrieveVariant(profile string) (Variant, error)` 等价物，返回 `(kind→impl)` 映射的 base/pipeline/chat；为保持 config 不依赖 retrieve，返回 `map[string]string` + `map[string]string`（params）。

- [ ] **Step 1: 写失败测试**

创建 `backend/internal/retrieve/registry_test.go`：

```go
package retrieve

import (
	"testing"

	"aiops/internal/config"
)

func TestBuildChainSelectsImplByVariant(t *testing.T) {
	env := newSearchEnv(nil, nil, nil, config.Default())
	v := BaselineVariant()
	v.Steps["rerank"] = "none"
	v.Steps["fuse"] = "weighted"
	ch, err := BuildChain(v, Deps{Env: env})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, l := range ch.links {
		got[l.link.Kind()] = l.impl
	}
	if got["rerank"] != "none" || got["fuse"] != "weighted" || got["recall"] != "hybrid" {
		t.Fatalf("got=%v", got)
	}
}

func TestBuildChainRejectsUnknownImpl(t *testing.T) {
	v := BaselineVariant()
	v.Steps["recall"] = "no-such-impl"
	if _, err := BuildChain(v, Deps{Env: newSearchEnv(nil, nil, nil, config.Default())}); err == nil {
		t.Fatal("未知实现应报错")
	}
}

func TestBuildChainSkipsNone(t *testing.T) {
	v := BaselineVariant()
	v.Steps["fingerprint"] = "none"
	ch, _ := BuildChain(v, Deps{Env: newSearchEnv(nil, nil, nil, config.Default())})
	for _, l := range ch.links {
		if l.link.Kind() == "fingerprint" {
			t.Fatal("none 应跳过该环节")
		}
	}
}
```

创建/追加 `backend/internal/config/retrieve_variant_test.go`：

```go
package config

import "testing"

func TestRetrieveVariantDefaults(t *testing.T) {
	c := Default()
	if got := c.RetrieveSteps("pipeline")["rewrite"]; got != "passthrough" {
		t.Fatalf("pipeline rewrite=%q", got)
	}
	if got := c.RetrieveSteps("chat")["rewrite"]; got != "llm" {
		t.Fatalf("chat rewrite=%q", got)
	}
	if got := c.RetrieveSteps("pipeline")["fuse"]; got != "rrf" {
		t.Fatalf("fuse=%q", got)
	}
}

func TestRetrieveVariantEnvOverride(t *testing.T) {
	c := LoadFromEnv(func(k string) string {
		switch k {
		case "RETRIEVE_VARIANT":
			return "rerank=none,fuse=weighted"
		case "RETRIEVE_VARIANT_CHAT":
			return "rewrite=passthrough"
		}
		return ""
	})
	if c.RetrieveSteps("pipeline")["rerank"] != "none" || c.RetrieveSteps("pipeline")["fuse"] != "weighted" {
		t.Fatalf("base=%v", c.RetrieveSteps("pipeline"))
	}
	if c.RetrieveSteps("chat")["rewrite"] != "passthrough" {
		t.Fatalf("chat=%v", c.RetrieveSteps("chat"))
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/retrieve/ -run TestBuildChain -v && go test ./internal/config/ -run TestRetrieveVariant -v`
Expected: FAIL，`undefined: BuildChain` / `c.RetrieveSteps undefined`。

- [ ] **Step 3: 实现注册表 `registry.go`**

```go
package retrieve

import "fmt"

// Deps 构造 Link 的依赖集合。
type Deps struct {
	Env       *searchEnv
	Completer Completer
}

// LinkFactory 按名构造 Link。
type LinkFactory func(Deps) Link

type linkKey struct{ kind, name string }

var linkRegistry = map[linkKey]LinkFactory{}

// RegisterLink 注册实现；重复注册 panic（编程错误）。
func RegisterLink(kind, name string, f LinkFactory) {
	k := linkKey{kind, name}
	if _, dup := linkRegistry[k]; dup {
		panic(fmt.Sprintf("重复注册 Link %s/%s", kind, name))
	}
	linkRegistry[k] = f
}

func init() {
	RegisterLink("fingerprint", "exact", func(d Deps) Link { return &fingerprintLink{env: d.Env, impl: "exact"} })
	RegisterLink("fingerprint", "off", func(d Deps) Link { return &fingerprintLink{env: d.Env, impl: "off"} })
	RegisterLink("rewrite", "passthrough", func(d Deps) Link { return &rewriteLink{env: d.Env, completer: d.Completer, impl: "passthrough"} })
	RegisterLink("rewrite", "llm", func(d Deps) Link { return &rewriteLink{env: d.Env, completer: d.Completer, impl: "llm"} })
	RegisterLink("recall", "hybrid", func(d Deps) Link { return &recallLink{env: d.Env, impl: "hybrid"} })
	RegisterLink("fuse", "rrf", func(d Deps) Link { return &fuseLink{env: d.Env, impl: "rrf"} })
	RegisterLink("fuse", "weighted", func(d Deps) Link { return &fuseLink{env: d.Env, impl: "weighted"} })
	RegisterLink("relevance", "default", func(Deps) Link { return &relevanceLink{} })
	RegisterLink("rerank", "openai", func(d Deps) Link { return &rerankLink{env: d.Env, impl: "openai"} })
	RegisterLink("rerank", "none", func(d Deps) Link { return &rerankLink{env: d.Env, impl: "none"} })
	RegisterLink("gate", "floor", func(d Deps) Link { return &gateLink{env: d.Env, impl: "floor"} })
}
```

- [ ] **Step 4: 实现 `BuildChain`（追加到 `chain.go`）**

```go
// BuildChain 按 CanonicalKinds 顺序装配；未指定用 baseline 默认；"none"/"off" 跳过。
func BuildChain(v Variant, deps Deps) (*Chain, error) {
	base := BaselineVariant()
	steps := base.Steps
	for k, impl := range v.Steps {
		steps[k] = impl
	}
	var links []chainLink
	for _, kind := range CanonicalKinds {
		impl := steps[kind]
		if impl == "none" || impl == "off" {
			continue
		}
		f, ok := linkRegistry[linkKey{kind, impl}]
		if !ok {
			return nil, fmt.Errorf("未注册的实现 %s/%s", kind, impl)
		}
		links = append(links, chainLink{impl: impl, link: f(deps)})
	}
	return &Chain{links: links}, nil
}
```

在 `chain.go` import 加 `"fmt"`。删除 `baselineChain`（Task 2 的临时函数）及其在 Step 7 测试中的用法，测试改用 `BuildChain(BaselineVariant(), ...)`。同时删掉 `chain_test.go` 中 `configDefaultForTest` 若不再需要（改为 `BuildChain` 调用）。

- [ ] **Step 5: 加 `NewSearcherVariant` 并改造构造器**

```go
func NewSearcherVariant(st *store.Store, embedder Embedder, rr Reranker, cfg config.Config, completer Completer, v Variant) (Searcher, error) {
	env := newSearchEnv(st, embedder, rr, cfg)
	ch, err := BuildChain(v, Deps{Env: env, Completer: completer})
	if err != nil {
		return nil, err
	}
	return &searcher{env: env, defaultChain: ch}, nil
}

func NewSearcherWithConfig(st *store.Store, embedder Embedder, rr Reranker, cfg config.Config) Searcher {
	env := newSearchEnv(st, embedder, rr, cfg)
	return &searcher{env: env, defaultChain: defaultChainFromEnv(env)}
}

// defaultChainFromEnv 由 cfg 单项构造 baseline（保持既有 NewSearcherWithConfig 语义）。
func defaultChainFromEnv(env *searchEnv) *Chain {
	v := BaselineVariant()
	if !env.cfg.RetrieveFingerprint {
		v.Steps["fingerprint"] = "off"
	}
	if !env.cfg.RetrieveRRF {
		v.Steps["fuse"] = "weighted"
	}
	if env.reranker == nil {
		v.Steps["rerank"] = "none"
	}
	ch, err := BuildChain(v, Deps{Env: env})
	if err != nil { // 不应发生：内置实现
		panic(err)
	}
	return ch
}
```

`NewSearcher` 保持调用 `NewSearcherWithConfig`。

- [ ] **Step 6: config 增加变体映射**

在 `Config` 结构体新增：
```go
	RetrieveRewrite string // RETRIEVE_REWRITE，默认 passthrough
	RetrieveRecall  string // RETRIEVE_RECALL，默认 hybrid
	RetrieveGate    string // RETRIEVE_GATE，默认 floor
	RetrieveVariant string // RETRIEVE_VARIANT，基础覆盖串
	RetrieveVariantPipeline string // RETRIEVE_VARIANT_PIPELINE
	RetrieveVariantChat     string // RETRIEVE_VARIANT_CHAT
```

在 `LoadFromEnv` 读取（带默认）：
```go
	rewrite := lookup("RETRIEVE_REWRITE")
	if rewrite == "" {
		rewrite = "passthrough"
	}
	recall := lookup("RETRIEVE_RECALL")
	if recall == "" {
		recall = "hybrid"
	}
	gate := lookup("RETRIEVE_GATE")
	if gate == "" {
		gate = "floor"
	}
```
并在返回结构体填充 `RetrieveRewrite: rewrite, RetrieveRecall: recall, RetrieveGate: gate, RetrieveVariant: lookup("RETRIEVE_VARIANT"), RetrieveVariantPipeline: lookup("RETRIEVE_VARIANT_PIPELINE"), RetrieveVariantChat: lookup("RETRIEVE_VARIANT_CHAT")`。

新增方法（config 包内，不依赖 retrieve）：
```go
// RetrieveSteps 返回某 profile 生效的 kind→impl 映射（单项 env → RETRIEVE_VARIANT → 路径覆盖）。
// profile: "pipeline" | "chat"；未知 profile 等价 pipeline。
func (c Config) RetrieveSteps(profile string) map[string]string {
	steps := map[string]string{
		"fingerprint": "exact", "rewrite": c.RetrieveRewrite, "recall": c.RetrieveRecall,
		"fuse": "rrf", "relevance": "default", "rerank": "openai", "gate": c.RetrieveGate,
	}
	if !c.RetrieveFingerprint {
		steps["fingerprint"] = "off"
	}
	if !c.RetrieveRRF {
		steps["fuse"] = "weighted"
	}
	if c.RerankBaseURL == "" {
		steps["rerank"] = "none"
	}
	applySpec := func(spec string) {
		for _, tok := range strings.Split(spec, ",") {
			tok = strings.TrimSpace(tok)
			if tok == "" || strings.Contains(tok, ".") {
				continue
			}
			k, v, ok := strings.Cut(tok, "=")
			if ok {
				steps[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
	}
	applySpec(c.RetrieveVariant)
	if profile == "chat" {
		steps["rewrite"] = "llm"
		applySpec(c.RetrieveVariantChat)
	} else {
		applySpec(c.RetrieveVariantPipeline)
	}
	return steps
}
```
在 config.go import 加 `"strings"`。

在 retrieve 侧新增由 config 构造 Variant 的桥接（`variant.go` 或 `chain.go`）：
```go
// VariantFromConfig 由 config 单项/覆盖串构造某 profile 的 Variant。
func VariantFromConfig(cfg config.Config, profile string, params map[string]map[string]string) Variant {
	steps := cfg.RetrieveSteps(profile)
	name := "baseline"
	if profile == "chat" {
		name = "chat"
	}
	return Variant{Name: name, Steps: steps, Params: params}
}
```
（`retrieve` 已 import config。）

- [ ] **Step 7: 运行测试确认通过**

Run: `go test ./internal/retrieve/ ./internal/config/ -v`
Expected: PASS。

- [ ] **Step 8: 提交**

```bash
git add backend/internal/retrieve/ backend/internal/config/
git commit -m "feat: 检索链路注册表、变体装配与配置映射"
```

---

### Task 4: profile/history 透传 + agent 改写迁移

**Files:**
- Modify: `backend/internal/retrieve/vector.go`（`Query` 增 `History`）
- Modify: `backend/internal/retrieve/searcher.go`（`Options` 增 `Profile`；searcher 持 profile chain map）
- Create: `backend/internal/retrieve/profile.go`
- Modify: `backend/internal/context/chunk.go`（`Query` 增 `Profile`/`History`）
- Modify: `backend/internal/context/assembler.go`（透传 Profile/History）
- Modify: `backend/internal/context/tools.go`（工具从 ctx 取 profile/history）
- Modify: `backend/internal/agent/agent.go`（Handle 注入 profile/history；删除 `rewriteWithHistory` 调用）
- Modify: `backend/internal/pipeline/engine.go`（Run 注入 profile）
- Modify: `backend/cmd/server/main.go`（按 profile 构造 searcher）
- Test: `backend/internal/retrieve/profile_test.go`
- Test: `backend/internal/agent/agent_test.go`（改既有 pipeline 不改写断言）

**Interfaces:**
- Consumes: `Variant`、`NewSearcherVariant`、config 的 `RetrieveSteps`（Task 3）。
- Produces:
  - `type Options struct { TopK int; Profile string }`
  - `type Query struct { ...; History []string }`
  - `func WithProfile(ctx context.Context, p string) context.Context`
  - `func ProfileFromCtx(ctx context.Context) string`
  - `func WithHistory(ctx context.Context, h []string) context.Context`
  - `func HistoryFromCtx(ctx context.Context) []string`
  - `func NewSearcherProfiles(st, embedder, rr, cfg, completer, params) (Searcher, error)`

- [ ] **Step 1: 写失败测试**

创建 `backend/internal/retrieve/profile_test.go`：

```go
package retrieve

import (
	"context"
	"testing"
)

func TestProfileCtxRoundTrip(t *testing.T) {
	ctx := WithProfile(context.Background(), "chat")
	if got := ProfileFromCtx(ctx); got != "chat" {
		t.Fatalf("profile=%q", got)
	}
	if got := ProfileFromCtx(context.Background()); got != "pipeline" {
		t.Fatalf("default=%q", got)
	}
}

func TestHistoryCtxRoundTrip(t *testing.T) {
	ctx := WithHistory(context.Background(), []string{"a", "b"})
	got := HistoryFromCtx(ctx)
	if len(got) != 2 || got[1] != "b" {
		t.Fatalf("history=%v", got)
	}
}

func TestProfileOfPrefersOption(t *testing.T) {
	ctx := WithProfile(context.Background(), "chat")
	if got := ProfileOf(ctx, Options{Profile: "pipeline"}); got != "pipeline" {
		t.Fatalf("got=%q", got)
	}
	if got := ProfileOf(ctx, Options{}); got != "chat" {
		t.Fatalf("got=%q", got)
	}
}
```

追加 agent 测试（在 `backend/internal/agent/agent_test.go`，沿用既有 fake searcher）：断言 pipeline 路径下 `History` 为空（即经 SearchCases 收到的 Query.History 长度为 0）。若文件已有 captureSearcher，则加：

```go
func TestPipelineSearchCarriesNoHistory(t *testing.T) {
	var qs []retrieve.Query
	rec := &captureQuerySearcher{qs: &qs}
	// 构造 pipeline Engine 并 SetSearcher(rec)，Run 一个 alert（复用文件内既有 runPipeline 辅助）
	// 断言 len(qs) > 0 且所有 q.History 为空
}
```

（若既有辅助不便复用，则以 `retrieve.Query{History: ...}` 单测 `ProfileOf` + history ctx 覆盖，并保留既有 `agent_test.go:579` 的 pipeline 检索断言。）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/retrieve/ -run 'TestProfile|TestHistory' -v`
Expected: FAIL，`undefined: WithProfile` 等。

- [ ] **Step 3: 实现 profile ctx**

创建 `backend/internal/retrieve/profile.go`：

```go
package retrieve

import "context"

type profileKey struct{}
type historyKey struct{}

func WithProfile(ctx context.Context, p string) context.Context {
	return context.WithValue(ctx, profileKey{}, p)
}

// ProfileFromCtx 取路径标识；缺省 "pipeline"。
func ProfileFromCtx(ctx context.Context) string {
	if p, ok := ctx.Value(profileKey{}).(string); ok && p != "" {
		return p
	}
	return "pipeline"
}

func WithHistory(ctx context.Context, h []string) context.Context {
	return context.WithValue(ctx, historyKey{}, h)
}

func HistoryFromCtx(ctx context.Context) []string {
	if h, ok := ctx.Value(historyKey{}).([]string); ok {
		return h
	}
	return nil
}

// ProfileOf 解析顺序：Options.Profile → ctx → "pipeline"。
func ProfileOf(ctx context.Context, opts Options) string {
	if opts.Profile != "" {
		return opts.Profile
	}
	return ProfileFromCtx(ctx)
}
```

- [ ] **Step 4: `Query` 增 `History`，`Options` 增 `Profile`**

`backend/internal/retrieve/vector.go` 的 `Query` 末尾加 `History []string // 对话历史，供改写环节`。
`backend/internal/retrieve/searcher.go` 的 `Options` 加 `Profile string`。

- [ ] **Step 5: searcher 按 profile 选 chain**

把 `searcher` 改为：
```go
type searcher struct {
	env     *searchEnv
	chains  map[string]*Chain
	fallback *Chain
}
```
`search` 开头：
```go
	ch := s.chainFor(ProfileOf(ctx, opts))
```
新增：
```go
func (s *searcher) chainFor(profile string) *Chain {
	if ch, ok := s.chains[profile]; ok && ch != nil {
		return ch
	}
	return s.fallback
}
```
`NewSearcherWithConfig`/`NewSearcherVariant` 返回值改为 `chains: map[string]*Chain{"pipeline": ch}, fallback: ch`。
新增：
```go
// NewSearcherProfiles 生产构造：pipeline/chat 各绑一个 chain。
func NewSearcherProfiles(st *store.Store, embedder Embedder, rr Reranker, cfg config.Config, completer Completer, params map[string]map[string]string) (Searcher, error) {
	env := newSearchEnv(st, embedder, rr, cfg)
	pipeline, err := BuildChain(VariantFromConfig(cfg, "pipeline", params), Deps{Env: env, Completer: completer})
	if err != nil {
		return nil, err
	}
	chat, err := BuildChain(VariantFromConfig(cfg, "chat", params), Deps{Env: env, Completer: completer})
	if err != nil {
		return nil, err
	}
	return &searcher{env: env, chains: map[string]*Chain{"pipeline": pipeline, "chat": chat}, fallback: pipeline}, nil
}
```

- [ ] **Step 6: 组装器/工具透传 Profile/History**

`backend/internal/context/chunk.go` 的 `Query` 加 `Profile string`、`History []string`。
`backend/internal/context/assembler.go` 的两处 `SearchCases/SearchManuals` 调用改为：
```go
		opts := retrieve.Options{TopK: casesTopK, Profile: q.Profile}
		res, err := a.search.SearchCases(context.Background(), retrieve.Query{
			Text: qText(q), Metric: q.Metric, Device: q.Device, Intents: q.Intents, Version: q.Version, History: q.History,
		}, opts)
```
（manuals 同）。
`backend/internal/agent/agent.go` 的 `buildAssembled` 与 line 281 的 `asm.Assemble(...)` 传 `Profile: "chat"`，并在 `answerOne`/`answerSingle` 能拿到 history 处传 `History`（见 Step 7）。
`backend/internal/context/tools.go` 的两个工具 Execute 改为：
```go
			return s.SearchCases(ctx, retrieve.Query{
				Text: strArg(args, "query"), Metric: strArg(args, "metric"), Device: strArg(args, "device"),
				History: retrieve.HistoryFromCtx(ctx),
			}, retrieve.Options{TopK: intArg(args, "top_k", 3), Profile: retrieve.ProfileFromCtx(ctx)})
```
（manual 同，加 `Profile`。）

- [ ] **Step 7: agent 停止自行改写，改为下传 history**

1. `Handle` 开头：`ctx = retrieve.WithProfile(ctx, "chat")`。
2. 删除 `answer` 中对 `a.rewriteWithHistory(ctx, history, subs[i])` 的调用，直接 `qText := subs[i]`；保留 `history := a.fetchHistory(...)`。
3. 将 history 透传到检索：`answerOne`/`answerOneStatus` 需要接收 history 并放入 `ctxpkg.Query.History`。最小改动：在 `answerOne`/`answerOneStatus` 增加 `history []string` 参数，构造 `Query{... History: history}`；对使用 `ctxpkg.Query`/assembler 的路径设置 `History`，对 ReAct 工具路径调用前用 `ctx = retrieve.WithHistory(ctx, history)`。
4. 删除 `rewriteWithHistory`（保留 `rewriteQuery` 若仍被测试引用；否则一并删除并更新 `agent_test.go`）。
5. `lightAdapter` 若仅 `rewriteWithHistory` 使用则删除。

> 实施提示：先 `grep -n "rewriteWithHistory\|rewriteQuery\|lightAdapter" backend/internal/agent/` 找全引用，逐处替换；`agent_test.go` 中针对改写的测试改为「注入 history 后断言 captureSearcher 收到 History」。

- [ ] **Step 8: pipeline 注入 profile**

`backend/internal/pipeline/engine.go` 的 `Run` 在 `st := &State{...}` 之后加：
```go
	ctx = retrieve.WithProfile(ctx, "pipeline")
```
（`retrieve` 已 import。）

- [ ] **Step 9: main.go 使用 profiles 构造**

`backend/cmd/server/main.go:72` 的 `searcher := retrieve.NewSearcherWithConfig(...)` 替换为：

```go
	var rewriteCompleter retrieve.Completer
	if light != nil {
		rewriteCompleter = lightAdapter{c: light}
	}
	searcher, err := retrieve.NewSearcherProfiles(st, embedder, reranker, cfg, rewriteCompleter, nil)
	if err != nil {
		log.Fatalf("searcher: %v", err)
	}
```

并在文件末尾（或 import 后）新增适配类型：

```go
// lightAdapter 把 ctxpkg.Completer(双参) 适配为 retrieve.Completer(单 prompt)。
type lightAdapter struct{ c ctxpkg.Completer }

func (l lightAdapter) Complete(ctx context.Context, prompt string) (string, error) {
	return l.c.Complete(ctx, "", prompt)
}
```

注意 `searcher` 原先用 `:=`，现在该变量同时被 `eng.SetSearcher`、`reg.Register`、`ag.SetSearcher` 使用，改动后仍为局部变量，无需其他调整。`light` 为 `*llm.Client`，实现 `ctxpkg.Completer`。

- [ ] **Step 10: 运行测试确认通过**

Run: `go test ./internal/retrieve/ ./internal/context/ ./internal/agent/ ./internal/pipeline/ -v`
Expected: PASS。若 `agent_test.go` 中改写相关用例需重写，按 Step 7 提示更新后必须绿。

- [ ] **Step 11: 提交**

```bash
git add backend/internal backend/cmd/server
git commit -m "feat: profile/history 透传并迁移查询改写为链路环节"
```

---

### Task 5: rageval 变体矩阵 + 对比报告

**Files:**
- Modify: `backend/cmd/rageval/main.go`
- Create: `backend/cmd/rageval/compare.go`
- Test: `backend/cmd/rageval/compare_test.go`

**Interfaces:**
- Consumes: `retrieve.Variant`、`retrieve.ParseVariants`/`ParseVariantsJSON`、`retrieve.NewSearcherVariant`、`retrieve.LinkTrace`、既有 `Compute*` 指标。
- Produces（package main）：
  - `type Metrics struct { MRR, NDCG, Recall, Precision, RejectRate float64 }`
  - `type VariantReport struct { Name string; Spec map[string]string; Overall Metrics; ByType map[string]Metrics; Items []goldenset.ItemResult }`
  - `type ComparisonReport struct { Meta CompareMeta; Variants []VariantReport; Deltas map[string]Metrics }`
  - `type CompareMeta struct { Timestamp time.Time; Golden string; Enhance bool; Baseline string }`
  - `func aggregate(items []goldenset.ItemResult) (Metrics, map[string]Metrics)`
  - `func delta(base, v Metrics) Metrics`

- [ ] **Step 1: 写失败测试**

创建 `backend/cmd/rageval/compare_test.go`：

```go
package main

import (
	"testing"

	"aiops/internal/goldenset"
)

func TestAggregateAndDelta(t *testing.T) {
	items := []goldenset.ItemResult{
		{ID: "q1", Type: "fact", Behavior: "hit", MRR: 1, NDCG: 1, Recall: 1, Precision: 1},
		{ID: "q2", Type: "fact", Behavior: "hit", MRR: 0, NDCG: 0, Recall: 0, Precision: 0},
		{ID: "q3", Type: "no_answer", Behavior: "reject", RejectOK: true},
	}
	overall, byType := aggregate(items)
	if overall.RejectRate != 1.0 {
		t.Fatalf("reject rate=%v", overall.RejectRate)
	}
	if byType["fact"].MRR != 0.5 {
		t.Fatalf("byType fact mrr=%v", byType["fact"].MRR)
	}
	d := delta(overall, Metrics{MRR: overall.MRR - 0.1})
	if d.MRR < 0.099 || d.MRR > 0.101 {
		t.Fatalf("delta=%v", d.MRR)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./cmd/rageval/ -run TestAggregateAndDelta -v`
Expected: FAIL，`undefined: aggregate`。

- [ ] **Step 3: 实现 `compare.go`**

```go
package main

import "aiops/internal/goldenset"

type Metrics struct {
	MRR        float64 `json:"mrr"`
	NDCG       float64 `json:"ndcg"`
	Recall     float64 `json:"recall"`
	Precision  float64 `json:"precision"`
	RejectRate float64 `json:"reject_rate"`
}

type VariantReport struct {
	Name    string                    `json:"name"`
	Spec    map[string]string         `json:"spec"`
	Overall Metrics                   `json:"overall"`
	ByType  map[string]Metrics        `json:"by_type,omitempty"`
	Items   []goldenset.ItemResult    `json:"items"`
}

type CompareMeta struct {
	Timestamp string `json:"ts"`
	Golden    string `json:"golden"`
	Enhance   bool   `json:"enhance"`
	Baseline  string `json:"baseline"`
}

type ComparisonReport struct {
	Meta     CompareMeta        `json:"meta"`
	Variants []VariantReport    `json:"variants"`
	Deltas   map[string]Metrics `json:"deltas"`
}

// aggregate 计算 overall 与 by_type 指标；hit 题参与 MRR/NDCG/Recall/Precision，reject 题参与 RejectRate。
func aggregate(items []goldenset.ItemResult) (Metrics, map[string]Metrics) {
	type acc struct {
		n, hit, reject, rejectOK int
		mrr, ndcg, rec, prec     float64
	}
	aggs := map[string]*acc{}
	total := &acc{}
	for _, it := range items {
		a := aggs[it.Type]
		if a == nil {
			a = &acc{}
			aggs[it.Type] = a
		}
		a.n++
		total.n++
		if it.Behavior == "reject" {
			a.reject++
			total.reject++
			if it.RejectOK {
				a.rejectOK++
				total.rejectOK++
			}
			continue
		}
		a.hit++
		total.hit++
		a.mrr += it.MRR
		a.ndcg += it.NDCG
		a.rec += it.Recall
		a.prec += it.Precision
		total.mrr += it.MRR
		total.ndcg += it.NDCG
		total.rec += it.Recall
		total.prec += it.Precision
	}
	toMetrics := func(a *acc) Metrics {
		m := Metrics{}
		if a.hit > 0 {
			m.MRR = a.mrr / float64(a.hit)
			m.NDCG = a.ndcg / float64(a.hit)
			m.Recall = a.rec / float64(a.hit)
			m.Precision = a.prec / float64(a.hit)
		}
		if a.reject > 0 {
			m.RejectRate = float64(a.rejectOK) / float64(a.reject)
		}
		return m
	}
	byType := map[string]Metrics{}
	for k, a := range aggs {
		byType[k] = toMetrics(a)
	}
	return toMetrics(total), byType
}

func delta(base, v Metrics) Metrics {
	return Metrics{
		MRR: v.MRR - base.MRR, NDCG: v.NDCG - base.NDCG, Recall: v.Recall - base.Recall,
		Precision: v.Precision - base.Precision, RejectRate: v.RejectRate - base.RejectRate,
	}
}
```

- [ ] **Step 3b: golden `Item` 增加 `History`（Task 5 需要，先于 Phoenix）**

`backend/internal/goldenset/goldenset.go` 的 `Item` 加 `History []string \`json:"history"\``；`normalize` 保持缺省 nil（不改）。测试：

```go
func TestItemHistoryParsed(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "g.jsonl")
	if err := os.WriteFile(p, []byte(`{"id":"c1","type":"fact","question":"它怎么办","history":["GPU 掉卡了"],"expect":{"doc_ids":["d1"],"behavior":"hit"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	items, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].History) != 1 || items[0].History[0] != "GPU 掉卡了" {
		t.Fatalf("history=%v", items)
	}
}
```

（`goldenset_test.go` 若缺 `path/filepath`、`os` import 则补齐。）

- [ ] **Step 3c: rageval 侧 `retrieve.Completer` 适配**

rageval 现有 `llmCompleter` 是 `kb.Completer`（双参）。新增本地适配器（`main.go` 内）：

```go
type rewriteAdapter struct{ c *llm.Client }

func (r rewriteAdapter) Complete(ctx context.Context, prompt string) (string, error) {
	return r.c.Complete(ctx, prompt)
}
```

在构造 searcher 时传 `rewriteAdapter{c: lc}`（`lc` 为 `llm.NewClient(cfg)` 返回值，可能 nil；nil 时传 `nil`）。注意 `llm.Client.Complete` 签名为 `(ctx, prompt)`（`backend/internal/llm/llm.go:54`）；若签名不同，按实际调整适配器。

- [ ] **Step 4: 改造 `main.go` 为多变体循环**

保留 `resolveGoldenPath` 与缺失 id 校验逻辑，将「单变体逐题」段落替换为：

```go
	variantsFlag := flag.String("variants", "", "变体串，如 baseline;rerank=none;fuse=weighted")
	variantsFile := flag.String("variants-file", "", "变体 JSON 文件")
	// ... flag.Parse() 之后 ...
	named := map[string]retrieve.Variant{}
	if *variantsFile != "" {
		data, err := os.ReadFile(*variantsFile)
		if err != nil { fmt.Fprintf(os.Stderr, "读变体文件失败: %v\n", err); os.Exit(1) }
		vs, err := retrieve.ParseVariantsJSON(data)
		if err != nil { fmt.Fprintf(os.Stderr, "解析变体文件失败: %v\n", err); os.Exit(1) }
		for _, v := range vs { named[v.Name] = v }
	}
	variants, err := retrieve.ParseVariants(*variantsFlag, named)
	if err != nil { fmt.Fprintf(os.Stderr, "解析变体失败: %v\n", err); os.Exit(1) }
	// 保证 baseline 作为对比基准被评估
	if !hasVariant(variants, "baseline") {
		b, _ := retrieve.ParseVariantPatch("baseline", "", retrieve.BaselineVariant())
		variants = append([]retrieve.Variant{b}, variants...)
	}

	var reports []VariantReport
	var rewriteC retrieve.Completer
	if lc != nil {
		rewriteC = rewriteAdapter{c: lc}
	}
	for _, v := range variants {
		searcher, err := retrieve.NewSearcherVariant(s, embedder, reranker, cfg, rewriteC, v)
		if err != nil { fmt.Fprintf(os.Stderr, "构造变体 %s 失败: %v\n", v.Name, err); os.Exit(1) }
		var itemResults []goldenset.ItemResult
		for _, it := range items {
			k := it.TopK
			if k <= 0 { k = 3 }
			ctx := context.Background()
			qStart := time.Now()
			res, err := searcher.SearchCases(ctx, retrieve.Query{Text: it.Question, History: it.History}, retrieve.Options{TopK: k})
			qEnd := time.Now()
			if err != nil { res = nil }
			// ... 生成 ranked / ir（含 Traces，见 Task 6）与既有指标计算（MRR/NDCG/Recall/Precision/RejectOK）...
			itemResults = append(itemResults, ir)
		}
		overall, byType := aggregate(itemResults)
		reports = append(reports, VariantReport{Name: v.Name, Spec: v.Steps, Overall: overall, ByType: byType, Items: itemResults})
	}

	base := reports[0].Overall
	cmp := ComparisonReport{Meta: CompareMeta{
		Timestamp: time.Now().Format(time.RFC3339), Golden: goldenPath, Enhance: *enhance, Baseline: reports[0].Name,
	}, Variants: reports, Deltas: map[string]Metrics{}}
	for _, r := range reports[1:] {
		cmp.Deltas[r.Name] = delta(base, r.Overall)
	}
	// stdout：每变体一行 + delta
	printComparison(cmp)
	if *out != "" {
		b, _ := json.MarshalIndent(cmp, "", "  ")
		if err := os.WriteFile(*out, b, 0o644); err != nil { fmt.Fprintf(os.Stderr, "写报告失败: %v\n", err); os.Exit(1) }
	}
```

新增辅助（同文件）：
```go
func hasVariant(vs []retrieve.Variant, name string) bool {
	for _, v := range vs { if v.Name == name { return true } }
	return false
}

func printComparison(c ComparisonReport) {
	fmt.Printf("rageval: %s baseline=%s\n", c.Meta.Golden, c.Meta.Baseline)
	fmt.Printf("%-24s %6s %8s %8s %10s %8s\n", "variant", "MRR", "NDCG", "Recall", "Precision", "Reject")
	for _, v := range c.Variants {
		m := v.Overall
		fmt.Printf("%-24s %6.3f %8.3f %8.3f %10.3f %8.3f\n", v.Name, m.MRR, m.NDCG, m.Recall, m.Precision, m.RejectRate)
	}
	if len(c.Deltas) > 0 {
		fmt.Printf("-- delta vs %s --\n", c.Meta.Baseline)
		for name, d := range c.Deltas {
			fmt.Printf("%-24s ΔMRR %+.3f ΔNDCG %+.3f ΔRecall %+.3f ΔPrec %+.3f ΔReject %+.3f\n", name, d.MRR, d.NDCG, d.Recall, d.Precision, d.RejectRate)
		}
	}
}
```

构建 reranker：在 seed 后加
```go
	var reranker retrieve.Reranker
	if rr, err := retrieve.NewReranker(cfg); err == nil {
		reranker = rr
	}
```
并把 `searcher := retrieve.NewSearcher(s, embedder)` 一行删除（由循环内构造替代）。

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./cmd/rageval/ -v`
Expected: PASS（`TestAggregateAndDelta` 及既有指标测试）。

- [ ] **Step 6: 手工冒烟**

Run: `cd backend && go run ./cmd/rageval -variants "baseline;rerank=none;fuse=weighted"`
Expected: 打印 3 行变体与 delta；无 LLM key 时 rerank/embed 缺失仍可跑（向量/精排通道降级）。

- [ ] **Step 7: 提交**

```bash
git add backend/cmd/rageval/
git commit -m "feat: rageval 支持多变体矩阵与对比报告"
```

---

### Task 6: golden history + 逐变体 Phoenix 回写

**Files:**
- Modify: `backend/internal/goldenset/report.go`（`ItemResult.Traces`、`RagMeta.Variant/VariantSpec`、`PushRagExperiment` 命名）
- Modify: `backend/cmd/rageval/main.go`（逐变体回写 Phoenix；写 `Traces`）
- Modify: `backend/internal/retrieve/searcher.go`（TraceSink）
- Test: `backend/internal/goldenset/report_test.go`

**Interfaces:**
- Consumes: Task 5 的 `VariantReport`。
- Produces:
  - `goldenset.Item.History []string`
  - `goldenset.ItemResult.Traces []retrieve.LinkTrace`（为避免 goldenset→retrieve 依赖，定义本地 `goldenset.LinkTrace` 并由 rageval 转换）
  - `goldenset.RagMeta.Variant string`、`VariantSpec map[string]string`
  - `func PushRagExperiment(w phoenix.Writer, datasetID string, report RagReport, namePrefix string) (phoenix.ExperimentSummary, error)`（实验名改为 `namePrefix`，由调用方拼变体）

- [ ] **Step 1: 写失败测试**

在 `backend/internal/goldenset/report_test.go` 追加：

```go
func TestPushRagExperimentUsesGivenName(t *testing.T) {
	w := &fakeWriter{}
	_, err := PushRagExperiment(w, "DS", RagReport{Meta: RagMeta{Variant: "rerank=none"}}, "aiops-rag-golden/rerank=none")
	if err != nil {
		t.Fatal(err)
	}
	if w.lastExperimentName != "aiops-rag-golden/rerank=none" {
		t.Fatalf("name=%q", w.lastExperimentName)
	}
}
```

（若 `fakeWriter` 未记录实验名，加 `lastExperimentName string` 字段并在 `CreateExperiment` 中保存。）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/goldenset/ -run 'TestPushRagExperimentUsesGivenName' -v`
Expected: FAIL。

- [ ] **Step 3: goldenset 字段与命名**

（`Item.History` 已在 Task 5 Step 3b 加入，此处不动。）
`report.go`：
- 加 `type LinkTrace struct { Kind, Impl string; DurationMS int64; Hit bool; Note string }`
- `ItemResult` 加 `Traces []LinkTrace \`json:"traces,omitempty"\``
- `RagMeta` 加 `Variant string \`json:"variant"\``、`VariantSpec map[string]string \`json:"variant_spec,omitempty"\``
- `PushRagExperiment` 中实验名 `namePrefix+"/rag"` 改为直接用 `namePrefix`；metadata 加 `"variant": report.Meta.Variant`。

**兼容修正**：`backend/cmd/datasetctl/main.go:164` 与 `backend/internal/goldenset/report_test.go:81,152` 依赖旧命名 `prefix+"/rag"`。同步更新：
- `datasetctl`：`goldenset.PushRagExperiment(c, dsID, rep, prefix+"/rag")`。
- `report_test.go`：把期望的实验名与调用参数改为显式包含变体/`rag` 后缀（如 `"aiops-rag-golden/rag"`），确保测试反映新语义。

- [ ] **Step 4: rageval 写 Traces 并回写 Phoenix**

在 Task 5 的逐题循环里，把检索返回前的 `QueryCtx.Trace` 拿到：`NewSearcherVariant` 的 `Searcher` 接口不暴露 Trace。改为在该循环内对每个变体用 `retrieve.SearchWithTrace(ctx, searcher, ...)`？——为免改接口，采用简单方案：在 `retrieve` 增加可选的 trace 回调：

```go
// TraceSink 供评测收集每环节轨迹（可选）。
type TraceSink func(traces []LinkTrace)
func WithTraceSink(ctx context.Context, sink TraceSink) context.Context
func traceSinkFromCtx(ctx context.Context) TraceSink
```
在 `searcher.search` 的 `defaultChain.Run` 后调用 `if sink := traceSinkFromCtx(ctx); sink != nil { sink(qc.Trace) }`。

rageval 循环内：
```go
			var traces []retrieve.LinkTrace
			ctx := retrieve.WithTraceSink(context.Background(), func(t []retrieve.LinkTrace) { traces = t })
			// ... SearchCases(ctx, ...)
			// ir.Traces = convertTraces(traces)
```
新增转换：
```go
func convertTraces(in []retrieve.LinkTrace) []goldenset.LinkTrace {
	out := make([]goldenset.LinkTrace, 0, len(in))
	for _, t := range in {
		out = append(out, goldenset.LinkTrace{Kind: t.Kind, Impl: t.Impl, DurationMS: t.DurationMS, Hit: t.Hit, Note: t.Note})
	}
	return out
}
```

Phoenix 回写（循环后）：
```go
	if cfg.PhoenixBaseURL != "" {
		pc := phoenix.NewClient(cfg.PhoenixBaseURL, cfg.PhoenixAPIKey)
		dsID, err := findRagDataset(pc)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Phoenix 数据集解析失败，跳过回写: %v\n", err)
		} else {
			for _, r := range reports {
				rep := goldenset.RagReport{
					Meta: goldenset.RagMeta{Timestamp: time.Now(), Enhance: *enhance, TopK: 3, Variant: r.Name, VariantSpec: r.Spec},
					Items: r.Items, Overall: toOverall(r),
				}
				sum, err := goldenset.PushRagExperiment(pc, dsID, rep, goldenset.RagDatasetName+"/"+r.Name)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Phoenix 回写变体 %s: %v (%d failures)\n", r.Name, err, sum.Failures)
					continue
				}
				fmt.Printf("Phoenix 变体 %s → experiment %s\n", r.Name, sum.ID)
			}
		}
	}
```
另需 `toOverall(r VariantReport) goldenset.Overall` 与 `findRagDataset(pc *phoenix.Client) (string, error)`（`ListDatasets` 按名匹配，未找到提示先 `datasetctl push`）。

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/goldenset/ ./cmd/rageval/ -v`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add backend/internal/goldenset/ backend/cmd/rageval/ backend/internal/retrieve/
git commit -m "feat: golden history、链路轨迹与 Phoenix 逐变体回写"
```

---

### Task 7: 文档、环境变量与全量回归

**Files:**
- Modify: `README.md`（环境变量表新增 `RETRIEVE_*`）
- Modify: `.env.example`
- Modify: `backend/.env.example`（若存在）
- Modify: `AGENTS.md`（检索管线段落补充 Link/变体与 rageval `-variants`）

- [ ] **Step 1: 更新文档**

在 README 环境变量表新增：`RETRIEVE_REWRITE`（默认 `passthrough`）、`RETRIEVE_RECALL`（`hybrid`）、`RETRIEVE_GATE`（`floor`）、`RETRIEVE_VARIANT`、`RETRIEVE_VARIANT_PIPELINE`、`RETRIEVE_VARIANT_CHAT`；检索小节补充 rageval `-variants` / `-variants-file` 用法示例。
在 `.env.example` 增加同名注释示例。
在 `AGENTS.md` 「检索」条目补充「查询侧链路拆为 Link 管道 + 变体注册表；rageval 支持 `-variants` 矩阵对比」。

- [ ] **Step 2: 全量测试**

Run: `cd backend && go vet ./... && go test ./...`
Expected: 全绿。

- [ ] **Step 3: 评测门禁**

Run: `cd backend && make eval-ci`
Expected: 通过（阈值 0.9）；默认变体不导致指标回退。

- [ ] **Step 4: 默认行为等价性抽查**

Run: `cd backend && go run ./cmd/rageval`（不传 `-variants`）
Expected: 单变体 `baseline` 输出，数值与重构前同一 seed/golden 一致（可对照改动前记录）。

- [ ] **Step 5: 提交**

```bash
git add README.md .env.example backend/.env.example AGENTS.md
git commit -m "docs: 补充检索变体配置与 rageval 矩阵用法"
```

---

## Self-Review

**Spec coverage：**
- §3 架构/文件表 → Task 2、3。
- §4 Link/QueryCtx/Chain/注册表 → Task 2、3。
- §5 七内置环节 → Task 2。
- §6 变体规格与配置（CLI/JSON/生产 env）→ Task 1、3、5。
- §7 profile/history + 改写迁移 → Task 4。
- §8 rageval 矩阵 → Task 5。
- §9 报告与 Phoenix → Task 5、6。
- §10 字段变更 → Task 4（Query/Options）、Task 6（golden）。
- §11 兼容性 → Task 2（保留函数）、Task 4（接口不变）、Task 7（回归）。
- §12 错误处理 → Task 3 fail-fast、Task 2 静默降级。
- §13 测试计划 → 各 Task 测试 + Task 7 全量。
- §14 成功标准 → Task 5 冒烟、Task 7 门禁。
- §15 不做项 → 无对应任务（符合）。

**Placeholder scan：** 无 TBD/TODO；机械搬移以「原样搬移 + receiver 重命名」精确指令给出。

**Type consistency：** `Candidate`（Task 1 定义，Task 2 使用）；`Link`/`QueryCtx`/`Chain`（Task 2 定义，Task 3 使用）；`Variant`/`BuildChain`/`Deps`（Task 1/3），`ProfileOf`/`WithHistory`（Task 4），`Metrics`/`aggregate`/`delta`（Task 5），`goldenset.LinkTrace`/`RagMeta.Variant`（Task 6）。命名跨任务一致。
