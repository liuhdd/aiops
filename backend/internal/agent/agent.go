package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"aiops/internal/llm"
	"aiops/internal/model"
	"aiops/internal/pipeline"
	"aiops/internal/store"
)

type Agent struct {
	store *store.Store
	llm   *llm.Client
	eng   *pipeline.Engine
}

func New(s *store.Store, lc *llm.Client, eng *pipeline.Engine) *Agent {
	return &Agent{store: s, llm: lc, eng: eng}
}

func (a *Agent) Handle(ctx context.Context, userText string) (model.Message, error) {
	userMsg := model.Message{
		ID: model.NewID("msg"), Role: model.RoleUser,
		Content: userText, CreatedAt: time.Now(),
	}
	_ = a.store.SaveMessage(userMsg)

	alertID := extractAlertID(userText, a.store)
	reply := "你好，我是AIOps运维助手。你可以问我：有哪些告警 / 查一下<告警ID>什么原因 / <告警ID>怎么处理。"

	switch ParseIntent(userText) {
	case ListAlerts:
		alerts, _ := a.store.ListAlerts()
		if len(alerts) == 0 {
			reply = "当前没有告警。"
		} else {
			var b strings.Builder
			b.WriteString("当前告警列表：\n")
			for _, al := range alerts {
				fmt.Fprintf(&b, "- %s [%s] %s %s\n", al.ID, al.Severity, al.Device, al.Message)
			}
			reply = b.String()
		}
	case InspectAlert, AskRootCause, AskRecommendation:
		if alertID == "" {
			reply = "请指明告警ID，例如：alarm-123 什么原因？"
		} else if a.eng != nil {
			alert, err := a.store.GetAlert(alertID)
			if err != nil {
				reply = fmt.Sprintf("未找到告警 %s。", alertID)
			} else {
				if _, runErr := a.store.GetRun("run-" + alertID); runErr != nil {
					_ = a.eng.Run(ctx, alert, nil)
				}
				time.Sleep(50 * time.Millisecond)
				reply = a.answerFor(alertID, ParseIntent(userText))
			}
		} else {
			reply = a.answerFor(alertID, ParseIntent(userText))
		}
	case AskStatus:
		reply = "告警状态可在左侧列表查看，点击告警查看排查时间线。"
	}

	assistantMsg := model.Message{
		ID: model.NewID("msg"), Role: model.RoleAssistant,
		Content: reply, CreatedAt: time.Now(),
	}
	if alertID != "" {
		assistantMsg.AlertID = alertID
	}
	_ = a.store.SaveMessage(assistantMsg)
	return assistantMsg, nil
}

func extractAlertID(text string, s *store.Store) string {
	low := strings.ToLower(text)
	if i := strings.Index(low, "alarm"); i >= 0 {
		rest := text[i+5:] // skip "alarm"
		rest = strings.TrimPrefix(rest, "-")
		digits := ""
		for _, r := range rest {
			isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
			if !isHex {
				break
			}
			digits += string(r)
		}
		if digits != "" {
			return "alarm-" + digits
		}
	}
	alerts, _ := s.ListAlerts()
	for _, a := range alerts {
		if strings.Contains(low, strings.ToLower(a.ID)) || strings.Contains(low, a.Device) {
			return a.ID
		}
	}
	return ""
}

func (a *Agent) answerFor(alertID string, intent Intent) string {
	run, err := a.store.GetRun("run-" + alertID)
	if err != nil {
		return fmt.Sprintf("告警 %s 尚未完成排查，请稍后再问。", alertID)
	}
	if run.Status == model.StatusRunning {
		return fmt.Sprintf("告警 %s 正在排查中，请稍后查看时间线。", alertID)
	}
	var rca model.RCA
	var rec model.Recommendation
	if st, ok, _ := a.store.GetLatestStage(run.ID, "rca"); ok {
		_ = llm.ParseJSON(st.Output, &rca)
	}
	if st, ok, _ := a.store.GetLatestStage(run.ID, "recommend"); ok {
		_ = llm.ParseJSON(st.Output, &rec)
	}
	switch intent {
	case AskRecommendation:
		if len(rec.Actions) > 0 {
			return fmt.Sprintf("处置建议：\n%s", strings.Join(rec.Actions, "\n"))
		}
	case AskRootCause:
		if rca.RootCause != "" {
			return fmt.Sprintf("根因：%s\n置信度：%.0f%%\n证据链：%s",
				rca.RootCause, rca.Confidence*100, strings.Join(rca.EvidenceChain, "；"))
		}
	}
	return fmt.Sprintf("告警 %s：%s", alertID, rca.RootCause)
}
