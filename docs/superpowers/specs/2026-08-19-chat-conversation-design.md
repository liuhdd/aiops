# 智能助手独立弹窗与会话隔离设计

## 背景与目标

当前智能助手（`ChatPanel`）内嵌在运维面板（ops）右栏，固定宽 360px，始终可见；后端 `messages` 为全局扁平列表（无会话概念），助手短程记忆取全局最近 10 条消息（`context/assembler.go` 的 `RecentMessages(10)`）。

本设计将智能助手改造为**全局右下角悬浮面板**：点击按钮弹出/收缩，对话历史可上下滚动，支持**开启新对话**，并做**后端会话隔离**（不同会话的历史与上下文互不影响，前端可切换回旧会话）。

**目标**：助手独立成弹窗、会话级隔离、可切换旧会话、历史可滚动。

**非目标**：不改告警/流水线数据与流程；不引入路由库；不做会话重命名/删除/搜索；不做多端会话同步。

## 总体方案

采用「完整会话实体」：后端新增 `conversations` 表与 `messages.conversation_id` 列，新增会话 API；助手短程记忆按会话过滤；前端助手改为全局悬浮面板（`FloatButton` 触发），面板内含会话切换与「新对话」。

## 后端

### 数据模型与迁移

新增 `conversations` 表：

```sql
CREATE TABLE IF NOT EXISTS conversations (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL DEFAULT '',
  created_at DATETIME,
  updated_at DATETIME)
```

`messages` 表复用现有 `ensureColumn(s.db, "messages", "conversation_id", "conversation_id TEXT")` 加列。

旧数据迁移：迁移时若存在 `conversation_id IS NULL` 的消息，创建一条标题为「历史对话」的会话，并将这些消息回填到该会话。

`model` 层：

- 新增 `model.Conversation{ ID, Title string; CreatedAt, UpdatedAt time.Time }`
- `model.Message` 增加 `ConversationID string` → JSON `conversation_id,omitempty`

`store` 层（`internal/store/store.go`）新增/调整：

- `CreateConversation(title string) (Conversation, error)` — 自动生成 ID 与时间戳
- `ListConversations() ([]Conversation, error)` — 按 `updated_at DESC`（最近活跃在前）
- `GetConversation(id) (Conversation, error)` — 不存在返回 `ErrConversationNotFound`
- `SetConversationTitle(id, title)`、`TouchConversation(id)`（更新 `updated_at`）
- `SaveMessage(m)` — 写入 `conversation_id`
- `ListMessages(conversationID)` — 按会话取，`created_at ASC`
- `RecentMessages(conversationID string, n int)` — 改签名；`conversationID==""` 时保持全局行为（流水线上下文向后兼容），否则只取该会话最近 n 条

### API

新增/调整（`internal/api/server.go`）：

- `GET /api/conversations` → `[]Conversation`（活跃倒序）
- `POST /api/conversations` → 创建新会话，返回 `Conversation`（初始 title 为空，第一条用户消息后自动命名）
- `GET /api/conversations/{id}/messages` → `[]ChatMessage`；id 不存在返回 404
- `POST /api/chat` → 请求体改为 `{ "text": string, "conversation_id": string }`；`conversation_id` 必填且必须存在，否则 400/404
- WS `message` 事件 payload 的 `ChatMessage` 现在带 `conversation_id`，前端据此路由到对应会话

### 助手与上下文改造

- `agent.Handle` 签名变化：`Handle(ctx, conversationID string, userText string)`（conversationID 放在 userText 前）
  - 用户/助手消息都带 `conversationID` 入库
  - 若会话 title 为空：取第一条用户消息截断约 20 字作为 title
  - 每次消息后 `TouchConversation` 更新活跃时间
- `ctxpkg.Query` 增加 `ConversationID string`
- `Assembler.candidates` 改用 `RecentMessages(q.ConversationID, 10)`
- `ctxpkg.Store.RecentMessages` 签名同步改为 `(conversationID string, n int)`，`context/store.go` 透传

上下文贯穿：`Handle → answer → buildAssembled → Assemble(Query{ConversationID})`，确保轻量 LLM 与规则路径的上下文都限定在当前会话；流水线（无 conversationID）维持全局最近消息原行为。

## 前端

### App.tsx

- 移除运维面板里 360px 的 `<ChatPanel>` 列，布局只剩告警列表 + 排查进度两栏
- 根节点全局渲染一个 `<ChatAssistant />`（fixed 定位，与 view 无关），三个页面都可用

### 新组件 ChatAssistant.tsx（容器）

- **触发按钮**：AntD `FloatButton`，固定右下角，图标 `MessageOutlined`；点击切换面板展开/收起
- **悬浮面板**：fixed 右下角（约 380×560），由头部 + 消息区 + 输入区组成：
  - 头部：会话下拉（`Select`，显示各会话标题，切换即加载该会话消息）+「新对话」按钮 + 收起按钮
  - 消息区：`overflowY: auto` 可上下滚动，新消息/切会话后自动滚到底部；空会话显示引导语
  - 输入区：复用现有输入框 + 发送逻辑
- **状态**（组件内 useState/useEffect，自包含）：
  - `conversations` 列表、`currentId`、`messagesByConversation: Record<id, ChatMessage[]>`、`open`
  - 挂载时 `GET /api/conversations`：有则选中最近一个并加载其消息；无则自动 `POST` 建一个
  - 「新对话」→ `POST /api/conversations` 并切换
  - 发送 → `POST /api/chat`（带 `conversation_id`），成功后重取当前会话消息
  - WS `message` 事件 → 按 `conversation_id` 追加到对应会话，并刷新会话列表（活跃排序）

### ChatPanel.tsx 重构为纯展示

props 改为 `{ messages, onSend, onNewConversation, onSelectConversation, conversations, currentId }`，不再内嵌 `<Card>`；消息区保留可滚动样式并加自动滚动（`useEffect` 滚动到底部）。

### types.ts

新增 `Conversation` 接口；`ChatMessage` 增加 `conversation_id?`。

## 测试

### 后端（`go test ./...`）

- `store_test.go`：conversations CRUD（创建/列表/查询/标题/活跃排序/不存在报错）、`ListMessages` 按会话过滤、`RecentMessages` 按会话过滤、空会话返回空切片、旧消息迁移回填「历史对话」
- `agent_test.go`：`Handle(ctx, convID, text)` 入库带 conversation_id、首条消息自动命名 title、消息落库后会话 updated_at 更新
- `server_test.go`：`GET/POST /api/conversations`、`GET /api/conversations/:id/messages`、`POST /api/chat` 缺/错 conversation_id 报错、带 conversation_id 正常返回
- `context/store_test.go`：`RecentMessages(convID, n)` 过滤正确

### 前端（`npx vitest run`）

- `ChatPanel.test.tsx` 更新为新 props，保留「渲染消息、发送、空引导语」用例
- 新增 `ChatAssistant.test.tsx`：
  - 初始收起，点按钮展开/再点收起
  - 「新对话」创建并切换到空会话
  - 会话下拉可切换、显示对应消息
  - 消息区有滚动容器（`overflowY` 断言）
  - 发送调用 `postChat` 带当前 `conversation_id`

### 验证命令

- 后端：`go test ./...`
- 前端：`npx vitest run` + `npm run build`

## 设计决策与取舍

1. **完整会话实体而非最小隔离**：用户明确选择后端会话隔离且要切换回旧会话，需要标题/活跃排序等元信息，故新增 `conversations` 表而非仅 `conversation_id` 列。
2. **旧数据迁移进「历史对话」**：避免存量消息丢失，迁移在 `store.migrate()` 内完成，复用 `ensureColumn` 机制。
3. **`conversationID==""` 保持全局行为**：流水线 stage 的 `Query` 无会话概念，`RecentMessages` 空 id 回退到全局最近消息，避免影响流水线上下文组装。
4. **悬浮面板非 Modal/Drawer**：助手需与页面操作并存，非阻塞悬浮窗体验更符合「小窗助手」心智。
5. **组件自包含状态**：`ChatAssistant` 独占会话状态，`App.tsx` 只需渲染一次，运维面板布局简化。