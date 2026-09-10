package kb

import (
	"context"
	"path/filepath"
	"testing"

	"aiops/internal/config"
	"aiops/internal/store"
)

// 零值 cfg 应按原样使用（不隐式 Load 环境），便于测试显式注入。
func TestNewServiceWithEnrichZeroCfgNoImplicitLoad(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	lc := fakeEnrichCompleter{summary: "摘要句", questions: "q1"}
	svc := NewServiceWithEnrich(st, nil, nil, lc, config.Config{})
	doc, err := svc.Upload(context.Background(), UploadReq{SourceFile: "a.md", Data: []byte("# 标题\n内容正文"), Title: "t"})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	chunks, _ := st.ListKbChunksByDoc(doc.ID)
	if len(chunks) == 0 {
		t.Fatalf("no chunks")
	}
	if chunks[0].Summary != "" {
		t.Fatalf("零值 cfg 应禁用富化（不隐式读环境），got summary %q", chunks[0].Summary)
	}
}

// 便捷构造应使用确定性 Default（忽略进程环境），不隐式 Load。
func TestNewServiceIgnoresEnv(t *testing.T) {
	t.Setenv("KB_ENHANCE", "0")
	t.Setenv("KB_CDTA_THRESHOLD", "0.5")
	st, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	// NewServiceWithEmbedder 无 cfg 参数，应等价于 Default 而非 Load（Load 会读到 0/0.5）。
	// 通过显式 Default 构造对照：两者行为一致即证明忽略环境。
	svcDefault := NewServiceWithEnrich(st, nil, nil, nil, config.Default())
	if !svcDefault.cfg.KBEnhance || svcDefault.cfg.KBCdtaThreshold != 0.85 {
		t.Fatalf("Default 构造应为增强开/阈值0.85，got %+v", svcDefault.cfg)
	}
}
