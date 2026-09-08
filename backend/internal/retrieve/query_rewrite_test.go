package retrieve

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type rewriteFake struct {
	out    string
	err    error
	called bool
	prompt string
	delay  time.Duration
}

func (f *rewriteFake) Complete(ctx context.Context, prompt string) (string, error) {
	f.called = true
	f.prompt = prompt
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return f.out, f.err
}

func TestRewriteQueryPassthrough(t *testing.T) {
	got, _ := RewriteQuery(context.Background(), nil, nil, "GPU 掉卡怎么处理")
	if got != "GPU 掉卡怎么处理" {
		t.Fatalf("got %q", got)
	}
	// 空历史直通
	fc := &rewriteFake{out: "改写后"}
	got2, _ := RewriteQuery(context.Background(), fc, nil, "GPU 掉卡怎么处理")
	if got2 != "GPU 掉卡怎么处理" || fc.called {
		t.Fatalf("空历史应直通不调 LLM, got %q called=%v", got2, fc.called)
	}
	got3, _ := RewriteQuery(context.Background(), fc, []string{}, "GPU 掉卡怎么处理")
	if got3 != "GPU 掉卡怎么处理" || fc.called {
		t.Fatalf("空 history 切片应直通, got %q", got3)
	}
}

func TestRewriteQueryStandalonePassthrough(t *testing.T) {
	fc := &rewriteFake{out: "不应被调用"}
	long := strings.Repeat("a", 41) + "怎么"
	// 长度>40 且含"怎么" 视为已独立，直通
	got, _ := RewriteQuery(context.Background(), fc, []string{"历史1"}, long)
	if got != long || fc.called {
		t.Fatalf("已独立查询应直通, got %q called=%v", got, fc.called)
	}
}

func TestRewriteQuerySuccess(t *testing.T) {
	fc := &rewriteFake{out: "  改写后的独立句  "}
	got, err := RewriteQuery(context.Background(), fc, []string{"历史1", "历史2"}, "它怎么处理")
	if err != nil {
		t.Fatalf("unexpected err %v", err)
	}
	if got != "改写后的独立句" {
		t.Fatalf("got %q", got)
	}
	if !fc.called || !strings.Contains(fc.prompt, "它怎么处理") {
		t.Fatalf("prompt 未包含原查询, prompt=%q", fc.prompt)
	}
	if !strings.Contains(fc.prompt, "历史") {
		t.Fatalf("prompt 应包含历史, got %q", fc.prompt)
	}
}

func TestRewriteQueryEmptyOutputFallback(t *testing.T) {
	fc := &rewriteFake{out: "   "}
	orig := "短查询"
	got, _ := RewriteQuery(context.Background(), fc, []string{"h"}, orig)
	if got != orig {
		t.Fatalf("空输出应回退原文, got %q", got)
	}
	// 错误回退
	fc2 := &rewriteFake{err: errors.New("llm down")}
	got2, err2 := RewriteQuery(context.Background(), fc2, []string{"h"}, orig)
	if got2 != orig || err2 == nil {
		t.Fatalf("LLM 错误应回退原文并返回 err, got %q err=%v", got2, err2)
	}
}

func TestRewriteQueryPromptKeepsEntities(t *testing.T) {
	f := &rewriteFake{out: "改写后的句子"}
	_, err := RewriteQuery(context.Background(), f, []string{"上一句"}, "磁盘满了怎么办")
	if err != nil {
		t.Fatalf("RewriteQuery: %v", err)
	}
	for _, kw := range []string{"原样保留", "历史：上一句", "查询：磁盘满了怎么办"} {
		if !strings.Contains(f.prompt, kw) {
			t.Fatalf("prompt 应含 %q, got %q", kw, f.prompt)
		}
	}
}

func TestRewriteQueryTimeoutFallback(t *testing.T) {
	fc := &rewriteFake{out: "超时后结果", delay: 3 * time.Second}
	orig := "需要改写的查询"
	start := time.Now()
	got, err := RewriteQuery(context.Background(), fc, []string{"h1"}, orig)
	elapsed := time.Since(start)
	if got != orig {
		t.Fatalf("超时应回退原文, got %q", got)
	}
	if err == nil {
		t.Fatalf("超时应返回 err")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "deadline") {
		// 只要有 err 即可，不强求具体类型
	}
	if elapsed > 2500*time.Millisecond || elapsed < 1900*time.Millisecond {
		t.Fatalf("超时应约 2s, elapsed=%v", elapsed)
	}
}

func TestIsNilCompleter(t *testing.T) {
	if !IsNilCompleter(nil) {
		t.Fatal("nil 应为 true")
	}
	var fc *rewriteFake = nil
	if !IsNilCompleter(fc) {
		t.Fatal("typed nil 应为 true")
	}
	if IsNilCompleter(&rewriteFake{}) {
		t.Fatal("非 nil 应为 false")
	}
}
