package retrieve

import "context"

type gateLink struct {
	env  *searchEnv
	impl string // floor
}

func (l *gateLink) Kind() string { return "gate" }

func (l *gateLink) Run(_ context.Context, qc *QueryCtx) error {
	if qc.RerankApplied {
		n := len(qc.Cands)
		if n > 20 {
			n = 20
		}
		if n > 0 {
			head := gateB(qc.Cands[:n], l.env.cfg.RerankFloor)
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
