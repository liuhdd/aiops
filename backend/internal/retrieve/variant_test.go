package retrieve

import "testing"

func TestBaselineVariantCoversAllKinds(t *testing.T) {
	v := BaselineVariant()
	if v.Name != "baseline" {
		t.Fatalf("name=%q", v.Name)
	}
	for _, k := range CanonicalKinds {
		if _, ok := v.Steps[k]; !ok {
			t.Fatalf("baseline 缺少 kind %q", k)
		}
	}
}

func TestParseVariantPatchStepsAndParams(t *testing.T) {
	got, err := ParseVariantPatch("x", "rerank=none,fuse=weighted,gate.floor=0.15", BaselineVariant())
	if err != nil {
		t.Fatal(err)
	}
	if got.Steps["rerank"] != "none" || got.Steps["fuse"] != "weighted" {
		t.Fatalf("steps=%v", got.Steps)
	}
	if got.Params["gate"]["floor"] != "0.15" {
		t.Fatalf("params=%v", got.Params)
	}
	if got.Steps["recall"] != "hybrid" { // 未覆盖项继承 base
		t.Fatalf("未覆盖项被清空: %v", got.Steps)
	}
}

func TestParseVariantPatchUnknownKind(t *testing.T) {
	if _, err := ParseVariantPatch("x", "nope=on", BaselineVariant()); err == nil {
		t.Fatal("未知 kind 应报错")
	}
}

func TestParseVariantsSplitsAndAutoNames(t *testing.T) {
	vs, err := ParseVariants("baseline;rerank=none;fuse=weighted", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 3 || vs[0].Name != "baseline" || vs[1].Name != "rerank=none" || vs[2].Name != "fuse=weighted" {
		t.Fatalf("variants=%+v", vs)
	}
	if vs[1].Steps["rerank"] != "none" {
		t.Fatalf("patch 未生效: %+v", vs[1])
	}
}

func TestParseVariantsJSON(t *testing.T) {
	data := []byte(`[{"name":"no-rerank","steps":{"rerank":"none"},"params":{"gate":{"floor":"0.15"}}}]`)
	vs, err := ParseVariantsJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Name != "no-rerank" || vs[0].Steps["rerank"] != "none" || vs[0].Params["gate"]["floor"] != "0.15" {
		t.Fatalf("variants=%+v", vs)
	}
}
