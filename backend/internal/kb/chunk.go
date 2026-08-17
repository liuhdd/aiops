package kb

import (
	"regexp"
	"strings"
)

// DefaultChunkSize 无标题结构时的固定块大小（rune 数）。
const DefaultChunkSize = 2000

// ChunkResult 一个知识块（heading 可为空）。
type ChunkResult struct {
	Heading string
	Content string
}

var headingRe = regexp.MustCompile(`^#{1,4}\s(.*)$`)

func headingOf(line string) (string, bool) {
	m := headingRe.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
}

func splitBySize(content string, size int) []ChunkResult {
	if size <= 0 {
		size = DefaultChunkSize
	}
	runes := []rune(content)
	var out []ChunkResult
	for start := 0; start < len(runes); start += size {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, ChunkResult{Content: string(runes[start:end])})
	}
	return out
}

// Chunk 优先按 Markdown 标题切块；无标题或组超长时按固定大小二次切。空内容返回 nil。
func Chunk(content string) []ChunkResult {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	var groups []ChunkResult
	cur := ChunkResult{}
	flush := func() {
		if strings.TrimSpace(cur.Content) != "" {
			groups = append(groups, cur)
		}
		cur = ChunkResult{}
	}
	for _, line := range strings.Split(content, "\n") {
		if h, ok := headingOf(line); ok {
			flush()
			cur.Heading = h
			continue
		}
		if cur.Content != "" {
			cur.Content += "\n"
		}
		cur.Content += line
	}
	flush()
	// 纯标题文档：无正文分组时保证至少产 1 块（标题行作为内容）。
	if len(groups) == 0 && strings.TrimSpace(content) != "" {
		groups = append(groups, ChunkResult{Content: content})
	}
	var out []ChunkResult
	for _, g := range groups {
		if len([]rune(g.Content)) <= DefaultChunkSize {
			out = append(out, g)
			continue
		}
		for i, piece := range splitBySize(g.Content, DefaultChunkSize) {
			p := piece
			if i == 0 {
				p.Heading = g.Heading
			}
			out = append(out, p)
		}
	}
	return out
}
