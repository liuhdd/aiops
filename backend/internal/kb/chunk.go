package kb

import (
	"log"
	"regexp"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// DefaultChunkSize 非流程块的目标上限（rune 数）；切点只落在段落/原子块边界，超长单段落按大小二次切分。
const DefaultChunkSize = 2000

// MaxFlowChunkSize 流程块上限（rune 数）；超限仅日志告警，仍不拆散，保证步骤序列语义闭合。
const MaxFlowChunkSize = 8000

// ChunkResult 一个知识块（heading 可为空）。
type ChunkResult struct {
	Heading string
	// HeadingPath 自顶层标题到本块标题的完整路径，以 " > " 连接；无标题块为空。
	HeadingPath string
	Content     string
	// IsFlow 是否为含步骤序列的语义闭合流程块。
	IsFlow bool
}

// headingRe 仅回退路径使用：匹配 1-6 级 ATX 标题。
var headingRe = regexp.MustCompile(`^#{1,6}\s(.*)$`)

// mdParser GFM 解析器（表格为扩展支持，围栏代码块/引用块/Setext 标题为 core 支持）。
var mdParser = goldmark.New(goldmark.WithExtensions(extension.GFM))

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

// Chunk 优先按 goldmark AST 做语义闭合切块：表格/代码块/引用块/列表为原子块
// （不从中间拆散），含步骤序列（有序列表）的节合并为单个流程块；切点只落在
// 段落/原子块边界。块 Content 为原文连续行且顺序与文档一致。
// goldmark 解析失败时回退旧逐行逻辑；空内容返回 nil。
func Chunk(content string) (out []ChunkResult) {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("kb: chunkAST panic %v，回退旧逻辑", r)
			out = chunkLineFallback(content)
		}
	}()
	if chunks, ok := chunkAST(content); ok {
		return chunks
	}
	return chunkLineFallback(content)
}

// blockSpan 顶层块的原文行区间与原子属性。
type blockSpan struct {
	start, end int  // 原文行区间 [start, end)，不含标题行
	atomic     bool // 表格/代码块/引用块/列表：不可从中间拆分
	ordered    bool // 有序列表（步骤序列）
}

// astSection 一个标题节：标题与其后的顶层块。
type astSection struct {
	heading string
	path    string
	blocks  []blockSpan
}

// builtChunk 内部构建中的块，singleAtom 供二次切分时跳过单原子块。
type builtChunk struct {
	ChunkResult
	singleAtom bool
}

// chunkAST goldmark 语义切块；ok=false 表示解析失败，调用方回退旧逻辑。
func chunkAST(content string) (chunks []ChunkResult, ok bool) {
	src := []byte(content)
	lines := strings.Split(content, "\n")
	doc := mdParser.Parser().Parse(text.NewReader(src))
	if doc == nil {
		return nil, false
	}
	lineStarts := make([]int, 0, len(lines)+1)
	for off := 0; off <= len(src); {
		lineStarts = append(lineStarts, off)
		i := strings.IndexByte(content[off:], '\n')
		if i < 0 {
			break
		}
		off += i + 1
	}

	var sections []*astSection
	cur := &astSection{}
	sections = append(sections, cur)
	// 标题层级栈，用于维护 HeadingPath。
	type levelTitle struct {
		level int
		title string
	}
	var stack []levelTitle
	firstHeading, firstPath := "", ""
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		if h, isHeading := n.(*ast.Heading); isHeading {
			title := strings.TrimSpace(string(h.Text(src)))
			for len(stack) > 0 && stack[len(stack)-1].level >= h.Level {
				stack = stack[:len(stack)-1]
			}
			stack = append(stack, levelTitle{level: h.Level, title: title})
			parts := make([]string, 0, len(stack))
			for _, lt := range stack {
				parts = append(parts, lt.title)
			}
			cur = &astSection{heading: title, path: strings.Join(parts, " > ")}
			sections = append(sections, cur)
			if firstHeading == "" {
				firstHeading, firstPath = title, cur.path
			}
			continue
		}
		start, end := spanLines(n, lineStarts, len(lines))
		if start < 0 || end <= start {
			continue
		}
		// 围栏代码块的 Lines() 仅覆盖起止围栏之间的内容行，补上围栏行以保留代码原样。
		if _, isFence := n.(*ast.FencedCodeBlock); isFence {
			if start > 0 && strings.HasPrefix(strings.TrimSpace(lines[start-1]), "```") {
				start--
			}
			if end < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[end]), "```") {
				end++
			}
		}
		bs := blockSpan{start: start, end: end}
		switch node := n.(type) {
		case *ast.List:
			bs.atomic = true
			bs.ordered = node.IsOrdered()
		case *ast.FencedCodeBlock, *ast.CodeBlock, *ast.Blockquote, *east.Table:
			bs.atomic = true
		}
		cur.blocks = append(cur.blocks, bs)
	}

	for _, sec := range sections {
		chunks = append(chunks, emitSection(sec, lines)...)
	}
	// 纯标题文档：无正文块时保证至少产 1 块（标题行作为内容）。
	if len(chunks) == 0 {
		chunks = append(chunks, ChunkResult{Heading: firstHeading, HeadingPath: firstPath, Content: content})
	}
	return chunks, true
}

// emitSection 产出一个节的块：含步骤序列的节合并为单个流程块；其余节按块边界累积。
func emitSection(sec *astSection, lines []string) []ChunkResult {
	if len(sec.blocks) == 0 {
		return nil
	}
	for _, b := range sec.blocks {
		if b.ordered {
			content := strings.Join(lines[sec.blocks[0].start:sec.blocks[len(sec.blocks)-1].end], "\n")
			if len([]rune(content)) > MaxFlowChunkSize {
				log.Printf("kb: 流程块超限（heading=%q，%d rune），仍保持不拆", sec.path, len([]rune(content)))
			}
			return []ChunkResult{{Heading: sec.heading, HeadingPath: sec.path, Content: content, IsFlow: true}}
		}
	}
	var built []builtChunk
	accStart, accEnd, accCount := sec.blocks[0].start, sec.blocks[0].start, 0
	accSingleAtom := false
	flush := func() {
		if accEnd <= accStart {
			return
		}
		built = append(built, builtChunk{
			ChunkResult: ChunkResult{
				Heading:     sec.heading,
				HeadingPath: sec.path,
				Content:     strings.Join(lines[accStart:accEnd], "\n"),
			},
			singleAtom: accSingleAtom,
		})
	}
	for _, b := range sec.blocks {
		if candidate := strings.Join(lines[accStart:b.end], "\n"); accCount > 0 && len([]rune(candidate)) > DefaultChunkSize {
			flush()
			accStart, accEnd, accCount = b.start, b.start, 0
		}
		accEnd = b.end
		accCount++
		accSingleAtom = accCount == 1 && b.atomic
	}
	flush()
	// 二次切分：超长非原子块按大小切（首块保留标题）；单原子块宁可超限也不拆散。
	var out []ChunkResult
	for _, c := range built {
		if c.IsFlow || c.singleAtom || len([]rune(c.Content)) <= DefaultChunkSize {
			out = append(out, c.ChunkResult)
			continue
		}
		for i, piece := range splitBySize(c.Content, DefaultChunkSize) {
			if i == 0 {
				piece.Heading = c.Heading
				piece.HeadingPath = c.HeadingPath
			}
			out = append(out, piece)
		}
	}
	return out
}

// spanLines 取节点（含后代）覆盖的原文行区间 [start, end)；无有效行返回 (-1, -1)。
func spanLines(n ast.Node, lineStarts []int, nlines int) (int, int) {
	minOff, maxOff := -1, -1
	_ = ast.Walk(n, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		// 行内节点无行信息，调用 Lines() 会 panic，只采集块节点。
		if !entering || node.Type() != ast.TypeBlock {
			return ast.WalkContinue, nil
		}
		segs := node.Lines()
		for i := 0; i < segs.Len(); i++ {
			seg := segs.At(i)
			if seg.Start >= seg.Stop {
				continue
			}
			if minOff < 0 || seg.Start < minOff {
				minOff = seg.Start
			}
			if maxOff < 0 || seg.Stop > maxOff {
				maxOff = seg.Stop
			}
		}
		return ast.WalkContinue, nil
	})
	if minOff < 0 {
		return -1, -1
	}
	start := lineOf(lineStarts, minOff)
	end := lineOf(lineStarts, maxOff-1) + 1
	if end > nlines {
		end = nlines
	}
	return start, end
}

// lineOf 字节偏移所属行号。
func lineOf(lineStarts []int, off int) int {
	i := sort.Search(len(lineStarts), func(i int) bool { return lineStarts[i] > off })
	return i - 1
}

// chunkLineFallback 旧逐行切块逻辑（goldmark 解析失败时回退）。
func chunkLineFallback(content string) []ChunkResult {
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
			cur.HeadingPath = h
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
				p.HeadingPath = g.HeadingPath
			}
			out = append(out, p)
		}
	}
	return out
}
