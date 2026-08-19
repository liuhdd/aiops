package model

import "time"

type KBCategory string

const (
	KBCase    KBCategory = "case"
	KBManual  KBCategory = "manual"
	KBRunbook KBCategory = "runbook"
	KBOther   KBCategory = "other"
)

func IsKBCategory(c KBCategory) bool {
	switch c {
	case KBCase, KBManual, KBRunbook, KBOther:
		return true
	}
	return false
}

type KbDocument struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Category    KBCategory `json:"category"`
	DeviceTypes []string   `json:"device_types"`
	Vendor      []string   `json:"vendor"`
	Model       []string   `json:"model"`
	Metrics     []string   `json:"metrics"`
	Keywords    []string   `json:"keywords"`
	Intents     []string   `json:"intents"`
	SourceFile  string     `json:"source_file"`
	Content     string     `json:"content"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type KbChunk struct {
	ID        string    `json:"id"`
	DocID     string    `json:"doc_id"`
	Seq       int       `json:"seq"`
	Heading   string    `json:"heading"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// KbDocSummary 列表视图：文档 + 块数。
type KbDocSummary struct {
	KbDocument
	ChunkCount int `json:"chunk_count"`
}

// KbEmbedding 知识块向量（kb_embeddings 表）。
type KbEmbedding struct {
	ChunkID   string
	DocID     string
	Embedding []float64
	Model     string
	CreatedAt time.Time
}

// KbKnowledgeChunk 检索视图：块 + 所属文档元数据（RAG 联查返回）。
type KbKnowledgeChunk struct {
	ChunkID     string
	DocID       string
	Title       string
	Category    KBCategory
	DeviceTypes []string
	Vendor      []string
	Model       []string
	Metrics     []string
	Keywords    []string
	Intents     []string
	Heading     string
	Content     string
	CreatedAt   time.Time
}
