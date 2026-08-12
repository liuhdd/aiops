package eval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"aiops/internal/agent"
	ctxpkg "aiops/internal/context"
	"aiops/internal/coordinator"
	"aiops/internal/llm"
	"aiops/internal/model"
	"aiops/internal/pipeline"
	"aiops/internal/store"
)

// Config 一种评测配置（配置对比矩阵）
type Config struct {
	Name  string // "rule" | "llm-single" | "llm-react"
	LLM   bool   // 是否注入 llm.Client
	React bool
	TopK  int
}

// EvalConfigs 全部评测配置
var EvalConfigs = []Config{
	{Name: ConfigRule, LLM: false, React: false, TopK: 0},
	{Name: ConfigLLMSingle, LLM: true, React: false, TopK: 3},
	{Name: ConfigLLMReact, LLM: true, React: true, TopK: 3},
}

// RunOutcome 单次运行的结果（Outcome = 环境真实变化）
type RunOutcome struct {
	RunStatus     string
	Stages        []model.StageResult
	RCA           model.RCA
	Evidence      model.Evidence
	Recommend     model.Recommendation
	ToolEvents    []model.ToolEvent
	Scratchpad    []model.ScratchpadEntry
	DeviceContext model.DeviceContext
	LTM           []model.LongTermMemory
}

// TrialResult 一次 trial 的原始结果（Transcript + Outcome）
type TrialResult struct {
	Trial     int
	Config    string
	RunStatus string
	Intent    string
	Reply     string
	Outcome   RunOutcome
}

// RunTask 对单个任务跑一次 trial。lc 为 nil 时使用规则路径（LLM=false 配置强制 nil）。
func RunTask(ctx context.Context, t Task, cfg Config, lc *llm.Client, trial int) (*TrialResult, error) {
	dir, err := os.MkdirTemp("", "eval-")
	if err != nil {
		return nil, fmt.Errorf("MkdirTemp: %w", err)
	}
	defer os.RemoveAll(dir)
	st, err := store.New(filepath.Join(dir, "t.db"))
	if err != nil {
		return nil, fmt.Errorf("store.New: %w", err)
	}
	defer st.Close()

	var cl *llm.Client
	if cfg.LLM && !isNilClient(lc) {
		cl = lc
	}
	cs := ctxpkg.NewStore(st)
	reg := ctxpkg.NewRegistry()
	for _, tl := range ctxpkg.DefaultTools() {
		reg.Register(tl)
	}
	eng := pipeline.BuildWithContext(st, cl, coordinator.New(), cs, reg, cfg.React, cfg.TopK)

	res := &TrialResult{Trial: trial, Config: cfg.Name}

	switch t.Object {
	case ObjectChat:
		return runChat(ctx, st, eng, t, res)
	default:
		return runPipeline(ctx, st, eng, t, res)
	}
}

func runPipeline(ctx context.Context, st *store.Store, eng *pipeline.Engine, t Task, res *TrialResult) (*TrialResult, error) {
	if err := eng.Run(ctx, t.Alert, nil); err != nil {
		res.RunStatus = model.StatusFailed
		return res, err
	}
	runID := "run-" + t.Alert.ID
	run, err := st.GetRun(runID)
	if err != nil {
		return res, err
	}
	res.RunStatus = run.Status
	res.Outcome.RunStatus = run.Status
	stages, _ := st.ListStages(runID)
	res.Outcome.Stages = stages
	if stg, ok, _ := st.GetLatestStage(runID, "rca"); ok {
		_ = llm.ParseJSON(stg.Output, &res.Outcome.RCA)
	}
	if stg, ok, _ := st.GetLatestStage(runID, "evidence"); ok {
		_ = llm.ParseJSON(stg.Output, &res.Outcome.Evidence)
	}
	if stg, ok, _ := st.GetLatestStage(runID, "recommend"); ok {
		_ = llm.ParseJSON(stg.Output, &res.Outcome.Recommend)
	}
	res.Outcome.ToolEvents, _ = st.ListToolEvents(runID)
	res.Outcome.Scratchpad, _ = st.ListScratchpad(runID)
	res.Outcome.LTM, _ = st.ListLongTermMemory(t.Alert.Device, t.Alert.Metric)
	if dc, err := st.GetDeviceContext(t.Alert.Device); err == nil {
		res.Outcome.DeviceContext = dc
	}
	return res, nil
}

func runChat(ctx context.Context, st *store.Store, eng *pipeline.Engine, t Task, res *TrialResult) (*TrialResult, error) {
	// 需要 alert 供流水线触发；chat 任务若引用设备，预先写入一条告警
	if _, err := ensureAlertForChat(st, t); err != nil {
		return res, err
	}
	// agent.New(s, lc, eng)：规则路径下 lc 传 nil，Handle 走 answerFor
	ag := agent.New(st, nil, eng)
	msg, err := ag.Handle(ctx, t.ChatInput)
	if err != nil {
		return res, err
	}
	res.Reply = msg.Content
	res.RunStatus = "chat"
	res.Intent = intentName(agent.ParseIntent(t.ChatInput))
	return res, nil
}

func ensureAlertForChat(st *store.Store, t Task) (model.Alert, error) {
	// chat 反例/不涉告警：直接返回空
	if t.Expected.Intent == "Unknown" || t.Expected.Intent == "ListAlerts" {
		return model.Alert{}, nil
	}
	// 其余 chat 任务需要一个真实告警（用对应 pipeline 任务的 device/metric）
	// 复用 disk/gpu/switch 场景
	scene := map[string][4]string{
		"chat-root-cause-db":  {"db-01", "disk_usage", "critical", "磁盘使用率超过95%"},
		"chat-recommend-gpu":  {"compute-02", "gpu_util", "critical", "GPU掉卡，利用率归零"},
		"chat-inspect-switch": {"switch-core-01", "net_in", "critical", "核心交换机入口流量中断"},
	}[t.ID]
	dev, metric, sev, msg := scene[0], scene[1], scene[2], scene[3]
	alert := model.Alert{
		ID: "eval-chat-" + t.ID, Device: dev, Metric: metric,
		Severity: sev, Message: msg, Status: "active",
		FiredAt: time.Now(), CreatedAt: time.Now(),
	}
	if err := st.SaveAlert(alert); err != nil {
		return alert, err
	}
	return alert, nil
}

func isNilClient(lc *llm.Client) bool {
	return lc == nil
}

// intentName 将 agent.Intent 转为字符串名（与 Task 3 的 intentNameStr 等价，独立定义避免重复声明）
func intentName(i agent.Intent) string {
	switch i {
	case agent.AskRootCause:
		return "AskRootCause"
	case agent.AskRecommendation:
		return "AskRecommendation"
	case agent.InspectAlert:
		return "InspectAlert"
	case agent.ListAlerts:
		return "ListAlerts"
	case agent.AskStatus:
		return "AskStatus"
	}
	return "Unknown"
}
