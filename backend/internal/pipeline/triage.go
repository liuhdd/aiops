package pipeline

import (
	"context"

	"aiops/internal/model"
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
	alerts, err := t.store.ListAlerts()
	if err != nil {
		return err
	}
	// 同 device+metric 已有 completed run => 视为重复
	for _, a := range alerts {
		if a.Device == st.Alert.Device && a.Metric == st.Alert.Metric {
			if run, err := t.store.GetRun("run-" + a.ID); err == nil && run.Status == model.StatusCompleted {
				st.Triage = &model.TriageResult{
					Deduped:  true,
					Proceed:  false,
					Reason:   "近期同类告警已排查过: " + a.ID,
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
