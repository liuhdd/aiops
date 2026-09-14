package retrieve

import (
	"context"
	"time"
)

type chainLink struct {
	impl string
	link Link
}

// Chain 按固定顺序装配的 Link 管道；ShortCircuit 命中即停止后续。
type Chain struct {
	links []chainLink
}

// Run 依序执行 Link 并记录 LinkTrace；Link 返回错误则中断并向上传递。
func (c *Chain) Run(ctx context.Context, qc *QueryCtx) error {
	for _, cl := range c.links {
		if qc.ShortCircuit {
			return nil
		}
		start := time.Now()
		err := cl.link.Run(ctx, qc)
		qc.Trace = append(qc.Trace, LinkTrace{
			Kind: cl.link.Kind(), Impl: cl.impl,
			DurationMS: time.Since(start).Milliseconds(),
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func baselineChain(env *searchEnv) *Chain {
	cfg := env.cfg
	fp, fuse, rr := "exact", "rrf", "openai"
	if !cfg.RetrieveFingerprint {
		fp = "off"
	}
	if !cfg.RetrieveRRF {
		fuse = "weighted"
	}
	if env.reranker == nil {
		rr = "none"
	}
	return &Chain{links: []chainLink{
		{impl: fp, link: &fingerprintLink{env: env, impl: fp}},
		{impl: "passthrough", link: &rewriteLink{env: env, impl: "passthrough"}},
		{impl: "hybrid", link: &recallLink{env: env, impl: "hybrid"}},
		{impl: fuse, link: &fuseLink{env: env, impl: fuse}},
		{impl: "default", link: &relevanceLink{}},
		{impl: rr, link: &rerankLink{env: env, impl: rr}},
		{impl: "floor", link: &gateLink{env: env, impl: "floor"}},
	}}
}
