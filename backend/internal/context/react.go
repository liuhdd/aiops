package context

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"aiops/internal/llm"
)

type Completer interface {
	Complete(ctx context.Context, system, user string) (string, error)
}

type ReActToolCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type ReActStep struct {
	Thought     string          `json:"thought"`
	ToolCall    *ReActToolCall  `json:"tool_call,omitempty"`
	FinalAnswer json.RawMessage `json:"final_answer,omitempty"`
}

type ReActTurn struct {
	Thought string
	Tool    string
	Args    map[string]any
	Result  string
	Status  string
}

type ReActResult struct {
	Final json.RawMessage
	Turns []ReActTurn
}

type ReactOptions struct {
	MaxSteps          int
	ToolErrorsAllowed int
}

func RunReact(ctx context.Context, c Completer, system, user string, reg *Registry,
	sideEffect func(name string, args map[string]any) (any, error), opts ReactOptions) (*ReActResult, error) {
	maxSteps := opts.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 5
	}
	allowed := opts.ToolErrorsAllowed
	if allowed <= 0 {
		allowed = 2
	}
	res := &ReActResult{}
	toolErrors := 0
	var turns []ReActTurn
	for step := 0; step < maxSteps; step++ {
		prompt := user + "\n\n【历史推理】\n" + renderTurns(turns) + "\n请给出下一步："
		raw, err := c.Complete(ctx, system, prompt)
		if err != nil {
			return res, fmt.Errorf("react llm: %w", err)
		}
		var out ReActStep
		if err := llm.ParseJSON(raw, &out); err != nil {
			return res, fmt.Errorf("react parse: %w", err)
		}
		if len(out.FinalAnswer) > 0 {
			res.Final = out.FinalAnswer
			res.Turns = turns
			return res, nil
		}
		if out.ToolCall == nil {
			return res, errors.New("react: step without tool_call or final_answer")
		}
		tc := out.ToolCall
		turn := ReActTurn{Thought: out.Thought, Tool: tc.Name, Args: tc.Args}
		var result any
		var execErr error
		if sideEffect != nil {
			result, execErr = sideEffect(tc.Name, tc.Args)
		} else {
			result, execErr = reg.Execute(ctx, tc.Name, tc.Args)
		}
		if execErr != nil {
			turn.Status = "error"
			turn.Result = execErr.Error()
			toolErrors++
			if toolErrors >= allowed {
				res.Turns = turns
				return res, fmt.Errorf("react: %d consecutive tool errors", toolErrors)
			}
		} else {
			turn.Status = "ok"
			turn.Result = jsonSafe(result)
			toolErrors = 0
		}
		turns = append(turns, turn)
	}
	res.Turns = turns
	return res, nil
}

func renderTurns(turns []ReActTurn) string {
	out := ""
	for i, t := range turns {
		out += fmt.Sprintf("%d. thought=%q tool=%q result=%s(%s)\n", i+1, t.Thought, t.Tool, t.Result, t.Status)
	}
	return out
}

func jsonSafe(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
