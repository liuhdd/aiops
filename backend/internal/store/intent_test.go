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
