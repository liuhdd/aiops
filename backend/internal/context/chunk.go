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
	Device   string
	Metric   string
	Severity string
	Stage    string
	RunID    string
	Keywords []string
	Intents  []string
}
