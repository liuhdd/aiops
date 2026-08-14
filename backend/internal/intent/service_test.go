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
