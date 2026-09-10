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
	ErrMsg    string    `json:"err_msg,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	Intent    string
	Intents   []string `json:"intents,omitempty"`
	Reply     string
	Outcome   RunOutcome
}

// RunTask 对单个任务跑一次 trial。lc 为 nil 时使用规则路径（LLM=false 配置强制 nil）。
// 超时由调用方负责：调用方创建带超时的 ctx 并同时传给 RunTask 与 Grade（覆盖流水线 + LLM 评委）。
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
		return runChat(ctx, st, eng, t, cfg, lc, cs, res)
	default:
		return runPipeline(ctx, st, eng, t, res)
	}
}

func runPipeline(ctx context.Context, st *store.Store, eng *pipeline.Engine, t Task, res *TrialResult) (*TrialResult, error) {
	if err := eng.Run(ctx, t.Alert, nil); err != nil {
		res.RunStatus = model.StatusFailed
		res.ErrMsg = err.Error()
		return res, err
	}
	runID := "run-" + t.Alert.ID
	run, err := st.GetRun(runID)
	if err != nil {
		res.RunStatus = model.StatusFailed
		res.ErrMsg = err.Error()
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

func runChat(ctx context.Context, st *store.Store, eng *pipeline.Engine, t Task, cfg Config, lc *llm.Client, cs *ctxpkg.Store, res *TrialResult) (*TrialResult, error) {
	// 需要 alert 供流水线触发；chat 任务若引用设备，预先写入一条告警
	if _, err := ensureAlertForChat(st, t); err != nil {
		res.RunStatus = "chat-failed"
		res.ErrMsg = err.Error()
		return res, err
	}
	var ag *agent.Agent
	if cfg.LLM && !isNilClient(lc) {
		// 对齐生产：注入主 client（eval 中复用为轻模型），Handle 走多意图路径
		ag = agent.NewWithLight(st, lc, lc, eng, cs, cfg.TopK, 3)
	} else {
		ag = agent.New(st, nil, eng)
	}
	msg, err := ag.Handle(ctx, "", t.ChatInput)
	if err != nil {
		res.RunStatus = "chat-failed"
		res.ErrMsg = err.Error()
		return res, err
	}
	res.Reply = msg.Content
	res.RunStatus = "chat"
	var light ctxpkg.Completer
	if cfg.LLM && !isNilClient(lc) {
		light = lc
	}
	res.Intents = intentNames(agent.ParseIntentSet(ctx, nil, t.ChatInput, light, 3))
	if len(res.Intents) > 0 {
		res.Intent = res.Intents[0]
	}
	return res, nil
}

func ensureAlertForChat(st *store.Store, t Task) (model.Alert, error) {
	// chat 反例/不涉告警：直接返回空
	if t.Expected.Intent == "Unknown" || t.Expected.Intent == "ListAlerts" || t.Expected.Intent == "AskStatus" {
		return model.Alert{}, nil
	}
	// 其余 chat 任务需要一个真实告警（用对应 pipeline 任务的 device/metric）
	// 复用 disk/gpu/switch 场景
	scene := map[string][4]string{
		"chat-root-cause-db":             {"db-01", "disk_usage", "critical", "磁盘使用率超过95%"},
		"chat-recommend-gpu":             {"compute-02", "gpu_util", "critical", "GPU掉卡，利用率归零"},
		"chat-inspect-switch":            {"switch-core-01", "net_in", "critical", "核心交换机入口流量中断"},
		"chat-multi":                     {"db-01", "disk_usage", "critical", "磁盘使用率超过95%"},
		"chat-disk-reason-2":             {"db-01", "disk_usage", "critical", "磁盘使用率超过95%"},
		"chat-root-cause-cpu":            {"compute-01", "cpu_usage", "warning", "CPU使用率持续高于85%"},
		"chat-recommend-disk":            {"db-01", "disk_usage", "critical", "磁盘使用率超过95%"},
		"chat-root-cause-gpu":            {"compute-02", "gpu_util", "critical", "GPU掉卡，利用率归零"},
		"chat-recommend-mem":             {"web-01", "mem_usage", "warning", "内存使用率持续高于90%"},
		"chat-root-cause-net":            {"switch-core-01", "net_in", "critical", "核心交换机入口流量中断"},
		"chat-recommend-storage":         {"storage-01", "disk_usage", "critical", "磁盘使用率超过95%"},
		"chat-multi-rootcause-recommend": {"db-01", "disk_usage", "critical", "磁盘使用率超过95%"},
		"chat-multi-three":               {"compute-02", "gpu_util", "critical", "GPU掉卡，利用率归零"},
	}[t.ID]
	dev, metric, sev, msg := scene[0], scene[1], scene[2], scene[3]
	if t.ID == "chat-multi-recommend" {
		now := time.Now()
		for _, id := range []string{"alarm-1", "alarm-2"} {
			a := model.Alert{ID: id, Device: "db-01", Metric: "disk_usage",
				Severity: "critical", Message: "磁盘使用率超过95%", Status: "active",
				FiredAt: now, CreatedAt: now}
			if err := st.SaveAlert(a); err != nil {
				return a, err
			}
		}
		return model.Alert{}, nil
	}
	if scene == ([4]string{}) {
		return model.Alert{}, nil
	}
	alertID := "eval-chat-" + t.ID
	if t.ID == "chat-multi" {
		// chat-multi 输入显式引用 alarm-1，需按真实告警 ID 播种
		alertID = "alarm-1"
	}
	alert := model.Alert{
		ID: alertID, Device: dev, Metric: metric,
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
