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
