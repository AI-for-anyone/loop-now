package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"loop-now/internal/server/admincli"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/app"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/config"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/dbcli"
	"loop-now/internal/server/preflight"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
	"loop-now/internal/testutil/fakepibin"
	"loop-now/internal/testutil/loopctlfake"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/redistest"
)

// The production installer reexecs os.Executable. In this test executable only,
// delegate that hidden entry to the real production admin dispatcher; normal
// tests and the actual loop-server binary use their original entry points.
func init() {
	if len(os.Args) > 1 && (os.Args[1] == "internal-db-cli" || os.Args[1] == "internal-gcloud-cli") {
		os.Exit(admincli.Run(context.Background(), os.Args[1:], admincli.Options{Getenv: os.Getenv, Stdout: os.Stdout, Stderr: os.Stderr}))
	}
}

type dbLoopbackTransport struct {
	base     http.RoundTripper
	url      string
	archives map[string]string
}

func (t dbLoopbackTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	key, ok := t.archives[r.URL.String()]
	if !ok {
		return nil, fmt.Errorf("unexpected tool download rejected")
	}
	clone := r.Clone(r.Context())
	parsed, _ := http.NewRequest("GET", t.url+"/"+key, nil)
	clone.URL = parsed.URL
	clone.Host = parsed.URL.Host
	return t.base.RoundTrip(clone)
}
func newToolsRuntimeApp(t *testing.T, options app.Options, customize func(*config.Config)) (*server, string, string) {
	t.Helper()
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("reviewed bundle/native platform is darwin-arm64 only")
	}
	ctx := context.Background()
	url := pgtest.URL(t)
	conn, e := pgx.Connect(ctx, url)
	if e != nil {
		t.Fatal("TEST PG unavailable")
	}
	schema := "db_runtime_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = conn.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		defer conn.Close(ctx)
		if _, e := conn.Exec(ctx, `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`); e != nil {
			t.Error("schema cleanup failed")
		}
	})
	redisURL := redistest.URL(t)
	opts, e := redis.ParseURL(redisURL)
	if e != nil || opts.DB != 10 {
		t.Fatal("requires TEST Redis DB10")
	}
	rc := redis.NewClient(opts)
	prefix := "loopnow:db-runtime:" + uuid.NewString() + ":"
	t.Cleanup(func() {
		if e := redistest.DeletePrefix(ctx, rc, prefix); e != nil {
			t.Error("Redis prefix cleanup failed")
		}
		rc.Close()
	})
	root := t.TempDir()
	dump := filepath.Join(root, "private-dump.json")
	t.Setenv("FAKEPI_DUMP", dump)
	probe, e := preflight.Resolve(ctx, preflight.Options{PiBin: "pi", PiVersion: "1.1.0", NodeBin: "node", WorkRoot: filepath.Join(root, "probe"), HomeDirs: []string{"/nonexistent-home"}})
	if e != nil {
		t.Fatal("fixed local catalog prerequisite unavailable")
	}
	values := map[string]string{config.EnvDatabaseURL: url, config.EnvRedisURL: redisURL, config.EnvListenAddr: "127.0.0.1:0", config.EnvWorkRoot: filepath.Join(root, "work"), config.EnvRedisKeyPrefix: prefix, config.EnvPiBin: fakepibin.Path(t), config.EnvPiAIDir: probe.PiAIDir, config.EnvNodeBin: probe.NodeBin, config.EnvLoopctlDistDir: loopctlfake.Dir(t), config.EnvExecEnvPassthrough: "FAKEPI_DUMP", config.EnvPiStartTimeout: "10s", config.EnvPiStopGrace: "1s", config.EnvCLIInstallTimeout: "1m"}
	cfg, e := config.Load(func(k string) string { return values[k] })
	if e != nil {
		t.Fatal("isolated config invalid")
	}
	downloads, e := filepath.Abs("../../../.local-build/cli-installation-proof/downloads")
	if e != nil {
		t.Fatal(e)
	}
	packages := map[string]string{"mysql": "mysql-8.4.9-macos15-arm64.tar.gz", "sdk": "google-cloud-cli-568.0.0-darwin-arm.tar.gz", "python": "python-3.13.7-macos11.tar.gz"}
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := packages[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(downloads, name))
	}))
	t.Cleanup(vendor.Close)
	client := &http.Client{Transport: dbLoopbackTransport{http.DefaultTransport, vendor.URL, map[string]string{"https://cdn.mysql.com/archives/mysql-8.4/mysql-8.4.9-macos15-arm64.tar.gz": "mysql", "https://storage.googleapis.com/cloud-sdk-release/google-cloud-cli-568.0.0-darwin-arm.tar.gz": "sdk", "https://dl.google.com/dl/cloudsdk/channels/rapid/python-3.13.7-macos11.tar.gz": "python"}}}
	if customize != nil {
		customize(cfg)
	}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	options.SearchPath = schema
	options.Listener = ln
	options.HomeDirs = []string{"/nonexistent-home"}
	options.CLIHTTPClient = client
	a, e := app.Build(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), options)
	if e != nil {
		t.Fatal("actual isolated App build failed", e)
	}
	go a.Serve()
	s := &server{t: t, a: a, url: "http://" + a.Addr() + "/api/v1"}
	t.Cleanup(s.stop)
	return s, root, dump
}
func TestDatabaseActualAppCapturedScopeAndCacheRebuild(t *testing.T) {
	s, root, dump := newToolsRuntimeApp(t, app.Options{}, nil)
	a := s.a
	var e error
	ctx := context.Background()
	path, agent := s.createAgent("数据库运行快照")
	// Only synthetic model key; all actual execution is fakePi with network-free output.
	s.must(201, "POST", "/projects/"+agent.ProjectID.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "fixture-model", "credential": map[string]any{"type": "api_key", "key": "synthetic-only"}})
	if e = a.Store.AddAgentCLI(ctx, agent.ID, "mysql"); e != nil {
		t.Fatal(e)
	}
	accounts := []cm.Account{}
	for _, name := range []string{"A", "B"} {
		v := decode[cm.Account](t, s.must(201, "POST", "/projects/"+agent.ProjectID.String()+"/credential-accounts", map[string]any{"purpose": "cli", "cli": "mysql", "name": name, "credential": map[string]any{"type": "cli", "fields": map[string]string{"host": "127.0.0.1", "port": "1", "database": "fixture-" + name, "user": "fixture-" + name, "password": "synthetic-" + name, "sslMode": "DISABLED"}}}))
		accounts = append(accounts, v)
	}
	state := decode[cm.DBConnections](t, s.must(200, "GET", path+"/db-connections/mysql", nil))
	s.must(200, "PUT", path+"/db-connections/mysql", map[string]any{"expectedConfigVersion": state.ConfigVersion, "accountIds": []uuid.UUID{accounts[0].ID, accounts[1].ID}})
	s.must(202, "POST", path+"/messages", map[string]string{"text": "!text=offline !sleep=100ms", "whenBusy": "queue"})
	s.waitIdle(path)
	type processDump struct {
		Env map[string]string `json:"env"`
		Cwd string            `json:"cwd"`
	}
	var process processDump
	raw, e := os.ReadFile(dump)
	if e != nil || json.Unmarshal(raw, &process) != nil {
		t.Fatal("private execution dump missing")
	}
	clear(raw)
	home := process.Env["HOME"]
	manifest := filepath.Join(home, dbcli.ConfigPath("mysql"))
	readConnections := func() []dbcli.Connection {
		t.Helper()
		b, e := os.ReadFile(manifest)
		if e != nil {
			t.Fatal("captured manifest missing")
		}
		defer clear(b)
		var v []dbcli.Connection
		if json.Unmarshal(b, &v) != nil {
			t.Fatal("captured manifest invalid")
		}
		return v
	}
	captured := readConnections()
	if len(captured) != 2 {
		t.Fatal("same CLI set overwritten")
	}
	oldVersions := map[uuid.UUID]uuid.UUID{}
	for _, c := range captured {
		oldVersions[c.AccountID] = c.VersionID
	}
	env := []string{}
	for k, v := range process.Env {
		env = append(env, k+"="+v)
	}
	// An actual Skill shell invokes the production generated PATH entry. No
	// connection/secret is printed; discovery JSON contains only captured IDs.
	skill := filepath.Join(root, "skill.sh")
	os.WriteFile(skill, []byte("#!/bin/sh\nexec mysql --loop-list-connections\n"), 0700)
	cmd := exec.Command("/bin/sh", skill)
	cmd.Env = env
	cmd.Dir = process.Cwd
	out, e := cmd.Output()
	if e != nil {
		t.Fatal("production wrapper discovery failed", e)
	}
	var infos []dbcli.Info
	if json.Unmarshal(out, &infos) != nil || len(infos) != 2 {
		t.Fatal("safe discovery missing captured connections")
	}
	clear(out)
	for _, info := range infos {
		if oldVersions[info.AccountID] != info.VersionID {
			t.Fatal("discovery read current rather than capture")
		}
	}
	// The captured cache is reconstructible from the same in-memory/PG source on
	// a new dispatch; rebuilding it must not create a new session or switch IDs.
	sessionBefore, e := a.Store.GetAgentSession(ctx, agent.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(manifest, []byte("truncated"), 0600); e != nil {
		t.Fatal(e)
	}
	s.must(202, "POST", path+"/messages", map[string]string{"text": "!text=rebuild", "whenBusy": "queue"})
	s.waitIdle(path)
	rebuilt := readConnections()
	if len(rebuilt) != 2 {
		t.Fatal("cache not rebuilt")
	}
	for _, c := range rebuilt {
		if oldVersions[c.AccountID] != c.VersionID {
			t.Fatal("cache rebuild changed captured version")
		}
	}
	sessionAfter, e := a.Store.GetAgentSession(ctx, agent.ID)
	if e != nil {
		t.Fatal(e)
	}
	if sessionAfter.SessionFile != sessionBefore.SessionFile {
		t.Fatal("cache rebuild created another session")
	}

	// Create through the actual production Tasks.Service (separate task/runtime
	// UUIDs), not a hand-written snapshot fixture. FakePi produces no output so
	// the node blocks while keeping its frozen execution scope for inspection.
	if _, e = a.Store.SetProjectManager(ctx, *agent.ProjectID, agent.ID); e != nil {
		t.Fatal(e)
	}
	taskRaw, e := a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: agent.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "数据库固定快照", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "检查", AgentID: agent.ID, Inputs: map[string]workflow.Source{}, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{{Name: "answer", Type: "string", Required: true}}}}, Edges: []workflow.Edge{}}})
	if e != nil {
		t.Fatal("actual task creation failed", e)
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
			t.Fatal("fake task not settled")
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.waitIdle(path)
	instances, e := a.Store.TaskInstances(ctx, task.Task.ID)
	if e != nil || len(instances) != 1 || instances[0].ID == task.Task.ID {
		t.Fatal("real separate task/runtime capture missing")
	}
	frozen, e := a.Tasks.Source.ExecutionSnapshot(ctx, instances[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	frozenPayload := frozen.CLIPayloads["mysql"].Files[0].Content
	var frozenConnections []dbcli.Connection
	if json.Unmarshal(frozenPayload, &frozenConnections) != nil || len(frozenConnections) != 2 {
		t.Fatal("task connection set not captured")
	}
	// While normal work is actually executing, replace A's version and shrink
	// the next authorization set to B. Neither the current cache nor task changes.
	s.must(202, "POST", path+"/messages", map[string]string{"text": "!text=current !sleep=500ms", "whenBusy": "queue"})
	deadline = time.Now().Add(5 * time.Second)
	for {
		st := decode[apitypes.AgentStatus](t, s.must(200, "GET", path+"/status", nil))
		if st.Busy && st.CurrentRun != nil && st.CurrentRun.Status == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("normal run did not begin")
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.must(200, "PUT", "/projects/"+agent.ProjectID.String()+"/credential-accounts/"+accounts[0].ID.String()+"/credential", map[string]any{"expectedRevision": accounts[0].Revision, "credential": map[string]any{"type": "cli", "fields": map[string]string{"host": "127.0.0.1", "port": "1", "database": "fixture-A", "user": "fixture-A", "password": "synthetic-replaced-A", "sslMode": "DISABLED"}}})
	state = decode[cm.DBConnections](t, s.must(200, "GET", path+"/db-connections/mysql", nil))
	s.must(200, "PUT", path+"/db-connections/mysql", map[string]any{"expectedConfigVersion": state.ConfigVersion, "accountIds": []uuid.UUID{accounts[1].ID}})
	current := readConnections()
	if len(current) != 2 {
		t.Fatal("current run authorization changed")
	}
	for _, c := range current {
		if oldVersions[c.AccountID] != c.VersionID {
			t.Fatal("current run version changed")
		}
	}
	s.waitIdle(path)
	s.must(202, "POST", path+"/messages", map[string]string{"text": "!text=nextB", "whenBusy": "queue"})
	s.waitIdle(path)
	next := readConnections()
	if len(next) != 1 || next[0].AccountID != accounts[1].ID {
		t.Fatal("next normal dispatch did not apply B set")
	}
	again, e := a.Tasks.Source.ExecutionSnapshot(ctx, instances[0].ID)
	if e != nil || !bytes.Equal(again.CLIPayloads["mysql"].Files[0].Content, frozenPayload) {
		t.Fatal("existing task retrofitted current project connections")
	}
	if _, e = a.Tasks.Cancel(ctx, auth.Principal{Kind: auth.KindConsole}, task.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()}); e != nil {
		t.Fatal("task fixture cleanup failed", e)
	}
	s.waitIdle(path)
	// Explicit empty authorization cannot fallback to either project account.
	state = decode[cm.DBConnections](t, s.must(200, "GET", path+"/db-connections/mysql", nil))
	s.must(200, "PUT", path+"/db-connections/mysql", map[string]any{"expectedConfigVersion": state.ConfigVersion, "accountIds": []uuid.UUID{}})
	s.must(202, "POST", path+"/messages", map[string]string{"text": "!text=empty", "whenBusy": "queue"})
	s.waitIdle(path)
	if len(readConnections()) != 0 {
		t.Fatal("empty set injected project accounts")
	}
}
