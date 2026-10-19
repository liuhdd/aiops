package pipeline

import (
	"path/filepath"
	"testing"

	ctxpkg "aiops/internal/context"
	"aiops/internal/coordinator"
	"aiops/internal/store"
)

func TestBuildHasSixStages(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := ctxpkg.NewStore(s)
	reg := ctxpkg.NewRegistry()
	e := Build(s, nil, coordinator.New(), cs, reg, 3)
	if len(e.Stages()) != 6 {
		t.Fatalf("stages = %d, want 6", len(e.Stages()))
	}
}
