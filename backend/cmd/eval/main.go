package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"aiops/internal/config"
	"aiops/internal/eval"
	"aiops/internal/llm"
	"aiops/internal/mockdata"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		cmdRun(os.Args[2:])
	case "export-samples":
		cmdExport(os.Args[2:])
	case "calibrate":
		cmdCalibrate(os.Args[2:])
	case "ci":
		cmdCI(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `用法:
  go run ./cmd/eval run [--trials N] [--configs a,b] [--report out.json]
  go run ./cmd/eval export-samples [--n N] [--report in.json] [-o samples.json]
  go run ./cmd/eval calibrate [--samples samples.json] [-o calib.json]
  go run ./cmd/eval ci [--threshold 0.9] [--all]`)
}

// 解析 flags，兼容 "--trials=3" 与 "--trials 3"
func parseFlags(args []string) (*flag.FlagSet, *int, *string, *string) {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	trials := fs.Int("trials", 3, "每任务每配置试验次数")
	configs := fs.String("configs", eval.ConfigLLMReact, "逗号分隔配置（仅 llm-react）")
	report := fs.String("report", "", "JSON 报告输出路径")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	return fs, trials, configs, report
}

func cmdRun(args []string) {
	_, trials, configs, report := parseFlags(args)
	configsList, err := parseConfigs(*configs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	cfg := config.Load()
	lc := mustClient(cfg)
	mockdata.GenerateAll(42)

	scored := map[string][]eval.ScoredTrial{}
	tasks := eval.DefaultTasks()
	for _, t := range tasks {
		for _, c := range configsList {
			if len(t.Configs) > 0 && !containsStr(t.Configs, c.Name) {
				continue
			}
			n := *trials
			key := t.ID + "|" + c.Name
			for trial := 1; trial <= n; trial++ {
				// 每次 trial 独立超时：同一 ctx 覆盖整次 trial（流水线运行 + LLM 评委），
				// 避免挂起的评委 LLM 无限阻塞评测。评分完成后才 cancel。
				trialCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				start := time.Now()
				res, err := eval.RunTask(trialCtx, t, c, lc, trial)
				if err != nil && res == nil {
					cancel()
					fmt.Fprintf(os.Stderr, "[warn] %s/%s trial %d: %v\n", t.ID, c.Name, trial, err)
					continue
				}
				scores := eval.Grade(trialCtx, t, c, res, lc)
				if res != nil {
					res.StartedAt = start
					res.EndedAt = time.Now()
				}
				cancel()
				st := eval.ScoredTrial{TrialResult: res, Scores: scores}
				if err != nil {
					// trial 内部失败：记 pass=false、score=0，原因写入 ErrMsg，不中断整体
					fmt.Fprintf(os.Stderr, "[warn] %s/%s trial %d: %v\n", t.ID, c.Name, trial, err)
					st.Score = 0
					st.Pass = false
				} else {
					st.Score = eval.WeightedScore(scores)
					st.Pass = st.Score >= 0.6
				}
				scored[key] = append(scored[key], st)
			}
		}
	}

	rep := eval.BuildReport(tasks, configsList, scored)
	fmt.Print(rep.Summarize())
	if report != nil && *report != "" {
		b, _ := rep.ToJSON()
		if err := os.WriteFile(*report, b, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "写报告失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("报告已写入 %s\n", *report)
	}
}

func cmdCI(args []string) {
	fs := flag.NewFlagSet("ci", flag.ContinueOnError)
	threshold := fs.Float64("threshold", 0.9, "pass@k 最低阈值")
	all := fs.Bool("all", false, "包含 Extended 任务（默认跳过）")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	cfg := config.Load()
	lc := mustClient(cfg)
	mockdata.GenerateAll(42)
	tasks := selectCITasks(*all)

	configsList := eval.EvalConfigs
	scored := map[string][]eval.ScoredTrial{}
	for _, t := range tasks {
		for _, c := range configsList {
			if len(t.Configs) > 0 && !containsStr(t.Configs, c.Name) {
				continue
			}
			for trial := 1; trial <= 3; trial++ {
				// 每次 trial 独立超时：同一 ctx 覆盖整次 trial（流水线运行 + LLM 评委），
				// 避免挂起的评委 LLM 无限阻塞评测。评分完成后才 cancel。
				trialCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				start := time.Now()
				res, err := eval.RunTask(trialCtx, t, c, lc, trial)
				if err != nil && res == nil {
					cancel()
					fmt.Fprintf(os.Stderr, "[warn] %s/%s trial %d: %v\n", t.ID, c.Name, trial, err)
					continue
				}
				scores := eval.Grade(trialCtx, t, c, res, lc)
				if res != nil {
					res.StartedAt = start
					res.EndedAt = time.Now()
				}
				cancel()
				st := eval.ScoredTrial{TrialResult: res, Scores: scores}
				if err != nil {
					// trial 内部失败：记 pass=false、score=0，原因写入 ErrMsg，不中断整体
					fmt.Fprintf(os.Stderr, "[warn] %s/%s trial %d: %v\n", t.ID, c.Name, trial, err)
					st.Score = 0
					st.Pass = false
				} else {
					st.Score = eval.WeightedScore(scores)
					st.Pass = st.Score >= 0.6
				}
				scored[t.ID+"|"+c.Name] = append(scored[t.ID+"|"+c.Name], st)
			}
		}
	}
	rep := eval.BuildReport(tasks, configsList, scored)
	fmt.Print(rep.Summarize())
	if rep.Summary.Overall.PassAtK < *threshold {
		fmt.Fprintf(os.Stderr, "CI 失败: pass@k=%.2f < %.2f\n", rep.Summary.Overall.PassAtK, *threshold)
		os.Exit(1)
	}
}

// selectCITasks 返回 CI 门禁任务集：默认跳过 Extended，all=true 时全量。
func selectCITasks(all bool) []eval.Task {
	var out []eval.Task
	for _, t := range eval.DefaultTasks() {
		if t.Extended && !all {
			continue
		}
		out = append(out, t)
	}
	return out
}

// export-samples: 从评测报告导出含 {task, transcript, LLM 判定} 的样本供人工标注
func cmdExport(args []string) {
	fs := flag.NewFlagSet("export-samples", flag.ContinueOnError)
	n := fs.Int("n", 20, "导出样本数")
	reportIn := fs.String("report", "", "评测报告 JSON（必填）")
	out := fs.String("o", "samples.json", "输出路径")
	fs.Parse(args)
	if *reportIn == "" {
		fmt.Fprintln(os.Stderr, "缺 --report 参数（评测报告 JSON）")
		os.Exit(2)
	}
	b, err := os.ReadFile(*reportIn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读报告失败: %v\n", err)
		os.Exit(1)
	}
	var rep eval.Report
	if err := json.Unmarshal(b, &rep); err != nil {
		fmt.Fprintf(os.Stderr, "报告解析失败: %v\n", err)
		os.Exit(1)
	}
	samples := eval.ExportSamples(rep, *n)
	if len(samples) == 0 {
		fmt.Fprintln(os.Stderr, "报告无 trial 可导出")
		os.Exit(0)
	}
	sb, err := json.MarshalIndent(samples, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "样本序列化失败: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, sb, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "写样本失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("已导出 %d 条样本到 %s\n", len(samples), *out)
}

// calibrate: 读取人工标注样本，计算人-LLM 一致率
func cmdCalibrate(args []string) {
	fs := flag.NewFlagSet("calibrate", flag.ContinueOnError)
	samples := fs.String("samples", "samples.json", "人工标注样本")
	out := fs.String("o", "calib.json", "校准结果输出")
	fs.Parse(args)
	b, err := os.ReadFile(*samples)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读样本失败: %v\n", err)
		os.Exit(1)
	}
	var ss []eval.Sample
	if err := json.Unmarshal(b, &ss); err != nil {
		fmt.Fprintf(os.Stderr, "样本解析失败: %v\n", err)
		os.Exit(1)
	}
	agreement, annotated, disagreements, err := eval.Calibrate(ss)
	if err != nil {
		fmt.Fprintf(os.Stderr, "校准失败: %v\n", err)
		os.Exit(1)
	}
	if disagreements == nil {
		disagreements = []eval.Sample{}
	}
	result := map[string]any{
		"samples_file":  *samples,
		"annotated":     annotated,
		"agreement":     agreement,
		"disagreements": disagreements,
	}
	if annotated > 0 && agreement < 0.8 {
		result["warning"] = "人-LLM 一致率低于 0.8，建议调整 LLM 评委 rubric"
	}
	ob, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "校准结果序列化失败: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, ob, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "写校准结果失败: %v\n", err)
		os.Exit(1)
	}
	if annotated > 0 {
		fmt.Printf("一致率: %.1f%% (%d 条已标注)\n", agreement*100, annotated)
	} else {
		fmt.Println("暂无已标注样本，未计算一致率")
	}
	fmt.Printf("校准结果已写入 %s\n", *out)
}

func mustClient(cfg config.Config) *llm.Client {
	lc, err := llm.NewClient(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化评测模型失败: %v\n", err)
		os.Exit(1)
	}
	return lc
}

// parseConfigs 拒绝不存在的执行配置，避免静默忽略已删除的模式。
// parseConfigs rejects unsupported configurations instead of silently dropping them.
func parseConfigs(raw string) ([]eval.Config, error) {
	var configs []eval.Config
	for _, name := range strings.Split(raw, ",") {
		found := false
		for _, cfg := range eval.EvalConfigs {
			if cfg.Name == name {
				configs = append(configs, cfg)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("不支持的评测配置 %q，可用配置: %s", name, eval.ConfigLLMReact)
		}
	}
	return configs, nil
}

func containsStr(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
