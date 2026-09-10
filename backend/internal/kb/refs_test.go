package kb

import (
	"strings"
	"testing"
)

func TestExtractRefs(t *testing.T) {
	content := "详见[配置](./config.md)与[安装步骤](#installation)，另见 [[wikilinks]] 与 ![[embeds]]，站外[文档](https://example.com/x)忽略锚点"
	refs := ExtractRefs(content)
	byType := map[string]int{}
	for _, r := range refs {
		byType[r.Type]++
	}
	if byType["md_link"] != 1 || byType["anchor"] != 1 || byType["wikilink"] != 1 || byType["embed"] != 1 {
		t.Fatalf("refs = %+v", refs)
	}
	// anchor 抽取
	for _, r := range refs {
		if r.Type == "anchor" && r.Anchor != "installation" {
			t.Fatalf("anchor = %+v", r)
		}
		if r.Type == "wikilink" && r.Target != "wikilinks" {
			t.Fatalf("wikilink = %+v", r)
		}
	}
}

func TestInlineEmbeds(t *testing.T) {
	lookup := func(target string) (string, string, bool) {
		if target == "config" {
			return "# 配置\n## 网络\nIP 设置", "2.0.0", true
		}
		return "", "", false
	}
	out := InlineEmbeds("前言\n![[config#网络]]\n后记", lookup, 2)
	if !strings.Contains(out, "嵌入自 [[config#网络]]") || !strings.Contains(out, "IP 设置") {
		t.Fatalf("out = %q", out)
	}
	if strings.Contains(out, "![[config") {
		t.Fatalf("应已替换标记")
	}
}

func TestInlineEmbedsMissAndCycle(t *testing.T) {
	lookup := func(target string) (string, string, bool) { return "", "", false }
	out := InlineEmbeds("![[missing]]", lookup, 2)
	if !strings.Contains(out, "![[missing]]") {
		t.Fatalf("未命中应保留原文标记, got %q", out)
	}
	// 循环：A 展开后含 B，B 展开后含 A，最深 2 层后保留标记
	n := 0
	cyc := func(target string) (string, string, bool) {
		n++
		if target == "A" {
			return "A 内容 ![[B]]", "", true
		}
		return "B 内容 ![[A]]", "", true
	}
	out2 := InlineEmbeds("![[A]]", cyc, 2)
	if strings.Count(out2, "![[") > 1 {
		t.Fatalf("循环应截断, got %q", out2)
	}
}
