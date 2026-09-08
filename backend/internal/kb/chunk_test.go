package kb

import (
	"strconv"
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

func TestChunkHeadingOnlyProducesOneChunk(t *testing.T) {
	content := "# 只有标题\n## 没有正文"
	got := Chunk(content)
	if len(got) != 1 {
		t.Fatalf("纯标题文档应产 1 块, got %d", len(got))
	}
	if !strings.Contains(got[0].Content, "只有标题") || !strings.Contains(got[0].Content, "没有正文") {
		t.Fatalf("块应包含标题行内容, got %+v", got[0])
	}
}

func TestChunkKeepsTableIntact(t *testing.T) {
	var b strings.Builder
	b.WriteString("# 运维\n前言\n\n| 指标 | 阈值 |\n| --- | --- |\n")
	for i := 0; i < 100; i++ {
		b.WriteString("| cpu_usage | 90% |\n")
	}
	b.WriteString("\n尾声\n")
	got := Chunk(b.String())
	// 表格行不得被拆散到不同块（任一块包含表头即须包含全部表格行）
	for _, c := range got {
		if strings.Contains(c.Content, "| 指标 | 阈值 |") && !strings.Contains(c.Content, "尾声") {
			if strings.Count(c.Content, "| cpu_usage |") != 100 {
				t.Fatalf("表格被拆散，块内容行数不对")
			}
		}
	}
}

func TestChunkKeepsCodeFence(t *testing.T) {
	content := "# 部署\n```bash\n# 安装驱动\nsh install.sh\n```\n尾声"
	got := Chunk(content)
	if len(got) != 1 {
		t.Fatalf("代码块注释 # 不得误判标题，chunks = %+v", got)
	}
	if !strings.Contains(got[0].Content, "sh install.sh") {
		t.Fatalf("代码块内容丢失: %+v", got[0])
	}
}

func TestChunkFlowAtomicity(t *testing.T) {
	var b strings.Builder
	b.WriteString("# GPU 运维\n## 掉卡处置\n前言\n\n")
	for i := 1; i <= 7; i++ {
		b.WriteString(strings.Repeat("x", 400) + "\n")
		b.WriteString(strconv.Itoa(i) + ". 执行步骤" + "\n")
	}
	b.WriteString("> 若复位失败则跳至步骤 7\n\n| 项 | 值 |\n| --- | --- |\n| a | b |")
	b.WriteString("```bash\necho done\n```\n")
	got := Chunk(b.String())
	flows := 0
	for _, c := range got {
		if c.IsFlow {
			flows++
			for i := 1; i <= 7; i++ {
				if !strings.Contains(c.Content, strconv.Itoa(i)+". 执行步骤") {
					t.Fatalf("流程块缺步骤 %d", i)
				}
			}
			if c.HeadingPath != "GPU 运维 > 掉卡处置" {
				t.Fatalf("heading_path = %q", c.HeadingPath)
			}
		}
	}
	if flows != 1 {
		t.Fatalf("含步骤的 Section 应合并为 exactly 1 个流程块, got %d (%+v)", flows, got)
	}
}
