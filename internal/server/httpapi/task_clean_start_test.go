package httpapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/folders"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks"
)

// cleanStartFixture creates a task whose one step is done by the Manager, with the step's extra fields, and a Source
// whose pause, freeze and fork callbacks count their calls. The Manager's regular conversation is active and its folder
// holds a note, so a fork would have something to copy.
func cleanStartFixture(t *testing.T, step map[string]any) (*env, *tasks.Source, store.TaskInstance, *atomic.Int32) {
	t.Helper()
	e, manager, d := taskEnv(t)
	ctx := context.Background()
	agent := d.Nodes[0].AgentID
	s := map[string]any{"id": "work", "title": "Handle", "description": "Do the work", "agentId": agent, "inputFields": []any{}, "outputFields": []any{}, "inputs": map[string]any{}}
	for k, v := range step {
		s[k] = v
	}
	created := decode[taskmodel.Detail](t, manager.expect(201, "POST", "/tasks", map[string]any{"actionId": uuid.New(), "title": "Clean start", "request": map[string]any{},
		"workflow": map[string]any{"startStepId": "work", "steps": []any{s}, "edges": []any{}}}))
	if err := e.st.RecordSessionFile(ctx, agent, "/sessions/regular.jsonl"); err != nil {
		t.Fatal(err)
	}
	calls := &atomic.Int32{}
	src := &tasks.Source{Normal: e.api.Config, Store: e.st, ModelAuth: e.api.Config.ModelAuth.Coordinator, WorkRoot: t.TempDir(), MaxSessionBytes: 4096,
		PauseNormal:  func(context.Context, uuid.UUID) (func(), error) { calls.Add(1); return func() {}, nil },
		FreezeNormal: func(context.Context, uuid.UUID) error { calls.Add(1); return nil },
		ForkBaseline: func(context.Context, string, uuid.UUID, string, int64) ([]byte, error) {
			calls.Add(1)
			return []byte(`{"type":"session"}`), nil
		}}
	note := filepath.Join(folders.AgentWorkspace(src.WorkRoot, agent), "notes.md")
	if err := os.MkdirAll(filepath.Dir(note), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(note, []byte("a note"), 0o600); err != nil {
		t.Fatal(err)
	}
	instances, err := e.st.TaskInstances(ctx, created.Task.ID)
	if err != nil || len(instances) != 1 {
		t.Fatal(err, len(instances))
	}
	return e, src, instances[0], calls
}

func preparedInstance(t *testing.T, e *env, src *tasks.Source, inst store.TaskInstance) store.TaskInstance {
	t.Helper()
	ctx := context.Background()
	if err := src.Prepare(ctx, inst); err != nil {
		t.Fatal(err)
	}
	got, err := e.st.TaskInstance(ctx, inst.ID)
	if err != nil || !got.Prepared {
		t.Fatal("the instance is not prepared", err)
	}
	if b, err := os.ReadFile(filepath.Join(folders.AgentWorkspace(src.WorkRoot, inst.ID), "notes.md")); err != nil || string(b) != "a note" {
		t.Fatal("the Agent folder was not copied", err)
	}
	return got
}

// A new task's step starts from a clean conversation: preparing it neither pauses nor freezes the base Agent (so it
// does not wait for a reply in progress) and takes no fork of its regular conversation; the Agent folder is still
// copied.
func TestCleanTaskPreparesWithoutPausingOrForking(t *testing.T) {
	e, src, inst, calls := cleanStartFixture(t, nil)
	got := preparedInstance(t, e, src, inst)
	if got.ForkSource != nil || calls.Load() != 0 {
		t.Fatalf("a clean step forked or paused its Agent: fork=%v calls=%d", got.ForkSource, calls.Load())
	}
	// Nothing about the regular conversation matters: not even a missing session blocks a clean step.
	e2, src2, inst2, calls2 := cleanStartFixture(t, nil)
	if _, err := e2.st.Pool.Exec(context.Background(), `UPDATE agent_sessions SET state='missing' WHERE agent_id=$1`, inst2.AgentID); err != nil {
		t.Fatal(err)
	}
	if got := preparedInstance(t, e2, src2, inst2); got.ForkSource != nil || calls2.Load() != 0 {
		t.Fatal("a clean step looked at the regular session")
	}
	// The pause and fork callbacks are only needed for a fork.
	e3, _, inst3, _ := cleanStartFixture(t, nil)
	bare := &tasks.Source{Normal: e3.api.Config, Store: e3.st, ModelAuth: e3.api.Config.ModelAuth.Coordinator, WorkRoot: t.TempDir()}
	if err := bare.Prepare(context.Background(), inst3); err != nil {
		t.Fatal("a clean step needs no fork preparer:", err)
	}
}

// A step that asks for its Agent's conversation is prepared as before: the Agent is paused and frozen while its
// regular session is forked.
func TestStepWithConversationForks(t *testing.T) {
	e, src, inst, calls := cleanStartFixture(t, map[string]any{"withConversation": true, "prompt": "Background"})
	got := preparedInstance(t, e, src, inst)
	if got.ForkSource == nil || calls.Load() != 3 {
		t.Fatalf("an opt-in step must fork after pausing and freezing: fork=%v calls=%d", got.ForkSource, calls.Load())
	}
}

// A task created before steps started clean (snapshot schema 10) keeps forking its Agents' regular conversations.
func TestOldSchemaTaskStillForks(t *testing.T) {
	e, src, inst, calls := cleanStartFixture(t, nil)
	var env map[string]any
	if err := json.Unmarshal(inst.Snapshot, &env); err != nil {
		t.Fatal(err)
	}
	env["schemaVersion"] = 10
	raw, _ := json.Marshal(env)
	sum := sha256.Sum256(raw)
	if _, err := e.st.Pool.Exec(context.Background(), `UPDATE task_instances SET snapshot=$2,snapshot_sha=$3 WHERE id=$1`, inst.ID, raw, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	inst.Snapshot = raw
	got := preparedInstance(t, e, src, inst)
	if got.ForkSource == nil || calls.Load() != 3 {
		t.Fatalf("an old task must keep forking: fork=%v calls=%d", got.ForkSource, calls.Load())
	}
}
