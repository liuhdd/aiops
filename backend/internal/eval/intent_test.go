package eval

import "testing"

func TestIntentMetricsFull(t *testing.T) {
	tasks := []Task{
		{ID: "c1", Object: ObjectChat, Expected: Expected{Intent: "AskRootCause"}},
		{ID: "c2", Object: ObjectChat, Expected: Expected{Intent: "Unknown"}},
		{ID: "c3", Object: ObjectChat, Expected: Expected{Intents: []string{"ListAlerts", "AskRootCause"}}},
		{ID: "p1", Object: ObjectPipeline, Expected: Expected{Intent: "Unknown"}}, // 非 chat，应忽略
	}
	configs := []Config{{Name: "rule"}}
	scored := map[string][]ScoredTrial{
		"c1|rule": {{TrialResult: &TrialResult{Intents: []string{"AskRootCause"}}}},
		"c2|rule": {{TrialResult: &TrialResult{Intents: []string{"Unknown"}}}},
		"c3|rule": {{TrialResult: &TrialResult{Intents: []string{"ListAlerts", "AskRootCause"}}}},
	}
	st := intentMetrics(tasks, configs, scored)["rule"]
	// total=3, correct=3 → accuracy=1.0；judgedDisputed=1(c2)，shouldDisputed=1(c2)，tp=1
	if st.Accuracy != 1.0 || st.Precision != 1.0 || st.Recall != 1.0 {
		t.Fatalf("intentMetrics = %+v, want 全 1.0", st)
	}
}

func TestIntentMetricsMisclassify(t *testing.T) {
	// c2 应争议(Unknown)但系统误判为 ListAlerts → recall=0, precision=0/0=0, accuracy=1/2
	tasks := []Task{
		{ID: "c1", Object: ObjectChat, Expected: Expected{Intent: "AskRootCause"}},
		{ID: "c2", Object: ObjectChat, Expected: Expected{Intent: "Unknown"}},
	}
	configs := []Config{{Name: "rule"}}
	scored := map[string][]ScoredTrial{
		"c1|rule": {{TrialResult: &TrialResult{Intents: []string{"AskRootCause"}}}},
		"c2|rule": {{TrialResult: &TrialResult{Intents: []string{"ListAlerts"}}}},
	}
	st := intentMetrics(tasks, configs, scored)["rule"]
	if st.Accuracy != 0.5 {
		t.Fatalf("accuracy = %v, want 0.5", st.Accuracy)
	}
	if st.Recall != 0 {
		t.Fatalf("recall = %v, want 0", st.Recall)
	}
}

func TestIntentMetricsZeroDenom(t *testing.T) {
	tasks := []Task{{ID: "c1", Object: ObjectChat, Expected: Expected{Intent: "AskRootCause"}}}
	configs := []Config{{Name: "rule"}}
	// 无任何 trial → total=0，全部 0
	st := intentMetrics(tasks, configs, map[string][]ScoredTrial{})["rule"]
	if st.Accuracy != 0 || st.Precision != 0 || st.Recall != 0 {
		t.Fatalf("无 trial 应全 0: %+v", st)
	}
	// 有 trial 但无 shouldDisputed → recall 分母 0，记 0
	scored := map[string][]ScoredTrial{
		"c1|rule": {{TrialResult: &TrialResult{Intents: []string{"AskRootCause"}}}},
	}
	st2 := intentMetrics(tasks, configs, scored)["rule"]
	if st2.Accuracy != 1.0 || st2.Recall != 0 {
		t.Fatalf("无争议任务 recall 应为 0: %+v", st2)
	}
}

func TestAllUnknown(t *testing.T) {
	if !allUnknown(&TrialResult{Intents: []string{"Unknown"}}) {
		t.Fatal("单 Unknown 应判为争议")
	}
	if !allUnknown(&TrialResult{Intents: []string{"Unknown", "Unknown"}}) {
		t.Fatal("全 Unknown 应判为争议")
	}
	if allUnknown(&TrialResult{Intents: []string{"Unknown", "ListAlerts"}}) {
		t.Fatal("含非 Unknown 不应判为争议")
	}
	if allUnknown(&TrialResult{}) {
		t.Fatal("空意图不应判为争议")
	}
	// Intents 空但 Intent 为 Unknown → 视为全 Unknown
	if !allUnknown(&TrialResult{Intent: "Unknown"}) {
		t.Fatal("Intent=Unknown 回退应判为争议")
	}
}

func TestIsShouldDisputed(t *testing.T) {
	if !isShouldDisputed(Task{Expected: Expected{Intent: "Unknown"}}) {
		t.Fatal("Intent=Unknown 应为应争议")
	}
	if isShouldDisputed(Task{Expected: Expected{Intent: "AskRootCause"}}) {
		t.Fatal("Intent=AskRootCause 不应为应争议")
	}
	if isShouldDisputed(Task{Expected: Expected{Intents: []string{"ListAlerts"}}}) {
		t.Fatal("Intents 非空任务不应为应争议")
	}
}
