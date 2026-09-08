package kb

import (
	"regexp"
	"sort"
	"strings"

	"aiops/internal/model"
)

var (
	// embedRe 匹配 ![[target#anchor]] 嵌入标记。
	embedRe = regexp.MustCompile(`!\[\[([^\]]+)\]\]`)
	// wikiRe 匹配 [[target]] 互链，命中后需排除 embed（前一字符为 !）。
	wikiRe = regexp.MustCompile(`\[\[([^\]]+)\]\]`)
	// mdLinkRe 匹配 [文本](目标) 行内链接。
	mdLinkRe = regexp.MustCompile(`\[([^\]]*)\]\(([^)]+)\)`)
)

// ExtractRefs 抽取块内跨文档引用：md_link/anchor/wikilink/embed 四类，站外 http(s) 忽略。
func ExtractRefs(content string) []model.ChunkRef {
	type cand struct {
		pos int
		ref model.ChunkRef
	}
	var out []cand
	for _, loc := range embedRe.FindAllStringSubmatchIndex(content, -1) {
		raw := content[loc[0]:loc[1]]
		inner := content[loc[2]:loc[3]]
		target, anchor := parseWikiInner(inner)
		if target == "" {
			continue
		}
		out = append(out, cand{pos: loc[0], ref: model.ChunkRef{Raw: raw, Type: "embed", Target: target, Anchor: anchor}})
	}
	for _, loc := range wikiRe.FindAllStringSubmatchIndex(content, -1) {
		// embed 中的 [[...]] 部分不再重复计为 wikilink。
		if loc[0] > 0 && content[loc[0]-1] == '!' {
			continue
		}
		raw := content[loc[0]:loc[1]]
		inner := content[loc[2]:loc[3]]
		target, anchor := parseWikiInner(inner)
		if target == "" {
			continue
		}
		out = append(out, cand{pos: loc[0], ref: model.ChunkRef{Raw: raw, Type: "wikilink", Target: target, Anchor: anchor}})
	}
	for _, loc := range mdLinkRe.FindAllStringSubmatchIndex(content, -1) {
		// 图片式 ![alt](...) 不计为引用。
		if loc[0] > 0 && content[loc[0]-1] == '!' {
			continue
		}
		raw := content[loc[0]:loc[1]]
		dest := strings.TrimSpace(content[loc[4]:loc[5]])
		if dest == "" {
			continue
		}
		// 去标题部分，仅保留目标地址。
		if f := strings.Fields(dest); len(f) > 1 {
			dest = strings.Trim(f[0], `"'<>`)
		} else {
			dest = strings.Trim(dest, `"'<>`)
		}
		lower := strings.ToLower(dest)
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
			continue
		}
		if strings.HasPrefix(dest, "#") {
			anchor := strings.TrimSpace(strings.TrimPrefix(dest, "#"))
			if anchor == "" {
				continue
			}
			out = append(out, cand{pos: loc[0], ref: model.ChunkRef{Raw: raw, Type: "anchor", Anchor: anchor}})
			continue
		}
		target, anchor := dest, ""
		if idx := strings.Index(dest, "#"); idx >= 0 {
			target = strings.TrimSpace(dest[:idx])
			anchor = strings.TrimSpace(dest[idx+1:])
		}
		if target == "" {
			continue
		}
		out = append(out, cand{pos: loc[0], ref: model.ChunkRef{Raw: raw, Type: "md_link", Target: target, Anchor: anchor}})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].pos < out[j].pos })
	refs := make([]model.ChunkRef, 0, len(out))
	for _, c := range out {
		refs = append(refs, c.ref)
	}
	return refs
}

// InlineEmbeds 将内容中的 ![[target#anchor]] 替换为目标文档段 inline 块；未命中保留原标记，递归受 maxDepth 截断防循环。
func InlineEmbeds(content string, lookup func(target string) (string, string, bool), maxDepth int) string {
	if maxDepth <= 0 {
		return content
	}
	return embedRe.ReplaceAllStringFunc(content, func(m string) string {
		sub := embedRe.FindStringSubmatch(m)
		if sub == nil {
			return m
		}
		target, anchor := parseWikiInner(sub[1])
		if target == "" {
			return m
		}
		doc, ver, ok := lookup(target)
		if !ok {
			return m
		}
		snippet := extractSection(doc, anchor)
		snippet = InlineEmbeds(snippet, lookup, maxDepth-1)
		return formatEmbed(target, anchor, ver, snippet)
	})
}

// parseWikiInner 解析 wikilink/embed 内部：[[a|别名]] 取 | 前，# 后为锚点。
func parseWikiInner(inner string) (string, string) {
	s := strings.TrimSpace(inner)
	if idx := strings.Index(s, "|"); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	if idx := strings.Index(s, "#"); idx >= 0 {
		return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+1:])
	}
	return s, ""
}

// extractSection 有 anchor 时取目标文档该 heading 段（至下一同级或更高级 heading 止），无 anchor 取全文；均截断至 1500 rune。
func extractSection(doc, anchor string) string {
	if strings.TrimSpace(anchor) == "" {
		return truncateRunes(doc, 1500)
	}
	lines := strings.Split(doc, "\n")
	found, level := -1, 0
	for i, ln := range lines {
		lv, title, ok := parseSectionHeading(ln)
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(title), strings.TrimSpace(anchor)) {
			found, level = i, lv
			break
		}
	}
	if found < 0 {
		return truncateRunes(doc, 1500)
	}
	end := len(lines)
	for j := found + 1; j < len(lines); j++ {
		if lv, _, ok := parseSectionHeading(lines[j]); ok && lv <= level {
			end = j
			break
		}
	}
	return truncateRunes(strings.Join(lines[found:end], "\n"), 1500)
}

// parseSectionHeading 解析 Markdown 标题行，返回级别与标题文本。
func parseSectionHeading(line string) (int, string, bool) {
	t := strings.TrimSpace(line)
	i := 0
	for i < len(t) && t[i] == '#' {
		i++
	}
	if i == 0 || i > 6 {
		return 0, "", false
	}
	title := strings.TrimSpace(t[i:])
	if title == "" {
		return 0, "", false
	}
	return i, title, true
}

// formatEmbed 组装嵌入块：首行标注来源与版本，其余行加引用前缀。
func formatEmbed(target, anchor, version, snippet string) string {
	disp := strings.TrimSpace(target)
	if a := strings.TrimSpace(anchor); a != "" {
		disp += "#" + a
	}
	var b strings.Builder
	b.WriteString("> 📎 嵌入自 [[")
	b.WriteString(disp)
	b.WriteString("]]")
	if v := strings.TrimSpace(version); v != "" {
		if !strings.HasPrefix(v, "v") && !strings.HasPrefix(v, "V") {
			v = "v" + v
		}
		b.WriteString("（")
		b.WriteString(v)
		b.WriteString("）")
	}
	b.WriteString("\n")
	lines := strings.Split(snippet, "\n")
	for i, ln := range lines {
		if ln == "" {
			b.WriteString(">")
		} else {
			b.WriteString("> ")
			b.WriteString(ln)
		}
		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
