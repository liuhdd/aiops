package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	ctxpkg "aiops/internal/context"
	"aiops/internal/llm"
	"aiops/internal/mockdata"
	"aiops/internal/model"
	"aiops/internal/pipeline"
	"aiops/internal/prompt"
	"aiops/internal/retrieve"
	"aiops/internal/store"
)

type Agent struct {
	store    *store.Store
	llm      ctxpkg.Completer
	lightLLM ctxpkg.Completer // 轻量意图模型（重写+拆分+分类）
	maxSplit int              // 多意图拆分上限
	eng      *pipeline.Engine
	ctxStore *ctxpkg.Store
	topK     int
	leaves   func() []model.LeafSpec // 活跃叶子 provider；nil 回退 SeedLeaves
	search   retrieve.Searcher       // 混合检索器；nil 时知识源回退库内全量扫描
}

// SetSearcher 注入混合检索器（供组装器知识源使用）。
func (a *Agent) SetSearcher(s retrieve.Searcher) { a.search = s }

func New(s *store.Store, lc ctxpkg.Completer, eng *pipeline.Engine) *Agent {
	return NewWithLight(s, lc, lc, eng, nil, 3, 3)
}

func NewWithContext(s *store.Store, lc ctxpkg.Completer, eng *pipeline.Engine, cs *ctxpkg.Store, topK int) *Agent {
	return NewWithLight(s, lc, lc, eng, cs, topK, 3)
}

func NewWithLight(s *store.Store, lc, light ctxpkg.Completer, eng *pipeline.Engine, cs *ctxpkg.Store, topK, maxSplit int) *Agent {
	return NewWithLeaves(s, lc, light, eng, cs, topK, maxSplit, nil)
}

func NewWithLeaves(s *store.Store, lc, light ctxpkg.Completer, eng *pipeline.Engine, cs *ctxpkg.Store, topK, maxSplit int, leaves func() []model.LeafSpec) *Agent {
	return &Agent{store: s, llm: lc, lightLLM: light, maxSplit: maxSplit, eng: eng, ctxStore: cs, topK: topK, leaves: leaves}
}

func (a *Agent) buildAssembled(conversationID, device, metric string, intents []string) *ctxpkg.Assembled {
	if a.ctxStore == nil || device == "" || metric == "" {
		return nil
	}
	asm := ctxpkg.NewAssembler(a.ctxStore, a.topK)
	asm.SetSearcher(a.search)
	asm.SetLeaves(a.leaves)
	as, err := asm.Assemble(ctxpkg.Query{Device: device, Metric: metric, Stage: "chat", Intents: intents, ConversationID: conversationID, Profile: "chat"}, false, nil)
	if err != nil {
		return nil
	}
	return as
}

func (a *Agent) memorySuffix(conversationID, device, metric string) string {
	as := a.buildAssembled(conversationID, device, metric, nil)
	if as == nil || !strings.Contains(as.User, "【设备历史经验】") {
		return ""
	}
	// 取长期记忆段落首行摘要
	for _, line := range strings.Split(as.User, "\n") {
		if strings.HasPrefix(line, "上次(") {
			return "\n参考历史经验：" + line
		}
	}
	return ""
}

func (a *Agent) Handle(ctx context.Context, conversationID string, userText string) (model.Message, error) {
	ctx = retrieve.WithProfile(ctx, "chat")
	userMsg := model.Message{
		ID: model.NewID("msg"), Role: model.RoleUser,
		Content: userText, ConversationID: conversationID, CreatedAt: time.Now(),
	}
	_ = a.store.SaveMessage(userMsg)
	if conv, err := a.store.GetConversation(conversationID); err == nil && conv.Title == "" {
		_ = a.store.SetConversationTitle(conversationID, titleFor(userText))
	}
	reply := a.answer(ctx, conversationID, userText)
	assistantMsg := model.Message{
		ID: model.NewID("msg"), Role: model.RoleAssistant,
		Content: reply, ConversationID: conversationID, CreatedAt: time.Now(),
	}
	if alertID := extractAlertID(userText, a.store); alertID != "" {
		assistantMsg.AlertID = alertID
	}
	_ = a.store.SaveMessage(assistantMsg)
	_ = a.store.TouchConversation(conversationID)
	return assistantMsg, nil
}

// titleFor 取首条消息前 20 个 rune 作为会话标题。
func titleFor(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) > 20 {
		runes = runes[:20]
	}
	return string(runes)
}

// answer 多意图优先：有轻量 LLM 时重写拆分后逐子问题聚合；否则回退单意图规则路径。
func (a *Agent) answer(ctx context.Context, conversationID string, userText string) string {
	if a.lightLLM == nil || ctxpkg.IsNilCompleter(a.lightLLM) {
		return a.answerSingle(ctx, conversationID, userText)
	}
	_, subs, err := RewriteAndSplit(ctx, userText, a.lightLLM)
	if err != nil || len(subs) == 0 {
		return a.answerSingle(ctx, conversationID, userText)
	}
	// 原文已有明确的规则意图，但重写拆分出的子问题全部丢失该意图（LLM 常把单意图
	// 问题拆成含"告警"等宽泛词的碎片，被规则误判为 ListAlerts）→ 回退单意图路径，
	// 以原文为准，避免"什么原因"被重写成告警列表。
	if sc := RuleScore(a.activeLeaves(), userText); sc.Confidence >= 0.4 && !a.subsKeepIntent(subs, sc.Intent) {
		return a.answerSingle(ctx, conversationID, userText)
	}
	history := a.fetchHistory(conversationID, userText)
	if len(subs) == 1 {
		qText := a.rewrite(ctx, history, subs[0])
		return a.answerOne(ctx, conversationID, qText)
	}
	limit := a.maxSplit
	if limit <= 0 {
		limit = 3
	}
	var b strings.Builder
	for i, sub := range subs {
		if i >= limit {
			fmt.Fprintf(&b, "[%d] %s → skipped（超出上限 %d）\n", i+1, sub, limit)
			continue
		}
		qText := a.rewrite(ctx, history, sub)
		content, status := a.answerOneStatus(ctx, conversationID, qText)
		fmt.Fprintf(&b, "[%d] %s → %s\n%s\n", i+1, sub, status, content)
	}
	return strings.TrimRight(b.String(), "\n")
}

// subsKeepIntent 返回拆分出的子问题中是否有任一命中目标意图（规则）。
func (a *Agent) subsKeepIntent(subs []string, intent Intent) bool {
	for _, s := range subs {
		if RuleScore(a.activeLeaves(), s).Intent == intent {
			return true
		}
	}
	return false
}

// activeLeaves 返回当前活跃叶子（provider 为空/返回空时回退 SeedLeaves）。
func (a *Agent) activeLeaves() []model.LeafSpec {
	if a.leaves != nil {
		if l := a.leaves(); len(l) > 0 {
			return l
		}
	}
	return SeedLeaves
}

// placeholderReply KB/MCP/未知叶子的占位回复；Unknown 返回引导语。
func (a *Agent) placeholderReply(code Intent) string {
	if code == Unknown {
		return a.guideText()
	}
	return "已识别该意图，绑定的知识库/MCP 尚未实现。"
}

// answerSingle 无轻量 LLM 时的单意图规则路径（原 Handle 逻辑）。
func (a *Agent) answerSingle(ctx context.Context, conversationID string, userText string) string {
	alertID := extractAlertID(userText, a.store)
	intent := RuleMatch(a.activeLeaves(), userText)
	switch intent {
	case ListAlerts:
		return a.listAlertsReply()
	case InspectAlert, AskRootCause, AskRecommendation:
		if alertID == "" {
			return "请指明告警ID，例如：alarm-123 什么原因？"
		}
		alert, err := a.store.GetAlert(alertID)
		if err != nil {
			return fmt.Sprintf("未找到告警 %s。", alertID)
		}
		if a.eng != nil {
			if err := a.ensureInvestigation(ctx, alert); err != nil {
				return fmt.Sprintf("告警 %s 排查失败：%v", alertID, err)
			}
		}
		if !ctxpkg.IsNilCompleter(a.llm) {
			return a.answerWithLLM(ctx, conversationID, alertID, intent, userText)
		}
		return a.answerFor(conversationID, alertID, intent)
	case AskStatus:
		return "告警状态可在左侧列表查看，点击告警查看排查时间线。"
	default:
		if !isSystemIntent(intent) {
			if content, ok := a.kbReply(ctx, conversationID, intent, userText); ok {
				return content
			}
		}
		return a.placeholderReply(intent)
	}
}

// answerOne 单子问题的回复（不带头部状态行）。
func (a *Agent) answerOne(ctx context.Context, conversationID string, sub string) string {
	content, _ := a.answerOneStatus(ctx, conversationID, sub)
	return content
}

// answerOneStatus 单个子问题：规则意图优先，Unknown 用轻量 LLM 分类；返回内容与状态。
func (a *Agent) answerOneStatus(ctx context.Context, conversationID string, sub string) (string, string) {
	intent, _ := ParseIntentWithLight(ctx, a.activeLeaves(), sub, a.lightLLM)
	switch intent {
	case ListAlerts:
		return a.listAlertsReply(), "success"
	case AskStatus:
		return "告警状态可在左侧列表查看，点击告警查看排查时间线。", "success"
	case InspectAlert, AskRootCause, AskRecommendation:
		alertID := extractAlertID(sub, a.store)
		if alertID == "" {
			return "请指明告警ID，例如：alarm-123 什么原因？", "no_alert"
		}
		alert, err := a.store.GetAlert(alertID)
		if err != nil {
			return fmt.Sprintf("未找到告警 %s。", alertID), "no_alert"
		}
		if a.eng != nil {
			if err := a.ensureInvestigation(ctx, alert); err != nil {
				return fmt.Sprintf("告警 %s 排查失败：%v", alertID, err), "failed"
			}
		}
		if !ctxpkg.IsNilCompleter(a.llm) {
			return a.answerWithLLM(ctx, conversationID, alertID, intent, sub), "success"
		}
		return a.answerFor(conversationID, alertID, intent), "success"
	default:
		if !isSystemIntent(intent) {
			if content, ok := a.kbReply(ctx, conversationID, intent, sub); ok {
				return content, "success"
			}
			return a.placeholderReply(intent), statusFor(intent)
		}
		return a.placeholderReply(intent), statusFor(intent)
	}
}

// ensureInvestigation 执行排查并传播已记录的失败。 / Run an investigation and surface persisted failures.
func (a *Agent) ensureInvestigation(ctx context.Context, alert model.Alert) error {
	runID := "run-" + alert.ID
	run, err := a.store.GetRun(runID)
	if errors.Is(err, sql.ErrNoRows) {
		return a.eng.Run(ctx, alert, nil)
	}
	if err != nil {
		return fmt.Errorf("read investigation %q: %w", runID, err)
	}
	if run.Status == model.StatusFailed {
		stage, ok, err := a.store.GetLatestStage(runID, "rca")
		if err != nil {
			return fmt.Errorf("read failed RCA for run %q: %w", runID, err)
		}
		if ok && stage.Error != "" {
			return fmt.Errorf("RCA failed for run %q: %s", runID, stage.Error)
		}
		return fmt.Errorf("investigation %q failed; inspect its pipeline timeline", runID)
	}
	return nil
}

// statusFor 意图对应的子问题状态行。
func statusFor(code Intent) string {
	if code == Unknown {
		return "unknown"
	}
	return "success"
}

// isSystemIntent 内置 SYSTEM 意图（种子 6 个）。
func isSystemIntent(code Intent) bool {
	switch code {
	case Unknown, ListAlerts, InspectAlert, AskRootCause, AskRecommendation, AskStatus:
		return true
	}
	return false
}

// kbReply 按意图 code 检索知识库并返回命中文档行；无命中返回 ok=false。
// question 作为检索文本，使 Searcher 的 BM25/向量/信号通道都能参与召回；
// 每路径的 question 已由 agent 预先改写（见 rewrite），故不再下传 History 以免二次改写。
func (a *Agent) kbReply(ctx context.Context, conversationID, code, question string) (string, bool) {
	if a.ctxStore == nil {
		return "", false
	}
	asm := ctxpkg.NewAssembler(a.ctxStore, a.topK)
	asm.SetSearcher(a.search)
	asm.SetLeaves(a.leaves)
	as, err := asm.Assemble(ctxpkg.Query{Text: question, Stage: "chat", Intents: []string{code}, ConversationID: conversationID, Profile: "chat"}, false, nil)
	if err != nil {
		return "", false
	}
	var lines []string
	for _, c := range as.Selected {
		if c.Source != ctxpkg.SourceKnowledge {
			continue
		}
		// 仅保留 intents 元数据与意图码精确相交的块，避免无关文档混入。
		if !ctxpkg.MatchesIntent(c.Metadata["intents"], code) {
			continue
		}
		lines = append(lines, "- "+c.Content)
	}
	if len(lines) == 0 {
		return "", false
	}
	return "知识库相关文档：\n" + strings.Join(lines, "\n"), true
}

func (a *Agent) listAlertsReply() string {
	alerts, _ := a.store.ListAlerts()
	if len(alerts) == 0 {
		return "当前没有告警。"
	}
	var b strings.Builder
	b.WriteString("当前告警列表：\n")
	for _, al := range alerts {
		fmt.Fprintf(&b, "- %s [%s] %s %s\n", al.ID, al.Severity, al.Device, al.Message)
	}
	return b.String()
}

// guideText Unknown 意图的增强引导语。
func (a *Agent) guideText() string {
	return "你好，我是AIOps运维助手。我可以帮你：\n- 查看告警列表：如\"有哪些告警\"\n- 排查单个告警：如\"查一下 alarm-123 什么原因\"\n- 获取处置建议：如\"alarm-123 怎么处理\"\n- 查看排查进度：如\"排查进度如何\"\n如果输入和以上无关，请重新描述你的问题。"
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

func (a *Agent) answerFor(conversationID, alertID string, intent Intent) string {
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
	reply := ""
	switch intent {
	case AskRecommendation:
		if len(rec.Actions) > 0 {
			reply = fmt.Sprintf("处置建议：\n%s", strings.Join(rec.Actions, "\n"))
		}
	case AskRootCause:
		if rca.RootCause != "" {
			reply = fmt.Sprintf("根因：%s\n置信度：%.0f%%\n证据链：%s",
				rca.RootCause, rca.Confidence*100, strings.Join(rca.EvidenceChain, "；"))
		}
	}
	if reply == "" {
		reply = fmt.Sprintf("告警 %s：%s", alertID, rca.RootCause)
	}
	// 长期记忆附加
	alert, err := a.store.GetAlert(alertID)
	if err == nil {
		reply += a.memorySuffix(conversationID, alert.Device, alert.Metric)
	}
	return reply
}

func (a *Agent) answerWithLLM(ctx context.Context, conversationID, alertID string, intent Intent, userText string) string {
	alert, err := a.store.GetAlert(alertID)
	if err != nil {
		return a.answerFor(conversationID, alertID, intent)
	}
	var rca model.RCA
	var rec model.Recommendation
	if run, rerr := a.store.GetRun("run-" + alertID); rerr == nil {
		if st, ok, _ := a.store.GetLatestStage(run.ID, "rca"); ok {
			_ = llm.ParseJSON(st.Output, &rca)
		}
		if st, ok, _ := a.store.GetLatestStage(run.ID, "recommend"); ok {
			_ = llm.ParseJSON(st.Output, &rec)
		}
	}
	ctxStr := ""
	system := prompt.ChatAnswerSystem()
	if as := a.buildAssembled(conversationID, alert.Device, alert.Metric, []string{intent}); as != nil {
		ctxStr = as.User
		system += "\n" + as.System
	}
	user := prompt.ChatAnswerUser(alertID, alert.Device, alert.Metric,
		rca.RootCause, strings.Join(rec.Actions, "；"), ctxStr, userText)
	raw, cerr := a.llm.Complete(ctx, system, user)
	if cerr != nil || strings.TrimSpace(raw) == "" {
		return a.answerFor(conversationID, alertID, intent)
	}
	return raw
}

func extractQuery(text string, s *store.Store) (device, metric string) {
	low := strings.ToLower(text)
	knownMetrics := []string{"cpu_usage", "disk_usage", "gpu_util", "mem_usage", "net_in"}
	for _, m := range knownMetrics {
		if strings.Contains(low, m) {
			metric = m
			break
		}
	}
	for _, d := range mockdata.Devices() {
		if strings.Contains(low, strings.ToLower(d.Name)) {
			device = d.Name
			break
		}
	}
	return device, metric
}

// fetchHistory 取近 6 条对话历史，排除当前轮的用户原文（Handle 已先落库）。
func (a *Agent) fetchHistory(conversationID, currentUserText string) []string {
	var msgs []model.Message
	var err error
	if a.store != nil {
		msgs, err = a.store.RecentMessages(conversationID, 6)
	} else if a.ctxStore != nil {
		msgs, err = a.ctxStore.RecentMessages(conversationID, 6)
	}
	if err != nil || len(msgs) == 0 {
		return nil
	}
	// RecentMessages 返回时间升序；末条若为当前轮原文则剔除
	if len(msgs) > 0 && msgs[len(msgs)-1].Content == currentUserText {
		msgs = msgs[:len(msgs)-1]
	}
	if len(msgs) > 6 {
		msgs = msgs[len(msgs)-6:]
	}
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if strings.TrimSpace(m.Content) != "" {
			out = append(out, m.Content)
		}
	}
	return out
}

// rewrite 用会话历史把子问题改写为独立问句；与链路 rewrite=llm 复用同一实现
// （retrieve.RewriteQuery），保证「单一改写来源」。light 为空/无历史直通，
// 失败或空输出回退原文。
func (a *Agent) rewrite(ctx context.Context, history []string, q string) string {
	if a.lightLLM == nil || ctxpkg.IsNilCompleter(a.lightLLM) {
		return q
	}
	rw, err := retrieve.RewriteQuery(ctx, retrieveCompleter{c: a.lightLLM}, history, q)
	if err != nil || strings.TrimSpace(rw) == "" {
		return q
	}
	return rw
}

// retrieveCompleter 把 ctxpkg.Completer(双参) 适配为 retrieve.Completer(单 prompt)。
type retrieveCompleter struct{ c ctxpkg.Completer }

func (r retrieveCompleter) Complete(ctx context.Context, prompt string) (string, error) {
	return r.c.Complete(ctx, "", prompt)
}
