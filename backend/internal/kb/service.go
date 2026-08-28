package kb

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"aiops/internal/config"
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
	st       *store.Store
	leaves   func() []model.LeafSpec
	embed    retrieve.Embedder
	enrichLC Completer
	cfg      config.Config
}

func NewService(st *store.Store, leaves func() []model.LeafSpec) *Service {
	return NewServiceWithEmbedder(st, leaves, nil)
}

// NewServiceWithEmbedder 构造 Service；embed 可为 nil（不上向量）。
func NewServiceWithEmbedder(st *store.Store, leaves func() []model.LeafSpec, embed retrieve.Embedder) *Service {
	return NewServiceWithEnrich(st, leaves, embed, nil)
}

// NewServiceWithEnrich 构造 Service；lc 可为 nil（富化回退）。
func NewServiceWithEnrich(st *store.Store, leaves func() []model.LeafSpec, embed retrieve.Embedder, lc Completer) *Service {
	return &Service{st: st, leaves: leaves, embed: embed, enrichLC: lc, cfg: config.Load()}
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
		doc.Fingerprints = ExtractFingerprints(doc.Title, doc.Content)
		if err := s.insertEnrichedDoc(context.Background(), doc, d.Content, now); err != nil {
			return err
		}
	}
	return nil
}

// Upload 解析→打标（合并覆盖）→切块→富化→嵌入→CDTA→事务入库。
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
	doc.Fingerprints = ExtractFingerprints(doc.Title, doc.Content)
	if err := s.insertEnrichedDoc(ctx, doc, content, now); err != nil {
		return nil, err
	}
	return &doc, nil
}

func (s *Service) prepareEnriched(ctx context.Context, chunks []ChunkResult) []EnrichedChunk {
	if !s.cfg.KBEnhance || IsNilCompleter(s.enrichLC) {
		out := make([]EnrichedChunk, len(chunks))
		for i, c := range chunks {
			out[i] = EnrichedChunk{ChunkResult: c, EnhancedContent: c.Content}
		}
		return out
	}
	enriched := EnrichChunks(ctx, s.enrichLC, chunks)
	if !s.cfg.KBEnhanceSummary {
		for i := range enriched {
			enriched[i].Summary = ""
			enriched[i].EnhancedContent = enriched[i].ChunkResult.Content
		}
	}
	if !s.cfg.KBEnhanceQuestions {
		for i := range enriched {
			enriched[i].Questions = nil
		}
	}
	return enriched
}

func (s *Service) embedTexts(ctx context.Context, texts []string) [][]float64 {
	if s.embed == nil || len(texts) == 0 {
		return nil
	}
	trunc := make([]string, len(texts))
	for i, t := range texts {
		trunc[i] = truncateRunes(t, 8000)
	}
	vecs, err := s.embed.Embed(ctx, trunc)
	if err != nil {
		return nil
	}
	return vecs
}

func (s *Service) shouldUseCDTA(vecs [][]float64) bool {
	if !s.cfg.KBEnhance || !s.cfg.KBEnhanceCDTA {
		return false
	}
	if s.embed == nil || len(vecs) == 0 {
		return false
	}
	return true
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

type pendingChunk struct {
	id       string
	seq      int
	enriched EnrichedChunk
	vec      []float64
	qTexts   []string
	qVecs    [][]float64
}

type mergeEntry struct {
	oldChunk     model.KbChunk
	newContents  []string
	newQuestions []string
	newQVecs     [][]float64
}

func (s *Service) insertEnrichedDoc(ctx context.Context, doc model.KbDocument, content string, now time.Time) error {
	chunks := Chunk(content)
	enriched := s.prepareEnriched(ctx, chunks)
	if len(enriched) == 0 {
		// 空内容时仍需插入文档（无块）
		return s.st.WithKBTx(func(tx store.KBStore) error {
			return tx.InsertKbDocument(doc)
		})
	}
	ids := make([]string, len(enriched))
	for i := range ids {
		ids[i] = model.NewID("kbchunk")
	}
	// 文本与向量
	texts := make([]string, len(enriched))
	for i, ec := range enriched {
		texts[i] = ec.EnhancedContent
	}
	vecs := s.embedTexts(ctx, texts)
	// 问题扁平
	var qTextsFlat []string
	qOffsets := make([]int, len(enriched))
	qCounts := make([]int, len(enriched))
	offset := 0
	for i, ec := range enriched {
		qOffsets[i] = offset
		qCounts[i] = len(ec.Questions)
		for _, q := range ec.Questions {
			qTextsFlat = append(qTextsFlat, q)
		}
		offset += len(ec.Questions)
	}
	var qVecsFlat [][]float64
	if len(qTextsFlat) > 0 {
		qVecsFlat = s.embedTexts(ctx, qTextsFlat)
	}
	// 按块切分问题向量
	perChunkQVecs := make([][][]float64, len(enriched))
	for i := range enriched {
		cnt := qCounts[i]
		if cnt == 0 || qVecsFlat == nil {
			continue
		}
		start := qOffsets[i]
		end := start + cnt
		if end > len(qVecsFlat) {
			end = len(qVecsFlat)
		}
		slice := make([][]float64, 0, cnt)
		for j := start; j < end; j++ {
			if j < len(qVecsFlat) {
				slice = append(slice, qVecsFlat[j])
			} else {
				slice = append(slice, nil)
			}
		}
		perChunkQVecs[i] = slice
	}
	threshold := s.cfg.KBCdtaThreshold
	if threshold == 0 {
		threshold = 0.85
	}
	useCDTA := s.shouldUseCDTA(vecs)
	// 旧库向量
	oldEmbs := map[string][]float64{}
	if useCDTA {
		if embs, err := s.st.ListKbEmbeddings(); err == nil {
			for _, e := range embs {
				if strings.HasPrefix(e.ChunkID, "q_") {
					continue
				}
				oldEmbs[e.ChunkID] = e.Embedding
			}
		} else {
			useCDTA = false
		}
	}
	var pendings []pendingChunk
	merges := map[string]*mergeEntry{}
	if !useCDTA {
		// 全部作为新块
		for i, ec := range enriched {
			var v []float64
			if i < len(vecs) {
				v = vecs[i]
			}
			qTexts := ec.Questions
			qVecs := perChunkQVecs[i]
			pendings = append(pendings, pendingChunk{id: ids[i], seq: i, enriched: ec, vec: v, qTexts: qTexts, qVecs: qVecs})
		}
	} else {
		// CDTA 融合
		for i, ec := range enriched {
			var v []float64
			if i < len(vecs) {
				v = vecs[i]
			}
			qTexts := ec.Questions
			qVecs := perChunkQVecs[i]
			// 批内去重：与已 pending 的对比
			mergedIntoPending := -1
			if v != nil && len(v) > 0 {
				for pIdx, p := range pendings {
					if p.vec == nil || len(p.vec) == 0 {
						continue
					}
					if retrieve.Cosine(v, p.vec) >= threshold {
						mergedIntoPending = pIdx
						break
					}
				}
			}
			if mergedIntoPending >= 0 {
				// 合并到 pending
				p := &pendings[mergedIntoPending]
				p.enriched.EnhancedContent += "\n\n---\n\n" + ec.EnhancedContent
				// 合并问题去重
				for qi, q := range qTexts {
					if containsStr(p.qTexts, q) {
						continue
					}
					p.qTexts = append(p.qTexts, q)
					if qi < len(qVecs) {
						p.qVecs = append(p.qVecs, qVecs[qi])
					} else {
						p.qVecs = append(p.qVecs, nil)
					}
				}
				continue
			}
			// 跨库去重
			bestID := ""
			bestScore := 0.0
			if v != nil && len(v) > 0 {
				for oldID, oldVec := range oldEmbs {
					score := retrieve.Cosine(v, oldVec)
					if score > bestScore {
						bestScore = score
						bestID = oldID
					}
				}
			}
			if bestScore >= threshold && bestID != "" {
				// 命中旧块
				if _, exists := merges[bestID]; !exists {
					oc, err := s.st.GetKbChunk(bestID)
					if err != nil {
						// 拉取失败则当未命中
						pendings = append(pendings, pendingChunk{id: ids[i], seq: i, enriched: ec, vec: v, qTexts: qTexts, qVecs: qVecs})
						continue
					}
					merges[bestID] = &mergeEntry{oldChunk: oc, newContents: []string{ec.EnhancedContent}, newQuestions: append([]string(nil), qTexts...), newQVecs: append([][]float64(nil), qVecs...)}
				} else {
					me := merges[bestID]
					me.newContents = append(me.newContents, ec.EnhancedContent)
					for qi, q := range qTexts {
						if containsStr(me.newQuestions, q) {
							continue
						}
						me.newQuestions = append(me.newQuestions, q)
						if qi < len(qVecs) {
							me.newQVecs = append(me.newQVecs, qVecs[qi])
						} else {
							me.newQVecs = append(me.newQVecs, nil)
						}
					}
				}
				continue
			}
			// 未命中，作为新块
			pendings = append(pendings, pendingChunk{id: ids[i], seq: i, enriched: ec, vec: v, qTexts: qTexts, qVecs: qVecs})
		}
	}
	// 事务写入
	return s.st.WithKBTx(func(tx store.KBStore) error {
		if err := tx.InsertKbDocument(doc); err != nil {
			return err
		}
		// 新块
		for idx, p := range pendings {
			chunk := model.KbChunk{
				ID: p.id, DocID: doc.ID, Seq: idx, Heading: p.enriched.Heading,
				Content: p.enriched.EnhancedContent, Summary: p.enriched.Summary,
				SourceDocIDs: []string{doc.ID}, CreatedAt: now,
			}
			if err := tx.InsertKbChunk(chunk); err != nil {
				return err
			}
			// 问题
			for qi, q := range p.qTexts {
				qID := model.NewID("q")
				qq := model.KbChunkQuestion{ID: qID, ChunkID: p.id, DocID: doc.ID, Question: q, CreatedAt: now}
				if err := tx.InsertKbChunkQuestion(qq); err != nil {
					return err
				}
				if qi < len(p.qVecs) && p.qVecs[qi] != nil && len(p.qVecs[qi]) > 0 {
					emb := model.KbEmbedding{ChunkID: "q_" + qID, DocID: doc.ID, Embedding: p.qVecs[qi], Model: s.embed.Model(), CreatedAt: now}
					if err := tx.UpsertKbEmbedding(emb); err != nil {
						return err
					}
				}
			}
			if p.vec != nil && len(p.vec) > 0 {
				emb := model.KbEmbedding{ChunkID: p.id, DocID: doc.ID, Embedding: p.vec, Model: s.embed.Model(), CreatedAt: now}
				if err := tx.UpsertKbEmbedding(emb); err != nil {
					return err
				}
			}
		}
		// 合并到旧块
		for oldID, me := range merges {
			old := me.oldChunk
			// 重新读取以保证事务内最新（若 tx 支持 GetKbChunk）
			if getter, ok := tx.(interface {
				GetKbChunk(string) (model.KbChunk, error)
			}); ok {
				if latest, err := getter.GetKbChunk(oldID); err == nil {
					old = latest
				}
			}
			newContent := old.Content
			for _, c := range me.newContents {
				newContent += "\n\n---\n\n" + c
			}
			// source_doc_ids 去重
			srcIDs := append([]string(nil), old.SourceDocIDs...)
			if len(srcIDs) == 0 {
				// 兼容历史块为空时填充旧 DocID
				if old.DocID != "" {
					srcIDs = []string{old.DocID}
				}
			}
			if !containsStr(srcIDs, doc.ID) {
				srcIDs = append(srcIDs, doc.ID)
			}
			// 去重防重复
			uniq := map[string]bool{}
			var dedup []string
			for _, id := range srcIDs {
				if !uniq[id] {
					uniq[id] = true
					dedup = append(dedup, id)
				}
			}
			updated := model.KbChunk{
				ID: old.ID, DocID: old.DocID, Seq: old.Seq, Heading: old.Heading,
				Content: newContent, Summary: old.Summary, SourceDocIDs: dedup, CreatedAt: old.CreatedAt,
			}
			if err := tx.UpdateKbChunk(updated); err != nil {
				return err
			}
			// 已有问题去重
			existingQs, _ := tx.ListKbChunkQuestionsByChunk(oldID)
			existingSet := map[string]bool{}
			for _, eq := range existingQs {
				existingSet[eq.Question] = true
			}
			for qi, q := range me.newQuestions {
				if existingSet[q] {
					continue
				}
				qID := model.NewID("q")
				qq := model.KbChunkQuestion{ID: qID, ChunkID: oldID, DocID: old.DocID, Question: q, CreatedAt: now}
				if err := tx.InsertKbChunkQuestion(qq); err != nil {
					return err
				}
				if qi < len(me.newQVecs) && me.newQVecs[qi] != nil && len(me.newQVecs[qi]) > 0 {
					emb := model.KbEmbedding{ChunkID: "q_" + qID, DocID: old.DocID, Embedding: me.newQVecs[qi], Model: s.embed.Model(), CreatedAt: now}
					if err := tx.UpsertKbEmbedding(emb); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
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
		if err := tx.DeleteKbChunkQuestionsByDoc(id); err != nil {
			return err
		}
		if err := tx.DeleteKbEmbeddingsByDoc(id); err != nil {
			return err
		}
		return tx.DeleteKbDocument(id)
	})
}
