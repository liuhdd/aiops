package scope

import (
	"errors"
	"testing"
)

func TestEncode(t *testing.T) {
	cases := []struct {
		sc   Scope
		want string
	}{
		{Global(), "global"},
		{Device("compute-01"), "device:compute-01"},
		{Task("run-alarm-1", "compute-01"), "task:run-alarm-1"},
	}
	for _, c := range cases {
		if got := c.sc.Encode(); got != c.want {
			t.Fatalf("Encode(%+v) = %q, want %q", c.sc, got, c.want)
		}
	}
}

func TestParseRoundTrip(t *testing.T) {
	for _, s := range []string{"global", "device:compute-01", "task:run-alarm-1"} {
		sc, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		if sc.Encode() != s {
			t.Fatalf("roundtrip %q -> %q", s, sc.Encode())
		}
	}
}

func TestParseUnknown(t *testing.T) {
	if _, err := Parse("tenant:foo"); err == nil {
		t.Fatalf("expected error for unknown scope")
	}
}

func TestErrScopeDeniedIsComparable(t *testing.T) {
	if !errors.Is(ErrScopeDenied, ErrScopeDenied) {
		t.Fatalf("ErrScopeDenied must be sentinel")
	}
}
