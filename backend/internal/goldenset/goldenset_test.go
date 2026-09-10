package goldenset

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFixture(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "g.jsonl")
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadNormalizesDefaults(t *testing.T) {
	p := writeFixture(t,
		`{"id":"q1","type":"fact","question":"Q","expect":{"doc_ids":["d1"]}}`,
		`{"id":"q2","type":"no_answer","question":"Q2","expect":{},"top_k":5}`,
	)
	items, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("len = %d", len(items))
	}
	if items[0].TopK != 3 || items[0].Expect.Behavior != "hit" {
		t.Fatalf("q1 = %+v", items[0])
	}
	if items[0].Expect.DistractorDocIDs == nil {
		t.Fatal("distractor 应归一为空切片而非 nil")
	}
	if items[1].TopK != 5 || items[1].Expect.Behavior != "reject" || len(items[1].Expect.DocIDs) != 0 {
		t.Fatalf("q2 = %+v", items[1])
	}
}

func TestToPhoenixExamples(t *testing.T) {
	items := []Item{{ID: "q1", Type: "fact", Question: "Q", TopK: 3, Expect: Expect{DocIDs: []string{"d1"}, Behavior: "hit", DistractorDocIDs: []string{}}}}
	exs := ToPhoenixExamples(items)
	if len(exs) != 1 || exs[0].ID != "q1" {
		t.Fatalf("exs = %+v", exs)
	}
	if exs[0].Input["question"] != "Q" || exs[0].Input["top_k"] != 3 {
		t.Fatalf("input = %v", exs[0].Input)
	}
	if exs[0].Output["behavior"] != "hit" {
		t.Fatalf("output = %v", exs[0].Output)
	}
	if exs[0].Metadata["type"] != "fact" || exs[0].Splits[0] != "fact" {
		t.Fatalf("meta/splits = %v/%v", exs[0].Metadata, exs[0].Splits)
	}
}
