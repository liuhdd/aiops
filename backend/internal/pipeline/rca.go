package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	ctxpkg "aiops/internal/context"
	"aiops/internal/llm"
	"aiops/internal/model"
)

type RCAStage struct {
	llm ctxpkg.Completer
}

func NewRCAStage(lc ctxpkg.Completer) *RCAStage { return &RCAStage{llm: lc} }

func (RCAStage) StageID() string { return "rca" }
func (RCAStage) Name() string    { return "根因分析" }

func (s *RCAStage) Run(ctx context.Context, st *State) error {
	if isNilCompleter(s.llm) {
		st.RCA = ruleRCA(st)
		return nil
	}
	if st.Ctx != nil && st.Ctx.ReactEnabled() {
		return s.react(ctx, st)
	}
	return s.singleCall(ctx, st)
}

// isNilCompleter 兼容 typed-nil：*llm.Client(nil) 包进接口后接口非 nil，需反射判断底层指针
func isNilCompleter(c ctxpkg.Completer) bool {
	if c == nil {
		return true
	}
	v := reflect.ValueOf(c)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return v.IsNil()
	}
	return false
}

func (s *RCAStage) react(ctx context.Context, st *State) error {
	q := ctxpkg.Query{
		Device: st.Alert.Device, Metric: st.Alert.Metric, Severity: st.Alert.Severity,
		Stage: "rca", RunID: st.Run.ID, Keywords: []string{st.Alert.Message},
	}
	as, err := st.Ctx.Assemble(q, true)
	if err != nil {
		st.RCA = ruleRCA(st)
		return nil
	}
	system := "你是算力中心硬件运维专家。基于上下文，若证据不足可调用工具补充，推理记录写入write_scratchpad。最终输出JSON: {\"final_answer\":{\"root_cause\":string,\"evidence_chain\":[string],\"confidence\":0~1,\"gaps\":[string]}}。除final_answer或tool_call外不输出其它内容。"
	res, err := st.Ctx.RunReact(ctx, s.llm, system, q, as.User)
	if err != nil || res.Final == nil {
		st.RCA = ruleRCA(st)
		return nil
	}
	var rc model.RCA
	if err := json.Unmarshal(res.Final, &rc); err != nil {
		st.RCA = ruleRCA(st)
		return nil
	}
	st.RCA = &rc
	return nil
}

func (s *RCAStage) singleCall(ctx context.Context, st *State) error {
	system := "你是算力中心硬件运维专家。基于给定证据输出JSON：{\"root_cause\":string,\"evidence_chain\":[string],\"confidence\":0~1,\"gaps\":[string]}。只输出JSON。"
	user := ""
	if st.Ctx != nil {
		if as, err := st.Ctx.Assemble(ctxpkg.Query{
			Device: st.Alert.Device, Metric: st.Alert.Metric, Severity: st.Alert.Severity,
			Stage: "rca", RunID: st.Run.ID, Keywords: []string{st.Alert.Message}}, false); err == nil {
			user = as.User + "\n"
		}
	}
	user += fmt.Sprintf("设备:%s 指标:%s\n证据:%s\n%s",
		st.Alert.Device, st.Alert.Metric,
		model.MustJSON(st.Evidence),
		extraGapHint(st))
	raw, err := s.llm.Complete(ctx, system, user)
	if err != nil {
		st.RCA = ruleRCA(st)
		st.RCA.Gaps = append(st.RCA.Gaps, "LLM调用失败，已使用规则兜底")
		return nil
	}
	rc, err := ParseRCAJSON(raw)
	if err != nil {
		st.RCA = ruleRCA(st)
		return nil
	}
	st.RCA = rc
	return nil
}

func extraGapHint(st *State) string {
	if st.Iteration > 0 {
		return "这是第" + fmt.Sprint(st.Iteration) + "轮补充取证，请重点分析新增证据。"
	}
	return "请基于当前证据给出根因结论。"
}

func ruleRCA(st *State) *model.RCA {
	metricRoots := map[string]string{
		"cpu_usage":  "CPU热点进程导致负载饱和",
		"disk_usage": "磁盘空间不足",
		"gpu_util":   "GPU掉卡或驱动异常",
		"mem_usage":  "内存泄漏或OOM杀进程",
		"net_in":     "网络链路中断",
	}
	root := metricRoots[st.Alert.Metric]
	if root == "" {
		root = "未知根因，需人工介入"
	}
	chain := []string{fmt.Sprintf("%s(%s) 指标异常", st.Alert.Device, st.Alert.Metric)}
	if st.Evidence != nil {
		for _, l := range st.Evidence.Logs {
			chain = append(chain, "日志: "+strings.TrimSpace(l.Content))
			if len(chain) >= 3 {
				break
			}
		}
		if len(st.Evidence.Metrics) > 0 {
			last := st.Evidence.Metrics[len(st.Evidence.Metrics)-1]
			chain = append(chain, fmt.Sprintf("指标末值: %.1f", last.Value))
		}
	}
	return &model.RCA{
		RootCause:     root,
		EvidenceChain: chain,
		Confidence:    0.7,
		Gaps:          st.EvidenceGaps(),
	}
}

func ParseRCAJSON(raw string) (*model.RCA, error) {
	var rc model.RCA
	if err := llm.ParseJSON(raw, &rc); err != nil {
		return nil, err
	}
	return &rc, nil
}
