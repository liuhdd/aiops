package prompt

import (
	"log"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Entry 一条提示词注册信息：name 为 Phoenix prompt 标识；Default 为内嵌默认模板（{{var}} 占位）；
// Role 为 "system"/"user"（push 灌库时作为消息角色）。
type Entry struct {
	Name    string
	Role    string
	Default string
}

// All 全部提示词注册表。Default 与代码中原文案逐字一致（见 spec §4 变量映射）。
var All = map[string]Entry{
	"chat-answer-system": {"chat-answer-system", "system",
		"【核心规则】必须严格基于【相关上下文】与已有排查结论回答；上下文中没有的信息回答\"暂无相关信息\"，禁止使用自身知识补充，禁止编造。" +
			"你是算力中心硬件运维助手，结论需注明依据来源（上下文段落或已有根因）。" +
			"【输出要求】分点回答，每点不超过3句，语言简洁专业。"},
	"chat-answer-user": {"chat-answer-user", "user",
		"告警 {{alert_id}}：设备={{device}} 指标={{metric}}\n已有根因：{{rca}}\n已有处置：{{actions}}\n\n【相关上下文】\n{{context}}\n\n用户问题：{{question}}"},
	"knowledge-intro": {"knowledge-intro", "system",
		"以下是当前任务相关的上下文信息。回答时优先依据【知识库】【设备历史经验】等分段内容，引用结论时标注来源文档；分段中无相关信息时直接说明，不要引用不存在的段落。"},
	"rca-system": {"rca-system", "system",
		"你是算力中心硬件运维专家。基于给定证据输出JSON：{\"root_cause\":string,\"evidence_chain\":[string],\"confidence\":0~1,\"gaps\":[string]}。证据中不存在的内容必须写入 gaps，禁止编造 evidence_chain。只输出JSON。"},
	"recommend-system": {"recommend-system", "system",
		"你是算力中心运维助手。基于根因和预案，给出简明的处置建议步骤列表。步骤须与 runbook_ref 对应，无对应预案时 risk 填\"无预案参考\"。只输出JSON：{\"actions\":[string],\"runbook_ref\":string,\"risk\":string}"},
	"query-rewrite": {"query-rewrite", "user",
		"将下述查询结合对话历史改写为独立检索句，仅输出改写句。设备名、告警ID、指标名等实体词必须原样保留。\n历史：{{history}}\n查询：{{query}}"},
	"intent-rewrite-split": {"intent-rewrite-split", "system",
		"你是 AIOps 运维助手的问题理解模块。把用户提问重写为规范、完整的中文问题，并拆分为相互独立的子问题。只输出 JSON：{\"rewritten\":\"<重写后的完整问题>\",\"sub_questions\":[\"<子问题1>\",\"<子问题2>\"]}。若只有一个意图，sub_questions 只含一项。子问题必须完整自包含（补全指代、告警ID、设备名等）。"},
	"intent-classify": {"intent-classify", "system",
		"你是意图分类器。判断用户请求属于哪个意图，只输出 JSON：{\"intent\":\"<意图名>\",\"confidence\":<0到1之间的小数>}。意图名必须是 {{candidates}}。无法确定时返回 {\"intent\":\"Unknown\",\"confidence\":0}。"},
	"rca-react-system": {"rca-react-system", "system",
		"你是算力中心硬件运维专家。基于上下文，若证据不足可调用工具补充，推理记录写入write_scratchpad。最终输出JSON: {\"final_answer\":{\"root_cause\":string,\"evidence_chain\":[string],\"confidence\":0~1,\"gaps\":[string]}}。除final_answer或tool_call外不输出其它内容。"},
	"rca-gap-hint": {"rca-gap-hint", "user",
		"这是第{{iteration}}轮补充取证，请重点分析新增证据。"},
	"eval-judge-system": {"eval-judge-system", "system",
		"你是评测评委。只输出 JSON: {\"score\":0~1,\"verdict\":\"PASS|FAIL|INSUFFICIENT_INFO\"}。若信息不足判断必须返回 INSUFFICIENT_INFO，不要猜测。"},
	"eval-judge-user": {"eval-judge-user", "user",
		"评测任务: {{task_id}}\n证据:{{evidence}}\nRCA:{{rca}}\n评分维度: {{rubric}}"},
	"kb-summarize": {"kb-summarize", "user",
		"用1-2句中文概括下列运维知识的核心处置/根因，不要扩写：\n{{content}}"},
	"kb-gen-questions": {"kb-gen-questions", "user",
		"基于下列运维知识，列出它能回答的2-3个用户提问，运维口吻，每行一问，不要编号：\n{{content}}"},
}

const (
	defaultTTL   = 60 * time.Second
	negativeTTL  = time.Minute // 拉取失败的负缓存窗口，期间不再请求 Phoenix
	warnInterval = time.Minute // 同名降级 warn 日志去重窗口
)

var (
	mu      sync.RWMutex
	fetcher *PhoenixFetcher // nil = 未配置，全走默认模板
	ttl     = defaultTTL
	cache   = map[string]cacheItem{}
	group   = &singleflight.Group{}
	warnAt  = map[string]time.Time{}
)

type cacheItem struct {
	text    string // 空 = 负缓存（拉取失败）
	expires time.Time
}

// Configure 注入 Phoenix 拉取器；f 为 nil 即关闭拉取并清缓存（测试用它复位）。
// d <= 0 时用默认 TTL。未调用本函数时 Render 永远走默认模板。
func Configure(f *PhoenixFetcher, d time.Duration) {
	mu.Lock()
	defer mu.Unlock()
	fetcher = f
	if d > 0 {
		ttl = d
	}
	cache = map[string]cacheItem{}
	warnAt = map[string]time.Time{}
	// group 整体换新（mu 保护下仅换指针，不触碰旧 group 内部字段）：
	// 防止新配置的调用被合并进在途旧拉取；旧拉取结果另由 fetcher != f 校验作废。
	group = &singleflight.Group{}
}

// Render 取 name 对应生效模板并渲染 {{var}}；未注册 name 返回空串。
func Render(name string, vars map[string]string) string {
	if _, ok := All[name]; !ok {
		return ""
	}
	return render(template(name), vars)
}

// template 返回生效模板：Phoenix 缓存优先，默认模板兜底。
// 首次同步惰性拉取；TTL 内命中缓存；过期且有旧值时后台刷新、先返回旧值。
func template(name string) string {
	e, ok := All[name]
	if !ok {
		return ""
	}
	mu.RLock()
	f := fetcher
	c, hit := cache[name]
	mu.RUnlock()
	if f == nil {
		return e.Default
	}
	now := time.Now()
	if hit && c.text != "" && now.Before(c.expires) {
		return c.text
	}
	if hit && c.text != "" { // 有旧值但已过期：后台刷新，先返回旧值
		go refresh(name)
		return c.text
	}
	refresh(name) // 首次或负缓存窗口内：同步尝试
	mu.RLock()
	c = cache[name]
	mu.RUnlock()
	if c.text != "" {
		return c.text
	}
	return e.Default
}

// refresh 拉取 name 并写缓存；成功写正缓存（TTL），失败写负缓存（negativeTTL）并记去重 warn。
// singleflight 合并同 name 并发拉取。
func refresh(name string) {
	e, ok := All[name]
	if !ok {
		return
	}
	mu.RLock()
	g := group // 快照当前 group，避免 Do 执行期间与 Configure 换新竞争
	mu.RUnlock()
	g.Do(name, func() (any, error) {
		mu.RLock()
		c, hit := cache[name]
		f := fetcher
		mu.RUnlock()
		if f == nil {
			return nil, nil // 已被 Configure(nil) 复位，放弃本次拉取
		}
		if hit && time.Now().Before(c.expires) {
			return nil, nil // 其他 goroutine 已刷新（含负缓存窗口内）
		}
		text, ok2 := f.Fetch(name)
		mu.Lock()
		if fetcher != f { // 期间被复位/换拉取器，结果作废，避免污染新配置的缓存
			mu.Unlock()
			return nil, nil
		}
		if ok2 {
			cache[name] = cacheItem{text: text, expires: time.Now().Add(ttl)}
		} else {
			cache[name] = cacheItem{text: "", expires: time.Now().Add(negativeTTL)}
			if t, seen := warnAt[name]; !seen || time.Since(t) > warnInterval {
				warnAt[name] = time.Now()
				log.Printf("[prompt] %s（%s）拉取失败，回退默认模板", name, e.Name)
			}
		}
		mu.Unlock()
		return nil, nil
	})
}

// render {{var}} 替换；模板中未出现的变量原样保留占位符，便于发现模板/变量不匹配。
func render(tpl string, vars map[string]string) string {
	if len(vars) == 0 {
		return tpl
	}
	pairs := make([]string, 0, len(vars)*2)
	for k, v := range vars {
		pairs = append(pairs, "{{"+k+"}}", v)
	}
	return strings.NewReplacer(pairs...).Replace(tpl)
}
