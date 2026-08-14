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
