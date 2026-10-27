package tasks

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/pluginhost"
	"loop-now/internal/server/store"
)

type nodeHooks struct {
	events []map[string]any
	block  bool
}

func (h *nodeHooks) Hook(_ context.Context, kind pluginhost.Kind, event map[string]any, _ *uuid.UUID) (pluginhost.Outcome, error) {
	h.events = append(h.events, event)
	return pluginhost.Outcome{Blocked: h.block, Plugin: "gate", Reason: "需要人工批准"}, nil
}

// node.before_start 在节点第一次开始前询问插件：被阻止时任务进入阻塞，原因写明插件与说明，节点不开始。
func TestNodeBeforeStartBlocksTask(t *testing.T) {
	s, id, _ := fixture(t, false)
	ctx := context.Background()
	hooks := &nodeHooks{block: true}
	s.Hooks = hooks
	if _, e := s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		_, e := s.Store.EnqueueTaskTurn(ctx, m, "do work", "task_node", 1, nil)
		return nil, e
	}); e != nil {
		t.Fatal(e)
	}
	before := current(t, s, id)
	node := before.Current()
	if node.Status != "queued" {
		t.Fatalf("节点应尚未开始：%s", node.Status)
	}
	if e := s.drive(ctx, id); e != nil {
		t.Fatal(e)
	}
	r := current(t, s, id)
	if r.View.Task.Status != "blocked" || r.View.Task.Reason == nil || !strings.Contains(*r.View.Task.Reason, "gate") || !strings.Contains(*r.View.Task.Reason, "需要人工批准") {
		t.Fatalf("任务应阻塞：%+v", r.View.Task)
	}
	if cur := r.Current(); cur == nil || cur.StartedAt != nil {
		t.Fatal("节点不应开始")
	}
	if len(hooks.events) != 1 || hooks.events[0]["type"] != "node.before_start" || hooks.events[0]["nodeRunId"] != node.ID || hooks.events[0]["stepId"] != "work" {
		t.Fatalf("事件：%v", hooks.events)
	}
}
