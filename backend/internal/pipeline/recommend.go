package pipeline

import (
	"context"
	"fmt"

	"aiops/internal/llm"
	"aiops/internal/mockdata"
	"aiops/internal/model"
)

type RecommendStage struct {
	llm *llm.Client
}

func NewRecommendStage(lc *llm.Client) *RecommendStage { return &RecommendStage{llm: lc} }

func (RecommendStage) StageID() string { return "recommend" }
func (RecommendStage) Name() string    { return "处置建议" }

func (s *RecommendStage) Run(ctx context.Context, st *State) error {
	steps, ok := mockdata.SearchRunbook(st.Alert.Metric)
	if !ok {
		steps = []string{"联系人工介入排查"}
	}
	actions := steps
	if s.llm != nil {
		system := "你是算力中心运维助手。基于根因和预案，给出简明的处置建议步骤列表。只输出JSON：{\"actions\":[string],\"runbook_ref\":string,\"risk\":string}"
		root := ""
		if st.RCA != nil {
			root = st.RCA.RootCause
		}
		user := fmt.Sprintf("根因:%s\n预案:%v", root, steps)
		if raw, err := s.llm.Complete(ctx, system, user); err == nil {
			var rec model.Recommendation
			if llm.ParseJSON(raw, &rec) == nil && len(rec.Actions) > 0 {
				st.Recommendation = &rec
				return nil
			}
		}
	}
	st.Recommendation = &model.Recommendation{
		Actions:    actions,
		RunbookRef: st.Alert.Metric,
		Risk:       "低",
	}
	return nil
}
