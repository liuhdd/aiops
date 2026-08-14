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
