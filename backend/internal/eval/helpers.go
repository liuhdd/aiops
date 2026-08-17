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

// intentNames 将子意图结果集转为字符串名（与运行时同一命名）。
func intentNames(its []agent.ScoredIntent) []string {
	out := make([]string, 0, len(its))
	for _, i := range its {
		out = append(out, agent.IntentName(i.Intent))
	}
	return out
}
