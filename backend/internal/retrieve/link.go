package retrieve

import (
	"context"

	"aiops/internal/model"
)

// QueryCtx 链路环节间共享的受控中间态；每环节只读写自己的段。
type QueryCtx struct {
	Raw      Query
	Text     string
	Options  Options
	Category model.KBCategory
	IsManual bool

	ShortCircuit  bool
	Fallback      []Result
	Cands         []Candidate
	Results       []Result
	RerankApplied bool
	Trace         []LinkTrace
}

// LinkTrace 单环节运行痕迹，供评测报告逐环节对比。
type LinkTrace struct {
	Kind       string
	Impl       string
	DurationMS int64
	Hit        bool
	Note       string
}

// Link 链路环节；Kind 标识槽位。
type Link interface {
	Kind() string
	Run(ctx context.Context, qc *QueryCtx) error
}

func effectiveTopK(opts Options) int {
	if opts.TopK > 0 {
		return opts.TopK
	}
	return 3
}
