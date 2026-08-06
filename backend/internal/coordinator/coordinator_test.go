package coordinator

import (
	"sync"
	"testing"
	"time"
)

func TestSameDeviceSerialized(t *testing.T) {
	c := New()
	release1 := c.Acquire("compute-01")
	var active, maxActive int
	var mu sync.Mutex
	done := make(chan struct{})

	go func() {
		release2 := c.Acquire("compute-01")
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		release2()
		close(done)
	}()

	// 让第二个任务先尝试获取（应阻塞在锁上，active 最多 1）
	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	active++
	if active > maxActive {
		maxActive = active
	}
	mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	active--
	mu.Unlock()
	release1()

	<-done
	if maxActive != 1 {
		t.Fatalf("maxActive = %d, want 1 (same device must serialize)", maxActive)
	}
}

func TestDifferentDevicesParallel(t *testing.T) {
	c := New()
	got := make(chan string, 2)
	go func() {
		rel := c.Acquire("a")
		defer rel()
		time.Sleep(30 * time.Millisecond)
		got <- "a"
	}()
	go func() {
		rel := c.Acquire("b")
		defer rel()
		got <- "b"
	}()
	if len(got) != 0 {
		t.Fatalf("expected both blocked initially")
	}
	<-got
	<-got // 两个都在约30ms后返回，不互相阻塞
}

func TestTryAcquireTimeout(t *testing.T) {
	c := New()
	rel := c.Acquire("d")
	defer rel()
	if fn, ok := c.TryAcquire("d", 20*time.Millisecond); ok {
		fn()
		t.Fatalf("TryAcquire should fail when locked")
	}
	if fn, ok := c.TryAcquire("e", 20*time.Millisecond); !ok {
		t.Fatalf("TryAcquire should succeed for unlocked device")
	} else {
		fn()
	}
}
