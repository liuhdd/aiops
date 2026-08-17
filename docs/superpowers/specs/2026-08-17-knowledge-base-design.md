# 知识库（Knowledge Base）设计文档

- 日期：2026-08-17
- 状态：已确认
- 范围：可管理、可检索的硬件运维知识库——运维案例 / 产品手册 / 操作手册 / 其他文档资料；支持文件上传解析、自动打标、字段筛选、意图驱动检索；数据库取代现有 mock 文档成为 RAG 唯一知识源。

## 1. 目标

1. **文档管理**：用户在管理页上传 txt/md/docx 文档，解析为纯文本入库；可编辑元数据、删除文档。
2. **结构化字段**：每篇文档带分类（案例/手册/操作手册/其他）、设备类型、厂商、型号、指标、关键词、绑定意图，管理页支持按字段组合筛选。
3. **意图驱动的 RAG 检索**：聊天 Agent 识别用户提问的意图 code，据此从知识库检索对应文档；流水线 rca/recommend 阶段按阶段映射意图检索。数据库取代 `mockdata.Docs()` 成为唯一知识源（用现有 10 条 mock 文档做种子数据）。

## 2. 已确认决策

- **录入方式**：支持文件上传（仅 `.txt` / `.md` / `.docx`）并解析，无第三方依赖（标准库 zip+xml 解析 docx）。
- **存储粒度**：文档级一条 + 自动分块；长文档按 Markdown 标题/固定大小切块，短文档退化为单块。RAG 检索粒度到块。
- **取代 mock 文档**：RAG 只从 DB 读知识；`mockdata.Docs()` 退役，其 10 条内容作为种子导入 `kb_documents`。
- **意图绑定**：文档可绑定多个意图 code；检索时用识别出的意图过滤/加权命中文档。
- **字段筛选**：device_types / vendor / model 均为多值（JSON 数组），列表接口与前端筛选栏按字段组合过滤。
- **自动打标**：解析时规则抽取 device_types / metrics / keywords，并建议绑定意图（命中叶子 rule_keywords/name/description）；vendor/model 人工填写。
- **RAG 检索逻辑**：沿用现有确定性 Selector（关键词重叠 + 标签命中 + 时效衰减），新增"意图命中"加权信号；不引入 LLM 路由。

## 3. 架构总览

新增包 `backend/internal/kb/`，四个文件、单一职责：

| 文件 | 职责 |
|------|------|
| `parser.go` | 文件解析（txt/md/docx → 纯文本） |
| `chunk.go` | 文本切块（标题切块 / 固定大小切块） |
| `tagger.go` | 自动打标（device_types/metrics/keywords/意图建议） |
| `service.go` | 上传/导入事务、种子导入、CRUD、列表筛选、检索查询 |

依赖方向：`kb → (store, model)`；`api → kb`；`store →（无新依赖）`；`context → store`。不产生环。

数据流：

- 启动：main 调 `kb.Service.SeedFromMock(mockdata.Docs())`（表空时导入）。
- 上传：multipart → `parser` 解析 → `tagger` 打标（+表单覆盖）→ `chunk` 切块 → 事务写 `kb_documents` + `kb_chunks`。
- 检索：`context.Assembler.candidates` 改为 `store.ListKnowledgeChunks()`（联查 doc+chunk，块带文档标签/意图/时间）→ 现有 `Selector` 打分（含新增意图加权）→ TopK 注入。
- 管理页：CRUD + 筛选走全局作用域（与 chat/simulator 同级）。

## 4. 数据模型

`store.migrate()` 新增 2 张表：

```sql
CREATE TABLE IF NOT EXISTS kb_documents (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  category TEXT NOT NULL,            -- case(运维案例) / manual(产品手册) / runbook(操作手册) / other
  device_types TEXT NOT NULL DEFAULT '[]',  -- JSON 数组
  vendor TEXT NOT NULL DEFAULT '[]',        -- JSON 数组（厂商）
  model TEXT NOT NULL DEFAULT '[]',         -- JSON 数组（型号）
  metrics TEXT NOT NULL DEFAULT '[]',       -- JSON 数组
  keywords TEXT NOT NULL DEFAULT '[]',      -- JSON 数组
  intents TEXT NOT NULL DEFAULT '[]',       -- JSON 数组（绑定的意图 code）
  source_file TEXT NOT NULL DEFAULT '',     -- 来源文件名
  content TEXT NOT NULL,                    -- 全文纯文本
  created_at DATETIME,
  updated_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_kb_docs_category ON kb_documents(category);

CREATE TABLE IF NOT EXISTS kb_chunks (
  id TEXT PRIMARY KEY,
  doc_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  heading TEXT NOT NULL DEFAULT '',   -- 章节标题（无则空）
  content TEXT NOT NULL,
  created_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_kb_chunks_doc ON kb_chunks(doc_id, seq);
```

- 标签/意图字段均为 JSON 数组字符串列（复用 `model.MustJSON` 编解码），空数组存 `[]`。
- 块继承文档标签与意图（检索时联查携带），块内不重复存储标签。
- 删除文档级联删块（service 内事务处理）。
- `category` 枚举常量定义在 `model` 包：`KBCase / KBManual / KBRunbook / KBOther`，校验非法值。

## 5. 文档导入管道

**`parser.go`（纯标准库）**

- 按扩展名分发：
  - `.txt` / `.md`：直接按 UTF-8 读取为文本。
  - `.docx`：`archive/zip` 解压，`encoding/xml` 解析 `word/document.xml`，抽取所有 `w:t` 文本，按段落（`w:p`）补 `\n`。
- 校验：非法扩展名、空文件、解析后空内容 → 返回明确错误（API 层映射 400）。

**`chunk.go`**

- 优先按 Markdown 标题切：行首 `^#{1,4}\s` 开启新块，标题去 `#` 后作为 `heading`；同标题后续段落归入该块。
- 无标题（或标题间文本过长）时按固定大小切：默认约 2000 字符/块，不跨块重叠。
- 每块带 `seq`（0 起）；文档至少 1 块。

**`tagger.go`**

- 输入：正文文本 + 活跃叶子列表（`[]model.LeafSpec`，供意图建议）。
- `device_types`：命中 CMDB 已知设备类型名词集合（gpu-server/server/storage/switch，取自 `mockdata.Devices()` 的 `Role` 去重集合）。
- `metrics`：命中已知指标名集合（cpu_usage/disk_usage/gpu_util/mem_usage/net_in）。
- `keywords`：命中预设运维关键词表（OOM/Xid/NVLink/扩容/掉卡/热插拔/固件 等）。
- `intents` 建议：扫描正文命中叶子的 `RuleKeywords` / `Name` / `Description` → 收集对应叶子 code（不覆盖用户表单显式选择）。
- 打标结果仅作初始值：上传表单可覆盖/补充；事后 `PATCH` 手动改。`vendor` / `model` 不自动抽取，人工填写。

**`service.go`**

- `SeedFromMock(docs []mockdata.Doc)`：`kb_documents` 为空时导入（事务），`intents` 置空。
- `Upload(ctx, req)`：解析 → 打标 → 合并表单覆盖 → 切块 → 事务写 doc + chunks；返回完整文档。
- `List(ctx, filter)`：按 category / device_type / vendor / model / intent / q(标题正文关键词) 组合筛选，返回摘要列表（含块数）。
- `Get(ctx, id)`：文档详情（含全部块）。
- `Update(ctx, id, req)`：全量覆盖元数据（title/category/各标签字段），不动 content（如需改正文走重新上传或删除重建）。
- `Delete(ctx, id)`：事务删 doc + chunks。
- 列表用 SQL 过滤 JSON 数组字段：SQLite 无原生 JSON 数组匹配，采用 `LIKE` 子串匹配 `'"<值>"'`（JSON 序列化保证唯一分隔），值需转义内部引号。

## 6. 意图驱动的 RAG 检索

**文档绑定意图**：`kb_documents.intents` 存意图 code 数组，上传/编辑时从活跃叶子下拉选择（`GET /api/intents/leaves` 已有）。

**检索链**：

1. 聊天 Agent：`answerSingle` / `answerOneStatus` 识别出意图 code（`ParseIntentWithLight`，规则优先 + 轻量 LLM 兜底，逻辑不变），把 code 传入 `ctxpkg.Query{Intents: []string{...}}`。
2. 流水线：rca 阶段传 `AskRootCause`，recommend 阶段传 `AskRecommendation`（内置 SYSTEM 意图）；随阶段 Query 注入。
3. `store.ListKnowledgeChunks()` 联查 doc+chunk，每块生成 `Chunk{Source: SourceKnowledge}`，Metadata 携带 `metrics` / `device_types` / `vendor` / `model` / `intents` / `doc_title` / `doc_id` / `created_at`。
4. `Selector.Score` 新增一路信号**意图命中**：query.Intents 与 chunk.Metadata 的 `intents` 有交集 → 该信号为 1，否则 0；权重 `intentWeight = 0.3`（与 kw 0.4 / tag 0.4 / recency 0.2 并存，重新归一化到 1.0：kw 0.3 / tag 0.3 / intent 0.25 / recency 0.15）。命中意图的文档显著排前。
5. `knowledgeSection` 回显带文档来源：`[类型] 文档标题 §heading: 块内容`。

**替换 mock**：`assembler.go` 的 `candidates()` 中 `mockdata.Docs()` 遍历替换为 `ListKnowledgeChunks()`；`mockdata.Docs()` / `docs` 变量保留（供种子导入）但不再被检索路径引用。

## 7. API

`api/server.go` 新增（全局作用域，复用 `writeJSON` / `withCORS`）：

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/kb/docs` | 列表；query 参数：`category`、`device_type`、`vendor`、`model`、`intent`、`q`，可组合 |
| GET | `/api/kb/docs/{id}` | 详情（含全部块） |
| POST | `/api/kb/upload` | multipart：`file` + `category` + 可选 `title` / `device_types` / `vendor` / `model` / `metrics` / `keywords` / `intents`（覆盖/补充自动打标） |
| PATCH | `/api/kb/docs/{id}` | JSON 覆盖元数据（不含 content） |
| DELETE | `/api/kb/docs/{id}` | 删除文档（级联删块） |

- 上传大小限制：`http.MaxBytesReader` 64MB。
- 错误映射：非法文件类型/空内容 → 400；不存在的文档 → 404；非法 category → 400。

## 8. 前端

**导航**：`App.tsx` 增加第三个 tab「知识库」，`view === 'kb'` 渲染新组件 `KnowledgeBase.tsx`。

**`KnowledgeBase.tsx`（新建）**：

- **筛选栏**：类型下拉（全部/运维案例/产品手册/操作手册/其他）、设备类型多选、厂商输入、型号输入、意图下拉（来自 `/api/intents/leaves`）、关键词输入；变更即触发列表刷新。
- **列表**：标题、分类徽标、设备类型/厂商/型号标签、块数、来源文件、更新时间；行操作：详情 / 编辑 / 删除。
- **上传表单**：文件选择（accept 限定 .txt/.md/.docx）、类型下拉、可选标题、可选各标签覆盖（多选输入）、可选意图多选；提交 `POST /api/kb/upload`（FormData）。
- **详情/编辑**：展示块列表（heading + 内容预览）；编辑弹层改元数据（`PATCH`）。

**`api/client.ts` / `types.ts`**：新增 `KbDoc` / `KbChunk` / `KbDocUpsert` 类型与 `fetchKbDocs(filter)` / `fetchKbDoc(id)` / `uploadKbDoc(form)` / `updateKbDoc(id, body)` / `deleteKbDoc(id)`。

**WS 集成**：不新增事件；管理页变更后前端本地刷新即可。

## 9. 集成改动清单

- `store/store.go`：`migrate()` 新增两张表 + 索引；新增 `KbDocument`/`KbChunk` 的 store 方法（InsertDoc/InsertChunk/ListChunksByDoc/ListDocs(filter)/GetDoc/DeleteDoc 等）。
- `model/`：新增 `kb.go`（`KbDocument`/`KbChunk`/`KBCategory` 常量），`MustJSON`/`ParseJSON` 复用。
- `context/selector.go`：`Score` 增加意图加权；`Query` 增加 `Intents []string`。
- `context/assembler.go`：`candidates()` 改用 `ListKnowledgeChunks()`；`knowledgeSection` 格式带文档标题/章节。
- `context/store.go`：透传 `ListKnowledgeChunks()`。
- `agent/agent.go`：识别意图后构建 Query 时注入 `Intents`。
- `pipeline/`：rca/recommend 阶段组装 Query 时按阶段注入意图 code。
- `cmd/server/main.go`：构造 `kb.Service`，调用 `SeedFromMock(mockdata.Docs())`；把 service 注入 `api.NewServer`。
- `mockdata/docs.go`：`Docs()` 保留供种子导入；检索路径不再引用。

## 10. 配置 / 错误处理 / 测试

**配置**：无新增 env。上传大小上限常量固定 64MB。

**错误处理**：

- 解析失败/非法类型 → 400，导入不落库。
- 打标/切块失败不阻断（结果为空数组仍可入库）。
- 级联删除、事务写入失败 → 500，无部分写入。
- 意图建议无命中 → `intents=[]`（不阻断导入）。

**测试**：

- `store/kb_test.go`：两表 CRUD、级联删除、`ListKnowledgeChunks` 联查、JSON 数组 LIKE 筛选。
- `kb/parser_test.go`：txt/md 直读、docx 解析（最小 sample 字节：手写 zip+xml 生成）、非法扩展名/空内容。
- `kb/chunk_test.go`：标题切块、无标题固定切块、单块退化、序号连续性。
- `kb/tagger_test.go`：device_types/metrics/keywords 命中/未命中、意图建议（命中/未命中叶子）。
- `kb/service_test.go`：Upload 全流程（含表单覆盖）、List 组合筛选、Update/Delete。
- `context/selector_test.go`：意图命中加权的打分变化。
- `context/assembler_test.go`：种子导入后知识块进入候选、无 mock 引用。
- `api/server_test.go`：上传端点（multipart + 校验 + 错误映射）。
- 前端 `KnowledgeBase.test.tsx`：列表渲染、筛选参数传递、上传/删除调用。