package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"aiops/internal/kb"
	"aiops/internal/model"
	"aiops/internal/store"
)

// TestVersionFilterConsistency 断言 store.matchChunkVersion 与 kb.MatchVersion
// 同语义（store 不能导入 kb，此处经 ListKnowledgeChunksFiltered 做一致性校验）。
func TestVersionFilterConsistency(t *testing.T) {
	cases := []struct {
		name     string
		min, max string
		query    string
	}{
		{"无标注全命中", "", "", "2.0"},
		{"空查询永真", "2.0.0", "2.9.9", ""},
		{"下界内", "2.0.0", "", "3.1.0"},
		{"下界外", "2.0.0", "", "1.9.0"},
		{"区间内", "2.0.0", "2.9.9", "2.5.0"},
		{"区间上界外", "2.0.0", "2.9.9", "3.0.0"},
		{"区间下界外", "2.0.0", "2.9.9", "1.0.0"},
		{"精确值命中", "3.0.0", "3.0.0", "3.0"},
		{"精确值未命中", "3.0.0", "3.0.0", "3.0.1"},
		{"仅上界命中", "", "2.9.9", "2.0"},
		{"仅上界未命中", "", "2.9.9", "3.0"},
		{"非法查询永真", "2.0.0", "2.9.9", "notaversion"},
		{"v前缀归一", "v2.0", "", "2.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := store.New(filepath.Join(t.TempDir(), "t.db"))
			if err != nil {
				t.Fatalf("store.New: %v", err)
			}
			defer s.Close()
			docID := model.NewID("kbdoc")
			chunkID := model.NewID("kbchunk")
			now := time.Now()
			if err := s.WithKBTx(func(tx store.KBStore) error {
				if err := tx.InsertKbDocument(model.KbDocument{
					ID: docID, Title: "版本一致性", Category: model.KBManual,
					Content: "正文", CreatedAt: now, UpdatedAt: now,
				}); err != nil {
					return err
				}
				return tx.InsertKbChunk(model.KbChunk{
					ID: chunkID, DocID: docID, Seq: 0,
					Content: "块正文", VersionMin: tc.min, VersionMax: tc.max, CreatedAt: now,
				})
			}); err != nil {
				t.Fatalf("insert: %v", err)
			}
			got, err := s.ListKnowledgeChunksFiltered(tc.query)
			if err != nil {
				t.Fatalf("Filtered: %v", err)
			}
			want := kb.MatchVersion(kb.VersionRange{Min: tc.min, Max: tc.max}, tc.query)
			found := len(got) == 1 && got[0].ChunkID == chunkID
			if found != want {
				t.Fatalf("min=%q max=%q query=%q: store=%v kb=%v", tc.min, tc.max, tc.query, found, want)
			}
		})
	}
}
