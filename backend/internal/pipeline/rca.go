package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	ctxpkg "aiops/internal/context"
	"aiops/internal/llm"
	"aiops/internal/model"
	"aiops/internal/prompt"
)

type RCAStage struct {
	llm ctxpkg.Completer
}

func NewRCAStage(lc ctxpkg.Completer) *RCAStage { return &RCAStage{llm: lc} }

func (RCAStage) StageID() string { return "rca" }
func (RCAStage) Name() string    { return "根因分析" }

func (s *RCAStage) Run(ctx context.Context, st *State) error {
	st.RCA = nil
	if ctxpkg.IsNilCompleter(s.llm) {
		return fmt.Errorf("RCA 缺少 LLM，告警=%s 设备=%s 指标=%s: %w", st.Alert.ID, st.Alert.Device, st.Alert.Metric, llm.ErrNoAPIKey)
	}
	if st.Ctx == nil {
		return fmt.Errorf("RCA 缺少上下文引擎，告警=%s 设备=%s 指标=%s", st.Alert.ID, st.Alert.Device, st.Alert.Metric)
	}
	return s.react(ctx, st)
}

func (s *RCAStage) react(ctx context.Context, st *State) error {
	q := ctxpkg.Query{
		Device: st.Alert.Device, Metric: st.Alert.Metric, Severity: st.Alert.Severity,
		Stage: "rca", RunID: st.Run.ID, Keywords: []string{st.Alert.Message},
		Intents: []string{"AskRootCause"},
	}
	as, err := st.Ctx.Assemble(q, true)
	if err != nil {
		return fmt.Errorf("RCA 上下文组装失败，告警=%s: %w", st.Alert.ID, err)
	}
	system := prompt.RCAReactSystem()
	res, err := st.Ctx.RunReact(ctx, s.llm, system, q, as.User)
	if err != nil {
		return fmt.Errorf("RCA ReAct 执行失败，告警=%s: %w", st.Alert.ID, err)
	}
	if len(res.Final) == 0 {
		return fmt.Errorf("RCA ReAct 达到最大步数仍未返回 final_answer，告警=%s", st.Alert.ID)
	}
	var rc struct {
		RootCause     *string   `json:"root_cause"`
		EvidenceChain *[]string `json:"evidence_chain"`
		Confidence    *float64  `json:"confidence"`
		Gaps          *[]string `json:"gaps"`
	}
	if err := json.Unmarshal(res.Final, &rc); err != nil {
		return fmt.Errorf("RCA final_answer 解析失败，告警=%s 响应=%s: %w", st.Alert.ID, res.Final, err)
	}
	if rc.RootCause == nil || rc.EvidenceChain == nil || rc.Confidence == nil || rc.Gaps == nil {
		return fmt.Errorf("RCA final_answer 必须包含 root_cause、evidence_chain、confidence、gaps，告警=%s 响应=%s", st.Alert.ID, res.Final)
	}
	if strings.TrimSpace(*rc.RootCause) == "" || *rc.Confidence < 0 || *rc.Confidence > 1 {
		return fmt.Errorf("RCA final_answer 的 root_cause 必须非空且 confidence 必须在 0 到 1 之间，告警=%s 响应=%s", st.Alert.ID, res.Final)
	}
	st.RCA = &model.RCA{
		RootCause: *rc.RootCause, EvidenceChain: *rc.EvidenceChain,
		Confidence: *rc.Confidence, Gaps: *rc.Gaps,
	}
	return nil
}
