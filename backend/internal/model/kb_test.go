package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestKBCategory(t *testing.T) {
	if !IsKBCategory(KBCase) || !IsKBCategory(KBManual) || !IsKBCategory(KBRunbook) || !IsKBCategory(KBOther) {
		t.Fatal("内置分类应合法")
	}
	if IsKBCategory("bogus") {
		t.Fatal("非法分类不应合法")
	}
}

func TestKbDocumentJSON(t *testing.T) {
	d := KbDocument{ID: "d1", Title: "交换机手册", Category: KBManual,
		DeviceTypes: []string{"switch"}, Vendor: []string{"H3C"}, Model: []string{"S5560"},
		Metrics: []string{"net_in"}, Keywords: []string{"光模块"}, Intents: []string{"AskRootCause"},
		SourceFile: "s5560.md", Content: "正文"}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out KbDocument
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Category != KBManual || len(out.Vendor) != 1 || out.Vendor[0] != "H3C" {
		t.Fatalf("roundtrip = %+v", out)
	}
}

func TestChunkRefJSONRoundTrip(t *testing.T) {
	c := KbChunk{ID: "k1", HeadingPath: "GPU 运维 > 掉卡处置", IsFlow: true,
		VersionMin: "3.0.0", SourceRefs: []ChunkRef{{Raw: "[[config]]", Type: "wikilink", Target: "config.md", Resolved: true}}}
	s := MustJSON(c)
	var back KbChunk
	if err := json.Unmarshal([]byte(s), &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.HeadingPath != "GPU 运维 > 掉卡处置" || !back.IsFlow || len(back.SourceRefs) != 1 {
		t.Fatalf("roundtrip = %+v", back)
	}
}

func TestKbDocSummary(t *testing.T) {
	s := KbDocSummary{KbDocument: KbDocument{ID: "d1", Title: "t"}, ChunkCount: 3}
	b, _ := json.Marshal(s)
	if !strings.Contains(string(b), `"chunk_count":3`) {
		t.Fatalf("summary json = %s", b)
	}
}
