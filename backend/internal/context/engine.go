package context

import (
	"context"
	"errors"
	"time"

	"aiops/internal/model"
	"aiops/internal/retrieve"
	"aiops/internal/scope"
)

type Engine struct {
	cs     *Store
	scoped *ScopedStore
	reg    *Registry
	sel    Selector
	asm    *Assembler
	runID  string
	device string
	metric string
	react  bool
	topK   int
}

func NewEngine(cs *Store, reg *Registry, runID, device, metric string, react bool, topK int) *Engine {
	sc := scope.Task(runID, device)
	return &Engine{
		cs: cs, scoped: NewScoped(cs, sc), reg: reg, sel: Selector{},
		asm: NewAssembler(cs, topK), runID: runID, device: device, metric: metric, react: react, topK: topK,
	}
}

func (e *Engine) RunID() string      { return e.runID }
func (e *Engine) ReactEnabled() bool { return e.react }

// SetSearcher 注入混合检索器（供组装器知识源使用）。
func (e *Engine) SetSearcher(s retrieve.Searcher) {
	e.asm.SetSearcher(s)
}

func (e *Engine) AddScratchpad(step, content, toolName string) error {
	return e.scoped.AddScratchpad(step, content, toolName)
}

func (e *Engine) ListScratchpad() ([]model.ScratchpadEntry, error) {
	return e.scoped.ListScratchpad()
}

func (e *Engine) AddToolEvent(ev model.ToolEvent) error {
	return e.scoped.AddToolEvent(ev)
}

func (e *Engine) ListToolEvents() ([]model.ToolEvent, error) {
	return e.scoped.ListToolEvents()
}

func (e *Engine) Assemble(q Query, includeTools bool) (*Assembled, error) {
	tools := []Tool{}
	if e.reg != nil {
		tools = e.reg.List()
	}
	return e.asm.Assemble(q, includeTools, tools)
}

func (e *Engine) SaveLongTermMemory(rca *model.RCA, rec *model.Recommendation) error {
	if rca == nil {
		return nil
	}
	actions := "[]"
	if rec != nil {
		actions = model.MustJSON(rec.Actions)
	}
	return e.cs.AddLongTermMemory(model.LongTermMemory{
		ID: model.NewID("ltm"), Device: e.device, Metric: e.metric,
		RootCause: rca.RootCause, Confidence: rca.Confidence, ActionsJSON: actions,
		RunID: e.runID, CreatedAt: time.Now(),
	})
}

func (e *Engine) RunReact(ctx context.Context, c Completer, system string, q Query, baseUser string) (*ReActResult, error) {
	user := baseUser
	if user == "" {
		if as, err := e.Assemble(q, true); err == nil {
			user = as.User
		}
	}
	side := func(name string, args map[string]any) (any, error) {
		if name == "write_scratchpad" {
			step := strArg(args, "step")
			if step == "" {
				step = "react"
			}
			if err := e.AddScratchpad(step, strArg(args, "content"), "write_scratchpad"); err != nil {
				return nil, err
			}
			return "ok", nil
		}
		if e.reg != nil {
			res, err := e.reg.Execute(ctx, name, args)
			if err == nil {
				_ = e.AddToolEvent(model.ToolEvent{
					ID: model.NewID("evt"), RunID: e.runID, ToolName: name,
					ArgsJSON: model.MustJSON(args), ResultJSON: model.MustJSON(res),
					Status: model.ToolStatusOK, CreatedAt: time.Now(),
				})
			}
			return res, err
		}
		return nil, errors.New("unknown tool " + name)
	}
	return RunReact(ctx, c, system, user, e.reg, side, ReactOptions{MaxSteps: 5, ToolErrorsAllowed: 2})
}
