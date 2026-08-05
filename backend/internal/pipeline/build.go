package pipeline

import (
	"aiops/internal/llm"
	"aiops/internal/store"
)

func Build(s *store.Store, lc *llm.Client) *Engine {
	return NewEngine(s, []Stage{
		NormalizeStage{},
		NewTriageStage(s),
		ContextStage{},
		EvidenceStage{},
		NewRCAStage(lc),
		NewRecommendStage(lc),
	})
}
