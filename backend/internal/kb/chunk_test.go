package kb

import (
	"strings"
	"testing"
)

func TestChunkByHeadings(t *testing.T) {
	content := "# 第一章\n第一段\n# 第二章\n第二段\n## 子节\n第三段"
	got := Chunk(content)
	if len(got) != 3 {
		t.Fatalf("chunks = %d, want 3", len(got))
	}
	if got[0].Heading != "第一章" || !strings.Contains(got[0].Content, "第一段") {
		t.Fatalf("chunk0 = %+v", got[0])
	}
	if got[1].Heading != "第二章" || !strings.Contains(got[1].Content, "第二段") {
		t.Fatalf("chunk1 = %+v", got[1])
	}
	if got[2].Heading != "子节" || !strings.Contains(got[2].Content, "第三段") {
		t.Fatalf("chunk2 = %+v", got[2])
	}
}

func TestChunkNoHeadingsSingle(t *testing.T) {
	content := "一段没有标题的正文内容，仅一行。"
	got := Chunk(content)
	if len(got) != 1 || got[0].Heading != "" || got[0].Content != content {
		t.Fatalf("chunks = %+v", got)
	}
}

func TestChunkNoHeadingsLongSplitsBySize(t *testing.T) {
	content := strings.Repeat("字", 4500)
	got := Chunk(content)
	if len(got) != 3 {
		t.Fatalf("4500字应切3块, got %d", len(got))
	}
	for _, c := range got {
		if len([]rune(c.Content)) > DefaultChunkSize {
			t.Fatalf("块超限: %d", len([]rune(c.Content)))
		}
	}
}

func TestChunkLongHeadingGroupSplits(t *testing.T) {
	content := "# 标题\n" + strings.Repeat("长", 4500)
	got := Chunk(content)
	if len(got) < 2 {
		t.Fatalf("超长标题组应多块, got %d", len(got))
	}
	if got[0].Heading != "标题" {
		t.Fatalf("首块应保留标题, got %+v", got[0])
	}
}

func TestChunkEmpty(t *testing.T) {
	if got := Chunk(""); got != nil {
		t.Fatalf("空内容应返回 nil, got %+v", got)
	}
}
