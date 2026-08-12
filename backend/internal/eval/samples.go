package eval

import (
	"fmt"
	"sort"

	"aiops/internal/model"
)

// JudgeVerdict LLM 评委对单个维度的判定
type JudgeVerdict struct {
	Grader  string  `json:"grader"`
	Score   float64 `json:"score"`
	Verdict string  `json:"verdict"` // PASS | FAIL | INSUFFICIENT_INFO
}

// Sample 单条人工校准样本：任务 + transcript + LLM 判定 + 人工判定（待填）
type Sample struct {
	TaskID     string                 `json:"task_id"`
	Config     string                 `json:"config"`
	Trial      int                    `json:"trial"`
	RunStatus  string                 `json:"run_status"`
	RCA        model.RCA              `json:"rca"`
	Evidence   model.Evidence         `json:"evidence"`
	Recommend  model.Recommendation   `json:"recommend"`
	Transcript string                 `json:"transcript"` // 阶段状态摘要（供人工对照）
	LLMVerds   []JudgeVerdict         `json:"llm_verdicts"`
	HumanVerd  string                 `json:"human_verdict"` // 人工回填: PASS | FAIL
}

// ExportSamples 从评测报告导出人工校准样本。
// 顺序确定：任务序 → 配置名排序 → trial 序；n<=0 时不设上限。
func ExportSamples(rep Report, n int) []Sample {
	var out []Sample
	for _, t := range rep.Tasks {
		cfgs := make([]string, 0, len(t.Configs))
		for c := range t.Configs {
			cfgs = append(cfgs, c)
		}
		sort.Strings(cfgs)
		for _, cfgName := range cfgs {
			for _, tr := range t.Configs[cfgName].Trials {
				out = append(out, buildSample(t.ID, cfgName, tr))
				if n > 0 && len(out) >= n {
					return out
				}
			}
		}
	}
	return out
}

func buildSample(taskID, cfgName string, tr ScoredTrial) Sample {
	var completed int
	for _, stg := range tr.Outcome.Stages {
		if stg.Status == model.StageCompleted {
			completed++
		}
	}
	var verds []JudgeVerdict
	for _, s := range tr.Scores {
		if (s.Name == "JudgeAccuracy" || s.Name == "JudgeGroundedness") && !s.NA {
			v := "FAIL"
			if s.Pass {
				v = "PASS"
			}
			verds = append(verds, JudgeVerdict{Grader: s.Name, Score: s.Score, Verdict: v})
		}
	}
	return Sample{
		TaskID:     taskID,
		Config:     cfgName,
		Trial:      tr.Trial,
		RunStatus:  tr.RunStatus,
		RCA:        tr.Outcome.RCA,
		Evidence:   tr.Outcome.Evidence,
		Recommend:  tr.Outcome.Recommend,
		Transcript: fmt.Sprintf("run=%s stages=%d/%d", tr.RunStatus, completed, len(tr.Outcome.Stages)),
		LLMVerds:   verds,
	}
}

// Calibrate 计算人-LLM 一致率。跳过未标注（HumanVerd 空）或缺少 LLM 判定的样本。
// LLM 总体判定：全部维度 PASS 才为 PASS，否则 FAIL。
func Calibrate(samples []Sample) (agreement float64, annotated int, disagreements []Sample, err error) {
	for _, s := range samples {
		if s.HumanVerd == "" || len(s.LLMVerds) == 0 {
			continue
		}
		annotated++
		llmOverall := "PASS"
		for _, v := range s.LLMVerds {
			if v.Verdict != "PASS" {
				llmOverall = "FAIL"
				break
			}
		}
		if s.HumanVerd != llmOverall {
			disagreements = append(disagreements, s)
		}
	}
	if annotated == 0 {
		return 0, 0, nil, nil
	}
	agreement = float64(annotated-len(disagreements)) / float64(annotated)
	return agreement, annotated, disagreements, nil
}
