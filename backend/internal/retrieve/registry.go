package retrieve

import "fmt"

// Deps 构造 Link 的依赖集合。
type Deps struct {
	Env       *searchEnv
	Completer Completer
	// GateFloor 变体参数解析后的 gate 精排分下限覆盖；nil 用 Env.cfg.RerankFloor。
	GateFloor *float64
}

// LinkFactory 按名构造 Link。
type LinkFactory func(Deps) Link

type linkKey struct{ kind, name string }

var linkRegistry = map[linkKey]LinkFactory{}

// RegisterLink 注册实现；重复注册 panic（编程错误）。
func RegisterLink(kind, name string, f LinkFactory) {
	k := linkKey{kind, name}
	if _, dup := linkRegistry[k]; dup {
		panic(fmt.Sprintf("重复注册 Link %s/%s", kind, name))
	}
	linkRegistry[k] = f
}

func init() {
	RegisterLink("fingerprint", "exact", func(d Deps) Link { return &fingerprintLink{env: d.Env, impl: "exact"} })
	RegisterLink("fingerprint", "off", func(d Deps) Link { return &fingerprintLink{env: d.Env, impl: "off"} })
	RegisterLink("rewrite", "passthrough", func(d Deps) Link { return &rewriteLink{env: d.Env, completer: d.Completer, impl: "passthrough"} })
	RegisterLink("rewrite", "llm", func(d Deps) Link { return &rewriteLink{env: d.Env, completer: d.Completer, impl: "llm"} })
	RegisterLink("recall", "hybrid", func(d Deps) Link { return &recallLink{env: d.Env, impl: "hybrid"} })
	RegisterLink("fuse", "rrf", func(d Deps) Link { return &fuseLink{env: d.Env, impl: "rrf"} })
	RegisterLink("fuse", "weighted", func(d Deps) Link { return &fuseLink{env: d.Env, impl: "weighted"} })
	RegisterLink("relevance", "default", func(Deps) Link { return &relevanceLink{} })
	RegisterLink("rerank", "openai", func(d Deps) Link { return &rerankLink{env: d.Env, impl: "openai"} })
	RegisterLink("rerank", "none", func(d Deps) Link { return &rerankLink{env: d.Env, impl: "none"} })
	RegisterLink("gate", "floor", func(d Deps) Link { return &gateLink{env: d.Env, impl: "floor", floor: d.GateFloor} })
}
