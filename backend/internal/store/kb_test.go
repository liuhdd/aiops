package store

import (
	"strings"
	"testing"
	"time"

	"aiops/internal/model"
)

func mkDoc(id, title string, category model.KBCategory) model.KbDocument {
	return model.KbDocument{ID: id, Title: title, Category: category,
		DeviceTypes: []string{"switch"}, Vendor: []string{"H3C"}, Model: []string{"S5560"},
		Metrics: []string{"net_in"}, Keywords: []string{"光模块"}, Intents: []string{"AskRootCause"},
		SourceFile: "s5560.md", Content: "交换机链路排查正文", CreatedAt: time.Now(), UpdatedAt: time.Now()}
}

func TestKbDocRoundTrip(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	d := mkDoc("d1", "交换机手册", model.KBManual)
	d.CreatedAt, d.UpdatedAt = now, now
	if err := s.WithKBTx(func(tx KBStore) error {
		if err := tx.InsertKbDocument(d); err != nil {
			return err
		}
		return tx.InsertKbChunk(model.KbChunk{ID: "c1", DocID: "d1", Seq: 0, Heading: "", Content: "正文", CreatedAt: now})
	}); err != nil {
		t.Fatalf("tx insert: %v", err)
	}
	got, err := s.GetKbDocument("d1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "交换机手册" || len(got.Vendor) != 1 || got.Vendor[0] != "H3C" || len(got.Intents) != 1 {
		t.Fatalf("roundtrip = %+v", got)
	}
	chunks, err := s.ListKbChunksByDoc("d1")
	if err != nil || len(chunks) != 1 || chunks[0].Seq != 0 {
		t.Fatalf("chunks: %v %+v", err, chunks)
	}
}

func TestKbListFilters(t *testing.T) {
	s := newTestStore(t)
	_ = s.WithKBTx(func(tx KBStore) error {
		_ = tx.InsertKbDocument(mkDoc("d1", "交换机手册", model.KBManual))
		_ = tx.InsertKbDocument(model.KbDocument{ID: "d2", Title: "GPU故障案例", Category: model.KBCase,
			DeviceTypes: []string{"gpu-server"}, Vendor: []string{"NVIDIA"}, Model: []string{"H800"},
			Metrics: []string{"gpu_util"}, Intents: []string{"AskRootCause"}, Content: "GPU掉卡排查", CreatedAt: time.Now(), UpdatedAt: time.Now()})
		return nil
	})
	if list, err := s.ListKbDocuments(KbFilter{Category: string(model.KBManual)}); err != nil || len(list) != 1 || list[0].ID != "d1" {
		t.Fatalf("by category: %v %+v", err, list)
	}
	if list, err := s.ListKbDocuments(KbFilter{DeviceType: "gpu-server"}); err != nil || len(list) != 1 || list[0].ID != "d2" {
		t.Fatalf("by device_type: %v %+v", err, list)
	}
	if list, err := s.ListKbDocuments(KbFilter{Vendor: "H3C"}); err != nil || len(list) != 1 || list[0].ID != "d1" {
		t.Fatalf("by vendor: %v %+v", err, list)
	}
	if list, err := s.ListKbDocuments(KbFilter{Model: "H800"}); err != nil || len(list) != 1 || list[0].ID != "d2" {
		t.Fatalf("by model: %v %+v", err, list)
	}
	if list, err := s.ListKbDocuments(KbFilter{Intent: "AskRootCause"}); err != nil || len(list) != 2 {
		t.Fatalf("by intent: %v %+v", err, list)
	}
	if list, err := s.ListKbDocuments(KbFilter{Q: "掉卡"}); err != nil || len(list) != 1 || list[0].ID != "d2" {
		t.Fatalf("by q: %v %+v", err, list)
	}
	if list, err := s.ListKbDocuments(KbFilter{Category: string(model.KBCase), Vendor: "H3C"}); err != nil || len(list) != 0 {
		t.Fatalf("组合应无命中: %v %+v", err, list)
	}
	if list, err := s.ListKbDocuments(KbFilter{}); err != nil || len(list) != 2 {
		t.Fatalf("无筛选应全量: %v len=%d", err, len(list))
	}
}

func TestKbListFilterLiteralWildcards(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	_ = s.WithKBTx(func(tx KBStore) error {
		_ = tx.InsertKbDocument(model.KbDocument{ID: "d1", Title: "100%故障排查", Category: model.KBManual,
			Vendor: []string{"H3C"}, Content: "含百分号内容", CreatedAt: now, UpdatedAt: now})
		_ = tx.InsertKbDocument(mkDoc("d2", "100kw普通文档", model.KBManual))
		return nil
	})
	// q 含 % 应字面匹配：未转义时 '%100%%' 会把 % 当通配符，命中 d1/d2 两个。
	list, err := s.ListKbDocuments(KbFilter{Q: "100%"})
	if err != nil || len(list) != 1 || list[0].ID != "d1" {
		t.Fatalf("Q 含 %% 应字面匹配: %v %+v", err, list)
	}
	// 下划线同理：S5_60 不匹配 S5560。
	list2, err := s.ListKbDocuments(KbFilter{Model: "S5_60"})
	if err != nil || len(list2) != 0 {
		t.Fatalf("Model 含 _ 应字面匹配: %v %+v", err, list2)
	}
}

func TestKbDeleteCascadesChunks(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	_ = s.WithKBTx(func(tx KBStore) error {
		_ = tx.InsertKbDocument(mkDoc("d1", "t", model.KBOther))
		return tx.InsertKbChunk(model.KbChunk{ID: "c1", DocID: "d1", Seq: 0, Content: "x", CreatedAt: now})
	})
	if err := s.WithKBTx(func(tx KBStore) error {
		if err := tx.DeleteKbChunks("d1"); err != nil {
			return err
		}
		return tx.DeleteKbDocument("d1")
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetKbDocument("d1"); err == nil || !strings.Contains(err.Error(), "no rows") {
		t.Fatalf("删除后应无文档, got %v", err)
	}
	chunks, _ := s.ListKbChunksByDoc("d1")
	if len(chunks) != 0 {
		t.Fatalf("块应级联删除, got %+v", chunks)
	}
}

func TestKbKnowledgeChunksJoin(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	_ = s.WithKBTx(func(tx KBStore) error {
		_ = tx.InsertKbDocument(mkDoc("d1", "交换机手册", model.KBManual))
		_ = tx.InsertKbChunk(model.KbChunk{ID: "c1", DocID: "d1", Seq: 0, Heading: "排查步骤", Content: "检查光模块", CreatedAt: now})
		return tx.InsertKbChunk(model.KbChunk{ID: "c2", DocID: "d1", Seq: 1, Heading: "处理", Content: "更换光模块", CreatedAt: now})
	})
	kcs, err := s.ListKnowledgeChunks()
	if err != nil || len(kcs) != 2 {
		t.Fatalf("ListKnowledgeChunks: %v %+v", err, kcs)
	}
	if kcs[0].Title != "交换机手册" || kcs[0].Category != model.KBManual || kcs[0].Heading != "排查步骤" || len(kcs[0].Vendor) != 1 {
		t.Fatalf("join = %+v", kcs[0])
	}
}

func TestKbCount(t *testing.T) {
	s := newTestStore(t)
	_ = s.WithKBTx(func(tx KBStore) error {
		_ = tx.InsertKbDocument(mkDoc("d1", "t", model.KBOther))
		return tx.InsertKbDocument(mkDoc("d2", "t2", model.KBOther))
	})
	n, err := s.CountKbDocuments()
	if err != nil || n != 2 {
		t.Fatalf("Count = %d %v", n, err)
	}
}

func TestKbTablesCreated(t *testing.T) {
	s := newTestStore(t)
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('kb_documents','kb_chunks')`).Scan(&n)
	if err != nil || n != 2 {
		t.Fatalf("kb tables = %d %v, want 2", n, err)
	}
}
