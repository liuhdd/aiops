package kb

import (
	"context"
	"reflect"
)

// DualCompleter 双参补全接口（system+user），与 llm.Client / context.Completer 同形。
// 定义在 kb 包内，避免 llm 反向依赖 kb。
type DualCompleter interface {
	Complete(ctx context.Context, system, user string) (string, error)
}

type dualAdapter struct {
	c DualCompleter
}

// AdaptCompleter 将双参补全适配为单参 kb.Completer；c 为 nil（含 typed-nil）时返回 nil。
func AdaptCompleter(c DualCompleter) Completer {
	if c == nil {
		return nil
	}
	if v := reflect.ValueOf(c); v.Kind() == reflect.Ptr && v.IsNil() {
		return nil
	}
	return &dualAdapter{c: c}
}

// Complete 将 prompt 转为 user 消息透传，system 置空。
func (a *dualAdapter) Complete(ctx context.Context, prompt string) (string, error) {
	if a == nil || a.c == nil {
		return "", nil
	}
	if v := reflect.ValueOf(a.c); v.Kind() == reflect.Ptr && v.IsNil() {
		return "", nil
	}
	return a.c.Complete(ctx, "", prompt)
}
