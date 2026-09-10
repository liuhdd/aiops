package context

type Source string

const (
	SourceScratchpad Source = "scratchpad"
	SourceShortTerm  Source = "short_term"
	SourceLongTerm   Source = "long_term"
	SourceKnowledge  Source = "knowledge"
)

type Chunk struct {
	Source   Source
	Content  string
	Metadata map[string]string
	Score    float64
}

type Query struct {
	Device         string
	Metric         string
	Severity       string
	Stage          string
	RunID          string
	ConversationID string
	Keywords       []string
	Intents        []string
	Text           string // 自由文本（如用户原问句），参与检索文本组装
	Version        string // 目标版本，透传给检索链路做版本区间过滤
}
