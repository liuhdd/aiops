package kb

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"aiops/internal/model"
)

func TestUploadFlowDocAtomic(t *testing.T) {
	svc := newTestService(t)
	var b strings.Builder
	b.WriteString("---\nversion: 3.0.0\n---\n# GPU 运维\n## 掉卡处置\n")
	for i := 1; i <= 5; i++ {
		b.WriteString(strings.Repeat("y", 500) + "\n" + strconv.Itoa(i) + ". 步骤\n")
	}
	doc, err := svc.Upload(context.Background(), UploadReq{SourceFile: "gpu.md", Data: []byte(b.String()), Category: model.KBManual})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if doc.VersionMin != "3.0.0" {
		t.Fatalf("doc version = %+v", doc)
	}
	detail, _ := svc.Get(context.Background(), doc.ID)
	flows := 0
	for _, c := range detail.Chunks {
		if c.IsFlow {
			flows++
			if c.VersionMin != "3.0.0" || c.HeadingPath != "GPU 运维 > 掉卡处置" {
				t.Fatalf("chunk = %+v", c)
			}
		}
	}
	if flows != 1 {
		t.Fatalf("应恰好 1 个流程块, got %+v", detail.Chunks)
	}
}

func TestUploadEmbedInline(t *testing.T) {
	svc := newTestService(t)
	// 先入库被引文档，再入库含 ![[ ]] 的文档
	base, err := svc.Upload(context.Background(), UploadReq{SourceFile: "config.md", Data: []byte("# 配置\n## 网络\nIP 设为 10.0.0.1"), Category: model.KBManual})
	if err != nil {
		t.Fatalf("base: %v", err)
	}
	doc, err := svc.Upload(context.Background(), UploadReq{SourceFile: "deploy.md", Data: []byte("# 部署\n先配网 ![[config#网络]] 再继续"), Category: model.KBManual})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	detail, _ := svc.Get(context.Background(), doc.ID)
	foundInline, foundEdge := false, false
	for _, c := range detail.Chunks {
		if strings.Contains(c.Content, "IP 设为 10.0.0.1") {
			foundInline = true
		}
		for _, r := range c.SourceRefs {
			if r.Type == "embed" && r.Resolved {
				foundEdge = true
			}
		}
	}
	if !foundInline || !foundEdge {
		t.Fatalf("inline=%v edge=%v (%+v)", foundInline, foundEdge, detail.Chunks)
	}
	_ = base
}

func TestUploadUnresolvedEmbedNoDup(t *testing.T) {
	svc := newTestService(t)
	raw := "![[missing-never-exists-xyz]]"
	content := "# 未解析回归\n## 首节\n首节正文\n## 次节\n含未解析 " + raw + " 尾"
	doc, err := svc.Upload(context.Background(), UploadReq{SourceFile: "unresolved.md", Data: []byte(content), Category: model.KBManual})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	detail, err := svc.Get(context.Background(), doc.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(detail.Chunks) < 2 {
		t.Fatalf("需要至少 2 块以覆盖非首块场景, got %+v", detail.Chunks)
	}
	ownerIdx := -1
	for i, c := range detail.Chunks {
		if strings.Contains(c.Content, raw) {
			ownerIdx = i
		}
	}
	if ownerIdx <= 0 {
		t.Fatalf("未解析标记应落在非首块, owner=%d (%+v)", ownerIdx, detail.Chunks)
	}
	count := 0
	for _, c := range detail.Chunks {
		for _, r := range c.SourceRefs {
			if r.Raw == raw {
				count++
			}
		}
	}
	if count != 1 {
		t.Fatalf("未解析 embed 应恰好出现一次, got %d (%+v)", count, detail.Chunks)
	}
}
