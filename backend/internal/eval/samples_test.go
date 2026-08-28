package eval

import (
	"testing"

	"aiops/internal/model"
)

func TestExportSamples(t *testing.T) {
	tr1 := &TrialResult{
		Trial: 1, Config: "rule", RunStatus: model.StatusCompleted,
		Outcome: RunOutcome{
			RCA:      model.RCA{RootCause: "磁盘空间不足"},
			Evidence: model.Evidence{Gaps: []string{}},
			Stages: []model.StageResult{
				{StageName: "rca", Status: model.StageCompleted},
				{StageName: "evidence", Status: model.StageFailed},
			},
		},
	}
	scored1 := ScoredTrial{
		TrialResult: tr1,
		Scores: []GraderResult{
			{Name: "JudgeAccuracy", Pass: true, Score: 0.9, Weight: 0.2},
			{Name: "JudgeGroundedness", Pass: true, Score: 0.8, Weight: 0.2},
		},
	}
	tr2 := &TrialResult{Trial: 2, Config: "rule", RunStatus: model.StatusCompleted}
	scored2 := ScoredTrial{TrialResult: tr2, Scores: []GraderResult{
		{Name: "RootCauseMatch", Pass: true, Score: 1, Weight: 0.3}, // 非 LLM 评委，不导出
		{Name: "JudgeAccuracy", NA: true, Weight: 0.2},              // NA 评委，不导出
	}}

	rep := Report{Tasks: []TaskReport{{
		ID: "disk-full-db-01", Object: ObjectPipeline,
		Configs: map[string]ConfigStats{"rule": {Trials: []ScoredTrial{scored1, scored2}}},
	}}}

	// 不设上限：两条样本
	got := ExportSamples(rep, 0)
	if len(got) != 2 {
		t.Fatalf("ExportSamples len = %d, want 2", len(got))
	}
	if got[0].TaskID != "disk-full-db-01" || got[0].Config != "rule" || got[0].Trial != 1 {
		t.Fatalf("sample[0] 元数据不对: %+v", got[0])
	}
	if got[0].HumanVerd != "" {
		t.Fatalf("HumanVerd 应为空待人工回填, got %q", got[0].HumanVerd)
	}
	if len(got[0].LLMVerds) != 2 {
		t.Fatalf("sample[0] LLMVerds = %d, want 2", len(got[0].LLMVerds))
	}
	if got[0].LLMVerds[0].Grader != "JudgeAccuracy" || got[0].LLMVerds[0].Verdict != "PASS" {
		t.Fatalf("LLMVerds[0] 不对: %+v", got[0].LLMVerds[0])
	}
	if got[0].Transcript == "" {
		t.Fatal("Transcript 应为非空摘要")
	}
	// NA / 非 LLM 评委不导出
	if len(got[1].LLMVerds) != 0 {
		t.Fatalf("sample[1] LLMVerds = %d, want 0", len(got[1].LLMVerds))
	}
	// cap by n
	gotCap := ExportSamples(rep, 1)
	if len(gotCap) != 1 || gotCap[0].Trial != 1 {
		t.Fatalf("cap=1 时应只导出第一条")
	}
	// 确定性：两次调用顺序一致
	a, b2 := ExportSamples(rep, 0), ExportSamples(rep, 0)
	for i := range a {
		if a[i].TaskID != b2[i].TaskID || a[i].Config != b2[i].Config || a[i].Trial != b2[i].Trial {
			t.Fatalf("两次导出顺序不一致 idx=%d: %+v vs %+v", i, a[i], b2[i])
		}
	}
}

func TestCalibrate(t *testing.T) {
	samples := []Sample{
		// (a) 一致: human PASS == LLM PASS
		{TaskID: "t1", HumanVerd: "PASS", LLMVerds: []JudgeVerdict{{Verdict: "PASS"}, {Verdict: "PASS"}}},
		// (b) 不一致: human FAIL vs LLM PASS
		{TaskID: "t2", HumanVerd: "FAIL", LLMVerds: []JudgeVerdict{{Verdict: "PASS"}}},
		// (c) 未标注: 跳过
		{TaskID: "t3", HumanVerd: "", LLMVerds: []JudgeVerdict{{Verdict: "PASS"}}},
		// (d) 无 LLM 判定: 跳过
		{TaskID: "t4", HumanVerd: "PASS", LLMVerds: nil},
	}
	agreement, annotated, disagreements, err := Calibrate(samples)
	if err != nil {
		t.Fatalf("Calibrate: %v", err)
	}
	if annotated != 2 {
		t.Fatalf("annotated = %d, want 2", annotated)
	}
	if len(disagreements) != 1 || disagreements[0].TaskID != "t2" {
		t.Fatalf("disagreements = %+v, want 1 (t2)", disagreements)
	}
	if agreement != 0.5 {
		t.Fatalf("agreement = %v, want 0.5", agreement)
	}
	// 全未标注 / 无 LLM 判定 => 0,0,nil
	agreement, annotated, disagreements, err = Calibrate([]Sample{{TaskID: "x"}})
	if err != nil || annotated != 0 || len(disagreements) != 0 {
		t.Fatalf("空样本应返回 0/0/nil: err=%v annotated=%d dis=%d", err, annotated, len(disagreements))
	}
}
