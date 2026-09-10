package kb

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"path/filepath"
	"strings"
)

var (
	ErrBadFile      = errors.New("不支持的文件类型")
	ErrEmptyContent = errors.New("文档内容为空")
)

// ParseDoc 按扩展名解析为纯文本；非法类型/空内容返回哨兵错误。
func ParseDoc(filename string, data []byte) (string, error) {
	content, _, err := ParseDocWithMeta(filename, data)
	return content, err
}

// DocMeta 文档级元数据（frontmatter）。
type DocMeta struct {
	Version   string
	AppliesTo []string
}

// StripFrontmatter 剥离开头的 YAML frontmatter（--- 包裹）；无则原样返回。
func StripFrontmatter(raw string) (string, DocMeta) {
	var meta DocMeta
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return raw, meta
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return raw, meta
	}
	meta = parseFrontmatterMeta(lines[1:end])
	return strings.Join(lines[end+1:], "\n"), meta
}

// ParseDocWithMeta 按扩展名解析并剥离 frontmatter。
func ParseDocWithMeta(filename string, data []byte) (string, DocMeta, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	var content string
	var err error
	switch ext {
	case ".txt", ".md":
		content = string(data)
	case ".docx":
		content, err = parseDocx(data)
	default:
		return "", DocMeta{}, ErrBadFile
	}
	if err != nil {
		return "", DocMeta{}, err
	}
	body, meta := StripFrontmatter(content)
	if strings.TrimSpace(body) == "" {
		return "", DocMeta{}, ErrEmptyContent
	}
	return body, meta, nil
}

// parseFrontmatterMeta 手写解析 version/applies_to；非法行忽略不报错。
func parseFrontmatterMeta(lines []string) DocMeta {
	var meta DocMeta
	collectList := false
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if collectList {
			if strings.HasPrefix(trimmed, "-") {
				meta.AppliesTo = append(meta.AppliesTo, unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))))
				continue
			}
			collectList = false
			if trimmed == "" {
				continue
			}
		}
		idx := strings.Index(trimmed, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:idx])
		val := strings.TrimSpace(trimmed[idx+1:])
		switch key {
		case "version":
			meta.Version = unquote(val)
		case "applies_to":
			if val == "" {
				collectList = true
				continue
			}
			if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
				inner := strings.TrimSpace(val[1 : len(val)-1])
				if inner == "" {
					continue
				}
				for _, part := range strings.Split(inner, ",") {
					if v := unquote(strings.TrimSpace(part)); v != "" {
						meta.AppliesTo = append(meta.AppliesTo, v)
					}
				}
				continue
			}
			if v := unquote(val); v != "" {
				meta.AppliesTo = append(meta.AppliesTo, v)
			}
		}
	}
	return meta
}

// unquote 去首尾空格与单双引号。
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// parseDocx 抽取 word/document.xml 中所有 w:t 文本，按 w:p 段落换行。
func parseDocx(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		raw, rerr := io.ReadAll(rc)
		rc.Close()
		if rerr != nil {
			return "", rerr
		}
		var doc struct {
			Body struct {
				Paras []struct {
					Runs []struct {
						Text string `xml:"t"`
					} `xml:"r"`
				} `xml:"p"`
			} `xml:"body"`
		}
		if err := xml.Unmarshal(raw, &doc); err != nil {
			return "", err
		}
		var b strings.Builder
		for _, p := range doc.Body.Paras {
			for _, r := range p.Runs {
				b.WriteString(r.Text)
			}
			b.WriteString("\n")
		}
		return b.String(), nil
	}
	return "", errors.New("docx: 缺少 word/document.xml")
}
