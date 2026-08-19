package context

import (
	"strconv"
	"strings"
	"time"

	"aiops/internal/model"
)

type Assembled struct {
	System   string
	User     string
	Selected []Chunk
}

type Assembler struct {
	cs   *Store
	sel  Selector
	topK int
}

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
	out.System = "以下是当前任务相关的上下文信息。"
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
	kcs, err := a.cs.ListKnowledgeChunks()
	if err != nil {
		return nil, err
	}
	for _, kc := range kcs {
		out = append(out, Chunk{Source: SourceKnowledge,
			Content: knowledgeContent(kc),
			Metadata: map[string]string{
				"metrics":      strings.Join(kc.Metrics, ","),
				"device_types": strings.Join(kc.DeviceTypes, ","),
				"vendor":       strings.Join(kc.Vendor, ","),
				"model":        strings.Join(kc.Model, ","),
				"intents":      strings.Join(kc.Intents, ","),
				"doc_title":    kc.Title,
				"doc_id":       kc.DocID,
				"created_at":   kc.CreatedAt.Format(time.RFC3339),
			}})
	}
	for i := range out {
		out[i].Score = a.sel.Score(out[i], q, now)
	}
	return out, nil
}

func knowledgeContent(kc model.KbKnowledgeChunk) string {
	head := "[" + categoryName(kc.Category) + "] " + kc.Title
	if kc.Heading != "" {
		head += " §" + kc.Heading
	}
	return head + ": " + kc.Content
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
