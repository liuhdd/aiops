package kb

import (
	"regexp"
	"strings"
)

var fpPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)Xid\s*\d+`),
	regexp.MustCompile(`ALERT-[A-Z]+-\d+`),
	regexp.MustCompile(`(?i)ERR-[A-Z0-9_-]+`),
}

func ExtractFingerprints(title, content string) []string {
	text := strings.TrimSpace(title + "\n" + content)
	if text == "" {
		return []string{}
	}
	seen := map[string]bool{}
	var out []string
	for _, re := range fpPatterns {
		for _, m := range re.FindAllString(text, -1) {
			m = strings.TrimSpace(m)
			// 归一化 Xid 的空格
			m = regexp.MustCompile(`\s+`).ReplaceAllString(m, " ")
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
