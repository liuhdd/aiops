package pipeline

import (
	"context"
	"errors"

	"aiops/internal/model"
	"aiops/internal/scope"
	"aiops/internal/store"
)

type TriageStage struct {
	store *store.Store
}

func NewTriageStage(s *store.Store) *TriageStage {
	return &TriageStage{store: s}
}

func (TriageStage) StageID() string { return "triage" }
func (t *TriageStage) Name() string { return "过滤分诊" }

func (t *TriageStage) Run(_ context.Context, st *State) error {
	ss := st.Store
	if ss == nil {
		if t.store == nil {
			return errors.New("triage: no store available")
		}
		ss = store.NewScoped(t.store, scope.Global())
	}
	alerts, err := ss.ListAlerts()
	if err != nil {
		return err
	}
	// 同 device+metric 已有 completed run => 视为重复
	for _, a := range alerts {
		if a.Device == st.Alert.Device && a.Metric == st.Alert.Metric {
			if run, err := ss.GetRun("run-" + a.ID); err == nil && run.Status == model.StatusCompleted {
				st.Triage = &model.TriageResult{
					Deduped: true,
					Proceed: false,
					Reason:  "近期同类告警已排查过: " + a.ID,
				}
				return nil
			}
		}
	}
	priority := map[string]string{"critical": "P1", "warning": "P2", "info": "P3"}[st.Alert.Severity]
	if priority == "" {
		priority = "P3"
	}
	st.Triage = &model.TriageResult{
		Deduped:  false,
		Proceed:  true,
		Priority: priority,
		Reason:   "新告警，进入排查",
	}
	return nil
}
