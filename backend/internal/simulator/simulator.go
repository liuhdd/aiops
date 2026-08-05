package simulator

import (
	"context"
	"sync"
	"time"

	"aiops/internal/model"
	"aiops/internal/store"
)

type AlertTemplate struct {
	Device   string
	Metric   string
	Severity string
	Message  string
}

var Templates = []AlertTemplate{
	{Device: "compute-01", Metric: "cpu_usage", Severity: "warning", Message: "CPU使用率持续高于85%"},
	{Device: "db-01", Metric: "disk_usage", Severity: "critical", Message: "磁盘使用率超过95%"},
	{Device: "compute-02", Metric: "gpu_util", Severity: "critical", Message: "GPU掉卡，利用率归零"},
	{Device: "web-01", Metric: "mem_usage", Severity: "warning", Message: "内存使用率持续高于90%"},
	{Device: "switch-core-01", Metric: "net_in", Severity: "critical", Message: "核心交换机入口流量中断"},
}

type Simulator struct {
	store   *store.Store
	onAlert func(model.Alert)
	index   int
	seq     int
	mu      sync.Mutex
	cancel  context.CancelFunc
}

func New(s *store.Store, onAlert func(model.Alert)) *Simulator {
	return &Simulator{store: s, onAlert: onAlert}
}

func (sim *Simulator) Tick() model.Alert {
	sim.mu.Lock()
	defer sim.mu.Unlock()
	t := Templates[sim.index%len(Templates)]
	sim.index++
	sim.seq++
	now := time.Now()
	a := model.Alert{
		ID:        model.NewID("alarm"),
		Device:    t.Device,
		Metric:    t.Metric,
		Severity:  t.Severity,
		Message:   t.Message,
		Status:    "active",
		FiredAt:   now,
		CreatedAt: now,
	}
	_ = sim.store.SaveAlert(a)
	if sim.onAlert != nil {
		sim.onAlert(a)
	}
	return a
}

func (sim *Simulator) Start(ctx context.Context, interval time.Duration) {
	ctx, sim.cancel = context.WithCancel(ctx)
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				sim.Tick()
			}
		}
	}()
}

func (sim *Simulator) Stop() {
	if sim.cancel != nil {
		sim.cancel()
	}
}
