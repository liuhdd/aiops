package retrieve

import (
	"regexp"
	"strings"
)

var fpPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)Xid\s*\d+`),
	regexp.MustCompile(`ALERT-[A-Z]+-\d+`),
	regexp.MustCompile(`(?i)ERR-[A-Z0-9_-]+`),
}

var spaceRe = regexp.MustCompile(`\s+`)

// extractFingerprints 从文本中抽取指纹，去重后返回；空输入返回空切片（非 nil）。
func extractFingerprints(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{}
	}
	seen := map[string]bool{}
	var out []string
	for _, re := range fpPatterns {
		for _, m := range re.FindAllString(text, -1) {
			m = strings.TrimSpace(m)
			m = spaceRe.ReplaceAllString(m, " ")
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	if out == nil {
		return []string{}
	}
	return out
}
