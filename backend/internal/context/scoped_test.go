package context

import (
	"errors"
	"path/filepath"
	"testing"

	"aiops/internal/model"
	"aiops/internal/scope"
	"aiops/internal/store"
)

func TestScopedTaskWriteAndDeny(t *testing.T) {
	s, _ := store.New(filepath.Join(t.TempDir(), "t.db"))
	defer s.Close()
	cs := NewStore(s)

	// 合法任务作用域写入
	task := NewScoped(cs, scope.Task("run-1", "compute-01"))
	if err := task.AddScratchpad("rca", "root cause note", ""); err != nil {
		t.Fatalf("AddScratchpad: %v", err)
	}
	ev := model.ToolEvent{ID: "e1", RunID: "run-1", ToolName: "query_metric",
		ArgsJSON: "{}", ResultJSON: "[]", Status: model.ToolStatusOK}
	if err := task.AddToolEvent(ev); err != nil {
		t.Fatalf("AddToolEvent: %v", err)
	}

	// 越权：任务作用域写其它 run
	// 说明：AddScratchpad 写入的 run_id 恒取自绑定作用域的 TaskID，作用域绑定本身即隔离，
	// 本层不存在可断言的跨 run scratchpad 写；跨 run 拒绝仅在 AddToolEvent 携带的
	// ev.RunID 与绑定任务不匹配时成立，故此处只测工具事件。
	other := NewScoped(cs, scope.Task("run-other", "compute-01"))
	if err := other.AddToolEvent(model.ToolEvent{RunID: "run-1", ToolName: "x",
		ArgsJSON: "{}", ResultJSON: "[]", Status: model.ToolStatusOK}); !errors.Is(err, scope.ErrScopeDenied) {
		t.Fatalf("cross-run tool err = %v, want ErrScopeDenied", err)
	}

	// 全局作用域不允许任务写
	g := NewScoped(cs, scope.Global())
	if err := g.AddScratchpad("rca", "x", ""); !errors.Is(err, scope.ErrScopeDenied) {
		t.Fatalf("global write err = %v, want ErrScopeDenied", err)
	}
}
