package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/loopctl/cli"
	"loop-now/internal/loopctl/commands"
	"loop-now/internal/loopctl/conn"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/store"
)

func TestProjectAgentStatusHTTPAndCLI(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	// No dispatch is needed: the endpoint must project persisted status, never
	// start Pi or consume queued work. All rows belong to this TEST schema.
	if err := e.conv.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	makeAgent := func(name string) apitypes.Agent {
		return decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": name, "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	}
	source, target := makeAgent("observer"), makeAgent("worker")
	observer := e.agentCaller(source.ID)
	statusPath := func(ref string) string { return "/agents/status?agent=" + url.QueryEscape(ref) }
	refs := []string{target.ID.String(), "worker", e.dc.Project.Name + "/worker", e.dc.Organization.Name + "/" + e.dc.Project.Name + "/worker", source.ID.String()}
	for _, ref := range refs {
		got := decode[apitypes.AgentStatus](t, observer.expect(200, "GET", statusPath(ref), nil))
		if got.Busy || got.CurrentRun != nil || got.Session.Epoch != 1 || got.Session.State != "none" {
			t.Fatal("incorrect idle projection", got)
		}
	}
	runCLI := func(c caller, args ...string) (int, []byte) {
		var out bytes.Buffer
		vars := map[string]string{conn.EnvAgentURL: c.base, conn.EnvAgentToken: c.token}
		code := cli.Run(ctx, commands.Registry(), args, &cli.System{Stdout: &out, Stdin: strings.NewReader(""), Getenv: func(k string) string { return vars[k] }, Version: "status-test"})
		return code, out.Bytes()
	}
	for _, ref := range refs {
		code, raw := runCLI(observer, "agent", "status", ref)
		if code != 0 {
			t.Fatalf("real CLI→HTTP→PG failed %d: %s", code, raw)
		}
		var envelope struct {
			Data apitypes.AgentStatus `json:"data"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Data.AgentID == uuid.Nil {
			t.Fatal("invalid CLI projection", err)
		}
	}
	for _, argv := range [][]string{{"agent", "get", target.ID.String()}, {"agent", "history", target.ID.String()}, {"agent", "list", "--platform"}, {"agent", "session", "reset", target.ID.String(), "--yes"}} {
		code, raw := runCLI(observer, argv...)
		if code == 0 || !bytes.Contains(raw, []byte("command_not_allowed")) {
			t.Fatalf("management allowed: %v %d %s", argv, code, raw)
		}
	}

	// Busy + queue status is exactly the existing normal status (no message text).
	_, active, err := e.st.InsertMessageAndRun(ctx, store.NewRun{AgentID: target.ID, RunID: uuid.New(), Text: "SECRET_BODY_NOT_STATUS", Start: true, InstanceID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = e.st.InsertMessageAndRun(ctx, store.NewRun{AgentID: target.ID, RunID: uuid.New(), Text: "QUEUED_BODY_NOT_STATUS", InstanceID: uuid.New()}); err != nil {
		t.Fatal(err)
	}
	raw := observer.expect(200, "GET", statusPath(target.ID.String()), nil)
	busy := decode[apitypes.AgentStatus](t, raw)
	if !busy.Busy || busy.CurrentRun == nil || busy.CurrentRun.ID != active.ID || busy.Queued.Normal != 1 || bytes.Contains(raw, []byte("BODY_NOT_STATUS")) {
		t.Fatal("busy/queue projection mismatch", string(raw))
	}
	canonical := e.expect(200, "GET", "/agents/"+target.ID.String()+"/status", nil)
	if !bytes.Equal(raw, canonical) {
		t.Fatal("new endpoint changed normal status projection")
	}
	sleepy := makeAgent("sleepy")
	_, sleepRun, err := e.st.InsertMessageAndRun(ctx, store.NewRun{AgentID: sleepy.ID, RunID: uuid.New(), Text: "sleep", Start: true, InstanceID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.st.SetRunRunning(ctx, sleepRun.ID, 1); err != nil {
		t.Fatal(err)
	}
	receipt, err := e.st.RequestSleep(ctx, auth.Principal{Kind: auth.KindAgent, ID: sleepy.ID}, sleepmodel.Input{ActionID: uuid.New(), RunID: sleepRun.ID, Seconds: 120}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.st.RecordSessionFile(ctx, sleepy.ID, "TEST-session-not-exposed.jsonl"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = e.st.FinishRun(ctx, sleepRun.ID, store.RunEnd{Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	raw = observer.expect(200, "GET", statusPath(sleepy.ID.String()), nil)
	sleeping := decode[apitypes.AgentStatus](t, raw)
	if sleeping.Busy || sleeping.Sleep == nil || sleeping.Sleep.ID != receipt.Sleep.ID || sleeping.Sleep.State != "sleeping" || bytes.Contains(raw, []byte("TEST-session-not-exposed")) {
		t.Fatal("sleep/session projection", string(raw))
	}
	// HTTP authorization and precise rejection of unsupported target forms.
	otherProject, err := e.st.CreateProject(ctx, e.dc.Organization.ID, "other")
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := e.st.CreateAgent(ctx, store.ProjectScope(otherProject.ID), "worker", "anthropic", "claude-x", "")
	if err != nil {
		t.Fatal(err)
	}
	platform := e.platformAgent("platform")
	for _, ref := range []string{outsider.ID.String(), "other/worker", e.dc.Organization.Name + "/other/worker", platform.ID.String()} {
		observer.expectError(403, "forbidden", "GET", statusPath(ref), nil)
	}
	e.agentCaller(platform.ID).expectError(403, "forbidden", "GET", statusPath(target.ID.String()), nil)
	observer.expect(422, "GET", "/agents/status", nil, "X-Contract-Invalid-Request", "1")
	observer.expectError(422, "validation_failed", "GET", statusPath("worker#"+uuid.NewString()), nil)
	observer.expectError(422, "validation_failed", "GET", statusPath("a/b/c/d"), nil)
	observer.expectError(404, "not_found", "GET", statusPath(uuid.NewString()), nil)
	observer.expectError(404, "not_found", "GET", statusPath("absent"), nil)
	deleted := makeAgent("deleted")
	e.expect(204, "DELETE", "/agents/"+deleted.ID.String(), nil)
	observer.expectError(404, "not_found", "GET", statusPath(deleted.ID.String()), nil)
	observer.expectError(404, "not_found", "GET", statusPath("deleted"), nil)
	// Same project and Agent name in another organization must never match short
	// forms or be treated as authorized by project-name equality.
	org := uuid.New()
	if _, err = e.st.Pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,'other org')`, org); err != nil {
		t.Fatal(err)
	}
	sameNameProject, err := e.st.CreateProject(ctx, org, e.dc.Project.Name)
	if err != nil {
		t.Fatal(err)
	}
	sameName, err := e.st.CreateAgent(ctx, store.ProjectScope(sameNameProject.ID), "worker", "anthropic", "claude-x", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{sameName.ID.String(), "other org/" + e.dc.Project.Name + "/worker"} {
		observer.expectError(403, "forbidden", "GET", statusPath(ref), nil)
	}
	// A non-default organization caller resolves two-part addresses by its own org.
	otherObserver := e.agentCaller(sameName.ID)
	got := decode[apitypes.AgentStatus](t, otherObserver.expect(200, "GET", statusPath(e.dc.Project.Name+"/worker"), nil))
	if got.AgentID != sameName.ID {
		t.Fatal("two-part address used default organization")
	}
	_, secret := e.identity("external-status")
	e.external(secret).expect(200, "GET", "/agents/"+target.ID.String()+"/status", nil)
	// No Agent route for directory/config/history/reset or the old status route.
	rawCaller := observer
	rawCaller.client = e.srv.Client()
	for _, path := range []string{"/agents", "/agents/" + target.ID.String(), "/agents/" + target.ID.String() + "/messages", "/agents/" + target.ID.String() + "/status"} {
		rawCaller.expect(404, "GET", path, nil)
	}
	for _, ref := range []string{target.ID.String(), "worker"} {
		code, raw := runCLI(observer, "agent", "status", ref+"#"+uuid.NewString())
		if code == 0 || !bytes.Contains(raw, []byte("validation_failed")) {
			t.Fatalf("CLI task target accepted %d %s", code, raw)
		}
	}
	// Deleted callers cannot use their former project through a retained token.
	e.expect(204, "DELETE", "/agents/"+source.ID.String(), nil)
	observer.expectError(401, "unauthenticated", "GET", statusPath(target.ID.String()), nil)
}

func TestTaskTokenProjectAgentStatusUsesBaseAgent(t *testing.T) {
	e, _, d := taskEnv(t)
	node := d.Nodes[0]
	taskCaller := e.agentCaller(node.RuntimeID)
	got := decode[apitypes.AgentStatus](t, taskCaller.expect(200, "GET", "/agents/status?agent="+node.AgentID.String(), nil))
	if got.AgentID != node.AgentID || got.AgentID == node.RuntimeID || got.Busy {
		t.Fatal("task token did not return base normal instance", got)
	}
}
