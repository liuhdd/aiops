package kb

import "testing"

func TestParseDocVersionPriority(t *testing.T) {
	// frontmatter 优先于正文 @version
	got := ParseDocVersion("@version 1.0.0\n正文", DocMeta{Version: "2.1.0"})
	if got.Min != "2.1.0" {
		t.Fatalf("got %+v", got)
	}
	// 正文 Available since 回退（只扫前 30 行）
	got2 := ParseDocVersion("前言\nAvailable since v3.0\n正文", DocMeta{})
	if got2.Min != "3.0.0" {
		t.Fatalf("got %+v", got2)
	}
	// 无标注为空（全版本适用）
	got3 := ParseDocVersion("纯正文", DocMeta{})
	if got3.Min != "" || got3.Max != "" {
		t.Fatalf("got %+v", got3)
	}
}

func TestChunkVersionInherit(t *testing.T) {
	doc := VersionRange{Min: "2.0.0"}
	// 块内标注优先
	if got := ParseChunkVersion("本步骤 Available since v3.0", doc); got.Min != "3.0.0" {
		t.Fatalf("got %+v", got)
	}
	// 无标注继承文档级
	if got := ParseChunkVersion("普通步骤", doc); got.Min != "2.0.0" {
		t.Fatalf("got %+v", got)
	}
}

func TestMatchVersion(t *testing.T) {
	if !MatchVersion(VersionRange{}, "3.1.0") {
		t.Fatalf("无标注块应永真")
	}
	if !MatchVersion(VersionRange{Min: "3.0.0"}, "3.1.0") || MatchVersion(VersionRange{Min: "3.0.0"}, "2.9.0") {
		t.Fatalf("下界匹配错误")
	}
	if MatchVersion(VersionRange{Min: "3.0.0", Max: "3.5.0"}, "4.0.0") {
		t.Fatalf("上界匹配错误")
	}
	if !MatchVersion(VersionRange{Min: "3.0.0"}, "") {
		t.Fatalf("空查询应永真")
	}
}
