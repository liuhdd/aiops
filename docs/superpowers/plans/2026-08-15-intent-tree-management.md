# 意图树管理 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增意图树管理：意图以树形组织（分组 + 意图叶子），支持增删改查/停用启用/软删除/递归组树；叶子可按 kind 绑定 KB/MCP（预留字段）、SYSTEM 不绑；改 kind 联动清绑定；批量启停/删除带「子树完整性」保护；写操作全留痕；「DB 唯一事实源 + 进程内读缓存 + 写清缓存」；运行时识别改由树驱动（规则 + LLM 闭集均从活跃叶子派生）。

**Architecture:** 新增 `model.IntentNode/IntentKind/LeafSpec/IntentTreeNode/IntentAudit`；`store` 增加 `intent_nodes`/`intent_audit` 两张表 + seed + `IntentStore` 接口与 `WithIntentTx` 事务；新增 `internal/intent` 包（`Service` + `Cache` 抽象与进程内实现）；`agent` 的 `Intent` 由枚举改 string、识别函数改为从 `[]model.LeafSpec` 驱动并保留 `SeedLeaves` 兜底；`api` 增加 `/api/intents` 路由；`main.go` 装配服务并注入 `ActiveLeaves` 到 agent；前端新增意图管理页。

**Tech Stack:** Go 1.24 + SQLite（`modernc.org/sqlite`，driver 名 `"sqlite"`）+ React 18/Vite/TS。不引入真实 Redis（进程内缓存抽象）。注释与 commit message 用中文。

## Global Constraints

- Go 模块 `aiops`，包路径 `aiops/internal/...`。SQLite 单写：`store.New` 已设 `SetMaxOpenConns(1)`，测试用 `filepath.Join(t.TempDir(), "*.db")`，用完 `s.Close()`。
- `store.New` 的 `migrate()` 会幂等建表 + seed（表空才写入）；种子节点 ID 固定：分组 `grp-builtin`、叶子 `leaf-<Code>`。
- 无 `LLM_API_KEY` 时 LLM 类配置跳过，规则路径仍可跑（CI 护栏 `TestRuleRegression*` 保持通过）。
- 写操作全部走 `store.WithIntentTx`（单事务：校验 → 写 → 留痕），事务提交后 `cache.Invalidate`。
- 提交信息用 conventional prefixes（`feat:`/`test:`/`docs:`/`chore:`）。
- 测试命令：后端 `go test ./...`；前端 `npx vitest run`；构建 `go build ./...`、`npm run build`。

---

### Task 1: model 新增意图模型类型

**Files:**
- Create: `backend/internal/model/intent.go`
- Test: `backend/internal/model/intent_test.go`

**Interfaces:**
- Produces:
  - `type IntentKind string`；常量 `KindGroup/KindSystem/KindKB/KindMCP`
  - `var SystemIntentCodes = []string{"ListAlerts","InspectAlert","AskRootCause","AskRecommendation","AskStatus","Unknown"}`
  - `func IsSystemCode(code string) bool`
  - `type IntentNode struct{ ID,ParentID,Name string; Kind IntentKind; Code,BindTarget string; RuleKeywords []string; Description string; Enabled,Deleted bool; SortOrder int; CreatedAt,UpdatedAt time.Time }`（JSON tag 用 snake_case）
  - `func (n IntentNode) IsLeaf() bool`（`Kind != KindGroup`）
  - `type IntentTreeNode struct{ IntentNode; Children []IntentTreeNode }`
  - `type LeafSpec struct{ Code,Name,Description string; RuleKeywords []string }`
  - `type IntentAudit struct{ ID,NodeID,Action,Operator string; BeforeJSON,AfterJSON string; CreatedAt time.Time }`
  - 留痕动作常量 `AuditCreate/AuditUpdate/AuditDelete/AuditEnable/AuditDisable`
- Consumes: 无

- [ ] **Step 1: 写失败测试（新建 `intent_test.go`）**

```go
package model

import (
	"encoding/json"
	"testing"
)

func TestIntentKindAndIsLeaf(t *testing.T) {
	g := IntentNode{ID: "g", Kind: KindGroup}
	if g.IsLeaf() {
		t.Fatal("GROUP 应为非叶子")
	}
	l := IntentNode{ID: "l", Kind: KindKB}
	if !l.IsLeaf() {
		t.Fatal("KB 应为叶子")
	}
}

func TestIsSystemCode(t *testing.T) {
	if !IsSystemCode("ListAlerts") || !IsSystemCode("Unknown") {
		t.Fatal("内置 code 应命中")
	}
	if IsSystemCode("MyIntent") {
		t.Fatal("非内置 code 不应命中")
	}
}

func TestIntentNodeJSON(t *testing.T) {
	n := IntentNode{ID: "n1", ParentID: "g", Name: "告警列表", Kind: KindSystem,
		Code: "ListAlerts", RuleKeywords: []string{"列表", "告警"}, Enabled: true}
	b, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out IntentNode
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.RuleKeywords == nil || len(out.RuleKeywords) != 2 || out.Kind != KindSystem {
		t.Fatalf("roundtrip = %+v", out)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/model/ -run 'TestIntentKindAndIsLeaf|TestIsSystemCode|TestIntentNodeJSON' -v`
Expected: FAIL——`undefined: IntentKind` 等。

- [ ] **Step 3: 写实现（新建 `intent.go`）**

```go
package model

import "time"

type IntentKind string

const (
	KindGroup  IntentKind = "GROUP"
	KindSystem IntentKind = "SYSTEM"
	KindKB     IntentKind = "KB"
	KindMCP    IntentKind = "MCP"
)

// SystemIntentCodes 内置 SYSTEM 意图 code 全集（seed 与校验共用）。
var SystemIntentCodes = []string{
	"ListAlerts", "InspectAlert", "AskRootCause", "AskRecommendation", "AskStatus", "Unknown",
}

func IsSystemCode(code string) bool {
	for _, c := range SystemIntentCodes {
		if c == code {
			return true
		}
	}
	return false
}

type IntentNode struct {
	ID           string     `json:"id"`
	ParentID     string     `json:"parent_id"`
	Name         string     `json:"name"`
	Kind         IntentKind `json:"kind"`
	Code         string     `json:"code"`
	BindTarget   string     `json:"bind_target"`
	RuleKeywords []string   `json:"rule_keywords"`
	Description  string     `json:"description"`
	Enabled      bool       `json:"enabled"`
	Deleted      bool       `json:"deleted"`
	SortOrder    int        `json:"sort_order"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// IsLeaf kind != GROUP。
func (n IntentNode) IsLeaf() bool { return n.Kind != KindGroup }

// IntentTreeNode 递归组树节点（管理页渲染）。
type IntentTreeNode struct {
	IntentNode
	Children []IntentTreeNode `json:"children"`
}

// LeafSpec 识别视图（服务层派生，agent 消费）。
type LeafSpec struct {
	Code         string   `json:"code"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	RuleKeywords []string `json:"rule_keywords"`
}

// 留痕动作常量
const (
	AuditCreate  = "create"
	AuditUpdate  = "update"
	AuditDelete  = "delete"
	AuditEnable  = "enable"
	AuditDisable = "disable"
)

type IntentAudit struct {
	ID         string    `json:"id"`
	NodeID     string    `json:"node_id"`
	Action     string    `json:"action"`
	Operator   string    `json:"operator"`
	BeforeJSON string    `json:"before_json,omitempty"`
	AfterJSON  string    `json:"after_json,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/model/ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/model/intent.go backend/internal/model/intent_test.go
git commit -m "feat: model新增意图树模型类型"
```

---

### Task 2: store 意图表 + seed + IntentStore 接口

**Files:**
- Modify: `backend/internal/store/store.go`（`migrate()` 追加建表与 `seedIntents()` 调用）
- Create: `backend/internal/store/intent.go`
- Test: `backend/internal/store/intent_test.go`

**Interfaces:**
- Consumes: `model.IntentNode`/`model.IntentAudit`/`model.KindGroup`/`model.KindSystem`（Task 1）
- Produces:
  - `type IntentStore interface{ ListIntentNodes() ([]model.IntentNode, error); GetIntentNode(id string) (model.IntentNode, error); InsertIntentNode(n model.IntentNode) error; UpdateIntentNode(n model.IntentNode) error; SoftDeleteIntentNodes(ids []string) error; SetIntentNodesEnabled(ids []string, enabled bool) error; InsertIntentAudit(a model.IntentAudit) error }`
  - `func (s *Store) ListIntentNodes() ([]model.IntentNode, error)`
  - `func (s *Store) ListIntentAudit() ([]model.IntentAudit, error)`
  - `func (s *Store) WithIntentTx(fn func(tx IntentStore) error) error`

- [ ] **Step 1: 写失败测试（新建 `intent_test.go`）**

```go
package store

import (
	"errors"
	"testing"

	"aiops/internal/model"
)

func TestSeedIntents(t *testing.T) {
	s := newTestStore(t)
	nodes, err := s.ListIntentNodes()
	if err != nil {
		t.Fatalf("ListIntentNodes: %v", err)
	}
	if len(nodes) != 7 {
		t.Fatalf("seed 后应有 7 个节点（1 分组 + 6 叶子），got %d", len(nodes))
	}
	byID := map[string]model.IntentNode{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	if byID["grp-builtin"].Kind != model.KindGroup {
		t.Fatalf("grp-builtin 应为分组")
	}
	if byID["leaf-ListAlerts"].Code != "ListAlerts" || byID["leaf-ListAlerts"].Kind != model.KindSystem {
		t.Fatalf("leaf-ListAlerts = %+v", byID["leaf-ListAlerts"])
	}
	if len(byID["leaf-ListAlerts"].RuleKeywords) == 0 {
		t.Fatal("ListAlerts 种子应有关键词")
	}
}

func TestIntentNodeRoundTrip(t *testing.T) {
	s := newTestStore(t)
	n := model.IntentNode{ID: "n1", ParentID: "grp-builtin", Name: "自定义", Kind: model.KindKB,
		Code: "MyKB", BindTarget: "kb-1", RuleKeywords: []string{"手册"}, Enabled: true, SortOrder: 9}
	if err := s.WithIntentTx(func(tx IntentStore) error {
		return tx.InsertIntentNode(n)
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := s.ListIntentNodes()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, x := range got {
		if x.ID == "n1" && x.BindTarget == "kb-1" && len(x.RuleKeywords) == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("roundtrip 失败: %+v", got)
	}
}

func TestIntentSoftDeleteAndEnable(t *testing.T) {
	s := newTestStore(t)
	if err := s.WithIntentTx(func(tx IntentStore) error {
		if err := tx.SoftDeleteIntentNodes([]string{"leaf-Unknown"}); err != nil {
			return err
		}
		return tx.SetIntentNodesEnabled([]string{"leaf-ListAlerts"}, false)
	}); err != nil {
		t.Fatalf("tx: %v", err)
	}
	got, _ := s.ListIntentNodes()
	byID := map[string]model.IntentNode{}
	for _, n := range got {
		byID[n.ID] = n
	}
	if !byID["leaf-Unknown"].Deleted {
		t.Fatal("leaf-Unknown 应已软删除")
	}
	if byID["leaf-ListAlerts"].Enabled {
		t.Fatal("leaf-ListAlerts 应已停用")
	}
}

func TestIntentAuditAndRollback(t *testing.T) {
	s := newTestStore(t)
	audit := model.IntentAudit{ID: "a1", NodeID: "n1", Action: model.AuditCreate, Operator: "tester"}
	if err := s.WithIntentTx(func(tx IntentStore) error {
		return tx.InsertIntentAudit(audit)
	}); err != nil {
		t.Fatalf("audit: %v", err)
	}
	list, err := s.ListIntentAudit()
	if err != nil || len(list) != 1 || list[0].Operator != "tester" {
		t.Fatalf("ListIntentAudit = %v %+v", err, list)
	}
	// 事务回滚：fn 返回错误时不落库
	_ = s.WithIntentTx(func(tx IntentStore) error {
		_ = tx.InsertIntentAudit(model.IntentAudit{ID: "a2", NodeID: "n1", Action: model.AuditUpdate, Operator: "x"})
		return errors.New("boom")
	})
	list2, _ := s.ListIntentAudit()
	if len(list2) != 1 {
		t.Fatalf("回滚后 audit 应仍为 1 条, got %d", len(list2))
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/store/ -run 'TestSeedIntents|TestIntentNodeRoundTrip|TestIntentSoftDeleteAndEnable|TestIntentAuditAndRollback' -v`
Expected: FAIL——`undefined: IntentStore` / `ListIntentNodes` 等。

- [ ] **Step 3: 写实现（修改 `store.go` + 新建 `intent.go`）**

`store.go` 的 `migrate()` 在 `device_context` 建表后追加：

```go
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS intent_nodes (
		id TEXT PRIMARY KEY,
		parent_id TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL,
		kind TEXT NOT NULL,
		code TEXT NOT NULL DEFAULT '',
		bind_target TEXT NOT NULL DEFAULT '',
		rule_keywords TEXT NOT NULL DEFAULT '[]',
		description TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 1,
		deleted INTEGER NOT NULL DEFAULT 0,
		sort_order INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME,
		updated_at DATETIME)`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_intent_parent ON intent_nodes(parent_id, sort_order)`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS intent_audit (
		id TEXT PRIMARY KEY,
		node_id TEXT NOT NULL,
		action TEXT NOT NULL,
		operator TEXT NOT NULL,
		before_json TEXT,
		after_json TEXT,
		created_at DATETIME)`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_intent_audit_node ON intent_audit(node_id, created_at)`); err != nil {
		return err
	}
	if err := s.seedIntents(); err != nil {
		return err
	}
```

新建 `intent.go`：

```go
package store

import (
	"database/sql"
	"encoding/json"
	"time"

	"aiops/internal/model"
)

// dbExec 抽象 *sql.DB 与 *sql.Tx 的公共方法，供 intentRepo 复用。
type dbExec interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// IntentStore 意图节点读写接口（*Store 与事务内 repo 均实现）。
type IntentStore interface {
	ListIntentNodes() ([]model.IntentNode, error)
	GetIntentNode(id string) (model.IntentNode, error)
	InsertIntentNode(n model.IntentNode) error
	UpdateIntentNode(n model.IntentNode) error
	SoftDeleteIntentNodes(ids []string) error
	SetIntentNodesEnabled(ids []string, enabled bool) error
	InsertIntentAudit(a model.IntentAudit) error
}

type intentRepo struct{ db dbExec }

const intentCols = `id, parent_id, name, kind, code, bind_target, rule_keywords, description, enabled, deleted, sort_order, created_at, updated_at`

func scanIntentNode(scan func(dest ...any) error) (model.IntentNode, error) {
	var n model.IntentNode
	var kw string
	var enabled, deleted int
	if err := scan(&n.ID, &n.ParentID, &n.Name, &n.Kind, &n.Code, &n.BindTarget,
		&kw, &n.Description, &enabled, &deleted, &n.SortOrder, &n.CreatedAt, &n.UpdatedAt); err != nil {
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
	return n, nil
}

func (r *intentRepo) ListIntentNodes() ([]model.IntentNode, error) {
	rows, err := r.db.Query(`SELECT ` + intentCols + ` FROM intent_nodes ORDER BY sort_order ASC, created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.IntentNode
	for rows.Next() {
		n, err := scanIntentNode(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (r *intentRepo) GetIntentNode(id string) (model.IntentNode, error) {
	n, err := scanIntentNode(r.db.QueryRow(`SELECT `+intentCols+` FROM intent_nodes WHERE id = ?`, id).Scan)
	if err == sql.ErrNoRows {
		return model.IntentNode{}, sql.ErrNoRows
	}
	return n, err
}

func (r *intentRepo) InsertIntentNode(n model.IntentNode) error {
	kw, _ := json.Marshal(n.RuleKeywords)
	enabled, deleted := 0, 0
	if n.Enabled {
		enabled = 1
	}
	if n.Deleted {
		deleted = 1
	}
	_, err := r.db.Exec(`INSERT INTO intent_nodes (id, parent_id, name, kind, code, bind_target, rule_keywords, description, enabled, deleted, sort_order, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.ID, n.ParentID, n.Name, n.Kind, n.Code, n.BindTarget, string(kw), n.Description, enabled, deleted, n.SortOrder, n.CreatedAt, n.UpdatedAt)
	return err
}

func (r *intentRepo) UpdateIntentNode(n model.IntentNode) error {
	kw, _ := json.Marshal(n.RuleKeywords)
	enabled, deleted := 0, 0
	if n.Enabled {
		enabled = 1
	}
	if n.Deleted {
		deleted = 1
	}
	_, err := r.db.Exec(`UPDATE intent_nodes SET parent_id=?, name=?, kind=?, code=?, bind_target=?, rule_keywords=?, description=?, enabled=?, deleted=?, sort_order=?, updated_at=? WHERE id=?`,
		n.ParentID, n.Name, n.Kind, n.Code, n.BindTarget, string(kw), n.Description, enabled, deleted, n.SortOrder, n.UpdatedAt, n.ID)
	return err
}

func (r *intentRepo) SoftDeleteIntentNodes(ids []string) error {
	for _, id := range ids {
		if _, err := r.db.Exec(`UPDATE intent_nodes SET deleted=1, updated_at=? WHERE id=?`, time.Now(), id); err != nil {
			return err
		}
	}
	return nil
}

func (r *intentRepo) SetIntentNodesEnabled(ids []string, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	for _, id := range ids {
		if _, err := r.db.Exec(`UPDATE intent_nodes SET enabled=?, updated_at=? WHERE id=?`, v, time.Now(), id); err != nil {
			return err
		}
	}
	return nil
}

func (r *intentRepo) InsertIntentAudit(a model.IntentAudit) error {
	_, err := r.db.Exec(`INSERT INTO intent_audit (id, node_id, action, operator, before_json, after_json, created_at) VALUES (?,?,?,?,?,?,?)`,
		a.ID, a.NodeID, a.Action, a.Operator, a.BeforeJSON, a.AfterJSON, a.CreatedAt)
	return err
}

// ---- *Store 委托与事务 ----

func (s *Store) ListIntentNodes() ([]model.IntentNode, error) {
	return (&intentRepo{db: s.db}).ListIntentNodes()
}

func (s *Store) ListIntentAudit() ([]model.IntentAudit, error) {
	rows, err := s.db.Query(`SELECT id, node_id, action, operator, before_json, after_json, created_at FROM intent_audit ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.IntentAudit
	for rows.Next() {
		var a model.IntentAudit
		if err := rows.Scan(&a.ID, &a.NodeID, &a.Action, &a.Operator, &a.BeforeJSON, &a.AfterJSON, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// WithIntentTx 在单事务内执行意图写操作；fn 返回错误则回滚。
func (s *Store) WithIntentTx(fn func(tx IntentStore) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(&intentRepo{db: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// seedIntents 表空时写入内置种子（1 分组 + 6 SYSTEM 叶子）。
func (s *Store) seedIntents() error {
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM intent_nodes`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	now := time.Now()
	if _, err := s.db.Exec(`INSERT INTO intent_nodes (id, parent_id, name, kind, code, bind_target, rule_keywords, description, enabled, deleted, sort_order, created_at, updated_at)
		VALUES ('grp-builtin','','内置意图','GROUP','','','[]','',1,0,0,?,?)`, now, now); err != nil {
		return err
	}
	seeds := []struct {
		code, desc string
		kw         []string
		order      int
	}{
		{"AskRootCause", "询问根因", []string{"什么原因", "为什么", "根因"}, 0},
		{"AskRecommendation", "询问处置建议", []string{"怎么处理", "怎么办", "建议", "处置"}, 1},
		{"AskStatus", "询问状态/进度", []string{"状态", "进度"}, 2},
		{"InspectAlert", "排查某个告警", []string{}, 3},
		{"ListAlerts", "查看告警列表", []string{"列表", "有哪些告警", "告警"}, 4},
		{"Unknown", "与告警运维无关", []string{}, 5},
	}
	for _, sd := range seeds {
		kw, _ := json.Marshal(sd.kw)
		if _, err := s.db.Exec(`INSERT INTO intent_nodes (id, parent_id, name, kind, code, bind_target, rule_keywords, description, enabled, deleted, sort_order, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,1,0,?,?,?)`,
			"leaf-"+sd.code, "grp-builtin", sd.code, string(model.KindSystem), sd.code, "", string(kw), sd.desc, sd.order, now, now); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/store/ -v`
Expected: 全部 PASS，含既有 `TestAlertRoundTrip`/`TestMigrateCreatesContextTables`。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/store/store.go backend/internal/store/intent.go backend/internal/store/intent_test.go
git commit -m "feat: store新增意图树表/seed/IntentStore事务接口"
```

---

### Task 3: intent 缓存抽象与进程内实现

**Files:**
- Create: `backend/internal/intent/cache.go`
- Test: `backend/internal/intent/cache_test.go`

**Interfaces:**
- Consumes: `model.IntentNode`（Task 1）
- Produces:
  - `type Cache interface{ Get(ctx context.Context) ([]model.IntentNode, bool); Put(ctx context.Context, nodes []model.IntentNode); Invalidate(ctx context.Context) }`
  - `func NewMemCache() *MemCache`

- [ ] **Step 1: 写失败测试（新建 `cache_test.go`）**

```go
package intent

import (
	"context"
	"testing"

	"aiops/internal/model"
)

func TestMemCache(t *testing.T) {
	c := NewMemCache()
	ctx := context.Background()
	if _, ok := c.Get(ctx); ok {
		t.Fatal("初始应为未命中")
	}
	nodes := []model.IntentNode{{ID: "n1", Name: "x"}}
	c.Put(ctx, nodes)
	got, ok := c.Get(ctx)
	if !ok || len(got) != 1 || got[0].ID != "n1" {
		t.Fatalf("命中失败: %v %+v", ok, got)
	}
	c.Invalidate(ctx)
	if _, ok := c.Get(ctx); ok {
		t.Fatal("失效后应为未命中")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/intent/ -run TestMemCache -v`
Expected: FAIL——`undefined: NewMemCache`（包目录尚不存在时 `no test files`）。

- [ ] **Step 3: 写实现（新建 `cache.go`）**

```go
package intent

import (
	"context"
	"sync"

	"aiops/internal/model"
)

// Cache 意图树读缓存抽象：DB 是唯一事实源，任何写操作后 Invalidate，下次读回源重建。
type Cache interface {
	Get(ctx context.Context) ([]model.IntentNode, bool)
	Put(ctx context.Context, nodes []model.IntentNode)
	Invalidate(ctx context.Context)
}

// MemCache 进程内实现（sync.RWMutex 保护）；后续可替换为 Redis 实现。
type MemCache struct {
	mu    sync.RWMutex
	nodes []model.IntentNode
	valid bool
}

func NewMemCache() *MemCache { return &MemCache{} }

func (c *MemCache) Get(_ context.Context) ([]model.IntentNode, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.valid {
		return nil, false
	}
	return c.nodes, true
}

func (c *MemCache) Put(_ context.Context, nodes []model.IntentNode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nodes = nodes
	c.valid = true
}

func (c *MemCache) Invalidate(_ context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nodes = nil
	c.valid = false
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/intent/ -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/intent/cache.go backend/internal/intent/cache_test.go
git commit -m "feat: intent新增缓存抽象与进程内实现"
```

---

### Task 4: intent 服务层（CRUD + 完整性校验 + 留痕 + 缓存）

**Files:**
- Create: `backend/internal/intent/service.go`
- Test: `backend/internal/intent/service_test.go`

**Interfaces:**
- Consumes: `store.IntentStore`/`store.WithIntentTx`（Task 2）、`Cache`/`NewMemCache`（Task 3）、`model.IntentNode`/`model.LeafSpec`/`model.IntentTreeNode`/`model.Audit*`（Task 1）
- Produces:
  - `var ErrNotFound, ErrDuplicateCode, ErrSubtreeIncomplete = ...`（哨兵错误）
  - `type CreateReq struct{ ParentID,Name string; Kind model.IntentKind; Code,BindTarget string; RuleKeywords []string; Description string; SortOrder int }`
  - `type UpdateReq struct{ ParentID,Name string; Kind model.IntentKind; Code,BindTarget string; RuleKeywords []string; Description string; SortOrder int }`（全量覆盖）
  - `func NewService(st *store.Store, cache Cache) *Service`
  - `func (s *Service) Create(ctx context.Context, operator string, req CreateReq) (*model.IntentNode, error)`
  - `func (s *Service) Update(ctx context.Context, operator string, id string, req UpdateReq) (*model.IntentNode, error)`
  - `func (s *Service) Delete(ctx context.Context, operator string, ids []string) error`
  - `func (s *Service) SetEnabled(ctx context.Context, operator string, ids []string, enabled bool) error`
  - `func (s *Service) Tree(ctx context.Context) ([]model.IntentTreeNode, error)`
  - `func (s *Service) ActiveLeaves(ctx context.Context) ([]model.LeafSpec, error)`
  - `func BuildForest(nodes []model.IntentNode) []model.IntentTreeNode`
  - `func ActiveLeavesFrom(nodes []model.IntentNode) []model.LeafSpec`

- [ ] **Step 1: 写失败测试（新建 `service_test.go`）**

> 注意：`Create` 生成的节点 ID 为随机 `node-*`（`model.NewID`），测试用返回值 `n.ID` 承接，不硬编码 ID。

```go
package intent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"aiops/internal/model"
	"aiops/internal/store"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return NewService(st, NewMemCache())
}

func TestTreeAndSeed(t *testing.T) {
	svc := newTestService(t)
	tree, err := svc.Tree(context.Background())
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if len(tree) != 1 || tree[0].Name != "内置意图" || len(tree[0].Children) != 6 {
		t.Fatalf("tree = %+v", tree)
	}
}

func TestCreateAndDuplicateCode(t *testing.T) {
	svc := newTestService(t)
	n, err := svc.Create(context.Background(), "t", CreateReq{ParentID: "grp-builtin", Name: "查手册", Kind: model.KindKB, Code: "kb1", BindTarget: "kb-1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if n.ID == "" || n.Kind != model.KindKB || n.BindTarget != "kb-1" {
		t.Fatalf("create result = %+v", n)
	}
	if _, err := svc.Create(context.Background(), "t", CreateReq{ParentID: "grp-builtin", Name: "重复", Kind: model.KindKB, Code: "kb1"}); err != ErrDuplicateCode {
		t.Fatalf("重复 code 应 ErrDuplicateCode, got %v", err)
	}
	if _, err := svc.Create(context.Background(), "t", CreateReq{ParentID: "grp-builtin", Name: "非法", Kind: model.KindSystem, Code: "Nope"}); err == nil {
		t.Fatal("SYSTEM 非内置 code 应报错")
	}
	if _, err := svc.Create(context.Background(), "t", CreateReq{ParentID: "leaf-ListAlerts", Name: "x", Kind: model.KindKB, Code: "k2"}); err == nil {
		t.Fatal("叶子下挂子节点应报错")
	}
}

func TestUpdateKindChangeClearsBind(t *testing.T) {
	svc := newTestService(t)
	n, _ := svc.Create(context.Background(), "t", CreateReq{ParentID: "grp-builtin", Name: "kb意图", Kind: model.KindKB, Code: "kb9", BindTarget: "kb-99"})
	// KB -> MCP：清绑定，保留 code
	got, err := svc.Update(context.Background(), "t", n.ID, UpdateReq{ParentID: n.ParentID, Name: n.Name, Kind: model.KindMCP, Code: n.Code, BindTarget: "mcp-1", RuleKeywords: []string{}, Description: ""})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.BindTarget != "" {
		t.Fatalf("改 kind 应清绑定, got %q", got.BindTarget)
	}
	if got.Kind != model.KindMCP {
		t.Fatalf("kind 应改为 MCP, got %v", got.Kind)
	}
}

func TestDeleteSubtreeIntegrity(t *testing.T) {
	svc := newTestService(t)
	// 只删分组不删后代 → 报错并提示漏了谁
	err := svc.Delete(context.Background(), "t", []string{"grp-builtin"})
	if err == nil || !strings.Contains(err.Error(), "leaf-") {
		t.Fatalf("应报子树不完整并列出后代, got %v", err)
	}
	// 完整子树删除成功
	all := []string{"grp-builtin", "leaf-AskRootCause", "leaf-AskRecommendation", "leaf-AskStatus", "leaf-InspectAlert", "leaf-ListAlerts", "leaf-Unknown"}
	if err := svc.Delete(context.Background(), "t", all); err != nil {
		t.Fatalf("完整删除应成功: %v", err)
	}
	tree, _ := svc.Tree(context.Background())
	if len(tree) != 0 {
		t.Fatalf("删除后树应为空, got %+v", tree)
	}
}

func TestSetEnabledSubtreeIntegrityAndActiveLeaves(t *testing.T) {
	svc := newTestService(t)
	if err := svc.SetEnabled(context.Background(), "t", []string{"grp-builtin"}, false); err == nil {
		t.Fatal("只停分组不停叶子应报错")
	}
	all := []string{"grp-builtin", "leaf-AskRootCause", "leaf-AskRecommendation", "leaf-AskStatus", "leaf-InspectAlert", "leaf-ListAlerts", "leaf-Unknown"}
	if err := svc.SetEnabled(context.Background(), "t", all, false); err != nil {
		t.Fatalf("完整停用应成功: %v", err)
	}
	leaves, _ := svc.ActiveLeaves(context.Background())
	if len(leaves) != 0 {
		t.Fatalf("全停用后 ActiveLeaves 应为空, got %d", len(leaves))
	}
}

func TestCacheInvalidatedOnWrite(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()
	fake := &countCache{}
	svc := NewService(st, fake)
	if _, err := svc.Tree(context.Background()); err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if fake.puts != 1 {
		t.Fatalf("首次读应回源 Put 一次, got %d", fake.puts)
	}
	_, _ = svc.Create(context.Background(), "t", CreateReq{ParentID: "grp-builtin", Name: "x", Kind: model.KindKB, Code: "k3"})
	if fake.invalidates != 1 {
		t.Fatalf("写操作应 Invalidate 一次, got %d", fake.invalidates)
	}
}

type countCache struct{ puts, invalidates int }

func (c *countCache) Get(context.Context) ([]model.IntentNode, bool) { return nil, false }
func (c *countCache) Put(context.Context, []model.IntentNode)         { c.puts++ }
func (c *countCache) Invalidate(context.Context)                      { c.invalidates++ }
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/intent/ -run 'TestTreeAndSeed|TestCreateAndDuplicateCode|TestUpdateKindChangeClearsBind|TestDeleteSubtreeIntegrity|TestSetEnabledSubtreeIntegrityAndActiveLeaves|TestCacheInvalidatedOnWrite' -v`
Expected: FAIL——`undefined: Service`/`NewService` 等。

- [ ] **Step 3: 写实现（新建 `service.go`）**

```go
package intent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"aiops/internal/model"
	"aiops/internal/store"
)

var (
	ErrNotFound         = errors.New("节点不存在")
	ErrDuplicateCode    = errors.New("意图 code 已存在")
	ErrSubtreeIncomplete = errors.New("子树不完整")
)

type Service struct {
	st    *store.Store
	cache Cache
}

func NewService(st *store.Store, cache Cache) *Service {
	if cache == nil {
		cache = NewMemCache()
	}
	return &Service{st: st, cache: cache}
}

type CreateReq struct {
	ParentID     string           `json:"parent_id"`
	Name         string           `json:"name"`
	Kind         model.IntentKind `json:"kind"`
	Code         string           `json:"code"`
	BindTarget   string           `json:"bind_target"`
	RuleKeywords []string         `json:"rule_keywords"`
	Description  string           `json:"description"`
	SortOrder    int              `json:"sort_order"`
}

// UpdateReq 全量覆盖更新：前端持有当前节点，回传全部可编辑字段。
type UpdateReq struct {
	ParentID     string           `json:"parent_id"`
	Name         string           `json:"name"`
	Kind         model.IntentKind `json:"kind"`
	Code         string           `json:"code"`
	BindTarget   string           `json:"bind_target"`
	RuleKeywords []string         `json:"rule_keywords"`
	Description  string           `json:"description"`
	SortOrder    int              `json:"sort_order"`
}

func (s *Service) Create(ctx context.Context, operator string, req CreateReq) (*model.IntentNode, error) {
	now := time.Now()
	n := model.IntentNode{
		ID: model.NewID("node"), ParentID: req.ParentID, Name: req.Name, Kind: req.Kind,
		Code: req.Code, BindTarget: req.BindTarget, RuleKeywords: req.RuleKeywords,
		Description: req.Description, Enabled: true, SortOrder: req.SortOrder, CreatedAt: now, UpdatedAt: now,
	}
	normalize(&n)
	var created *model.IntentNode
	err := s.st.WithIntentTx(func(tx store.IntentStore) error {
		if err := validateNode(n, tx); err != nil {
			return err
		}
		if err := tx.InsertIntentNode(n); err != nil {
			return err
		}
		created = &n
		return tx.InsertIntentAudit(model.IntentAudit{
			ID: model.NewID("audit"), NodeID: n.ID, Action: model.AuditCreate,
			Operator: operator, AfterJSON: model.MustJSON(n), CreatedAt: now,
		})
	})
	if err != nil {
		return nil, err
	}
	s.cache.Invalidate(ctx)
	return created, nil
}

func (s *Service) Update(ctx context.Context, operator string, id string, req UpdateReq) (*model.IntentNode, error) {
	var updated *model.IntentNode
	err := s.st.WithIntentTx(func(tx store.IntentStore) error {
		cur, err := tx.GetIntentNode(id)
		if err != nil {
			return ErrNotFound
		}
		n := cur
		n.Name = req.Name
		n.Code = req.Code
		n.Description = req.Description
		n.SortOrder = req.SortOrder
		// 改 kind 联动清绑定
		if req.Kind != n.Kind {
			n.Kind = req.Kind
			n.BindTarget = ""
		} else {
			n.Kind = req.Kind
			n.BindTarget = req.BindTarget
		}
		n.RuleKeywords = req.RuleKeywords
		// 移动校验（防成环）
		if req.ParentID != cur.ParentID {
			if err := ensureNoCycle(tx, id, req.ParentID); err != nil {
				return err
			}
			n.ParentID = req.ParentID
		}
		normalize(&n)
		if err := validateNode(n, tx); err != nil {
			return err
		}
		n.UpdatedAt = time.Now()
		if err := tx.UpdateIntentNode(n); err != nil {
			return err
		}
		updated = &n
		return tx.InsertIntentAudit(model.IntentAudit{
			ID: model.NewID("audit"), NodeID: id, Action: model.AuditUpdate,
			Operator: operator, BeforeJSON: model.MustJSON(cur), AfterJSON: model.MustJSON(n), CreatedAt: n.UpdatedAt,
		})
	})
	if err != nil {
		return nil, err
	}
	s.cache.Invalidate(ctx)
	return updated, nil
}

func (s *Service) Delete(ctx context.Context, operator string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	err := s.st.WithIntentTx(func(tx store.IntentStore) error {
		nodes, err := tx.ListIntentNodes()
		if err != nil {
			return err
		}
		if err := validateDownwardClosed(nodes, ids); err != nil {
			return err
		}
		byID := map[string]model.IntentNode{}
		for _, n := range nodes {
			byID[n.ID] = n
		}
		var targets []string
		for _, id := range ids {
			if n, ok := byID[id]; ok && !n.Deleted {
				targets = append(targets, id)
			}
		}
		if len(targets) > 0 {
			if err := tx.SoftDeleteIntentNodes(targets); err != nil {
				return err
			}
		}
		now := time.Now()
		for _, id := range targets {
			before := byID[id]
			after := before
			after.Deleted = true
			after.UpdatedAt = now
			if err := tx.InsertIntentAudit(model.IntentAudit{
				ID: model.NewID("audit"), NodeID: id, Action: model.AuditDelete,
				Operator: operator, BeforeJSON: model.MustJSON(before), AfterJSON: model.MustJSON(after), CreatedAt: now,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.cache.Invalidate(ctx)
	return nil
}

func (s *Service) SetEnabled(ctx context.Context, operator string, ids []string, enabled bool) error {
	if len(ids) == 0 {
		return nil
	}
	action := model.AuditDisable
	if enabled {
		action = model.AuditEnable
	}
	err := s.st.WithIntentTx(func(tx store.IntentStore) error {
		nodes, err := tx.ListIntentNodes()
		if err != nil {
			return err
		}
		if err := validateDownwardClosed(nodes, ids); err != nil {
			return err
		}
		byID := map[string]model.IntentNode{}
		for _, n := range nodes {
			byID[n.ID] = n
		}
		var targets []string
		for _, id := range ids {
			if n, ok := byID[id]; ok && !n.Deleted && n.Enabled != enabled {
				targets = append(targets, id)
			}
		}
		if len(targets) > 0 {
			if err := tx.SetIntentNodesEnabled(targets, enabled); err != nil {
				return err
			}
		}
		now := time.Now()
		for _, id := range targets {
			before := byID[id]
			after := before
			after.Enabled = enabled
			after.UpdatedAt = now
			if err := tx.InsertIntentAudit(model.IntentAudit{
				ID: model.NewID("audit"), NodeID: id, Action: action,
				Operator: operator, BeforeJSON: model.MustJSON(before), AfterJSON: model.MustJSON(after), CreatedAt: now,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.cache.Invalidate(ctx)
	return nil
}

func (s *Service) Tree(ctx context.Context) ([]model.IntentTreeNode, error) {
	nodes, err := s.cachedNodes(ctx)
	if err != nil {
		return nil, err
	}
	return BuildForest(nodes), nil
}

func (s *Service) ActiveLeaves(ctx context.Context) ([]model.LeafSpec, error) {
	nodes, err := s.cachedNodes(ctx)
	if err != nil {
		return nil, err
	}
	return ActiveLeavesFrom(nodes), nil
}

func (s *Service) cachedNodes(ctx context.Context) ([]model.IntentNode, error) {
	if nodes, ok := s.cache.Get(ctx); ok {
		return nodes, nil
	}
	all, err := s.st.ListIntentNodes()
	if err != nil {
		return nil, err
	}
	live := make([]model.IntentNode, 0, len(all))
	for _, n := range all {
		if !n.Deleted {
			live = append(live, n)
		}
	}
	s.cache.Put(ctx, live)
	return live, nil
}

// BuildForest 将扁平节点按 parent_id 递归组树（返回顶层森林）。
func BuildForest(nodes []model.IntentNode) []model.IntentTreeNode {
	children := map[string][]model.IntentNode{}
	var roots []model.IntentNode
	for _, n := range nodes {
		if n.ParentID == "" {
			roots = append(roots, n)
		} else {
			children[n.ParentID] = append(children[n.ParentID], n)
		}
	}
	var build func(n model.IntentNode) model.IntentTreeNode
	build = func(n model.IntentNode) model.IntentTreeNode {
		node := model.IntentTreeNode{IntentNode: n, Children: []model.IntentTreeNode{}}
		for _, c := range children[n.ID] {
			node.Children = append(node.Children, build(c))
		}
		return node
	}
	out := make([]model.IntentTreeNode, 0, len(roots))
	for _, r := range roots {
		out = append(out, build(r))
	}
	return out
}

// ActiveLeavesFrom 派生活跃叶子：叶子 + 自身与全部祖先 enabled。
func ActiveLeavesFrom(nodes []model.IntentNode) []model.LeafSpec {
	byID := make(map[string]model.IntentNode, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}
	enabledToRoot := func(n model.IntentNode) bool {
		for {
			if !n.Enabled {
				return false
			}
			if n.ParentID == "" {
				return true
			}
			parent, ok := byID[n.ParentID]
			if !ok {
				return false
			}
			n = parent
		}
	}
	var out []model.LeafSpec
	for _, n := range nodes {
		if n.Kind == model.KindGroup {
			continue
		}
		if !enabledToRoot(n) {
			continue
		}
		out = append(out, model.LeafSpec{Code: n.Code, Name: n.Name, Description: n.Description, RuleKeywords: n.RuleKeywords})
	}
	return out
}

func normalize(n *model.IntentNode) {
	if n.Kind == model.KindGroup {
		n.Code = ""
		n.BindTarget = ""
		n.RuleKeywords = []string{}
		n.Description = ""
		return
	}
	if n.Kind == model.KindSystem {
		n.BindTarget = ""
	}
	if n.RuleKeywords == nil {
		n.RuleKeywords = []string{}
	}
}

func validateNode(n model.IntentNode, tx store.IntentStore) error {
	if strings.TrimSpace(n.Name) == "" {
		return errors.New("节点名称不能为空")
	}
	switch n.Kind {
	case model.KindGroup, model.KindSystem, model.KindKB, model.KindMCP:
	default:
		return errors.New("非法 kind")
	}
	if n.ParentID != "" {
		p, err := tx.GetIntentNode(n.ParentID)
		if err != nil {
			return errors.New("父节点不存在")
		}
		if p.Kind != model.KindGroup {
			return errors.New("父节点必须是分组")
		}
	}
	if n.Kind == model.KindGroup {
		return nil
	}
	if strings.TrimSpace(n.Code) == "" {
		return errors.New("意图叶子必须填写 code")
	}
	if n.Kind == model.KindSystem && !model.IsSystemCode(n.Code) {
		return errors.New("SYSTEM code 非内置")
	}
	return ensureCodeUnique(tx, n.Code, n.ID)
}

func ensureCodeUnique(tx store.IntentStore, code, selfID string) error {
	nodes, err := tx.ListIntentNodes()
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if n.Deleted || n.Kind == model.KindGroup {
			continue
		}
		if n.Code == code && n.ID != selfID {
			return ErrDuplicateCode
		}
	}
	return nil
}

func ensureNoCycle(tx store.IntentStore, id, newParentID string) error {
	cur := newParentID
	for cur != "" {
		if cur == id {
			return errors.New("不能移动到自身或后代下")
		}
		p, err := tx.GetIntentNode(cur)
		if err != nil {
			break // 父不存在由 validateNode 报错
		}
		cur = p.ParentID
	}
	return nil
}

func validateDownwardClosed(nodes []model.IntentNode, ids []string) error {
	byID := map[string]model.IntentNode{}
	for _, n := range nodes {
		if !n.Deleted {
			byID[n.ID] = n
		}
	}
	children := map[string][]string{}
	for _, n := range byID {
		if n.ParentID != "" {
			children[n.ParentID] = append(children[n.ParentID], n.ID)
		}
	}
	inSet := map[string]bool{}
	for _, id := range ids {
		inSet[id] = true
	}
	for _, id := range ids {
		if _, ok := byID[id]; !ok {
			continue // 已删除/不存在 → 幂等跳过
		}
		var missing []string
		collectDescendants(id, children, inSet, &missing)
		if len(missing) > 0 {
			return fmt.Errorf("%w：节点 %s(%s) 的后代 %v 未包含在本次操作中", ErrSubtreeIncomplete, byID[id].Name, id, missing)
		}
	}
	return nil
}

func collectDescendants(id string, children map[string][]string, inSet map[string]bool, missing *[]string) {
	for _, c := range children[id] {
		if !inSet[c] {
			*missing = append(*missing, c)
		}
		collectDescendants(c, children, inSet, missing)
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/intent/ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/intent/service.go backend/internal/intent/service_test.go
git commit -m "feat: intent服务层CRUD/子树完整性校验/留痕/缓存"
```

---

### Task 5: agent 意图识别改由树驱动

**Files:**
- Modify: `backend/internal/agent/intent.go`（`Intent` 改 string、`SeedLeaves`、`RuleMatch`/`ClassifyWithLight`/`ParseIntentWithLight`/`ParseIntentSet`/`ParseIntent`/`IntentName`）
- Modify: `backend/internal/agent/agent.go`（`Agent` 加 `leaves` 字段、`NewWithLeaves`、`activeLeaves`、`answerSingle`/`answerOneStatus` 用 `RuleMatch`/`ParseIntentWithLight`、KB/MCP 占位回复）
- Test: `backend/internal/agent/agent_test.go`

**Interfaces:**
- Consumes: `model.LeafSpec`（Task 1）
- Produces:
  - `type Intent = string`；常量 `Unknown/ListAlerts/InspectAlert/AskRootCause/AskRecommendation/AskStatus`（字符串值）
  - `var SeedLeaves []model.LeafSpec`
  - `func RuleMatch(leaves []model.LeafSpec, text string) Intent`
  - `func ClassifyWithLight(ctx context.Context, leaves []model.LeafSpec, text string, c ctxpkg.Completer) Intent`
  - `func ParseIntent(text string) Intent`（= `RuleMatch(nil, text)`，向后兼容）
  - `func ParseIntentWithLight(ctx context.Context, leaves []model.LeafSpec, text string, c ctxpkg.Completer) Intent`
  - `func ParseIntentSet(ctx context.Context, leaves []model.LeafSpec, text string, c ctxpkg.Completer, maxSplit int) []Intent`
  - `func IntentName(i Intent) string`
  - `func NewWithLeaves(s *store.Store, lc, light ctxpkg.Completer, eng *pipeline.Engine, cs *ctxpkg.Store, topK, maxSplit int, leaves func() []model.LeafSpec) *Agent`
- 依赖 Task 4 之后完成（main 装配在 Task 8 注入 `svc.ActiveLeaves`）

- [ ] **Step 1: 写失败测试（修改/追加 `agent_test.go`）**

`TestParseIntent` 保持不变（护栏）。修改 `TestClassifyWithLight`/`TestParseIntentWithLight`/`TestParseIntentSet`/`TestIntentName` 使其适配新签名（加 `nil`/`SeedLeaves` 参数），并追加树驱动用例：

```go
func TestClassifyWithLight(t *testing.T) {
	c := &scriptedLLM{raw: `{"intent":"AskRootCause"}`}
	if got := ClassifyWithLight(context.Background(), nil, "为什么磁盘满了", c); got != AskRootCause {
		t.Fatalf("ClassifyWithLight = %v, want AskRootCause", got)
	}
	bad := &scriptedLLM{raw: `{"intent":"Nope"}`}
	if got := ClassifyWithLight(context.Background(), nil, "x", bad); got != Unknown {
		t.Fatalf("非法意图应为 Unknown, got %v", got)
	}
	fail := &scriptedLLM{err: errors.New("boom")}
	if got := ClassifyWithLight(context.Background(), nil, "x", fail); got != Unknown {
		t.Fatalf("LLM 出错应为 Unknown, got %v", got)
	}
}

func TestParseIntentWithLight(t *testing.T) {
	c := &scriptedLLM{raw: `{"intent":"Nope"}`}
	if got := ParseIntentWithLight(context.Background(), nil, "alarm-1什么原因", c); got != AskRootCause {
		t.Fatalf("规则命中应为 AskRootCause, got %v", got)
	}
	if got := ParseIntentWithLight(context.Background(), nil, "帮我看看今天天气", &scriptedLLM{raw: `{"intent":"ListAlerts"}`}); got != ListAlerts {
		t.Fatalf("LLM 分类应为 ListAlerts, got %v", got)
	}
	if got := ParseIntentWithLight(context.Background(), nil, "今天天气如何", nil); got != Unknown {
		t.Fatalf("nil completer 应回退 Unknown, got %v", got)
	}
}

func TestParseIntentSet(t *testing.T) {
	c := &scriptedLLM{raw: `{"rewritten":"有哪些告警？alarm-1什么原因？","sub_questions":["有哪些告警","alarm-1什么原因"]}`}
	got := ParseIntentSet(context.Background(), nil, "有哪些告警？alarm-1什么原因？", c, 3)
	if len(got) != 2 || got[0] != ListAlerts || got[1] != AskRootCause {
		t.Fatalf("ParseIntentSet = %v", got)
	}
}

func TestIntentName(t *testing.T) {
	if IntentName(AskRootCause) != "AskRootCause" {
		t.Fatalf("IntentName = %q", IntentName(AskRootCause))
	}
	if IntentName("Bogus") != "Bogus" {
		t.Fatalf("IntentName 应为恒等映射")
	}
}

func TestRuleMatchFromLeaves(t *testing.T) {
	leaves := []model.LeafSpec{
		{Code: "AskRootCause", RuleKeywords: []string{"为什么"}},
		{Code: "ListAlerts", RuleKeywords: []string{"列表"}},
	}
	if got := RuleMatch(leaves, "为什么磁盘满了"); got != "AskRootCause" {
		t.Fatalf("RuleMatch 应命中 AskRootCause, got %q", got)
	}
	if got := RuleMatch(leaves, "看看列表"); got != "ListAlerts" {
		t.Fatalf("RuleMatch 应命中 ListAlerts, got %q", got)
	}
	if got := RuleMatch(leaves, "今天天气"); got != Unknown {
		t.Fatalf("未命中应 Unknown, got %q", got)
	}
}

func TestAgentRecognizesCustomLeaf(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	now := time.Now()
	_ = s.SaveAlert(model.Alert{ID: "alarm-1", Device: "db-01", Metric: "disk_usage", Severity: "critical", FiredAt: now, CreatedAt: now})
	leaves := func() []model.LeafSpec {
		return []model.LeafSpec{{Code: "MyKB", Name: "MyKB", Description: "查知识库", RuleKeywords: []string{"查手册"}}}
	}
	ag := NewWithLeaves(s, nil, nil, nil, nil, 3, 3, leaves)
	msg, _ := ag.Handle(context.Background(), "帮我查手册")
	if !strings.Contains(msg.Content, "尚未实现") {
		t.Fatalf("KB 意图应输出占位语, got %q", msg.Content)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent/ -run 'TestClassifyWithLight|TestParseIntentWithLight|TestParseIntentSet|TestIntentName|TestRuleMatchFromLeaves|TestAgentRecognizesCustomLeaf' -v`
Expected: FAIL——`undefined: RuleMatch`/`NewWithLeaves` 等，且 `classifyWithLight` 旧签名不再匹配。

- [ ] **Step 3: 写实现（重写 `intent.go` 识别部分 + 修改 `agent.go`）**

重写 `intent.go`（整体替换 `Intent` 枚举、`ParseIntent`、`classifyWithLight`、`ParseIntentWithLight`、`ParseIntentSet`、`IntentName`；`RewriteAndSplit` 保持不变）：

```go
package agent

import (
	"context"
	"errors"
	"regexp"
	"strings"

	ctxpkg "aiops/internal/context"
	"aiops/internal/llm"
	"aiops/internal/model"
)

// Intent 意图 code（字符串别名）。
type Intent = string

const (
	Unknown           Intent = "Unknown"
	ListAlerts        Intent = "ListAlerts"
	InspectAlert      Intent = "InspectAlert"
	AskRootCause      Intent = "AskRootCause"
	AskRecommendation Intent = "AskRecommendation"
	AskStatus         Intent = "AskStatus"
)

// SeedLeaves 内置 6 种子（provider 为空时的兜底；顺序即规则匹配优先级）。
var SeedLeaves = []model.LeafSpec{
	{Code: AskRootCause, Name: "AskRootCause", Description: "询问根因", RuleKeywords: []string{"什么原因", "为什么", "根因"}},
	{Code: AskRecommendation, Name: "AskRecommendation", Description: "询问处置建议", RuleKeywords: []string{"怎么处理", "怎么办", "建议", "处置"}},
	{Code: AskStatus, Name: "AskStatus", Description: "询问状态/进度", RuleKeywords: []string{"状态", "进度"}},
	{Code: InspectAlert, Name: "InspectAlert", Description: "排查某个告警", RuleKeywords: []string{}},
	{Code: ListAlerts, Name: "ListAlerts", Description: "查看告警列表", RuleKeywords: []string{"列表", "有哪些告警", "告警"}},
	{Code: Unknown, Name: "Unknown", Description: "与告警运维无关", RuleKeywords: []string{}},
}

var alarmRe = regexp.MustCompile(`(?i)alarm[-_ ]?\w*|(compute|web|db|storage|switch)-?0?\d*`)

// inspectAlertMatch 内置复合规则：查/排查/看 + 告警/设备名。
func inspectAlertMatch(low string) bool {
	verb := strings.Contains(low, "查") || strings.Contains(low, "排查") || strings.Contains(low, "看")
	return verb && alarmRe.MatchString(low)
}

// RuleMatch 规则优先：按 leaves 顺序匹配；InspectAlert 用内置复合规则，其余用 rule_keywords 子串命中。
// leaves 为空时回退 SeedLeaves。
func RuleMatch(leaves []model.LeafSpec, text string) Intent {
	if len(leaves) == 0 {
		leaves = SeedLeaves
	}
	low := strings.ToLower(text)
	for _, l := range leaves {
		if l.Code == Unknown {
			continue
		}
		if l.Code == InspectAlert {
			if inspectAlertMatch(low) {
				return InspectAlert
			}
			continue
		}
		for _, kw := range l.RuleKeywords {
			if kw != "" && strings.Contains(low, strings.ToLower(kw)) {
				return l.Code
			}
		}
	}
	return Unknown
}

// ParseIntent 单意图规则识别（向后兼容，等价 RuleMatch(nil, text)）。
func ParseIntent(text string) Intent { return RuleMatch(nil, text) }

// ClassifyWithLight 轻量 LLM 分类：闭集 = leaves 的 code+description；失败/非法映射 Unknown。
func ClassifyWithLight(ctx context.Context, leaves []model.LeafSpec, text string, c ctxpkg.Completer) Intent {
	if len(leaves) == 0 {
		leaves = SeedLeaves
	}
	var parts []string
	for _, l := range leaves {
		desc := l.Description
		if desc == "" {
			desc = l.Code
		}
		parts = append(parts, l.Code+"("+desc+")")
	}
	system := "你是意图分类器。判断用户请求属于哪个意图，只输出 JSON：{\"intent\":\"<意图名>\"}。意图名必须是 " + strings.Join(parts, "、") + "。无法确定时返回 Unknown。"
	raw, err := c.Complete(ctx, system, text)
	if err != nil || strings.TrimSpace(raw) == "" {
		return Unknown
	}
	var out struct {
		Intent string `json:"intent"`
	}
	if err := llm.ParseJSON(raw, &out); err != nil {
		return Unknown
	}
	for _, l := range leaves {
		if out.Intent == l.Code {
			return l.Code
		}
	}
	return Unknown
}

// ParseIntentWithLight 规则优先；Unknown 用轻量 LLM 分类。
func ParseIntentWithLight(ctx context.Context, leaves []model.LeafSpec, text string, c ctxpkg.Completer) Intent {
	if intent := RuleMatch(leaves, text); intent != Unknown {
		return intent
	}
	if c == nil || ctxpkg.IsNilCompleter(c) {
		return Unknown
	}
	return ClassifyWithLight(ctx, leaves, text, c)
}

// ParseIntentSet 解析一条输入的全部意图：重写拆分 → 逐子问题 ParseIntentWithLight，截断到 maxSplit。
func ParseIntentSet(ctx context.Context, leaves []model.LeafSpec, text string, c ctxpkg.Completer, maxSplit int) []Intent {
	if c == nil || ctxpkg.IsNilCompleter(c) {
		return []Intent{RuleMatch(leaves, text)}
	}
	_, subs, err := RewriteAndSplit(ctx, text, c)
	if err != nil || len(subs) == 0 {
		return []Intent{RuleMatch(leaves, text)}
	}
	if maxSplit <= 0 {
		maxSplit = 3
	}
	if len(subs) > maxSplit {
		subs = subs[:maxSplit]
	}
	out := make([]Intent, 0, len(subs))
	for _, s := range subs {
		out = append(out, ParseIntentWithLight(ctx, leaves, s, c))
	}
	return out
}

// IntentName 返回意图 code（恒等）。
func IntentName(i Intent) string { return i }
```

`RewriteAndSplit` 保持原样不动。

修改 `agent.go`：

1. `Agent` 结构体追加字段：

```go
	leaves func() []model.LeafSpec // 活跃叶子 provider；nil 回退 SeedLeaves
```

2. 构造函数追加 `NewWithLeaves`，`NewWithLight` 委托它（签名不变）：

```go
func NewWithLight(s *store.Store, lc, light ctxpkg.Completer, eng *pipeline.Engine, cs *ctxpkg.Store, topK, maxSplit int) *Agent {
	return NewWithLeaves(s, lc, light, eng, cs, topK, maxSplit, nil)
}

func NewWithLeaves(s *store.Store, lc, light ctxpkg.Completer, eng *pipeline.Engine, cs *ctxpkg.Store, topK, maxSplit int, leaves func() []model.LeafSpec) *Agent {
	return &Agent{store: s, llm: lc, lightLLM: light, maxSplit: maxSplit, eng: eng, ctxStore: cs, topK: topK, leaves: leaves}
}
```

3. 新增 `activeLeaves` 与 `placeholderReply`：

```go
func (a *Agent) activeLeaves() []model.LeafSpec {
	if a.leaves != nil {
		if l := a.leaves(); len(l) > 0 {
			return l
		}
	}
	return SeedLeaves
}

func (a *Agent) placeholderReply(code Intent) string {
	if code == Unknown {
		return a.guideText()
	}
	return "已识别该意图，绑定的知识库/MCP 尚未实现。"
}
```

4. `answerSingle` 现有第 2 行 `intent := ParseIntent(userText)` 改为 `intent := RuleMatch(a.activeLeaves(), userText)`，`default` 分支的 `return a.guideText()` 改为 `return a.placeholderReply(intent)`。其余分支（`listAlertsReply`/`answerWithLLM`/`answerFor`/`AskStatus`）不变（已复用 `intent` 变量）。

5. `answerOneStatus` 的 `intent := ParseIntentWithLight(ctx, sub, a.lightLLM)` 改为 `intent := ParseIntentWithLight(ctx, a.activeLeaves(), sub, a.lightLLM)`，其 `default` 分支改为：

```go
	default:
		return a.placeholderReply(intent), statusFor(intent)
```

并在 `agent.go` 文件内追加：

```go
func statusFor(code Intent) string {
	if code == Unknown {
		return "unknown"
	}
	return "success"
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agent/ -v`
Expected: 全部 PASS（含既有 `TestParseIntent`/`TestExtractAlertIDHex`/`TestHandleMultiIntentAggregation`/`TestHandleUnknownGuidance` 等护栏）。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/agent/intent.go backend/internal/agent/agent.go backend/internal/agent/agent_test.go
git commit -m "feat: agent意图识别改由树驱动(Intent改string+SeedLeaves兜底)"
```

---

### Task 6: eval 适配（ParseIntentSet 签名）

**Files:**
- Modify: `backend/internal/eval/runner.go:155`
- Test: `backend/internal/eval/runner_test.go`（无需改，跑回归）

**Interfaces:**
- Consumes: `agent.ParseIntentSet(ctx, leaves, text, c, maxSplit)`（Task 5）
- Produces: 无新增符号

- [ ] **Step 1: 修改 `runner.go`**

`runChat` 中：

```go
	res.Intents = intentNames(agent.ParseIntentSet(ctx, t.ChatInput, light, 3))
```

改为：

```go
	res.Intents = intentNames(agent.ParseIntentSet(ctx, nil, t.ChatInput, light, 3))
```

（`nil` 表示使用 `SeedLeaves` 兜底，与 eval 每次 `store.New` 后的种子一致。）

- [ ] **Step 2: 运行测试确认通过**

Run: `go test ./internal/eval/ -run 'TestRuleRegressionMainTasks|TestRuleRegressionGpuTempFails|TestRunTaskChat' -v`
Expected: PASS。

- [ ] **Step 3: 提交**

```bash
git add backend/internal/eval/runner.go
git commit -m "feat: eval适配ParseIntentSet树驱动签名"
```

---

### Task 7: api 新增 /api/intents 路由

**Files:**
- Modify: `backend/internal/api/server.go`（`Server` 加 `svc` 字段、`NewServer` 加参数、路由、CORS 方法、handlers）
- Test: `backend/internal/api/intent_test.go`；Modify: `backend/internal/api/server_test.go`（`NewServer` 调用点）

**Interfaces:**
- Consumes: `intent.Service`/`intent.CreateReq`/`intent.UpdateReq`（Task 4）
- Produces: `func NewServer(cfg config.Config, s *store.Store, ag *agent.Agent, eng *pipeline.Engine, h *hub.Hub, sim *simulator.Simulator, svc *intent.Service) http.Handler`

- [ ] **Step 1: 写失败测试（新建 `intent_test.go`）**

```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"aiops/internal/agent"
	"aiops/internal/config"
	"aiops/internal/hub"
	"aiops/internal/intent"
	"aiops/internal/store"
)

func newIntentServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	t.Cleanup(func() { st.Close() })
	ag := agent.New(st, nil, nil)
	svc := intent.NewService(st, intent.NewMemCache())
	srv := NewServer(config.Config{}, st, ag, nil, hub.New(), nil, svc)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts, st
}

func TestIntentTreeEndpoint(t *testing.T) {
	ts, _ := newIntentServer(t)
	res, err := http.Get(ts.URL + "/api/intents/tree")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	var tree []json.RawMessage
	if err := json.NewDecoder(res.Body).Decode(&tree); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tree) != 1 {
		t.Fatalf("tree len = %d, want 1", len(tree))
	}
}

func TestIntentStatusIntegrityError(t *testing.T) {
	ts, _ := newIntentServer(t)
	body := strings.NewReader(`{"ids":["grp-builtin"],"enabled":false}`)
	req, _ := http.NewRequest("POST", ts.URL+"/api/intents/status", body)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("完整性失败应 400, got %d", res.StatusCode)
	}
	var e map[string]string
	_ = json.NewDecoder(res.Body).Decode(&e)
	if !strings.Contains(e["error"], "未包含") {
		t.Fatalf("错误信息应提示漏了谁, got %q", e["error"])
	}
}
```

同时修改 `server_test.go` 两处 `NewServer(config.Config{}, s, ag, nil, h, nil)` 追加第 7 参：

```go
	srv := NewServer(config.Config{}, s, ag, nil, h, nil, intent.NewService(s, intent.NewMemCache()))
```

（并在 `server_test.go` import 块加 `"aiops/internal/intent"`。）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/api/ -run 'TestIntentTreeEndpoint|TestIntentStatusIntegrityError' -v`
Expected: FAIL——`undefined: intent.NewService`（或 `NewServer` 参数不匹配）。

- [ ] **Step 3: 写实现（修改 `server.go`）**

import 块追加 `"aiops/internal/intent"`。`Server` 结构体加 `svc *intent.Service`；`NewServer` 签名加 `svc *intent.Service` 参数并赋给 `srv.svc`。

`routes()` 追加：

```go
	s.mux.HandleFunc("GET /api/intents/tree", s.handleIntentTree)
	s.mux.HandleFunc("GET /api/intents/leaves", s.handleIntentLeaves)
	s.mux.HandleFunc("POST /api/intents", s.handleIntentCreate)
	s.mux.HandleFunc("PATCH /api/intents/{id}", s.handleIntentUpdate)
	s.mux.HandleFunc("DELETE /api/intents", s.handleIntentDelete)
	s.mux.HandleFunc("POST /api/intents/status", s.handleIntentStatus)
```

`withCORS` 的 `Access-Control-Allow-Methods` 改为 `"GET, POST, PATCH, DELETE, OPTIONS"`。

文件末尾追加 handlers 与辅助：

```go
func operator(r *http.Request) string {
	op := r.Header.Get("X-Operator")
	if op == "" {
		op = "admin"
	}
	return op
}

func intentStatus(err error) int {
	switch {
	case err == intent.ErrDuplicateCode:
		return http.StatusConflict
	case err == intent.ErrNotFound:
		return http.StatusNotFound
	case err == intent.ErrSubtreeIncomplete:
		return http.StatusBadRequest
	default:
		return http.StatusBadRequest
	}
}

func (s *Server) handleIntentTree(w http.ResponseWriter, r *http.Request) {
	tree, err := s.svc.Tree(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, tree)
}

func (s *Server) handleIntentLeaves(w http.ResponseWriter, r *http.Request) {
	leaves, err := s.svc.ActiveLeaves(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, leaves)
}

func (s *Server) handleIntentCreate(w http.ResponseWriter, r *http.Request) {
	var req intent.CreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}
	n, err := s.svc.Create(r.Context(), operator(r), req)
	if err != nil {
		writeJSON(w, intentStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleIntentUpdate(w http.ResponseWriter, r *http.Request) {
	var req intent.UpdateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}
	n, err := s.svc.Update(r.Context(), operator(r), r.PathValue("id"), req)
	if err != nil {
		writeJSON(w, intentStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleIntentDelete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}
	if err := s.svc.Delete(r.Context(), operator(r), body.IDs); err != nil {
		writeJSON(w, intentStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleIntentStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs     []string `json:"ids"`
		Enabled bool     `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}
	if err := s.svc.SetEnabled(r.Context(), operator(r), body.IDs, body.Enabled); err != nil {
		writeJSON(w, intentStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
```

注意：`intentStatus` 只在 `err != nil` 时被调用；`ErrNotFound` 与其他校验错误均按语义映射，未列出的错误统一 400。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/api/ -v`
Expected: 全部 PASS（含既有 `TestHealthzAndAlerts`/`TestChatEndpoint`）。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/api/server.go backend/internal/api/intent_test.go backend/internal/api/server_test.go
git commit -m "feat: api新增意图树CRUD/启停/删除路由"
```

---

### Task 8: main.go 装配意图服务并注入 agent

**Files:**
- Modify: `backend/cmd/server/main.go:50-62`

**Interfaces:**
- Consumes: `intent.NewService`/`intent.NewMemCache`（Tasks 3-4）、`agent.NewWithLeaves`（Task 5）、`api.NewServer` 新签名（Task 7）

- [ ] **Step 1: 修改 `main.go`**

import 块加 `"aiops/internal/intent"`。在 `ag := ...` 之前追加服务并改 agent 构造：

```go
	svc := intent.NewService(st, intent.NewMemCache())
	ag := agent.NewWithLeaves(st, lc, light, eng, cs, cfg.TopK, cfg.MaxSplit, func() []model.LeafSpec {
		leaves, _ := svc.ActiveLeaves(context.Background())
		return leaves
	})
```

`NewServer` 调用追加 `svc`：

```go
	handler := api.NewServer(cfg, st, ag, eng, h, sim, svc)
```

- [ ] **Step 2: 验证编译与测试**

Run: `go build ./... && go test ./internal/agent/ ./internal/api/`
Expected: 编译通过、测试 PASS。

- [ ] **Step 3: 提交**

```bash
git add backend/cmd/server/main.go
git commit -m "feat: main装配意图服务并注入ActiveLeaves到agent"
```

---

### Task 9: 前端类型与 API client

**Files:**
- Modify: `frontend/src/types.ts`
- Modify: `frontend/src/api/client.ts`

**Interfaces:**
- Consumes: 后端意图 JSON 结构（Task 1/4/7）
- Produces:
  - `IntentKind`、`IntentNode`、`IntentTreeNode` 类型
  - `fetchIntentTree()`、`createIntent(body)`、`updateIntent(id, body)`、`deleteIntents(ids)`、`setIntentStatus(ids, enabled)`

- [ ] **Step 1: 修改 `types.ts`（追加）**

```ts
export type IntentKind = 'GROUP' | 'SYSTEM' | 'KB' | 'MCP'

export interface IntentNode {
  id: string
  parent_id: string
  name: string
  kind: IntentKind
  code: string
  bind_target: string
  rule_keywords: string[]
  description: string
  enabled: boolean
  deleted: boolean
  sort_order: number
  created_at: string
  updated_at: string
}

export interface IntentTreeNode extends IntentNode {
  children: IntentTreeNode[]
}

export interface IntentUpsert {
  parent_id: string
  name: string
  kind: IntentKind
  code: string
  bind_target: string
  rule_keywords: string[]
  description: string
  sort_order: number
}
```

- [ ] **Step 2: 修改 `client.ts`**

将 `req` 的 `!res.ok` 分支改为透出服务端错误信息：

```ts
async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(base + path, init)
  if (!res.ok) {
    let msg = `HTTP ${res.status}`
    try {
      const data = await res.json()
      if (data && typeof data.error === 'string') msg = data.error
    } catch {
      /* ignore */
    }
    throw new Error(msg)
  }
  return res.json() as Promise<T>
}
```

文件末尾追加：

```ts
export const fetchIntentTree = () => req<IntentTreeNode[]>('/intents/tree')
export const createIntent = (body: IntentUpsert) =>
  req<IntentNode>('/intents', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
export const updateIntent = (id: string, body: IntentUpsert) =>
  req<IntentNode>(`/intents/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
export const deleteIntents = (ids: string[]) =>
  req<{ status: string }>('/intents', {
    method: 'DELETE',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids }),
  })
export const setIntentStatus = (ids: string[], enabled: boolean) =>
  req<{ status: string }>('/intents/status', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids, enabled }),
  })
```

（import 行追加 `IntentTreeNode, IntentNode, IntentUpsert`。）

- [ ] **Step 3: 验证构建**

Run: `npm run build`（在 `frontend/`）
Expected: 编译通过。

- [ ] **Step 4: 提交**

```bash
git add frontend/src/types.ts frontend/src/api/client.ts
git commit -m "feat: 前端意图类型与API client"
```

---

### Task 10: 前端意图树组件 + 管理页 + 测试

**Files:**
- Create: `frontend/src/components/IntentTree.tsx`
- Create: `frontend/src/components/IntentEditor.tsx`
- Create: `frontend/src/components/IntentManager.tsx`
- Modify: `frontend/src/App.tsx`
- Test: `frontend/src/components/IntentTree.test.tsx`

**Interfaces:**
- Consumes: `IntentTreeNode`/`IntentUpsert`（Task 9）、`client` 意图 API（Task 9）
- Produces: `collectIds(node: IntentTreeNode): string[]`、`IntentTree`、`IntentEditor`、`IntentManager` 组件

- [ ] **Step 1: 写失败测试（新建 `IntentTree.test.tsx`）**

```tsx
import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import IntentTree, { collectIds } from './IntentTree'
import type { IntentTreeNode } from '../types'

const leaf = (id: string, name: string): IntentTreeNode => ({
  id, parent_id: 'g', name, kind: 'KB', code: name, bind_target: '', rule_keywords: [],
  description: '', enabled: true, deleted: false, sort_order: 0, created_at: '', updated_at: '', children: [],
})

const group: IntentTreeNode = {
  id: 'g', parent_id: '', name: '分组', kind: 'GROUP', code: '', bind_target: '', rule_keywords: [],
  description: '', enabled: true, deleted: false, sort_order: 0, created_at: '', updated_at: '',
  children: [leaf('a', '意图A'), leaf('b', '意图B')],
}

describe('IntentTree', () => {
  it('collectIds 收集整棵子树', () => {
    expect(collectIds(group).sort()).toEqual(['a', 'b', 'g'].sort())
  })

  it('切换分组提交整棵子树 id', () => {
    const onToggle = vi.fn()
    render(<IntentTree nodes={[group]} onToggle={onToggle} onDelete={vi.fn()} onAddChild={vi.fn()} onEdit={vi.fn()} />)
    fireEvent.click(screen.getAllByText('停用')[0])
    expect(onToggle).toHaveBeenCalledWith(group, ['g', 'a', 'b'])
  })

  it('显示错误信息', () => {
    render(<IntentTree nodes={[group]} onToggle={vi.fn()} onDelete={vi.fn()} onAddChild={vi.fn()} onEdit={vi.fn()}
      error="子树不完整：节点 分组(g) 的后代 [a] 未包含" />)
    expect(screen.getByText(/子树不完整/)).toBeTruthy()
  })
})
```

- [ ] **Step 2: 运行测试确认失败**

Run: `npx vitest run src/components/IntentTree.test.tsx`
Expected: FAIL——`Cannot find module './IntentTree'`。

- [ ] **Step 3: 写实现（新建三个组件 + 改 App.tsx）**

`IntentTree.tsx`：

```tsx
import { useState } from 'react'
import type { IntentTreeNode } from '../types'

export function collectIds(node: IntentTreeNode): string[] {
  const ids = [node.id]
  for (const c of node.children) ids.push(...collectIds(c))
  return ids
}

const kindBadge: Record<string, string> = {
  GROUP: '分组', SYSTEM: '内置', KB: '知识库', MCP: 'MCP',
}

export default function IntentTree({
  nodes, onToggle, onDelete, onAddChild, onEdit, error,
}: {
  nodes: IntentTreeNode[]
  onToggle: (node: IntentTreeNode, ids: string[]) => void
  onDelete: (node: IntentTreeNode, ids: string[]) => void
  onAddChild: (node: IntentTreeNode) => void
  onEdit: (node: IntentTreeNode) => void
  error?: string
}) {
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})

  const renderNode = (node: IntentTreeNode, depth: number): JSX.Element => {
    const isGroup = node.kind === 'GROUP'
    const isCollapsed = collapsed[node.id]
    return (
      <div key={node.id}>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', padding: '4px 0', paddingLeft: depth * 20 }}>
          {isGroup && (
            <button onClick={() => setCollapsed((c) => ({ ...c, [node.id]: !c[node.id] }))}>
              {isCollapsed ? '+' : '-'}
            </button>
          )}
          <span style={{ fontWeight: isGroup ? 700 : 400 }}>{node.name}</span>
          <span style={{ color: '#888' }}>{kindBadge[node.kind]}</span>
          {!isGroup && <span style={{ color: '#888' }}>{node.code}</span>}
          <span style={{ color: node.enabled ? '#2e7d32' : '#999' }}>{node.enabled ? '启用' : '停用'}</span>
          <button onClick={() => onToggle(node, collectIds(node))}>{node.enabled ? '停用' : '启用'}</button>
          {isGroup && <button onClick={() => onAddChild(node)}>+子节点</button>}
          <button onClick={() => onEdit(node)}>编辑</button>
          <button onClick={() => onDelete(node, collectIds(node))}>删除</button>
        </div>
        {isGroup && !isCollapsed && node.children.map((c) => renderNode(c, depth + 1))}
      </div>
    )
  }

  return (
    <div>
      {error && <div style={{ color: '#d32f2f', padding: 8 }}>{error}</div>}
      {nodes.length === 0 && <div style={{ color: '#888', padding: 16 }}>暂无意图节点</div>}
      {nodes.map((n) => renderNode(n, 0))}
    </div>
  )
}
```

`IntentEditor.tsx`：

```tsx
import { useState } from 'react'
import type { IntentTreeNode, IntentKind, IntentUpsert } from '../types'

export default function IntentEditor({
  initial, groups, onSubmit, onCancel,
}: {
  initial?: IntentTreeNode
  groups: IntentTreeNode[]
  onSubmit: (v: IntentUpsert) => void
  onCancel: () => void
}) {
  const [name, setName] = useState(initial?.name ?? '')
  const [kind, setKind] = useState<IntentKind>(initial?.kind ?? 'GROUP')
  const [code, setCode] = useState(initial?.code ?? '')
  const [bind, setBind] = useState(initial?.bind_target ?? '')
  const [kw, setKw] = useState((initial?.rule_keywords ?? []).join(', '))
  const [desc, setDesc] = useState(initial?.description ?? '')
  const [sort, setSort] = useState(String(initial?.sort_order ?? 0))
  const [parent, setParent] = useState(initial?.parent_id ?? '')

  const submit = () => {
    onSubmit({
      parent_id: parent, name, kind, code, bind_target: bind,
      rule_keywords: kw.split(/[,，]/).map((s) => s.trim()).filter(Boolean),
      description: desc, sort_order: Number(sort) || 0,
    })
  }

  const input = { padding: 4, margin: '4px 0' }

  return (
    <div style={{ border: '1px solid #ddd', padding: 12, marginTop: 12 }}>
      <h3>{initial ? '编辑节点' : '新建节点'}</h3>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4, maxWidth: 480 }}>
        <label>名称 <input style={input} value={name} onChange={(e) => setName(e.target.value)} /></label>
        <label>类型
          <select style={input} value={kind} onChange={(e) => setKind(e.target.value as IntentKind)}>
            <option value="GROUP">分组</option>
            <option value="SYSTEM">内置</option>
            <option value="KB">知识库</option>
            <option value="MCP">MCP工具</option>
          </select>
        </label>
        {kind !== 'GROUP' && <label>code <input style={input} value={code} onChange={(e) => setCode(e.target.value)} /></label>}
        {kind === 'KB' && <label>知识库ID <input style={input} value={bind} onChange={(e) => setBind(e.target.value)} /></label>}
        {kind === 'MCP' && <label>mcpToolId <input style={input} value={bind} onChange={(e) => setBind(e.target.value)} /></label>}
        {kind !== 'GROUP' && <label>关键词(逗号分隔) <input style={input} value={kw} onChange={(e) => setKw(e.target.value)} /></label>}
        {kind !== 'GROUP' && <label>描述 <input style={input} value={desc} onChange={(e) => setDesc(e.target.value)} /></label>}
        <label>排序 <input style={input} value={sort} onChange={(e) => setSort(e.target.value)} /></label>
        <label>父节点
          <select style={input} value={parent} onChange={(e) => setParent(e.target.value)}>
            <option value="">（顶层）</option>
            {groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
          </select>
        </label>
      </div>
      <div style={{ marginTop: 8 }}>
        <button onClick={submit}>保存</button>
        <button onClick={onCancel}>取消</button>
      </div>
    </div>
  )
}
```

`IntentManager.tsx`：

```tsx
import { useCallback, useEffect, useState } from 'react'
import IntentTree from './IntentTree'
import IntentEditor from './IntentEditor'
import { fetchIntentTree, createIntent, updateIntent, deleteIntents, setIntentStatus } from '../api/client'
import type { IntentTreeNode, IntentUpsert } from '../types'

function collectGroups(nodes: IntentTreeNode[]): IntentTreeNode[] {
  const out: IntentTreeNode[] = []
  for (const n of nodes) {
    if (n.kind === 'GROUP') {
      out.push(n)
      out.push(...collectGroups(n.children))
    }
  }
  return out
}

export default function IntentManager() {
  const [nodes, setNodes] = useState<IntentTreeNode[]>([])
  const [error, setError] = useState('')
  const [editing, setEditing] = useState<{ node?: IntentTreeNode; parentId?: string } | null>(null)

  const reload = useCallback(async () => {
    try {
      setNodes(await fetchIntentTree())
      setError('')
    } catch (e) {
      setError((e as Error).message)
    }
  }, [])

  useEffect(() => { reload() }, [reload])

  const toggle = async (node: IntentTreeNode, ids: string[]) => {
    try {
      await setIntentStatus(ids, !node.enabled)
      await reload()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const remove = async (node: IntentTreeNode, ids: string[]) => {
    try {
      await deleteIntents(ids)
      await reload()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const submit = async (v: IntentUpsert) => {
    try {
      if (editing?.node) await updateIntent(editing.node.id, v)
      else await createIntent({ ...v, parent_id: editing?.parentId ?? v.parent_id })
      setEditing(null)
      await reload()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div style={{ padding: 16 }}>
      <h2>意图管理</h2>
      {error && <div style={{ color: '#d32f2f', padding: 8 }}>{error}</div>}
      <IntentTree
        nodes={nodes}
        onToggle={toggle}
        onDelete={remove}
        onAddChild={(n) => setEditing({ parentId: n.id })}
        onEdit={(n) => setEditing({ node: n })}
      />
      {editing && (
        <IntentEditor
          initial={editing.node}
          groups={collectGroups(nodes)}
          onSubmit={submit}
          onCancel={() => setEditing(null)}
        />
      )}
    </div>
  )
}
```

`App.tsx`：顶部加导航切换「运维面板 / 意图管理」：

```tsx
import { useState } from 'react'
import { useEffect } from 'react'
...
import IntentManager from './components/IntentManager'
```

在 `App` 函数内加 `const [view, setView] = useState<'ops' | 'intents'>('ops')`，返回改为：

```tsx
  if (view === 'intents') {
    return (
      <div style={{ fontFamily: 'system-ui, sans-serif' }}>
        <h1 style={{ padding: '8px 16px' }}>AIOps 硬件智能运维</h1>
        <nav style={{ padding: '0 16px' }}>
          <button onClick={() => setView('ops')}>运维面板</button>
          <button onClick={() => setView('intents')} style={{ fontWeight: 700 }}>意图管理</button>
        </nav>
        <IntentManager />
      </div>
    )
  }
```

原 ops 视图的 `<h1>` 下方插入同样的 `nav`（其中「意图管理」按钮 `onClick={() => setView('intents')}`、「运维面板」按钮加粗）。其余原逻辑不变。

- [ ] **Step 4: 运行测试确认通过**

Run: `npx vitest run src/components/IntentTree.test.tsx`
Expected: PASS。

- [ ] **Step 5: 构建验证与提交**

Run: `npm run build`（在 `frontend/`）
Expected: 编译通过。

```bash
git add frontend/src/components/IntentTree.tsx frontend/src/components/IntentEditor.tsx frontend/src/components/IntentManager.tsx frontend/src/components/IntentTree.test.tsx frontend/src/App.tsx
git commit -m "feat: 前端意图树管理页与子树完整性交互"
```

---

### Task 11: 全量回归验证

**Files:**
- 无新增

**Interfaces:**
- Consumes: 全部已实现符号

- [ ] **Step 1: 后端全量单测**

Run: `go test ./...`
Expected: 全部 PASS。重点确认 `internal/eval` 的 `TestRuleRegressionMainTasks`/`TestRuleRegressionGpuTempFails`、`internal/agent`、`internal/store`、`internal/intent`、`internal/api`。

- [ ] **Step 2: 后端构建与 vet**

Run: `go build ./... && go vet ./...`
Expected: 无编译错误、无告警。

- [ ] **Step 3: 前端全量测试与构建**

Run: `npx vitest run && npm run build`（在 `frontend/`）
Expected: 全部 PASS、编译通过。

- [ ] **Step 4: 冒烟（可选，无 LLM key）**

Run: `LLM_API_KEY= go run ./cmd/eval ci --threshold 0.9`（在 `backend/`）
Expected: 退出码 0。

- [ ] **Step 5: 提交（若有改动）**

```bash
git status
# 无未提交改动时跳过 commit
```

---

## 自检清单（执行前确认）

- **Spec 覆盖**：数据模型/kind 语义/约束/种子/留痕表 → Task 1/2；REST API 六路由 → Task 7；服务层 Cache 抽象 + 进程内实现 → Task 3/4；DB 唯一事实源 + 写清缓存 + 读回源重建 → Task 4 `cachedNodes`；子树完整性 `validateDownwardClosed`（漏了谁）→ Task 4；写操作全留痕 → Task 2/4；改 kind 联动清绑定 → Task 4 `Update`；软删除 → Task 2/4；运行时识别由树驱动（规则 + LLM 闭集 + InspectAlert 内置 + SeedLeaves 兜底）→ Task 5；eval 适配 → Task 6；main 装配注入 ActiveLeaves → Task 8；前端管理页 + 子树交互 → Task 9/10；回归护栏 → Task 11。
- **placeholder 扫描**：所有代码步骤均给出完整实现；无 TBD/TODO。所有测试用例均为可直接运行的完整版本。
- **类型一致性**：`IntentStore`/`WithIntentTx`（Task 2）在 Task 4 引用一致；`Cache`/`NewMemCache`（Task 3）在 Task 4/7/8 一致；`CreateReq`/`UpdateReq`（Task 4）在 Task 7/9 一致；`RuleMatch`/`ClassifyWithLight`/`ParseIntentWithLight`/`ParseIntentSet`/`NewWithLeaves`（Task 5）在 Task 6/8 一致；`Intent` 为 `= string` 别名在 Task 5/6 一致（eval `intentNames`/`IntentName` 仍兼容）；前端 `IntentTreeNode`/`IntentUpsert`（Task 9）在 Task 10 一致。
