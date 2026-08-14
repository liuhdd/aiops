# 意图树管理设计

## 背景与目标

当前聊天 Agent 的意图由 `backend/internal/agent/intent.go` 硬编码：`ParseIntent` 纯规则关键词匹配，`classifyWithLight` 写死 6 类闭集，意图集合不可管理。用户无法增删改、停用/启用意图，无法分组，也无法为意图预留知识库/MCP 绑定。

本设计引入**意图树管理**：意图以树形组织（分组节点 + 意图叶子），支持增删改查、停用启用、软删除、分组递归组树；意图叶子可绑定知识库（KB）或 MCP 工具（MCP），内置意图（SYSTEM）不绑定。树同时作为**运行时识别的唯一事实源**——规则匹配与 LLM 分类闭集均从树动态派生。

**核心不变量**：DB 是唯一事实源，缓存（进程内 Redis 抽象）只读；任何写操作都清缓存，改完节点下次请求自动从 DB 重建。

**目标**：提供意图的全生命周期管理（前端管理页 → REST API → 服务层 → DB/缓存）；写操作全留痕；批量启停/删除带「子树完整性」保护；运行时识别改由树驱动，现有 6 类意图行为保持不变（回归护栏）。

**非目标**：不实现知识库与 MCP 工具本身的执行（仅预留字段与绑定、识别占位）；不做软删除后的恢复；不引入真实 Redis 依赖；不引入前端路由库；不改 `extractAlertID`/`extractQuery`；不改 `answerFor`/`answerWithLLM` 的回答逻辑。

## 数据模型（`internal/model/intent.go` + `store.migrate()`）

### `intent_nodes` 表（邻接表，单虚拟根）

```sql
CREATE TABLE intent_nodes (
  id TEXT PRIMARY KEY,
  parent_id TEXT NOT NULL DEFAULT '',   -- '' 表示顶层（森林根）
  name TEXT NOT NULL,
  kind TEXT NOT NULL,                   -- GROUP | SYSTEM | KB | MCP
  code TEXT NOT NULL DEFAULT '',        -- 仅叶子；内部意图标识
  bind_target TEXT NOT NULL DEFAULT '', -- KB=kbId / MCP=mcpToolId / 其他=''
  rule_keywords TEXT NOT NULL DEFAULT '[]', -- 仅叶子；JSON 字符串数组，规则命中关键词
  description TEXT NOT NULL DEFAULT '',     -- 仅叶子；LLM 分类提示词用
  enabled INTEGER NOT NULL DEFAULT 1,
  deleted INTEGER NOT NULL DEFAULT 0,   -- 软删除
  sort_order INTEGER NOT NULL DEFAULT 0,
  created_at DATETIME,
  updated_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_intent_parent ON intent_nodes(parent_id, sort_order);
```

`rule_keywords` 存 JSON 字符串数组，读写时用 `encoding/json` 序列化（服务层负责，模型暴露 `[]string`）。

### kind 语义与约束

| kind | 是否叶子 | 绑定 | code |
|---|---|---|---|
| `GROUP` | 否（可挂任意子节点） | 无 | 空 |
| `SYSTEM` | 是 | 不绑 | 必须 ∈ 内置集合 `{ListAlerts, InspectAlert, AskRootCause, AskRecommendation, AskStatus, Unknown}` |
| `KB` | 是 | `bind_target` = kbId（预留） | 用户自定，全局唯一 |
| `MCP` | 是 | `bind_target` = mcpToolId（预留） | 用户自定，全局唯一 |

硬约束（服务层强制，事务内校验）：

1. 叶子（kind != GROUP）不能有子节点；`GROUP` 可挂任意子（含嵌套分组）。
2. 叶子 `code` 在**未删除**叶子中全局唯一；`SYSTEM` 的 code 必须来自内置集合（新建或改 kind 到 SYSTEM 时校验）。
3. **改 kind 联动清绑定**：kind 变化时 `bind_target` 清空；`GROUP ↔ 叶子` 互转时，`code`/`bind_target`/`rule_keywords`/`description` 相应清空或要求补填（SYSTEM→KB/MCP 保留 code，仅清绑定；KB/MCP→SYSTEM 校验 code 属于内置集合）。
4. 同一父下的兄弟按 `sort_order` 升序；`sort_order` 冲突时按 `created_at` 兜底。
5. 移动（改 `parent_id`）时校验：新父存在且是 `GROUP`；新父不是自身或自身后代（防成环）。

### 种子数据（migrate 后表空时写入）

顶层分组 `内置意图`（GROUP，sort_order=0），其下 6 个 SYSTEM 叶子，`rule_keywords` 复刻现有 `ParseIntent` 关键词（保回归）。种子顺序（`sort_order`）复刻现有 switch 命中顺序：AskRootCause(0)、AskRecommendation(1)、AskStatus(2)、InspectAlert(3)、ListAlerts(4)、Unknown(5)。

| 叶子 | rule_keywords | description（LLM 提示词用） |
|---|---|---|
| AskRootCause | `["什么原因","为什么","根因"]` | 询问根因 |
| AskRecommendation | `["怎么处理","怎么办","建议","处置"]` | 询问处置建议 |
| AskStatus | `["状态","进度"]` | 询问状态/进度 |
| InspectAlert | `[]`（内置复合规则） | 排查某个告警 |
| ListAlerts | `["列表","有哪些告警","告警"]` | 查看告警列表 |
| Unknown | `[]`（兜底） | 与告警运维无关 |

`InspectAlert` 现有规则是「查/排查/看 + 告警/设备名」（复合正则 `alarmRe`），无法用纯关键词表达，故该 code 保留内置匹配器，其余叶子全走 `rule_keywords` 数据驱动。

### `intent_audit` 留痕表

```sql
CREATE TABLE intent_audit (
  id TEXT PRIMARY KEY,
  node_id TEXT NOT NULL,
  action TEXT NOT NULL,          -- create/update/delete/enable/disable/move
  operator TEXT NOT NULL,
  before_json TEXT,
  after_json TEXT,
  created_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_intent_audit_node ON intent_audit(node_id, created_at);
```

- **写操作全部留痕**：每个受影响节点写一条 audit（批量操作按节点逐条），`before_json`/`after_json` 为该节点变更前后快照。
- `operator` 取 `X-Operator` 请求头，缺省 `admin`。
- audit 只追加、不修改、不随主表软删除。

## REST API（`internal/api`，统一 `/api/intents`）

| 方法/路径 | 说明 |
|---|---|
| `GET /api/intents/tree` | 返回完整森林（非删除节点，递归 `children`），管理页渲染用 |
| `POST /api/intents` | 新建节点（分组或叶子），指定 `parent_id`（空=顶层） |
| `PATCH /api/intents/{id}` | 更新（改名/改 kind/改绑定/改关键词/改描述/移动父级/改排序） |
| `DELETE /api/intents` | 批量软删除，body `{ids:[]}`（必须向下闭合） |
| `POST /api/intents/status` | 批量启停，body `{ids:[], enabled:bool}`（必须向下闭合） |
| `GET /api/intents/leaves` | 活跃叶子意图（识别源，走缓存），供 agent/调试 |

统一 JSON 请求/响应；错误统一 `{"error":"..."}`；HTTP 状态码：400（参数/完整性/成环）、404（节点不存在）、409（code 唯一冲突）、500。

## 服务层（`internal/intent`，新包）

职责单一、可独立测试：

```go
type Service struct { st *store.Store; cache Cache }

type Cache interface {                 // 进程内缓存抽象（默认实现；Redis 接口位保留）
    GetTree(ctx context.Context) (*Tree, bool)
    PutTree(ctx context.Context, *Tree)
    Invalidate(ctx context.Context)
}

// 写操作（统一事务 + 留痕 + 清缓存；均携带 operator）：
func (s *Service) Create(ctx context.Context, operator string, req CreateReq) (*Node, error)
func (s *Service) Update(ctx context.Context, operator string, id string, req UpdateReq) (*Node, error)
func (s *Service) Delete(ctx context.Context, operator string, ids []string) error          // 软删除
func (s *Service) SetEnabled(ctx context.Context, operator string, ids []string, enabled bool) error

// 读操作（cache-aside：命中返回，未命中从 DB 重建并 PutTree）：
func (s *Service) Tree(ctx context.Context) (*Tree, error)
func (s *Service) ActiveLeaves(ctx context.Context) ([]model.LeafSpec, error)    // 从 Tree 派生
```

- 写路径永远是 `事务(DB) → 留痕 → Invalidate()`；下一次读未命中自动从 DB 重建。
- `operator` 由 API 层从 `X-Operator` 请求头读取（缺省 `admin`）透传给所有写方法。

### 缓存设计（进程内实现）

- 单键整树快照：`[]IntentNode`（全量非删除节点，含 `enabled` 标志）。
- `Tree()` 直接返回快照组树；`ActiveLeaves()` 从同一快照派生「叶子 + enabled + 祖先全 enabled 且未删除」。
- `sync.RWMutex` 保护；`Invalidate` 清指针。零外部依赖，Redis 后续替换只需实现 `Cache` 接口 + 改一处装配。

### 子树完整性保护（`validateDownwardClosed`）

- 对 `Delete` / `SetEnabled` 传入的 `ids` 集合 S：**对 S 中每个节点，其全部（未删除）后代必须 ⊆ S**。
- 不满足 → 返回 400，错误信息列出缺失节点：`子树不完整：节点 <name>(<id>) 的后代 [x,y] 未包含在本次操作中`。
- 校验在**事务内**基于 DB 最新数据执行，避免并发竞态；幂等：已处于目标状态（已删除 / 已是目标 enabled）的节点跳过。

## 运行时识别接入（`internal/agent/intent.go`）

`Intent` 由 `int` 枚举改为 `string` code（`type Intent = string`），6 个内置常量保留同名字符串值；`agent.go` 的 `switch` 相应从枚举改 code 字符串，行为不变。

识别视图类型 `model.LeafSpec`（`internal/model/intent.go`，服务层与 agent 共用）：

```go
type LeafSpec struct{ Code, Name, Description string; RuleKeywords []string }
```

新增识别器（`internal/agent/intent.go`），从活跃叶子派生：

```go
// 规则优先：按 sort_order 遍历活跃叶子；InspectAlert 用内置复合规则，其余用 rule_keywords 子串命中
func RuleMatch(leaves []model.LeafSpec, text string) Intent

// LLM 兜底：闭集 = 活跃叶子 code+name+description 动态拼提示词
func ClassifyWithLight(ctx context.Context, leaves []model.LeafSpec, text string, c ctxpkg.Completer) Intent
```

- `Agent` 新增字段 `leaves func() []model.LeafSpec`（由 `cmd/server/main.go` 注入 `svc.ActiveLeaves`）；provider 为 nil 或返回空时**回退内置 6 种子常量**（保 CI / 无服务 / 无 LLM 场景）。
- 多意图 `ParseIntentSet`、`RewriteAndSplit` 流程不变，仅识别源替换。
- `KB`/`MCP` 叶子本轮能被识别（规则/LLM 命中），但绑定执行尚未实现 → 回复占位语「已识别该意图，绑定的知识库/MCP 尚未实现」。
- `InspectAlert` 的复合正则 `alarmRe` 保留为内置匹配器（种子 `rule_keywords` 为空），其余叶子纯数据驱动；匹配优先级由 `sort_order` 决定。
- eval 侧 `Intent` 类型同步为 string，`gradeChat`/`runChat` 比较改用 code 字符串；既有回归护栏保持绿。

## 前端管理页（React）

- `App.tsx` 加顶部导航切换「运维面板 / 意图管理」（不引入路由库）。
- 新组件：
  - `IntentTree.tsx` — 递归渲染树，分组展开/折叠，叶子显示 kind 徽标 + 启停开关。
  - `IntentEditor.tsx` — 新建/编辑表单（name/kind/code/bind_target/rule_keywords/description/sort）。
- 操作：新建子节点、编辑、**批量启停（自动带整棵子树）**、**批量删除（自动带整棵子树）**、移动（改父级）。
- 前端切换/删除某节点时**先本地展开收集全部后代 id**，随请求一起提交；后端仍二次校验（事实源），拒绝时把「漏了谁」错误透出。
- `client.ts`/`types.ts` 增加意图 API 与类型。

## 错误处理

| 场景 | 行为 |
|---|---|
| 子树完整性校验失败 | 400，列出缺失后代「漏了谁」 |
| code 重复 | 409 |
| 非法 kind / SYSTEM code 非内置 | 400 |
| 父节点不存在 / 父是叶子 / 移动成环 | 400 |
| 改 kind | 服务层强制清 `bind_target`（联动） |
| 缓存未命中 | 从 DB 重建 |
| agent 无服务 / 空树 | 回退内置 6 种子 |

## 测试

- `internal/intent/`：CRUD、改 kind 清绑定、code 唯一、向下闭合缺后代报错、软删除、缓存失效（fake cache）、留痕写入。
- `internal/store/`：migrate + seed（表空写入、非空跳过）。
- `internal/agent/`：树驱动规则匹配、内置 InspectAlert 复合规则、LLM 闭集从树拼装、无 provider 回退内置种子。
- `internal/api/`：各路由（含批量启停/删除完整性错误透出）。
- `internal/eval/`：既有回归护栏 `TestRuleRegressionMainTasks`、`TestRuleRegressionGpuTempFails` 保持绿；intent 比较改用 code 字符串。
- 前端 `IntentTree.test.tsx`：树渲染、切换提交完整子树 id、错误透出「漏了谁」。

## 验证

- `go test ./...`
- `go build ./...`
- `npx vitest run`
- `npm run build`

## 设计决策与取舍

1. **邻接表 + 全量加载内存组树**：意图树规模小（几十~几百节点），SQLite 单写者下最简单、易测试、天然适配「缓存存整树快照」。物化路径/闭包表属过度设计。
2. **进程内缓存抽象（Cache 接口）**：Go 后端现无 Redis 依赖，遵守「外部数据源进程内 mock」约定；接口位保留，后续换真实 Redis 仅实现接口 + 改装配。
3. **DB 唯一事实源 + 写清缓存 + 读回源重建**：cache-aside，任何写操作 `Invalidate`，改完节点下次读自动从 DB 重建，避免缓存漂移。
4. **子树完整性校验放在 service 层**：所有写操作统一过 `validateDownwardClosed`，API/前端/测试走同一契约，不易被绕过；事务内校验防并发竞态。
5. **规则 + LLM 闭集都从树驱动**：`InspectAlert` 复合正则保留内置匹配器，其余叶子纯数据驱动；真正「树作为识别源」，消除两套事实源漂移。
6. **provider 为 nil 回退内置 6 种子**：agent 无服务/空树时仍可用，保 CI 与无 LLM 场景，回归护栏不变。
7. **软删除 + 只追加留痕**：删除可追查，audit 表不随主表删除；恢复功能本轮不做。
8. **KB/MCP 预留字段 + 识别占位**：本轮只做绑定字段、kind 联动清绑定、识别命中与占位回复，不实现知识库/MCP 执行。
