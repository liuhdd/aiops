// Package goldenset 承载检索质量标注集（golden set）的解析、归一与 Phoenix 数据集映射。
package goldenset

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"

	"aiops/internal/phoenix"
)

type Expect struct {
	DocIDs           []string `json:"doc_ids"`
	DistractorDocIDs []string `json:"distractor_doc_ids"`
	Behavior         string   `json:"behavior"`
}

type Item struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Question string   `json:"question"`
	History  []string `json:"history"`
	Expect   Expect   `json:"expect"`
	TopK     int      `json:"top_k"`
}

// Load 读取 JSONL 标注集，逐行归一（TopK 缺省 3、behavior 缺省按 doc_ids 推断、切片非 nil）。
func Load(path string) ([]Item, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var items []Item
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var it Item
		if err := json.Unmarshal([]byte(line), &it); err != nil {
			return nil, err
		}
		normalize(&it)
		items = append(items, it)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func normalize(it *Item) {
	if it.TopK == 0 {
		it.TopK = 3
	}
	if it.Expect.DocIDs == nil {
		it.Expect.DocIDs = []string{}
	}
	if it.Expect.DistractorDocIDs == nil {
		it.Expect.DistractorDocIDs = []string{}
	}
	if it.Expect.Behavior == "" {
		if len(it.Expect.DocIDs) > 0 {
			it.Expect.Behavior = "hit"
		} else {
			it.Expect.Behavior = "reject"
		}
	}
}

// RagDatasetName Phoenix 上检索数据集的名称。
const RagDatasetName = "aiops-rag-golden"

// ToPhoenixExamples 把标注集映射为 Phoenix 数据集样本（output 存作者原始 id）。
func ToPhoenixExamples(items []Item) []phoenix.Example {
	out := make([]phoenix.Example, 0, len(items))
	for _, it := range items {
		out = append(out, phoenix.Example{
			ID:       it.ID,
			Input:    map[string]any{"question": it.Question, "top_k": it.TopK},
			Output:   map[string]any{"doc_ids": it.Expect.DocIDs, "distractor_doc_ids": it.Expect.DistractorDocIDs, "behavior": it.Expect.Behavior},
			Metadata: map[string]any{"id": it.ID, "type": it.Type},
			Splits:   []string{it.Type},
		})
	}
	return out
}
