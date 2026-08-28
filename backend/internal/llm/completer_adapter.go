package llm

import (
	"context"

	"aiops/internal/kb"
)

// KbCompleterAdapter 将双参 llm.Client 适配为单参 kb.Completer。
type KbCompleterAdapter struct {
	c *Client
}

// NewKbCompleter 构造适配器；c 为 nil 时返回 nil，便于与 kb.IsNilCompleter 配合做回退。
func NewKbCompleter(c *Client) kb.Completer {
	if c == nil {
		return nil
	}
	return &KbCompleterAdapter{c: c}
}

// Complete 将 prompt 转为 user 消息透传，system 置空。
func (a *KbCompleterAdapter) Complete(ctx context.Context, prompt string) (string, error) {
	if a == nil || a.c == nil {
		return "", nil
	}
	return a.c.Complete(ctx, "", prompt)
}
