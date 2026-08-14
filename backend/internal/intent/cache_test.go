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
