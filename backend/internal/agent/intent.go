package agent

import (
	"context"
	"errors"
	"regexp"
	"strings"

	ctxpkg "aiops/internal/context"
	"aiops/internal/llm"
)

type Intent int

const (
	Unknown Intent = iota
	ListAlerts
	InspectAlert
	AskRootCause
	AskRecommendation
	AskStatus
)

var alarmRe = regexp.MustCompile(`(?i)alarm[-_ ]?\w*|(compute|web|db|storage|switch)-?0?\d*`)

func ParseIntent(text string) Intent {
	t := strings.ToLower(text)
	switch {
	case strings.Contains(t, "什么原因") || strings.Contains(t, "为什么") || strings.Contains(t, "根因"):
		return AskRootCause
	case strings.Contains(t, "怎么处理") || strings.Contains(t, "怎么办") || strings.Contains(t, "建议") || strings.Contains(t, "处置"):
		return AskRecommendation
	case strings.Contains(t, "状态") || strings.Contains(t, "进度"):
		return AskStatus
	case (strings.Contains(t, "查") || strings.Contains(t, "排查") || strings.Contains(t, "看")) &&
		alarmRe.MatchString(t):
		return InspectAlert
	case strings.Contains(t, "列表") || strings.Contains(t, "有哪些告警") || strings.Contains(t, "告警"):
		return ListAlerts
	}
	return Unknown
}

// RewriteAndSplit 轻量 LLM 一次调用完成问题重写与多意图拆分。
// 返回 rewritten（规范化完整问题）与 subs（拆分出的子问题列表）；LLM 不可用/失败返回 err。
func RewriteAndSplit(ctx context.Context, text string, c ctxpkg.Completer) (rewritten string, subs []string, err error) {
	system := "你是 AIOps 运维助手的问题理解模块。把用户提问重写为规范、完整的中文问题，并拆分为相互独立的子问题。只输出 JSON：{\"rewritten\":\"<重写后的完整问题>\",\"sub_questions\":[\"<子问题1>\",\"<子问题2>\"]}。若只有一个意图，sub_questions 只含一项。子问题必须完整自包含（补全指代、告警ID、设备名等）。"
	raw, cerr := c.Complete(ctx, system, text)
	if cerr != nil || strings.TrimSpace(raw) == "" {
		return "", nil, cerr
	}
	var out struct {
		Rewritten string   `json:"rewritten"`
		Subs      []string `json:"sub_questions"`
	}
	if err := llm.ParseJSON(raw, &out); err != nil {
		return "", nil, err
	}
	if out.Rewritten == "" || len(out.Subs) == 0 {
		return "", nil, errors.New("rewrite: 空结果")
	}
	return out.Rewritten, out.Subs, nil
}

// classifyWithLight 轻量 LLM 分类为 6 类之一；失败/非法映射 Unknown。
func classifyWithLight(ctx context.Context, text string, c ctxpkg.Completer) Intent {
	system := "你是意图分类器。判断用户请求属于哪个意图，只输出 JSON：{\"intent\":\"<意图名>\"}。意图名必须是 ListAlerts(查看告警列表)、InspectAlert(排查某个告警)、AskRootCause(询问根因)、AskRecommendation(询问处置建议)、AskStatus(询问状态/进度)、Unknown(与告警运维无关)。无法确定时返回 Unknown。"
	raw, err := c.Complete(ctx, system, text)
	if err != nil || strings.TrimSpace(raw) == "" {
		return Unknown
	}
	var out struct {
		Intent string `json:"intent"`
	}
	if err := llm.ParseJSON(raw, &out); err != nil {
		return Unknown
	}
	switch out.Intent {
	case "ListAlerts":
		return ListAlerts
	case "InspectAlert":
		return InspectAlert
	case "AskRootCause":
		return AskRootCause
	case "AskRecommendation":
		return AskRecommendation
	case "AskStatus":
		return AskStatus
	}
	return Unknown
}

// ParseIntentWithLight 规则优先；规则判 Unknown 时用轻量 LLM 分类；LLM 不可用/失败回退 Unknown。
func ParseIntentWithLight(ctx context.Context, text string, c ctxpkg.Completer) Intent {
	if intent := ParseIntent(text); intent != Unknown {
		return intent
	}
	if c == nil || ctxpkg.IsNilCompleter(c) {
		return Unknown
	}
	return classifyWithLight(ctx, text, c)
}

// ParseIntentSet 解析一条输入的全部意图：LLM 重写+拆分 → 逐子问题 ParseIntentWithLight，截断到 maxSplit。
// LLM 不可用或拆分失败时回退为单意图规则 ParseIntent(text)。
func ParseIntentSet(ctx context.Context, text string, c ctxpkg.Completer, maxSplit int) []Intent {
	if c == nil || ctxpkg.IsNilCompleter(c) {
		return []Intent{ParseIntent(text)}
	}
	_, subs, err := RewriteAndSplit(ctx, text, c)
	if err != nil || len(subs) == 0 {
		return []Intent{ParseIntent(text)}
	}
	if len(subs) > maxSplit {
		subs = subs[:maxSplit]
	}
	out := make([]Intent, 0, len(subs))
	for _, s := range subs {
		out = append(out, ParseIntentWithLight(ctx, s, c))
	}
	return out
}

// IntentName 返回意图的字符串名；未知值返回 "Unknown"。
func IntentName(i Intent) string {
	switch i {
	case ListAlerts:
		return "ListAlerts"
	case InspectAlert:
		return "InspectAlert"
	case AskRootCause:
		return "AskRootCause"
	case AskRecommendation:
		return "AskRecommendation"
	case AskStatus:
		return "AskStatus"
	}
	return "Unknown"
}
