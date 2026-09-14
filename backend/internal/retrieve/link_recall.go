package retrieve

import "context"

type recallLink struct {
	env  *searchEnv
	impl string // hybrid
}

func (l *recallLink) Kind() string { return "recall" }

func (l *recallLink) Run(ctx context.Context, qc *QueryCtx) error {
	if err := l.env.ensureIndex(qc.Raw.Version); err != nil {
		return err
	}
	q := qc.Raw
	q.Text = qc.Text
	qc.Cands = l.env.dualRecall(ctx, q, qc.IsManual, qc.Category)
	return nil
}
