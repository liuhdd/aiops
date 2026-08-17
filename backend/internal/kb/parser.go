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
	ext := strings.ToLower(filepath.Ext(filename))
	var content string
	var err error
	switch ext {
	case ".txt", ".md":
		content = string(data)
	case ".docx":
		content, err = parseDocx(data)
	default:
		return "", ErrBadFile
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(content) == "" {
		return "", ErrEmptyContent
	}
	return content, nil
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
