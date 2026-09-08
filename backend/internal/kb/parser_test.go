package kb

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestParseTxtAndMd(t *testing.T) {
	if got, err := ParseDoc("a.txt", []byte("hello")); err != nil || got != "hello" {
		t.Fatalf("txt = %q %v", got, err)
	}
	if got, err := ParseDoc("b.md", []byte("# 标题\n正文")); err != nil || got != "# 标题\n正文" {
		t.Fatalf("md = %q %v", got, err)
	}
}

func TestParseDocx(t *testing.T) {
	data := makeDocx(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>第一段</w:t></w:r></w:p><w:p><w:r><w:t>第二段</w:t></w:r></w:p></w:body></w:document>`)
	got, err := ParseDoc("doc.docx", data)
	if err != nil {
		t.Fatalf("docx parse: %v", err)
	}
	if !bytes.Contains([]byte(got), []byte("第一段")) || !bytes.Contains([]byte(got), []byte("第二段")) {
		t.Fatalf("docx text = %q", got)
	}
}

func TestParseBadFile(t *testing.T) {
	if _, err := ParseDoc("a.pdf", []byte("x")); !errors.Is(err, ErrBadFile) {
		t.Fatalf("pdf 应 ErrBadFile, got %v", err)
	}
	if _, err := ParseDoc("a.exe", nil); !errors.Is(err, ErrBadFile) {
		t.Fatalf("exe 应 ErrBadFile, got %v", err)
	}
	if _, err := ParseDoc("noext", []byte("x")); !errors.Is(err, ErrBadFile) {
		t.Fatalf("无扩展名应 ErrBadFile, got %v", err)
	}
}

func TestParseEmptyContent(t *testing.T) {
	if _, err := ParseDoc("a.txt", []byte("")); !errors.Is(err, ErrEmptyContent) {
		t.Fatalf("空内容应 ErrEmptyContent, got %v", err)
	}
	if _, err := ParseDoc("a.txt", []byte("   \n\t ")); !errors.Is(err, ErrEmptyContent) {
		t.Fatalf("空白内容应 ErrEmptyContent, got %v", err)
	}
}

// makeDocx 用手写 zip 生成最小 docx 字节。
func makeDocx(documentXML string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("word/document.xml")
	_, _ = w.Write([]byte(documentXML))
	_ = zw.Close()
	return buf.Bytes()
}

func TestParseFrontmatter(t *testing.T) {
	raw := "---\nversion: 2.1.0\napplies_to: [\">=2.0\", \"<3.0\"]\n---\n# 标题\n正文"
	body, meta := StripFrontmatter(raw)
	if meta.Version != "2.1.0" {
		t.Fatalf("version = %q", meta.Version)
	}
	if len(meta.AppliesTo) != 2 {
		t.Fatalf("applies_to = %v", meta.AppliesTo)
	}
	if strings.Contains(body, "version:") || !strings.Contains(body, "# 标题") {
		t.Fatalf("body = %q", body)
	}
}

func TestParseFrontmatterAbsent(t *testing.T) {
	body, meta := StripFrontmatter("# 标题\n正文")
	if meta.Version != "" || body != "# 标题\n正文" {
		t.Fatalf("got %q %+v", body, meta)
	}
}

func TestParseDocStripsFrontmatter(t *testing.T) {
	got, err := ParseDoc("a.md", []byte("---\nversion: 1.0.0\n---\nhello"))
	if err != nil || got != "hello" {
		t.Fatalf("got %q %v", got, err)
	}
}
