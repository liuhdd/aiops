package retrieve

import (
	"context"
	"reflect"
	"strings"
	"time"

	"aiops/internal/prompt"
)

// Completer 复用 llm/complete 语义的最小接口，避免 import 业务 context 包。
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

// RewriteQuery 仅做 standalone 改写，不做扩写/假设。
// 约束：light 为空或无历史时直通、已独立查询时直通、超时 2s 失败回退原文。
func RewriteQuery(ctx context.Context, light Completer, history []string, q string) (string, error) {
	if light == nil || IsNilCompleter(light) || len(history) == 0 {
		return q, nil
	}
	if len([]rune(q)) > 40 && strings.Contains(q, "怎么") {
		return q, nil
	}
	ctx2, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	instr := prompt.QueryRewriteInstruction(strings.Join(history, "\n"), q)
	out, err := light.Complete(ctx2, instr)
	if err != nil || strings.TrimSpace(out) == "" {
		return q, err
	}
	return strings.TrimSpace(out), nil
}
