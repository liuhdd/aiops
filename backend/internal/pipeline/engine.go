package pipeline

import (
	"context"
	"time"

	"aiops/internal/model"
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
}

type Stage interface {
	StageID() string
	Name() string
	Run(ctx context.Context, st *State) error
}

type Engine struct {
	store  *store.Store
	stages []Stage
}

func NewEngine(s *store.Store, stages []Stage) *Engine {
	return &Engine{store: s, stages: stages}
}

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

	st := &State{
		Run:       run,
		Alert:     alert,
		Iteration: 0,
		RawAlerts: input,
	}
	st.SuggestLogs = suggestLogs(alert.Metric)

	degraded := false
	skipRest := false

	// 外层循环 = 回溯次数。每一轮从头重跑全部阶段，
	// RCA 若仍有 gaps 且还有额度则进入下一轮（回溯），否则结束。
	for iteration := 0; iteration <= run.MaxIteration; iteration++ {
		st.Iteration = iteration
		backtrack := false
		for _, stage := range e.stages {
			if skipRest {
				break
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
			if err := e.store.SaveStage(res); err != nil {
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
			}
			if err := e.store.SaveStage(res); err != nil {
				return err
			}
			// 分诊阶段 decide skip
			if stage.StageID() == "triage" && st.Triage != nil && !st.Triage.Proceed {
				skipRest = true
				break
			}
			// 回溯判定：rca 有 gaps 且还有迭代额度
			if stage.StageID() == "rca" && st.RCA != nil && len(st.RCA.Gaps) > 0 && iteration < run.MaxIteration {
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
	if err := e.store.UpdateRunStatus(run.ID, status); err != nil {
		return err
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
