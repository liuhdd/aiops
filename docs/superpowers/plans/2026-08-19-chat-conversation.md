# 智能助手独立弹窗与会话隔离 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将智能助手改为全局右下角悬浮面板（点击弹出/收起、历史可滚动、可开新对话），并实现后端会话隔离（会话历史与上下文互不影响，前端可切换旧会话）。

**Architecture:** 后端新增 `conversations` 表与 `messages.conversation_id` 列，`RecentMessages` 按会话过滤，`agent.Handle` 签名改为 `(ctx, conversationID, text)`；前端新增 `ChatAssistant` 悬浮容器（FloatButton + 浮动面板），`ChatPanel` 重构为纯展示，App.tsx 移除内嵌聊天列。

**Tech Stack:** Go + SQLite(modernc) + Eino、React 18 + AntD v6 + Vite + Vitest。

## Global Constraints

- 注释、文档、commit message 一律中文；commit subject 用 conventional 前缀（`feat:`/`fix:`/`test:`/`docs:`/`chore:`）。
- SQLite 驱动名为 `"sqlite"`（非 `sqlite3`）；`store.New` 保持 `SetMaxOpenConns(1)`，不加连接。
- 流水线 stage 的 `Query` 无会话概念：`RecentMessages(conversationID, n)` 在 `conversationID==""` 时必须保持全局最近消息行为（向后兼容）。
- 会话 title 为空时，取第一条用户消息截断约 20 字（rune 计数）自动命名。
- 旧消息（`conversation_id IS NULL`）迁移进标题为「历史对话」的会话。
- 前端消息区必须可上下滚动（`overflowY: auto`）并自动滚到底部。
- 验证命令：后端 `go test ./...`；前端 `npx vitest run` + `npm run build`。

---

### Task 1: 后端 model + store + context 会话隔离地基

**Files:**
- Modify: `backend/internal/model/message.go`（新增 Conversation、Message.ConversationID）
- Modify: `backend/internal/store/store.go`（conversations 表/迁移/回填、会话方法、SaveMessage、ListMessages、RecentMessages、ErrConversationNotFound）
- Modify: `backend/internal/context/chunk.go`（Query.ConversationID）
- Modify: `backend/internal/context/store.go`（RecentMessages 签名）
- Modify: `backend/internal/context/assembler.go`（按会话取短程记忆）
- Test: `backend/internal/store/store_test.go`、`backend/internal/context/store_test.go`

**Interfaces:**
- Produces: `model.Conversation{ID,Title string; CreatedAt,UpdatedAt time.Time}`；`model.Message.ConversationID string`（json `conversation_id,omitempty`）；`store.ErrConversationNotFound`；`store.CreateConversation(title string) (model.Conversation, error)`；`store.ListConversations() ([]model.Conversation, error)`；`store.GetConversation(id string) (model.Conversation, error)`；`store.SetConversationTitle(id, title string) error`；`store.TouchConversation(id string) error`；`store.SaveMessage(m model.Message) error`（含 conversation_id）；`store.ListMessages(conversationID string) ([]model.Message, error)`（`""` 返回全部）；`store.RecentMessages(conversationID string, n int) ([]model.Message, error)`；`ctxpkg.Query.ConversationID string`。

- [ ] **Step 1: 写失败的 store 测试**

`backend/internal/store/store_test.go` 顶部 import 增加 `"database/sql"`（`TestMigrateBackfillsLegacyMessages` 需要）。在文件内追加：

```go
func TestConversationsCRUD(t *testing.T) {
	s := newTestStore(t)
	conv, err := s.CreateConversation("")
	if err != nil || conv.ID == "" {
		t.Fatalf("CreateConversation: %v %+v", err, conv)
	}
	if err := s.SetConversationTitle(conv.ID, "会话甲"); err != nil {
		t.Fatalf("SetConversationTitle: %v", err)
	}
	got, err := s.GetConversation(conv.ID)
	if err != nil || got.Title != "会话甲" {
		t.Fatalf("GetConversation: %v %+v", err, got)
	}
	if _, err := s.GetConversation("nope"); err != ErrConversationNotFound {
		t.Fatalf("不存在会话应返回 ErrConversationNotFound, got %v", err)
	}
	// 活跃排序：B 创建后 Touch，应排 A 前
	a, _ := s.CreateConversation("A")
	b, _ := s.CreateConversation("B")
	if err := s.TouchConversation(b.ID); err != nil {
		t.Fatalf("TouchConversation: %v", err)
	}
	list, err := s.ListConversations()
	if err != nil || len(list) != 3 {
		t.Fatalf("ListConversations: %v len=%d", err, len(list))
	}
	if list[0].ID != b.ID || list[1].ID != a.ID {
		t.Fatalf("活跃排序错误: %+v", list)
	}
}

func TestListMessagesByConversation(t *testing.T) {
	s := newTestStore(t)
	c1, _ := s.CreateConversation("")
	c2, _ := s.CreateConversation("")
	now := time.Now()
	_ = s.SaveMessage(model.Message{ID: "m1", Role: model.RoleUser, Content: "in1", ConversationID: c1.ID, CreatedAt: now})
	_ = s.SaveMessage(model.Message{ID: "m2", Role: model.RoleUser, Content: "in2", ConversationID: c1.ID, CreatedAt: now.Add(time.Minute)})
	_ = s.SaveMessage(model.Message{ID: "m3", Role: model.RoleUser, Content: "other", ConversationID: c2.ID, CreatedAt: now})
	msgs, err := s.ListMessages(c1.ID)
	if err != nil || len(msgs) != 2 || msgs[0].ID != "m1" || msgs[1].ID != "m2" {
		t.Fatalf("ListMessages(c1): %v %+v", err, msgs)
	}
	all, _ := s.ListMessages("")
	if len(all) != 3 {
		t.Fatalf("ListMessages(\"\") 应返回全部, got %d", len(all))
	}
}

func TestRecentMessagesByConversation(t *testing.T) {
	s := newTestStore(t)
	c1, _ := s.CreateConversation("")
	c2, _ := s.CreateConversation("")
	now := time.Now()
	for i := 0; i < 3; i++ {
		_ = s.SaveMessage(model.Message{ID: model.NewID("msg"), Role: model.RoleUser, Content: "a" + string(rune('0'+i)), ConversationID: c1.ID, CreatedAt: now.Add(time.Duration(i) * time.Minute)})
		_ = s.SaveMessage(model.Message{ID: model.NewID("msg"), Role: model.RoleUser, Content: "b" + string(rune('0'+i)), ConversationID: c2.ID, CreatedAt: now.Add(time.Duration(i) * time.Minute)})
	}
	got, err := s.RecentMessages(c1.ID, 2)
	if err != nil || len(got) != 2 || got[0].Content != "a1" || got[1].Content != "a2" {
		t.Fatalf("RecentMessages(c1,2): %v %+v", err, got)
	}
	// 空会话 → 空非 nil 切片
	empty, _ := s.RecentMessages("nope", 5)
	if empty == nil {
		t.Fatal("空会话 RecentMessages 应返回非 nil 切片")
	}
}

func TestMigrateBackfillsLegacyMessages(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE messages (id TEXT PRIMARY KEY, role TEXT, content TEXT, alert_id TEXT, created_at DATETIME)`); err != nil {
		t.Fatalf("create legacy messages: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO messages (id, role, content, alert_id, created_at) VALUES ('old-1','user','hi',NULL,?)`, time.Now()); err != nil {
		t.Fatalf("insert legacy: %v", err)
	}
	_ = db.Close()
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("migrate legacy db: %v", err)
	}
	defer s.Close()
	convs, _ := s.ListConversations()
	if len(convs) != 1 || convs[0].Title != "历史对话" {
		t.Fatalf("回填应创建「历史对话」, got %+v", convs)
	}
	msgs, _ := s.ListMessages(convs[0].ID)
	if len(msgs) != 1 || msgs[0].ID != "old-1" {
		t.Fatalf("旧消息应回填进历史对话, got %+v", msgs)
	}
}
```

同时更新既有用例（同文件）：

```go
// TestMessages：ListMessages() → ListMessages("")
msgs, err := s.ListMessages("")

// TestListMessagesEmptyReturnsEmptySlice：ListMessages() → ListMessages("")
msgs, err := s.ListMessages("")
```

在 `backend/internal/context/store_test.go` 更新 `TestRecentMessagesOrder` 的调用：

```go
got, err := cs.RecentMessages("", 3)
```

并追加：

```go
func TestRecentMessagesByConversationFilter(t *testing.T) {
	cs, s := newTestStore(t)
	now := time.Now()
	for i := 0; i < 3; i++ {
		_ = s.SaveMessage(model.Message{ID: model.NewID("msg"), Role: model.RoleUser, Content: "a" + string(rune('0'+i)), ConversationID: "c1", CreatedAt: now.Add(time.Duration(i) * time.Minute)})
		_ = s.SaveMessage(model.Message{ID: model.NewID("msg"), Role: model.RoleUser, Content: "b" + string(rune('0'+i)), ConversationID: "c2", CreatedAt: now.Add(time.Duration(i) * time.Minute)})
	}
	got, err := cs.RecentMessages("c1", 2)
	if err != nil || len(got) != 2 || got[0].Content != "a1" || got[1].Content != "a2" {
		t.Fatalf("RecentMessages(c1,2): %v %+v", err, got)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/store/ ./internal/context/`
Expected: 编译错误（`ErrConversationNotFound`、`CreateConversation` 等未定义；`RecentMessages` 参数数量不符）——失败原因即待实现项。

- [ ] **Step 3: 实现 model 变更**

`backend/internal/model/message.go`：

```go
package model

import "time"

type Conversation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Message struct {
	ID             string    `json:"id"`
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	AlertID        string    `json:"alert_id,omitempty"`
	ConversationID string    `json:"conversation_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)
```

（保持该文件原有其余内容不变，仅按此结构修改。）

- [ ] **Step 4: 实现 store 变更**

`backend/internal/store/store.go`：

1) 顶部 errors 区新增哨兵错误（放在 `package store` 声明之后、`type Store` 之前）：

```go
var ErrConversationNotFound = errors.New("conversation not found")
```

2) `migrate()` 中，在 `ensureColumn` 各调用之后、`seedIntents()` 之前追加：

```go
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS conversations (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL DEFAULT '',
		created_at DATETIME,
		updated_at DATETIME)`); err != nil {
		return err
	}
	if err := ensureColumn(s.db, "messages", "conversation_id", "conversation_id TEXT"); err != nil {
		return err
	}
	if err := s.backfillLegacyMessages(); err != nil {
		return err
	}
```

3) 新增回填方法（放在 `ensureColumn` 函数之后）：

```go
// backfillLegacyMessages 将存量无会话消息归入「历史对话」会话。
func (s *Store) backfillLegacyMessages() error {
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM messages WHERE conversation_id IS NULL`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	conv, err := s.CreateConversation("历史对话")
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE messages SET conversation_id = ? WHERE conversation_id IS NULL`, conv.ID)
	return err
}
```

4) 替换 `SaveMessage`、`ListMessages`、`RecentMessages`，并新增会话方法（替换原 `ListMessages` 与 `RecentMessages` 位置）：

```go
// ---- 会话 ----

func (s *Store) CreateConversation(title string) (model.Conversation, error) {
	now := time.Now()
	c := model.Conversation{ID: model.NewID("conv"), Title: title, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.Exec(`INSERT INTO conversations (id, title, created_at, updated_at) VALUES (?,?,?,?)`,
		c.ID, c.Title, c.CreatedAt, c.UpdatedAt)
	return c, err
}

func (s *Store) ListConversations() ([]model.Conversation, error) {
	rows, err := s.db.Query(`SELECT id, title, created_at, updated_at FROM conversations ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.Conversation, 0)
	for rows.Next() {
		var c model.Conversation
		if err := rows.Scan(&c.ID, &c.Title, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetConversation(id string) (model.Conversation, error) {
	var c model.Conversation
	err := s.db.QueryRow(`SELECT id, title, created_at, updated_at FROM conversations WHERE id = ?`, id).
		Scan(&c.ID, &c.Title, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrConversationNotFound
	}
	return c, err
}

func (s *Store) SetConversationTitle(id, title string) error {
	_, err := s.db.Exec(`UPDATE conversations SET title = ? WHERE id = ?`, title, id)
	return err
}

func (s *Store) TouchConversation(id string) error {
	_, err := s.db.Exec(`UPDATE conversations SET updated_at = ? WHERE id = ?`, time.Now(), id)
	return err
}

func (s *Store) SaveMessage(m model.Message) error {
	_, err := s.db.Exec(`INSERT INTO messages (id, role, content, alert_id, conversation_id, created_at) VALUES (?,?,?,?,?,?)`,
		m.ID, m.Role, m.Content, m.AlertID, m.ConversationID, m.CreatedAt)
	return err
}

func (s *Store) ListMessages(conversationID string) ([]model.Message, error) {
	var rows *sql.Rows
	var err error
	if conversationID == "" {
		rows, err = s.db.Query(`SELECT id, role, content, alert_id, conversation_id, created_at FROM messages ORDER BY created_at ASC`)
	} else {
		rows, err = s.db.Query(`SELECT id, role, content, alert_id, conversation_id, created_at FROM messages WHERE conversation_id = ? ORDER BY created_at ASC`, conversationID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.Message, 0)
	for rows.Next() {
		var m model.Message
		var convID sql.NullString
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.AlertID, &convID, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.ConversationID = convID.String
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) RecentMessages(conversationID string, n int) ([]model.Message, error) {
	var rows *sql.Rows
	var err error
	if conversationID == "" {
		rows, err = s.db.Query(`SELECT id, role, content, alert_id, conversation_id, created_at FROM messages ORDER BY created_at DESC LIMIT ?`, n)
	} else {
		rows, err = s.db.Query(`SELECT id, role, content, alert_id, conversation_id, created_at FROM messages WHERE conversation_id = ? ORDER BY created_at DESC LIMIT ?`, conversationID, n)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var desc []model.Message
	for rows.Next() {
		var m model.Message
		var convID sql.NullString
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.AlertID, &convID, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.ConversationID = convID.String
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

- [ ] **Step 5: 实现 context 变更**

`backend/internal/context/chunk.go` 的 `Query` 增加字段：

```go
type Query struct {
	Device         string
	Metric         string
	Severity       string
	Stage          string
	RunID          string
	ConversationID string
	Keywords       []string
	Intents        []string
}
```

`backend/internal/context/store.go` 改签名：

```go
func (cs *Store) RecentMessages(conversationID string, n int) ([]model.Message, error) {
	return cs.st.RecentMessages(conversationID, n)
}
```

`backend/internal/context/assembler.go` 第 86 行改为：

```go
	msgs, err := a.cs.RecentMessages(q.ConversationID, 10)
```

- [ ] **Step 6: 运行测试确认通过**

Run: `go test ./internal/store/ ./internal/context/ ./internal/llm/ ./internal/pipeline/`
Expected: 全部 PASS。（agent 包的测试文件因 `ListMessages` 签名变化会编译失败，属预期，Task 2 一并修复；`go build ./...` 此时应通过，因为 agent 非测试代码不直接调用 `ListMessages`。）

- [ ] **Step 7: 提交**

```bash
git add backend/internal/model/message.go backend/internal/store/store.go backend/internal/store/store_test.go backend/internal/context/chunk.go backend/internal/context/store.go backend/internal/context/store_test.go backend/internal/context/assembler.go
git commit -m "feat: 会话实体与消息会话隔离（conversations 表/迁移回填/RecentMessages 按会话过滤）"
```

---

### Task 2: 后端 agent 会话贯穿

**Files:**
- Modify: `backend/internal/agent/agent.go`（Handle 签名、answer 链、buildAssembled、kbReply、memorySuffix、titleFor）
- Test: `backend/internal/agent/agent_test.go`（更新全部 Handle 调用 + 新增会话用例）

**Interfaces:**
- Consumes: Task 1 的 `store.GetConversation/SetConversationTitle/TouchConversation`、`model.Message.ConversationID`、`ctxpkg.Query.ConversationID`。
- Produces: `agent.Handle(ctx context.Context, conversationID string, userText string) (model.Message, error)`；私有 `titleFor(text string) string`。

- [ ] **Step 1: 更新既有测试的 Handle 调用 + 写失败的新测试**

在 `backend/internal/agent/agent_test.go`，将全部 `ag.Handle(context.Background(), "..."` 改为 `ag.Handle(context.Background(), "", "..."`（共 10 处：`TestAgentInspectAlertTriggersRun`、`TestAgentRuleAnswerIncludesLongTermMemory`、`TestAgentLLMAnswerUsesAssembledContext`、`TestAgentLLMFallsBackToRule`、`TestAgentRecognizesCustomLeaf`、`TestHandleMultiIntentAggregation`、`TestHandleRootCauseQuestionNotDegradedByRewrite`、`TestHandleUnknownGuidance`、`TestHandleAggregationUnknownAndSkipped`、`TestAgentKBIntentReturnsKnowledge`、`TestAgentKBIntentFallsBackToPlaceholder`、`TestAgentKBReplyFiltersByIntent`）。

`TestAgentInspectAlertTriggersRun` 中 `msgs, _ := s.ListMessages()` 改为 `msgs, _ := s.ListMessages("")`。

追加新用例：

```go
func TestHandleSavesMessagesInConversation(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	conv, _ := s.CreateConversation("")
	ag := New(s, nil, nil)
	_, err := ag.Handle(context.Background(), conv.ID, "有哪些告警")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	msgs, _ := s.ListMessages(conv.ID)
	if len(msgs) != 2 {
		t.Fatalf("会话应落库 2 条消息, got %d", len(msgs))
	}
	for _, m := range msgs {
		if m.ConversationID != conv.ID {
			t.Fatalf("消息 conversation_id = %q, want %q", m.ConversationID, conv.ID)
		}
	}
	c, _ := s.GetConversation(conv.ID)
	if c.Title == "" {
		t.Fatal("首条消息后应自动命名会话标题")
	}
	if c.UpdatedAt.Before(conv.UpdatedAt) {
		t.Fatal("消息后会话 updated_at 应更新")
	}
}

func TestHandleTitlesConversationFromFirstMessage(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	conv, _ := s.CreateConversation("")
	ag := New(s, nil, nil)
	_, _ = ag.Handle(context.Background(), conv.ID, "这是一个很长的第一句话需要被截断到二十个字符以内")
	c, _ := s.GetConversation(conv.ID)
	if len([]rune(c.Title)) != 20 {
		t.Fatalf("title=%q rune 数=%d, want 20", c.Title, len([]rune(c.Title)))
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent/`
Expected: 编译失败（`Handle` 参数数量不符）——确认新增用例待实现。

- [ ] **Step 3: 实现 agent 变更**

`backend/internal/agent/agent.go`：

1) `Handle` 改为：

```go
func (a *Agent) Handle(ctx context.Context, conversationID string, userText string) (model.Message, error) {
	userMsg := model.Message{
		ID: model.NewID("msg"), Role: model.RoleUser,
		Content: userText, ConversationID: conversationID, CreatedAt: time.Now(),
	}
	_ = a.store.SaveMessage(userMsg)
	if conv, err := a.store.GetConversation(conversationID); err == nil && conv.Title == "" {
		_ = a.store.SetConversationTitle(conversationID, titleFor(userText))
	}
	reply := a.answer(ctx, conversationID, userText)
	assistantMsg := model.Message{
		ID: model.NewID("msg"), Role: model.RoleAssistant,
		Content: reply, ConversationID: conversationID, CreatedAt: time.Now(),
	}
	if alertID := extractAlertID(userText, a.store); alertID != "" {
		assistantMsg.AlertID = alertID
	}
	_ = a.store.SaveMessage(assistantMsg)
	_ = a.store.TouchConversation(conversationID)
	return assistantMsg, nil
}

// titleFor 取首条消息前 20 个 rune 作为会话标题。
func titleFor(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) > 20 {
		runes = runes[:20]
	}
	return string(runes)
}
```

2) `answer` 改为（`Handle` 下方原 `answer` 函数整体替换）：

```go
// answer 多意图优先：有轻量 LLM 时重写拆分后逐子问题聚合；否则回退单意图规则路径。
func (a *Agent) answer(ctx context.Context, conversationID string, userText string) string {
	if a.lightLLM == nil || ctxpkg.IsNilCompleter(a.lightLLM) {
		return a.answerSingle(ctx, conversationID, userText)
	}
	_, subs, err := RewriteAndSplit(ctx, userText, a.lightLLM)
	if err != nil || len(subs) == 0 {
		return a.answerSingle(ctx, conversationID, userText)
	}
	if sc := RuleScore(a.activeLeaves(), userText); sc.Confidence >= 0.4 && !a.subsKeepIntent(subs, sc.Intent) {
		return a.answerSingle(ctx, conversationID, userText)
	}
	if len(subs) == 1 {
		return a.answerOne(ctx, conversationID, subs[0])
	}
	limit := a.maxSplit
	if limit <= 0 {
		limit = 3
	}
	var b strings.Builder
	for i, sub := range subs {
		if i >= limit {
			fmt.Fprintf(&b, "[%d] %s → skipped（超出上限 %d）\n", i+1, sub, limit)
			continue
		}
		content, status := a.answerOneStatus(ctx, conversationID, sub)
		fmt.Fprintf(&b, "[%d] %s → %s\n%s\n", i+1, sub, status, content)
	}
	return strings.TrimRight(b.String(), "\n")
}
```

（保持原注释与逻辑不变，仅改签名与内部调用。）

3) `answerSingle` 改为（签名加 `conversationID string` 参数，内部两处调用改传参）：

```go
func (a *Agent) answerSingle(ctx context.Context, conversationID string, userText string) string {
	alertID := extractAlertID(userText, a.store)
	intent := RuleMatch(a.activeLeaves(), userText)
	switch intent {
	case ListAlerts:
		return a.listAlertsReply()
	case InspectAlert, AskRootCause, AskRecommendation:
		if alertID == "" {
			return "请指明告警ID，例如：alarm-123 什么原因？"
		}
		alert, err := a.store.GetAlert(alertID)
		if err != nil {
			return fmt.Sprintf("未找到告警 %s。", alertID)
		}
		if a.eng != nil {
			if _, runErr := a.store.GetRun("run-" + alertID); runErr != nil {
				_ = a.eng.Run(ctx, alert, nil)
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !ctxpkg.IsNilCompleter(a.llm) {
			return a.answerWithLLM(ctx, conversationID, alertID, intent, userText)
		}
		return a.answerFor(conversationID, alertID, intent)
	case AskStatus:
		return "告警状态可在左侧列表查看，点击告警查看排查时间线。"
	default:
		if !isSystemIntent(intent) {
			if content, ok := a.kbReply(ctx, conversationID, intent); ok {
				return content
			}
		}
		return a.placeholderReply(intent)
	}
}
```

4) `answerOne` 改为：

```go
func (a *Agent) answerOne(ctx context.Context, conversationID string, sub string) string {
	content, _ := a.answerOneStatus(ctx, conversationID, sub)
	return content
}
```

5) `answerOneStatus` 改为（签名加 `conversationID`，内部 `answerWithLLM` 与 `kbReply` 调用改传参）：

```go
func (a *Agent) answerOneStatus(ctx context.Context, conversationID string, sub string) (string, string) {
	intent, _ := ParseIntentWithLight(ctx, a.activeLeaves(), sub, a.lightLLM)
	switch intent {
	case ListAlerts:
		return a.listAlertsReply(), "success"
	case AskStatus:
		return "告警状态可在左侧列表查看，点击告警查看排查时间线。", "success"
	case InspectAlert, AskRootCause, AskRecommendation:
		alertID := extractAlertID(sub, a.store)
		if alertID == "" {
			return "请指明告警ID，例如：alarm-123 什么原因？", "no_alert"
		}
		alert, err := a.store.GetAlert(alertID)
		if err != nil {
			return fmt.Sprintf("未找到告警 %s。", alertID), "no_alert"
		}
		if a.eng != nil {
			if _, runErr := a.store.GetRun("run-" + alertID); runErr != nil {
				_ = a.eng.Run(ctx, alert, nil)
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !ctxpkg.IsNilCompleter(a.llm) {
			return a.answerWithLLM(ctx, conversationID, alertID, intent, sub), "success"
		}
		return a.answerFor(conversationID, alertID, intent), "success"
	default:
		if !isSystemIntent(intent) {
			if content, ok := a.kbReply(ctx, conversationID, intent); ok {
				return content, "success"
			}
			return a.placeholderReply(intent), statusFor(intent)
		}
		return a.placeholderReply(intent), statusFor(intent)
	}
}
```

6) `kbReply` 改为（签名加 `conversationID`，Query 带 `ConversationID`）：

```go
// kbReply 按意图 code 检索知识库并返回命中文档行；无命中返回 ok=false。
func (a *Agent) kbReply(ctx context.Context, conversationID string, code string) (string, bool) {
	if a.ctxStore == nil {
		return "", false
	}
	asm := ctxpkg.NewAssembler(a.ctxStore, a.topK)
	as, err := asm.Assemble(ctxpkg.Query{Stage: "chat", Intents: []string{code}, ConversationID: conversationID}, false, nil)
	if err != nil {
		return "", false
	}
	var lines []string
	for _, c := range as.Selected {
		if c.Source != ctxpkg.SourceKnowledge {
			continue
		}
		if !ctxpkg.MatchesIntent(c.Metadata["intents"], code) {
			continue
		}
		lines = append(lines, "- "+c.Content)
	}
	if len(lines) == 0 {
		return "", false
	}
	return "知识库相关文档：\n" + strings.Join(lines, "\n"), true
}
```

7) `buildAssembled` 改为（签名加 `conversationID`，Query 带 `ConversationID`）：

```go
func (a *Agent) buildAssembled(conversationID, device, metric string, intents []string) *ctxpkg.Assembled {
	if a.ctxStore == nil || device == "" || metric == "" {
		return nil
	}
	asm := ctxpkg.NewAssembler(a.ctxStore, a.topK)
	as, err := asm.Assemble(ctxpkg.Query{Device: device, Metric: metric, Stage: "chat", Intents: intents, ConversationID: conversationID}, false, nil)
	if err != nil {
		return nil
	}
	return as
}
```

8) `memorySuffix` 改为：

```go
func (a *Agent) memorySuffix(conversationID, device, metric string) string {
	as := a.buildAssembled(conversationID, device, metric, nil)
	if as == nil || !strings.Contains(as.User, "【设备历史经验】") {
		return ""
	}
	for _, line := range strings.Split(as.User, "\n") {
		if strings.HasPrefix(line, "上次(") {
			return "\n参考历史经验：" + line
		}
	}
	return ""
}
```

9) `answerFor` 改为（签名加 `conversationID`，内部 `memorySuffix` 调用改传参）：

```go
func (a *Agent) answerFor(conversationID, alertID string, intent Intent) string {
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
	alert, err := a.store.GetAlert(alertID)
	if err == nil {
		reply += a.memorySuffix(conversationID, alert.Device, alert.Metric)
	}
	return reply
}
```

`answerFor` 的调用处全部改传 `conversationID`：`answerSingle` 内 `a.answerFor(alertID, intent)` → `a.answerFor(conversationID, alertID, intent)`；`answerOneStatus` 内 `a.answerFor(alertID, intent)` → `a.answerFor(conversationID, alertID, intent)`；`answerWithLLM` 内两处 `a.answerFor(alertID, intent)` → `a.answerFor(conversationID, alertID, intent)`。

10) `answerWithLLM` 改为：

```go
func (a *Agent) answerWithLLM(ctx context.Context, conversationID, alertID string, intent Intent, userText string) string {
	alert, err := a.store.GetAlert(alertID)
	if err != nil {
		return a.answerFor(conversationID, alertID, intent)
	}
	var rca model.RCA
	var rec model.Recommendation
	if run, rerr := a.store.GetRun("run-" + alertID); rerr == nil {
		if st, ok, _ := a.store.GetLatestStage(run.ID, "rca"); ok {
			_ = llm.ParseJSON(st.Output, &rca)
		}
		if st, ok, _ := a.store.GetLatestStage(run.ID, "recommend"); ok {
			_ = llm.ParseJSON(st.Output, &rec)
		}
	}
	ctxStr := ""
	if as := a.buildAssembled(conversationID, alert.Device, alert.Metric, []string{intent}); as != nil {
		ctxStr = as.User
	}
	system := "你是算力中心硬件运维助手。结合给定上下文与已有排查结论，用中文简明回答用户的根因或处置问题。"
	user := fmt.Sprintf("告警 %s：设备=%s 指标=%s\n已有根因：%s\n已有处置：%s\n\n【相关上下文】\n%s\n\n用户问题：%s",
		alertID, alert.Device, alert.Metric, rca.RootCause, strings.Join(rec.Actions, "；"), ctxStr, userText)
	raw, cerr := a.llm.Complete(ctx, system, user)
	if cerr != nil || strings.TrimSpace(raw) == "" {
		return a.answerFor(conversationID, alertID, intent)
	}
	return raw
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agent/`
Expected: 全部 PASS（含新增会话用例）。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/agent/agent.go backend/internal/agent/agent_test.go
git commit -m "feat: 助手会话贯穿 conversation_id 并自动命名会话标题"
```

---

### Task 3: 后端 API 会话端点

**Files:**
- Modify: `backend/internal/api/server.go`（routes、handleListConversations、handleCreateConversation、handleListConversationMessages、handleChat）
- Test: `backend/internal/api/server_test.go`

**Interfaces:**
- Consumes: Task 1 的 `store.ListConversations/CreateConversation/GetConversation/ListMessages`；Task 2 的 `agent.Handle(ctx, conversationID, text)`。
- Produces: `GET /api/conversations`、`POST /api/conversations`、`GET /api/conversations/{id}/messages`；`POST /api/chat` 请求体 `{text, conversation_id}`。

- [ ] **Step 1: 写失败的测试**

在 `backend/internal/api/server_test.go` 更新 `TestChatEndpoint` 并追加用例：

```go
func TestChatEndpoint(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	conv, _ := s.CreateConversation("")
	h := hub.New()
	ag := agent.New(s, nil, nil)
	srv := NewServer(config.Config{}, s, ag, nil, h, nil, intent.NewService(s, intent.NewMemCache()), kb.NewService(s, nil))

	body := strings.NewReader(fmt.Sprintf(`{"text":"有哪些告警","conversation_id":%q}`, conv.ID))
	req := httptest.NewRequest("POST", "/api/chat", body)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("chat code = %d body=%s", rr.Code, rr.Body.String())
	}
	var m model.Message
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Role != model.RoleAssistant || m.ConversationID != conv.ID {
		t.Fatalf("msg = %+v", m)
	}
}

func TestChatMissingConversationID(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	h := hub.New()
	ag := agent.New(s, nil, nil)
	srv := NewServer(config.Config{}, s, ag, nil, h, nil, intent.NewService(s, intent.NewMemCache()), kb.NewService(s, nil))
	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(`{"text":"有哪些告警"}`))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("缺 conversation_id 应 400, got %d", rr.Code)
	}
}

func TestChatUnknownConversation(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	h := hub.New()
	ag := agent.New(s, nil, nil)
	srv := NewServer(config.Config{}, s, ag, nil, h, nil, intent.NewService(s, intent.NewMemCache()), kb.NewService(s, nil))
	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(`{"text":"有哪些告警","conversation_id":"nope"}`))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("会话不存在应 404, got %d", rr.Code)
	}
}

func TestConversationsEndpoints(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	h := hub.New()
	ag := agent.New(s, nil, nil)
	srv := NewServer(config.Config{}, s, ag, nil, h, nil, intent.NewService(s, intent.NewMemCache()), kb.NewService(s, nil))

	// 空列表非 null
	req := httptest.NewRequest("GET", "/api/conversations", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list code = %d", rr.Code)
	}
	if !strings.HasPrefix(strings.TrimSpace(rr.Body.String()), "[") {
		t.Fatalf("空会话列表应为 [] 数组, got %s", rr.Body.String())
	}

	// 创建
	req = httptest.NewRequest("POST", "/api/conversations", nil)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("create code = %d", rr.Code)
	}
	var conv model.Conversation
	if err := json.Unmarshal(rr.Body.Bytes(), &conv); err != nil || conv.ID == "" {
		t.Fatalf("create conv: %v %+v", err, conv)
	}

	// 会话消息（空 → []）
	req = httptest.NewRequest("GET", "/api/conversations/"+conv.ID+"/messages", nil)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("messages code = %d", rr.Code)
	}
	var msgs []model.Message
	if err := json.Unmarshal(rr.Body.Bytes(), &msgs); err != nil || msgs == nil {
		t.Fatalf("空会话消息应为 [] 非 null: %v", err)
	}

	// 不存在会话 404
	req = httptest.NewRequest("GET", "/api/conversations/nope/messages", nil)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("不存在会话消息应 404, got %d", rr.Code)
	}
}
```

`server_test.go` 顶部 import 需加 `fmt`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/api/`
Expected: 编译失败（handleListConversations 等未定义）——失败原因即待实现项。

- [ ] **Step 3: 实现 API 变更**

`backend/internal/api/server.go`：

1) `routes()` 中在 `GET /api/messages` 之后追加：

```go
	s.mux.HandleFunc("GET /api/conversations", s.handleListConversations)
	s.mux.HandleFunc("POST /api/conversations", s.handleCreateConversation)
	s.mux.HandleFunc("GET /api/conversations/{id}/messages", s.handleListConversationMessages)
```

2) `handleChat` 改为：

```go
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text           string `json:"text"`
		ConversationID string `json:"conversation_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}
	if strings.TrimSpace(body.ConversationID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "conversation_id required"})
		return
	}
	if _, err := s.st.GetConversation(body.ConversationID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "conversation not found"})
		return
	}
	msg, err := s.ag.Handle(r.Context(), body.ConversationID, body.Text)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.hub.Publish("message", msg)
	writeJSON(w, http.StatusOK, msg)
}
```

3) 新增三个 handler（放在 `handleListMessages` 之后）：

```go
func (s *Server) handleListConversations(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListConversations()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateConversation(w http.ResponseWriter, r *http.Request) {
	conv, err := s.st.CreateConversation("")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, conv)
}

func (s *Server) handleListConversationMessages(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.st.GetConversation(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "conversation not found"})
		return
	}
	msgs, err := s.st.ListMessages(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, msgs)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/api/`
Expected: 全部 PASS。

- [ ] **Step 5: 全量后端测试**

Run: `go test ./...`
Expected: 全部 PASS。

- [ ] **Step 6: 提交**

```bash
git add backend/internal/api/server.go backend/internal/api/server_test.go
git commit -m "feat: 新增会话 API 并在 /api/chat 校验 conversation_id"
```

---

### Task 4: 前端 types 与 api client

**Files:**
- Modify: `frontend/src/types.ts`
- Modify: `frontend/src/api/client.ts`
- Test: 无独立测试（纯类型/函数，随 Task 6 端到端覆盖）

**Interfaces:**
- Produces: `Conversation{id,title,created_at,updated_at}`；`ChatMessage.conversation_id?`；`fetchConversations() => Promise<Conversation[]>`；`createConversation() => Promise<Conversation>`；`fetchConversationMessages(id) => Promise<ChatMessage[]>`；`postChat(conversationId, text) => Promise<ChatMessage>`（body 含 `conversation_id`）。移除 `fetchMessages`。

- [ ] **Step 1: 修改 types.ts**

`frontend/src/types.ts` 在 `ChatMessage` 之前新增：

```ts
export interface Conversation {
  id: string
  title: string
  created_at: string
  updated_at: string
}
```

`ChatMessage` 增加字段：

```ts
export interface ChatMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  alert_id?: string
  conversation_id?: string
  created_at: string
}
```

- [ ] **Step 2: 修改 client.ts**

`frontend/src/api/client.ts`：

1) import 增加 `Conversation`。
2) 替换：

```ts
export const fetchMessages = () => req<ChatMessage[]>('/messages')
export const postChat = (text: string) =>
  req<ChatMessage>('/chat', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ text }),
  })
```

为：

```ts
export const fetchConversations = () => req<Conversation[]>('/conversations')
export const createConversation = () =>
  req<Conversation>('/conversations', { method: 'POST' })
export const fetchConversationMessages = (id: string) =>
  req<ChatMessage[]>(`/conversations/${id}/messages`)
export const postChat = (conversationId: string, text: string) =>
  req<ChatMessage>('/chat', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ text, conversation_id: conversationId }),
  })
```

- [ ] **Step 3: 验证编译**

Run: `npx tsc -b --noEmit`（或 `npm run build` 前先跑）→ Expected: 无类型错误（此时 App.tsx 仍引用 fetchMessages 会报错，若报错则按 Step 4 一并处理）。

- [ ] **Step 4: 清理 App.tsx（移除内嵌聊天列）**

`frontend/src/App.tsx` 中：

1) `import { fetchAlerts, fetchAlert, fetchMessages, postChat } from './api/client'` 改为 `import { fetchAlerts, fetchAlert } from './api/client'`。
2) `import type { Alert, AlertDetail, ChatMessage } from './types'` 改为 `import type { Alert, AlertDetail } from './types'`。
3) 删除 `const [messages, setMessages] = useState<ChatMessage[]>([])`。
4) 删除 messages 的 useEffect（含其内部 WS `message` 订阅；`alert`/`stage`/`run` 两个 useEffect 保留）与 `handleSend`。
5) 删除 `import ChatPanel from './components/ChatPanel'` 及 ops 布局中的聊天列 `<div style={{ width: 360 }}>...</div>`，两栏布局保留。

此步骤后 App.tsx 不再引用 `fetchMessages`/`postChat`/`ChatMessage`/`ChatPanel`，编译通过；悬浮助手由 Task 7 接入。

- [ ] **Step 5: 验证编译与测试**

Run: `npx tsc -b --noEmit && npx vitest run`
Expected: 编译通过；现有前端测试除 `ChatPanel.test.tsx` 外全绿（ChatPanel 测试 Task 5 更新）。

- [ ] **Step 6: 提交**

```bash
git add frontend/src/types.ts frontend/src/api/client.ts frontend/src/App.tsx
git commit -m "feat: 前端会话类型与 API client（fetchConversations/createConversation/postChat 带 conversation_id）"
```

---

### Task 5: 前端 ChatPanel 重构为纯展示（含滚动）

**Files:**
- Modify: `frontend/src/components/ChatPanel.tsx`
- Test: `frontend/src/components/ChatPanel.test.tsx`

**Interfaces:**
- Consumes: Task 4 的 `Conversation` 类型。
- Produces: `ChatPanel({ messages, onSend, onNewConversation, onSelectConversation, conversations, currentId, onClose })`。

- [ ] **Step 1: 更新失败测试**

`frontend/src/components/ChatPanel.test.tsx` 整体替换为：

```tsx
import { describe, expect, it, vi } from 'vitest'
import { type ComponentProps } from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import ChatPanel from './ChatPanel'
import type { ChatMessage, Conversation } from '../types'

const msgs: ChatMessage[] = [
  { id: 'm1', role: 'user', content: '有哪些告警', created_at: '' },
  { id: 'm2', role: 'assistant', content: '当前没有告警。', created_at: '' },
]

const convs: Conversation[] = [
  { id: 'c1', title: '会话一', created_at: '', updated_at: '' },
  { id: 'c2', title: '会话二', created_at: '', updated_at: '' },
]

function renderPanel(over: Partial<ComponentProps<typeof ChatPanel>> = {}) {
  const props = {
    messages: msgs,
    onSend: vi.fn().mockResolvedValue({} as ChatMessage),
    conversations: convs,
    currentId: 'c1',
    onSelectConversation: vi.fn(),
    onNewConversation: vi.fn(),
    onClose: vi.fn(),
    ...over,
  }
  const utils = render(<ChatPanel {...props} />)
  return { props, utils }
}

describe('ChatPanel', () => {
  it('渲染消息与标题', () => {
    renderPanel()
    expect(screen.getByText('智能助手')).toBeTruthy()
    expect(screen.getByText('有哪些告警')).toBeTruthy()
  })

  it('发送调用 onSend 并清空输入', async () => {
    const { props } = renderPanel()
    fireEvent.change(screen.getByPlaceholderText('输入消息...'), { target: { value: '你好' } })
    fireEvent.click(screen.getByText('发送'))
    await waitFor(() => expect(props.onSend).toHaveBeenCalledWith('你好'))
  })

  it('空消息显示引导语', () => {
    renderPanel({ messages: [] })
    expect(screen.getByText(/开始对话/)).toBeTruthy()
  })

  it('会话切换与新建按钮触发回调', () => {
    const { props } = renderPanel()
    fireEvent.mouseDown(screen.getByRole('combobox'))
    fireEvent.click(screen.getByText('会话二'))
    expect(props.onSelectConversation).toHaveBeenCalledWith('c2')
    fireEvent.click(screen.getByText('新对话'))
    expect(props.onNewConversation).toHaveBeenCalled()
  })

  it('对话历史容器可上下滚动', () => {
    renderPanel()
    const el = screen.getByLabelText('对话历史')
    expect(getComputedStyle(el).overflowY).toBe('auto')
  })

  it('收起按钮触发 onClose', () => {
    const { props } = renderPanel()
    fireEvent.click(screen.getByLabelText('收起助手'))
    expect(props.onClose).toHaveBeenCalled()
  })
})
```

（若 `getByRole('combobox')` 匹配不到，可改为 `screen.getByLabelText('会话切换')`；实现时按需二选一，保持测试意图一致。）

- [ ] **Step 2: 运行测试确认失败**

Run: `npx vitest run src/components/ChatPanel.test.tsx`
Expected: 失败（props 不匹配 / 无 combobox / 无「新对话」等）。

- [ ] **Step 3: 实现 ChatPanel**

`frontend/src/components/ChatPanel.tsx` 整体替换为：

```tsx
import { useEffect, useRef, useState } from 'react'
import { Button, Input, Select, Space, Typography } from 'antd'
import { CloseOutlined, PlusOutlined, SendOutlined } from '@ant-design/icons'
import type { ChatMessage, Conversation } from '../types'

export default function ChatPanel({
  messages,
  onSend,
  onNewConversation,
  onSelectConversation,
  conversations,
  currentId,
  onClose,
}: {
  messages: ChatMessage[]
  onSend: (text: string) => Promise<ChatMessage>
  onNewConversation: () => void
  onSelectConversation: (id: string) => void
  conversations: Conversation[]
  currentId: string
  onClose: () => void
}) {
  const [text, setText] = useState('')
  const [sending, setSending] = useState(false)
  const scrollRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [messages])

  const submit = async () => {
    if (!text.trim() || sending) return
    setSending(true)
    try {
      await onSend(text.trim())
      setText('')
    } finally {
      setSending(false)
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, paddingBottom: 8 }}>
        <Typography.Text strong style={{ whiteSpace: 'nowrap' }}>智能助手</Typography.Text>
        <Select
          aria-label="会话切换"
          value={currentId}
          onChange={(v) => onSelectConversation(v)}
          style={{ flex: 1, minWidth: 0 }}
          options={conversations.map((c) => ({ value: c.id, label: c.title || '未命名对话' }))}
          placeholder="选择会话"
          popupMatchSelectWidth={false}
        />
        <Button size="small" icon={<PlusOutlined />} onClick={onNewConversation}>
          新对话
        </Button>
        <Button size="small" type="text" icon={<CloseOutlined />} aria-label="收起助手" onClick={onClose} />
      </div>
      <div
        ref={scrollRef}
        aria-label="对话历史"
        style={{ flex: 1, overflowY: 'auto', minHeight: 0, borderTop: '1px solid rgba(255,255,255,0.08)' }}
      >
        {messages.length === 0 && (
          <Typography.Text type="secondary">开始对话，例如：查一下 alarm-100 什么原因</Typography.Text>
        )}
        {messages.map((m) => (
          <div
            key={m.id}
            style={{
              display: 'flex',
              justifyContent: m.role === 'user' ? 'flex-end' : 'flex-start',
              margin: '6px 0',
            }}
          >
            <div className={`chat-bubble${m.role === 'user' ? ' chat-bubble-user' : ''}`}>
              {m.content}
            </div>
          </div>
        ))}
      </div>
      <Space.Compact style={{ width: '100%', marginTop: 8 }}>
        <Input.TextArea
          value={text}
          onChange={(e) => setText(e.target.value)}
          onPressEnter={(e) => {
            if (!e.shiftKey) {
              e.preventDefault()
              submit()
            }
          }}
          placeholder="输入消息..."
          autoSize={{ minRows: 1, maxRows: 4 }}
          aria-label="输入消息"
        />
        <Button type="primary" icon={<SendOutlined />} onClick={submit} loading={sending}>
          发送
        </Button>
      </Space.Compact>
    </div>
  )
}
```

注意：`minHeight: 0` 保证 flex 子项在固定高度容器内可收缩，滚动才生效。

- [ ] **Step 4: 运行测试确认通过**

Run: `npx vitest run src/components/ChatPanel.test.tsx`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/components/ChatPanel.tsx frontend/src/components/ChatPanel.test.tsx
git commit -m "feat: 聊天面板改纯展示组件并支持会话切换与历史滚动"
```

---

### Task 6: 前端 ChatAssistant 悬浮容器

**Files:**
- Create: `frontend/src/components/ChatAssistant.tsx`
- Test: `frontend/src/components/ChatAssistant.test.tsx`

**Interfaces:**
- Consumes: Task 4 的 `fetchConversations/createConversation/fetchConversationMessages/postChat`、`subscribeWS`；Task 5 的 `ChatPanel`。
- Produces: `ChatAssistant`（自包含状态：conversations/currentId/messagesByConversation/open）。

- [ ] **Step 1: 写失败测试**

`frontend/src/components/ChatAssistant.test.tsx`：

```tsx
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import ChatAssistant from './ChatAssistant'
import type { ChatMessage, Conversation } from '../types'

const mocks = vi.hoisted(() => ({
  fetchConversations: vi.fn(),
  createConversation: vi.fn(),
  fetchConversationMessages: vi.fn(),
  postChat: vi.fn(),
  subscribeWS: vi.fn(() => () => {}),
}))

vi.mock('../api/client', () => ({
  fetchConversations: mocks.fetchConversations,
  createConversation: mocks.createConversation,
  fetchConversationMessages: mocks.fetchConversationMessages,
  postChat: mocks.postChat,
}))
vi.mock('../api/ws', () => ({ subscribeWS: mocks.subscribeWS }))

const c1: Conversation = { id: 'c1', title: '会话一', created_at: '', updated_at: '' }
const c2: Conversation = { id: 'c2', title: '会话二', created_at: '', updated_at: '' }

beforeEach(() => {
  vi.clearAllMocks()
  mocks.fetchConversations.mockResolvedValue([c1])
  mocks.createConversation.mockResolvedValue({ id: 'c3', title: '', created_at: '', updated_at: '' })
  mocks.fetchConversationMessages.mockResolvedValue([])
  mocks.postChat.mockResolvedValue({ id: 'm9', role: 'assistant', content: 'ok', conversation_id: 'c1', created_at: '' })
})

describe('ChatAssistant', () => {
  it('初始收起，点击按钮展开/再点收起', async () => {
    render(<ChatAssistant />)
    expect(screen.queryByLabelText('对话历史')).toBeNull()
    fireEvent.click(screen.getByLabelText('智能助手'))
    expect(await screen.findByLabelText('对话历史')).toBeTruthy()
    fireEvent.click(screen.getByLabelText('智能助手'))
    expect(screen.queryByLabelText('对话历史')).toBeNull()
  })

  it('加载最近会话并展示其消息', async () => {
    mocks.fetchConversationMessages.mockResolvedValue([
      { id: 'm1', role: 'user', content: '有哪些告警', conversation_id: 'c1', created_at: '' },
    ] as ChatMessage[])
    render(<ChatAssistant />)
    fireEvent.click(screen.getByLabelText('智能助手'))
    expect(await screen.findByText('有哪些告警')).toBeTruthy()
  })

  it('新对话创建并切换', async () => {
    render(<ChatAssistant />)
    fireEvent.click(screen.getByLabelText('智能助手'))
    await waitFor(() => expect(mocks.fetchConversationMessages).toHaveBeenCalledWith('c1'))
    fireEvent.click(screen.getByText('新对话'))
    await waitFor(() => expect(mocks.createConversation).toHaveBeenCalled())
    expect(mocks.fetchConversationMessages).toHaveBeenCalledWith('c3')
  })

  it('切换会话加载对应消息', async () => {
    mocks.fetchConversations.mockResolvedValue([c1, c2])
    render(<ChatAssistant />)
    fireEvent.click(screen.getByLabelText('智能助手'))
    await waitFor(() => expect(mocks.fetchConversationMessages).toHaveBeenCalledWith('c1'))
    fireEvent.mouseDown(screen.getByRole('combobox'))
    fireEvent.click(await screen.findByText('会话二'))
    await waitFor(() => expect(mocks.fetchConversationMessages).toHaveBeenCalledWith('c2'))
  })

  it('发送携带当前 conversation_id', async () => {
    render(<ChatAssistant />)
    fireEvent.click(screen.getByLabelText('智能助手'))
    await waitFor(() => expect(mocks.fetchConversationMessages).toHaveBeenCalledWith('c1'))
    fireEvent.change(screen.getByPlaceholderText('输入消息...'), { target: { value: '你好' } })
    fireEvent.click(screen.getByText('发送'))
    await waitFor(() => expect(mocks.postChat).toHaveBeenCalledWith('c1', '你好'))
  })
})
```

- [ ] **Step 2: 运行测试确认失败**

Run: `npx vitest run src/components/ChatAssistant.test.tsx`
Expected: 失败（组件不存在）。

- [ ] **Step 3: 实现 ChatAssistant**

`frontend/src/components/ChatAssistant.tsx`：

```tsx
import { useEffect, useState } from 'react'
import { FloatButton } from 'antd'
import { MessageOutlined } from '@ant-design/icons'
import ChatPanel from './ChatPanel'
import { createConversation, fetchConversationMessages, fetchConversations, postChat } from '../api/client'
import { subscribeWS } from '../api/ws'
import type { ChatMessage, Conversation } from '../types'

export default function ChatAssistant() {
  const [open, setOpen] = useState(false)
  const [conversations, setConversations] = useState<Conversation[]>([])
  const [currentId, setCurrentId] = useState<string | null>(null)
  const [messagesByConv, setMessagesByConv] = useState<Record<string, ChatMessage[]>>({})

  useEffect(() => {
    let cancelled = false
    const init = async () => {
      let list = await fetchConversations()
      if (list.length === 0) {
        const conv = await createConversation()
        list = [conv]
      }
      if (cancelled) return
      setConversations(list)
      const first = list[0]
      setCurrentId(first.id)
      const msgs = await fetchConversationMessages(first.id)
      if (!cancelled) setMessagesByConv((prev) => ({ ...prev, [first.id]: msgs }))
    }
    init().catch(() => {})
    const unsub = subscribeWS((m) => {
      if (m.type !== 'message') return
      const msg = m.data as ChatMessage
      if (!msg.conversation_id) return
      setMessagesByConv((prev) => {
        const cur = prev[msg.conversation_id!] ?? []
        if (cur.some((x) => x.id === msg.id)) return prev
        return { ...prev, [msg.conversation_id!]: [...cur, msg] }
      })
      fetchConversations().then(setConversations).catch(() => {})
    })
    return () => { cancelled = true; unsub() }
  }, [])

  const selectConversation = async (id: string) => {
    setCurrentId(id)
    if (!messagesByConv[id]) {
      const msgs = await fetchConversationMessages(id)
      setMessagesByConv((prev) => ({ ...prev, [id]: msgs }))
    }
  }

  const newConversation = async () => {
    const conv = await createConversation()
    setConversations((prev) => [conv, ...prev])
    setCurrentId(conv.id)
    setMessagesByConv((prev) => ({ ...prev, [conv.id]: [] }))
  }

  const handleSend = async (text: string) => {
    const reply = await postChat(currentId ?? '', text)
    if (currentId) {
      const msgs = await fetchConversationMessages(currentId)
      setMessagesByConv((prev) => ({ ...prev, [currentId]: msgs }))
    }
    return reply
  }

  return (
    <>
      <FloatButton
        icon={<MessageOutlined />}
        type="primary"
        style={{ right: 24, bottom: 24 }}
        onClick={() => setOpen((o) => !o)}
        aria-label="智能助手"
      />
      {open && currentId && (
        <div
          role="dialog"
          aria-label="智能助手"
          style={{
            position: 'fixed',
            right: 24,
            bottom: 88,
            width: 380,
            height: 560,
            display: 'flex',
            flexDirection: 'column',
            background: '#1c2333',
            borderRadius: 8,
            boxShadow: '0 4px 16px rgba(0,0,0,0.4)',
            overflow: 'hidden',
            padding: 12,
          }}
        >
          <ChatPanel
            messages={messagesByConv[currentId] ?? []}
            onSend={handleSend}
            conversations={conversations}
            currentId={currentId}
            onSelectConversation={selectConversation}
            onNewConversation={newConversation}
            onClose={() => setOpen(false)}
          />
        </div>
      )}
    </>
  )
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `npx vitest run src/components/ChatAssistant.test.tsx`
Expected: 全部 PASS。若 antd `Select` 弹层在 jsdom 下不可交互，调整测试为直接调用回调（`screen.getByRole('combobox')` 不可用则用 `screen.getByLabelText('会话切换')`），保持断言意图。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/components/ChatAssistant.tsx frontend/src/components/ChatAssistant.test.tsx
git commit -m "feat: 智能助手悬浮面板（右下角按钮弹出/收起、会话切换与新建）"
```

---

### Task 7: App.tsx 整合悬浮助手

**Files:**
- Modify: `frontend/src/App.tsx`（移除聊天列、渲染 ChatAssistant）
- Test: 无独立测试文件；跑全量验证。

**Interfaces:**
- Consumes: Task 6 的 `ChatAssistant`。

- [ ] **Step 1: 修改 App.tsx**

App.tsx 的聊天列、`messages` state、messages useEffect 与 `handleSend` 已在 Task 4 Step 4 移除。此处仅两处改动：

1) import 新增 `ChatAssistant`（追加到 `StatsCards` import 之后）：

```tsx
import ChatAssistant from './components/ChatAssistant'
```

2) 在 return 的根 `<Layout>` 末尾（`</Layout>` 之前）追加：

```tsx
      <ChatAssistant />
```

- [ ] **Step 2: 运行前端全量测试**

Run: `npx vitest run`
Expected: 全部 PASS。

- [ ] **Step 3: 构建**

Run: `npm run build`
Expected: `tsc -b && vite build` 成功。

- [ ] **Step 4: 后端回归**

Run: `go test ./...`
Expected: 全部 PASS（确认前端改动未影响后端）。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/App.tsx
git commit -m "feat: 运维面板移除内嵌聊天列，全局渲染悬浮智能助手"
```

---

## 验证清单（全部任务完成后）

- `go test ./...` 全绿
- `npx vitest run` 全绿
- `npm run build` 成功
- 浏览器人工走查：右下角按钮弹出/收起、历史可滚动、可新建会话并切换旧会话、WS 推送只进对应会话。

## 设计决策与取舍

1. **签名改为 `Handle(ctx, conversationID, text)`**：会话隔离是核心需求，显式入参比全局状态清晰；旧测试统一传 `""` 保持全局行为。
2. **`RecentMessages("", n)` 回退全局**：流水线 `Query` 无会话字段，空 id 走原逻辑，避免影响流水线上下文组装。
3. **迁移回填「历史对话」**：存量消息不丢失，迁移在 `migrate()` 内用 `ensureColumn` + 回填两步完成。
4. **ChatAssistant 自包含状态**：`App.tsx` 只渲染一次，运维面板布局简化为两栏，不把会话状态提升到 App。