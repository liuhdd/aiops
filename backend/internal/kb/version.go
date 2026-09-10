package kb

import (
	"regexp"
	"strconv"
	"strings"
)

// VersionRange 归一化后的版本区间；Min/Max 为空表示无界，无标注=全版本适用。
type VersionRange struct {
	Min string
	Max string
}

var (
	atVersionRe  = regexp.MustCompile(`(?i)@version\s*(>=|==|=)?\s*v?(\d[\d.]*)(\+)?`)
	availSinceRe = regexp.MustCompile(`(?i)available\s+since\s*v?(\d[\d.]*)(\+)?`)
	appliesRe    = regexp.MustCompile(`适用于\s*v?(\d[\d.]*)`)
	onlyAvailRe  = regexp.MustCompile(`仅\s*v?(\d[\d.]*)\s*可用`)
	versionNumRe = regexp.MustCompile(`v?(\d[\d.]*)`)
)

// ParseDocVersion 抽取文档级版本：frontmatter 优先，否则扫描正文前 30 行。
func ParseDocVersion(body string, meta DocMeta) VersionRange {
	if strings.TrimSpace(meta.Version) != "" {
		return parseVersionExpr(meta.Version)
	}
	lines := strings.Split(body, "\n")
	if len(lines) > 30 {
		lines = lines[:30]
	}
	if v, ok := scanLinesVersion(lines); ok {
		return v
	}
	return VersionRange{}
}

// ParseChunkVersion 抽取块级版本：块内标注优先，无标注继承文档级。
func ParseChunkVersion(blockText string, docVer VersionRange) VersionRange {
	if v, ok := scanLinesVersion(strings.Split(blockText, "\n")); ok {
		return v
	}
	return docVer
}

// MatchVersion 判断查询版本是否命中版本区间；无标注块与空查询永真。
func MatchVersion(v VersionRange, query string) bool {
	if strings.TrimSpace(query) == "" {
		return true
	}
	if v.Min == "" && v.Max == "" {
		return true
	}
	q := normalizeVersion(query)
	if q == "" {
		return true
	}
	if v.Min != "" && compareSemver(q, v.Min) < 0 {
		return false
	}
	if v.Max != "" && compareSemver(q, v.Max) > 0 {
		return false
	}
	return true
}

// scanLinesVersion 按行顺序返回首个版本标注；@version 为精确值，
// Available since/适用于为下界，仅 vX 可用为精确值。
func scanLinesVersion(lines []string) (VersionRange, bool) {
	for _, ln := range lines {
		if m := atVersionRe.FindStringSubmatch(ln); m != nil {
			n := normalizeVersion(m[2])
			if n == "" {
				continue
			}
			if m[1] == ">=" || m[3] == "+" {
				return VersionRange{Min: n}, true
			}
			return VersionRange{Min: n, Max: n}, true
		}
		if m := onlyAvailRe.FindStringSubmatch(ln); m != nil {
			if n := normalizeVersion(m[1]); n != "" {
				return VersionRange{Min: n, Max: n}, true
			}
			continue
		}
		if m := availSinceRe.FindStringSubmatch(ln); m != nil {
			if n := normalizeVersion(m[1]); n != "" {
				return VersionRange{Min: n}, true
			}
			continue
		}
		if m := appliesRe.FindStringSubmatch(ln); m != nil {
			if n := normalizeVersion(m[1]); n != "" {
				return VersionRange{Min: n}, true
			}
			continue
		}
	}
	return VersionRange{}, false
}

// parseVersionExpr 解析 frontmatter version 原始值；裸版本为精确值，
// >= / + 后缀 / 及以上为下界。
func parseVersionExpr(raw string) VersionRange {
	s := strings.TrimSpace(raw)
	if s == "" {
		return VersionRange{}
	}
	if m := onlyAvailRe.FindStringSubmatch(s); m != nil {
		if n := normalizeVersion(m[1]); n != "" {
			return VersionRange{Min: n, Max: n}
		}
		return VersionRange{}
	}
	minOnly := false
	if strings.HasSuffix(s, "+") {
		minOnly = true
		s = strings.TrimSpace(strings.TrimSuffix(s, "+"))
	}
	if rest, ok := strings.CutPrefix(s, ">="); ok {
		minOnly = true
		s = strings.TrimSpace(rest)
	} else if rest, ok := strings.CutPrefix(s, "=="); ok {
		s = strings.TrimSpace(rest)
	} else if rest, ok := strings.CutPrefix(s, "="); ok {
		s = strings.TrimSpace(rest)
	}
	if strings.Contains(s, "及以上") || strings.Contains(s, "以上") {
		minOnly = true
	}
	m := versionNumRe.FindStringSubmatch(s)
	if m == nil {
		return VersionRange{}
	}
	n := normalizeVersion(m[1])
	if n == "" {
		return VersionRange{}
	}
	if minOnly {
		return VersionRange{Min: n}
	}
	return VersionRange{Min: n, Max: n}
}

// normalizeVersion 去 v 前缀并补齐三段数字；非法输入返回空串。
func normalizeVersion(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}
	s = strings.Trim(s[:i], ".")
	if s == "" {
		return ""
	}
	parts := strings.Split(s, ".")
	nums := make([]string, 0, 3)
	for _, p := range parts {
		j := 0
		for j < len(p) && p[j] >= '0' && p[j] <= '9' {
			j++
		}
		if j == 0 {
			nums = append(nums, "0")
		} else {
			n, _ := strconv.Atoi(p[:j])
			nums = append(nums, strconv.Itoa(n))
		}
		if len(nums) == 3 {
			break
		}
	}
	for len(nums) < 3 {
		nums = append(nums, "0")
	}
	return strings.Join(nums, ".")
}

// compareSemver 按数字三段比较归一化版本；返回 -1/0/1。
func compareSemver(a, b string) int {
	pa := strings.Split(normalizeVersion(a), ".")
	pb := strings.Split(normalizeVersion(b), ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}
