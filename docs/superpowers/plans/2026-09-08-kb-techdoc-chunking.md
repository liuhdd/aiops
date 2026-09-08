# 技术操作文档切块与版本引用 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 Markdown 技术操作文档在 RAG 入库时保持步骤流程原子性、保留文档级/块级版本、物化跨文档引用并内联 `![[embed]]`。

**Architecture:** `kb` 包内增强现有管线（Parse→InlineEmbeds→Chunk→Tag→Enrich→Embed→CDTA→事务入库），`chunk.go` 改为 goldmark AST 驱动，新增 `refs.go`/`version.go`，store 增量加列加表，检索侧只做版本过滤透传。

**Tech Stack:** Go 1.24, goldmark v1.7+（纯 Go，`extension.GFM` 开表格）, modernc.org/sqlite（driver 名 `"sqlite"`）, golang.org/x/sync（既有）。

## Global Constraints

- SQLite 单写者：`store.New` 已 `SetMaxOpenConns(1)`，禁止新增连接或额外 DB 句柄。
- 布尔 env 开关统一 `0`/`false` 关闭，默认全部开启。
- 新增 env 必须同步更新 README 环境变量表格（以 `backend/internal/config/config.go` 的 `LoadFromEnv` 为唯一权威）。
- 注释、文档、commit message 用中文；commit subject 用 conventional 前缀（`feat:`/`fix:`/`test:`/`docs:`/`chore:`）。
- 静态检查只有 `go vet ./...`；后端测试 `go test ./...`（backend 目录下执行）。
- 不碰前端；`retrieve.Searcher` 与 `kb.Service` 对外接口签名不变（只增可选字段/方法，不改现有方法签名）。
- 测试建库惯用法：`store.New(filepath.Join(t.TempDir(), "t.db"))`。

---

## File Structure

| 文件 | 职责 | 任务 |
|---|---|---|
| `backend/internal/model/kb.go`（改） | `ChunkRef`、`KbChunk`/`KbDocument`/`KbKnowledgeChunk` 扩字段 | Task 1 |
| `backend/internal/kb/parser.go`（改）+ `parser_test.go` | frontmatter 剥离，`ParseDocWithMeta` + 保留 `ParseDoc` wrapper | Task 2 |
| `backend/internal/kb/version.go`（新）+ `version_test.go` | 文档/块版本抽取与 semver 归一化比较 | Task 3 |
| `backend/internal/kb/refs.go`（新）+ `refs_test.go` | 四类引用抽取、`InlineEmbeds`（lookup 回调、2 层循环保护） | Task 4 |
| `backend/internal/kb/chunk.go`（重写）+ `chunk_test.go`（增） | AST 切块：原子块 + 流程闭合；`ChunkResult` 加 `HeadingPath`/`IsFlow` | Task 5 |
| `backend/go.mod`（改） | 新增 `github.com/yuin/goldmark` | Task 5 Step 0 |
| `backend/internal/store/store.go`（改，`migrate`） | 新列 + `kb_chunk_refs` 表 | Task 6 |
| `backend/internal/store/kb.go`（改） | 列常量/scan/insert/update 同步 + refs CRUD + 版本过滤查询 | Task 6 |
| `backend/internal/config/config.go`（改）+ README 表格 | `KB_FLOW_MAX_SIZE`、`KB_EMBED_DEPTH` | Task 7 |
| `backend/internal/kb/service.go`（改）+ `service_flow_test.go`（新） | Upload/Seed 全链路：meta→内联→切块→版本/引用→事务写边表；Delete 级联 | Task 8 |
| `backend/internal/context/chunk.go`、`assembler.go`、`store.go`，`store/kb.go`，`retrieve/searcher.go`（改） | `Query.Version` 透传 + 版本过滤 | Task 9 |
| `backend/cmd/kb-rechunk/main.go`（新） | 存量回填命令 + `--dry-run` | Task 10 |
| 全量回归 | `go test ./...`、`go vet ./...`、`make eval-ci` | Task 11 |

**Interfaces（跨任务契约，先锁定）：**

```go
// model/kb.go (Task 1)
type ChunkRef struct {
    Raw      string `json:"raw"`
    Type     string `json:"type"`   // md_link | wikilink | embed | anchor
    Target   string `json:"target"`
    Anchor   string `json:"anchor"`
    Resolved bool   `json:"resolved"`
}
// KbChunk 新增：VersionMin, VersionMax, HeadingPath string; SourceRefs []ChunkRef; IsFlow bool
// KbDocument 新增：Version, VersionMin, VersionMax string
// KbKnowledgeChunk 新增：VersionMin, VersionMax string

// kb/parser.go (Task 2)
type DocMeta struct {
    Version   string   // frontmatter version 原始值，可空
    AppliesTo []string // frontmatter applies_to，可空
}
func StripFrontmatter(raw string) (body string, meta DocMeta)
func ParseDocWithMeta(filename string, data []byte) (content string, meta DocMeta, err error)
// ParseDoc 保留：内部调 ParseDocWithMeta，只返回 content（行为：含 frontmatter 时返回剥离后正文）

// kb/version.go (Task 3)
type VersionRange struct{ Min, Max string } // 归一化 semver，空=无界
func ParseDocVersion(body string, meta DocMeta) VersionRange
func ParseChunkVersion(blockText string, docVer VersionRange) VersionRange
func MatchVersion(v VersionRange, query string) bool // query 为空永真；无标注块永真

// kb/refs.go (Task 4)
func ExtractRefs(content string) []model.ChunkRef
func InlineEmbeds(content string, lookup func(target string) (body string, version string, ok bool), maxDepth int) string

// kb/chunk.go (Task 5)
type ChunkResult struct {
    Heading     string
    HeadingPath string // 新增："父 > 子"，无父则等于 Heading 或空
    Content     string
    IsFlow      bool   // 新增
}
const MaxFlowChunkSize = 8000 // 流程块上限（与 service embed 截断 8000 对齐）
func Chunk(content string) []ChunkResult // 签名不变

// store (Task 6, KBStore 接口新增)
InsertKbChunkRef(r model.KbChunkRefRow) error
ListKbChunkRefsByChunk(chunkID string) ([]model.KbChunkRefRow, error)
DeleteKbChunkRefsByDoc(docID string) error
// model 新增：
type KbChunkRefRow struct {
    ID string; ChunkID string; DocID string; TargetPath string
    TargetDocID string; Anchor string; RefType string; Resolved bool; CreatedAt time.Time
}
// store 新增：
func (s *Store) ListKnowledgeChunksFiltered(version string) ([]model.KbKnowledgeChunk, error)
// ListKnowledgeChunks() 保留，委托 ListKnowledgeChunksFiltered("")。

// config (Task 7)
KBFlowMaxSize int // KB_FLOW_MAX_SIZE，默认 8000
KBEmbedDepth  int // KB_EMBED_DEPTH，默认 2
```

---

### Task 1: 模型扩展

**Files:**
- Modify: `backend/internal/model/kb.go`
- Test: `backend/internal/model/kb_test.go`（若不存在则新建；先 `ls backend/internal/model/` 确认）

**Interfaces:**
- Consumes: 无（首任务）。
- Produces: `ChunkRef`、`KbChunkRefRow`、扩展后的 `KbChunk`/`KbDocument`/`KbKnowledgeChunk`（Task 4/5/6/8 使用其精确字段名）。

- [ ] **Step 1: 写失败测试** — 新建 `backend/internal/model/kb_test.go`：

```go
package model

import "testing"

func TestChunkRefJSONRoundTrip(t *testing.T) {
    c := KbChunk{ID: "k1", HeadingPath: "GPU 运维 > 掉卡处置", IsFlow: true,
        VersionMin: "3.0.0", SourceRefs: []ChunkRef{{Raw: "[[config]]", Type: "wikilink", Target: "config.md", Resolved: true}}}
    s := MustJSON(c)
    var back KbChunk
    if err := jsonUnmarshal(s, &back); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }
    if back.HeadingPath != "GPU 运维 > 掉卡处置" || !back.IsFlow || len(back.SourceRefs) != 1 {
        t.Fatalf("roundtrip = %+v", back)
    }
}
```

注意：先 `grep -n "func.*JSON" backend/internal/model/*.go` 确认反序列化函数名（`MustJSON` 已知存在于 `store/kb.go:78` 用法；反序列化名按实际存在的名字替换，若只有 `MustJSON` 则用 `encoding/json` 直解）。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/model/ -run TestChunkRefJSONRoundTrip -v`
Expected: FAIL（`undefined: ChunkRef`）

- [ ] **Step 3: 最小实现** — `model/kb.go` 追加：

```go
type ChunkRef struct {
    Raw      string `json:"raw"`
    Type     string `json:"type"`
    Target   string `json:"target"`
    Anchor   string `json:"anchor"`
    Resolved bool   `json:"resolved"`
}

type KbChunkRefRow struct {
    ID          string    `json:"id"`
    ChunkID     string    `json:"chunk_id"`
    DocID       string    `json:"doc_id"`
    TargetPath  string    `json:"target_path"`
    TargetDocID string    `json:"target_doc_id"`
    Anchor      string    `json:"anchor"`
    RefType     string    `json:"ref_type"`
    Resolved    bool      `json:"resolved"`
    CreatedAt   time.Time `json:"created_at"`
}
```

`KbDocument` 追加 `Version, VersionMin, VersionMax string`；`KbChunk` 追加 `VersionMin, VersionMax, HeadingPath string`、`SourceRefs []ChunkRef`、`IsFlow bool`；`KbKnowledgeChunk` 追加 `VersionMin, VersionMax string`。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/model/ -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add backend/internal/model/kb.go backend/internal/model/kb_test.go
git commit -m "feat: 知识块模型新增版本/引用/流程字段"
```

---

### Task 2: frontmatter 剥离

**Files:**
- Modify: `backend/internal/kb/parser.go`
- Test: `backend/internal/kb/parser_test.go`（追加）

**Interfaces:**
- Consumes: Task 1（`DocMeta` 本任务内定义，不依赖 Task 1）。
- Produces: `StripFrontmatter`、`ParseDocWithMeta`、`DocMeta`（Task 3/8 使用）。

- [ ] **Step 1: 写失败测试** — `parser_test.go` 追加：

```go
func TestParseFrontmatter(t *testing.T) {
    raw := "---\nversion: 2.1.0\napplies_to: [\">=2.0\", \"<3.0\"]\n---\n# 标题\n正文"
    body, meta := StripFrontmatter(raw)
    if meta.Version != "2.1.0" {
        t.Fatalf("version = %q", meta.Version)
    }
    if len(meta.AppliesTo) != 2 {
        t.Fatalf("applies_to = %v", meta.AppliesTo)
    }
    if strings.Contains(body, "version:") || !strings.Contains(body, "# 标题") {
        t.Fatalf("body = %q", body)
    }
}

func TestParseFrontmatterAbsent(t *testing.T) {
    body, meta := StripFrontmatter("# 标题\n正文")
    if meta.Version != "" || body != "# 标题\n正文" {
        t.Fatalf("got %q %+v", body, meta)
    }
}

func TestParseDocStripsFrontmatter(t *testing.T) {
    got, err := ParseDoc("a.md", []byte("---\nversion: 1.0.0\n---\nhello"))
    if err != nil || got != "hello" {
        t.Fatalf("got %q %v", got, err)
    }
}
```

`applies_to` 解析规则（无 yaml 依赖，手写）：`version:` 取冒号后 trim 空格去引号；`applies_to:` 支持 `["a", "b"]` Flow 风格（按逗号切、去引号空格）与多行 `- a` 风格。非法 YAML（一行无冒号）→ 忽略该行不报错。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/kb/ -run 'TestParseFrontmatter|TestParseDocStripsFrontmatter' -v`
Expected: FAIL（`undefined: StripFrontmatter`）

- [ ] **Step 3: 最小实现** — `parser.go` 追加：

```go
// DocMeta 文档级元数据（frontmatter）。
type DocMeta struct {
    Version   string
    AppliesTo []string
}

// StripFrontmatter 剥离开头的 YAML frontmatter（--- 包裹）；无则原样返回。
func StripFrontmatter(raw string) (string, DocMeta) { /* ... */ }

// ParseDocWithMeta 按扩展名解析并剥离 frontmatter。
func ParseDocWithMeta(filename string, data []byte) (string, DocMeta, error) { /* ... */ }
```

并将现有 `ParseDoc` 改为调 `ParseDocWithMeta` 后只返回 content（空内容校验保留，校验对象为剥离后正文；纯 frontmatter 无正文仍报 `ErrEmptyContent`）。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/kb/ -run 'TestParse' -v && go test ./internal/kb/ -v`
Expected: PASS（含既有 `TestParseTxtAndMd` 等 4 个用例）

- [ ] **Step 5: 提交**

```bash
git add backend/internal/kb/parser.go backend/internal/kb/parser_test.go
git commit -m "feat: 解析 frontmatter 版本元数据"
```

---

### Task 3: 版本抽取

**Files:**
- Create: `backend/internal/kb/version.go`
- Test: `backend/internal/kb/version_test.go`

**Interfaces:**
- Consumes: Task 2（`DocMeta`）。
- Produces: `VersionRange`、`ParseDocVersion`、`ParseChunkVersion`、`MatchVersion`（Task 6/8/9 使用）。

- [ ] **Step 1: 写失败测试**：

```go
func TestParseDocVersionPriority(t *testing.T) {
    // frontmatter 优先于正文 @version
    got := ParseDocVersion("@version 1.0.0\n正文", DocMeta{Version: "2.1.0"})
    if got.Min != "2.1.0" {
        t.Fatalf("got %+v", got)
    }
    // 正文 Available since 回退（只扫前 30 行）
    got2 := ParseDocVersion("前言\nAvailable since v3.0\n正文", DocMeta{})
    if got2.Min != "3.0.0" {
        t.Fatalf("got %+v", got2)
    }
    // 无标注为空（全版本适用）
    got3 := ParseDocVersion("纯正文", DocMeta{})
    if got3.Min != "" || got3.Max != "" {
        t.Fatalf("got %+v", got3)
    }
}

func TestChunkVersionInherit(t *testing.T) {
    doc := VersionRange{Min: "2.0.0"}
    // 块内标注优先
    if got := ParseChunkVersion("本步骤 Available since v3.0", doc); got.Min != "3.0.0" {
        t.Fatalf("got %+v", got)
    }
    // 无标注继承文档级
    if got := ParseChunkVersion("普通步骤", doc); got.Min != "2.0.0" {
        t.Fatalf("got %+v", got)
    }
}

func TestMatchVersion(t *testing.T) {
    if !MatchVersion(VersionRange{}, "3.1.0") {
        t.Fatalf("无标注块应永真")
    }
    if !MatchVersion(VersionRange{Min: "3.0.0"}, "3.1.0") || MatchVersion(VersionRange{Min: "3.0.0"}, "2.9.0") {
        t.Fatalf("下界匹配错误")
    }
    if MatchVersion(VersionRange{Min: "3.0.0", Max: "3.5.0"}, "4.0.0") {
        t.Fatalf("上界匹配错误")
    }
    if !MatchVersion(VersionRange{Min: "3.0.0"}, "") {
        t.Fatalf("空查询应永真")
    }
}
```

语义锁定：归一化 `3`→`3.0.0`、`v` 前缀去之；`v3.0+`/`>=3.0` 只产生 Min；`仅 vX 可用`/`精确` 产生 Min=Max=X；比较按数字三段比较（手写 `compareSemver`，不引包）。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/kb/ -run 'TestParseDocVersion|TestChunkVersion|TestMatchVersion' -v`
Expected: FAIL（`undefined`）

- [ ] **Step 3: 最小实现** — 新建 `version.go` 实现上述 4 个符号 + `compareSemver`。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/kb/ -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add backend/internal/kb/version.go backend/internal/kb/version_test.go
git commit -m "feat: 文档与块级版本抽取及匹配"
```

---

### Task 4: 引用抽取与 embed 内联

**Files:**
- Create: `backend/internal/kb/refs.go`
- Test: `backend/internal/kb/refs_test.go`

**Interfaces:**
- Consumes: Task 1（`model.ChunkRef`）。
- Produces: `ExtractRefs`、`InlineEmbeds`（Task 5 切块后归属、Task 8 内联展开使用）。

- [ ] **Step 1: 写失败测试**：

```go
func TestExtractRefs(t *testing.T) {
    content := "详见[配置](./config.md)与[安装步骤](#installation)，另见 [[wikilinks]] 与 ![[embeds]]，站外[文档](https://example.com/x)忽略锚点"
    refs := ExtractRefs(content)
    byType := map[string]int{}
    for _, r := range refs {
        byType[r.Type]++
    }
    if byType["md_link"] != 1 || byType["anchor"] != 1 || byType["wikilink"] != 1 || byType["embed"] != 1 {
        t.Fatalf("refs = %+v", refs)
    }
    // anchor 抽取
    for _, r := range refs {
        if r.Type == "anchor" && r.Anchor != "installation" {
            t.Fatalf("anchor = %+v", r)
        }
        if r.Type == "wikilink" && r.Target != "wikilinks" {
            t.Fatalf("wikilink = %+v", r)
        }
    }
}

func TestInlineEmbeds(t *testing.T) {
    lookup := func(target string) (string, string, bool) {
        if target == "config" {
            return "# 配置\n## 网络\nIP 设置", "2.0.0", true
        }
        return "", "", false
    }
    out := InlineEmbeds("前言\n![[config#网络]]\n后记", lookup, 2)
    if !strings.Contains(out, "嵌入自 [[config#网络]]") || !strings.Contains(out, "IP 设置") {
        t.Fatalf("out = %q", out)
    }
    if strings.Contains(out, "![[config") {
        t.Fatalf("应已替换标记")
    }
}

func TestInlineEmbedsMissAndCycle(t *testing.T) {
    lookup := func(target string) (string, string, bool) { return "", "", false }
    out := InlineEmbeds("![[missing]]", lookup, 2)
    if !strings.Contains(out, "![[missing]]") {
        t.Fatalf("未命中应保留原文标记, got %q", out)
    }
    // 循环：A 展开后含 B，B 展开后含 A，最深 2 层后保留标记
    n := 0
    cyc := func(target string) (string, string, bool) {
        n++
        if target == "A" {
            return "A 内容 ![[B]]", "", true
        }
        return "B 内容 ![[A]]", "", true
    }
    out2 := InlineEmbeds("![[A]]", cyc, 2)
    if strings.Count(out2, "![[") > 1 {
        t.Fatalf("循环应截断, got %q", out2)
    }
}
```

实现要点（测试即契约）：`![[target#anchor]]` 有 anchor 时只内联目标文档该 heading 段（heading 行起至下一同级或更高级 heading 止），无 anchor 取全文≤1500 rune；替换格式 `> 📎 嵌入自 [[target#anchor]]（vX.Y.Z）\n> <段>`（无版本则省略括号）；`[[a|别名]]` Target 取 `|` 前；站外 http(s) 链接不抽取。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/kb/ -run 'TestExtractRefs|TestInlineEmbeds' -v`
Expected: FAIL

- [ ] **Step 3: 最小实现** — 新建 `refs.go`。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/kb/ -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add backend/internal/kb/refs.go backend/internal/kb/refs_test.go
git commit -m "feat: 跨文档引用抽取与 embed 内联"
```

---

### Task 5: AST 切块重写

**Files:**
- Modify: `backend/internal/kb/chunk.go`（重写 `Chunk`，保留 `DefaultChunkSize`、`splitBySize` 给非流程路径复用）
- Modify: `backend/go.mod`（新增 goldmark）
- Test: `backend/internal/kb/chunk_test.go`（追加；既有 6 用例必须全绿）

**Interfaces:**
- Consumes: Task 4（`ExtractRefs` 结果按行区间归属到块——归属逻辑在本任务内实现）。
- Produces: 扩展后的 `ChunkResult{Heading, HeadingPath, Content, IsFlow}`（Task 8 使用）。

- [ ] **Step 0: 加依赖**

Run: `go get github.com/yuin/goldmark@v1.7.8 && go mod tidy`
Expected: `go.mod` 新增 `github.com/yuin/goldmark`；`go build ./...` 通过。

- [ ] **Step 1: 写失败测试** — `chunk_test.go` 追加：

```go
func TestChunkKeepsTableIntact(t *testing.T) {
    var b strings.Builder
    b.WriteString("# 运维\n前言\n\n| 指标 | 阈值 |\n| --- | --- |\n")
    for i := 0; i < 100; i++ {
        b.WriteString("| cpu_usage | 90% |\n")
    }
    b.WriteString("\n尾声\n")
    got := Chunk(b.String())
    // 表格行不得被拆散到不同块（任一块包含表头即须包含全部表格行）
    for _, c := range got {
        if strings.Contains(c.Content, "| 指标 | 阈值 |") && !strings.Contains(c.Content, "尾声") {
            if strings.Count(c.Content, "| cpu_usage |") != 100 {
                t.Fatalf("表格被拆散，块内容行数不对")
            }
        }
    }
}

func TestChunkKeepsCodeFence(t *testing.T) {
    content := "# 部署\n```bash\n# 安装驱动\nsh install.sh\n```\n尾声"
    got := Chunk(content)
    if len(got) != 1 {
        t.Fatalf("代码块注释 # 不得误判标题，chunks = %+v", got)
    }
    if !strings.Contains(got[0].Content, "sh install.sh") {
        t.Fatalf("代码块内容丢失: %+v", got[0])
    }
}

func TestChunkFlowAtomicity(t *testing.T) {
    var b strings.Builder
    b.WriteString("# GPU 运维\n## 掉卡处置\n前言\n\n")
    for i := 1; i <= 7; i++ {
        b.WriteString(strings.Repeat("x", 400) + "\n")
        b.WriteString(strconv.Itoa(i) + ". 执行步骤" + "\n")
    }
    b.WriteString("> 若复位失败则跳至步骤 7\n\n| 项 | 值 |\n| --- | --- |\n| a | b |\n")
    b.WriteString("```bash\necho done\n```\n")
    got := Chunk(b.String())
    flows := 0
    for _, c := range got {
        if c.IsFlow {
            flows++
            for i := 1; i <= 7; i++ {
                if !strings.Contains(c.Content, strconv.Itoa(i)+". 执行步骤") {
                    t.Fatalf("流程块缺步骤 %d", i)
                }
            }
            if c.HeadingPath != "GPU 运维 > 掉卡处置" {
                t.Fatalf("heading_path = %q", c.HeadingPath)
            }
        }
    }
    if flows != 1 {
        t.Fatalf("含步骤的 Section 应合并为 exactly 1 个流程块, got %d (%+v)", flows, got)
    }
}
```

（`strconv` 按需 import。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/kb/ -run 'TestChunkKeeps|TestChunkFlow' -v`
Expected: FAIL（表格被 2000 rune 切断；`# 安装驱动` 误判标题成多块；7 步流程被拆散）

- [ ] **Step 3: 最小实现** — 重写 `chunk.go`：

```go
import (
    "github.com/yuin/goldmark"
    "github.com/yuin/goldmark/ast"
    "github.com/yuin/goldmark/extension"
    "github.com/yuin/goldmark/text"
)
```

要点（缺一不可）：
1. `goldmark.New(goldmark.WithExtensions(extension.GFM))` 解析（GFM 开表格；围栏/引用块/Setext 标题为 core 支持）。
2. `ast.Walk` 收集顶层 `*ast.Heading`（1-6 级）分节，维护 `HeadingPath` 栈；`nodeSource(n, src)` 按 `n.Lines()` 取原文行（保留表格/代码原样）。
3. 原子判定：` *ast.List`（`IsOrdered()` 即步骤序列）、`*ast.Table`、` *ast.FencedCodeBlock`/`*ast.CodeBlock`、`*ast.Blockquote`。
4. 含 `OrderedList` 的 Section → 单 FlowChunk（`IsFlow=true`，上限 `MaxFlowChunkSize=8000`，超则日志告警仍不拆）；无序 List/Table/CodeBlock 独立成 chunk；其余段落累积至 `DefaultChunkSize`，切点只在段落/原子边界。
5. `headingRe` 扩展到 `^#{1,6}\s` 仅作回退路径使用；goldmark 解析失败 → 回退旧逐行逻辑。
6. 引用归属：`ExtractRefs` 对全文扫一次记行号，按块行区间填 `ChunkResult` 外的并行结构（`Chunk()` 签名不变，归属由 Task 8 在 service 层按 `Content` 行偏移完成——本任务只需保证块 `Content` 为原文连续行，顺序=文档序）。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/kb/ -v`
Expected: PASS（3 个新用例 + 既有 6 用例；`TestChunkNoHeadingsLongSplitsBySize` 语义不变仍过）

- [ ] **Step 5: 提交**

```bash
git add backend/internal/kb/chunk.go backend/internal/kb/chunk_test.go backend/go.mod backend/go.sum
git commit -m "feat: AST 语义闭合切块，流程表格代码不拆散"
```

---

### Task 6: store 迁移与 refs 持久化

**Files:**
- Modify: `backend/internal/store/store.go`（`migrate` 追加）
- Modify: `backend/internal/store/kb.go`（列常量/scan/insert/update/refs CRUD/版本过滤）
- Test: `backend/internal/store/kb_refs_test.go`（新）+ `kb_test.go`（追加列读写断言）

**Interfaces:**
- Consumes: Task 1（模型字段）。
- Produces: 迁移后的表结构、`InsertKbChunkRef` 等方法、`ListKnowledgeChunksFiltered`（Task 8/9/10 使用）。

- [ ] **Step 1: 写失败测试** — 新建 `kb_refs_test.go`：

```go
func TestKbChunkRefsCRUD(t *testing.T) {
    st, _ := store.New(filepath.Join(t.TempDir(), "t.db")) // 同包内直接 New
    ...
}
```

注意 `kb_refs_test.go` 与 `store` 同包（看 `kb_test.go` 包名保持一致，`newTestStore` helper 若存在则复用——先 `grep -n "func newTest\|^package" backend/internal/store/kb_test.go` 确认）。测试内容：建 doc+chunk → `InsertKbChunkRef` 两条（含 `resolved=0` 未命中）→ `ListKbChunkRefsByChunk` 断言 2 条 → `DeleteKbChunkRefsByDoc` 后为空。另在 `kb_test.go` 追加：写入带 `VersionMin/HeadingPath/IsFlow/SourceRefs` 的 chunk 后读回断言相等；老库兼容：建表语句不含新列的旧 DB 文件打开后 `migrate` 自动补列（用 `ensureColumn` 语义，测 `GetKbChunk` 不报错且新字段零值）。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/store/ -run 'TestKbChunkRefs|TestKbChunkNewCols' -v`
Expected: FAIL

- [ ] **Step 3: 最小实现**：

1. `store.go:migrate()` 追加（仿既有 `ensureColumn` 风格）：
```go
if err := ensureColumn(s.db, "kb_documents", "version", "version TEXT NOT NULL DEFAULT ''"); err != nil { return err }
if err := ensureColumn(s.db, "kb_documents", "version_min", "version_min TEXT NOT NULL DEFAULT ''"); err != nil { return err }
if err := ensureColumn(s.db, "kb_documents", "version_max", "version_max TEXT NOT NULL DEFAULT ''"); err != nil { return err }
if err := ensureColumn(s.db, "kb_chunks", "version_min", "version_min TEXT NOT NULL DEFAULT ''"); err != nil { return err }
if err := ensureColumn(s.db, "kb_chunks", "version_max", "version_max TEXT NOT NULL DEFAULT ''"); err != nil { return err }
if err := ensureColumn(s.db, "kb_chunks", "heading_path", "heading_path TEXT NOT NULL DEFAULT ''"); err != nil { return err }
if err := ensureColumn(s.db, "kb_chunks", "source_refs", "source_refs TEXT NOT NULL DEFAULT '[]'"); err != nil { return err }
if err := ensureColumn(s.db, "kb_chunks", "is_flow", "is_flow INTEGER NOT NULL DEFAULT 0"); err != nil { return err }
if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS kb_chunk_refs (
    id TEXT PRIMARY KEY, chunk_id TEXT NOT NULL, doc_id TEXT NOT NULL,
    target_path TEXT NOT NULL, target_doc_id TEXT NOT NULL DEFAULT '',
    anchor TEXT NOT NULL DEFAULT '', ref_type TEXT NOT NULL,
    resolved INTEGER NOT NULL DEFAULT 0, created_at DATETIME)`); err != nil { return err }
// + 两个 CREATE INDEX IF NOT EXISTS
```
2. `kb.go`：`kbDocCols` 追加 `version, version_min, version_max`；`kbChunkCols` 追加 `version_min, version_max, heading_path, source_refs, is_flow`；`scanKbDoc/scanKbChunk/InsertKbDocument/InsertKbChunk` 同步；`UpdateKbChunk` 改为全列更新（含新列，避免 CDTA 合并丢字段）；`KBStore` 接口加 3 个 refs 方法 + repo 实现 + `*Store` 委托；`ListKnowledgeChunks` SELECT 加 `c.version_min, c.version_max` 并委托 `ListKnowledgeChunksFiltered("")`，后者 `version != ""` 时追加 `AND (c.version_min='' OR c.version_min<=?) AND (c.version_max='' OR c.version_max>=?)`（字符串比较要求归一化 semver 等宽可比——`version.go` 归一化保证 `主.次.补` 数字段，此处用 `MatchVersion` 做内存过滤更稳：先全量查再内存过滤，知识库块量小，避免 SQLite 字符串版本比较坑。**选内存过滤**，SQL 不变）。
3. `KbFilter` 暂不动（管理页筛选二期）。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/store/ -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add backend/internal/store/store.go backend/internal/store/kb.go backend/internal/store/kb_refs_test.go backend/internal/store/kb_test.go
git commit -m "feat: 知识库版本列与引用边表持久化"
```

---

### Task 7: 配置新增

**Files:**
- Modify: `backend/internal/config/config.go`
- Modify: `README.md`（环境变量表格追加两行）
- Test: `backend/internal/config/config_test.go`（追加）

**Interfaces:**
- Consumes: 无。
- Produces: `cfg.KBFlowMaxSize`、`cfg.KBEmbedDepth`（Task 8/10 使用）。

- [ ] **Step 1: 写失败测试** — `config_test.go` 追加（仿既有 `KB_CDTA_THRESHOLD` 用例，`map[string]string{"KB_FLOW_MAX_SIZE": "4000", "KB_EMBED_DEPTH": "3"}` 走 `LoadFromEnv`，断言值；再断言默认值 8000/2）。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/config/ -run TestKBFlow -v`
Expected: FAIL

- [ ] **Step 3: 最小实现** — 仿 `KBCdtaThreshold`（`config.go:33,138`）：struct 加 `KBFlowMaxSize int // KB_FLOW_MAX_SIZE，默认 8000`、`KBEmbedDepth int // KB_EMBED_DEPTH，默认 2`；`LoadFromEnv` 解析（非空则 `strconv.Atoi`，失败忽略保默认）；README 表格同步两行。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/config/ -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add backend/internal/config/config.go backend/internal/config/config_test.go README.md
git commit -m "feat: 新增流程块上限与 embed 深度配置"
```

---

### Task 8: service 全链路集成

**Files:**
- Modify: `backend/internal/kb/service.go`（`Upload`、`Seed`、`insertEnrichedDoc`、`Delete`）
- Test: `backend/internal/kb/service_flow_test.go`（新）

**Interfaces:**
- Consumes: Task 1–7 全部。
- Produces: 端到端可用的新管线（Task 10/11 消费）。

- [ ] **Step 1: 写失败测试** — `service_flow_test.go`（复用 `service_test.go:16 newTestService`）：

```go
func TestUploadFlowDocAtomic(t *testing.T) {
    svc := newTestService(t)
    var b strings.Builder
    b.WriteString("---\nversion: 3.0.0\n---\n# GPU 运维\n## 掉卡处置\n")
    for i := 1; i <= 5; i++ {
        b.WriteString(strings.Repeat("y", 500) + "\n" + strconv.Itoa(i) + ". 步骤\n")
    }
    doc, err := svc.Upload(context.Background(), UploadReq{SourceFile: "gpu.md", Data: []byte(b.String()), Category: model.KBManual})
    if err != nil { t.Fatalf("Upload: %v", err) }
    if doc.VersionMin != "3.0.0" { t.Fatalf("doc version = %+v", doc) }
    detail, _ := svc.Get(context.Background(), doc.ID)
    flows := 0
    for _, c := range detail.Chunks {
        if c.IsFlow {
            flows++
            if c.VersionMin != "3.0.0" || c.HeadingPath != "GPU 运维 > 掉卡处置" { t.Fatalf("chunk = %+v", c) }
        }
    }
    if flows != 1 { t.Fatalf("应恰好 1 个流程块, got %+v", detail.Chunks) }
}

func TestUploadEmbedInline(t *testing.T) {
    svc := newTestService(t)
    // 先入库被引文档，再入库含 ![[ ]] 的文档
    base, err := svc.Upload(context.Background(), UploadReq{SourceFile: "config.md", Data: []byte("# 配置\n## 网络\nIP 设为 10.0.0.1"), Category: model.KBManual})
    if err != nil { t.Fatalf("base: %v", err) }
    doc, err := svc.Upload(context.Background(), UploadReq{SourceFile: "deploy.md", Data: []byte("# 部署\n先配网 ![[config#网络]] 再继续"), Category: model.KBManual})
    if err != nil { t.Fatalf("Upload: %v", err) }
    detail, _ := svc.Get(context.Background(), doc.ID)
    foundInline, foundEdge := false, false
    for _, c := range detail.Chunks {
        if strings.Contains(c.Content, "IP 设为 10.0.0.1") { foundInline = true }
        for _, r := range c.SourceRefs {
            if r.Type == "embed" && r.Resolved { foundEdge = true }
        }
    }
    if !foundInline || !foundEdge { t.Fatalf("inline=%v edge=%v (%+v)", foundInline, foundEdge, detail.Chunks) }
    _ = base
}
```

lookup 实现（service 内）：按 `SourceFile` 精确匹配，其次 `Title` 匹配（`List` 全量后内存匹配，知识库文档量小）；返回目标文档 `Content`+`Version`。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/kb/ -run 'TestUploadFlowDoc|TestUploadEmbedInline' -v`
Expected: FAIL（无版本字段、无内联）

- [ ] **Step 3: 最小实现** — `service.go`：
  1. `Upload`：`ParseDoc`→`ParseDocWithMeta`；`doc.Version/VersionMin/VersionMax` 由 `ParseDocVersion(content, meta)` 填充；`content = InlineEmbeds(content, lookup, cfg.KBEmbedDepth)`；`chunks := Chunk(content)`；每块 `ParseChunkVersion(块Content, docVer)` + `ExtractRefs(块Content)` 组装 `ChunkInput`（service 内新类型，含 `ChunkResult/VersionMin/VersionMax/Refs`）。
  2. `insertEnrichedDoc` 改签名接收 `[]ChunkInput`（替代内部 `Chunk(content)` 调用）；新块写入时填充 5 个新列；事务内按块写 `kb_chunk_refs`（ref ID `model.NewID("kbref")`，`target_doc_id` 在 lookup 命中时回填）。
  3. `Seed` 同样走新链（SeedDoc 无 SourceFile：lookup 按 Title 匹配；逐文档顺序处理使先入库文档可被后文档 embed）。
  4. `Delete` 加 `DeleteKbChunkRefsByDoc` 级联。
  5. `UpdateKbChunk` 调用点（CDTA 合并 `updated` 构造，`service.go:487`）保留旧值新字段（`old` 已含）。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/kb/ -v && go test ./internal/store/ -v`
Expected: PASS（含既有 Upload/CDTA/enrich 全量用例）

- [ ] **Step 5: 提交**

```bash
git add backend/internal/kb/service.go backend/internal/kb/service_flow_test.go
git commit -m "feat: 上传链路集成版本引用与流程块"
```

---

### Task 9: 检索版本过滤透传

**Files:**
- Modify: `backend/internal/context/chunk.go`（`Query` 加 `Version string`）
- Modify: `backend/internal/context/assembler.go`（两处 `retrieve.Query{...}` 加 `Version: q.Version`；fallback 分支 `a.cs.ListKnowledgeChunks()` 改 `a.cs.ListKnowledgeChunksFiltered(q.Version)` + knowledgeContent 回显追加版本）
- Modify: `backend/internal/context/store.go`（加 `ListKnowledgeChunksFiltered` 委托）
- Modify: `backend/internal/retrieve/searcher.go`（两处 `s.st.ListKnowledgeChunks()` 改过滤版，`s.st` 类型先确认——见 Step 1）
- Test: `backend/internal/context/assembler_test.go`（追加）+ `retrieve/searcher_test.go`（追加）

**Interfaces:**
- Consumes: Task 6（`ListKnowledgeChunksFiltered`）。
- Produces: 版本过滤端到端（Task 11 验证）。

- [ ] **Step 1: 确认 searcher 的 store 类型与 Query 结构**

Run: `grep -n "st \|type Query" backend/internal/retrieve/searcher.go | head; grep -n "func (s \*Store) ListKnowledgeChunks\|type Store" backend/internal/context/store.go`
Expected: 看到 `st` 字段类型。若为 `*store.Store` 直接调新方法；若为接口则给该接口加 `ListKnowledgeChunksFiltered(string)` 方法。`retrieve.Query` 结构按 `assembler.go:106` 用法加 `Version string` 字段（与 `Intents` 并列）。

- [ ] **Step 2: 写失败测试**：

```go
// context/assembler_test.go 追加（仿既有 Query 构造）：
func TestAssembleVersionFilter(t *testing.T) {
    // 入库两文档：v2 块与 v3 块（经 kb.Service.Upload），Query{Version:"3.1.0"} 组装后知识回显含 v3 不含 v2
}
```

具体：用 `store.New(t.TempDir())` + `kb.NewService` 上传两篇（`---version: 2.0.0---` 与 `---version: 3.0.0---`，内容关键词相同），构造 `Assembler`（仿 `assembler_test.go:89` 的构造方式）调 `Assemble(Query{Text: 关键词, Version: "3.1.0"}, ...)`，断言输出含 v3 标题不含 v2 标题；`Version: ""` 时两者皆含。searcher 侧：`searcher_test.go` 追加 `TestSearchVersionFilter`，同数据集调 `SearchCases(ctx, retrieve.Query{Text: 关键词, Version: "2.5.0"})` 断言只回 v2。

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/context/ ./internal/retrieve/ -run 'TestAssembleVersionFilter|TestSearchVersionFilter' -v`
Expected: FAIL（无 Version 字段/无过滤）

- [ ] **Step 4: 最小实现** — 按 Files 清单改 5 处；`knowledgeContent` 回显追加 `[文档版本 Vx.y]`（`kc.VersionMin` 非空时）与标题路径（`kc.Heading` 既有，heading_path 透传到 `KbKnowledgeChunk`？——`KbKnowledgeChunk` 未加 HeadingPath（Task 1 未含），本任务补加 `HeadingPath` 字段并在 `ListKnowledgeChunksFiltered` SELECT 中带出 `c.heading_path`，回显格式 `[类型] 标题 > 路径 §heading: 内容`）。

- [ ] **Step 5: 运行确认通过**

Run: `go test ./internal/context/ ./internal/retrieve/ ./internal/store/ -v`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add backend/internal/context/ backend/internal/retrieve/searcher.go backend/internal/store/kb.go backend/internal/model/kb.go
git commit -m "feat: 检索链路透传版本过滤"
```

---

### Task 10: 存量回填命令

**Files:**
- Create: `backend/cmd/kb-rechunk/main.go`
- Test: 手动验证（staging DB），见 Step 4

**Interfaces:**
- Consumes: Task 1–8。
- Produces: `go run ./cmd/kb-rechunk [--dry-run] [--db PATH]`。

- [ ] **Step 1: 实现命令** — 仿 `cmd/rageval/main.go:34` flag 风格 + `cmd/server/main.go:27,41` 的 `config.Load()`/`store.New(cfg.DBPath)`：

```go
package main

import (
    "context"
    "flag"
    "fmt"
    "os"

    "aiops/internal/config"
    "aiops/internal/kb"
    "aiops/internal/store"
)

func main() {
    dryRun := flag.Bool("dry-run", false, "只统计差异，不写库")
    dbPath := flag.String("db", "", "DB 路径，空则用配置 DB_PATH")
    flag.Parse()

    cfg := config.Load()
    path := *dbPath
    if path == "" {
        path = cfg.DBPath
    }
    st, err := store.New(path)
    if err != nil { fmt.Fprintln(os.Stderr, "open db:", err); os.Exit(1) }
    defer st.Close()
    // 遍历全部文档：content → StripFrontmatter → InlineEmbeds(Title/SourceFile lookup) →
    // Chunk → 版本/引用 → --dry-run 输出统计，否则 WithKBTx 重写 chunks+refs
    _ = context.Background()
    _ = kb.MaxFlowChunkSize
}
```

完整逻辑：`ListKbDocuments(KbFilter{})` 取全文 → 新链重切 → 对比旧块（块数、流程块数、未解析引用数）打印 `doc 标题: chunks 旧N→新M, flow=K, unresolved=R` → 非 dry-run 时事务内 `DeleteKbChunks/DeleteKbChunkQuestionsByDoc/DeleteKbEmbeddingsByDoc/DeleteKbChunkRefsByDoc` 后按新链插入（复用 service 逻辑——若 service 未暴露重切入口，则在 service 加导出方法 `RechunkDoc(ctx, docID) (stat, error)`，Upload/回填共用；推荐后者，避免 cmd 复制事务逻辑）。

- [ ] **Step 2: 编译通过**

Run: `go build ./... && go vet ./cmd/kb-rechunk/`
Expected: 通过。

- [ ] **Step 3: dry-run 验证** — 准备含流程/版本/embed 的 staging DB（backend 下 `cp aiops.db /tmp/rechunk-staging.db` 若存在，否则先跑 server 生成或用测试 seed），执行：

Run: `go run ./cmd/kb-rechunk --dry-run --db /tmp/rechunk-staging.db`
Expected: 每文档输出差异统计行；DB mtime 不变（验证只读：`ls -l --time-style=full-iso /tmp/rechunk-staging.db` 前后一致）。

- [ ] **Step 4: 实跑验证**

Run: `go run ./cmd/kb-rechunk --db /tmp/rechunk-staging.db && go test ./internal/store/ -v`
Expected: 回填成功；测试仍绿。

- [ ] **Step 5: 提交**

```bash
git add backend/cmd/kb-rechunk/main.go backend/internal/kb/service.go
git commit -m "feat: 存量文档回填重切命令"
```

---

### Task 11: 全量回归验证

**Files:** 无改动（验证任务）。

- [ ] **Step 1: 全量单测 + vet**

Run: `go test ./... && go vet ./...`
Expected: 全部 PASS，无 vet 告警。

- [ ] **Step 2: 评测门禁**

Run: `make eval-ci`
Expected: 通过（阈值 0.9）。若 portal 不可用（如无 `LLM_API_KEY` 的纯规则路径），记录实际输出并确认与基线一致不退步。

- [ ] **Step 3: 成功标准抽查** — 构造 1 篇 golden 技术文档（含 7 步流程+表格+代码块+`@version`+`![[embed]]`），跑 Upload 后断言：表格行/列表项跨块率 0、版本字段非空、embed 内联成功、边表 `resolved=1`。可用 Task 8/10 的测试覆盖此条，此处做一次人工抽查并记录结果。

---

## Self-Review（计划自检，已执行）

1. **Spec 覆盖**：§4 数据模型→Task 1+6；frontmatter→Task 2；§5 切块→Task 5；§6 版本→Task 3、引用→Task 4、透传→Task 9；回填→Task 10；成功标准→Task 8 测试 + Task 11 Step 3。`heading_path` 透传到 `KbKnowledgeChunk` 在 Task 9 Step 4 补加（Task 1 未含，已显式列出，无遗漏）。
2. **Placeholder 扫描**：无 TBD/TODO；Task 9 Step 1 的类型确认是具体 grep 命令 + 双分支处理，不是占位。
3. **类型一致性**：`ChunkRef`（model）贯穿 Task 1/4/6/8；`ChunkResult{Heading, HeadingPath, Content, IsFlow}` 贯穿 Task 5/8；`VersionRange{Min, Max}` 贯穿 Task 3/8；edge 行类型 `KbChunkRefRow` 贯穿 Task 1/6/8；`ListKnowledgeChunksFiltered(version string)` 签名三处一致（Task 6/9）。
