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
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Category     KBCategory `json:"category"`
	DeviceTypes  []string   `json:"device_types"`
	Vendor       []string   `json:"vendor"`
	Model        []string   `json:"model"`
	Metrics      []string   `json:"metrics"`
	Keywords     []string   `json:"keywords"`
	Intents      []string   `json:"intents"`
	SourceFile   string     `json:"source_file"`
	Content      string     `json:"content"`
	Fingerprints []string   `json:"fingerprints"`
	// 技术文档版本范围（版本提取任务填充）。
	Version    string `json:"version"`
	VersionMin string `json:"version_min"`
	VersionMax string `json:"version_max"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type KbChunk struct {
	ID           string    `json:"id"`
	DocID        string    `json:"doc_id"`
	Seq          int       `json:"seq"`
	Heading      string    `json:"heading"`
	Content      string    `json:"content"`
	Summary      string    `json:"summary"`
	SourceDocIDs []string  `json:"source_doc_ids"`
	// 技术文档切块元数据：标题路径、版本范围、源码引用、是否为流程块。
	VersionMin  string     `json:"version_min"`
	VersionMax  string     `json:"version_max"`
	HeadingPath string     `json:"heading_path"`
	SourceRefs  []ChunkRef `json:"source_refs"`
	IsFlow      bool       `json:"is_flow"`
	CreatedAt   time.Time  `json:"created_at"`
}

// ChunkRef 块内源码引用（wikilink/链接/锚点等）。
type ChunkRef struct {
	Raw      string `json:"raw"`
	Type     string `json:"type"`
	Target   string `json:"target"`
	Anchor   string `json:"anchor"`
	Resolved bool   `json:"resolved"`
}

// KbChunkRefRow chunk 引用关系行（kb_chunk_refs 表）。
type KbChunkRefRow struct {
	ID          string    `json:"id"`
	ChunkID     string    `json:"chunk_id"`
	DocID       string    `json:"doc_id"`
	TargetPath  string    `json:"target_path"`
	TargetDocID string    `json:"target_doc_id"`
	Anchor      string    `json:"anchor"`
	RefType     string    `json:"ref_type"`
	Resolved    bool      `json:"resolved"`
	CreatedAt   time.Time `json:"created_at"`
}

type KbChunkQuestion struct {
	ID        string    `json:"id"`
	ChunkID   string    `json:"chunk_id"`
	DocID     string    `json:"doc_id"`
	Question  string    `json:"question"`
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
	ChunkID      string
	DocID        string
	Title        string
	Category     KBCategory
	DeviceTypes  []string
	Vendor       []string
	Model        []string
	Metrics      []string
	Keywords     []string
	Intents      []string
	Heading      string
	Content      string
	SourceDocIDs []string
	// 技术文档版本范围（检索视图透传）。
	VersionMin string
	VersionMax string
	CreatedAt    time.Time
}
