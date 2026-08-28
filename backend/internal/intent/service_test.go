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

func TestUpdateGroupWithChildrenToLeafFails(t *testing.T) {
	svc := newTestService(t)
	g, err := svc.Create(context.Background(), "t", CreateReq{Name: "有子分组", Kind: model.KindGroup})
	if err != nil {
		t.Fatalf("Create 分组: %v", err)
	}
	if _, err := svc.Create(context.Background(), "t", CreateReq{ParentID: g.ID, Name: "叶子", Kind: model.KindKB, Code: "leaf1"}); err != nil {
		t.Fatalf("Create 叶子: %v", err)
	}
	if _, err := svc.Update(context.Background(), "t", g.ID, UpdateReq{ParentID: g.ParentID, Name: g.Name, Kind: model.KindKB, Code: "leaf2"}); err == nil {
		t.Fatal("有子节点的分组转为意图叶子应报错")
	}
}

func TestCreateUnderDeletedParentFails(t *testing.T) {
	svc := newTestService(t)
	g, err := svc.Create(context.Background(), "t", CreateReq{Name: "临时分组", Kind: model.KindGroup})
	if err != nil {
		t.Fatalf("Create 分组: %v", err)
	}
	if err := svc.Delete(context.Background(), "t", []string{g.ID}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Create(context.Background(), "t", CreateReq{ParentID: g.ID, Name: "孤儿", Kind: model.KindKB, Code: "orphan1"}); err == nil {
		t.Fatal("软删除分组下创建节点应报错")
	}
}

func TestUpdateDeletedNodeFails(t *testing.T) {
	svc := newTestService(t)
	n, err := svc.Create(context.Background(), "t", CreateReq{ParentID: "grp-builtin", Name: "临时叶子", Kind: model.KindKB, Code: "tmp1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(context.Background(), "t", []string{n.ID}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Update(context.Background(), "t", n.ID, UpdateReq{ParentID: n.ParentID, Name: n.Name, Kind: model.KindKB, Code: n.Code}); err != ErrNotFound {
		t.Fatalf("软删除节点 Update 应 ErrNotFound, got %v", err)
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

func TestCreatePersistsExamples(t *testing.T) {
	svc := newTestService(t)
	n, err := svc.Create(context.Background(), "t", CreateReq{ParentID: "grp-builtin", Name: "带示例", Kind: model.KindKB, Code: "ex1", Examples: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(n.Examples) != 2 || n.Examples[0] != "a" {
		t.Fatalf("examples = %+v", n.Examples)
	}
	leaves, _ := svc.ActiveLeaves(context.Background())
	for _, l := range leaves {
		if l.Code == "ex1" && (len(l.Examples) != 2 || l.Examples[1] != "b") {
			t.Fatalf("leaf examples = %+v", l.Examples)
		}
	}
}

func TestCreateNormalizesNilExamples(t *testing.T) {
	svc := newTestService(t)
	n, err := svc.Create(context.Background(), "t", CreateReq{ParentID: "grp-builtin", Name: "无示例", Kind: model.KindKB, Code: "noex"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if n.Examples == nil || len(n.Examples) != 0 {
		t.Fatalf("Examples 应为非 nil 空切片, got %#v", n.Examples)
	}
}

func TestActiveLeavesPath(t *testing.T) {
	svc := newTestService(t)
	g, err := svc.Create(context.Background(), "t", CreateReq{Name: "告警排查", Kind: model.KindGroup})
	if err != nil {
		t.Fatalf("Create 分组: %v", err)
	}
	if _, err := svc.Create(context.Background(), "t", CreateReq{ParentID: g.ID, Name: "查手册", Kind: model.KindKB, Code: "kbx"}); err != nil {
		t.Fatalf("Create 叶子: %v", err)
	}
	leaves, _ := svc.ActiveLeaves(context.Background())
	found := false
	for _, l := range leaves {
		if l.Code == "kbx" {
			if l.Path != "告警排查" {
				t.Fatalf("Path = %q, want 告警排查", l.Path)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("未找到 kbx 叶子")
	}
}

type countCache struct{ puts, invalidates int }

func (c *countCache) Get(context.Context) ([]model.IntentNode, bool) { return nil, false }
func (c *countCache) Put(context.Context, []model.IntentNode)        { c.puts++ }
func (c *countCache) Invalidate(context.Context)                     { c.invalidates++ }
