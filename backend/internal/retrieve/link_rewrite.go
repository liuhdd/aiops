package retrieve

import "context"

type rewriteLink struct {
	env       *searchEnv
	completer Completer
	impl      string // passthrough | llm
}

func (l *rewriteLink) Kind() string { return "rewrite" }

func (l *rewriteLink) Run(ctx context.Context, qc *QueryCtx) error {
	if l.impl == "llm" && len(qc.Raw.History) > 0 && !IsNilCompleter(l.completer) {
		if out, err := RewriteQuery(ctx, l.completer, qc.Raw.History, qc.Text); err == nil && out != "" {
			qc.Text = out
		}
	}
	return nil
}
