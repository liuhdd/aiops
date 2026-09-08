package kb

import (
	"context"
	"database/sql"
	"errors"
	"log"
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
	return NewServiceWithEnrich(st, leaves, embed, nil, config.Load())
}

// NewServiceWithEnrich 构造 Service；lc 可为 nil（富化回退），cfg 为零值时回退到 config.Load()。
func NewServiceWithEnrich(st *store.Store, leaves func() []model.LeafSpec, embed retrieve.Embedder, lc Completer, cfg config.Config) *Service {
	if cfg == (config.Config{}) {
		cfg = config.Load()
	}
	return &Service{st: st, leaves: leaves, embed: embed, enrichLC: lc, cfg: cfg}
}

// Seed 表空时导入种子文档（幂等；逐文档顺序处理使先入库文档可被后文档 embed）。
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
		// 种子正文同样剥离 frontmatter，保证版本链与 Upload 一致。
		body, meta := StripFrontmatter(d.Content)
		if strings.TrimSpace(body) == "" {
			continue
		}
		docVer := ParseDocVersion(body, meta)
		inlined := InlineEmbeds(body, s.embedLookup(), s.embedDepth())
		doc := model.KbDocument{
			ID: model.NewID("kbdoc"), Title: d.Title, Category: cat,
			DeviceTypes: d.DeviceTypes, Metrics: d.Metrics, Keywords: d.Keywords,
			Content: inlined, CreatedAt: now, UpdatedAt: now,
		}
		doc.Version = strings.TrimSpace(meta.Version)
		if doc.Version == "" {
			doc.Version = docVer.Min
		}
		doc.VersionMin, doc.VersionMax = docVer.Min, docVer.Max
		doc.Fingerprints = ExtractFingerprints(doc.Title, doc.Content)
		inputs := s.buildChunkInputs(body, Chunk(inlined), docVer)
		s.logFlowOverflow(inputs)
		if err := s.insertEnrichedDoc(context.Background(), doc, inputs, now); err != nil {
			return err
		}
	}
	return nil
}

// Upload 解析→版本→内联→打标（合并覆盖）→切块→富化→嵌入→CDTA→事务入库。
func (s *Service) Upload(ctx context.Context, req UploadReq) (*model.KbDocument, error) {
	content, meta, err := ParseDocWithMeta(req.SourceFile, req.Data)
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
	docVer := ParseDocVersion(content, meta)
	inlined := InlineEmbeds(content, s.embedLookup(), s.embedDepth())
	var leaves []model.LeafSpec
	if s.leaves != nil {
		leaves = s.leaves()
	}
	tags := Tag(inlined, leaves)
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
		Content:     inlined,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	// 文档级版本：原始标注存 Version，归一化区间存 Min/Max（无标注留空=全版本适用）。
	doc.Version = strings.TrimSpace(meta.Version)
	if doc.Version == "" {
		doc.Version = docVer.Min
	}
	doc.VersionMin, doc.VersionMax = docVer.Min, docVer.Max
	doc.Fingerprints = ExtractFingerprints(doc.Title, doc.Content)
	inputs := s.buildChunkInputs(content, Chunk(inlined), docVer)
	s.logFlowOverflow(inputs)
	if err := s.insertEnrichedDoc(ctx, doc, inputs, now); err != nil {
		return nil, err
	}
	return &doc, nil
}

// ChunkInput 新管线块输入：切块结果 + 版本区间 + 抽取引用。
type ChunkInput struct {
	Chunk      ChunkResult
	VersionMin string
	VersionMax string
	Refs       []model.ChunkRef
}

// embedDepth 内联最大层数；配置无 >0 守卫，此处钳制保底。
func (s *Service) embedDepth() int {
	if s.cfg.KBEmbedDepth <= 0 {
		return 2
	}
	return s.cfg.KBEmbedDepth
}

// logFlowOverflow 流程块超限仅告警不拆（原子性优先）；配置无 >0 守卫，此处钳制保底。
func (s *Service) logFlowOverflow(inputs []ChunkInput) {
	maxFlow := s.cfg.KBFlowMaxSize
	if maxFlow <= 0 {
		maxFlow = 8000
	}
	for _, in := range inputs {
		if in.Chunk.IsFlow && len([]rune(in.Chunk.Content)) > maxFlow {
			log.Printf("kb: 流程块超限（heading=%q，%d rune，上限 %d），仍保持不拆", in.Chunk.HeadingPath, len([]rune(in.Chunk.Content)), maxFlow)
		}
	}
}

// lookupDoc 按 SourceFile 精确匹配，其次 Title 匹配；返回目标文档 Content 与版本。
func (s *Service) lookupDoc(target string) (content, version, docID string, ok bool) {
	t := strings.TrimSpace(target)
	if t == "" {
		return "", "", "", false
	}
	docs, err := s.st.ListKbDocuments(store.KbFilter{})
	if err != nil || len(docs) == 0 {
		return "", "", "", false
	}
	id := ""
	for _, d := range docs {
		if d.SourceFile != "" && d.SourceFile == t {
			id = d.ID
			break
		}
	}
	if id == "" {
		for _, d := range docs {
			if d.Title != "" && d.Title == t {
				id = d.ID
				break
			}
		}
	}
	if id == "" {
		return "", "", "", false
	}
	got, err := s.st.GetKbDocument(id)
	if err != nil {
		return "", "", "", false
	}
	v := got.Version
	if v == "" {
		v = got.VersionMin
	}
	return got.Content, v, got.ID, true
}

// embedLookup 适配 InlineEmbeds 回调签名的 lookup。
func (s *Service) embedLookup() func(target string) (string, string, bool) {
	return func(target string) (string, string, bool) {
		c, v, _, ok := s.lookupDoc(target)
		return c, v, ok
	}
}

// buildChunkInputs 为每块填充版本区间与引用。
// 内联会吞掉原文 ![[embed]] 标记，故 embed 引用从内联前原文抽取，
// 按嵌入出处（"> 📎 嵌入自 [[target#anchor]]"）归属到含展开块的 chunk；
// 其余类型标记在内联后保留，直接从块内容抽取。
func (s *Service) buildChunkInputs(origContent string, chunks []ChunkResult, docVer VersionRange) []ChunkInput {
	var embedRefs []model.ChunkRef
	for _, r := range ExtractRefs(origContent) {
		if r.Type == "embed" {
			embedRefs = append(embedRefs, r)
		}
	}
	inputs := make([]ChunkInput, 0, len(chunks))
	used := make([]bool, len(embedRefs))
	for _, c := range chunks {
		vr := ParseChunkVersion(c.Content, docVer)
		refs := filterAttributionLinks(c.Content, ExtractRefs(c.Content))
		for i, er := range embedRefs {
			if used[i] {
				continue
			}
			if hasRefRaw(refs, er.Raw) {
				used[i] = true
				continue
			}
			if !containsAttribution(c.Content, er.Target, er.Anchor) {
				continue
			}
			refs = append(refs, er)
			used[i] = true
		}
		inputs = append(inputs, ChunkInput{Chunk: c, VersionMin: vr.Min, VersionMax: vr.Max, Refs: refs})
	}
	// 兜底：未归属的 embed 引用挂到首块，避免引用丢失。
	for i, er := range embedRefs {
		if used[i] || len(inputs) == 0 {
			continue
		}
		if hasRefRaw(inputs[0].Refs, er.Raw) {
			continue
		}
		inputs[0].Refs = append(inputs[0].Refs, er)
	}
	return inputs
}

// containsAttribution 判断块内是否含指定目标的嵌入出处标注。
func containsAttribution(chunkContent, target, anchor string) bool {
	disp := strings.TrimSpace(target)
	if a := strings.TrimSpace(anchor); a != "" {
		disp += "#" + a
	}
	if disp == "" {
		return false
	}
	return strings.Contains(chunkContent, "嵌入自 [["+disp+"]]")
}

// filterAttributionLinks 剔除嵌入出处行自带的 [[wikilink]]（关系已由 embed 引用覆盖，避免边表重复）。
func filterAttributionLinks(chunkContent string, refs []model.ChunkRef) []model.ChunkRef {
	out := refs[:0:0]
	if len(refs) == 0 {
		return nil
	}
	for _, r := range refs {
		if r.Type == "wikilink" && strings.Contains(chunkContent, "嵌入自 "+r.Raw) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func hasRefRaw(refs []model.ChunkRef, raw string) bool {
	for _, r := range refs {
		if r.Raw == raw {
			return true
		}
	}
	return false
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
	id         string
	seq        int
	enriched   EnrichedChunk
	vec        []float64
	qTexts     []string
	qVecs      [][]float64
	verMin     string
	verMax     string
	refs       []model.ChunkRef
	refTargets []string // 与 refs 等长；未命中/anchor 为 ""
}

type mergeEntry struct {
	oldChunk      model.KbChunk
	newContents   []string
	newQuestions  []string
	newQVecs      [][]float64
	newRefs       []model.ChunkRef
	newRefTargets []string
}

// appendRefs 去重追加引用（按 Raw），目标 ID 数组同步对齐。
func appendRefs(dst []model.ChunkRef, dstTargets []string, src []model.ChunkRef, srcTargets []string) ([]model.ChunkRef, []string) {
	for i, r := range src {
		if hasRefRaw(dst, r.Raw) {
			continue
		}
		dst = append(dst, r)
		t := ""
		if i < len(srcTargets) {
			t = srcTargets[i]
		}
		dstTargets = append(dstTargets, t)
	}
	return dst, dstTargets
}

func (s *Service) insertEnrichedDoc(ctx context.Context, doc model.KbDocument, inputs []ChunkInput, now time.Time) error {
	chunks := make([]ChunkResult, len(inputs))
	for i, in := range inputs {
		chunks[i] = in.Chunk
	}
	// 引用解析回填 Resolved；读库须在事务外完成（SQLite 单写者）。
	// 目标文档 ID 与 refs 等长对齐，供事务内写 kb_chunk_refs。
	refTargets := make([][]string, len(inputs))
	for i := range inputs {
		targets := make([]string, len(inputs[i].Refs))
		for j := range inputs[i].Refs {
			r := &inputs[i].Refs[j]
			if r.Target == "" {
				r.Resolved = false
				continue
			}
			_, _, id, ok := s.lookupDoc(r.Target)
			r.Resolved = ok
			if ok {
				targets[j] = id
			}
		}
		refTargets[i] = targets
	}
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
			pendings = append(pendings, pendingChunk{id: ids[i], seq: i, enriched: ec, vec: v, qTexts: qTexts, qVecs: qVecs,
				verMin: inputs[i].VersionMin, verMax: inputs[i].VersionMax, refs: inputs[i].Refs, refTargets: refTargets[i]})
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
				// 合并引用去重（版本保留首块）
				p.refs, p.refTargets = appendRefs(p.refs, p.refTargets, inputs[i].Refs, refTargets[i])
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
						pendings = append(pendings, pendingChunk{id: ids[i], seq: i, enriched: ec, vec: v, qTexts: qTexts, qVecs: qVecs,
							verMin: inputs[i].VersionMin, verMax: inputs[i].VersionMax, refs: inputs[i].Refs, refTargets: refTargets[i]})
						continue
					}
					me := &mergeEntry{oldChunk: oc, newContents: []string{ec.EnhancedContent}, newQuestions: append([]string(nil), qTexts...), newQVecs: append([][]float64(nil), qVecs...)}
					me.newRefs, me.newRefTargets = appendRefs(nil, nil, inputs[i].Refs, refTargets[i])
					merges[bestID] = me
				} else {
					me := merges[bestID]
					me.newContents = append(me.newContents, ec.EnhancedContent)
					me.newRefs, me.newRefTargets = appendRefs(me.newRefs, me.newRefTargets, inputs[i].Refs, refTargets[i])
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
			pendings = append(pendings, pendingChunk{id: ids[i], seq: i, enriched: ec, vec: v, qTexts: qTexts, qVecs: qVecs,
				verMin: inputs[i].VersionMin, verMax: inputs[i].VersionMax, refs: inputs[i].Refs, refTargets: refTargets[i]})
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
				HeadingPath: p.enriched.HeadingPath, IsFlow: p.enriched.IsFlow,
				VersionMin: p.verMin, VersionMax: p.verMax, SourceRefs: p.refs,
				Content: p.enriched.EnhancedContent, Summary: p.enriched.Summary,
				SourceDocIDs: []string{doc.ID}, CreatedAt: now,
			}
			if chunk.SourceRefs == nil {
				chunk.SourceRefs = []model.ChunkRef{}
			}
			if err := tx.InsertKbChunk(chunk); err != nil {
				return err
			}
			// 引用边（纯 anchor 无目标文档，只保留在 SourceRefs JSON，不写边表）
			if err := insertChunkRefEdges(tx, p.id, doc.ID, p.refs, p.refTargets, now); err != nil {
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
			// 读改写：从事务内最新旧记录出发，只改内容/来源/引用，
			// 其余新列（版本/标题路径/流程标记等）原样保留，避免全列覆盖清零。
			updated := old
			updated.Content = newContent
			updated.SourceDocIDs = dedup
			// 合并新引用（去重），并为新增引用补边表行。
			var addedRefs []model.ChunkRef
			var addedTargets []string
			for i, nr := range me.newRefs {
				if hasRefRaw(updated.SourceRefs, nr.Raw) {
					continue
				}
				updated.SourceRefs = append(updated.SourceRefs, nr)
				addedRefs = append(addedRefs, nr)
				t := ""
				if i < len(me.newRefTargets) {
					t = me.newRefTargets[i]
				}
				addedTargets = append(addedTargets, t)
			}
			if err := tx.UpdateKbChunk(updated); err != nil {
				return err
			}
			if err := insertChunkRefEdges(tx, oldID, old.DocID, addedRefs, addedTargets, now); err != nil {
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

// insertChunkRefEdges 在事务内按块写 kb_chunk_refs；纯 anchor 跳过边表。
func insertChunkRefEdges(tx store.KBStore, chunkID, docID string, refs []model.ChunkRef, targets []string, now time.Time) error {
	for i, r := range refs {
		if r.Target == "" {
			continue
		}
		t := ""
		if i < len(targets) {
			t = targets[i]
		}
		row := model.KbChunkRefRow{
			ID: model.NewID("kbref"), ChunkID: chunkID, DocID: docID,
			TargetPath: r.Target, TargetDocID: t, Anchor: r.Anchor,
			RefType: r.Type, Resolved: r.Resolved, CreatedAt: now,
		}
		if err := tx.InsertKbChunkRef(row); err != nil {
			return err
		}
	}
	return nil
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
		if err := tx.DeleteKbChunkRefsByDoc(id); err != nil {
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
