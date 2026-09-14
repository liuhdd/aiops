package retrieve

import "context"

type rerankLink struct {
	env  *searchEnv
	impl string // openai | none
}

func (l *rerankLink) Kind() string { return "rerank" }

func (l *rerankLink) Run(ctx context.Context, qc *QueryCtx) error {
	if l.impl == "none" || l.env.reranker == nil {
		return nil
	}
	qc.Cands = l.env.applyRerank(ctx, qc.Text, qc.Cands)
	qc.RerankApplied = true
	return nil
}
