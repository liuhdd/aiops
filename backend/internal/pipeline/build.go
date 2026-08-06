package pipeline

import (
	"aiops/internal/coordinator"
	"aiops/internal/llm"
	"aiops/internal/store"
)

func Build(s *store.Store, lc *llm.Client, coord *coordinator.Coordinator) *Engine {
	e := NewEngine(s, []Stage{
		NormalizeStage{},
		NewTriageStage(s),
		ContextStage{},
		EvidenceStage{},
		NewRCAStage(lc),
		NewRecommendStage(lc),
	})
	e.SetCoordinator(coord)
	return e
}
