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
	{Code: AskRootCause, Name: "AskRootCause", Description: "询问根因", Examples: []string{"alarm-123 什么原因", "这台机器为什么告警"}, RuleKeywords: []string{"什么原因", "为什么", "根因"}},
	{Code: AskRecommendation, Name: "AskRecommendation", Description: "询问处置建议", Examples: []string{"alarm-123 怎么处理", "磁盘满了怎么办"}, RuleKeywords: []string{"怎么处理", "怎么办", "建议", "处置"}},
	{Code: AskStatus, Name: "AskStatus", Description: "询问状态/进度", Examples: []string{"排查进度如何", "现在什么状态"}, RuleKeywords: []string{"状态", "进度"}},
	{Code: InspectAlert, Name: "InspectAlert", Description: "排查某个告警", Examples: []string{"查一下 compute-01", "看看 switch-core-01"}, RuleKeywords: []string{}},
	{Code: ListAlerts, Name: "ListAlerts", Description: "查看告警列表", Examples: []string{"有哪些告警", "列出所有告警"}, RuleKeywords: []string{"列表", "有哪些告警", "告警"}},
	{Code: Unknown, Name: "Unknown", Description: "与告警运维无关", Examples: []string{}, RuleKeywords: []string{}},
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

// ClassifyWithLight 轻量 LLM 分类并自评分：闭集 = leaves 的 code+description+examples+path；
// 返回 (Intent, confidence)；失败/非法映射 (Unknown, 0)。
func ClassifyWithLight(ctx context.Context, leaves []model.LeafSpec, text string, c ctxpkg.Completer) (Intent, float64) {
	if len(leaves) == 0 {
		leaves = SeedLeaves
	}
	var parts []string
	for _, l := range leaves {
		desc := l.Description
		if desc == "" {
			desc = l.Code
		}
		s := l.Code + "(" + desc
		if len(l.Examples) > 0 {
			s += "，示例：" + strings.Join(l.Examples, "、")
		}
		if l.Path != "" {
			s += "，路径：" + l.Path
		}
		parts = append(parts, s+")")
	}
	system := "你是意图分类器。判断用户请求属于哪个意图，只输出 JSON：{\"intent\":\"<意图名>\",\"confidence\":<0到1之间的小数>}。意图名必须是 " + strings.Join(parts, "、") + "。无法确定时返回 {\"intent\":\"Unknown\",\"confidence\":0}。"
	raw, err := c.Complete(ctx, system, text)
	if err != nil || strings.TrimSpace(raw) == "" {
		return Unknown, 0
	}
	var out struct {
		Intent     string  `json:"intent"`
		Confidence float64 `json:"confidence"`
	}
	if err := llm.ParseJSON(raw, &out); err != nil {
		return Unknown, 0
	}
	for _, l := range leaves {
		if out.Intent == l.Code {
			if out.Confidence < 0 || out.Confidence > 1 {
				return Unknown, 0
			}
			return l.Code, out.Confidence
		}
	}
	return Unknown, 0
}

// ParseIntentWithLight 规则优先；规则分 <0.4 用轻量 LLM 分类；LLM 分 <0.4 落回 Unknown。
func ParseIntentWithLight(ctx context.Context, leaves []model.LeafSpec, text string, c ctxpkg.Completer) (Intent, float64) {
	sc := RuleScore(leaves, text)
	if sc.Confidence >= 0.4 {
		return sc.Intent, sc.Confidence
	}
	if c == nil || ctxpkg.IsNilCompleter(c) {
		return Unknown, 0
	}
	intent, conf := ClassifyWithLight(ctx, leaves, text, c)
	if conf < 0.4 {
		return Unknown, conf
	}
	return intent, conf
}

// ParseIntentSet 解析一条输入的全部意图：重写拆分 → 逐子问题 ParseIntentWithLight，截断到 maxSplit。
func ParseIntentSet(ctx context.Context, leaves []model.LeafSpec, text string, c ctxpkg.Completer, maxSplit int) []ScoredIntent {
	if c == nil || ctxpkg.IsNilCompleter(c) {
		return []ScoredIntent{RuleScore(leaves, text)}
	}
	_, subs, err := RewriteAndSplit(ctx, text, c)
	if err != nil || len(subs) == 0 {
		return []ScoredIntent{RuleScore(leaves, text)}
	}
	if maxSplit <= 0 {
		maxSplit = 3
	}
	if len(subs) > maxSplit {
		subs = subs[:maxSplit]
	}
	out := make([]ScoredIntent, 0, len(subs))
	for _, s := range subs {
		intent, conf := ParseIntentWithLight(ctx, leaves, s, c)
		out = append(out, ScoredIntent{Intent: intent, Confidence: conf})
	}
	return out
}

// IntentName 返回意图 code（恒等）。
func IntentName(i Intent) string { return i }
