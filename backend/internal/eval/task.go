package eval

import (
	"fmt"

	"aiops/internal/model"
)

// Object 评测对象
const (
	ObjectPipeline = "pipeline"
	ObjectChat     = "chat"
	ObjectContext  = "context"
)

// 配置名
const (
	ConfigRule      = "rule"
	ConfigLLMSingle = "llm-single"
	ConfigLLMReact  = "llm-react"
)

// Task 一份评测"考题"：明确输入 + 无歧义 ground-truth
type Task struct {
	ID        string      // "disk-full-db-01" 等
	Object    string      // pipeline | chat | context
	Alert     model.Alert // 输入告警
	Expected  Expected    // ground-truth
	Configs   []string    // 适用配置；空=全部
	ChatInput string      // Object==chat 时用户输入
}

// Expected ground-truth
type Expected struct {
	RootCause []string // 期望根因关键词（任一命中即通过）
	Metrics   []string // 期望证据指标
	Logs      []string // 期望日志关键字
	NoGaps    bool     // 期望证据/根因无 gaps
	Runbook   string   // 期望 runbook_ref
	Intent    string   // chat: 期望意图
	ToolUse   []string // context: 期望调用的工具
}

// 告警消息模板（对齐 simulator.Templates 与 mockdata scenario）
func mkAlert(device, metric, severity, message string) model.Alert {
	return model.Alert{
		ID:       "eval-" + device + "-" + metric, // 唯一，避免 triage 对 run ID 冲突
		Device:   device,
		Metric:   metric,
		Severity: severity,
		Message:  message,
		Status:   "active",
	}
}

// DefaultTasks 内置任务集（基于 mockdata 已知异常，ground-truth 对齐 scenario()/日志模板/ruleRCA/runbooks）
func DefaultTasks() []Task {
	tasks := []Task{
		// ---- L1 流水线主任务（5 个已知异常场景） ----
		{
			ID: "cpu-hot-compute-01", Object: ObjectPipeline,
			Alert: mkAlert("compute-01", "cpu_usage", "warning", "CPU使用率持续高于85%"),
			Expected: Expected{
				RootCause: []string{"CPU热点进程", "负载饱和"},
				Metrics:   []string{"cpu_usage"},
				Logs:      []string{"cpu"},
				NoGaps:    true, Runbook: "cpu_usage",
			},
		},
		{
			ID: "disk-full-db-01", Object: ObjectPipeline,
			Alert: mkAlert("db-01", "disk_usage", "critical", "磁盘使用率超过95%"),
			Expected: Expected{
				RootCause: []string{"磁盘空间不足"},
				Metrics:   []string{"disk_usage"},
				Logs:      []string{"no space"},
				NoGaps:    true, Runbook: "disk_usage",
			},
		},
		{
			ID: "gpu-drop-compute-02", Object: ObjectPipeline,
			Alert: mkAlert("compute-02", "gpu_util", "critical", "GPU掉卡，利用率归零"),
			Expected: Expected{
				RootCause: []string{"GPU掉卡", "驱动异常"},
				Metrics:   []string{"gpu_util"},
				Logs:      []string{"Xid", "fallen off the bus"},
				NoGaps:    true, Runbook: "gpu_util",
			},
		},
		{
			ID: "mem-oom-web-01", Object: ObjectPipeline,
			Alert: mkAlert("web-01", "mem_usage", "warning", "内存使用率持续高于90%"),
			Expected: Expected{
				RootCause: []string{"内存泄漏", "OOM"},
				Metrics:   []string{"mem_usage"},
				Logs:      []string{"OutOfMemory"},
				NoGaps:    true, Runbook: "mem_usage",
			},
		},
		{
			ID: "net-down-switch", Object: ObjectPipeline,
			Alert: mkAlert("switch-core-01", "net_in", "critical", "核心交换机入口流量中断"),
			Expected: Expected{
				RootCause: []string{"网络链路中断"},
				Metrics:   []string{"net_in"},
				Logs:      []string{"link down"},
				NoGaps:    true, Runbook: "net_in",
			},
		},
		// ---- L2 边界任务（LLM 超越规则） ----
		{
			ID: "gpu-temp-compute-02", Object: ObjectPipeline,
			Alert: mkAlert("compute-02", "gpu_temp", "critical", "GPU温度过高告警"),
			Expected: Expected{
				RootCause: []string{"温度过高", "散热", "过温"},
				Metrics:   []string{"gpu_temp"},
				Logs:      []string{"NVRM"},
				NoGaps:    true,
			},
		},
		// ---- L3 Chat Agent 任务（含正反例） ----
		{
			ID: "chat-root-cause-db", Object: ObjectChat,
			ChatInput: "为什么 db-01 磁盘满了？",
			Expected:  Expected{Intent: "AskRootCause", RootCause: []string{"磁盘空间不足"}},
		},
		{
			ID: "chat-recommend-gpu", Object: ObjectChat,
			ChatInput: "怎么处理 GPU 掉卡？",
			Expected:  Expected{Intent: "AskRecommendation", RootCause: []string{"处置建议"}},
		},
		{
			ID: "chat-inspect-switch", Object: ObjectChat,
			ChatInput: "查一下 switch-core-01 的告警",
			Expected:  Expected{Intent: "InspectAlert"},
		},
		{
			ID: "chat-list-alerts", Object: ObjectChat,
			ChatInput: "有哪些告警",
			Expected:  Expected{Intent: "ListAlerts"},
		},
		{
			ID: "chat-irrelevant", Object: ObjectChat,
			ChatInput: "今天天气如何？",
			Expected:  Expected{Intent: "Unknown"},
		},
		// ---- L4 Context/ReAct 任务（react=true 配置生效） ----
		{
			ID: "react-gpu-evidence", Object: ObjectContext,
			Alert: mkAlert("compute-02", "gpu_util", "critical", "GPU掉卡，利用率归零"),
			Expected: Expected{
				ToolUse: []string{"query_metric", "search_logs"},
			},
		},
	}
	return tasks
}

// 默认所有配置
func allConfigs() []string {
	return []string{ConfigRule, ConfigLLMSingle, ConfigLLMReact}
}

func effectiveConfigs(t Task) []string {
	if len(t.Configs) == 0 {
		return allConfigs()
	}
	return t.Configs
}

// TaskByID 按 ID 查找任务
func TaskByID(id string) (Task, bool) {
	for _, t := range DefaultTasks() {
		if t.ID == id {
			return t, true
		}
	}
	return Task{}, false
}

// ValidateTasks 校验任务集一致性：pipeline 任务 device+metric 唯一、ground-truth 非空
func ValidateTasks() error {
	seen := map[string]bool{}
	for _, t := range DefaultTasks() {
		if t.Object == ObjectChat {
			if t.ChatInput == "" {
				return fmt.Errorf("chat 任务 %s 缺 ChatInput", t.ID)
			}
			if t.Expected.Intent == "" {
				return fmt.Errorf("chat 任务 %s 缺期望 Intent", t.ID)
			}
			continue
		}
		if t.Alert.Device == "" || t.Alert.Metric == "" {
			return fmt.Errorf("任务 %s 缺 device/metric", t.ID)
		}
		if t.Object == ObjectContext {
			continue
		}
		key := t.Alert.Device + "|" + t.Alert.Metric
		if seen[key] {
			return fmt.Errorf("pipeline 任务 device+metric 重复: %s", key)
		}
		seen[key] = true
	}
	return nil
}
