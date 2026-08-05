package pipeline

import (
	"context"
	"fmt"
	"strings"

	"aiops/internal/llm"
	"aiops/internal/model"
)

type RCAStage struct {
	llm *llm.Client
}

func NewRCAStage(lc *llm.Client) *RCAStage { return &RCAStage{llm: lc} }

func (RCAStage) StageID() string { return "rca" }
func (RCAStage) Name() string    { return "根因分析" }

func (s *RCAStage) Run(ctx context.Context, st *State) error {
	if s.llm == nil {
		st.RCA = ruleRCA(st)
		return nil
	}
	system := "你是算力中心硬件运维专家。基于给定证据输出JSON：{\"root_cause\":string,\"evidence_chain\":[string],\"confidence\":0~1,\"gaps\":[string]}。只输出JSON。"
	user := fmt.Sprintf("设备:%s 指标:%s\n证据:%s\n%s",
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
