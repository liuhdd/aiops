package kb

import (
	"strings"

	"aiops/internal/model"
)

// 打标词表（与 mockdata 设备 Role 集合保持一致，独立维护避免包依赖）。
var deviceTypeVocab = []string{"gpu-server", "server", "switch", "storage"}
var metricVocab = []string{"cpu_usage", "disk_usage", "gpu_util", "mem_usage", "net_in"}
var keywordVocab = []string{"OOM", "Xid", "NVLink", "扩容", "掉卡", "热插拔", "固件", "光模块", "散热", "日志轮转", "STP", "广播风暴", "tablespace", "InnoDB"}

// TagResult 自动打标结果。
type TagResult struct {
	DeviceTypes []string
	Metrics     []string
	Keywords    []string
	Intents     []string
}

// Tag 扫描正文命中词表；leaves 非空时按其 RuleKeywords/Name/Description 建议绑定意图（跳过 Unknown）。
func Tag(content string, leaves []model.LeafSpec) TagResult {
	low := strings.ToLower(content)
	var out TagResult
	for _, v := range deviceTypeVocab {
		if strings.Contains(low, strings.ToLower(v)) {
			out.DeviceTypes = append(out.DeviceTypes, v)
		}
	}
	for _, v := range metricVocab {
		if strings.Contains(low, strings.ToLower(v)) {
			out.Metrics = append(out.Metrics, v)
		}
	}
	for _, v := range keywordVocab {
		if strings.Contains(low, strings.ToLower(v)) {
			out.Keywords = append(out.Keywords, v)
		}
	}
	for _, l := range leaves {
		if l.Code == "" || l.Code == "Unknown" {
			continue
		}
		if intentHit(l, low) {
			out.Intents = append(out.Intents, l.Code)
		}
	}
	return out
}

func intentHit(l model.LeafSpec, low string) bool {
	for _, kw := range l.RuleKeywords {
		if kw != "" && strings.Contains(low, strings.ToLower(kw)) {
			return true
		}
	}
	if l.Name != "" && strings.Contains(low, strings.ToLower(l.Name)) {
		return true
	}
	return l.Description != "" && strings.Contains(low, strings.ToLower(l.Description))
}
