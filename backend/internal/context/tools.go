package context

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"aiops/internal/mockdata"
	"aiops/internal/model"
	"aiops/internal/retrieve"
)

var ErrScratchpadHandled = errors.New("write_scratchpad 由引擎接管")

type Tool struct {
	Name        string
	Description string
	ParamSchema map[string]string
	Execute     func(ctx context.Context, args map[string]any) (any, error)
}

type Registry struct {
	tools map[string]Tool
}

func NewRegistry() *Registry { return &Registry{tools: map[string]Tool{}} }

func (r *Registry) Register(t Tool) { r.tools[t.Name] = t }
func (r *Registry) List() []Tool {
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (r *Registry) Lookup(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) Execute(ctx context.Context, name string, args map[string]any) (any, error) {
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool %q", name)
	}
	return t.Execute(ctx, args)
}

func DefaultTools() []Tool {
	return []Tool{
		{Name: "query_metric", Description: "查询设备监控指标时序", ParamSchema: map[string]string{
			"device": "设备名", "metric": "指标名", "window_minutes": "查询窗口分钟数（整数）"},
			Execute: func(ctx context.Context, args map[string]any) (any, error) {
				device, metric, win := strArg(args, "device"), strArg(args, "metric"), intArg(args, "window_minutes", 120)
				from := time.Now().Add(-time.Duration(win) * time.Minute)
				pts, err := mockdata.QueryMetric(device, metric, from, time.Now())
				if err != nil {
					return nil, err
				}
				return mockdata.ToMetricViews(pts), nil
			}},
		{Name: "search_logs", Description: "按关键字检索设备日志", ParamSchema: map[string]string{
			"device": "设备名", "keywords": "关键字数组", "window_minutes": "查询窗口分钟数（整数）"},
			Execute: func(ctx context.Context, args map[string]any) (any, error) {
				device := strArg(args, "device")
				win := intArg(args, "window_minutes", 120)
				from := time.Now().Add(-time.Duration(win) * time.Minute)
				logs, err := mockdata.SearchLogs(device, listArg(args, "keywords"), from, time.Now())
				if err != nil {
					return nil, err
				}
				return mockdata.ToLogViews(logs), nil
			}},
		{Name: "get_device_info", Description: "获取设备资产信息", ParamSchema: map[string]string{"device": "设备名"},
			Execute: func(ctx context.Context, args map[string]any) (any, error) {
				name := strArg(args, "device")
				for _, d := range mockdata.Devices() {
					if d.Name == name {
						return d, nil
					}
				}
				return nil, fmt.Errorf("device %q not found", name)
			}},
		{Name: "get_neighbors", Description: "获取设备拓扑邻居", ParamSchema: map[string]string{"device": "设备名"},
			Execute: func(ctx context.Context, args map[string]any) (any, error) {
				return mockdata.Neighbors(strArg(args, "device")), nil
			}},
		{Name: "search_runbook", Description: "按指标查处置预案", ParamSchema: map[string]string{"metric": "指标名"},
			Execute: func(ctx context.Context, args map[string]any) (any, error) {
				steps, ok := mockdata.SearchRunbook(strArg(args, "metric"))
				if !ok {
					return nil, fmt.Errorf("no runbook for metric %q", strArg(args, "metric"))
				}
				return steps, nil
			}},
		{Name: "write_scratchpad", Description: "写入临时笔记板（记录推理过程）", ParamSchema: map[string]string{
			"step": "笔记步骤名", "content": "笔记内容"},
			Execute: func(ctx context.Context, args map[string]any) (any, error) {
				return nil, ErrScratchpadHandled
			}},
	}
}

// NewSearchCaseTool 检索相似历史运维案例（故障症状→根因→处置）。
func NewSearchCaseTool(s retrieve.Searcher) Tool {
	return Tool{
		Name: "search_case", Description: "检索相似历史运维案例（故障症状→根因→处置）",
		ParamSchema: map[string]string{"query": "问题或症状描述（必填）", "metric": "可选，指标名", "device": "可选，设备名", "top_k": "可选，返回条数（默认3）"},
		Execute: func(ctx context.Context, args map[string]any) (any, error) {
			if s == nil {
				return nil, errors.New("knowledge searcher unavailable")
			}
			return s.SearchCases(ctx, retrieve.Query{
				Text: strArg(args, "query"), Metric: strArg(args, "metric"), Device: strArg(args, "device"),
			}, retrieve.Options{TopK: intArg(args, "top_k", 3)})
		},
	}
}

// NewSearchManualTool 检索产品手册/操作手册（规格、告警含义、操作步骤）。
func NewSearchManualTool(s retrieve.Searcher) Tool {
	return Tool{
		Name: "search_manual", Description: "检索产品手册/操作手册（规格、告警含义、操作步骤）",
		ParamSchema: map[string]string{"query": "要查的问题（必填）", "category": "可选，manual/runbook", "metric": "可选，指标名", "top_k": "可选，返回条数（默认3）"},
		Execute: func(ctx context.Context, args map[string]any) (any, error) {
			if s == nil {
				return nil, errors.New("knowledge searcher unavailable")
			}
			return s.SearchManuals(ctx, retrieve.Query{
				Text: strArg(args, "query"), Category: model.KBCategory(strArg(args, "category")),
				Metric: strArg(args, "metric"),
			}, retrieve.Options{TopK: intArg(args, "top_k", 3)})
		},
	}
}

func strArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func intArg(args map[string]any, key string, def int) int {
	switch v := args[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	case string:
		n := 0
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return def
}

func listArg(args map[string]any, key string) []string {
	raw, ok := args[key].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
