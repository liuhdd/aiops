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

// TraceSink 供评测收集每环节轨迹（可选）。
type TraceSink func(traces []LinkTrace)

type traceSinkKey struct{}

// WithTraceSink 把轨迹收集回调注入 ctx；检索完成后回调一次完整轨迹。
func WithTraceSink(ctx context.Context, sink TraceSink) context.Context {
	return context.WithValue(ctx, traceSinkKey{}, sink)
}

// traceSinkFromCtx 取 ctx 上的轨迹回调；缺省 nil。
func traceSinkFromCtx(ctx context.Context) TraceSink {
	if sink, ok := ctx.Value(traceSinkKey{}).(TraceSink); ok {
		return sink
	}
	return nil
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
