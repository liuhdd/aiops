package model

import (
	"encoding/json"
	"testing"
)

func TestIntentKindAndIsLeaf(t *testing.T) {
	g := IntentNode{ID: "g", Kind: KindGroup}
	if g.IsLeaf() {
		t.Fatal("GROUP 应为非叶子")
	}
	l := IntentNode{ID: "l", Kind: KindKB}
	if !l.IsLeaf() {
		t.Fatal("KB 应为叶子")
	}
}

func TestIsSystemCode(t *testing.T) {
	if !IsSystemCode("ListAlerts") || !IsSystemCode("Unknown") {
		t.Fatal("内置 code 应命中")
	}
	if IsSystemCode("MyIntent") {
		t.Fatal("非内置 code 不应命中")
	}
}

func TestIntentNodeJSON(t *testing.T) {
	n := IntentNode{ID: "n1", ParentID: "g", Name: "告警列表", Kind: KindSystem,
		Code: "ListAlerts", RuleKeywords: []string{"列表", "告警"}, Enabled: true}
	b, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out IntentNode
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.RuleKeywords == nil || len(out.RuleKeywords) != 2 || out.Kind != KindSystem {
		t.Fatalf("roundtrip = %+v", out)
	}
}
