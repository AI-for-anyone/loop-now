package app_test

// These opt-in tests exercise the real App, isolated PG/Redis, production helper
// IPC/coordinator and fakePi RPC. The helper fixture imports only node:fs/readline;
// it never imports a provider, makes HTTP calls, or reads a host login.
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/app"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/config"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
)

type projectCredentialsRuntime struct {
	t                         *testing.T
	w                         *world
	s                         *server
	helper                    *modelauth.Helper
	control, refreshLog, dump string
	project                   uuid.UUID
	addresses                 []string
	canonicalRoot             string
}

func projectCredentialsRuntimeNew(t *testing.T) *projectCredentialsRuntime {
	t.Helper()
	if os.Getenv("LOOP_NOW_PROJECT_CREDENTIALS_RUNTIME_INTEGRATION") != "1" {
		t.Skip("explicit isolated project credential runtime integration only")
	}
	db, e := url.Parse(os.Getenv("LOOP_NOW_TEST_DATABASE_URL"))
	if e != nil || !strings.HasSuffix(db.Path, "_test") {
		t.Fatal("requires dedicated TEST database")
	}
	rd, e := url.Parse(os.Getenv("LOOP_NOW_TEST_REDIS_URL"))
	if e != nil || rd.Path != "/10" {
		t.Fatal("requires dedicated TEST Redis DB10")
	}
	var resource *projectCredentialsRuntime
	// Registered before world/temp cleanup so the audit runs after all owners.
	t.Cleanup(func() {
		if resource != nil {
			resource.auditCleanup()
		}
	})
	w := newWorld(t)
	w.cfg.PiMaxProcesses = 8
	w.cfg.SleepScanInterval = 50 * time.Millisecond
	w.cfg.SleepScanBatch = 100
	w.cfg.AuthLoginTimeout = 5 * time.Second
	w.cfg.AuthRefreshTimeout = 5 * time.Second
	dir := t.TempDir()
	r := &projectCredentialsRuntime{t: t, w: w, control: filepath.Join(dir, "control.json"), refreshLog: filepath.Join(dir, "refresh-count"), dump: filepath.Join(dir, "private-process-dump")}
	resource = r
	r.setControl("account-A", false)
	// Only these private files hold synthetic credentials/ephemeral runtime tokens;
	// no dump or protocol frame is copied to persistent verification artifacts.
	t.Setenv("FAKEPI_DUMP", r.dump)
	w.cfg.ExecEnvPassthrough = append(w.cfg.ExecEnvPassthrough, "FAKEPI_DUMP", "FAKEPI_PROMPT_DELAY")
	entry := filepath.Join(dir, "offline-helper.mjs")
	source := `import fs from 'node:fs'; import readline from 'node:readline';
const control=CONTROL, log=LOG;
fs.writeSync(4, JSON.stringify({type:'ready',protocolVersion:1,expectedPiVersion:'1.1.0'})+'\n');
for await (const line of readline.createInterface({input:fs.createReadStream(null,{fd:3})})) {
 const c=JSON.parse(line); if(c.type!=='login'&&c.type!=='refresh') continue;
 const ctl=JSON.parse(fs.readFileSync(control,'utf8'));
 if(c.type==='refresh') fs.appendFileSync(log,'refresh\n',{mode:0o600});
 const failed=c.type==='refresh'&&ctl.failRefresh;
 const credential={type:'oauth',access:'synthetic-runtime-access-'+Date.now(),refresh:'synthetic-runtime-refresh',accountId:c.type==='refresh'?c.credential.accountId:ctl.account,expires:Date.now()+3600000};
 fs.writeSync(4,JSON.stringify(failed?{type:'result',id:c.id,status:'failed',code:'auth_failed'}:{type:'result',id:c.id,status:'succeeded',credential})+'\n');
 process.exit(0);
}`
	controlJSON, _ := json.Marshal(r.control)
	logJSON, _ := json.Marshal(r.refreshLog)
	source = strings.ReplaceAll(strings.ReplaceAll(source, "CONTROL", string(controlJSON)), "LOG", string(logJSON))
	if e := os.WriteFile(entry, []byte(source), 0600); e != nil {
		t.Fatal(e)
	}
	// Production helper process wrapper, with an explicitly offline entry only.
	r.helper = &modelauth.Helper{Options: modelauth.HelperOptions{NodeBin: filepath.Join(os.Getenv("HOME"), ".local/node-v24.21.0/bin/node"), Entry: entry, WorkRoot: dir, BasePath: "/usr/bin:/bin", PiPackageRoot: w.piAIDir, Timeout: 5 * time.Second, StopGrace: time.Second}}
	r.start()
	t.Cleanup(func() {
		if r.s != nil {
			r.s.stop()
			r.s = nil
		}
	})
	dc := decode[apitypes.Context](t, r.s.must(200, "GET", "/context", nil))
	r.project = dc.Project.ID
	return r
}
func (r *projectCredentialsRuntime) setControl(account string, fail bool) {
	r.t.Helper()
	b, _ := json.Marshal(map[string]any{"account": account, "failRefresh": fail})
	if e := os.WriteFile(r.control, b, 0600); e != nil {
		r.t.Fatal(e)
	}
}
func (r *projectCredentialsRuntime) start() {
	r.t.Helper()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		r.t.Fatal(e)
	}
	a, e := app.Build(context.Background(), &r.w.cfg, config.NewLogger(io.Discard, r.w.cfg.LogFormat, r.w.cfg.LogLevel), app.Options{SearchPath: r.w.schema, Listener: ln, HomeDirs: []string{"/nonexistent-home"}, AuthLoginHelper: r.helper, AuthRefresher: r.helper})
	if e != nil {
		ln.Close()
		r.t.Fatal(e)
	}
	go func() { _ = a.Serve() }()
	r.s = &server{t: r.t, a: a, url: "http://" + a.Addr() + "/api/v1"}
	r.addresses = append(r.addresses, a.Addr())
	if root, e := filepath.EvalSymlinks(r.w.cfg.WorkRoot); e == nil {
		r.canonicalRoot = root
	} else {
		r.t.Fatal("owned root normalization failed")
	}
}
func (r *projectCredentialsRuntime) restart() { r.s.stop(); r.s = nil; r.start() }
func (r *projectCredentialsRuntime) agent(name string) (string, apitypes.Agent) {
	r.t.Helper()
	a := decode[apitypes.Agent](r.t, r.s.must(201, "POST", "/projects/"+r.project.String()+"/agents", map[string]any{"name": name, "model": map[string]string{"provider": "openai-codex", "modelId": "gpt-6.1-sol"}, "prompt": "offline runtime acceptance"}))
	return "/agents/" + a.ID.String(), a
}
func (r *projectCredentialsRuntime) login(label string, old *cm.Account) cm.Account {
	r.t.Helper()
	r.setControl(label, false)
	body := map[string]any{"provider": "openai-codex"}
	if old == nil {
		body["name"] = "runtime account " + label
	} else {
		body["accountId"] = old.ID
		body["expectedRevision"] = old.Revision
	}
	base := "/projects/" + r.project.String() + "/model-auth/operations"
	op := decode[cm.LoginState](r.t, r.s.must(202, "POST", base, body))
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		state := decode[cm.LoginState](r.t, r.s.must(200, "GET", base+"/"+op.Operation.ID.String(), nil))
		if state.Operation.Status == "succeeded" && state.Account != nil && state.Account.CurrentVersionID != nil {
			return *state.Account
		}
		if state.Operation.Status == "failed" {
			r.t.Fatalf("offline helper failed: %v", state.Operation.ErrorCode)
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.t.Fatal("offline helper login timeout")
	return cm.Account{}
}
func (r *projectCredentialsRuntime) cache(runtime uuid.UUID, account string) {
	r.t.Helper()
	path := filepath.Join(r.w.cfg.WorkRoot, "agents", runtime.String(), "pi", "auth.json")
	b, e := os.ReadFile(path)
	if e != nil {
		r.t.Fatal("runtime cache unreadable", e)
	}
	defer clear(b)
	var view map[string]struct {
		AccountID string `json:"accountId"`
	}
	if json.Unmarshal(b, &view) != nil || view["openai-codex"].AccountID != account {
		r.t.Fatal("runtime cache account mismatch")
	}
	st, e := os.Stat(path)
	if e != nil || st.Mode().Perm() != 0600 {
		r.t.Fatal("cache permissions not private")
	}
	st, e = os.Stat(filepath.Dir(path))
	if e != nil || st.Mode().Perm() != 0700 {
		r.t.Fatal("cache directory permissions not private")
	}
}
func (r *projectCredentialsRuntime) normalSession(id uuid.UUID) string {
	r.t.Helper()
	session, e := r.s.a.Store.GetAgentSession(context.Background(), id)
	if e != nil || session.SessionFile == "" {
		r.t.Fatal("native normal session missing", e)
	}
	return session.SessionFile
}
func (r *projectCredentialsRuntime) turn(path, text string) {
	r.t.Helper()
	r.s.must(202, "POST", path+"/messages", map[string]any{"text": text})
	r.s.waitIdle(path)
	replies := r.s.replies(path)
	if len(replies) == 0 || replies[len(replies)-1].Run.Status != "completed" {
		r.t.Fatal("expected fakePi completed turn")
	}
}
func (r *projectCredentialsRuntime) forceExpired(version uuid.UUID) int64 {
	r.t.Helper()
	ctx := context.Background()
	v, e := r.s.a.Store.GetModelAuthVersion(ctx, version)
	if e != nil {
		r.t.Fatal(e)
	}
	var c map[string]any
	if json.Unmarshal(v.Secret, &c) != nil {
		r.t.Fatal("synthetic credential decode")
	}
	c["expires"] = int64(1)
	changed, _ := json.Marshal(c)
	defer clear(changed)
	// TEST fault injection changes expiry, never generation/account/version. The
	// next real coordinator/helper refresh must advance generation itself.
	_, e = r.s.a.Store.Pool.Exec(ctx, `UPDATE model_auth_versions SET secret=$2 WHERE id=$1`, version, changed)
	if e != nil {
		r.t.Fatal(e)
	}
	return v.Generation
}
func projectCredentialsRuntimeNativeCounts(t *testing.T, path string) (int, int) {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(b)
	prompts, tools := 0, 0
	for _, line := range bytes.Split(b, []byte("\n")) {
		var v struct {
			Message struct {
				Role string `json:"role"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &v) == nil {
			switch v.Message.Role {
			case "user":
				prompts++
			case "toolResult":
				tools++
			}
		}
	}
	return prompts, tools
}

// Synthetic preinstalled executable prevents installer network access. This
// checks credential/permission wiring, not the official gh download/signature.
func (r *projectCredentialsRuntime) cli(path string) cm.Account {
	r.t.Helper()
	binary := filepath.Join(r.w.cfg.WorkRoot, "tools", "gh", "2.100.0", "bin", "gh")
	if e := os.MkdirAll(filepath.Dir(binary), 0700); e != nil {
		r.t.Fatal(e)
	}
	if e := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0755); e != nil {
		r.t.Fatal(e)
	}
	account := decode[cm.Account](r.t, r.s.must(201, "POST", "/projects/"+r.project.String()+"/credential-accounts", map[string]any{"purpose": "cli", "cli": "gh", "name": "runtime CLI A", "credential": map[string]any{"type": "cli", "fields": map[string]string{"token": "synthetic-cli-A"}}}))
	r.s.must(200, "PUT", path+"/clis/gh", nil)
	return account
}

func TestProjectCredentialsRuntimeNormalTransitions(t *testing.T) {
	r := projectCredentialsRuntimeNew(t)
	ctx := context.Background()
	a := r.login("account-A", nil)
	path, agent := r.agent("runtime-normal")
	r.cli(path)
	unprivilegedPath, _ := r.agent("runtime-no-cli-permission")
	r.turn(unprivilegedPath, "no-cli-permission")
	privateDump, e := os.ReadFile(r.dump)
	if e != nil {
		t.Fatal(e)
	}
	var unprivileged struct {
		Env map[string]string `json:"env"`
	}
	if json.Unmarshal(privateDump, &unprivileged) != nil {
		t.Fatal("private unprivileged dump decode")
	}
	clear(privateDump)
	if _, exists := unprivileged.Env["GH_TOKEN"]; exists {
		t.Fatal("project CLI credential injected without Agent permission")
	}
	r.s.must(202, "POST", path+"/messages", map[string]any{"text": "normal-current-A !sleep=1200ms !tool=normal-A"})
	r.s.waitRunning(path)
	pidA := r.s.a.Executor.Process(agent.ID).PID
	if pidA == 0 {
		t.Fatal("normal A process missing")
	}
	r.cache(agent.ID, "account-A")
	b := r.login("account-B", &a)
	if a.ID != b.ID || *a.CurrentVersionID == *b.CurrentVersionID {
		t.Fatal("relogin must preserve account ID and change grant")
	}
	if r.s.a.Executor.Process(agent.ID).PID != pidA {
		t.Fatal("relogin interrupted current turn")
	}
	r.cache(agent.ID, "account-A")
	status := decode[apitypes.AgentStatus](t, r.s.must(200, "GET", path+"/status", nil))
	if !status.Busy {
		t.Fatal("replacement did not occur during the current busy turn")
	}
	r.s.waitIdle(path)
	session := r.normalSession(agent.ID)
	r.turn(path, "normal-next-B !tool=normal-B")
	pidB := r.s.a.Executor.Process(agent.ID).PID
	if pidB == 0 || pidB == pidA {
		t.Fatal("next turn did not restart for new grant")
	}
	r.cache(agent.ID, "account-B")
	if r.normalSession(agent.ID) != session {
		t.Fatal("grant replacement replaced native session")
	}
	r.s.must(200, "PATCH", "/projects/"+r.project.String()+"/credential-accounts/"+b.ID.String(), map[string]any{"name": "renamed safe metadata", "expectedRevision": b.Revision})
	r.turn(path, "same-grant-after-rename")
	if r.s.a.Executor.Process(agent.ID).PID != pidB {
		t.Fatal("metadata rename restarted process")
	}
	generation := r.forceExpired(*b.CurrentVersionID)
	r.turn(path, "same-grant-after-refresh")
	after, e := r.s.a.Store.GetModelAuthVersion(ctx, *b.CurrentVersionID)
	if e != nil || after.Generation != generation+1 {
		t.Fatal("real helper/coordinator refresh did not commit generation")
	}
	if r.s.a.Executor.Process(agent.ID).PID != pidB {
		t.Fatal("generation rotation restarted process")
	}
	r.cache(agent.ID, "account-B")
	log, e := os.ReadFile(r.refreshLog)
	if e != nil || string(log) != "refresh\n" {
		t.Fatal("offline helper refresh count not exactly one")
	}
	raw, e := os.ReadFile(r.dump)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(raw)
	var dump struct {
		Env  map[string]string `json:"env"`
		Args []string          `json:"args"`
	}
	if json.Unmarshal(raw, &dump) != nil {
		t.Fatal("private dump decode")
	}
	for key, value := range dump.Env {
		if key == "OPENAI_API_KEY" || key == "ANTHROPIC_API_KEY" || strings.Contains(value, "synthetic-runtime-access") || strings.Contains(value, "synthetic-runtime-refresh") {
			t.Fatal("model secret leaked into environment")
		}
	}
	expectedHome, homeErr := filepath.EvalSymlinks(filepath.Join(r.w.cfg.WorkRoot, "agents", agent.ID.String(), "home"))
	actualHome, actualErr := filepath.EvalSymlinks(dump.Env["HOME"])
	if homeErr != nil || actualErr != nil || expectedHome != actualHome {
		t.Fatal("runtime used host HOME")
	}
	if dump.Env["GH_TOKEN"] != "synthetic-cli-A" {
		t.Fatal("allowed project CLI credential not injected")
	}
	t.Logf("normal A->B: currentPID=%d nextPID=%d sameSession=true renameNoRestart=true helperRefreshCount=1 generation=%d->%d privateHOME=true modelEnvAbsent=true", pidA, pidB, generation, after.Generation)
}

func TestProjectCredentialsRuntimeCacheRestartRecovery(t *testing.T) {
	r := projectCredentialsRuntimeNew(t)
	a := r.login("account-A", nil)
	path, agent := r.agent("runtime-cache")
	r.turn(path, "cache-baseline !tool=baseline-once")
	session := r.normalSession(agent.ID)
	pid := r.s.a.Executor.Process(agent.ID).PID
	cache := filepath.Join(r.w.cfg.WorkRoot, "agents", agent.ID.String(), "pi", "auth.json")
	for i, damage := range []string{"truncated", "missing"} {
		if damage == "truncated" {
			if e := os.WriteFile(cache, []byte("{"), 0600); e != nil {
				t.Fatal(e)
			}
		} else {
			if e := os.Remove(cache); e != nil {
				t.Fatal(e)
			}
		}
		r.turn(path, "cache-repair-"+damage+" !tool=repair-once")
		r.cache(agent.ID, "account-A")
		if r.s.a.Executor.Process(agent.ID).PID != pid || r.normalSession(agent.ID) != session {
			t.Fatal("cache repair replaced process/session")
		}
		p, k := projectCredentialsRuntimeNativeCounts(t, session)
		if p != i+2 || k != i+2 {
			t.Fatal("cache repair replayed native prompt/tool", p, k)
		}
	}
	if e := os.WriteFile(cache, []byte("{"), 0600); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(session)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(before)
	r.restart()
	time.Sleep(150 * time.Millisecond)
	after, e := os.ReadFile(session)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("idle App restart replayed native session")
	}
	clear(after)
	if r.normalSession(agent.ID) != session {
		t.Fatal("restart replaced native session path")
	}
	r.turn(path, "cache-after-app-restart !tool=restart-once")
	r.cache(agent.ID, "account-A")
	pid = r.s.a.Executor.Process(agent.ID).PID
	if e := os.Remove(cache); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(cache, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(cache, "owned-obstruction"), []byte("test-only"), 0600); e != nil {
		t.Fatal(e)
	}
	r.s.must(202, "POST", path+"/messages", map[string]any{"text": "cache-unrepairable !tool=must-not-run"})
	r.s.waitIdle(path)
	replies := r.s.replies(path)
	last := replies[len(replies)-1]
	if last.Run.Status != "failed" || last.Run.ErrorCode == nil || *last.Run.ErrorCode != executor.ErrCredentialUnavailable {
		t.Fatal("unrepairable cache must fail turn")
	}
	p, k := projectCredentialsRuntimeNativeCounts(t, session)
	if p != 4 || k != 4 {
		t.Fatal("unrepairable cache reached/replayed RPC", p, k)
	}
	if e := os.RemoveAll(cache); e != nil {
		t.Fatal(e)
	}
	r.turn(path, "cache-explicit-recovery !tool=recover-once")
	r.cache(agent.ID, "account-A")
	if r.normalSession(agent.ID) != session || r.s.a.Executor.Process(agent.ID).PID != pid {
		t.Fatal("explicit repair replaced reusable session/process")
	}
	// An explicit selection prevents a second available account from masking
	// simulated refresh rejection. No production provider is contacted.
	current := decode[apitypes.Agent](t, r.s.must(200, "GET", path, nil))
	r.s.must(200, "PUT", path+"/credential-selections", map[string]any{"purpose": "model", "provider": "openai-codex", "accountId": a.ID, "expectedConfigVersion": current.ConfigVersion})
	other := r.login("account-C", nil)
	if other.ID == a.ID {
		t.Fatal("secondary account missing")
	}
	r.forceExpired(*a.CurrentVersionID)
	r.setControl("account-A", true)
	p0, k0 := projectCredentialsRuntimeNativeCounts(t, session)
	r.s.must(202, "POST", path+"/messages", map[string]any{"text": "refresh-rejected !tool=must-not-fallback"})
	r.s.waitIdle(path)
	replies = r.s.replies(path)
	last = replies[len(replies)-1]
	if last.Run.Status != "failed" || last.Run.ErrorCode == nil || *last.Run.ErrorCode != executor.ErrCredentialUnavailable {
		t.Fatal("refresh rejection did not fail")
	}
	p, k = projectCredentialsRuntimeNativeCounts(t, session)
	if p != p0 || k != k0 {
		t.Fatal("refresh rejection fell back/replayed RPC")
	}
	selections := decode[cm.Selections](t, r.s.must(200, "GET", path+"/credential-selections", nil))
	if len(selections.Selections) == 0 || selections.Selections[0].SelectedAccountID == nil || *selections.Selections[0].SelectedAccountID != a.ID {
		t.Fatal("refresh rejection changed selection")
	}
	r.cache(agent.ID, "account-A")
	rejectedVersion, e := r.s.a.Store.GetModelAuthVersion(context.Background(), *a.CurrentVersionID)
	if e != nil || rejectedVersion.RefreshState != "unavailable" {
		t.Fatal("refresh rejection was not durably unavailable")
	}
	t.Logf("same-grant cache: truncate/remove repaired without PID/session change; restartNoReplay=true nonrepairableFailsBeforeRPC=true rejectionNoFallback=true nativePrompts=%d nativeTools=%d", p, k)
}

func (r *projectCredentialsRuntime) taskBinding(runtime, version, accountID uuid.UUID, label string) {
	r.t.Helper()
	snap, e := r.s.a.Tasks.Source.ExecutionSnapshot(context.Background(), runtime)
	if e != nil || snap.ModelBinding == nil {
		r.t.Fatal("task snapshot unavailable", e)
	}
	b := snap.ModelBinding
	if b.OwnerKind != "project" || b.ProjectID == nil || *b.ProjectID != r.project || b.AccountID == nil || *b.AccountID != accountID || b.VersionID != version {
		r.t.Fatal("task immutable owner/account/grant binding changed")
	}
	data, e := snap.ModelCredential.AuthFile("openai-codex")
	if e != nil {
		r.t.Fatal(e)
	}
	defer clear(data)
	var cache map[string]struct {
		AccountID string `json:"accountId"`
	}
	if json.Unmarshal(data, &cache) != nil || cache["openai-codex"].AccountID != label {
		r.t.Fatal("task captured model account changed")
	}
	if snap.CLIPayloads["gh"].Env["GH_TOKEN"] != "synthetic-cli-A" {
		r.t.Fatal("task captured allowed CLI credential changed")
	}
	r.cache(runtime, label)
}
func (r *projectCredentialsRuntime) taskIdle(runtime uuid.UUID) {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		e := r.s.a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM task_turns WHERE runtime_id=$1 AND status IN ('queued','starting','running')`, runtime).Scan(&n)
		if e != nil {
			r.t.Fatal(e)
		}
		if n == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.t.Fatal("task runtime did not settle")
}
func (r *projectCredentialsRuntime) taskLatest(runtime uuid.UUID) store.TaskTurn {
	r.t.Helper()
	var id uuid.UUID
	if e := r.s.a.Store.Pool.QueryRow(context.Background(), `SELECT id FROM task_turns WHERE runtime_id=$1 ORDER BY queue_seq DESC LIMIT 1`, runtime).Scan(&id); e != nil {
		r.t.Fatal(e)
	}
	turn, e := r.s.a.Store.TaskTurn(context.Background(), id)
	if e != nil {
		r.t.Fatal(e)
	}
	return turn
}
func TestProjectCredentialsRuntimeTaskFixedSnapshots(t *testing.T) {
	t.Setenv("FAKEPI_PROMPT_DELAY", "600ms")
	r := projectCredentialsRuntimeNew(t)
	ctx := context.Background()
	accountA := r.login("account-A", nil)
	path, worker := r.agent("runtime-worker")
	cliA := r.cli(path)
	// Explicitly selecting the account is essential to the deletion assertion:
	// automatic zero candidates is an unconfigured slot, not a deleted selection.
	selectedAgent := decode[apitypes.Agent](t, r.s.must(200, "GET", path, nil))
	r.s.must(200, "PUT", path+"/credential-selections", map[string]any{"purpose": "model", "provider": "openai-codex", "accountId": accountA.ID, "expectedConfigVersion": selectedAgent.ConfigVersion})
	_, manager := r.agent("runtime-manager")
	r.s.must(200, "PUT", "/projects/"+r.project.String()+"/manager", map[string]any{"agentId": manager.ID})
	r.turn(path, "task-parent-baseline !tool=parent-baseline")
	steps := []workflow.Step{
		{ID: "first", Title: "first", AgentID: worker.ID, Description: " !sleep=800ms !tool=first-node !text=first", InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}},
		{ID: "second", Title: "second", AgentID: worker.ID, Description: " !error=blocked-fixture !text=second !tool=second-node", InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}},
	}
	raw, e := r.s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: manager.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "project captured runtime", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "first", Steps: steps, Edges: []workflow.Edge{{ID: "next", From: "first", To: "second", Label: "next"}}}})
	if e != nil {
		t.Fatal(e)
	}
	created := decode[taskmodel.Detail](t, raw)
	runtime := created.Nodes[0].RuntimeID
	principal := auth.Principal{Kind: auth.KindAgent, ID: worker.ID, TaskID: created.Task.ID, RuntimeID: runtime}
	source := sleepWaitTurn(t, r.s, runtime, uuid.Nil)
	reason := " !sleep=800ms !text=sleep-wake"
	receipt, e := r.s.a.Store.RequestSleep(ctx, principal, sleepmodel.Input{ActionID: uuid.New(), RunID: source.ID, Seconds: 120, Reason: &reason}, 1000)
	if e != nil {
		t.Fatal(e)
	}
	waiting := taskWait(t, r.s, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "waiting" && len(d.SleepingInstances) == 1 })
	if waiting.SleepingInstances[0].ID != receipt.Sleep.ID {
		t.Fatal("sleep receipt projection mismatch")
	}
	oldWake := *waiting.SleepingInstances[0].WakeAt
	accountB := r.login("account-B", &accountA)
	cliB := decode[cm.Account](t, r.s.must(200, "PUT", "/projects/"+r.project.String()+"/credential-accounts/"+cliA.ID.String()+"/credential", map[string]any{"expectedRevision": cliA.Revision, "credential": map[string]any{"type": "cli", "fields": map[string]string{"token": "synthetic-cli-B"}}}))
	if cliB.CurrentVersionID == nil || *cliB.CurrentVersionID == *cliA.CurrentVersionID {
		t.Fatal("CLI grant replacement did not create version")
	}
	r.taskBinding(runtime, *accountA.CurrentVersionID, accountA.ID, "account-A")
	// Sleeping is durable across an owned App restart, including the original
	// deadline. Restart must not create a wake/tool/prompt before an input exists.
	inst, e := r.s.a.Store.TaskInstance(ctx, runtime)
	if e != nil || inst.SessionFile == nil {
		t.Fatal("task session missing", e)
	}
	session := *inst.SessionFile
	before, e := os.ReadFile(session)
	if e != nil {
		t.Fatal(e)
	}
	r.restart()
	restored := taskWait(t, r.s, created.Task.ID, func(d taskmodel.Detail) bool { return len(d.SleepingInstances) == 1 })
	if restored.SleepingInstances[0].ID != receipt.Sleep.ID || restored.SleepingInstances[0].WakeAt == nil || !restored.SleepingInstances[0].WakeAt.Equal(oldWake) {
		t.Fatal("restart changed captured sleep/deadline")
	}
	after, e := os.ReadFile(session)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("sleep restart replayed native tools/prompts")
	}
	clear(before)
	clear(after)
	address := "默认项目/runtime-worker#" + created.Task.ID.String()
	if _, e = r.s.a.API.Inbox.Send(ctx, auth.Console(), address, "wake captured node !sleep=800ms !text=wake-A", "queue"); e != nil {
		t.Fatal(e)
	}
	wake := sleepWaitTurn(t, r.s, runtime, source.ID)
	if wake.ExecutionMode != "task_node" {
		t.Fatal("current node wake changed mode", wake.ExecutionMode)
	}
	// Wait for the runtime to acquire its cache, not merely PG running status.
	taskWait(t, r.s, created.Task.ID, func(d taskmodel.Detail) bool { return r.s.a.Executor.Process(runtime).State == "busy" })
	r.taskBinding(runtime, *accountA.CurrentVersionID, accountA.ID, "account-A")
	edge := "next"
	if _, e = r.s.a.Tasks.Submit(ctx, principal, created.Task.ID, created.Nodes[0].ID, taskmodel.SubmitInput{ActionID: uuid.New(), Output: map[string]any{}, EdgeID: &edge}); e != nil {
		t.Fatal(e)
	}
	blocked := taskWait(t, r.s, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" && len(d.Nodes) == 2 })
	if blocked.Nodes[1].RuntimeID != runtime {
		t.Fatal("same Agent nodes did not reuse captured runtime")
	}
	r.taskBinding(runtime, *accountA.CurrentVersionID, accountA.ID, "account-A")
	cache := filepath.Join(r.w.cfg.WorkRoot, "agents", runtime.String(), "pi", "auth.json")
	if e := os.WriteFile(cache, []byte("{"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = r.s.a.Tasks.Retry(ctx, auth.Console(), created.Task.ID, taskmodel.RetryInput{ActionID: uuid.New(), ExpectedNodeRunID: *blocked.Task.CurrentNodeRunID, Note: "explicit retry old captured account"}); e != nil {
		t.Fatal(e)
	}
	blocked = taskWait(t, r.s, created.Task.ID, func(d taskmodel.Detail) bool {
		return d.Task.Status == "blocked" && len(d.Nodes) == 3 && d.Nodes[2].Attempt == 2
	})
	r.taskBinding(runtime, *accountA.CurrentVersionID, accountA.ID, "account-A")
	// Ordinary Inbox turns on a blocked task remain separate from the workflow.
	if _, e = r.s.a.API.Inbox.Send(ctx, auth.Console(), address, "ordinary captured conversation !sleep=800ms !tool=ordinary-once", "queue"); e != nil {
		t.Fatal(e)
	}
	ordinary := sleepWaitTurn(t, r.s, runtime, wake.ID)
	if ordinary.ExecutionMode != "task_conversation" {
		t.Fatal("blocked task ordinary turn became node", ordinary.ExecutionMode)
	}
	reason = "ordinary sleep"
	ordinaryReceipt, e := r.s.a.Store.RequestSleep(ctx, principal, sleepmodel.Input{ActionID: uuid.New(), RunID: ordinary.ID, Seconds: 1, Reason: &reason}, 1000)
	if e != nil {
		t.Fatal(e)
	}
	taskWait(t, r.s, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" && len(d.SleepingInstances) == 1 })
	// Exercise the real scheduler deadline, not another fixture event/message.
	ordinaryWake := sleepWaitTurn(t, r.s, runtime, ordinary.ID)
	if ordinaryWake.Origin != "sleep_wake" || ordinaryWake.WakeSleepID == nil || *ordinaryWake.WakeSleepID != ordinaryReceipt.Sleep.ID {
		t.Fatal("real timer wake lost source sleep identity")
	}
	if ordinaryWake.ExecutionMode != "task_conversation" {
		t.Fatal("ordinary sleep wake changed workflow mode")
	}
	r.taskIdle(runtime)
	r.taskBinding(runtime, *accountA.CurrentVersionID, accountA.ID, "account-A")
	taskWait(t, r.s, created.Task.ID, func(d taskmodel.Detail) bool {
		return d.Task.Status == "blocked" && len(d.SleepingInstances) == 0 && d.Task.CurrentNodeRunID != nil && *d.Task.CurrentNodeRunID == *blocked.Task.CurrentNodeRunID
	})
	// Both original and replaced versions must remain resolvable to captured work
	// after account deletion. A new normal turn must not use that tombstone.
	r.s.must(200, "DELETE", "/projects/"+r.project.String()+"/credential-accounts/"+accountB.ID.String()+"?expectedRevision="+fmt.Sprint(accountB.Revision), nil)
	r.taskBinding(runtime, *accountA.CurrentVersionID, accountA.ID, "account-A")
	r.s.must(202, "POST", path+"/messages", map[string]any{"text": "normal-after-account-deleted !tool=must-not-run"})
	r.s.waitIdle(path)
	replies := r.s.replies(path)
	if replies[len(replies)-1].Run.Status != "failed" || replies[len(replies)-1].Run.ErrorCode == nil || *replies[len(replies)-1].Run.ErrorCode != executor.ErrCredentialUnavailable {
		t.Fatal("deleted normal account unexpectedly runnable")
	}
	if e := os.Remove(cache); e != nil {
		t.Fatal(e)
	}
	before, e = os.ReadFile(session)
	if e != nil {
		t.Fatal(e)
	}
	r.restart()
	time.Sleep(150 * time.Millisecond)
	inst, e = r.s.a.Store.TaskInstance(ctx, runtime)
	if e != nil || inst.SessionFile == nil || *inst.SessionFile != session {
		t.Fatal("task restart changed native session", e)
	}
	after, e = os.ReadFile(session)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("blocked task restart replayed tool/prompt")
	}
	clear(before)
	clear(after)
	if _, e = r.s.a.API.Inbox.Send(ctx, auth.Console(), address, "after-deletion-and-restart !tool=deleted-old-grant-once", "queue"); e != nil {
		t.Fatal(e)
	}
	r.taskIdle(runtime)
	r.taskBinding(runtime, *accountA.CurrentVersionID, accountA.ID, "account-A")
	last := r.taskLatest(runtime)
	if last.Status != "completed" || last.ExecutionMode != "task_conversation" {
		t.Fatal("captured old grant not runnable after deletion/restart", last.Status, last.ExecutionMode)
	}
	taskWait(t, r.s, created.Task.ID, func(d taskmodel.Detail) bool {
		return d.Task.Status == "blocked" && d.Task.CurrentNodeRunID != nil && *d.Task.CurrentNodeRunID == *blocked.Task.CurrentNodeRunID
	})
	if _, e = r.s.a.Tasks.Cancel(ctx, auth.Console(), created.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()}); e != nil {
		t.Fatal(e)
	}
	taskWait(t, r.s, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "cancelled" && d.Cleanup == "archived" })
	t.Log("captured project account/CLI A: next-node=true retryCacheRepair=true current-nodeSleepWake=true blockedConversationSleepWake=true deletion=true restartSameSessionNoReplay=true workflowRemainsBlocked=true")
}

func (r *projectCredentialsRuntime) auditCleanup() {
	t := r.t
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, e := pgx.Connect(ctx, r.w.cfg.DatabaseURL)
	if e != nil {
		t.Error("TEST schema cleanup audit could not connect")
		return
	}
	defer conn.Close(ctx)
	var exists bool
	if e = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, r.w.schema).Scan(&exists); e != nil || exists {
		t.Error("owned TEST schema remains after cleanup")
	}
	opts, e := redis.ParseURL(r.w.cfg.RedisURL)
	if e != nil {
		t.Error("TEST Redis audit configuration invalid")
		return
	}
	client := redis.NewClient(opts)
	defer client.Close()
	keys, e := client.Keys(ctx, r.w.cfg.RedisKeyPrefix+"*").Result()
	if e != nil || len(keys) != 0 {
		t.Error("owned TEST Redis prefix remains after cleanup")
	}
	if _, e = os.Stat(r.w.cfg.WorkRoot); !os.IsNotExist(e) {
		t.Error("owned work directory remains")
	}
	if fakepiAlive(t, r.canonicalRoot) {
		t.Error("owned fakePi process remains")
	}
	for _, address := range r.addresses {
		c, e := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if e == nil {
			c.Close()
			t.Error("owned App listener remains open")
		}
	}
	if !t.Failed() {
		t.Log("cleanup readback: schemaAbsent=true ownedRedisKeys=0 workRootAbsent=true fakePiAbsent=true ownedListenersClosed=true")
	}
}
