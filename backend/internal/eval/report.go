package eval

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// Report 评测报告
type Report struct {
	Meta    Meta         `json:"meta"`
	Summary Summary      `json:"summary"`
	Tasks   []TaskReport `json:"tasks"`
}

type Meta struct {
	Timestamp  time.Time `json:"ts"`
	Trials     int       `json:"trials"`
	Configs    []string  `json:"configs"`
	LLMEnabled bool      `json:"llm_enabled"`
}

type Summary struct {
	Overall  Stats            `json:"overall"`
	ByConfig map[string]Stats `json:"by_config"`
	ByObject map[string]Stats `json:"by_object"`
}

type Stats struct {
	PassAtK   float64      `json:"pass_at_k"`
	MeanScore float64      `json:"mean_score"`
	Stddev    float64      `json:"stddev"`
	Saturated bool         `json:"saturated"`
	Intent    *IntentStats `json:"intent,omitempty"` // chat 意图分类指标（仅 ByConfig 填充）
}

type TaskReport struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Configs map[string]ConfigStats `json:"configs"`
}

type ConfigStats struct {
	PassAtK   float64       `json:"pass_at_k"`
	MeanScore float64       `json:"mean_score"`
	Stddev    float64       `json:"stddev"`
	Trials    []ScoredTrial `json:"trials"`
}

// BuildReport 汇总评测结果
func BuildReport(tasks []Task, configs []Config, scored map[string][]ScoredTrial) Report {
	now := time.Now()
	llmEnabled := false
	// 判断是否启用 LLM：存在 LLM 类配置且有带非 NA LLM 评委的 trial
	for _, trs := range scored {
		for _, tr := range trs {
			for _, s := range tr.Scores {
				if (s.Name == "JudgeAccuracy" || s.Name == "JudgeGroundedness") && !s.NA {
					llmEnabled = true
				}
			}
		}
	}
	cfgNames := make([]string, 0, len(configs))
	for _, c := range configs {
		cfgNames = append(cfgNames, c.Name)
	}
	rep := Report{
		Meta: Meta{Timestamp: now, Trials: trialsCount(scored), Configs: cfgNames, LLMEnabled: llmEnabled},
		Summary: Summary{
			ByConfig: map[string]Stats{},
			ByObject: map[string]Stats{},
		},
	}
	taskReps := make([]TaskReport, 0, len(tasks))
	// 全局汇总
	var allStats []Stats
	for _, t := range tasks {
		tr := TaskReport{ID: t.ID, Object: t.Object, Configs: map[string]ConfigStats{}}
		for _, c := range configs {
			key := t.ID + "|" + c.Name
			trs := scored[key]
			if len(trs) == 0 {
				continue
			}
			pk, mean, sd := summarize(trs)
			tr.Configs[c.Name] = ConfigStats{PassAtK: pk, MeanScore: mean, Stddev: sd, Trials: trs}
			cfgSt := Stats{PassAtK: pk, MeanScore: mean, Stddev: sd, Saturated: pk > 0.85}
			allStats = append(allStats, cfgSt)
		}
		taskReps = append(taskReps, tr)
	}
	rep.Tasks = taskReps
	// 第二遍汇总：按配置/对象维度累加各任务的统计量后再求均值
	// （"配置级 pass@k = 该配置下所有任务 pass@k 的均值"），避免旧 mergeStats 的顺序相关偏差且补齐 Saturated。
	type statAccum struct {
		sumPk, sumMean, sumSd float64
		n                     int
	}
	cfgAcc := map[string]*statAccum{}
	objAcc := map[string]*statAccum{}
	for _, tr := range rep.Tasks {
		for cfgName, cs := range tr.Configs {
			a := cfgAcc[cfgName]
			if a == nil {
				a = &statAccum{}
				cfgAcc[cfgName] = a
			}
			a.sumPk += cs.PassAtK
			a.sumMean += cs.MeanScore
			a.sumSd += cs.Stddev
			a.n++
		}
		// 对象维度：取该任务所有配置的 trials
		var objTrials []ScoredTrial
		for _, c := range configs {
			objTrials = append(objTrials, scored[tr.ID+"|"+c.Name]...)
		}
		if len(objTrials) > 0 {
			pk, mean, sd := summarize(objTrials)
			a := objAcc[tr.Object]
			if a == nil {
				a = &statAccum{}
				objAcc[tr.Object] = a
			}
			a.sumPk += pk
			a.sumMean += mean
			a.sumSd += sd
			a.n++
		}
	}
	intents := intentMetrics(tasks, configs, scored)
	for name, a := range cfgAcc {
		pk := a.sumPk / float64(a.n)
		ist := intents[name]
		rep.Summary.ByConfig[name] = Stats{
			PassAtK: pk, MeanScore: a.sumMean / float64(a.n),
			Stddev: a.sumSd / float64(a.n), Saturated: pk > 0.85,
			Intent: &ist,
		}
	}
	for name, a := range objAcc {
		pk := a.sumPk / float64(a.n)
		rep.Summary.ByObject[name] = Stats{
			PassAtK: pk, MeanScore: a.sumMean / float64(a.n),
			Stddev: a.sumSd / float64(a.n), Saturated: pk > 0.85,
		}
	}
	// 全局
	if len(allStats) > 0 {
		rep.Summary.Overall = averageStats(allStats)
	}
	return rep
}

// summarize 计算一组 trial 的 pass@k / mean / stddev
func summarize(trs []ScoredTrial) (pk, mean, stddev float64) {
	pk = passAtK(trs)
	var scores []float64
	for _, tr := range trs {
		scores = append(scores, tr.Score)
	}
	mean, stddev = meanStddev(scores)
	return pk, mean, stddev
}

// passAtK 至少一次 trial 通过的比例（峰值能力）
func passAtK(trs []ScoredTrial) float64 {
	if len(trs) == 0 {
		return 0
	}
	for _, tr := range trs {
		if tr.Pass {
			return 1.0
		}
	}
	return 0
}

func meanStddev(xs []float64) (mean, stddev float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	for _, x := range xs {
		mean += x
	}
	mean /= float64(len(xs))
	for _, x := range xs {
		stddev += (x - mean) * (x - mean)
	}
	stddev = math.Sqrt(stddev / float64(len(xs)))
	return mean, stddev
}

func averageStats(ss []Stats) Stats {
	if len(ss) == 0 {
		return Stats{}
	}
	var pk, m, sd float64
	for _, s := range ss {
		pk += s.PassAtK
		m += s.MeanScore
		sd += s.Stddev
	}
	return Stats{
		PassAtK:   pk / float64(len(ss)),
		MeanScore: m / float64(len(ss)),
		Stddev:    sd / float64(len(ss)),
		Saturated: (pk / float64(len(ss))) > 0.85,
	}
}

func trialsCount(scored map[string][]ScoredTrial) int {
	n := 0
	for _, trs := range scored {
		n += len(trs)
	}
	return n
}

// ToJSON 序列化报告
func (r Report) ToJSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// Summarize 打印人可读摘要。
// 注意：按计划命名 "Summary()" 会与 Report 的 Summary 字段同名冲突（Go 禁止），故命名为 Summarize。
func (r Report) Summarize() string {
	var b strings.Builder
	b.WriteString("== 评测摘要 ==\n")
	fmt.Fprintf(&b, "全局: pass@k=%.2f 平均分=%.2f stddev=%.2f\n",
		r.Summary.Overall.PassAtK, r.Summary.Overall.MeanScore, r.Summary.Overall.Stddev)
	for cfg, st := range r.Summary.ByConfig {
		fmt.Fprintf(&b, "  [%s] pass@k=%.2f 平均分=%.2f\n", cfg, st.PassAtK, st.MeanScore)
		if st.Intent != nil {
			fmt.Fprintf(&b, "       intent: 正确率=%.2f 可信度=%.2f 召回率=%.2f\n",
				st.Intent.Accuracy, st.Intent.Precision, st.Intent.Recall)
		}
	}
	if r.Summary.Overall.Saturated {
		b.WriteString("警告: 评测饱和 (pass@k>0.85)，建议补充难题。\n")
	}
	return b.String()
}
