package kb

import "testing"

func TestExtractFingerprints(t *testing.T) {
	cases := []struct {
		title, content string
		want           []string
	}{
		{"GPU 掉卡 Xid 48", "日志含 Xid  48 重复", []string{"Xid 48"}},
		{"ALERT-GPU-001 告警", "", []string{"ALERT-GPU-001"}},
		{"", "NVMe 错误 ERR-NVME-0x4004", []string{"ERR-NVME-0x4004"}},
		{"", "无指纹文本", nil},
		{"Xid 48 和 Xid 48", "", []string{"Xid 48"}},
	}
	for _, c := range cases {
		got := ExtractFingerprints(c.title, c.content)
		// set 语义比对
		if c.want == nil {
			if len(got) != 0 {
				t.Fatalf("title=%q content=%q want empty got %v", c.title, c.content, got)
			}
			continue
		}
		if len(got) != len(c.want) {
			t.Fatalf("title=%q content=%q want %v got %v", c.title, c.content, c.want, got)
		}
		wantSet := map[string]bool{}
		for _, w := range c.want {
			wantSet[w] = true
		}
		for _, g := range got {
			if !wantSet[g] {
				t.Fatalf("title=%q content=%q want %v got %v", c.title, c.content, c.want, got)
			}
		}
	}
}

func TestExtractFingerprintsEmptyInput(t *testing.T) {
	if got := ExtractFingerprints("", ""); got == nil || len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}
