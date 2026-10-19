package pipeline

import (
	"aiops/internal/context"
	"aiops/internal/coordinator"
	"aiops/internal/llm"
	"aiops/internal/store"
)

func Build(s *store.Store, lc *llm.Client, coord *coordinator.Coordinator,
	cs *context.Store, reg *context.Registry, topK int) *Engine {
	e := NewEngine(s, []Stage{
		NormalizeStage{},
		NewTriageStage(s),
		ContextStage{},
		EvidenceStage{},
		NewRCAStage(lc),
		NewRecommendStage(lc),
	})
	e.SetCoordinator(coord)
	e.ctxStore = cs
	e.tools = reg
	e.topK = topK
	return e
}
