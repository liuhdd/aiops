package eval

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"aiops/internal/model"
	"aiops/internal/phoenix"
)

func TestAgentDatasetExamplesCoverAllTasks(t *testing.T) {
	tasks := DefaultTasks()
	exs := AgentDatasetExamples()
	if len(exs) != len(tasks) {
		t.Fatalf("examples=%d tasks=%d", len(exs), len(tasks))
	}
	byID := map[string]bool{}
	for _, e := range exs {
		byID[e.ID] = true
		if e.Metadata["task_id"] != e.ID {
			t.Fatalf("metadata.task_id 应与 example id 一致: %+v", e)
		}
		if len(e.Splits) != 1 {
			t.Fatalf("splits 应为单元素: %+v", e)
		}
	}
	for _, task := range tasks {
		if !byID[task.ID] {
			t.Fatalf("缺任务 %s", task.ID)
		}
	}
}

func TestAgentDatasetExampleShapes(t *testing.T) {
	byID := map[string]int{}
	exs := AgentDatasetExamples()
	for i, e := range exs {
		byID[e.ID] = i
	}
	pipe := exs[byID["disk-full-db-01"]]
	alert, ok := pipe.Input["alert"].(map[string]any)
	if !ok || alert["device"] != "db-01" || alert["metric"] != "disk_usage" {
		t.Fatalf("pipeline input = %v", pipe.Input)
	}
	if pipe.Output["runbook"] != "disk_usage" {
		t.Fatalf("pipeline output = %v", pipe.Output)
	}

	chat := exs[byID["chat-list-alerts"]]
	if chat.Input["chat_input"] != "有哪些告警" || chat.Input["object"] != ObjectChat {
		t.Fatalf("chat input = %v", chat.Input)
	}
	if chat.Output["intent"] != "ListAlerts" {
		t.Fatalf("chat output = %v", chat.Output)
	}
}

func TestTrialResultSerializesTimestamps(t *testing.T) {
	start := time.Date(2026, 9, 10, 1, 2, 3, 0, time.UTC)
	tr := TrialResult{Trial: 1, Config: "rule", StartedAt: start, EndedAt: start.Add(time.Second)}
	b, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"started_at"`) || !strings.Contains(s, `"ended_at"`) {
		t.Fatalf("缺少时间戳字段: %s", s)
	}
}

type fakeWriter struct {
	runs        []phoenix.RunRequest
	evals       []phoenix.EvaluationRequest
	experiments []string
	exID        string
}

func (f *fakeWriter) ListExamples(datasetID, versionID string) ([]phoenix.Example, error) {
	return []phoenix.Example{
		{ID: "EX-t1", Metadata: map[string]any{"task_id": "t1"}},
		{ID: "EX-t2", Metadata: map[string]any{"task_id": "t2"}},
	}, nil
}
func (f *fakeWriter) CreateExperiment(datasetID, name string, repetitions int, metadata map[string]any) (phoenix.Experiment, error) {
	f.experiments = append(f.experiments, name)
	return phoenix.Experiment{ID: "E-" + name}, nil
}
func (f *fakeWriter) LogRun(experimentID string, r phoenix.RunRequest) (string, error) {
	f.runs = append(f.runs, r)
	return "R", nil
}
func (f *fakeWriter) LogEvaluation(e phoenix.EvaluationRequest) (string, error) {
	f.evals = append(f.evals, e)
	return "EV", nil
}

func TestPushAgentExperimentFullMapping(t *testing.T) {
	rep := Report{
		Meta: Meta{Configs: []string{"rule"}, LLMEnabled: false},
		Tasks: []TaskReport{
			{ID: "t1", Configs: map[string]ConfigStats{
				"rule": {Trials: []ScoredTrial{{TrialResult: &TrialResult{Config: "rule", Trial: 1, RunStatus: "completed", Outcome: RunOutcome{RCA: model.RCA{RootCause: "x"}}},
					Scores: []GraderResult{
						{Name: "RootCauseMatch", Pass: true, Score: 1, Weight: 0.3, Detail: "ok"},
						{Name: "JudgeAccuracy", NA: true},
					}, Pass: true, Score: 1}}},
			}},
		},
	}
	w := &fakeWriter{}
	sums, err := PushAgentExperiment(w, "DS", rep, nil, "aiops-agents-eval")
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 {
		t.Fatalf("summaries = %+v", sums)
	}
	if sums[0].Name != "aiops-agents-eval/rule" || sums[0].ID != "E-aiops-agents-eval/rule" {
		t.Fatalf("summary name/id = %+v", sums[0])
	}
	if sums[0].RunsWritten != 1 || sums[0].EvalsWritten != 2 || sums[0].Failures != 0 {
		t.Fatalf("summary counts = %+v", sums[0])
	}
	if len(w.experiments) != 1 || w.experiments[0] != "aiops-agents-eval/rule" {
		t.Fatalf("experiments = %v", w.experiments)
	}
	if len(w.runs) != 1 || w.runs[0].DatasetExampleID != "EX-t1" || w.runs[0].RepetitionNumber != 1 {
		t.Fatalf("runs = %+v", w.runs)
	}
	// 1 个 grader(NA 跳过) + 1 个 WeightedScore = 2
	if len(w.evals) != 2 {
		t.Fatalf("evals = %+v", w.evals)
	}
	if w.evals[0].Name != "RootCauseMatch" || w.evals[0].AnnotatorKind != "CODE" {
		t.Fatalf("eval0 = %+v", w.evals[0])
	}
	if w.evals[1].Name != "WeightedScore" {
		t.Fatalf("eval1 = %+v", w.evals[1])
	}
}

func TestPushAgentExperimentJudgeUsesLLMKind(t *testing.T) {
	rep := Report{
		Meta: Meta{Configs: []string{"llm-single"}},
		Tasks: []TaskReport{
			{ID: "t1", Configs: map[string]ConfigStats{
				"llm-single": {Trials: []ScoredTrial{{TrialResult: &TrialResult{Config: "llm-single", Trial: 1},
					Scores: []GraderResult{{Name: "JudgeAccuracy", Pass: true, Score: 0.9, Weight: 0.2}}, Pass: true, Score: 0.9}}},
			}},
		},
	}
	w := &fakeWriter{}
	if _, err := PushAgentExperiment(w, "DS", rep, nil, "p"); err != nil {
		t.Fatal(err)
	}
	if w.evals[0].AnnotatorKind != "LLM" {
		t.Fatalf("judge 应用 LLM kind: %+v", w.evals[0])
	}
}

func TestPushAgentExperimentCountsMissingExampleAsFailure(t *testing.T) {
	rep := Report{
		Meta: Meta{Configs: []string{"rule"}},
		Tasks: []TaskReport{
			{ID: "t-missing", Configs: map[string]ConfigStats{
				"rule": {Trials: []ScoredTrial{{TrialResult: &TrialResult{Config: "rule", Trial: 1, RunStatus: "completed"}}}},
			}},
		},
	}
	w := &fakeWriter{}
	sums, err := PushAgentExperiment(w, "DS", rep, nil, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.runs) != 0 || len(w.evals) != 0 {
		t.Fatalf("缺失 example 不应写 run/eval: runs=%d evals=%d", len(w.runs), len(w.evals))
	}
	if len(sums) != 1 || sums[0].Failures != 1 || sums[0].RunsWritten != 0 || sums[0].EvalsWritten != 0 {
		t.Fatalf("summary = %+v", sums)
	}
}

func TestRunOutputNilGuard(t *testing.T) {
	if got := runOutput(nil); len(got) != 0 {
		t.Fatalf("runOutput(nil) = %+v", got)
	}
}
