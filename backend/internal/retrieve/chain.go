package retrieve

import (
	"context"
	"fmt"
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

// BuildChain 按 CanonicalKinds 顺序装配；未指定用 baseline 默认。
// 已注册的 none/off（fingerprint/off、rerank/none）保留，由 Link 自身处理禁用态，
// 以复刻重构前链路；未注册的 none/off 视为跳过该环节；其余未注册实现报错。
func BuildChain(v Variant, deps Deps) (*Chain, error) {
	for k := range v.Steps {
		if !canonicalKind(k) {
			return nil, fmt.Errorf("未知环节 %q", k)
		}
	}
	base := BaselineVariant()
	steps := base.Steps
	for k, impl := range v.Steps {
		steps[k] = impl
	}
	var links []chainLink
	for _, kind := range CanonicalKinds {
		impl := steps[kind]
		f, ok := linkRegistry[linkKey{kind, impl}]
		if !ok {
			if impl == "none" || impl == "off" {
				continue
			}
			return nil, fmt.Errorf("未注册的实现 %s/%s", kind, impl)
		}
		links = append(links, chainLink{impl: impl, link: f(deps)})
	}
	return &Chain{links: links}, nil
}
