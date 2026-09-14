package main

import (
	"bytes"
	"strings"
	"testing"

	"aiops/internal/retrieve"
)

func namesOf(vs []retrieve.Variant) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.Name
	}
	return out
}

func TestSelectVariantsFileOnly(t *testing.T) {
	file := []retrieve.Variant{
		{Name: "no-rerank", Steps: map[string]string{"rerank": "none"}},
		{Name: "weighted", Steps: map[string]string{"fuse": "weighted"}},
	}
	got, err := selectVariants("", file)
	if err != nil {
		t.Fatal(err)
	}
	names := namesOf(got)
	if len(names) != 3 || names[0] != "baseline" || names[1] != "no-rerank" || names[2] != "weighted" {
		t.Fatalf("仅 file 时应运行文件变体 + baseline，got=%v", names)
	}
}

func TestSelectVariantsSpecOnly(t *testing.T) {
	got, err := selectVariants("baseline;rerank=none", nil)
	if err != nil {
		t.Fatal(err)
	}
	if names := namesOf(got); len(names) != 2 || names[0] != "baseline" || names[1] != "rerank=none" {
		t.Fatalf("仅 spec 语义变化: %v", names)
	}
}

func TestSelectVariantsSpecReferencesFile(t *testing.T) {
	file := []retrieve.Variant{{Name: "nr", Steps: map[string]string{"rerank": "none"}}}
	got, err := selectVariants("nr", file)
	if err != nil {
		t.Fatal(err)
	}
	if names := namesOf(got); len(names) != 2 || names[0] != "baseline" || names[1] != "nr" {
		t.Fatalf("spec 引用文件具名变体失败: %v", names)
	}
}

func TestSelectVariantsEmptyDefaultsBaseline(t *testing.T) {
	got, err := selectVariants("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if names := namesOf(got); len(names) != 1 || names[0] != "baseline" {
		t.Fatalf("两者皆空应默认 baseline: %v", names)
	}
}

func TestSelectVariantsInvalidSpec(t *testing.T) {
	if _, err := selectVariants("nope=x", nil); err == nil {
		t.Fatal("非法 spec 应报错")
	}
}

func TestPrintComparisonDeltaOrderDeterministic(t *testing.T) {
	c := ComparisonReport{
		Meta: CompareMeta{Baseline: "baseline"},
		Variants: []VariantReport{
			{Name: "baseline"},
			{Name: "zeta"},
			{Name: "alpha"},
		},
		Deltas: map[string]Metrics{
			"zeta":  {MRR: 0.2},
			"alpha": {MRR: 0.1},
		},
	}
	var buf bytes.Buffer
	printComparison(&buf, c)
	out := buf.String()
	zi := strings.Index(out, "zeta")
	ai := strings.Index(out, "alpha")
	if zi < 0 || ai < 0 {
		t.Fatalf("delta 行缺失:\n%s", out)
	}
	if zi > ai {
		t.Fatalf("delta 应按 variants 顺序输出（zeta 先于 alpha）:\n%s", out)
	}
}
