package app_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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
	"loop-now/internal/testutil/fakecodexbin"
)

// A task step run by a Codex Agent ends and its cleanup succeeds: Codex keeps the instance's session (a thread) itself,
// so the archive records the thread, the instance directory is reclaimed, and the thread is archived in Codex.
func TestCodexTaskEndsAndIsCleanedUp(t *testing.T) {
	w := newWorld(t)
	realHome := t.TempDir() // the "real" home: Codex's data goes to its .codex, never to the user's
	w.options = func(o *app.Options) {
		o.CodexBin = fakecodexbin.Path(t)
		o.RealHome = func() (string, error) { return realHome, nil }
	}
	s := w.start(func(c *config.Config) { c.CodexModelsRefresh = time.Hour })
	defer s.stop()
	ctx := context.Background()
	s.must(200, "GET", "/runtimes?refresh=true", nil) // read Codex's models now
	path, agent := s.createAgent("codex Manager")
	s.must(200, "PATCH", path, map[string]any{"model": map[string]string{"provider": "codex-cli", "modelId": "gpt-test"}})
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	s.must(200, "PUT", "/projects/"+dc.Project.ID.String()+"/manager", map[string]any{"agentId": agent.ID})
	s.must(202, "POST", path+"/messages", map[string]any{"text": "the password is silver otter"})
	s.waitIdle(path)
	if r := s.replies(path); r[len(r)-1].Run.Status != "completed" {
		t.Fatalf("the regular conversation runs on Codex: %+v", r[len(r)-1].Run)
	}

	body, err := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: agent.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "codex task", Request: map[string]any{},
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
	published, err := s.a.Tasks.Publish(ctx, principal, created.Task.ID, node.ID, uuid.New(), "result.txt", "text/plain", []byte("codex artifact"))
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
		RuntimeID  uuid.UUID `json:"runtimeId"`
		Runtime    string    `json:"runtime"`
		SessionRef string    `json:"sessionRef"`
		Session    []byte    `json:"session"`
	}
	if err = json.Unmarshal(data, &archive); err != nil || archive.RuntimeID != node.RuntimeID || archive.Runtime != "codex" || archive.SessionRef == "" || archive.Session != nil {
		t.Fatalf("the archive records the Codex thread: %s %v", data, err)
	}
	if _, err = os.Stat(filepath.Join(w.cfg.WorkRoot, "agents", node.RuntimeID.String())); !os.IsNotExist(err) {
		t.Fatal("task directory not reclaimed", err)
	}
	var archived []string
	files, _ := filepath.Glob(filepath.Join(realHome, ".codex", "fake-archived", "*.json"))
	for _, f := range files {
		archived = append(archived, strings.TrimSuffix(filepath.Base(f), ".json"))
	}
	if !slices.Contains(archived, archive.SessionRef) {
		t.Fatalf("the instance's thread %s is archived in Codex: %v", archive.SessionRef, archived)
	}
}
