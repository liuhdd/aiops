package store

import (
	"testing"
	"time"

	"aiops/internal/model"
)

// TestKbChunkRefsCRUD 引用边表增删查：建 doc+chunk → 写入两条引用（含 resolved=0 未命中）→
// 按 chunk 查回 2 条 → 按 doc 删除后为空。
func TestKbChunkRefsCRUD(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	if err := s.WithKBTx(func(tx KBStore) error {
		if err := tx.InsertKbDocument(model.KbDocument{ID: "d1", Title: "t", Category: model.KBOther, Content: "x", CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		if err := tx.InsertKbChunk(model.KbChunk{ID: "c1", DocID: "d1", Seq: 0, Content: "x", CreatedAt: now}); err != nil {
			return err
		}
		if err := tx.InsertKbChunkRef(model.KbChunkRefRow{ID: "r1", ChunkID: "c1", DocID: "d1", TargetPath: "a.md", TargetDocID: "da", RefType: "wikilink", Resolved: true, CreatedAt: now}); err != nil {
			return err
		}
		return tx.InsertKbChunkRef(model.KbChunkRefRow{ID: "r2", ChunkID: "c1", DocID: "d1", TargetPath: "missing.md", RefType: "wikilink", Resolved: false, CreatedAt: now})
	}); err != nil {
		t.Fatalf("tx insert: %v", err)
	}
	refs, err := s.ListKbChunkRefsByChunk("c1")
	if err != nil || len(refs) != 2 {
		t.Fatalf("refs: %v %+v", err, refs)
	}
	if refs[0].ID != "r1" || !refs[0].Resolved || refs[0].TargetDocID != "da" {
		t.Fatalf("r1 = %+v", refs[0])
	}
	if refs[1].ID != "r2" || refs[1].Resolved {
		t.Fatalf("r2 应为未命中: %+v", refs[1])
	}
	if err := s.WithKBTx(func(tx KBStore) error { return tx.DeleteKbChunkRefsByDoc("d1") }); err != nil {
		t.Fatalf("delete: %v", err)
	}
	refs, err = s.ListKbChunkRefsByChunk("c1")
	if err != nil || len(refs) != 0 {
		t.Fatalf("删除后应为空: %v %+v", err, refs)
	}
}

// TestListKnowledgeChunksFiltered 版本过滤：空查询全量；命中区间保留；区间外排除；
// 无标注块对任意查询永真。
func TestListKnowledgeChunksFiltered(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	if err := s.WithKBTx(func(tx KBStore) error {
		if err := tx.InsertKbDocument(model.KbDocument{ID: "d1", Title: "t", Category: model.KBOther, Content: "x", CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		if err := tx.InsertKbChunk(model.KbChunk{ID: "c1", DocID: "d1", Seq: 0, Content: "x", VersionMin: "3.0.0", VersionMax: "3.5.0", CreatedAt: now}); err != nil {
			return err
		}
		return tx.InsertKbChunk(model.KbChunk{ID: "c2", DocID: "d1", Seq: 1, Content: "y", CreatedAt: now})
	}); err != nil {
		t.Fatalf("tx insert: %v", err)
	}
	all, err := s.ListKnowledgeChunksFiltered("")
	if err != nil || len(all) != 2 {
		t.Fatalf("空查询应全量: %v %+v", err, all)
	}
	hit, err := s.ListKnowledgeChunksFiltered("3.1.0")
	if err != nil || len(hit) != 2 {
		t.Fatalf("3.1.0 应命中两块: %v %+v", err, hit)
	}
	miss, err := s.ListKnowledgeChunksFiltered("4.0.0")
	if err != nil || len(miss) != 1 || miss[0].ChunkID != "c2" {
		t.Fatalf("4.0.0 应仅剩无标注块: %v %+v", err, miss)
	}
}
