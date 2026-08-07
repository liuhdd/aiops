package context

import (
	"aiops/internal/model"
	"aiops/internal/scope"
)

type ScopedStore struct {
	cs *Store
	sc scope.Scope
}

func NewScoped(cs *Store, sc scope.Scope) *ScopedStore {
	return &ScopedStore{cs: cs, sc: sc}
}

func (ss *ScopedStore) AddScratchpad(step, content, toolName string) error {
	if ss.sc.Level != scope.LevelTask || ss.sc.TaskID == "" {
		return scope.ErrScopeDenied
	}
	return ss.cs.InsertScratchpad(model.ScratchpadEntry{
		ID: model.NewID("sp"), RunID: ss.sc.TaskID, Step: step,
		Content: content, ToolName: toolName, CreatedAt: nowFunc(),
	})
}

func (ss *ScopedStore) ListScratchpad() ([]model.ScratchpadEntry, error) {
	if ss.sc.Level != scope.LevelTask {
		return nil, scope.ErrScopeDenied
	}
	return ss.cs.ListScratchpad(ss.sc.TaskID)
}

func (ss *ScopedStore) AddToolEvent(ev model.ToolEvent) error {
	if ss.sc.Level != scope.LevelTask || ev.RunID != ss.sc.TaskID {
		return scope.ErrScopeDenied
	}
	return ss.cs.InsertToolEvent(ev)
}

func (ss *ScopedStore) ListToolEvents() ([]model.ToolEvent, error) {
	if ss.sc.Level != scope.LevelTask {
		return nil, scope.ErrScopeDenied
	}
	return ss.cs.ListToolEvents(ss.sc.TaskID)
}
