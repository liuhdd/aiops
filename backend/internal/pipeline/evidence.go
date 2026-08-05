package pipeline

import (
	"context"
	"time"

	"aiops/internal/mockdata"
	"aiops/internal/model"
)

type EvidenceStage struct{}

func (EvidenceStage) StageID() string { return "evidence" }
func (EvidenceStage) Name() string    { return "证据收集" }

func (EvidenceStage) Run(_ context.Context, st *State) error {
	from := st.Alert.FiredAt.Add(-2 * time.Hour)
	to := time.Now()
	metrics, _ := mockdata.QueryMetric(st.Alert.Device, st.Alert.Metric, from, to)
	logs, _ := mockdata.SearchLogs(st.Alert.Device, st.SuggestLogs, from, to)
	gaps := []string{}
	if len(logs) == 0 {
		gaps = append(gaps, "该时间窗内未检索到关键字日志")
	}
	st.Evidence = &model.Evidence{
		Metrics: metrics,
		Logs:    logs,
		Gaps:    gaps,
	}
	return nil
}
