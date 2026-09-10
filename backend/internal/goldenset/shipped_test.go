package goldenset

import (
	"path/filepath"
	"testing"

	"aiops/internal/mockdata"
)

func TestShippedGoldenSetLoads(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "golden_set.jsonl")
	items, err := Load(path)
	if err != nil {
		t.Fatalf("加载受控 golden set 失败: %v", err)
	}
	if len(items) < 57 {
		t.Fatalf("golden set 应 >=57 条, got %d", len(items))
	}
	ids := map[string]bool{}
	known := map[string]bool{}
	for _, d := range mockdata.Docs() {
		known[d.ID] = true
	}
	for _, it := range items {
		if ids[it.ID] {
			t.Fatalf("golden id 重复: %s", it.ID)
		}
		ids[it.ID] = true
		for _, did := range append(append([]string{}, it.Expect.DocIDs...), it.Expect.DistractorDocIDs...) {
			if !known[did] {
				t.Fatalf("%s 引用未知 doc_id %s", it.ID, did)
			}
		}
	}
}
