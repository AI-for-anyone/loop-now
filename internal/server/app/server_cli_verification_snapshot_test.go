package app_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"io"
	"loop-now/internal/server/app"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/cliverify"
	verifyservice "loop-now/internal/server/cliverify/service"
	"loop-now/internal/server/config"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/taskruntime"
	"loop-now/internal/server/workflow"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCLIVerificationDoesNotRetrofitTaskOrPrivatePAT(t *testing.T) {
	w := newWorld(t)
	w.cfg.NodeBin = "/Users/ljl/.local/node-v24.21.0/bin/node"
	w.cfg.CLIProbeTimeout = time.Second
	w.cfg.AuthRecordTimeout = time.Second
	bin := filepath.Join(w.cfg.WorkRoot, "tools/gh/2.100.0/bin")
	if e := os.MkdirAll(bin, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(bin, "gh"), []byte("explicit scope-regression command fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	factory := func(_ context.Context, spec verifyservice.CommandSpec) (*exec.Cmd, error) {
		if spec.Phase == "version" {
			return exec.Command("/bin/sh", "-c", "printf 'gh version 2.100.0\\n'"), nil
		}
		present := false
		for _, value := range spec.Environment {
			if value == "GH_TOKEN=synthetic-current-B" {
				present = true
			}
		}
		if !present {
			return nil, fmt.Errorf("verification did not use fixed current B")
		}
		return exec.Command("/bin/sh", "-c", `printf 'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{"id":123,"login":"fixture"}\n'`), nil
	}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	a, e := app.Build(context.Background(), &w.cfg, config.NewLogger(io.Discard, "json", 0), app.Options{SearchPath: w.schema, Listener: ln, HomeDirs: []string{"/nonexistent-home"}, CLIVerificationCommand: factory})
	if e != nil {
		t.Fatal(e)
	}
	go a.Serve()
	s := &server{t: t, a: a, url: "http://" + a.Addr() + "/api/v1"}
	defer s.stop()
	ctx := context.Background()
	// Stop dispatch before creating a real captured task. No model/fakePi turn is
	// needed to prove immutable credential identity; no task control is retried.
	if e = a.Tasks.Stop(ctx); e != nil {
		t.Fatal(e)
	}
	_, agent := s.createAgent("snapshot owner")
	s.must(201, "POST", "/projects/"+agent.ProjectID.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "synthetic model", "credential": map[string]any{"type": "api_key", "key": "synthetic-model-key"}})
	if e = a.Store.AddAgentCLI(ctx, agent.ID, "gh"); e != nil {
		t.Fatal(e)
	}
	raw := s.must(201, "POST", "/projects/"+agent.ProjectID.String()+"/credential-accounts", map[string]any{"purpose": "cli", "cli": "gh", "name": "project PAT", "credential": map[string]any{"type": "cli", "fields": map[string]string{"token": "synthetic-task-A"}}})
	var account struct {
		ID       uuid.UUID `json:"id"`
		Revision int64     `json:"revision"`
	}
	if json.Unmarshal(raw, &account) != nil || account.ID == uuid.Nil {
		t.Fatal("no original PAT")
	}
	if _, e = a.Store.SetProjectManager(ctx, *agent.ProjectID, agent.ID); e != nil {
		t.Fatal(e)
	}
	created, e := a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: agent.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "captured version isolation", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "work", AgentID: agent.ID, Inputs: map[string]workflow.Source{}, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{{Name: "answer", Type: "string", Required: true}}}}, Edges: []workflow.Edge{}}})
	if e != nil {
		t.Fatal("real Tasks.Create capture failed", e)
	}
	var task taskmodel.Detail
	if json.Unmarshal(created, &task) != nil {
		t.Fatal("invalid task response")
	}
	instances, e := a.Store.TaskInstances(ctx, task.Task.ID)
	if e != nil || len(instances) != 1 || instances[0].ID == task.Task.ID {
		t.Fatal("separate runtime capture missing", e)
	}
	original := sha256.Sum256(instances[0].Snapshot)
	snapshot, e := taskruntime.Decode(instances[0].Snapshot)
	if e != nil {
		t.Fatal(e)
	}
	old, e := snapshot.Credentials()
	if e != nil || old.CLI["gh"].Env["GH_TOKEN"] != "synthetic-task-A" {
		t.Fatal("original snapshot invalid", e)
	}
	platform, e := a.Store.CreateAgent(ctx, store.PlatformScope(), "private PAT owner", "anthropic", "metadata-only", "")
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Store.AddAgentCLI(ctx, platform.ID, "gh"); e != nil {
		t.Fatal(e)
	}
	plain, _ := json.Marshal(clitemplate.Payload{Env: map[string]string{"GH_TOKEN": "synthetic-private-PAT"}, Files: []clitemplate.PayloadFile{}})
	private, e := a.Store.PutCredential(ctx, platform.ID, store.PurposeCLI, "gh", store.CredentialMeta{TemplateVersion: "1"}, plain)
	clear(plain)
	if e != nil {
		t.Fatal(e)
	}
	privateHash := sha256.Sum256(private.Secret)
	s.must(200, "PUT", "/projects/"+agent.ProjectID.String()+"/credential-accounts/"+account.ID.String()+"/credential", map[string]any{"expectedRevision": account.Revision, "credential": map[string]any{"type": "cli", "fields": map[string]string{"token": "synthetic-current-B"}}})
	current, e := a.Store.GetProjectCredential(ctx, *agent.ProjectID, account.ID)
	if e != nil {
		t.Fatal(e)
	}
	id := uuid.New()
	base := "/projects/" + agent.ProjectID.String() + "/credential-accounts/" + account.ID.String() + "/verification-operations"
	s.must(202, "POST", base, cliverify.Start{OperationID: id, ExpectedVersionID: *current.View.CurrentVersionID})
	deadline := time.Now().Add(3 * time.Second)
	var op cliverify.Operation
	for {
		raw = s.must(200, "GET", base+"/"+id.String(), nil)
		if json.Unmarshal(raw, &op) != nil {
			t.Fatal("invalid operation")
		}
		if op.Status != "pending" && op.Status != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixed B verification not settled")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if op.Status != "succeeded" {
		t.Fatal("fixed B probe failed")
	}
	after, e := a.Store.TaskInstance(ctx, instances[0].ID)
	if e != nil || sha256.Sum256(after.Snapshot) != original {
		t.Fatal("verification rewrote old task snapshot", e)
	}
	snapshot, e = taskruntime.Decode(after.Snapshot)
	if e != nil {
		t.Fatal(e)
	}
	old, e = snapshot.Credentials()
	if e != nil || old.CLI["gh"].Env["GH_TOKEN"] != "synthetic-task-A" {
		t.Fatal("verification substituted B into old A", e)
	}
	rows, e := a.Store.ListCredentials(ctx, platform.ID)
	if e != nil || len(rows) != 1 || rows[0].ID != private.ID || sha256.Sum256(rows[0].Secret) != privateHash {
		t.Fatal("private PAT changed", e)
	}
	if !bytes.Contains(rows[0].Secret, []byte("synthetic-private-PAT")) {
		t.Fatal("private PAT content invalid")
	}
	if strings.Contains(string(raw), "synthetic-current-B") {
		t.Fatal("verification response leaked PAT")
	}
	var runs, turns int
	if e = a.Store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM runs),(SELECT count(*) FROM task_turns)`).Scan(&runs, &turns); e != nil || runs != 0 || turns != 0 {
		t.Fatal("verification dispatched model/task turn", e)
	}
	t.Log("actual Tasks.Create distinct task/runtime: captured A/hash fixed after current B verification; platform private PAT content/ID fixed; model/task0; no native/provider proof claimed for shell scope fixture")
}
