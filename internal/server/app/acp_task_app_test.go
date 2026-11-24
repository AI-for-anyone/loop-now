package app_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/app"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/config"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
	"loop-now/internal/testutil/fakeacpbin"
)

// A task step run by a Grok Agent: Grok cannot copy the regular session, so the task instance starts a new session whose
// first message carries the regular conversation (a handoff with reason task_start, on the turn's timeline). The task
// ends and its cleanup records the session Grok keeps.
func TestGrokTaskStartsWithTheConversation(t *testing.T) {
	w := newWorld(t)
	realHome := t.TempDir() // the "real" home: fake Grok keeps its sessions and login under .fakeacp there
	w.options = func(o *app.Options) {
		o.GrokBin = fakeacpbin.Path(t)
		o.RealHome = func() (string, error) { return realHome, nil }
	}
	s := w.start(func(c *config.Config) { c.CodexModelsRefresh = time.Hour })
	defer s.stop()
	ctx := context.Background()
	s.must(200, "GET", "/runtimes?refresh=true", nil) // read the local CLIs' models now
	path, agent := s.createAgent("grok Manager")
	s.must(200, "PATCH", path, map[string]any{"model": map[string]string{"provider": "grok-cli", "modelId": "fake-1"}})
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	s.must(200, "PUT", "/projects/"+dc.Project.ID.String()+"/manager", map[string]any{"agentId": agent.ID})
	s.must(202, "POST", path+"/messages", map[string]any{"text": "the password is silver otter"})
	s.waitIdle(path)
	if r := s.replies(path); r[len(r)-1].Run.Status != "completed" || r[len(r)-1].Run.UsageReported {
		t.Fatalf("the regular conversation runs on Grok, which reports no usage: %+v", r[len(r)-1].Run)
	}

	body, err := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: agent.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "grok task", Request: map[string]any{},
		Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "run", Description: " fake:sleep 1s", AgentID: agent.ID,
			InputFields: []workflow.Field{}, OutputFields: []workflow.Field{{Name: "file", Type: "artifact", Required: true}}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}})
	if err != nil {
		t.Fatal(err)
	}
	var created taskmodel.Detail
	if err = json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	running := taskWait(t, s, created.Task.ID, func(d taskmodel.Detail) bool {
		return d.Task.Status == "running" && len(d.Nodes[0].RunIDs) > 0 && s.a.Executor.Process(d.Nodes[0].RuntimeID).State == "busy"
	})
	node := running.Nodes[0]
	principal := auth.Principal{Kind: auth.KindAgent, ID: agent.ID, TaskID: created.Task.ID, RuntimeID: node.RuntimeID}
	published, err := s.a.Tasks.Publish(ctx, principal, created.Task.ID, node.ID, uuid.New(), "result.txt", "text/plain", []byte("grok artifact"))
	if err != nil {
		t.Fatal(err)
	}
	var artifact taskmodel.Artifact
	if err = json.Unmarshal(published, &artifact); err != nil {
		t.Fatal(err)
	}
	if _, err = s.a.Tasks.Submit(ctx, principal, created.Task.ID, node.ID, taskmodel.SubmitInput{ActionID: uuid.New(),
		Output: map[string]any{"file": map[string]any{"artifactId": artifact.ID.String()}}}); err != nil {
		t.Fatal(err)
	}
	taskWait(t, s, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "completed" && d.Cleanup == "archived" })

	var sha string
	if err = s.a.Store.Pool.QueryRow(ctx, `SELECT blob_sha256 FROM task_archives WHERE runtime_id=$1`, node.RuntimeID).Scan(&sha); err != nil {
		t.Fatal("task archive missing", err)
	}
	data, err := s.a.Store.ReadBlob(ctx, sha)
	if err != nil {
		t.Fatal(err)
	}
	var archive struct {
		Runtime    string `json:"runtime"`
		SessionRef string `json:"sessionRef"`
	}
	if err = json.Unmarshal(data, &archive); err != nil || archive.Runtime != "grok" || archive.SessionRef == "" {
		t.Fatalf("the archive records the Grok session: %s %v", data, err)
	}
	b, err := os.ReadFile(filepath.Join(realHome, ".fakeacp", "sessions", archive.SessionRef+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var session struct {
		Prompts []string `json:"prompts"`
	}
	if err = json.Unmarshal(b, &session); err != nil || len(session.Prompts) == 0 {
		t.Fatalf("the instance's session: %s %v", b, err)
	}
	first := session.Prompts[0]
	if !strings.Contains(first, "<conversation-handoff>") || !strings.Contains(first, "silver otter") || !strings.Contains(first, "task instance") ||
		!strings.Contains(first, "fake:sleep 1s") || strings.Index(first, "<conversation-handoff>") > strings.Index(first, "fake:sleep 1s") {
		t.Fatalf("the instance's first message carries the regular conversation, before the step:\n%s", first)
	}
	var handoff map[string]any
	if err = s.a.Store.Pool.QueryRow(ctx, `SELECT data FROM lifecycle_events WHERE task_id=$1 AND type='context.handoff'`, created.Task.ID).Scan(&handoff); err != nil {
		t.Fatal("the turn records the handoff", err)
	}
	if handoff["reason"] != "task_start" || handoff["fromRuntime"] != "grok" || handoff["toRuntime"] != "grok" || handoff["includedMessages"] == float64(0) {
		t.Fatalf("handoff record: %+v", handoff)
	}
}
