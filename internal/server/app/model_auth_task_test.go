package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/app"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/config"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
)

type taskAuthOffline struct {
	account   atomic.Value
	refreshes atomic.Int32
}

func (f *taskAuthOffline) Login(_ context.Context, _ uuid.UUID, notify func(string) error) (modelcredential.Credential, error) {
	if err := notify("https://auth.openai.com/oauth/authorize?state=offline&code_challenge=offline"); err != nil {
		return modelcredential.Credential{}, err
	}
	raw, _ := json.Marshal(map[string]any{"type": "oauth", "access": "synthetic-task-access", "refresh": "synthetic-task-refresh", "accountId": f.account.Load().(string), "expires": time.Now().Add(time.Hour).UnixMilli()})
	return modelcredential.Decode(raw)
}
func (f *taskAuthOffline) Refresh(context.Context, modelcredential.Credential) (modelcredential.Credential, error) {
	f.refreshes.Add(1)
	return modelcredential.Credential{}, errors.New("unexpected offline refresh")
}
func (w *world) startOfflineAuth(f *taskAuthOffline) *server {
	w.t.Helper()
	cfg := w.cfg
	cfg.PiMaxProcesses = 4
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		w.t.Fatal(err)
	}
	a, err := app.Build(context.Background(), &cfg, config.NewLogger(io.Discard, cfg.LogFormat, cfg.LogLevel), app.Options{SearchPath: w.schema, Listener: ln, HomeDirs: []string{"/nonexistent-home"}, AuthLoginHelper: f, AuthRefresher: f})
	if err != nil {
		ln.Close()
		w.t.Fatal(err)
	}
	go func() { _ = a.Serve() }()
	return &server{t: w.t, a: a, url: "http://" + a.Addr() + "/api/v1"}
}
func TestAuthVersionSnapshotsSurviveReloginTaskRetryAndAppRestart(t *testing.T) {
	w := newWorld(t)
	fixture := &taskAuthOffline{}
	fixture.account.Store("account-A")
	s := w.startOfflineAuth(fixture)
	stopped := false
	defer func() {
		if !stopped {
			s.stop()
		}
	}()
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	agent := decode[apitypes.Agent](t, s.must(201, "POST", "/projects/"+dc.Project.ID.String()+"/agents", map[string]any{"name": "账号版本Manager", "model": map[string]string{"provider": "openai-codex", "modelId": "gpt-6.1-sol"}, "prompt": "offline"}))
	path := "/agents/" + agent.ID.String()
	s.must(200, "PUT", "/projects/"+dc.Project.ID.String()+"/manager", map[string]any{"agentId": agent.ID})
	var shared *cm.Account
	login := func(account string) uuid.UUID {
		fixture.account.Store(account)
		body := map[string]any{"provider": "openai-codex", "name": "共享OAuth"}
		if shared != nil {
			body = map[string]any{"provider": "openai-codex", "accountId": shared.ID, "expectedRevision": shared.Revision}
		}
		base := "/projects/" + dc.Project.ID.String() + "/model-auth/operations"
		op := decode[cm.LoginState](t, s.must(202, "POST", base, body)).Operation
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			state := decode[cm.LoginState](t, s.must(200, "GET", base+"/"+op.ID.String(), nil))
			if state.Operation.Status == "succeeded" && state.Account != nil && state.Account.CurrentVersionID != nil {
				shared = state.Account
				return *shared.CurrentVersionID
			}
		}
		t.Fatal("offline shared login did not commit")
		return uuid.Nil
	}
	a := login("account-A")
	s.must(202, "POST", path+"/messages", map[string]any{"text": "normal A baseline"})
	s.waitIdle(path)
	create := func(title, description string) taskmodel.Detail {
		raw, err := s.a.Tasks.Create(context.Background(), auth.Principal{Kind: auth.KindAgent, ID: agent.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: title, Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "offline step", AgentID: agent.ID, Description: description, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}})
		if err != nil {
			t.Fatal(err)
		}
		return decode[taskmodel.Detail](t, raw)
	}
	old := create("旧任务A", " !error !text=offline-task-A")
	blocked := taskWait(t, s, old.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
	oldRuntime := blocked.Nodes[0].RuntimeID
	originalSession, err := s.a.Store.TaskInstance(context.Background(), oldRuntime)
	if err != nil || originalSession.SessionFile == nil {
		t.Fatal("old task session missing", err)
	}
	b := login("account-B")
	if a == b {
		t.Fatal("relogin reused grant")
	}
	s.must(202, "POST", path+"/messages", map[string]any{"text": "normal B next"})
	s.waitIdle(path)
	newer := create("新任务B", "offline-task-B")
	taskWait(t, s, newer.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "running" && len(d.Nodes[0].RunIDs) > 0 })
	assertBound := func(runtime, version uuid.UUID, account string) {
		snapshot, err := s.a.Tasks.Source.ExecutionSnapshot(context.Background(), runtime)
		if err != nil || snapshot.ModelBinding == nil || snapshot.ModelBinding.VersionID != version {
			t.Fatal("task snapshot changed authorization", err)
		}
		encoded, err := snapshot.ModelCredential.AuthFile("openai-codex")
		if err != nil || !strings.Contains(string(encoded), account) {
			t.Fatal("task snapshot changed account", err)
		}
	}
	assertBound(oldRuntime, a, "account-A")
	assertBound(newer.Nodes[0].RuntimeID, b, "account-B")
	cache := filepath.Join(w.cfg.WorkRoot, "agents", oldRuntime.String(), "pi", "auth.json")
	if err := os.WriteFile(cache, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.a.Tasks.Retry(context.Background(), auth.Principal{Kind: auth.KindConsole}, old.Task.ID, taskmodel.RetryInput{ActionID: uuid.New(), ExpectedNodeRunID: *blocked.Task.CurrentNodeRunID, Note: "explicit offline retry"}); err != nil {
		t.Fatal(err)
	}
	retried := taskWait(t, s, old.Task.ID, func(d taskmodel.Detail) bool {
		return d.Task.Status == "blocked" && d.Nodes[len(d.Nodes)-1].Attempt == 2
	})
	_ = retried
	after, err := s.a.Store.TaskInstance(context.Background(), oldRuntime)
	if err != nil || after.SessionFile == nil || *after.SessionFile != *originalSession.SessionFile {
		t.Fatal("cache retry replaced session", err)
	}
	data, err := os.ReadFile(cache)
	if err != nil || !strings.Contains(string(data), "account-A") || strings.Contains(string(data), "account-B") {
		t.Fatal("cache repair changed account", err)
	}
	s.stop()
	stopped = true
	s = w.startOfflineAuth(fixture)
	stopped = false
	assertBound(oldRuntime, a, "account-A")
	assertBound(newer.Nodes[0].RuntimeID, b, "account-B")
	current, err := s.a.Config.ExecutionSnapshot(context.Background(), agent.ID)
	if err != nil || current.ModelBinding == nil || current.ModelBinding.VersionID != b {
		t.Fatal("restart lost current account B", err)
	}
	if fixture.refreshes.Load() != 0 {
		t.Fatal("offline task test unexpectedly attempted refresh")
	}
}
