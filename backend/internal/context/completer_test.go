package context

import (
	"testing"

	"aiops/internal/llm"
)

func TestIsNilCompleter(t *testing.T) {
	var c Completer
	if !IsNilCompleter(c) {
		t.Fatalf("nil interface should be nil")
	}
	var typedNil *llm.Client
	if !IsNilCompleter(typedNil) {
		t.Fatalf("typed-nil *llm.Client should be nil")
	}
	if IsNilCompleter(&scriptedCompleter{}) {
		t.Fatalf("non-nil completer should not be nil")
	}
}
