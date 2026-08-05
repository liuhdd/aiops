package pipeline

import (
	"path/filepath"
	"testing"

	"aiops/internal/store"
)

func TestBuildHasSixStages(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	e := Build(s, nil)
	if len(e.Stages()) != 6 {
		t.Fatalf("stages = %d, want 6", len(e.Stages()))
	}
}
