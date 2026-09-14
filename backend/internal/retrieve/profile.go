package retrieve

import "context"

type profileKey struct{}
type historyKey struct{}

// WithProfile 在 ctx 上标注检索路径（pipeline/chat），供工具等无 Options 的入口取用。
func WithProfile(ctx context.Context, p string) context.Context {
	return context.WithValue(ctx, profileKey{}, p)
}

// ProfileFromCtx 取路径标识；缺省 "pipeline"。
func ProfileFromCtx(ctx context.Context) string {
	if p, ok := ctx.Value(profileKey{}).(string); ok && p != "" {
		return p
	}
	return "pipeline"
}

// WithHistory 在 ctx 上挂对话历史，供 ReAct 工具路径透传到链路改写环节。
func WithHistory(ctx context.Context, h []string) context.Context {
	return context.WithValue(ctx, historyKey{}, h)
}

// HistoryFromCtx 取 ctx 上的对话历史；缺省 nil。
func HistoryFromCtx(ctx context.Context) []string {
	if h, ok := ctx.Value(historyKey{}).([]string); ok {
		return h
	}
	return nil
}

// ProfileOf 解析顺序：Options.Profile → ctx → "pipeline"。
func ProfileOf(ctx context.Context, opts Options) string {
	if opts.Profile != "" {
		return opts.Profile
	}
	return ProfileFromCtx(ctx)
}
