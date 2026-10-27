package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/app"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/config"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
)

type toolsGoogleLogin func(context.Context, uuid.UUID, func(string) error) (clicredential.Google, error)

func (f toolsGoogleLogin) Login(ctx context.Context, id uuid.UUID, url func(string) error) (clicredential.Google, error) {
	return f(ctx, id, url)
}

type toolsGoogleRefresh func(context.Context, clicredential.Google) (clicredential.Google, error)

func (f toolsGoogleRefresh) Refresh(ctx context.Context, c clicredential.Google) (clicredential.Google, error) {
	return f(ctx, c)
}

type toolsProcessDump struct {
	Env map[string]string `json:"env"`
	Cwd string            `json:"cwd"`
}

func toolsScopeDump(t *testing.T, base string, scope uuid.UUID) toolsProcessDump {
	t.Helper()
	files, e := filepath.Glob(base + ".*")
	if e != nil {
		t.Fatal(e)
	}
	var found toolsProcessDump
	var latest time.Time
	for _, file := range files {
		raw, e := os.ReadFile(file)
		if e != nil {
			continue
		}
		var d toolsProcessDump
		e = json.Unmarshal(raw, &d)
		clear(raw)
		if e != nil || !strings.Contains(d.Env["HOME"], scope.String()) {
			continue
		}
		fi, e := os.Stat(file)
		if e == nil && fi.ModTime().After(latest) {
			latest = fi.ModTime()
			found = d
		}
	}
	if found.Env == nil {
		t.Fatal("private scope process metadata not found")
	}
	return found
}
func toolsBrokerAccess(t *testing.T, d toolsProcessDump) string {
	t.Helper()
	conn, e := net.DialTimeout("unix", d.Env["LOOP_NOW_GCLOUD_BRIDGE_SOCKET"], time.Second)
	if e != nil {
		t.Fatal("captured broker unavailable")
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	json.NewEncoder(conn).Encode(map[string]any{"protocol": 1, "type": "resolve", "requestId": uuid.New(), "capability": d.Env["LOOP_NOW_GCLOUD_BRIDGE_CAPABILITY"]})
	raw, e := io.ReadAll(conn)
	if e != nil {
		t.Fatal("broker did not close complete reply")
	}
	defer clear(raw)
	var reply struct{ Type, AccessToken string }
	if json.Unmarshal(raw, &reply) != nil || reply.Type != "credential" {
		t.Fatal("captured broker did not resolve")
	}
	return reply.AccessToken
}

// Complete official SDK extraction/validation is bounded by the configured
// one-minute install budget, including race instrumentation. Do not reuse the
// lightweight fakePi test helper's shorter thirty-second install assumption.
func toolsWaitIdle(t *testing.T, s *server, path string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		st := decode[apitypes.AgentStatus](t, s.must(200, "GET", path+"/status", nil))
		owner, e := s.a.Coord.LockOwner(context.Background(), strings.TrimPrefix(path, "/agents/"))
		if !st.Busy && st.Queued.Interrupt == 0 && st.Queued.Normal == 0 && e == nil && owner == "" {
			return
		}
		if time.Now().After(deadline) {
			phase, status, code := "", "", ""
			if st.CurrentRun != nil {
				status = st.CurrentRun.Status
				if st.CurrentRun.Phase != nil {
					phase = *st.CurrentRun.Phase
				}
				if st.CurrentRun.ErrorCode != nil {
					code = *st.CurrentRun.ErrorCode
				}
			}
			t.Fatalf("tool App idle deadline: busy=%t status=%s phase=%s code=%s", st.Busy, status, phase, code)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestGoogleActualAppFixedGrantAndPrivateRuntime(t *testing.T) {
	var logins, refreshes atomic.Int64
	login := toolsGoogleLogin(func(context.Context, uuid.UUID, func(string) error) (clicredential.Google, error) {
		n := logins.Add(1)
		label := []string{"A", "B"}[n-1]
		return clicredential.Google{Type: "authorized_user", Account: "fixture-" + label + "@example.invalid", ClientID: "fixture-client", ClientSecret: "fixture-client-secret", RefreshToken: "fixture-refresh-" + label, AccessToken: "fixture-access-" + label, ExpiresAt: time.Now().Add(4 * time.Hour).UnixMilli(), Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"}}, nil
	})
	options := app.Options{CLILoginHelper: login, CLIRefresher: toolsGoogleRefresh(func(context.Context, clicredential.Google) (clicredential.Google, error) {
		refreshes.Add(1)
		return clicredential.Google{}, fmt.Errorf("unexpected refresh in captured-valid-grant fixture")
	})}
	s, root, dump := newToolsRuntimeApp(t, options, func(cfg *config.Config) {
		// Each fakePi writes only its private process metadata to a unique file,
		// so concurrent normal/task processes cannot overwrite one another's dump.
		wrapper := filepath.Join(filepath.Dir(cfg.WorkRoot), "scope-fakepi")
		quoted := "'" + strings.ReplaceAll(cfg.PiBin, "'", "'\\''") + "'"
		script := "#!/bin/sh\nexport FAKEPI_DUMP=\"${FAKEPI_DUMP}.$$\"\nexec " + quoted + " \"$@\"\n"
		if e := os.WriteFile(wrapper, []byte(script), 0700); e != nil {
			t.Fatal(e)
		}
		cfg.PiBin = wrapper
	})
	a := s.a
	ctx := context.Background()
	path, agent := s.createAgent("Google固定授权")
	s.must(201, "POST", "/projects/"+agent.ProjectID.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "fixture-model", "credential": map[string]any{"type": "api_key", "key": "synthetic-only"}})
	if _, e := a.Store.SetProjectManager(ctx, *agent.ProjectID, agent.ID); e != nil {
		t.Fatal(e)
	}
	loginAccount := func(name string) cm.Account {
		id := uuid.New()
		s.must(202, "POST", "/projects/"+agent.ProjectID.String()+"/cli-auth/operations", map[string]any{"operationId": id, "cli": "gcloud", "authType": "google_user", "name": name})
		deadline := time.Now().Add(5 * time.Second)
		for {
			state := decode[cm.CLIAuthState](t, s.must(200, "GET", "/projects/"+agent.ProjectID.String()+"/cli-auth/operations/"+id.String(), nil))
			if state.Operation.Status == "succeeded" && state.Account != nil {
				return *state.Account
			}
			if time.Now().After(deadline) {
				t.Fatal("synthetic App login not committed")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	accountA := loginAccount("A")
	if e := a.Store.AddAgentCLI(ctx, agent.ID, "gcloud"); e != nil {
		t.Fatal(e)
	}
	s.must(202, "POST", path+"/messages", map[string]string{"text": "!text=initial", "whenBusy": "queue"})
	toolsWaitIdle(t, s, path)
	normalA := toolsScopeDump(t, dump, agent.ID)
	if normalA.Env["LOOP_NOW_GCLOUD_GRANT_VERSION"] != accountA.CurrentVersionID.String() || toolsBrokerAccess(t, normalA) != "fixture-access-A" {
		t.Fatal("normal scope not bound to A")
	}
	// Actual generated gcloud PATH wrapper -> production Go entry -> latest
	// Python bootstrap/private SDK. Version is offline and cannot call a model.
	env := []string{}
	for k, v := range normalA.Env {
		env = append(env, k+"="+v)
	}
	profile := filepath.Join(root, "gcloud-deny-network.sb")
	os.WriteFile(profile, []byte("(version 1)(allow default)(deny network*)"), 0600)
	cmd := exec.Command("/usr/bin/sandbox-exec", "-f", profile, "/bin/sh", "-c", "gcloud --version")
	cmd.Env = env
	cmd.Dir = normalA.Cwd
	out, e := cmd.CombinedOutput()
	valid := e == nil && bytes.Contains(out, []byte("Google Cloud SDK 568.0.0"))
	clear(out)
	if !valid {
		t.Fatal("controlled private gcloud PATH entry failed")
	}
	taskRaw, e := a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: agent.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "Google原授权任务", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "检查", AgentID: agent.ID, Inputs: map[string]workflow.Source{}, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{{Name: "answer", Type: "string", Required: true}}}}, Edges: []workflow.Edge{}}})
	if e != nil {
		t.Fatal(e)
	}
	task := decode[taskmodel.Detail](t, taskRaw)
	deadline := time.Now().Add(15 * time.Second)
	for {
		detail, e := a.Tasks.Get(ctx, auth.Principal{Kind: auth.KindConsole}, task.Task.ID)
		if e != nil {
			t.Fatal(e)
		}
		if detail.Task.Status == "blocked" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake task did not settle")
		}
		time.Sleep(10 * time.Millisecond)
	}
	instances, e := a.Store.TaskInstances(ctx, task.Task.ID)
	if e != nil || len(instances) != 1 {
		t.Fatal("task capture missing")
	}
	inst := instances[0]
	taskA := toolsScopeDump(t, dump, inst.ID)
	if toolsBrokerAccess(t, taskA) != "fixture-access-A" {
		t.Fatal("task broker did not bind A")
	}
	toolsWaitIdle(t, s, path)
	accepted := decode[struct {
		Run apitypes.Run `json:"run"`
	}](t, s.must(202, "POST", path+"/messages", map[string]string{"text": "!text=current_A !sleep=500ms", "whenBusy": "queue"}))
	// Task preparation intentionally freezes the earlier normal Pi. A new
	// normal process performs the real SDK/Python readiness checks again, so
	// wait for this exact accepted run within the existing preparation budget.
	deadline = time.Now().Add(30 * time.Second)
	for {
		r, e := a.Store.GetRun(ctx, accepted.Run.ID)
		if e != nil {
			t.Fatal("accepted normal run lookup failed")
		}
		if r.Status == "running" {
			break
		}
		if r.EndedAt != nil || time.Now().After(deadline) {
			t.Fatalf("normal execution did not start: status=%s phase=%s errorCode=%s", r.Status, r.Phase, r.ErrorCode)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// FreezeForFork closed the initial broker; compare the newly dispatched
	// normal scope, while retaining the independent task scope broker.
	normalA = toolsScopeDump(t, dump, agent.ID)
	if normalA.Env["LOOP_NOW_GCLOUD_GRANT_VERSION"] != accountA.CurrentVersionID.String() || toolsBrokerAccess(t, normalA) != "fixture-access-A" {
		t.Fatal("current normal scope did not retain A")
	}
	accountB := loginAccount("B")
	selections := decode[cm.Selections](t, s.must(200, "GET", path+"/credential-selections", nil))
	s.must(200, "PUT", path+"/credential-selections", map[string]any{"purpose": "cli", "cli": "gcloud", "accountId": accountB.ID, "expectedConfigVersion": selections.ConfigVersion})
	if toolsBrokerAccess(t, normalA) != "fixture-access-A" || toolsBrokerAccess(t, taskA) != "fixture-access-A" {
		t.Fatal("current normal/task switched account before next dispatch")
	}
	toolsWaitIdle(t, s, path)
	s.must(202, "POST", path+"/messages", map[string]string{"text": "!text=nextB", "whenBusy": "queue"})
	toolsWaitIdle(t, s, path)
	normalB := toolsScopeDump(t, dump, agent.ID)
	if normalB.Env["LOOP_NOW_GCLOUD_GRANT_VERSION"] != accountB.CurrentVersionID.String() || toolsBrokerAccess(t, normalB) != "fixture-access-B" {
		t.Fatal("next normal dispatch did not bind B")
	}
	captured, e := a.Tasks.Source.ExecutionSnapshot(ctx, inst.ID)
	if e != nil || captured.GoogleBinding == nil || captured.GoogleBinding.AccountID != accountA.ID || captured.GoogleBinding.VersionID != *accountA.CurrentVersionID || toolsBrokerAccess(t, taskA) != "fixture-access-A" {
		t.Fatal("old task grant retrofitted current B")
	}
	if _, exists := captured.CLIPayloads["gcloud"]; exists || bytes.Contains(inst.Snapshot, []byte("fixture-refresh-A")) {
		t.Fatal("task copied Google refresh secret into a static payload")
	}
	if e = os.MkdirAll(filepath.Join(normalB.Env["HOME"], ".loop-now/gcloud/config"), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(normalB.Env["HOME"], ".loop-now/gcloud/config/credentials.db"), []byte("truncated-cache"), 0600); e != nil {
		t.Fatal(e)
	}
	sessionBefore, e := a.Store.GetAgentSession(ctx, agent.ID)
	if e != nil {
		t.Fatal(e)
	}
	s.must(202, "POST", path+"/messages", map[string]string{"text": "!text=rebuildB", "whenBusy": "queue"})
	toolsWaitIdle(t, s, path)
	if toolsBrokerAccess(t, normalB) != "fixture-access-B" {
		t.Fatal("cache reconstruction changed grant")
	}
	sessionAfter, e := a.Store.GetAgentSession(ctx, agent.ID)
	if e != nil || sessionAfter.SessionFile != sessionBefore.SessionFile {
		t.Fatal("cache reconstruction replaced session")
	}
	if _, e = os.Stat(filepath.Join(normalB.Env["HOME"], ".loop-now/gcloud/config/credentials.db")); !os.IsNotExist(e) {
		t.Fatal("mutable SDK cache was not reset")
	}
	if logins.Load() != 2 || refreshes.Load() != 0 {
		t.Fatal("valid captured grants triggered unexpected auth lifecycle")
	}
	if _, e = a.Tasks.Cancel(ctx, auth.Principal{Kind: auth.KindConsole}, task.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()}); e != nil {
		t.Fatal(e)
	}
}
