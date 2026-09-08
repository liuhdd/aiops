# 技术操作文档切块与版本引用设计文档

- 日期：2026-09-08
- 状态：已确认
- 取代：`2026-08-31-technical-doc-structure-design.md`（草稿，未提交，已移除；其 D6/D7 沿用，D1/D3/D5 被本 spec 替代，详见 §2.2）
- 依赖：知识库基础（`2026-08-17-knowledge-base`）、混合检索（`2026-08-20-knowledge-hybrid-retrieval`）、Chunk 富化（`2026-08-28-knowledge-enrichment`）
- 范围：Markdown 技术/产品操作文档的**入库切块环节**——步骤流程原子性、文档级+块级版本保留、跨文档引用抽取与 embed 内联。检索侧仅做版本过滤字段透传（最小改动）。
- 不在范围：图遍历检索（KERAG 风格多跳）、文档路由分层、版本差异比对、前端"引用健康"页（二期）。

## 1. 背景与目标

### 1.1 现状问题

入库链路 `Parse → Tag → Chunk → Enrich → Embed → CDTA → 入库` 中，切块（`backend/internal/kb/chunk.go:44`）是纯逐行扫描 + 固定 rune 二次切分，解析（`backend/internal/kb/parser.go:19`）只做纯文本直读，对技术操作文档造成三类破坏：

1. **流程被拆散**：`splitBySize` 按 `runes[start:start+2000]` 硬切，"步骤 1→2→3"序列、条件分支、表格、代码块都会从中间断开；续片仅首片保留 `Heading`（`chunk.go:79 i == 0`），同名章节无法区分。
2. **版本信息丢失**：`@version 2.1.0` / `Available since v3.0` 只作为正文文本入库，无结构化字段，无法做版本过滤。
3. **引用关系丢失**：`[详见配置](./config.md)` / `[[wikilinks]]` / `![[embeds]]` 只作为普通文本，跨文档关系不可追踪，embed 内容不展开导致语义缺块。

### 1.2 目标

1. **步骤原子性**：一个完整操作流程（含步骤序列、条件分支、其间表格/代码块）必须在同一个 chunk 内，即使超长也不拆。
2. **版本双层保留**：文档级版本（frontmatter/`@version`）与块级版本（`Available since`）分别落库，检索可按版本过滤，无标注块始终可召回。
3. **引用物化+内联**：`![[embed]]` 在入库前内联展开（带出处标注与循环保护）；其余引用抽取为边表，供追踪与失效检测。
4. **可验证**：表格行/列表项跨块率为 0；版本保留率 100%；`go test ./...` + `make eval-ci` 全绿。

## 2. 已确认决策

| # | 决策 | 理由 |
|---|---|---|
| D1 | 语料以 Markdown 为主（含文件引用），引入 `goldmark` 做 AST 解析 | 正则无法可靠处理嵌套列表/表格/围栏代码块；goldmark 纯 Go 无 CGO |
| D2 | 版本在文档级 + 块级两层保留，块级优先、无标注继承文档级 | 同一文档内不同步骤可能有不同适用版本 |
| D3 | 流程严格不拆（方案 A 级原子性），大小约束让位，上限 8000 rune | 运维场景截断命令比召不回危害更大 |
| D4 | `![[embed]]` 入库前内联展开；普通引用物化为边表但不做图遍历 | 展开解决语义缺块；边表为将来升级留扩展点，不过度设计 |
| D5 | 失效/循环引用不阻断入库，标记 `resolved=0` | 与现有 Enrich/Embed 静默降级约定一致 |
| D6 | token 用 rune 近似，不引入 tokenizer（沿用旧草稿 D7） | 精度足够做预算闸门 |
| D7 | 链接一跳扩展默认关闭，先拿边表的可解释性与断链检测收益（沿用旧草稿 D6） | 与上下文预算冲突，能力预留 |

### 2.1 与旧草稿（2026-08-31）的决策差异

| 旧草稿决策 | 本 spec | 原因 |
|---|---|---|
| D1：手写围栏状态机 + 启发式节点树（`structure.go`） | goldmark AST 驱动 | `#` 代码注释误判标题等问题由解析器天然解决，无需维护状态机；中文 `步骤一` 等启发式仍保留为流程判定补充 |
| D3：版本硬过滤 + D4 三级降级链 | 版本过滤透传，无标注块始终命中，无强制降级链 | 本次确认不依赖 CMDB 固件版本（旧 Q1 无答案），硬过滤在版本缺失时风险高于收益；降级链待 CMDB 版本可用后再议 |
| D5：检索侧流程补全（small-to-big） | 不做检索侧补全 | 流程入库即原子单块，补全无必要；省一次 `ListKbChunksByFlow` 查询 |
| 超长代码/表格/流程按行/步切分 | 流程绝不切；代码/表格整块独立成 chunk（超 8000 告警但仍不拆） | 用户明确选择严格原子化（A） |
| `kb_links` 表（src_chunk_id/target_raw/target_doc_id/target_anchor/link_type） | `kb_chunk_refs` 表（见 §4，字段基本对应，+ `ref_type=embed` + `resolved` + `doc_id`） | 覆盖 embed 类型与解析状态，支持内联成功率度量 |

旧草稿 Q1（CMDB 版本）、Q2（自产/外购占比）、Q3（token 预算切分）仍开放，不阻塞本 spec（分别由 D3 回退语义、D2 被动解析、8KB 单块上限解耦）。

## 3. 总体架构

`kb.Service` 的 `Seed`/`Upload` 对外接口与 `retrieve.Searcher` 接口不变，调用方零改动。

```
Upload/Seed：
  Parse（含 frontmatter 剥离，§5）→ InlineEmbeds（§6）→ Chunk（AST 重写，§5）
    → Tag → Enrich → Embed → CDTA → WithKBTx 事务入库（含 kb_chunk_refs）
```

新增/改动模块：

| 模块 | 文件 | 职责 |
|---|---|---|
| frontmatter 剥离 | `internal/kb/parser.go`（改） | 剥 YAML frontmatter，返回 `(content, docMeta)`；保留 `ParseDoc` wrapper 兼容旧调用 |
| 切分 | `internal/kb/chunk.go`（重写） | goldmark AST → 原子块标记 → 流程闭合 → `[]ChunkResult`（+`HeadingPath`/`IsFlow`） |
| 引用 | `internal/kb/refs.go`（新） | `ExtractRefs` / `InlineEmbeds`（lookup 回调，循环保护 2 层） |
| 版本 | `internal/kb/version.go`（新） | `ParseDocVersion` / `ParseChunkVersion` / semver 归一化 |
| 存储 | `internal/store/store.go`（migrate）、`store/kb.go` | 新列 + `kb_chunk_refs` 表 + CRUD |
| 模型 | `internal/model/kb.go` | `KbChunk` 扩字段，新增 `ChunkRef` |
| 检索 | `store.ListKnowledgeChunks` + `context.Query` | 新增可选 `Version` 过滤透传，无参数时零行为变化 |

依赖新增：`github.com/yuin/goldmark v1.7+`（纯 Go）；frontmatter 用标准库手写解析（`---` 包裹 + `key: value` 行扫描，避免引入 yaml 依赖）。

## 4. 数据模型

`migrate()` 一律用 `ensureColumn` / `CREATE TABLE IF NOT EXISTS` 无损增量。

### 4.1 `kb_documents` 新增列

```sql
ALTER TABLE kb_documents ADD COLUMN version TEXT NOT NULL DEFAULT '';
ALTER TABLE kb_documents ADD COLUMN version_min TEXT NOT NULL DEFAULT '';
ALTER TABLE kb_documents ADD COLUMN version_max TEXT NOT NULL DEFAULT '';
```

- 来源优先级：frontmatter `version:` → 文档内 `@version x.y.z` → `Available since vX`；无标注留空（过滤时始终通过）。
- `version` 存原始标注（如 `v3.0+`），`version_min/max` 存归一化 semver（`3` → `3.0.0`，`+`/`>=` 只产生下界）。

### 4.2 `kb_chunks` 新增列

```sql
ALTER TABLE kb_chunks ADD COLUMN version_min TEXT NOT NULL DEFAULT '';
ALTER TABLE kb_chunks ADD COLUMN version_max TEXT NOT NULL DEFAULT '';
ALTER TABLE kb_chunks ADD COLUMN heading_path TEXT NOT NULL DEFAULT '';
ALTER TABLE kb_chunks ADD COLUMN source_refs TEXT NOT NULL DEFAULT '[]';
ALTER TABLE kb_chunks ADD COLUMN is_flow INTEGER NOT NULL DEFAULT 0;
```

- `heading_path` 如 `GPU 运维 > 掉卡处置`（`heading` 保留叶子标题，兼容现有回显）。
- `source_refs` JSON 数组：`[{"raw":"[[config]]","type":"wikilink","target":"config.md","anchor":"","resolved":true}]`。
- 存量行默认值 `''/0/[]`，读取 `ParseJSON` 回退。

### 4.3 新表 `kb_chunk_refs`

```sql
CREATE TABLE IF NOT EXISTS kb_chunk_refs (
  id TEXT PRIMARY KEY,
  chunk_id TEXT NOT NULL,
  doc_id TEXT NOT NULL,
  target_path TEXT NOT NULL,
  target_doc_id TEXT NOT NULL DEFAULT '',
  anchor TEXT NOT NULL DEFAULT '',
  ref_type TEXT NOT NULL,   -- md_link | wikilink | embed | anchor
  resolved INTEGER NOT NULL DEFAULT 0,
  created_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_refs_chunk ON kb_chunk_refs(chunk_id);
CREATE INDEX IF NOT EXISTS idx_refs_target ON kb_chunk_refs(target_doc_id);
```

### 4.4 Go 模型（`internal/model/kb.go`）

```go
type ChunkRef struct {
    Raw, Type, Target, Anchor string
    Resolved bool
}
type KbChunk struct {
    // ...现有字段
    VersionMin  string     `json:"version_min"`
    VersionMax  string     `json:"version_max"`
    HeadingPath string     `json:"heading_path"`
    SourceRefs  []ChunkRef `json:"source_refs"`
    IsFlow      bool       `json:"is_flow"`
}
```

`KbKnowledgeChunk` 联查透传版本字段供过滤。

## 5. 切块算法（AST 语义闭合）

替代 `chunk.go:44` 逐行标题切分：

```
Chunk(content) []ChunkResult:
1. content 已由 Parse 剥离 frontmatter、InlineEmbeds 展开 embed
2. ast = goldmark.Parse(content)；失败 → 回退现有逐行标题切块（永不丢文档）
3. Walk(ast)：顶层块按 Heading（1-6 级，含 Setext 式）组织为 Section{level, title, path, blocks[]}
4. 对每个 Section:
   - 含 OrderedList → 整个 Section（标题+前言+有序列表+其间表格/代码块/引用块）合并为 1 个 FlowChunk；
     Heading=Section 标题，HeadingPath=父链，IsFlow=true；不做 2000 rune 截断（上限 MaxFlowChunkSize=8000，超则日志告警仍不拆）
   - 含 Table/FencedCodeBlock 且 Section 无 OrderedList → 该原子块独立成 chunk（标题继承 Section 标题）
   - 否则：累积段落至 DefaultChunkSize(2000) 切，切点只允许在段落结束/Heading 开始/原子块之间
5. 块内顺序 = AST 文档序（步骤顺序与条件分支位置天然保留）
```

原子块（不可拆）：`OrderedList`/`List`（整体，含缩进子列表）、`Table`（整表）、`FencedCodeBlock`/`CodeBlock`（整体，含语言标记）、`Blockquote`（条件分支常用载体）。

补充规则：标题正则扩展到 `#{1,6}`（现状 `chunk.go:17 headingRe` 仅 1-4）；围栏未闭合回退逐行模式并告警；空块丢弃（与现有行为一致）。

长度口径：`DefaultChunkSize=2000` 保留为非流程块上限；`service.go:199 embedTexts` 的 `truncateRunes(t, 8000)` 已与 8000 上限对齐，无需改动。流程块向量稀释由既有 `KBEnhanceSummary` 摘要与问题索引缓解。

## 6. 版本与引用处理

### 6.1 版本抽取（`kb/version.go`）

```
ParseDocVersion(content, frontmatter) VersionRange:
1. frontmatter.version 优先（"2.1.0" / ">=3.0" / "v3.0+"）
2. 否则扫描正文前 30 行：@version\s+v?([\d.]+) | Available since v?([\d.]+) | 适用于 v?([\d.]+)及以上
3. 归一化 semver 三段；操作符仅支持 >= / 精确
ParseChunkVersion(blockText, docVer): 块内首个 Available since/仅 vX 可用 → chunkVer；无标注继承 docVer；空=全版本适用
```

匹配语义：检索 `version=3.1.0` 时 `chunk.version_min <= 3.1.0 <= (version_max || ∞)`；无标注块始终命中。块级 `version_min > doc.version` 时告警日志但块级优先。

### 6.2 引用抽取与 embed 内联（`kb/refs.go`）

```
ExtractRefs(content) []Ref: 全文一次扫描（保行号），切块后按行区间归属到块
- md_link:  \[(text)\]\((target)(?:#(anchor))?\)
- wikilink: \[[(target)(?:#(anchor))?(?:\|...)?\]]
- embed:    !\[[(target)(?:#(anchor))?\]]
- anchor:   \[(text)\]\(#(anchor)\)  （文档内跳转）
InlineEmbeds(content, lookup) string: Chunk 之前执行
1. 每个 ![[target#anchor]] 经 lookup（按 SourceFile/Title 匹配）取目标文档
2. 命中：抽 anchor 对应 heading 段（无 anchor 则全文≤1500 rune），替换为：
   > 📎 嵌入自 [[target#anchor]]（vX.Y.Z）
   > <被引段落原文>；Ref.Resolved=true
3. 未命中：保留原文标记，Resolved=false
4. 循环保护：visited 集合，A→B→A 第二层保留标记不展开，最深 2 层
```

边物化：`insertEnrichedDoc` 事务内按块写 `kb_chunk_refs`；md_link/wikilink 入库时 `lookup` 一次回填 `target_doc_id`。失效/循环不阻断入库。

### 6.3 检索侧透传（最小改动）

`context.Query` 增加可选 `Version string`；`store.ListKnowledgeChunks` 追加 `WHERE (version_min='' OR version_min<=?) AND (version_max='' OR version_max>=?)`。无 version 参数时与现状完全一致。

## 7. 配置

| 变量 | 默认 | 说明 |
|---|---|---|
| `KB_FLOW_MAX_SIZE` | `8000` | 流程块 rune 上限（超则告警仍不拆） |
| `KB_EMBED_DEPTH` | `2` | embed 内联最大层数 |

遵循现有约定：`0`/`false` 关闭；`LLM_API_KEY` 为空时 Enrich 照常回退，本 spec 无新增 LLM 依赖。

## 8. 测试计划（TDD，每任务独立可验证）

| # | 任务 | 失败测试先行 | 通过标准 |
|---|---|---|---|
| 1 | frontmatter 剥离 + `ParseDoc` 返回 docMeta | `parser_test.go: TestParseFrontmatter` | 旧调用点编译通过（wrapper 保留） |
| 2 | AST 切块原子块不拆 | `chunk_test.go: TestChunkKeepsTableIntact / KeepsCodeFence / KeepsOrderedList` | 现有 6 个 `TestChunk*` 全绿 |
| 3 | 流程闭合 | `TestChunkFlowAtomicity`（7 步+分支+表格断言 1 块且顺序保留） | `is_flow=1` 且 ≤8000 |
| 4 | 版本抽取与继承 | `version_test.go` | 块级优先、空继承 |
| 5 | 引用抽取 + 内联 + 循环保护 | `refs_test.go` | 未命中保留标记、循环 2 层截断 |
| 6 | store 迁移 + 边表 CRUD | `store/kb_refs_test.go` | 存量 DB 无损升级 |
| 7 | Upload 全链路集成 | `service_flow_test.go`（流程/版本/embed 端到端） | `go test ./...` 全绿 |
| 8 | 检索版本过滤 | `TestVersionFilter` | 无 version 参数零行为变化 |
| 9 | 存量回填 `go run ./cmd/kb-rechunk --dry-run` | staging DB 手动验证 | 默认关闭，不阻塞上线 |

## 9. 迁移与回滚

- **线上**：`migrate()` 自动加列/建表，存量块新列默认空值，行为零变化。
- **回填**：一次性命令 `cmd/kb-rechunk`（复用 Chunk+ExtractRefs+ParseVersion），按 `doc_id` 重切重写 `kb_chunks` + `kb_chunk_refs`；`--dry-run` 先输出差异统计（块数变化/超长流程数/未解析引用数）。
- **回滚**：`SELECT` 显式列名，旧代码可读新表；已重切块可由 `kb_documents.content` 全文重建。

## 10. 成功标准

1. 含步骤+表格+代码块的技术文档：表格行/列表项跨块率 = 0（golden 集断言）。
2. 版本标注保留率 100%（文档级+块级 golden 断言）；版本过滤 precision/recall ≥ 0.95（`rageval` 扩展用例）。
3. embed 可解析引用 ≥95% 内联成功；循环/失效 100% 不阻断且 `resolved=0`。
4. `go test ./...` + `make eval-ci` 全绿；P95 上传延迟增幅 ≤30%。

## 11. 明确不做（YAGNI）

- 不引入 `[@label]`/`(@node_id)` 编写规范（外购手册无法要求重写）。
- 不做多跳图检索，仅物化一跳边。
- 不做版本差异比对/升级提示、不改 chat 侧与前端接口（引用健康页为二期）。
