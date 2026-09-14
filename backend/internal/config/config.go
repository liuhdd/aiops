package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	LLMAPIKey               string
	LLMBaseURL              string
	LLMModel                string
	DBPath                  string
	SimInterval             time.Duration
	Port                    string
	React                   bool    // CONTEXT_REACT，ReAct 循环开关，默认 true
	TopK                    int     // CONTEXT_TOP_K，每源上下文条数，默认 3
	LLMLightBaseURL         string  // LLM_LIGHT_BASE_URL，轻量意图模型 BaseURL，默认同 LLM_BASE_URL
	LLMLightModel           string  // LLM_LIGHT_MODEL，轻量意图模型，默认同 LLM_MODEL
	LLMEmbedBaseURL         string  // LLM_EMBED_BASE_URL，embedding 接口 BaseURL，默认同 LLM_BASE_URL
	LLMEmbedModel           string  // LLM_EMBED_MODEL，embedding 模型，默认 text-embedding-3-small
	MaxSplit                int     // INTENT_MAX_SPLIT，单条输入多意图拆分上限，默认 3
	RetrieveFingerprint     bool    // RETRIEVE_FINGERPRINT，默认 true
	RetrieveRRF             bool    // RETRIEVE_RRF，默认 true
	RetrieveRewrite         string  // RETRIEVE_REWRITE，默认 passthrough
	RetrieveRecall          string  // RETRIEVE_RECALL，默认 hybrid
	RetrieveGate            string  // RETRIEVE_GATE，默认 floor
	RetrieveVariant         string  // RETRIEVE_VARIANT，基础覆盖串
	RetrieveVariantPipeline string  // RETRIEVE_VARIANT_PIPELINE
	RetrieveVariantChat     string  // RETRIEVE_VARIANT_CHAT
	RerankBaseURL           string  // RERANK_BASE_URL，为空即不启用
	RerankAPIKey            string  // RERANK_API_KEY，为空即不启用
	RerankModel             string  // RERANK_MODEL，默认 rerank
	RerankFloor             float64 // RERANK_FLOOR，默认 0.2
	KBEnhance               bool    // KB_ENHANCE，默认 true
	KBEnhanceSummary        bool    // KB_ENHANCE_SUMMARY，默认 true
	KBEnhanceQuestions      bool    // KB_ENHANCE_QUESTIONS，默认 true
	KBEnhanceCDTA           bool    // KB_ENHANCE_CDTA，默认 true
	KBCdtaThreshold         float64 // KB_CDTA_THRESHOLD，默认 0.85
	KBFlowMaxSize           int     // KB_FLOW_MAX_SIZE，默认 8000
	KBEmbedDepth            int     // KB_EMBED_DEPTH，默认 2
	PhoenixBaseURL          string  // PHOENIX_BASE_URL，为空即关闭 Phoenix 提示词拉取
	PhoenixTag              string  // PHOENIX_PROMPT_TAG，拉取 tag，默认 production
	PhoenixTTL              int     // PHOENIX_PROMPT_TTL，缓存刷新周期秒数，默认 60
	PhoenixAPIKey           string  // PHOENIX_API_KEY，非空时带 Bearer 头
}

func Load() Config {
	return LoadFromEnv(os.Getenv)
}

// Default 返回空环境下的默认配置（不读进程环境），供库构造与测试显式注入使用。
// 入口（cmd/*）仍应使用 Load() 读取真实环境。
func Default() Config {
	return LoadFromEnv(func(string) string { return "" })
}

func LoadFromEnv(lookup func(string) string) Config {
	base := lookup("LLM_BASE_URL")
	if base == "" {
		base = "https://api.deepseek.com/v1"
	}
	model := lookup("LLM_MODEL")
	if model == "" {
		model = "deepseek-chat"
	}
	port := lookup("PORT")
	if port == "" {
		port = ":8080"
	}
	db := lookup("DB_PATH")
	if db == "" {
		db = "aiops.db"
	}
	interval := 20
	if v := lookup("SIM_INTERVAL_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			interval = n
		}
	}
	react := true
	if v := lookup("CONTEXT_REACT"); v == "0" || v == "false" {
		react = false
	}
	topK := 3
	if v := lookup("CONTEXT_TOP_K"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			topK = n
		}
	}
	lightBase := lookup("LLM_LIGHT_BASE_URL")
	if lightBase == "" {
		lightBase = base
	}
	lightModel := lookup("LLM_LIGHT_MODEL")
	if lightModel == "" {
		lightModel = model
	}
	maxSplit := 3
	if v := lookup("INTENT_MAX_SPLIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxSplit = n
		}
	}
	embedBase := lookup("LLM_EMBED_BASE_URL")
	if embedBase == "" {
		embedBase = base
	}
	embedModel := lookup("LLM_EMBED_MODEL")
	if embedModel == "" {
		embedModel = "text-embedding-3-small"
	}
	retrieveFingerprint := true
	if v := lookup("RETRIEVE_FINGERPRINT"); v == "0" || v == "false" {
		retrieveFingerprint = false
	}
	retrieveRRF := true
	if v := lookup("RETRIEVE_RRF"); v == "0" || v == "false" {
		retrieveRRF = false
	}
	rewrite := lookup("RETRIEVE_REWRITE")
	if rewrite == "" {
		rewrite = "passthrough"
	}
	recall := lookup("RETRIEVE_RECALL")
	if recall == "" {
		recall = "hybrid"
	}
	gate := lookup("RETRIEVE_GATE")
	if gate == "" {
		gate = "floor"
	}
	rerankBaseURL := lookup("RERANK_BASE_URL")
	rerankAPIKey := lookup("RERANK_API_KEY")
	rerankModel := lookup("RERANK_MODEL")
	if rerankModel == "" {
		rerankModel = "rerank"
	}
	rerankFloor := 0.2
	if v := lookup("RERANK_FLOOR"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			rerankFloor = f
		}
	}
	kbEnhance := true
	if v := lookup("KB_ENHANCE"); v == "0" || v == "false" {
		kbEnhance = false
	}
	kbSummary := true
	if v := lookup("KB_ENHANCE_SUMMARY"); v == "0" || v == "false" {
		kbSummary = false
	}
	kbQuestions := true
	if v := lookup("KB_ENHANCE_QUESTIONS"); v == "0" || v == "false" {
		kbQuestions = false
	}
	kbCDTA := true
	if v := lookup("KB_ENHANCE_CDTA"); v == "0" || v == "false" {
		kbCDTA = false
	}
	kbCdtaThreshold := 0.85
	if v := lookup("KB_CDTA_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 && f < 1 {
			kbCdtaThreshold = f
		}
	}
	kbFlowMaxSize := 8000
	if v := lookup("KB_FLOW_MAX_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			kbFlowMaxSize = n
		}
	}
	kbEmbedDepth := 2
	if v := lookup("KB_EMBED_DEPTH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			kbEmbedDepth = n
		}
	}
	phoenixBase := lookup("PHOENIX_BASE_URL")
	phoenixTag := lookup("PHOENIX_PROMPT_TAG")
	if phoenixTag == "" {
		phoenixTag = "production"
	}
	phoenixTTL := 60
	if v := lookup("PHOENIX_PROMPT_TTL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			phoenixTTL = n
		}
	}
	phoenixAPIKey := lookup("PHOENIX_API_KEY")
	return Config{
		LLMAPIKey:               lookup("LLM_API_KEY"),
		LLMBaseURL:              base,
		LLMModel:                model,
		DBPath:                  db,
		SimInterval:             time.Duration(interval) * time.Second,
		Port:                    port,
		React:                   react,
		TopK:                    topK,
		LLMLightBaseURL:         lightBase,
		LLMLightModel:           lightModel,
		LLMEmbedBaseURL:         embedBase,
		LLMEmbedModel:           embedModel,
		MaxSplit:                maxSplit,
		RetrieveFingerprint:     retrieveFingerprint,
		RetrieveRRF:             retrieveRRF,
		RetrieveRewrite:         rewrite,
		RetrieveRecall:          recall,
		RetrieveGate:            gate,
		RetrieveVariant:         lookup("RETRIEVE_VARIANT"),
		RetrieveVariantPipeline: lookup("RETRIEVE_VARIANT_PIPELINE"),
		RetrieveVariantChat:     lookup("RETRIEVE_VARIANT_CHAT"),
		RerankBaseURL:           rerankBaseURL,
		RerankAPIKey:            rerankAPIKey,
		RerankModel:             rerankModel,
		RerankFloor:             rerankFloor,
		KBEnhance:               kbEnhance,
		KBEnhanceSummary:        kbSummary,
		KBEnhanceQuestions:      kbQuestions,
		KBEnhanceCDTA:           kbCDTA,
		KBCdtaThreshold:         kbCdtaThreshold,
		KBFlowMaxSize:           kbFlowMaxSize,
		KBEmbedDepth:            kbEmbedDepth,
		PhoenixBaseURL:          phoenixBase,
		PhoenixTag:              phoenixTag,
		PhoenixTTL:              phoenixTTL,
		PhoenixAPIKey:           phoenixAPIKey,
	}
}

// RetrieveSteps 返回某 profile 生效的 kind→impl 映射（单项 env → RETRIEVE_VARIANT → 路径覆盖）。
// profile: "pipeline" | "chat"；未知 profile 等价 pipeline。
func (c Config) RetrieveSteps(profile string) map[string]string {
	steps := map[string]string{
		"fingerprint": "exact", "rewrite": c.RetrieveRewrite, "recall": c.RetrieveRecall,
		"fuse": "rrf", "relevance": "default", "rerank": "openai", "gate": c.RetrieveGate,
	}
	if !c.RetrieveFingerprint {
		steps["fingerprint"] = "off"
	}
	if !c.RetrieveRRF {
		steps["fuse"] = "weighted"
	}
	if c.RerankBaseURL == "" {
		steps["rerank"] = "none"
	}
	applySpec := func(spec string) {
		for _, tok := range strings.Split(spec, ",") {
			tok = strings.TrimSpace(tok)
			if tok == "" || strings.Contains(tok, ".") {
				continue
			}
			k, v, ok := strings.Cut(tok, "=")
			if ok {
				steps[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
	}
	applySpec(c.RetrieveVariant)
	if profile == "chat" {
		steps["rewrite"] = "llm"
		applySpec(c.RetrieveVariantChat)
	} else {
		applySpec(c.RetrieveVariantPipeline)
	}
	return steps
}
