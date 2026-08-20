package kb

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"aiops/internal/model"
	"aiops/internal/retrieve"
	"aiops/internal/store"
)

var (
	ErrNotFound    = errors.New("文档不存在")
	ErrBadCategory = errors.New("非法分类")
)

// UploadReq 上传请求；标签字段非空时覆盖自动打标结果。
type UploadReq struct {
	Title       string
	Category    model.KBCategory
	SourceFile  string
	Data        []byte
	DeviceTypes []string
	Vendor      []string
	Model       []string
	Metrics     []string
	Keywords    []string
	Intents     []string
}

// UpdateReq 全量覆盖元数据（不动 content）。
type UpdateReq struct {
	Title       string           `json:"title"`
	Category    model.KBCategory `json:"category"`
	DeviceTypes []string         `json:"device_types"`
	Vendor      []string         `json:"vendor"`
	Model       []string         `json:"model"`
	Metrics     []string         `json:"metrics"`
	Keywords    []string         `json:"keywords"`
	Intents     []string         `json:"intents"`
}

// SeedDoc 种子文档（mockdata 导入视图）。
type SeedDoc struct {
	Title       string
	Category    model.KBCategory
	DeviceTypes []string
	Metrics     []string
	Keywords    []string
	Content     string
}

// DocDetail 详情视图：文档 + 全部块。
type DocDetail struct {
	model.KbDocument
	Chunks []model.KbChunk `json:"chunks"`
}

type Service struct {
	st     *store.Store
	leaves func() []model.LeafSpec
	embed  retrieve.Embedder
}

func NewService(st *store.Store, leaves func() []model.LeafSpec) *Service {
	return NewServiceWithEmbedder(st, leaves, nil)
}

// NewServiceWithEmbedder 构造 Service；embed 可为 nil（不上向量）。
func NewServiceWithEmbedder(st *store.Store, leaves func() []model.LeafSpec, embed retrieve.Embedder) *Service {
	return &Service{st: st, leaves: leaves, embed: embed}
}

// Seed 表空时导入种子文档（幂等）。
func (s *Service) Seed(docs []SeedDoc) error {
	n, err := s.st.CountKbDocuments()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	now := time.Now()
	for _, d := range docs {
		if strings.TrimSpace(d.Content) == "" {
			continue
		}
		cat := d.Category
		if cat == "" || !model.IsKBCategory(cat) {
			cat = model.KBOther
		}
		doc := model.KbDocument{
			ID: model.NewID("kbdoc"), Title: d.Title, Category: cat,
			DeviceTypes: d.DeviceTypes, Metrics: d.Metrics, Keywords: d.Keywords,
			Content: d.Content, CreatedAt: now, UpdatedAt: now,
		}
		chunks := Chunk(d.Content)
		ids := make([]string, len(chunks))
		for i := range chunks {
			ids[i] = model.NewID("kbchunk")
		}
		embs := s.embedChunks(context.Background(), doc.ID, chunks, ids, now)
		if err := s.insertDocAndChunks(doc, chunks, ids, embs, now); err != nil {
			return err
		}
	}
	return nil
}

// Upload 解析→打标（合并覆盖）→切块→事务入库。
func (s *Service) Upload(ctx context.Context, req UploadReq) (*model.KbDocument, error) {
	content, err := ParseDoc(req.SourceFile, req.Data)
	if err != nil {
		return nil, err
	}
	cat := req.Category
	if cat != "" && !model.IsKBCategory(cat) {
		return nil, ErrBadCategory
	}
	if cat == "" {
		cat = model.KBOther
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		base := filepath.Base(req.SourceFile)
		title = strings.TrimSuffix(base, filepath.Ext(base))
	}
	var leaves []model.LeafSpec
	if s.leaves != nil {
		leaves = s.leaves()
	}
	tags := Tag(content, leaves)
	merge := func(auto, override []string) []string {
		if len(override) > 0 {
			return override
		}
		return auto
	}
	now := time.Now()
	doc := model.KbDocument{
		ID: model.NewID("kbdoc"), Title: title, Category: cat,
		DeviceTypes: merge(tags.DeviceTypes, req.DeviceTypes),
		Vendor:      req.Vendor,
		Model:       req.Model,
		Metrics:     merge(tags.Metrics, req.Metrics),
		Keywords:    merge(tags.Keywords, req.Keywords),
		Intents:     merge(tags.Intents, req.Intents),
		SourceFile:  req.SourceFile,
		Content:     content,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	chunks := Chunk(content)
	ids := make([]string, len(chunks))
	for i := range chunks {
		ids[i] = model.NewID("kbchunk")
	}
	embs := s.embedChunks(ctx, doc.ID, chunks, ids, now)
	if err := s.insertDocAndChunks(doc, chunks, ids, embs, now); err != nil {
		return nil, err
	}
	return &doc, nil
}

func (s *Service) embedChunks(ctx context.Context, docID string, chunks []ChunkResult, ids []string, now time.Time) []model.KbEmbedding {
	if s.embed == nil {
		return nil
	}
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = truncateRunes(c.Content, 8000)
	}
	vecs, err := s.embed.Embed(ctx, texts)
	if err != nil {
		return nil
	}
	// 防御：向量数与块数不一致时只取一一对应的前缀，避免 ids 越界。
	if len(vecs) > len(ids) {
		vecs = vecs[:len(ids)]
	}
	out := make([]model.KbEmbedding, 0, len(chunks))
	for i, v := range vecs {
		if len(v) == 0 {
			continue
		}
		out = append(out, model.KbEmbedding{ChunkID: ids[i], DocID: docID, Embedding: v, Model: s.embed.Model(), CreatedAt: now})
	}
	return out
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func (s *Service) insertDocAndChunks(doc model.KbDocument, chunks []ChunkResult, ids []string, embs []model.KbEmbedding, now time.Time) error {
	return s.st.WithKBTx(func(tx store.KBStore) error {
		if err := tx.InsertKbDocument(doc); err != nil {
			return err
		}
		for i, c := range chunks {
			if err := tx.InsertKbChunk(model.KbChunk{ID: ids[i], DocID: doc.ID, Seq: i, Heading: c.Heading, Content: c.Content, CreatedAt: now}); err != nil {
				return err
			}
		}
		for _, e := range embs {
			if err := tx.UpsertKbEmbedding(e); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) List(ctx context.Context, f store.KbFilter) ([]model.KbDocSummary, error) {
	return s.st.ListKbDocuments(f)
}

func (s *Service) Get(ctx context.Context, id string) (*DocDetail, error) {
	doc, err := s.st.GetKbDocument(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	chunks, err := s.st.ListKbChunksByDoc(id)
	if err != nil {
		return nil, err
	}
	return &DocDetail{KbDocument: doc, Chunks: chunks}, nil
}

func (s *Service) Update(ctx context.Context, id string, req UpdateReq) (*model.KbDocument, error) {
	cur, err := s.st.GetKbDocument(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !model.IsKBCategory(req.Category) {
		return nil, ErrBadCategory
	}
	cur.Title = req.Title
	cur.Category = req.Category
	cur.DeviceTypes = req.DeviceTypes
	cur.Vendor = req.Vendor
	cur.Model = req.Model
	cur.Metrics = req.Metrics
	cur.Keywords = req.Keywords
	cur.Intents = req.Intents
	cur.UpdatedAt = time.Now()
	if err := s.st.WithKBTx(func(tx store.KBStore) error {
		return tx.InsertKbDocument(cur)
	}); err != nil {
		return nil, err
	}
	return &cur, nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	return s.st.WithKBTx(func(tx store.KBStore) error {
		if _, err := tx.GetKbDocument(id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if err := tx.DeleteKbChunks(id); err != nil {
			return err
		}
		if err := tx.DeleteKbEmbeddingsByDoc(id); err != nil {
			return err
		}
		return tx.DeleteKbDocument(id)
	})
}
