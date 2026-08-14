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
	ErrNotFound          = errors.New("节点不存在")
	ErrDuplicateCode     = errors.New("意图 code 已存在")
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
		if cur.Deleted {
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
		if p.Deleted {
			return errors.New("父节点不存在")
		}
		if p.Kind != model.KindGroup {
			return errors.New("父节点必须是分组")
		}
	}
	if n.Kind == model.KindGroup {
		return nil
	}
	nodes, err := tx.ListIntentNodes()
	if err != nil {
		return err
	}
	for _, c := range nodes {
		if c.ParentID == n.ID && !c.Deleted {
			return errors.New("有子节点的分组不能转为意图叶子")
		}
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
