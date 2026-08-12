package eval

import (
	"encoding/json"

	"aiops/internal/agent"
	"aiops/internal/llm"
)

// mustJSON 序列化任意值；失败时返回 "{}"
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// unmarshalJSON 解析 JSON 字符串（复用 llm.ParseJSON，支持 ``` 包裹）
func unmarshalJSON(raw string, v any) error {
	return llm.ParseJSON(raw, v)
}

// intentNameStr 将 agent.Intent 转为字符串名（iota 0..5）
func intentNameStr(i agent.Intent) string {
	switch i {
	case agent.Unknown:
		return "Unknown"
	case agent.ListAlerts:
		return "ListAlerts"
	case agent.InspectAlert:
		return "InspectAlert"
	case agent.AskRootCause:
		return "AskRootCause"
	case agent.AskRecommendation:
		return "AskRecommendation"
	case agent.AskStatus:
		return "AskStatus"
	}
	return "Unknown"
}
