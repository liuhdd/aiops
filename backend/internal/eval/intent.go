package eval

// IntentStats 意图分类聚合指标（逐配置）。
type IntentStats struct {
	Accuracy  float64 `json:"accuracy"`  // 正确率：分类正确任务数 / chat 任务总数
	Precision float64 `json:"precision"` // 可信度：正确判争议 / 所有判争议
	Recall    float64 `json:"recall"`    // 召回率：正确判争议 / 所有应争议
}

// intentMetrics 对每个配置聚合 chat 任务的意图分类指标。
// 任务级判定取 pass@k 语义：任一 trial 命中即算。
func intentMetrics(tasks []Task, configs []Config, scored map[string][]ScoredTrial) map[string]IntentStats {
	out := make(map[string]IntentStats, len(configs))
	for _, cfg := range configs {
		var total, correct, judgedDisputed, shouldDisputed, tp float64
		for _, t := range tasks {
			if t.Object != ObjectChat {
				continue
			}
			trs := scored[t.ID+"|"+cfg.Name]
			if len(trs) == 0 {
				continue
			}
			total++
			corr, jd := false, false
			for i := range trs {
				res := trs[i].TrialResult
				if intentMatch(t, res) {
					corr = true
				}
				if allUnknown(res) {
					jd = true
				}
			}
			if corr {
				correct++
			}
			if jd {
				judgedDisputed++
			}
			if isShouldDisputed(t) {
				shouldDisputed++
				if jd {
					tp++
				}
			}
		}
		st := IntentStats{}
		if total > 0 {
			st.Accuracy = correct / total
		}
		if judgedDisputed > 0 {
			st.Precision = tp / judgedDisputed
		}
		if shouldDisputed > 0 {
			st.Recall = tp / shouldDisputed
		}
		out[cfg.Name] = st
	}
	return out
}

// intentMatch 判断 trial 的意图是否命中 ground-truth（与 gradeChat 同一判定口径）。
func intentMatch(t Task, res *TrialResult) bool {
	got := res.Intents
	if len(got) == 0 {
		got = []string{res.Intent}
	}
	if len(t.Expected.Intents) > 0 {
		return containsAll(got, t.Expected.Intents)
	}
	return contains(got, t.Expected.Intent)
}

// allUnknown 判断 trial 的意图是否全部为 Unknown（判为争议 = 输出引导语）。
func allUnknown(res *TrialResult) bool {
	got := res.Intents
	if len(got) == 0 {
		got = []string{res.Intent}
	}
	if len(got) == 0 {
		return false
	}
	for _, i := range got {
		if i != "Unknown" {
			return false
		}
	}
	return true
}

// isShouldDisputed ground-truth：Expected.Intent == "Unknown" 且无 Intents。
func isShouldDisputed(t Task) bool {
	return t.Expected.Intent == "Unknown" && len(t.Expected.Intents) == 0
}
