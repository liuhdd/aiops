package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"aiops/internal/eval"
)

// CLI 必须拒绝缺模型执行。/ The CLI rejects execution without a model.
func TestCLIRequiresLLM(t *testing.T) {
	for _, subcommand := range []string{"run", "ci"} {
		t.Run(subcommand, func(t *testing.T) {
			cmd := exec.Command("go", "run", ".", subcommand)
			cmd.Env = append(os.Environ(), "LLM_API_KEY=")
			b, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(b), "初始化评测模型失败") || !strings.Contains(string(b), "LLM_API_KEY") {
				t.Fatalf("缺模型应非零退出并说明配置：err=%v\n%s", err, b)
			}
		})
	}
}

func TestCLIRejectsUnsupportedConfigs(t *testing.T) {
	for _, name := range []string{"rule", "llm-single", "unknown", "llm-react,rule", "llm-react,unknown"} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command("go", "run", ".", "run", "--configs", name)
			cmd.Env = append(os.Environ(), "LLM_API_KEY=")
			b, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(b), "不支持的评测配置") {
				t.Fatalf("应明确拒绝配置 %q：err=%v\n%s", name, err, b)
			}
		})
	}
}

func TestCLIExportCalibrate(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "report.json")
	samples := filepath.Join(dir, "samples.json")
	calib := filepath.Join(dir, "calib.json")

	rep := eval.BuildReport([]eval.Task{{ID: "sample-task", Object: eval.ObjectPipeline}},
		[]eval.Config{{Name: "candidate-a"}}, map[string][]eval.ScoredTrial{
			"sample-task|candidate-a": {{TrialResult: &eval.TrialResult{Trial: 1, Config: "candidate-a"}, Pass: true, Score: 1}},
		})
	b, err := rep.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(report, b, 0o644); err != nil {
		t.Fatal(err)
	}

	expCmd := exec.Command("go", "run", ".", "export-samples",
		"--n", "5", "--report", report, "-o", samples)
	expCmd.Env = append(os.Environ(), "LLM_API_KEY=")
	if b, err := expCmd.CombinedOutput(); err != nil {
		t.Fatalf("export-samples 失败: %v\n%s", err, b)
	}
	var out []struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(readFile(samples)), &out); err != nil {
		t.Fatalf("samples.json 不是合法 JSON: %v\n%s", err, readFile(samples))
	}
	if len(out) < 1 || out[0].TaskID == "" {
		t.Fatalf("samples.json 应至少含 1 条带 task_id 的样本:\n%s", readFile(samples))
	}

	calCmd := exec.Command("go", "run", ".", "calibrate",
		"--samples", samples, "-o", calib)
	calCmd.Env = append(os.Environ(), "LLM_API_KEY=")
	if b, err := calCmd.CombinedOutput(); err != nil {
		t.Fatalf("calibrate 失败: %v\n%s", err, b)
	}
	if !fileContains(calib, `"agreement"`) {
		t.Fatalf("calib.json 缺 agreement 字段\n%s", readFile(calib))
	}
}

func TestSelectCITasksSkipsExtended(t *testing.T) {
	all := eval.DefaultTasks()
	if len(all) == 0 {
		t.Fatal("任务集为空")
	}
	// 人为标记一条为 Extended（通过任务集副本无法改，改为直接测过滤函数对真实扩展题的判断）
	kept := selectCITasks(false)
	keptAll := selectCITasks(true)
	if len(keptAll) != len(all) {
		t.Fatalf("--all 应保留全部: got %d want %d", len(keptAll), len(all))
	}
	if len(kept) > len(keptAll) {
		t.Fatalf("默认不应多于 --all")
	}
	for _, tk := range kept {
		if tk.Extended {
			t.Fatalf("默认 CI 不应含 Extended 任务 %s", tk.ID)
		}
	}
}

func fileContains(path, sub string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(b), sub)
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}
