package retrieve

import "context"

type relevanceLink struct{}

func (l *relevanceLink) Kind() string { return "relevance" }

func (l *relevanceLink) Run(_ context.Context, qc *QueryCtx) error {
	if !hasRelevance(qc.Cands) {
		qc.Results = []Result{}
		qc.ShortCircuit = true
	}
	return nil
}
