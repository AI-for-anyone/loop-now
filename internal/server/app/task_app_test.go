package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
)

func taskWait(t *testing.T, s *server, id uuid.UUID, fn func(taskmodel.Detail) bool) taskmodel.Detail {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		r, e := s.a.Store.GetTask(context.Background(), id)
		if e != nil {
			t.Fatal(e)
		}
		if fn(r.View) {
			return r.View
		}
		time.Sleep(10 * time.Millisecond)
	}
	r, _ := s.a.Store.GetTask(context.Background(), id)
	var code *string
	_ = s.a.Store.Pool.QueryRow(context.Background(), `SELECT error_code FROM task_turns WHERE task_id=$1 ORDER BY queue_seq DESC LIMIT 1`, id).Scan(&code)
	t.Fatal("task condition timeout", r.View.Task.Status, r.View.Cleanup, "errorCode", func() string {
		if code != nil {
			return *code
		}
		return ""
	}())
	return taskmodel.Detail{}
}
func TestTaskRunsForkAndArchivesWithFakePi(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	path, agent := s.createAgent("任务Manager")
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	s.must(200, "PUT", "/projects/"+dc.Project.ID.String()+"/manager", map[string]any{"agentId": agent.ID})
	s.must(202, "POST", path+"/messages", map[string]any{"text": "baseline normal"})
	s.waitIdle(path)
	normal, e := s.a.Store.GetAgentSession(ctx, agent.ID)
	if e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(normal.SessionFile)
	if e != nil {
		t.Fatal(e)
	}
	body, e := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: agent.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "fakepi任务", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "执行", Description: " !sleep=1s !tool=bash !usage=7,11,0,0", AgentID: agent.ID, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{{Name: "file", Type: "artifact", Required: true}}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}})
	if e != nil {
		t.Fatal(e)
	}
	var created taskmodel.Detail
	if e = json.Unmarshal(body, &created); e != nil {
		t.Fatal(e)
	}
	running := taskWait(t, s, created.Task.ID, func(d taskmodel.Detail) bool {
		return d.Task.Status == "running" && len(d.Nodes[0].RunIDs) > 0 && s.a.Executor.Process(d.Nodes[0].RuntimeID).State == "busy"
	})
	node := running.Nodes[0]
	principal := auth.Principal{Kind: auth.KindAgent, ID: agent.ID, TaskID: created.Task.ID, RuntimeID: node.RuntimeID}
	published, e := s.a.Tasks.Publish(ctx, principal, created.Task.ID, node.ID, uuid.New(), "result.txt", "text/plain", []byte("retained artifact"))
	if e != nil {
		t.Fatal(e)
	}
	var artifact taskmodel.Artifact
	if e = json.Unmarshal(published, &artifact); e != nil {
		t.Fatal(e)
	}
	if _, e = s.a.Tasks.Submit(ctx, auth.Principal{Kind: auth.KindAgent, ID: agent.ID, TaskID: created.Task.ID, RuntimeID: node.RuntimeID}, created.Task.ID, node.ID, taskmodel.SubmitInput{ActionID: uuid.New(), Output: map[string]any{"file": map[string]any{"artifactId": artifact.ID.String()}}}); e != nil {
		t.Fatal(e)
	}
	finished := taskWait(t, s, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "completed" && d.Cleanup == "archived" })
	if finished.Nodes[0].Status != "completed" {
		t.Fatal("node not completed")
	}
	_, content, e := s.a.Tasks.Download(ctx, auth.Principal{Kind: auth.KindAgent, ID: agent.ID}, created.Task.ID, artifact.ID)
	if e != nil || string(content) != "retained artifact" {
		t.Fatal("artifact did not survive cleanup", e)
	}
	if _, e = os.Stat(filepath.Join(w.cfg.WorkRoot, "agents", node.RuntimeID.String())); !os.IsNotExist(e) {
		t.Fatal("task directory not reclaimed", e)
	}
	after, _ := os.ReadFile(normal.SessionFile)
	if !bytes.HasPrefix(after, before) {
		t.Fatal("task changed the frozen normal baseline; Manager notices may append later turns")
	}
	var tokenID uuid.UUID
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT id FROM agent_tokens WHERE runtime_id=$1 ORDER BY id DESC LIMIT 1`, node.RuntimeID).Scan(&tokenID); e != nil {
		t.Fatal(e)
	}
	token, e := s.a.Store.LookupAgentToken(ctx, tokenID)
	if e != nil || !token.Revoked || token.AgentID != agent.ID || token.TaskID == nil || *token.TaskID != created.Task.ID {
		t.Fatal("task process token not scoped/revoked", e)
	}
	var usage, events, archives int
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM usage_records WHERE task_id=$1 AND task_turn_id IS NOT NULL AND agent_id=$2 AND run_id IS NULL`, created.Task.ID, agent.ID).Scan(&usage); e != nil || usage == 0 {
		t.Fatal("task usage missing", e, usage)
	}
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM lifecycle_events WHERE task_id=$1 AND task_turn_id IS NOT NULL AND agent_id=$2 AND run_id IS NULL`, created.Task.ID, agent.ID).Scan(&events); e != nil || events == 0 {
		t.Fatal("task runtime records missing", e, events)
	}
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_archives WHERE runtime_id=$1`, node.RuntimeID).Scan(&archives); e != nil || archives != 1 {
		t.Fatal("task archive missing", e, archives)
	}
	secondBody, e := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: agent.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "引用隔离", Request: map[string]any{}, Workflow: created.Workflow})
	if e != nil {
		t.Fatal(e)
	}
	var second taskmodel.Detail
	if e = json.Unmarshal(secondBody, &second); e != nil {
		t.Fatal(e)
	}
	secondRunning := taskWait(t, s, second.Task.ID, func(d taskmodel.Detail) bool {
		return d.Task.Status == "running" && s.a.Executor.Process(d.Nodes[0].RuntimeID).State == "busy"
	})
	secondNode := secondRunning.Nodes[0]
	if _, e = s.a.Tasks.Submit(ctx, auth.Principal{Kind: auth.KindAgent, ID: agent.ID, TaskID: second.Task.ID, RuntimeID: secondNode.RuntimeID}, second.Task.ID, secondNode.ID, taskmodel.SubmitInput{ActionID: uuid.New(), Output: map[string]any{"file": map[string]any{"artifactId": artifact.ID.String()}}}); !apperr.Is(e, apperr.CodeValidationFailed) {
		t.Fatal("cross-task artifact accepted", e)
	}
	if _, e = s.a.Tasks.Cancel(ctx, auth.Console(), second.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()}); e != nil {
		t.Fatal(e)
	}
	taskWait(t, s, second.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "cancelled" && d.Cleanup == "archived" })

}

func TestTaskRestartBlocksSubmittedFakePiRoundWithoutReplay(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	ctx := context.Background()
	path, agent := s.createAgent("恢复Manager")
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	s.must(200, "PUT", "/projects/"+dc.Project.ID.String()+"/manager", map[string]any{"agentId": agent.ID})
	s.must(202, "POST", path+"/messages", map[string]any{"text": "baseline for restart"})
	s.waitIdle(path)
	body, e := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: agent.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "中断恢复", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "执行", Description: " !sleep=10s", AgentID: agent.ID, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}})
	if e != nil {
		s.stop()
		t.Fatal(e)
	}
	var created taskmodel.Detail
	if e = json.Unmarshal(body, &created); e != nil {
		s.stop()
		t.Fatal(e)
	}
	running := taskWait(t, s, created.Task.ID, func(d taskmodel.Detail) bool {
		return d.Task.Status == "running" && s.a.Executor.Process(d.Nodes[0].RuntimeID).State == "busy"
	})
	node := running.Nodes[0]
	if _, e = s.a.Tasks.Submit(ctx, auth.Principal{Kind: auth.KindAgent, ID: agent.ID, TaskID: created.Task.ID, RuntimeID: node.RuntimeID}, created.Task.ID, node.ID, taskmodel.SubmitInput{ActionID: uuid.New(), Output: map[string]any{}}); e != nil {
		s.stop()
		t.Fatal(e)
	}
	s.stop()
	restarted := w.start(nil)
	defer restarted.stop()
	restored := taskWait(t, restarted, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
	if restored.Nodes[0].Output == nil || restored.Nodes[0].RuntimeID != node.RuntimeID {
		t.Fatal("restart lost submitted output or instance")
	}
	time.Sleep(200 * time.Millisecond)
	var rounds int
	if e = restarted.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE task_id=$1`, created.Task.ID).Scan(&rounds); e != nil || rounds != 1 {
		t.Fatal("restart replayed round", e, rounds)
	}
	if restarted.a.Executor.Process(node.RuntimeID).State != "stopped" {
		t.Fatal("restart reactivated task Pi")
	}
	instance, e := restarted.a.Store.TaskInstance(ctx, node.RuntimeID)
	if e != nil || instance.SessionFile == nil {
		t.Fatal("restart session path missing", e)
	}
	saved, e := os.ReadFile(*instance.SessionFile)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(*instance.SessionFile); e != nil {
		t.Fatal(e)
	}
	if _, e = restarted.a.Tasks.Retry(ctx, auth.Console(), created.Task.ID, taskmodel.RetryInput{ActionID: uuid.New(), ExpectedNodeRunID: restored.Nodes[0].ID}); e != nil {
		t.Fatal(e)
	}
	taskWait(t, restarted, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" && len(d.Nodes) == 2 })
	var code string
	if e = restarted.a.Store.Pool.QueryRow(ctx, `SELECT error_code FROM task_turns WHERE task_id=$1 ORDER BY queue_seq DESC LIMIT 1`, created.Task.ID).Scan(&code); e != nil || code != "session_missing" {
		t.Fatal("missing session silently replaced", e, code)
	}
	if restarted.a.Executor.Process(node.RuntimeID).State != "stopped" {
		t.Fatal("missing session launched replacement Pi")
	}
	if e = os.WriteFile(*instance.SessionFile, saved, 0o600); e != nil {
		t.Fatal(e)
	}
	if e = restarted.a.Store.RecordTaskSession(ctx, node.RuntimeID, *instance.SessionFile); e != nil {
		t.Fatal(e)
	}

	if _, e = restarted.a.Tasks.Cancel(ctx, auth.Console(), created.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()}); e != nil {
		t.Fatal(e)
	}
	taskWait(t, restarted, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "cancelled" && d.Cleanup == "archived" })
}
