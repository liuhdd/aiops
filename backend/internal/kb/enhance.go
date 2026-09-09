package kb

import (
	"context"
	"reflect"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	aiprompt "aiops/internal/prompt"
)

// Completer LLM 补全最小接口。
type Completer interface {
	Complete(ctx context.Context, prompt string) (string, error)
}

// IsNilCompleter 兼容 typed-nil：*T(nil) 包装进接口后接口非 nil，需反射判断底层指针。
func IsNilCompleter(c Completer) bool {
	if c == nil {
		return true
	}
	v := reflect.ValueOf(c)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return v.IsNil()
	}
	return false
}

// Summarize 返回 1-2 句中文概括，去空，超时 5s，截断 300 rune。
func Summarize(ctx context.Context, lc Completer, chunk string) (string, error) {
	if IsNilCompleter(lc) || strings.TrimSpace(chunk) == "" {
		return "", nil
	}
	ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	p := aiprompt.KBSummarizePrompt(chunk)
	out, err := lc.Complete(ctx2, p)
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	if len([]rune(out)) > 300 {
		out = string([]rune(out)[:300])
	}
	return out, nil
}

// GenerateQuestions 返回 2-3 个运维口吻提问，每行一问，去编号、去重、最多 3。
func GenerateQuestions(ctx context.Context, lc Completer, chunk string) ([]string, error) {
	if IsNilCompleter(lc) || strings.TrimSpace(chunk) == "" {
		return nil, nil
	}
	ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	p := aiprompt.KBGenQuestionsPrompt(chunk)
	out, err := lc.Complete(ctx2, p)
	if err != nil {
		return nil, err
	}
	var qs []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "1234567890.、）) ")
		if line == "" {
			continue
		}
		qs = append(qs, line)
		if len(qs) >= 3 {
			break
		}
	}
	// 去重
	seen := map[string]bool{}
	var uniq []string
	for _, q := range qs {
		if !seen[q] {
			seen[q] = true
			uniq = append(uniq, q)
		}
	}
	return uniq, nil
}

// EnrichedChunk 富化后的块。
type EnrichedChunk struct {
	ChunkResult
	Summary         string
	Questions       []string
	EnhancedContent string
}

// EnrichChunks 单文档内限并发 3，单块 5s 超时，失败回退原文。
func EnrichChunks(ctx context.Context, lc Completer, chunks []ChunkResult) []EnrichedChunk {
	out := make([]EnrichedChunk, len(chunks))
	if IsNilCompleter(lc) {
		for i, c := range chunks {
			out[i] = EnrichedChunk{ChunkResult: c, EnhancedContent: c.Content}
		}
		return out
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(3)
	for i, ch := range chunks {
		i, ch := i, ch
		g.Go(func() error {
			sum, _ := Summarize(gctx, lc, ch.Content)
			qs, _ := GenerateQuestions(gctx, lc, ch.Content)
			ec := ch.Content
			if sum != "" {
				ec += "\n【摘要】" + sum
			}
			out[i] = EnrichedChunk{ChunkResult: ch, Summary: sum, Questions: qs, EnhancedContent: ec}
			return nil
		})
	}
	_ = g.Wait()
	return out
}
