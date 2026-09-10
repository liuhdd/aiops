package kb

import (
	"context"
	"testing"
)

type stubDual struct {
	sys, user string
	out       string
}

func (s *stubDual) Complete(_ context.Context, system, user string) (string, error) {
	s.sys = system
	s.user = user
	return s.out, nil
}

func TestAdaptCompleterPassthrough(t *testing.T) {
	d := &stubDual{out: "ok"}
	c := AdaptCompleter(d)
	if IsNilCompleter(c) {
		t.Fatalf("适配后不应为 nil")
	}
	got, err := c.Complete(context.Background(), "hello")
	if err != nil || got != "ok" {
		t.Fatalf("got %q err %v", got, err)
	}
	if d.sys != "" || d.user != "hello" {
		t.Fatalf("透传错误 sys=%q user=%q", d.sys, d.user)
	}
}

func TestAdaptCompleterNil(t *testing.T) {
	if !IsNilCompleter(AdaptCompleter(nil)) {
		t.Fatalf("nil 输入应返回 nil Completer")
	}
	var typedNil *stubDual
	if !IsNilCompleter(AdaptCompleter(typedNil)) {
		t.Fatalf("typed-nil 输入应返回 nil Completer")
	}
}
