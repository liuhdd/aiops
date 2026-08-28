package store

import (
	"encoding/json"
	"strings"

	"aiops/internal/model"
)

// KbFilter 知识库列表筛选（空值忽略）。
type KbFilter struct {
	Category   string
	DeviceType string
	Vendor     string
	Model      string
	Intent     string
	Q          string
}

// KBStore 知识库读写接口（*Store 与事务内 repo 均实现）。
type KBStore interface {
	InsertKbDocument(d model.KbDocument) error
	InsertKbChunk(c model.KbChunk) error
	GetKbDocument(id string) (model.KbDocument, error)
	GetKbChunk(id string) (model.KbChunk, error)
	UpdateKbChunk(c model.KbChunk) error
	ListKbChunksByDoc(docID string) ([]model.KbChunk, error)
	DeleteKbChunks(docID string) error
	DeleteKbDocument(id string) error
	UpsertKbEmbedding(e model.KbEmbedding) error
	DeleteKbEmbeddingsByDoc(docID string) error
	InsertKbChunkQuestion(q model.KbChunkQuestion) error
	ListKbChunkQuestionsByChunk(chunkID string) ([]model.KbChunkQuestion, error)
	ListKbChunkQuestions() ([]model.KbChunkQuestion, error)
	DeleteKbChunkQuestionsByDoc(docID string) error
}

type kbRepo struct{ db dbExec }

const kbDocCols = `id, title, category, device_types, vendor, model, metrics, keywords, intents, source_file, content, fingerprints, created_at, updated_at`
const kbChunkCols = `id, doc_id, seq, heading, content, summary, source_doc_ids, created_at`

func parseArr(s string) []string {
	var out []string
	if s != "" {
		_ = json.Unmarshal([]byte(s), &out)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func scanKbDoc(scan func(dest ...any) error) (model.KbDocument, error) {
	var d model.KbDocument
	var dt, vd, md, mtr, kw, in, fpStr string
	if err := scan(&d.ID, &d.Title, &d.Category, &dt, &vd, &md, &mtr, &kw, &in, &d.SourceFile, &d.Content, &fpStr, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return d, err
	}
	d.DeviceTypes = parseArr(dt)
	d.Vendor = parseArr(vd)
	d.Model = parseArr(md)
	d.Metrics = parseArr(mtr)
	d.Keywords = parseArr(kw)
	d.Intents = parseArr(in)
	d.Fingerprints = parseArr(fpStr)
	return d, nil
}

func (r *kbRepo) InsertKbDocument(d model.KbDocument) error {
	_, err := r.db.Exec(`INSERT INTO kb_documents (id, title, category, device_types, vendor, model, metrics, keywords, intents, source_file, content, fingerprints, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			title=excluded.title, category=excluded.category, device_types=excluded.device_types,
			vendor=excluded.vendor, model=excluded.model, metrics=excluded.metrics,
			keywords=excluded.keywords, intents=excluded.intents, source_file=excluded.source_file,
			content=excluded.content, fingerprints=excluded.fingerprints, updated_at=excluded.updated_at`,
		d.ID, d.Title, string(d.Category), model.MustJSON(d.DeviceTypes), model.MustJSON(d.Vendor),
		model.MustJSON(d.Model), model.MustJSON(d.Metrics), model.MustJSON(d.Keywords),
		model.MustJSON(d.Intents), d.SourceFile, d.Content, model.MustJSON(d.Fingerprints), d.CreatedAt, d.UpdatedAt)
	return err
}

func scanKbChunk(scan func(dest ...any) error) (model.KbChunk, error) {
	var c model.KbChunk
	var summary, sourceDocIDs string
	if err := scan(&c.ID, &c.DocID, &c.Seq, &c.Heading, &c.Content, &summary, &sourceDocIDs, &c.CreatedAt); err != nil {
		return c, err
	}
	c.Summary = summary
	c.SourceDocIDs = parseArr(sourceDocIDs)
	return c, nil
}

func (r *kbRepo) InsertKbChunk(c model.KbChunk) error {
	_, err := r.db.Exec(`INSERT INTO kb_chunks (id, doc_id, seq, heading, content, summary, source_doc_ids, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		c.ID, c.DocID, c.Seq, c.Heading, c.Content, c.Summary, model.MustJSON(c.SourceDocIDs), c.CreatedAt)
	return err
}

func (r *kbRepo) GetKbChunk(id string) (model.KbChunk, error) {
	return scanKbChunk(r.db.QueryRow(`SELECT `+kbChunkCols+` FROM kb_chunks WHERE id = ?`, id).Scan)
}

func (r *kbRepo) UpdateKbChunk(c model.KbChunk) error {
	_, err := r.db.Exec(`UPDATE kb_chunks SET content=?, summary=?, source_doc_ids=? WHERE id=?`,
		c.Content, c.Summary, model.MustJSON(c.SourceDocIDs), c.ID)
	return err
}

func (r *kbRepo) GetKbDocument(id string) (model.KbDocument, error) {
	return scanKbDoc(r.db.QueryRow(`SELECT `+kbDocCols+` FROM kb_documents WHERE id = ?`, id).Scan)
}

func (r *kbRepo) ListKbChunksByDoc(docID string) ([]model.KbChunk, error) {
	rows, err := r.db.Query(`SELECT `+kbChunkCols+` FROM kb_chunks WHERE doc_id = ? ORDER BY seq ASC`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.KbChunk, 0)
	for rows.Next() {
		c, err := scanKbChunk(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *kbRepo) DeleteKbChunks(docID string) error {
	_, err := r.db.Exec(`DELETE FROM kb_chunks WHERE doc_id = ?`, docID)
	return err
}

func (r *kbRepo) DeleteKbDocument(id string) error {
	_, err := r.db.Exec(`DELETE FROM kb_documents WHERE id = ?`, id)
	return err
}

// parseVec 反序列化 JSON 向量数组。
func parseVec(s string) []float64 {
	var v []float64
	if s != "" {
		_ = json.Unmarshal([]byte(s), &v)
	}
	return v
}

func (r *kbRepo) UpsertKbEmbedding(e model.KbEmbedding) error {
	_, err := r.db.Exec(`INSERT INTO kb_embeddings (chunk_id, doc_id, embedding, model, created_at)
		VALUES (?,?,?,?,?)
		ON CONFLICT(chunk_id) DO UPDATE SET embedding=excluded.embedding, model=excluded.model, created_at=excluded.created_at`,
		e.ChunkID, e.DocID, model.MustJSON(e.Embedding), e.Model, e.CreatedAt)
	return err
}

func scanKbEmbedding(scan func(dest ...any) error) (model.KbEmbedding, error) {
	var e model.KbEmbedding
	var emb string
	if err := scan(&e.ChunkID, &e.DocID, &emb, &e.Model, &e.CreatedAt); err != nil {
		return e, err
	}
	e.Embedding = parseVec(emb)
	return e, nil
}

func (r *kbRepo) GetKbEmbeddingsByDoc(docID string) ([]model.KbEmbedding, error) {
	rows, err := r.db.Query(`SELECT chunk_id, doc_id, embedding, model, created_at FROM kb_embeddings WHERE doc_id = ?`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.KbEmbedding, 0)
	for rows.Next() {
		e, err := scanKbEmbedding(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *kbRepo) ListKbEmbeddings() ([]model.KbEmbedding, error) {
	rows, err := r.db.Query(`SELECT chunk_id, doc_id, embedding, model, created_at FROM kb_embeddings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.KbEmbedding, 0)
	for rows.Next() {
		e, err := scanKbEmbedding(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *kbRepo) DeleteKbEmbeddingsByDoc(docID string) error {
	_, err := r.db.Exec(`DELETE FROM kb_embeddings WHERE doc_id = ?`, docID)
	return err
}

func (r *kbRepo) InsertKbChunkQuestion(q model.KbChunkQuestion) error {
	_, err := r.db.Exec(`INSERT INTO kb_chunk_questions (id, chunk_id, doc_id, question, created_at) VALUES (?,?,?,?,?)`, q.ID, q.ChunkID, q.DocID, q.Question, q.CreatedAt)
	return err
}

func (r *kbRepo) ListKbChunkQuestionsByChunk(chunkID string) ([]model.KbChunkQuestion, error) {
	rows, err := r.db.Query(`SELECT id, chunk_id, doc_id, question, created_at FROM kb_chunk_questions WHERE chunk_id = ? ORDER BY created_at ASC`, chunkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.KbChunkQuestion, 0)
	for rows.Next() {
		var q model.KbChunkQuestion
		if err := rows.Scan(&q.ID, &q.ChunkID, &q.DocID, &q.Question, &q.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (r *kbRepo) ListKbChunkQuestions() ([]model.KbChunkQuestion, error) {
	rows, err := r.db.Query(`SELECT id, chunk_id, doc_id, question, created_at FROM kb_chunk_questions ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.KbChunkQuestion, 0)
	for rows.Next() {
		var q model.KbChunkQuestion
		if err := rows.Scan(&q.ID, &q.ChunkID, &q.DocID, &q.Question, &q.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (r *kbRepo) DeleteKbChunkQuestionsByDoc(docID string) error {
	_, err := r.db.Exec(`DELETE FROM kb_chunk_questions WHERE doc_id = ?`, docID)
	return err
}

// likeEscape 转义 LIKE 通配符，使筛选词按字面匹配。
func likeEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// ---- *Store 委托与事务 ----

func (s *Store) ListKbDocuments(f KbFilter) ([]model.KbDocSummary, error) {
	var conds []string
	var args []any
	if f.Category != "" {
		conds = append(conds, "d.category = ?")
		args = append(args, f.Category)
	}
	if f.DeviceType != "" {
		conds = append(conds, `d.device_types LIKE '%'||?||'%' ESCAPE '\'`)
		args = append(args, `"`+likeEscape(f.DeviceType)+`"`)
	}
	if f.Vendor != "" {
		conds = append(conds, `d.vendor LIKE '%'||?||'%' ESCAPE '\'`)
		args = append(args, `"`+likeEscape(f.Vendor)+`"`)
	}
	if f.Model != "" {
		conds = append(conds, `d.model LIKE '%'||?||'%' ESCAPE '\'`)
		args = append(args, `"`+likeEscape(f.Model)+`"`)
	}
	if f.Intent != "" {
		conds = append(conds, `d.intents LIKE '%'||?||'%' ESCAPE '\'`)
		args = append(args, `"`+likeEscape(f.Intent)+`"`)
	}
	if f.Q != "" {
		conds = append(conds, `(d.title LIKE '%'||?||'%' ESCAPE '\' OR d.content LIKE '%'||?||'%' ESCAPE '\')`)
		args = append(args, likeEscape(f.Q), likeEscape(f.Q))
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	query := `SELECT d.id, d.title, d.category, d.device_types, d.vendor, d.model, d.metrics, d.keywords, d.intents, d.source_file, d.content, d.fingerprints, d.created_at, d.updated_at,
		(SELECT count(*) FROM kb_chunks c WHERE c.doc_id = d.id) AS chunk_count
		FROM kb_documents d` + where + ` ORDER BY d.updated_at DESC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.KbDocSummary, 0)
	for rows.Next() {
		// 注意：rows.Scan 每行只能调用一次，chunk_count 须与文档列一并扫描。
		var s model.KbDocSummary
		var dt, vd, md, mtr, kw, in, fpStr string
		if err := rows.Scan(&s.ID, &s.Title, &s.Category, &dt, &vd, &md, &mtr, &kw, &in,
			&s.SourceFile, &s.Content, &fpStr, &s.CreatedAt, &s.UpdatedAt, &s.ChunkCount); err != nil {
			return nil, err
		}
		s.DeviceTypes = parseArr(dt)
		s.Vendor = parseArr(vd)
		s.Model = parseArr(md)
		s.Metrics = parseArr(mtr)
		s.Keywords = parseArr(kw)
		s.Intents = parseArr(in)
		s.Fingerprints = parseArr(fpStr)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (s *Store) ListKbDocumentsByFingerprints(fps []string) ([]model.KbDocSummary, error) {
	if len(fps) == 0 {
		return nil, nil
	}
	var conds []string
	var args []any
	for _, fp := range fps {
		conds = append(conds, `d.fingerprints LIKE '%'||?||'%' ESCAPE '\'`)
		args = append(args, `"`+likeEscape(fp)+`"`)
	}
	q := `SELECT d.id, d.title, d.category, d.device_types, d.vendor, d.model, d.metrics, d.keywords, d.intents, d.source_file, d.content, d.fingerprints, d.created_at, d.updated_at, (SELECT count(*) FROM kb_chunks c WHERE c.doc_id=d.id) AS chunk_count FROM kb_documents d WHERE (` + strings.Join(conds, " OR ") + `)`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.KbDocSummary, 0)
	for rows.Next() {
		var s model.KbDocSummary
		var dt, vd, md, mtr, kw, in, fpStr string
		if err := rows.Scan(&s.ID, &s.Title, &s.Category, &dt, &vd, &md, &mtr, &kw, &in, &s.SourceFile, &s.Content, &fpStr, &s.CreatedAt, &s.UpdatedAt, &s.ChunkCount); err != nil {
			return nil, err
		}
		s.DeviceTypes = parseArr(dt)
		s.Vendor = parseArr(vd)
		s.Model = parseArr(md)
		s.Metrics = parseArr(mtr)
		s.Keywords = parseArr(kw)
		s.Intents = parseArr(in)
		s.Fingerprints = parseArr(fpStr)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (s *Store) GetKbDocument(id string) (model.KbDocument, error) {
	return (&kbRepo{db: s.db}).GetKbDocument(id)
}

func (s *Store) GetKbChunk(id string) (model.KbChunk, error) {
	return (&kbRepo{db: s.db}).GetKbChunk(id)
}

func (s *Store) UpdateKbChunk(c model.KbChunk) error {
	return (&kbRepo{db: s.db}).UpdateKbChunk(c)
}

func (s *Store) ListKbChunksByDoc(docID string) ([]model.KbChunk, error) {
	return (&kbRepo{db: s.db}).ListKbChunksByDoc(docID)
}

func (s *Store) GetKbEmbeddingsByDoc(docID string) ([]model.KbEmbedding, error) {
	return (&kbRepo{db: s.db}).GetKbEmbeddingsByDoc(docID)
}

func (s *Store) ListKbEmbeddings() ([]model.KbEmbedding, error) {
	return (&kbRepo{db: s.db}).ListKbEmbeddings()
}

func (s *Store) DeleteKbEmbeddingsByDoc(docID string) error {
	return (&kbRepo{db: s.db}).DeleteKbEmbeddingsByDoc(docID)
}

func (s *Store) InsertKbChunkQuestion(q model.KbChunkQuestion) error {
	return (&kbRepo{db: s.db}).InsertKbChunkQuestion(q)
}

func (s *Store) ListKbChunkQuestionsByChunk(chunkID string) ([]model.KbChunkQuestion, error) {
	return (&kbRepo{db: s.db}).ListKbChunkQuestionsByChunk(chunkID)
}

func (s *Store) ListKbChunkQuestions() ([]model.KbChunkQuestion, error) {
	return (&kbRepo{db: s.db}).ListKbChunkQuestions()
}

func (s *Store) DeleteKbChunkQuestionsByDoc(docID string) error {
	return (&kbRepo{db: s.db}).DeleteKbChunkQuestionsByDoc(docID)
}

func (s *Store) ListKnowledgeChunks() ([]model.KbKnowledgeChunk, error) {
	rows, err := s.db.Query(`SELECT c.id, c.doc_id, d.title, d.category, d.device_types, d.vendor, d.model, d.metrics, d.keywords, d.intents, c.heading, c.content, c.source_doc_ids, d.created_at
		FROM kb_chunks c JOIN kb_documents d ON d.id = c.doc_id
		ORDER BY d.created_at DESC, c.seq ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.KbKnowledgeChunk, 0)
	for rows.Next() {
		var k model.KbKnowledgeChunk
		var dt, vd, md, mtr, kw, in, srcIDs string
		if err := rows.Scan(&k.ChunkID, &k.DocID, &k.Title, &k.Category, &dt, &vd, &md, &mtr, &kw, &in, &k.Heading, &k.Content, &srcIDs, &k.CreatedAt); err != nil {
			return nil, err
		}
		k.DeviceTypes = parseArr(dt)
		k.Vendor = parseArr(vd)
		k.Model = parseArr(md)
		k.Metrics = parseArr(mtr)
		k.Keywords = parseArr(kw)
		k.Intents = parseArr(in)
		k.SourceDocIDs = parseArr(srcIDs)
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) CountKbDocuments() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM kb_documents`).Scan(&n)
	return n, err
}

// WithKBTx 在单事务内执行知识库写操作；fn 返回错误则回滚。
func (s *Store) WithKBTx(fn func(tx KBStore) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(&kbRepo{db: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
