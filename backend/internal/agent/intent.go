package agent

import (
	"regexp"
	"strings"
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
