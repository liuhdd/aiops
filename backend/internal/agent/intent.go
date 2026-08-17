package agent

import (
	"context"
	"errors"
	"regexp"
	"strings"

	ctxpkg "aiops/internal/context"
	"aiops/internal/llm"
	"aiops/internal/mockdata"
	"aiops/internal/model"
)

// Intent 意图 code（字符串别名）。
type Intent = string

const (
	Unknown           Intent = "Unknown"
	ListAlerts        Intent = "ListAlerts"
	InspectAlert      Intent = "InspectAlert"
	AskRootCause      Intent = "AskRootCause"
	AskRecommendation Intent = "AskRecommendation"
	AskStatus         Intent = "AskStatus"
)

// SeedLeaves 内置 6 种子（provider 为空时的兜底；顺序即规则匹配优先级）。
var SeedLeaves = []model.LeafSpec{
	{Code: AskRootCause, Name: "AskRootCause", Description: "询问根因", RuleKeywords: []string{"什么原因", "为什么", "根因"}},
	{Code: AskRecommendation, Name: "AskRecommendation", Description: "询问处置建议", RuleKeywords: []string{"怎么处理", "怎么办", "建议", "处置"}},
	{Code: AskStatus, Name: "AskStatus", Description: "询问状态/进度", RuleKeywords: []string{"状态", "进度"}},
	{Code: InspectAlert, Name: "InspectAlert", Description: "排查某个告警", RuleKeywords: []string{}},
	{Code: ListAlerts, Name: "ListAlerts", Description: "查看告警列表", RuleKeywords: []string{"列表", "有哪些告警", "告警"}},
	{Code: Unknown, Name: "Unknown", Description: "与告警运维无关", RuleKeywords: []string{}},
}

var alarmRe = regexp.MustCompile(`(?i)alarm[-_ ]?\w*|(compute|web|db|storage|switch)-?0?\d*`)

// inspectAlertMatch 内置复合规则：查/排查/看 + 告警/设备名。
func inspectAlertMatch(low string) bool {
	verb := strings.Contains(low, "查") || strings.Contains(low, "排查") || strings.Contains(low, "看")
	return verb && alarmRe.MatchString(low)
}

// ScoredIntent 带置信度的意图识别结果。
type ScoredIntent struct {
	Intent     Intent
	Confidence float64 // 0~1
}

// entityWords 实体词表（设备名 + 已知指标名）。
var entityWords = func() map[string]bool {
	m := make(map[string]bool)
	for _, d := range mockdata.Devices() {
		m[strings.ToLower(d.Name)] = true
	}
	for _, k := range []string{"cpu_usage", "disk_usage", "gpu_util", "mem_usage", "net_in"} {
		m[k] = true
	}
	return m
}()

// isEntityWord 判断关键词是否为实体名（设备/指标/alarm id）。
func isEntityWord(kw string) bool {
	if entityWords[strings.ToLower(kw)] {
		return true
	}
	return alarmRe.MatchString(kw)
}

// RuleScore 规则打分：按 leaves 顺序匹配，命中实体关键词 0.9、主题词/复合规则 0.6，无命中 Unknown(0)。
// 选择语义与 RuleMatch 一致（同分时先出现者胜），仅附加置信度。
func RuleScore(leaves []model.LeafSpec, text string) ScoredIntent {
	if len(leaves) == 0 {
		leaves = SeedLeaves
	}
	low := strings.ToLower(text)
	best := ScoredIntent{Intent: Unknown, Confidence: 0}
	for _, l := range leaves {
		if l.Code == Unknown {
			continue
		}
		if l.Code == InspectAlert {
			if inspectAlertMatch(low) && best.Confidence < 0.6 {
				best = ScoredIntent{Intent: InspectAlert, Confidence: 0.6}
			}
			continue
		}
		for _, kw := range l.RuleKeywords {
			if kw == "" || !strings.Contains(low, strings.ToLower(kw)) {
				continue
			}
			conf := 0.6
			if isEntityWord(kw) {
				conf = 0.9
			}
			if conf > best.Confidence {
				best = ScoredIntent{Intent: l.Code, Confidence: conf}
			}
		}
		for _, phrase := range []string{l.Description, l.Path} {
			if phrase == "" {
				continue
			}
			if strings.Contains(low, strings.ToLower(phrase)) && best.Confidence < 0.6 {
				best = ScoredIntent{Intent: l.Code, Confidence: 0.6}
			}
		}
	}
	if best.Confidence < 0.4 {
		return ScoredIntent{Intent: Unknown, Confidence: 0}
	}
	return best
}

// RuleMatch 规则优先：等价 RuleScore(...).Intent（向后兼容）。
func RuleMatch(leaves []model.LeafSpec, text string) Intent {
	return RuleScore(leaves, text).Intent
}

// ParseIntent 单意图规则识别（向后兼容，等价 RuleMatch(nil, text)）。
func ParseIntent(text string) Intent { return RuleMatch(nil, text) }

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

// ClassifyWithLight 轻量 LLM 分类：闭集 = leaves 的 code+description；失败/非法映射 Unknown。
func ClassifyWithLight(ctx context.Context, leaves []model.LeafSpec, text string, c ctxpkg.Completer) Intent {
	if len(leaves) == 0 {
		leaves = SeedLeaves
	}
	var parts []string
	for _, l := range leaves {
		desc := l.Description
		if desc == "" {
			desc = l.Code
		}
		parts = append(parts, l.Code+"("+desc+")")
	}
	system := "你是意图分类器。判断用户请求属于哪个意图，只输出 JSON：{\"intent\":\"<意图名>\"}。意图名必须是 " + strings.Join(parts, "、") + "。无法确定时返回 Unknown。"
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
	for _, l := range leaves {
		if out.Intent == l.Code {
			return l.Code
		}
	}
	return Unknown
}

// ParseIntentWithLight 规则优先；Unknown 用轻量 LLM 分类。
func ParseIntentWithLight(ctx context.Context, leaves []model.LeafSpec, text string, c ctxpkg.Completer) Intent {
	if intent := RuleMatch(leaves, text); intent != Unknown {
		return intent
	}
	if c == nil || ctxpkg.IsNilCompleter(c) {
		return Unknown
	}
	return ClassifyWithLight(ctx, leaves, text, c)
}

// ParseIntentSet 解析一条输入的全部意图：重写拆分 → 逐子问题 ParseIntentWithLight，截断到 maxSplit。
func ParseIntentSet(ctx context.Context, leaves []model.LeafSpec, text string, c ctxpkg.Completer, maxSplit int) []Intent {
	if c == nil || ctxpkg.IsNilCompleter(c) {
		return []Intent{RuleMatch(leaves, text)}
	}
	_, subs, err := RewriteAndSplit(ctx, text, c)
	if err != nil || len(subs) == 0 {
		return []Intent{RuleMatch(leaves, text)}
	}
	if maxSplit <= 0 {
		maxSplit = 3
	}
	if len(subs) > maxSplit {
		subs = subs[:maxSplit]
	}
	out := make([]Intent, 0, len(subs))
	for _, s := range subs {
		out = append(out, ParseIntentWithLight(ctx, leaves, s, c))
	}
	return out
}

// IntentName 返回意图 code（恒等）。
func IntentName(i Intent) string { return i }
