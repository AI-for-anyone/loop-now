package local

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
)

// fakeAsker answers permission requests with answer, or, when block is set, waits until each is cancelled and keeps
// why.
type fakeAsker struct {
	mu        sync.Mutex
	asked     []executor.PermissionAsk
	cancelled []string
	answer    executor.PermissionAnswer
	block     bool
	asking    chan executor.PermissionAsk
}

func newFakeAsker() *fakeAsker { return &fakeAsker{asking: make(chan executor.PermissionAsk, 16)} }

func (f *fakeAsker) AskPermission(ctx context.Context, q executor.PermissionAsk) executor.PermissionAnswer {
	f.mu.Lock()
	f.asked = append(f.asked, q)
	block, answer := f.block, f.answer
	f.mu.Unlock()
	f.asking <- q
	if !block {
		return answer
	}
	<-ctx.Done()
	f.mu.Lock()
	f.cancelled = append(f.cancelled, executor.CancelReason(ctx))
	f.mu.Unlock()
	return executor.PermissionAnswer{}
}

func (f *fakeAsker) set(answer executor.PermissionAnswer, block bool) {
	f.mu.Lock()
	f.answer, f.block = answer, block
	f.mu.Unlock()
}

func (f *fakeAsker) reasons() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cancelled...)
}

func withAsker(a executor.PermissionAsker) func(o *Options) {
	return func(o *Options) { o.Permissions = a }
}

// Each mode starts Claude Code with its permission mode; in any mode but Full access, permission prompts come to Loop
// Now (--permission-prompts host). A mode change restarts it with the new one.
func TestModeFlagsPerRuntime(t *testing.T) {
	h := newClaudeHarness(t, withAsker(newFakeAsker()))
	want := map[string]string{"supervised": "manual", "accept_edits": "acceptEdits", "auto": "auto", "full": "bypassPermissions"}
	for mode, flag := range want {
		id := h.claudeAgent(func(s *agentconfig.Snapshot) { s.PermissionMode = mode })
		if res, _ := h.exec(id, "hello"); res.Outcome != executor.OutcomeCompleted {
			t.Fatalf("%s: %+v", mode, res)
		}
		launches := h.launches()
		args := launches[len(launches)-1]
		prompts := "host"
		if mode == "full" {
			prompts = "none"
		}
		if argValue(args, "--permission-mode") != flag || argValue(args, "--permission-prompts") != prompts {
			t.Errorf("%s: %v", mode, args)
		}
	}
	legacy := h.claudeAgent(nil)
	h.exec(legacy, "hello")
	launches := h.launches()
	if args := launches[len(launches)-1]; argValue(args, "--permission-mode") != "bypassPermissions" {
		t.Errorf("no mode (a task from before modes) is Full access: %v", args)
	}
}

// A mode change applies from the next message: the runtime restarts with it.
func TestModeChangeAppliesNextMessage(t *testing.T) {
	h := newClaudeHarness(t, withAsker(newFakeAsker()))
	id := h.claudeAgent(func(s *agentconfig.Snapshot) { s.PermissionMode = "full" })
	h.exec(id, "hello")
	h.src.update(id, func(s *agentconfig.Snapshot) { s.ConfigVersion++; s.PermissionMode = "supervised" })
	h.exec(id, "again")
	launches := h.launches()
	if len(launches) != 2 || argValue(launches[1], "--permission-mode") != "manual" || argValue(launches[1], "--resume") == "" {
		t.Fatalf("restarted in the new mode, same session: %v", launches)
	}
}

// Claude Code's can_use_tool request becomes a permission request of the run: approving lets the tool run; rejecting
// with a reason tells Claude Code that reason.
func TestClaudeCanUseToolBecomesRequest(t *testing.T) {
	asker := newFakeAsker()
	h := newClaudeHarness(t, withAsker(asker))
	id := h.claudeAgent(func(s *agentconfig.Snapshot) { s.PermissionMode = "supervised" })
	asker.set(executor.PermissionAnswer{Approved: true}, false)
	run := uuid.Must(uuid.NewV7())
	res, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: run, Message: "fake:ask ls -la"}, newSink())
	if err != nil || res.Text != "allowed" {
		t.Fatalf("approved: %+v %v", res, err)
	}
	var q executor.PermissionAsk
	select {
	case q = <-asker.asking:
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was asked")
	}
	if q.AgentID != id || q.RunID != run || q.Runtime != "claude-code" || q.Kind != "command" || q.Detail != "ls -la" || !strings.Contains(q.Title, "ls -la") {
		t.Fatalf("request: %+v", q)
	}
	asker.set(executor.PermissionAnswer{Reason: "use rg instead"}, false)
	if res, _ := h.exec(id, "fake:ask rm -rf build"); res.Text != "denied: use rg instead" {
		t.Fatalf("rejected: %+v", res)
	}
	asker.set(executor.PermissionAnswer{}, false)
	if res, _ := h.exec(id, "fake:ask rm -rf build"); !strings.HasPrefix(res.Text, "denied: ") || res.Text == "denied: " {
		t.Fatalf("a rejection without a reason still tells Claude Code something: %+v", res)
	}
}

// Stopping a reply while a request waits cancels it (reply_stopped); Claude Code is not left waiting.
func TestStopCancelsPending(t *testing.T) {
	asker := newFakeAsker()
	asker.set(executor.PermissionAnswer{}, true)
	h := newClaudeHarness(t, withAsker(asker))
	id := h.claudeAgent(func(s *agentconfig.Snapshot) { s.PermissionMode = "supervised" })
	run := uuid.Must(uuid.NewV7())
	done := make(chan executor.Result, 1)
	go func() {
		res, _ := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: run, Message: "fake:ask make deploy"}, newSink())
		done <- res
	}()
	select {
	case <-asker.asking:
	case <-time.After(15 * time.Second):
		t.Fatal("no request")
	}
	if err := h.e.Interrupt(context.Background(), id, run); err != nil {
		t.Fatal(err)
	}
	select {
	case res := <-done:
		if res.Outcome != executor.OutcomeInterrupted {
			t.Fatalf("outcome: %+v", res)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the reply did not stop")
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(asker.reasons()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if r := asker.reasons(); len(r) != 1 || r[0] != "reply_stopped" {
		t.Fatalf("cancelled: %v", r)
	}
}

// Codex's thread gets the approval settings of the mode (T3's), and its approval requests become permission requests:
// approving accepts, rejecting declines.
func TestCodexApprovalBecomesRequest(t *testing.T) {
	asker := newFakeAsker()
	h := newCodexHarness(t, withAsker(asker))
	type cfg struct {
		ApprovalPolicy any `json:"approvalPolicy"`
		Sandbox        any `json:"sandbox"`
	}
	want := map[string][2]string{"supervised": {"untrusted", "read-only"}, "accept_edits": {"on-request", "workspace-write"},
		"auto": {"on-request", "workspace-write"}, "full": {"never", "danger-full-access"}}
	for mode, w := range want {
		id := h.codexAgent(func(s *agentconfig.Snapshot) { s.PermissionMode = mode })
		res, _ := h.exec(id, "fake:config")
		var c cfg
		if err := json.Unmarshal([]byte(res.Text), &c); err != nil || c.ApprovalPolicy != w[0] || c.Sandbox != w[1] {
			t.Errorf("%s: %s", mode, res.Text)
		}
	}
	id := h.codexAgent(func(s *agentconfig.Snapshot) { s.PermissionMode = "supervised" })
	asker.set(executor.PermissionAnswer{Approved: true}, false)
	run := uuid.Must(uuid.NewV7())
	res, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: run, Message: "fake:approval"}, newSink())
	if err != nil || res.Text != "approval=accept" {
		t.Fatalf("approved: %+v %v", res, err)
	}
	select {
	case q := <-asker.asking:
		if q.RunID != run || q.Runtime != "codex" || q.Kind != "command" || !strings.Contains(q.Title, "rm -rf build") || !strings.Contains(q.Detail, "rm -rf build") {
			t.Fatalf("request: %+v", q)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was asked")
	}
	asker.set(executor.PermissionAnswer{Reason: "not now"}, false)
	if res, _ := h.exec(id, "fake:approval"); res.Text != "approval=decline" {
		t.Fatalf("rejected: %+v", res)
	}
	full := h.codexAgent(func(s *agentconfig.Snapshot) { s.PermissionMode = "full" })
	if res, _ := h.exec(full, "fake:approval"); res.Text != "approval=accept" || len(asker.asking) != 1 {
		t.Fatalf("in Full access nothing is asked: %+v, %d asked", res, len(asker.asking))
	}
}

// Grok and Antigravity drop their approve-everything flag outside Full access and use their own session modes for
// Auto-accept edits and Auto when the session offers them (a mode Grok lacks runs as Supervised); their permission
// requests become permission requests: approving picks the agent's allow-once option, rejecting its reject-once.
func TestACPPermissionBecomesRequest(t *testing.T) {
	asker := newFakeAsker()
	type cfg struct {
		Args []string `json:"args"`
		Mode string   `json:"mode"`
	}
	config := func(h *acpHarness, id uuid.UUID) cfg {
		t.Helper()
		res, _ := h.exec(id, "fake:config")
		var c cfg
		if err := json.Unmarshal([]byte(res.Text), &c); err != nil {
			t.Fatalf("%s: %v", res.Text, err)
		}
		return c
	}
	has := func(args []string, flag string) bool {
		for _, a := range args {
			if a == flag {
				return true
			}
		}
		return false
	}
	grok := newACPHarness(t, "grok", withAsker(asker))
	for mode, want := range map[string]cfg{"full": {Mode: ""}, "supervised": {Mode: ""}, "accept_edits": {Mode: ""}, "auto": {Mode: "auto"}} {
		c := config(grok, grok.acpAgent(func(s *agentconfig.Snapshot) { s.PermissionMode = mode }))
		if has(c.Args, "--always-approve") != (mode == "full") || c.Mode != want.Mode {
			t.Errorf("grok %s: %+v", mode, c)
		}
	}
	agy := newACPHarness(t, "agy", withAsker(asker))
	for mode, want := range map[string]string{"full": "", "supervised": "", "accept_edits": "auto_edit", "auto": "auto"} {
		c := config(agy, agy.acpAgent(func(s *agentconfig.Snapshot) { s.PermissionMode = mode }))
		if has(c.Args, "--dangerously-skip-permissions") != (mode == "full") || c.Mode != want {
			t.Errorf("agy %s: %+v", mode, c)
		}
	}
	id := grok.acpAgent(func(s *agentconfig.Snapshot) { s.PermissionMode = "supervised" })
	asker.set(executor.PermissionAnswer{Approved: true}, false)
	run := uuid.Must(uuid.NewV7())
	res, err := grok.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: run, Message: "fake:permission"}, newSink())
	if err != nil || !strings.HasSuffix(res.Text, "permission=yes") {
		t.Fatalf("approved: %+v %v", res, err)
	}
	select {
	case q := <-asker.asking:
		if q.RunID != run || q.Runtime != "grok" || q.Kind != "command" || q.Title != "Run make deploy" || q.Detail != "make deploy" {
			t.Fatalf("request: %+v", q)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was asked")
	}
	asker.set(executor.PermissionAnswer{Reason: "no"}, false)
	if res, _ := grok.exec(id, "fake:permission"); !strings.HasSuffix(res.Text, "permission=no") {
		t.Fatalf("rejected: %+v", res)
	}
}
