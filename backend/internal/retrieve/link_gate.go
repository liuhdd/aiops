package retrieve

import "context"

type gateLink struct {
	env   *searchEnv
	impl  string   // floor
	floor *float64 // gate.floor 覆盖；nil 用 env.cfg.RerankFloor
}

func (l *gateLink) Kind() string { return "gate" }

// floorValue 当前生效的精排分下限：变体覆盖优先，缺省回退配置。
func (l *gateLink) floorValue() float64 {
	if l.floor != nil {
		return *l.floor
	}
	return l.env.cfg.RerankFloor
}

func (l *gateLink) Run(_ context.Context, qc *QueryCtx) error {
	if qc.RerankApplied {
		n := len(qc.Cands)
		if n > 20 {
			n = 20
		}
		if n > 0 {
			head := gateB(qc.Cands[:n], l.floorValue())
			qc.Cands = append(head, qc.Cands[n:]...)
		}
	}
	if topK := effectiveTopK(qc.Options); len(qc.Cands) > topK {
		qc.Cands = qc.Cands[:topK]
	}
	qc.Results = make([]Result, 0, len(qc.Cands))
	for _, c := range qc.Cands {
		qc.Results = append(qc.Results, Result{
			DocID: c.Chunk.DocID, Title: c.Chunk.Title, Category: c.Chunk.Category,
			Heading: c.Chunk.Heading, Content: c.Chunk.Content, Score: c.Final,
			Intents: c.Chunk.Intents, SourceDocIDs: c.Chunk.SourceDocIDs,
		})
	}
	return nil
}
