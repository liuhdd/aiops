package coordinator

import (
	"sync"
	"time"
)

type Coordinator struct {
	mu    sync.Mutex
	slots map[string]chan struct{}
}

func New() *Coordinator {
	return &Coordinator{slots: map[string]chan struct{}{}}
}

func (c *Coordinator) slot(device string) chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, ok := c.slots[device]
	if !ok {
		ch = make(chan struct{}, 1)
		c.slots[device] = ch
	}
	return ch
}

func (c *Coordinator) Acquire(device string) func() {
	ch := c.slot(device)
	ch <- struct{}{}
	return func() { <-ch }
}

func (c *Coordinator) TryAcquire(device string, timeout time.Duration) (func(), bool) {
	ch := c.slot(device)
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, true
	case <-time.After(timeout):
		return nil, false
	}
}
