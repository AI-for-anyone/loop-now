package tasks

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/db"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/permissions"
	"loop-now/internal/server/prompts"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskruntime"
)

// A task step runs in the permission mode its Agent had when the task started: changing the Agent's mode later does
// not change the step's.
func TestTaskInstanceUsesAgentModeAtStart(t *testing.T) {
	svc, taskID, p := fixture(t, false)
	ctx := context.Background()
	supervised, full := "supervised", "full"
	agent, err := svc.Store.UpdateAgent(ctx, p.ID, store.AgentPatch{PermissionMode: &supervised})
	if err != nil {
		t.Fatal(err)
	}
	var captured taskruntime.Configuration
	err = svc.Store.Pool.InTx(ctx, func(tx *db.Tx) error {
		project := prompts.ProjectRules{ID: *agent.ProjectID}
		if err := tx.QueryRow(ctx, `SELECT prompt,prompt_version FROM projects WHERE id=$1`, project.ID).Scan(&project.Prompt, &project.Version); err != nil {
			return err
		}
		c, err := svc.Store.CaptureTaskAgent(ctx, tx, p.ID, p.RuntimeID, project)
		captured = c.Configuration
		return err
	})
	if err != nil || captured.PermissionMode != "supervised" {
		t.Fatalf("captured: %q %v", captured.PermissionMode, err)
	}
	scope, err := taskruntime.TaskScope(p.ID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := taskruntime.Capture(scope, p.RuntimeID, taskruntime.CaptureInput{Configuration: captured, Credentials: taskruntime.Credentials{Model: map[string]string{}, CLI: map[string]taskruntime.CredentialPayload{}}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := snap.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Store.Pool.Exec(ctx, `UPDATE task_instances SET snapshot=$2 WHERE id=$1`, p.RuntimeID, data); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Store.UpdateAgent(ctx, p.ID, store.AgentPatch{PermissionMode: &full}); err != nil {
		t.Fatal(err)
	}
	got, err := (&Source{Store: svc.Store}).ExecutionSnapshot(ctx, p.RuntimeID)
	if err != nil || got.PermissionMode != "supervised" {
		t.Fatalf("the step's mode: %q %v", got.PermissionMode, err)
	}
}

// A task step's permission request belongs to its task turn and node, with the Agent the instance runs: the node's
// pending list shows it and goes to the task's event stream on every change; answering it gives the step the answer.
func TestTaskStepPermissionRequestOnNode(t *testing.T) {
	s, id, p := fixture(t, false)
	ctx := context.Background()
	tt := turn(t, s, id)
	svc := &permissions.Service{Store: s.Store, Poll: 10 * time.Millisecond, Changed: func(r store.PermissionRequest) {
		if err := EmitNodePermissionRequests(ctx, s.Store, *r.TaskTurnID); err != nil {
			t.Error(err)
		}
	}}
	answer := make(chan executor.PermissionAnswer, 1)
	go func() {
		answer <- svc.AskPermission(ctx, executor.PermissionAsk{AgentID: p.RuntimeID, RunID: tt.ID, Runtime: "claude-code", Kind: "command", Title: "Run make deploy"})
	}()
	console := auth.Principal{Kind: auth.KindConsole}
	var list apitypes.TaskNodePermissionRequests
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		var err error
		if list, err = s.NodePermissionRequests(ctx, console, id, tt.NodeRunID); err != nil {
			t.Fatal(err)
		}
		if len(list.Requests) == 1 {
			break
		}
	}
	if len(list.Requests) != 1 || list.RuntimeID != p.RuntimeID {
		t.Fatalf("node list: %+v", list)
	}
	r := list.Requests[0]
	if r.AgentID != p.ID || r.RunID != tt.ID || r.TaskID == nil || *r.TaskID != id || r.NodeRunID == nil || *r.NodeRunID != tt.NodeRunID {
		t.Fatalf("request: %+v", r)
	}
	if _, err := svc.Answer(ctx, r.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	select {
	case a := <-answer:
		if !a.Approved {
			t.Fatalf("answer: %+v", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the step got no answer")
	}
	events, err := s.Store.TaskEvents(ctx, &id, nil, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var sizes []int
	for _, e := range events {
		if e.Type == EventNodePermissionRequests {
			var v apitypes.TaskNodePermissionRequests
			if err := json.Unmarshal(e.Data, &v); err != nil {
				t.Fatal(err)
			}
			sizes = append(sizes, len(v.Requests))
		}
	}
	if len(sizes) < 2 || sizes[0] != 1 || sizes[len(sizes)-1] != 0 {
		t.Fatalf("node events: %v", sizes)
	}
	if _, err := s.NodePermissionRequests(ctx, auth.Principal{Kind: auth.KindAgent, ID: p.ID}, id, tt.NodeRunID); err == nil {
		t.Fatal("only the console reads a node's requests")
	}
}
