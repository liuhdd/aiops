package retrieve

import (
	"context"
	"testing"

	"aiops/internal/config"
)

type stubLink struct {
	kind   string
	called bool
	run    func(*QueryCtx)
}

func (s *stubLink) Kind() string { return s.kind }
func (s *stubLink) Run(_ context.Context, qc *QueryCtx) error {
	s.called = true
	if s.run != nil {
		s.run(qc)
	}
	return nil
}

func TestChainRunsLinksInOrderAndStopsOnShortCircuit(t *testing.T) {
	a := &stubLink{kind: "recall"}
	b := &stubLink{kind: "fuse", run: func(qc *QueryCtx) { qc.ShortCircuit = true }}
	c := &stubLink{kind: "rerank"}
	ch := &Chain{links: []chainLink{{impl: "a", link: a}, {impl: "b", link: b}, {impl: "c", link: c}}}
	if err := ch.Run(context.Background(), &QueryCtx{}); err != nil {
		t.Fatal(err)
	}
	if !a.called || !b.called || c.called {
		t.Fatalf("短路失败 a=%v b=%v c=%v", a.called, b.called, c.called)
	}
}

func TestEffectiveTopK(t *testing.T) {
	if effectiveTopK(Options{}) != 3 || effectiveTopK(Options{TopK: 5}) != 5 {
		t.Fatal("effectiveTopK 错误")
	}
}

func TestBaselineChainHasAllKinds(t *testing.T) {
	ch, err := BuildChain(BaselineVariant(), Deps{Env: newSearchEnv(nil, nil, nil, config.Default())})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, l := range ch.links {
		got[l.link.Kind()] = true
	}
	for _, k := range CanonicalKinds {
		if !got[k] {
			t.Fatalf("baseline 缺少 %q", k)
		}
	}
}

// gateLinkOf 取出链中的 gate Link，供参数覆盖行为断言。
func gateLinkOf(t *testing.T, ch *Chain) *gateLink {
	t.Helper()
	for _, cl := range ch.links {
		if g, ok := cl.link.(*gateLink); ok {
			return g
		}
	}
	t.Fatal("链中缺少 gate Link")
	return nil
}

func TestBuildChainRejectsUnknownParam(t *testing.T) {
	v := BaselineVariant()
	v.Params = map[string]map[string]string{"recall": {"foo": "1"}}
	if _, err := BuildChain(v, Deps{Env: newSearchEnv(nil, nil, nil, config.Default())}); err == nil {
		t.Fatal("未知 knob 参数应报错（fail-fast）")
	}
}

func TestBuildChainRejectsUnknownParamKind(t *testing.T) {
	v := BaselineVariant()
	v.Params = map[string]map[string]string{"nope": {"floor": "0.5"}}
	if _, err := BuildChain(v, Deps{Env: newSearchEnv(nil, nil, nil, config.Default())}); err == nil {
		t.Fatal("未知环节参数应报错（fail-fast）")
	}
}

func TestBuildChainRejectsInvalidGateFloor(t *testing.T) {
	for _, raw := range []string{"0", "-0.1", "1.5", "abc"} {
		v := BaselineVariant()
		v.Params = map[string]map[string]string{"gate": {"floor": raw}}
		if _, err := BuildChain(v, Deps{Env: newSearchEnv(nil, nil, nil, config.Default())}); err == nil {
			t.Fatalf("非法 gate.floor %q 应报错", raw)
		}
	}
}

func TestGateFloorParamOverridesConfig(t *testing.T) {
	cfg := config.Default()
	cfg.RerankFloor = 0.2
	env := newSearchEnv(nil, nil, nil, cfg)

	// 覆盖：gate.floor=0.9，低于 0.9 的候选应被剔除
	v := BaselineVariant()
	v.Params = map[string]map[string]string{"gate": {"floor": "0.9"}}
	ch, err := BuildChain(v, Deps{Env: env})
	if err != nil {
		t.Fatal(err)
	}
	qc := &QueryCtx{RerankApplied: true, Options: Options{TopK: 10}, Cands: []Candidate{{Final: 0.5}, {Final: 0.95}}}
	if err := gateLinkOf(t, ch).Run(context.Background(), qc); err != nil {
		t.Fatal(err)
	}
	if len(qc.Cands) != 1 || qc.Cands[0].Final != 0.95 {
		t.Fatalf("gate.floor 覆盖未生效: %+v", qc.Cands)
	}

	// 默认：无 params，仍用 cfg.RerankFloor=0.2
	ch2, err := BuildChain(BaselineVariant(), Deps{Env: env})
	if err != nil {
		t.Fatal(err)
	}
	qc2 := &QueryCtx{RerankApplied: true, Options: Options{TopK: 10}, Cands: []Candidate{{Final: 0.15}, {Final: 0.25}}}
	if err := gateLinkOf(t, ch2).Run(context.Background(), qc2); err != nil {
		t.Fatal(err)
	}
	if len(qc2.Cands) != 1 || qc2.Cands[0].Final != 0.25 {
		t.Fatalf("默认 floor 行为异常: %+v", qc2.Cands)
	}
}
