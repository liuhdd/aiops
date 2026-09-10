package context

import (
	"time"

	"aiops/internal/model"
	"aiops/internal/store"
)

var nowFunc = func() time.Time { return time.Now() }

type Store struct {
	st *store.Store
}

func NewStore(st *store.Store) *Store { return &Store{st: st} }

func (cs *Store) InsertScratchpad(e model.ScratchpadEntry) error {
	return cs.st.InsertScratchpad(e)
}

func (cs *Store) ListScratchpad(runID string) ([]model.ScratchpadEntry, error) {
	return cs.st.ListScratchpad(runID)
}

func (cs *Store) InsertToolEvent(ev model.ToolEvent) error {
	return cs.st.InsertToolEvent(ev)
}

func (cs *Store) ListToolEvents(runID string) ([]model.ToolEvent, error) {
	return cs.st.ListToolEvents(runID)
}

func (cs *Store) AddLongTermMemory(m model.LongTermMemory) error {
	return cs.st.AddLongTermMemory(m)
}

func (cs *Store) ListLongTermMemory(device, metric string) ([]model.LongTermMemory, error) {
	return cs.st.ListLongTermMemory(device, metric)
}

func (cs *Store) RecentMessages(conversationID string, n int) ([]model.Message, error) {
	return cs.st.RecentMessages(conversationID, n)
}

func (cs *Store) ListKnowledgeChunks() ([]model.KbKnowledgeChunk, error) {
	return cs.st.ListKnowledgeChunks()
}

// ListKnowledgeChunksFiltered 按版本过滤检索视图；version 为空返回全量。
func (cs *Store) ListKnowledgeChunksFiltered(version string) ([]model.KbKnowledgeChunk, error) {
	return cs.st.ListKnowledgeChunksFiltered(version)
}
