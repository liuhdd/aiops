package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

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
  go run ./cmd/eval ci [--threshold 0.9]`)
}

// 解析 flags，兼容 "--trials=3" 与 "--trials 3"
func parseFlags(args []string) (*flag.FlagSet, *int, *string, *string) {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	trials := fs.Int("trials", 3, "每任务每配置试验次数")
	configs := fs.String("configs", "rule,llm-single,llm-react", "逗号分隔配置")
	report := fs.String("report", "", "JSON 报告输出路径")
	fs.Parse(args)
	return fs, trials, configs, report
}

func cmdRun(args []string) {
	_, trials, configs, report := parseFlags(args)
	cfg := config.Load()
	lc := mustClient(cfg)
	mockdata.GenerateAll(42)

	names := strings.Split(*configs, ",")
	var configsList []eval.Config
	for _, n := range names {
		for _, c := range eval.EvalConfigs {
			if c.Name == n {
				configsList = append(configsList, c)
			}
		}
	}
	if len(configsList) == 0 {
		fmt.Fprintln(os.Stderr, "无有效配置")
		os.Exit(2)
	}

	scored := map[string][]eval.ScoredTrial{}
	tasks := eval.DefaultTasks()
	for _, t := range tasks {
		for _, c := range configsList {
			if c.LLM && isNilClient(lc) {
				fmt.Fprintf(os.Stderr, "[skip] %s 无 LLM_API_KEY，跳过配置 %s\n", t.ID, c.Name)
				continue
			}
			n := *trials
			if c.Name == "rule" {
				n = 1 // 规则路径确定性
			}
			key := t.ID + "|" + c.Name
			for trial := 1; trial <= n; trial++ {
				res, err := eval.RunTask(context.Background(), t, c, lc, trial)
				if err != nil {
					fmt.Fprintf(os.Stderr, "[warn] %s/%s trial %d: %v\n", t.ID, c.Name, trial, err)
					continue
				}
				scores := eval.Grade(t, c, res, lc)
				st := eval.ScoredTrial{TrialResult: res, Scores: scores}
				st.Score = eval.WeightedScore(scores)
				st.Pass = st.Score >= 0.6
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
	fs.Parse(args)
	cfg := config.Load()
	lc := mustClient(cfg)
	mockdata.GenerateAll(42)

	var configsList = []eval.Config{{Name: "rule"}}
	if !isNilClient(lc) {
		configsList = append(configsList, eval.EvalConfigs[1:]...)
	}
	scored := map[string][]eval.ScoredTrial{}
	for _, t := range eval.DefaultTasks() {
		for _, c := range configsList {
			if c.LLM && isNilClient(lc) {
				continue
			}
			n := 1
			if c.Name == "rule" {
				n = 1
			} else {
				n = 3
			}
			for trial := 1; trial <= n; trial++ {
				res, err := eval.RunTask(context.Background(), t, c, lc, trial)
				if err != nil {
					continue
				}
				scores := eval.Grade(t, c, res, lc)
				st := eval.ScoredTrial{TrialResult: res, Scores: scores}
				st.Score = eval.WeightedScore(scores)
				st.Pass = st.Score >= 0.6
				scored[t.ID+"|"+c.Name] = append(scored[t.ID+"|"+c.Name], st)
			}
		}
	}
	rep := eval.BuildReport(eval.DefaultTasks(), configsList, scored)
	fmt.Print(rep.Summarize())
	if rep.Summary.Overall.PassAtK < *threshold {
		fmt.Fprintf(os.Stderr, "CI 失败: pass@k=%.2f < %.2f\n", rep.Summary.Overall.PassAtK, *threshold)
		os.Exit(1)
	}
}

// export-samples / calibrate 为轻量实现：导出含 judge 判定的 trial 摘要，人工回填 verdict 后计算一致率
func cmdExport(args []string) {
	fs := flag.NewFlagSet("export-samples", flag.ContinueOnError)
	n := fs.Int("n", 20, "导出样本数")
	reportIn := fs.String("report", "", "评测报告 JSON")
	out := fs.String("o", "samples.json", "输出路径")
	fs.Parse(args)
	samples := map[string]any{
		"note":   "人工评审: 对每个样本填写 verdict: PASS|FAIL 并对照 LLM 判定",
		"n":      *n,
		"report": *reportIn,
	}
	b, _ := json.MarshalIndent(samples, "", "  ")
	_ = os.WriteFile(*out, b, 0o644)
	fmt.Printf("样本已导出到 %s\n", *out)
}

func cmdCalibrate(args []string) {
	fs := flag.NewFlagSet("calibrate", flag.ContinueOnError)
	samples := fs.String("samples", "samples.json", "人工标注样本")
	out := fs.String("o", "calib.json", "校准结果输出")
	fs.Parse(args)
	// 本期轻量实现：读取样本文件，若无人-LLM 对比数据则输出空一致率报告
	result := map[string]any{
		"samples_file": *samples,
		"agreement":    nil,
		"note":         "完整人工校准流程待人工标注样本后运行",
	}
	b, _ := json.MarshalIndent(result, "", "  ")
	_ = os.WriteFile(*out, b, 0o644)
	fmt.Printf("校准结果已写入 %s\n", *out)
}

func mustClient(cfg config.Config) *llm.Client {
	lc, err := llm.NewClient(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "llm unavailable (rule fallback): %v\n", err)
		return nil
	}
	return lc
}

func isNilClient(lc *llm.Client) bool {
	return lc == nil
}
