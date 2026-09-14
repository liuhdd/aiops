package retrieve

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// CanonicalKinds 链路环节规范顺序；变体按 Kind 覆盖，装配固定按此序。
var CanonicalKinds = []string{"fingerprint", "rewrite", "recall", "fuse", "relevance", "rerank", "gate"}

// Variant 一组「Kind → 实现名(+参数)」选择；Name 进报告与 Phoenix experiment 名。
type Variant struct {
	Name   string
	Steps  map[string]string
	Params map[string]map[string]string
}

func canonicalKind(k string) bool {
	for _, c := range CanonicalKinds {
		if c == k {
			return true
		}
	}
	return false
}

// BaselineVariant 返回与重构前行为一致的默认变体。
func BaselineVariant() Variant {
	return Variant{
		Name: "baseline",
		Steps: map[string]string{
			"fingerprint": "exact", "rewrite": "passthrough", "recall": "hybrid",
			"fuse": "rrf", "relevance": "default", "rerank": "openai", "gate": "floor",
		},
	}
}

// ParseVariantPatch 解析 "rerank=none,fuse=weighted,gate.floor=0.15" 并叠加到 base（不改 base）。
func ParseVariantPatch(name, spec string, base Variant) (Variant, error) {
	out := cloneVariant(base)
	if name != "" {
		out.Name = name
	}
	for _, tok := range strings.Split(spec, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		key, val, ok := strings.Cut(tok, "=")
		if !ok || val == "" {
			return Variant{}, fmt.Errorf("非法变体项 %q（应为 kind=impl 或 kind.knob=value）", tok)
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if k, knob, hasKnob := strings.Cut(key, "."); hasKnob {
			if !canonicalKind(k) {
				return Variant{}, fmt.Errorf("未知环节 %q", k)
			}
			if out.Params == nil {
				out.Params = map[string]map[string]string{}
			}
			if out.Params[k] == nil {
				out.Params[k] = map[string]string{}
			}
			out.Params[k][knob] = val
			continue
		}
		if !canonicalKind(key) {
			return Variant{}, fmt.Errorf("未知环节 %q", key)
		}
		out.Steps[key] = val
	}
	return out, nil
}

// ParseVariants 解析 ";" 分隔的变体串；含 "=" 的条目 = 在 baseline 上打补丁并自动命名。
// 不含 "=" 的条目按 named 查表；缺失时报错。
func ParseVariants(spec string, named map[string]Variant) ([]Variant, error) {
	base := BaselineVariant()
	var out []Variant
	for _, entry := range strings.Split(spec, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "=") {
			v, err := ParseVariantPatch(entry, entry, base)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
			continue
		}
		if v, ok := named[entry]; ok {
			out = append(out, cloneVariant(v))
			continue
		}
		if entry == "baseline" {
			out = append(out, cloneVariant(base))
			continue
		}
		return nil, fmt.Errorf("未知具名变体 %q", entry)
	}
	if len(out) == 0 {
		out = []Variant{cloneVariant(base)}
	}
	return out, nil
}

// ParseVariantsJSON 解析 -variants-file JSON 数组；未覆盖项继承 baseline。
func ParseVariantsJSON(data []byte) ([]Variant, error) {
	var raw []Variant
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make([]Variant, 0, len(raw))
	for _, v := range raw {
		if v.Name == "" {
			return nil, fmt.Errorf("变体缺少 name")
		}
		merged := cloneVariant(BaselineVariant())
		merged.Name = v.Name
		for k, impl := range v.Steps {
			if !canonicalKind(k) {
				return nil, fmt.Errorf("变体 %q 未知环节 %q", v.Name, k)
			}
			merged.Steps[k] = impl
		}
		if len(v.Params) > 0 {
			merged.Params = v.Params
		}
		out = append(out, merged)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func cloneVariant(v Variant) Variant {
	steps := make(map[string]string, len(v.Steps))
	for k, s := range v.Steps {
		steps[k] = s
	}
	params := map[string]map[string]string{}
	for k, m := range v.Params {
		cp := map[string]string{}
		for kk, vv := range m {
			cp[kk] = vv
		}
		params[k] = cp
	}
	return Variant{Name: v.Name, Steps: steps, Params: params}
}
