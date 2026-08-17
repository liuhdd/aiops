package kb

import (
	"testing"

	"aiops/internal/model"
)

func TestTagDeviceTypesAndMetrics(t *testing.T) {
	content := "gpu-server 掉卡，检查 gpu_util 与 NVLink 链路，必要时扩容。"
	tags := Tag(content, nil)
	if !contains(tags.DeviceTypes, "gpu-server") {
		t.Fatalf("应命中 gpu-server, got %v", tags.DeviceTypes)
	}
	if !contains(tags.Metrics, "gpu_util") {
		t.Fatalf("应命中 gpu_util, got %v", tags.Metrics)
	}
	if !contains(tags.Keywords, "NVLink") || !contains(tags.Keywords, "扩容") || !contains(tags.Keywords, "掉卡") {
		t.Fatalf("关键词应命中 NVLink/扩容/掉卡, got %v", tags.Keywords)
	}
}

func TestTagNoHit(t *testing.T) {
	content := "与运维无关的普通文本。"
	tags := Tag(content, nil)
	if len(tags.DeviceTypes) != 0 || len(tags.Metrics) != 0 || len(tags.Keywords) != 0 {
		t.Fatalf("不应命中, got %+v", tags)
	}
}

func TestTagIntentSuggestion(t *testing.T) {
	content := "如何排查磁盘空间不足的根因？请给出处置建议。"
	leaves := []model.LeafSpec{
		{Code: "AskRootCause", Name: "AskRootCause", Description: "询问根因", RuleKeywords: []string{"根因", "为什么"}},
		{Code: "AskRecommendation", Name: "AskRecommendation", Description: "询问处置建议", RuleKeywords: []string{"建议", "怎么办"}},
		{Code: "Unknown", Name: "Unknown", Description: "无关", RuleKeywords: []string{}},
	}
	tags := Tag(content, leaves)
	if !contains(tags.Intents, "AskRootCause") || !contains(tags.Intents, "AskRecommendation") {
		t.Fatalf("应建议绑定两意图, got %v", tags.Intents)
	}
}

func TestTagIntentByDescriptionAndName(t *testing.T) {
	content := "文档介绍散热与风扇转速。"
	leaves := []model.LeafSpec{{Code: "Cooling", Name: "散热排查", Description: "散热与风扇", RuleKeywords: []string{}}}
	tags := Tag(content, leaves)
	if !contains(tags.Intents, "Cooling") {
		t.Fatalf("应按 name/description 命中, got %v", tags.Intents)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
