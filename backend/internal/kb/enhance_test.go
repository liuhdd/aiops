package kb

import (
	"context"
	"strings"
	"testing"
)

type fakeCompleter struct{ fn func(string) string }

func (f fakeCompleter) Complete(ctx context.Context, p string) (string, error) { return f.fn(p), nil }

func TestSummarize(t *testing.T) {
	fc := fakeCompleter{fn: func(p string) string { return "这是一句摘要。" }}
	got, _ := Summarize(context.Background(), fc, "GPU 掉卡 Xid 48 的排查...")
	if got != "这是一句摘要。" {
		t.Fatalf("%q", got)
	}
	// nil Completer 回退空
	got2, _ := Summarize(context.Background(), nil, "x")
	if got2 != "" {
		t.Fatalf("want empty")
	}
}

func TestGenerateQuestions(t *testing.T) {
	fc := fakeCompleter{fn: func(p string) string { return "如何排查 Xid 48？\nXid 48 如何恢复？\n" }}
	got, _ := GenerateQuestions(context.Background(), fc, "GPU Xid 48")
	if len(got) != 2 || got[0] != "如何排查 Xid 48？" {
		t.Fatalf("%v", got)
	}
}

func TestEnrichChunksConcurrencyAndFallback(t *testing.T) {
	fc := fakeCompleter{fn: func(p string) string {
		if strings.Contains(p, "概括") {
			return "摘要"
		}
		return "问题1\n问题2"
	}}
	chunks := []ChunkResult{{Content: "块1"}, {Content: "块2"}, {Content: "块3"}, {Content: "块4"}}
	got := EnrichChunks(context.Background(), fc, chunks)
	if len(got) != 4 || got[0].EnhancedContent == "" {
		t.Fatalf("%+v", got)
	}
	// 超时回退：用阻塞 Completer
}

func TestEnrichChunksNilCompleter(t *testing.T) {
	chunks := []ChunkResult{{Content: "块1", Heading: "h1"}}
	got := EnrichChunks(context.Background(), nil, chunks)
	if got[0].Summary != "" || len(got[0].Questions) != 0 || got[0].EnhancedContent != "块1" {
		t.Fatalf("%+v", got)
	}
}
