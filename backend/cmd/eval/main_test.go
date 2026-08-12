package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 用 go run 编译运行 CLI（集成冒烟测试）
func TestCLIRunProducesReport(t *testing.T) {
	out := filepath.Join(t.TempDir(), "report.json")
	cmd := exec.Command("go", "run", ".", "run",
		"--trials", "1", "--configs", "rule", "--report", out)
	cmd.Env = append(os.Environ(), "LLM_API_KEY=") // 强制无 LLM
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run 失败: %v\n%s", err, b)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("报告未生成: %v\n%s", err, b)
	}
	if !fileContains(out, `"pass_at_k"`) {
		t.Fatalf("报告缺 pass_at_k 字段\n%s", readFile(out))
	}
}

func TestCLICIExitCode(t *testing.T) {
	// 阈值 1.5 不可能达到 => 必然退出码 1（确定性断言）
	cmd := exec.Command("go", "run", ".", "ci", "--threshold", "1.5")
	cmd.Env = append(os.Environ(), "LLM_API_KEY=")
	err := cmd.Run()
	if err == nil {
		t.Fatal("ci 应以非零退出")
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1", ee.ExitCode())
	}
}

func TestCLIExportCalibrate(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "report.json")
	samples := filepath.Join(dir, "samples.json")
	calib := filepath.Join(dir, "calib.json")

	runCmd := exec.Command("go", "run", ".", "run",
		"--trials", "1", "--configs", "rule", "--report", report)
	runCmd.Env = append(os.Environ(), "LLM_API_KEY=")
	if b, err := runCmd.CombinedOutput(); err != nil {
		t.Fatalf("run 失败: %v\n%s", err, b)
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
