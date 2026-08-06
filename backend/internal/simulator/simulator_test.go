package simulator

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"aiops/internal/model"
	"aiops/internal/store"
)

func TestTickGeneratesAlerts(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	sim := New(s, nil)
	a := sim.Tick()
	if a.ID == "" || a.Device == "" || a.Metric == "" {
		t.Fatalf("tick alert incomplete: %+v", a)
	}
	b := sim.Tick()
	if a.ID == b.ID {
		t.Fatalf("ids should differ")
	}
}

func TestStartPushesAlerts(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	var (
		mu  sync.Mutex
		got []model.Alert
	)
	sim := New(s, func(a model.Alert) {
		mu.Lock()
		got = append(got, a)
		mu.Unlock()
	})
	ctx, cancel := context.WithCancel(context.Background())
	sim.Start(ctx, 10*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	cancel()
	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 {
		t.Fatalf("no alerts pushed")
	}
}
