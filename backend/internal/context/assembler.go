package context

import (
	"context"
	"strconv"
	"strings"
	"time"

	"aiops/internal/model"
	"aiops/internal/prompt"
	"aiops/internal/retrieve"
)

type Assembled struct {
	System   string
	User     string
	Selected []Chunk
}

type Assembler struct {
	cs     *Store
	sel    Selector
	topK   int
	search retrieve.Searcher
	leaves func() []model.LeafSpec
}

func (a *Assembler) SetSearcher(s retrieve.Searcher)     { a.search = s }
func (a *Assembler) SetLeaves(f func() []model.LeafSpec) { a.leaves = f }

func NewAssembler(cs *Store, topK int) *Assembler {
	if topK <= 0 {
		topK = DefaultTopK
	}
	return &Assembler{cs: cs, sel: Selector{}, topK: topK}
}

func (a *Assembler) Assemble(q Query, includeTools bool, tools []Tool) (*Assembled, error) {
	now := time.Now()
	cands, err := a.candidates(q, now)
	if err != nil {
		return nil, err
	}
	selected := a.sel.Select(cands, q, a.topK, DefaultThreshold)
	out := &Assembled{Selected: selected}
	var sections []string
	if sec := scratchpadSection(selected); sec != "" {
		sections = append(sections, sec)
	}
	if sec := longTermSection(selected); sec != "" {
		sections = append(sections, sec)
	}
	if sec := knowledgeSection(selected); sec != "" {
		sections = append(sections, sec)
	}
	if sec := shortTermSection(selected); sec != "" {
		sections = append(sections, sec)
	}
	if includeTools && len(tools) > 0 {
		sections = append(sections, toolsSection(tools))
		if sec := toolHistorySection(q.RunID, a.cs); sec != "" {
			sections = append(sections, sec)
		}
	}
	out.System = prompt.KnowledgeIntro
	out.User = strings.Join(sections, "\n")
	return out, nil
}

func (a *Assembler) candidates(q Query, now time.Time) ([]Chunk, error) {
	var out []Chunk
	if q.RunID != "" {
		sp, err := a.cs.ListScratchpad(q.RunID)
		if err != nil {
			return nil, err
		}
		for _, e := range sp {
			out = append(out, Chunk{Source: SourceScratchpad,
				Content:  "step=" + e.Step + ": " + e.Content,
				Metadata: map[string]string{"created_at": e.CreatedAt.Format(time.RFC3339)}})
		}
	}
	if q.Device != "" && q.Metric != "" {
		ltm, err := a.cs.ListLongTermMemory(q.Device, q.Metric)
		if err != nil {
			return nil, err
		}
		for _, m := range ltm {
			out = append(out, Chunk{Source: SourceLongTerm,
				Content:  "上次(" + m.CreatedAt.Format("2006-01-02") + ") 根因=" + m.RootCause + " 置信度=" + strconv.Itoa(int(m.Confidence*100)) + "% 处置=" + m.ActionsJSON,
				Metadata: map[string]string{"device": m.Device, "metric": m.Metric, "created_at": m.CreatedAt.Format(time.RFC3339)}})
		}
	}
	msgs, err := a.cs.RecentMessages(q.ConversationID, 10)
	if err != nil {
		return nil, err
	}
	for _, m := range msgs {
		out = append(out, Chunk{Source: SourceShortTerm,
			Content:  m.Role + ": " + m.Content,
			Metadata: map[string]string{"created_at": m.CreatedAt.Format(time.RFC3339)}})
	}
	if a.search != nil {
		casesTopK, manualsTopK := retrieve.Router(q.Stage, q.Intents, a.leafList())
		if casesTopK > 0 {
			res, err := a.search.SearchCases(context.Background(), retrieve.Query{
				Text: qText(q), Metric: q.Metric, Device: q.Device, Intents: q.Intents,
			}, retrieve.Options{TopK: casesTopK})
			// 单次检索失败静默降级为空知识（不阻断组装）；nil searcher 走下方全量扫描回退。
			if err == nil {
				for _, r := range res {
					c := resultChunk(r)
					c.Score = searcherChunkScore
					out = append(out, c)
				}
			}
		}
		if manualsTopK > 0 {
			res, err := a.search.SearchManuals(context.Background(), retrieve.Query{
				Text: qText(q), Metric: q.Metric, Device: q.Device, Intents: q.Intents,
			}, retrieve.Options{TopK: manualsTopK})
			// 单次检索失败静默降级为空知识（不阻断组装）；nil searcher 走下方全量扫描回退。
			if err == nil {
				for _, r := range res {
					c := resultChunk(r)
					c.Score = searcherChunkScore
					out = append(out, c)
				}
			}
		}
	} else {
		kcs, err := a.cs.ListKnowledgeChunks()
		if err != nil {
			return nil, err
		}
		for _, kc := range kcs {
			out = append(out, Chunk{Source: SourceKnowledge,
				Content: knowledgeContent(kc),
				Metadata: map[string]string{
					"metrics":        strings.Join(kc.Metrics, ","),
					"device_types":   strings.Join(kc.DeviceTypes, ","),
					"vendor":         strings.Join(kc.Vendor, ","),
					"model":          strings.Join(kc.Model, ","),
					"intents":        strings.Join(kc.Intents, ","),
					"doc_title":      kc.Title,
					"doc_id":         kc.DocID,
					"source_doc_ids": strings.Join(kc.SourceDocIDs, ","),
					"created_at":     kc.CreatedAt.Format(time.RFC3339),
				}})
		}
	}
	for i := range out {
		if out[i].Score > 0 {
			continue // 检索结果已由 Searcher 排序，置满分以通过 Selector 阈值
		}
		out[i].Score = a.sel.Score(out[i], q, now)
	}
	return out, nil
}

func knowledgeContent(kc model.KbKnowledgeChunk) string {
	head := "[" + categoryName(kc.Category) + "] " + kc.Title
	if kc.Heading != "" {
		head += " §" + kc.Heading
	}
	content := head + ": " + kc.Content
	if len(kc.SourceDocIDs) > 1 {
		content += "\n来源：" + strings.Join(kc.SourceDocIDs, ",")
	}
	return content
}

func categoryName(c model.KBCategory) string {
	switch c {
	case model.KBCase:
		return "运维案例"
	case model.KBManual:
		return "产品手册"
	case model.KBRunbook:
		return "操作手册"
	default:
		return "其他"
	}
}

func scratchpadSection(sel []Chunk) string {
	var b strings.Builder
	for _, c := range sel {
		if c.Source == SourceScratchpad {
			b.WriteString(c.Content + "\n")
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "【临时笔记】\n" + b.String()
}

func longTermSection(sel []Chunk) string {
	var b strings.Builder
	for _, c := range sel {
		if c.Source == SourceLongTerm {
			b.WriteString(c.Content + "\n")
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "【设备历史经验】\n" + b.String()
}

func knowledgeSection(sel []Chunk) string {
	var b strings.Builder
	for _, c := range sel {
		if c.Source == SourceKnowledge {
			b.WriteString(c.Content + "\n")
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "【知识库】\n" + b.String()
}

func shortTermSection(sel []Chunk) string {
	var b strings.Builder
	for _, c := range sel {
		if c.Source == SourceShortTerm {
			b.WriteString(c.Content + "\n")
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "【短期记忆】\n" + b.String()
}

func toolsSection(tools []Tool) string {
	var b strings.Builder
	for _, t := range tools {
		b.WriteString(t.Name + "(" + joinParams(t.ParamSchema) + ") - " + t.Description + "\n")
	}
	return "【可用工具】\n" + b.String()
}

func toolHistorySection(runID string, cs *Store) string {
	if runID == "" {
		return ""
	}
	evs, err := cs.ListToolEvents(runID)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, ev := range evs {
		b.WriteString(ev.ToolName + "(" + ev.ArgsJSON + ") → " + ev.Status + "\n")
	}
	if b.Len() == 0 {
		return ""
	}
	return "【工具调用历史】\n" + b.String()
}

func joinParams(schema map[string]string) string {
	keys := make([]string, 0, len(schema))
	for k := range schema {
		keys = append(keys, k)
	}
	return strings.Join(keys, ",")
}

func (a *Assembler) leafList() []model.LeafSpec {
	if a.leaves != nil {
		if l := a.leaves(); len(l) > 0 {
			return l
		}
	}
	return nil
}

// qText 组装检索文本：自由文本 + 设备 + 指标 + 关键词。
func qText(q Query) string {
	return strings.Join(append([]string{q.Text, q.Device, q.Metric}, q.Keywords...), " ")
}

// searcherChunkScore 检索结果块固定满分。检索候选已由 Searcher 按相关性排序，
// 直接置满分以绕过回退 Selector 的阈值过滤（否则部分排序靠后的低分候选会被丢弃）。
const searcherChunkScore = 1.0

// resultChunk 将检索结果块转成组装用 Chunk（复用 knowledgeContent 回显格式）。
func resultChunk(r retrieve.Result) Chunk {
	kc := model.KbKnowledgeChunk{
		DocID: r.DocID, Title: r.Title, Category: r.Category,
		Heading: r.Heading, Content: r.Content, SourceDocIDs: r.SourceDocIDs,
	}
	return Chunk{
		Source:  SourceKnowledge,
		Content: knowledgeContent(kc),
		Metadata: map[string]string{
			"doc_title":      r.Title,
			"doc_id":         r.DocID,
			"intents":        strings.Join(r.Intents, ","),
			"source_doc_ids": strings.Join(r.SourceDocIDs, ","),
		},
	}
}
