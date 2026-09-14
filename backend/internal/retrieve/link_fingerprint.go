package retrieve

import "context"

type fingerprintLink struct {
	env  *searchEnv
	impl string // exact | off
}

func (l *fingerprintLink) Kind() string { return "fingerprint" }

func (l *fingerprintLink) Run(_ context.Context, qc *QueryCtx) error {
	if l.impl == "off" {
		return nil
	}
	res, hit, err := l.env.fingerprintFastPath(qc.Raw, qc.Category, qc.IsManual, effectiveTopK(qc.Options))
	if err != nil {
		return nil // 快路径失败静默降级为未命中
	}
	if hit {
		qc.Fallback = res
		qc.Results = res
		qc.ShortCircuit = true
	}
	return nil
}
