package pipeline

import (
	"context"
	"strings"
	"time"

	ctxpkg "aiops/internal/context"
	"aiops/internal/coordinator"
	"aiops/internal/model"
	"aiops/internal/scope"
	"aiops/internal/store"
)

type State struct {
	Run            model.PipelineRun
	Alert          model.Alert
	Triage         *model.TriageResult
	Context        *model.Context
	Evidence       *model.Evidence
	RCA            *model.RCA
	Recommendation *model.Recommendation
	Iteration      int
	RawAlerts      map[string]string
	SuggestLogs    []string
	Scope          *scope.Scope // 本任务的嵌套作用域
	Store          *store.ScopedStore
	DeviceContext  *model.DeviceContext
	Ctx            *ctxpkg.Engine
	Degraded       bool
}

func (st *State) EvidenceGaps() []string {
	if st.Evidence == nil {
		return nil
	}
	return st.Evidence.Gaps
}

type Stage interface {
	StageID() string
	Name() string
	Run(ctx context.Context, st *State) error
}

type Engine struct {
	store    *store.Store
	stages   []Stage
	Publish  func(eventType string, data any)
	coord    *coordinator.Coordinator
	ctxStore *ctxpkg.Store
	tools    *ctxpkg.Registry
	react    bool
	topK     int
}

func NewEngine(s *store.Store, stages []Stage) *Engine {
	return &Engine{store: s, stages: stages}
}

func (e *Engine) SetCoordinator(c *coordinator.Coordinator) { e.coord = c }

func (e *Engine) Stages() []Stage { return e.stages }

func (e *Engine) Run(ctx context.Context, alert model.Alert, input map[string]string) error {
	if input == nil {
		input = map[string]string{}
	}
	now := time.Now()
	run := model.PipelineRun{
		ID:           "run-" + alert.ID,
		AlertID:      alert.ID,
		Status:       model.StatusRunning,
		Iteration:    0,
		MaxIteration: 2,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := e.store.SaveRun(run); err != nil {
		return err
	}

	if e.coord != nil {
		const lockTimeout = 30 * time.Second
		release, ok := e.coord.TryAcquire(alert.Device, lockTimeout)
		if !ok {
			_ = e.store.UpdateRunStatus(run.ID, model.StatusDegraded)
			run.Status = model.StatusDegraded
			if e.Publish != nil {
				e.Publish("run", run)
			}
			return nil
		}
		defer release()
	}

	taskScope := scope.Task(run.ID, alert.Device)
	st := &State{
		Run:       run,
		Alert:     alert,
		Iteration: 0,
		RawAlerts: input,
		Scope:     &taskScope,
		Store:     store.NewScoped(e.store, taskScope),
	}
	if e.ctxStore != nil {
		st.Ctx = ctxpkg.NewEngine(e.ctxStore, e.tools, run.ID, alert.Device, alert.Metric, e.react, e.topK)
	}
	st.SuggestLogs = suggestLogs(alert.Metric)

	degraded := false
	skipRest := false

	evidenceIndex := -1
	for i, stage := range e.stages {
		if stage.StageID() == "evidence" {
			evidenceIndex = i
			break
		}
	}

	// 外层循环 = 回溯次数。首轮重跑全部阶段，
	// 回溯轮重跑 evidence 起的所有阶段（evidence/rca/recommend），
	// RCA 若仍有 gaps 且还有额度则进入下一轮（回溯），否则结束。
	for iteration := 0; iteration <= run.MaxIteration; iteration++ {
		st.Iteration = iteration
		backtrack := false
		for stageIdx, stage := range e.stages {
			if skipRest {
				break
			}
			if iteration > 0 && stageIdx < evidenceIndex {
				continue
			}
			res := model.StageResult{
				ID:        model.NewID("st"),
				RunID:     run.ID,
				StageID:   stage.StageID(),
				StageName: stage.Name(),
				Status:    model.StageRunning,
				Iteration: iteration,
				StartedAt: time.Now(),
			}
			if err := st.Store.SaveStage(res); err != nil {
				return err
			}
			stageErr := stage.Run(ctx, st)
			res.EndedAt = time.Now()
			if stageErr != nil {
				degraded = true
				res.Status = model.StageFailed
				res.Error = stageErr.Error()
			} else {
				res.Status = model.StageCompleted
				res.Output = stageOutput(st, stage.StageID())
				if st.Ctx != nil {
					_ = st.Ctx.AddScratchpad(stage.StageID(), stageSummary(st, stage.StageID()), "")
				}
			}
			if err := st.Store.SaveStage(res); err != nil {
				return err
			}
			if e.Publish != nil {
				e.Publish("stage", res)
			}
			if st.Degraded {
				degraded = true
			}
			// 分诊阶段 decide skip
			if stage.StageID() == "triage" && st.Triage != nil && !st.Triage.Proceed {
				skipRest = true
				break
			}
			// 回溯判定：rca 有 gaps 且还有迭代额度（已降级则不再回溯）
			if stage.StageID() == "rca" && st.RCA != nil && len(st.RCA.Gaps) > 0 && !st.Degraded && iteration < run.MaxIteration {
				backtrack = true
				break
			}
		}
		if skipRest {
			break
		}
		if !backtrack {
			break
		}
		// 下一轮：进入回溯（continue 会从 iteration+1 重新开始）
	}

	status := model.StatusCompleted
	if skipRest {
		status = model.StatusSkipped
	} else if degraded {
		status = model.StatusDegraded
	}
	if err := st.Store.UpdateRunStatus(run.ID, status); err != nil {
		return err
	}
	run.Status = status
	if e.Publish != nil {
		e.Publish("run", run)
	}
	if st.Ctx != nil && st.RCA != nil {
		_ = st.Ctx.SaveLongTermMemory(st.RCA, st.Recommendation)
	}
	return nil
}

func stageOutput(st *State, stageID string) string {
	switch stageID {
	case "normalize":
		return model.MustJSON(st.Alert)
	case "triage":
		return model.MustJSON(st.Triage)
	case "context":
		return model.MustJSON(st.Context)
	case "evidence":
		return model.MustJSON(st.Evidence)
	case "rca":
		return model.MustJSON(st.RCA)
	case "recommend":
		return model.MustJSON(st.Recommendation)
	}
	return "{}"
}

func suggestLogs(metric string) []string {
	switch metric {
	case "cpu_usage":
		return []string{"load", "cpu"}
	case "disk_usage":
		return []string{"no space", "error"}
	case "gpu_util":
		return []string{"Xid", "NVLink", "nvidia"}
	case "mem_usage":
		return []string{"oom", "OutOfMemory", "memory"}
	case "net_in":
		return []string{"link", "down", "transceiver"}
	}
	return nil
}

func stageSummary(st *State, stageID string) string {
	switch stageID {
	case "triage":
		if st.Triage != nil {
			if st.Triage.Proceed {
				return "优先级 " + st.Triage.Priority + "，进入排查"
			}
			return "已去重跳过: " + st.Triage.Reason
		}
	case "context":
		if st.Context != nil && st.Context.DeviceInfo.Name != "" {
			return "设备 " + st.Context.DeviceInfo.Name + " 快照已收集"
		}
	case "rca":
		if st.RCA != nil {
			return "根因: " + st.RCA.RootCause
		}
	case "recommend":
		if st.Recommendation != nil {
			return "处置: " + strings.Join(st.Recommendation.Actions, "；")
		}
	}
	return "阶段完成"
}
