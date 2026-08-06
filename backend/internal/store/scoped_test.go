package store

import (
	"errors"
	"testing"
	"time"

	"aiops/internal/model"
	"aiops/internal/scope"
)

func TestScopedTaskWriteOwnRunAllowed(t *testing.T) {
	s := newTestStore(t)
	sc := scope.Task("run-1", "compute-01")
	ss := NewScoped(s, sc)
	now := time.Now()
	st := model.StageResult{ID: "st-1", RunID: "run-1", StageID: "rca", StageName: "根因分析",
		Status: model.StageCompleted, Output: "{}", StartedAt: now, EndedAt: now}
	if err := ss.SaveStage(st); err != nil {
		t.Fatalf("SaveStage own run: %v", err)
	}
}

func TestScopedTaskWriteOtherRunDenied(t *testing.T) {
	s := newTestStore(t)
	sc := scope.Task("run-1", "compute-01")
	ss := NewScoped(s, sc)
	now := time.Now()
	st := model.StageResult{ID: "st-2", RunID: "run-999", StageID: "rca", StageName: "根因分析",
		Status: model.StageCompleted, Output: "{}", StartedAt: now, EndedAt: now}
	if err := ss.SaveStage(st); !errors.Is(err, scope.ErrScopeDenied) {
		t.Fatalf("SaveStage other run err = %v, want ErrScopeDenied", err)
	}
}

func TestScopedTaskListStagesOtherRunDenied(t *testing.T) {
	s := newTestStore(t)
	ss := NewScoped(s, scope.Task("run-1", "compute-01"))
	if _, err := ss.ListStages("run-999"); !errors.Is(err, scope.ErrScopeDenied) {
		t.Fatalf("ListStages other run err = %v, want ErrScopeDenied", err)
	}
}

func TestScopedListAlertsDeviceFilter(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	_ = s.SaveAlert(model.Alert{ID: "alarm-1", Device: "compute-01", Metric: "cpu_usage", Status: "active", FiredAt: now, CreatedAt: now})
	_ = s.SaveAlert(model.Alert{ID: "alarm-2", Device: "db-01", Metric: "disk_usage", Status: "active", FiredAt: now, CreatedAt: now})

	taskSS := NewScoped(s, scope.Task("run-1", "compute-01"))
	list, err := taskSS.ListAlerts()
	if err != nil || len(list) != 1 || list[0].ID != "alarm-1" {
		t.Fatalf("task ListAlerts = %+v err=%v, want only compute-01", list, err)
	}

	globalSS := NewScoped(s, scope.Global())
	list, err = globalSS.ListAlerts()
	if err != nil || len(list) != 2 {
		t.Fatalf("global ListAlerts len = %d, want 2", len(list))
	}
}

func TestScopedGetAlertOtherDeviceDenied(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	_ = s.SaveAlert(model.Alert{ID: "alarm-1", Device: "compute-01", Metric: "cpu_usage", Status: "active", FiredAt: now, CreatedAt: now})
	ss := NewScoped(s, scope.Task("run-1", "db-01"))
	if _, err := ss.GetAlert("alarm-1"); !errors.Is(err, scope.ErrScopeDenied) {
		t.Fatalf("GetAlert other device err = %v, want ErrScopeDenied", err)
	}
}

func TestScopedGetRunReadUpward(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	_ = s.SaveAlert(model.Alert{ID: "alarm-1", Device: "compute-01", Metric: "cpu_usage", Status: "active", FiredAt: now, CreatedAt: now})
	_ = s.SaveRun(model.PipelineRun{ID: "run-1", AlertID: "alarm-1", Status: model.StatusCompleted,
		Iteration: 0, MaxIteration: 2, CreatedAt: now, UpdatedAt: now})
	// 同设备任务可读 run（读向上）
	ss := NewScoped(s, scope.Task("run-2", "compute-01"))
	if _, err := ss.GetRun("run-1"); err != nil {
		t.Fatalf("GetRun read-upward same device err = %v", err)
	}
	// 他设备任务读 run 被拒
	other := NewScoped(s, scope.Task("run-3", "db-01"))
	if _, err := other.GetRun("run-1"); !errors.Is(err, scope.ErrScopeDenied) {
		t.Fatalf("GetRun other device err = %v, want ErrScopeDenied", err)
	}
}

func TestScopedDeviceContextPermission(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	_ = s.SaveDeviceContext(model.DeviceContext{DeviceID: "compute-01", StateJSON: `{}`, UpdatedBy: "x", UpdatedAt: now})

	ok := NewScoped(s, scope.Task("run-1", "compute-01"))
	if _, err := ok.GetDeviceContext("compute-01"); err != nil {
		t.Fatalf("GetDeviceContext own device err = %v", err)
	}
	denied := NewScoped(s, scope.Task("run-1", "db-01"))
	if _, err := denied.GetDeviceContext("compute-01"); !errors.Is(err, scope.ErrScopeDenied) {
		t.Fatalf("GetDeviceContext other device err = %v, want ErrScopeDenied", err)
	}
	if err := denied.UpdateDeviceContext(model.DeviceContext{DeviceID: "compute-01", StateJSON: `{}`, UpdatedBy: "r", UpdatedAt: now}); !errors.Is(err, scope.ErrScopeDenied) {
		t.Fatalf("UpdateDeviceContext other device err = %v, want ErrScopeDenied", err)
	}
}
