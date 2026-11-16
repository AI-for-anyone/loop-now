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
	return pluginhost.Outcome{Blocked: h.block, Plugin: "gate", Reason: "needs human approval"}, nil
}

// node.before_start asks plugins before a node first starts: when blocked, the task becomes blocked with a reason
// naming the plugin and its explanation, and the node does not start.
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
		t.Fatalf("the node should not have started yet: %s", node.Status)
	}
	if e := s.drive(ctx, id); e != nil {
		t.Fatal(e)
	}
	r := current(t, s, id)
	if r.View.Task.Status != "blocked" || r.View.Task.Reason == nil || !strings.Contains(*r.View.Task.Reason, "gate") || !strings.Contains(*r.View.Task.Reason, "needs human approval") {
		t.Fatalf("the task should be blocked: %+v", r.View.Task)
	}
	if cur := r.Current(); cur == nil || cur.StartedAt != nil {
		t.Fatal("the node should not start")
	}
	if len(hooks.events) != 1 || hooks.events[0]["type"] != "node.before_start" || hooks.events[0]["nodeRunId"] != node.ID || hooks.events[0]["stepId"] != "work" {
		t.Fatalf("events: %v", hooks.events)
	}
}
