package retrieve

import "context"

type fuseLink struct {
	env  *searchEnv
	impl string // rrf | weighted
}

func (l *fuseLink) Kind() string { return "fuse" }

func (l *fuseLink) Run(_ context.Context, qc *QueryCtx) error {
	if l.impl == "weighted" {
		if qc.IsManual {
			qc.Cands = RankManuals(qc.Cands, l.env.now())
		} else {
			qc.Cands = RankCases(qc.Cands, l.env.now())
		}
		return nil
	}
	qc.Cands = FuseRRF(qc.Cands, qc.IsManual)
	return nil
}
