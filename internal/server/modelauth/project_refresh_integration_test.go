package modelauth

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	authhelper "loop-now/auth-helper"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/prompts"
	"loop-now/internal/server/store"
)

const projectRefreshNode = "/Users/ljl/.local/node-v24.21.0/bin/node"
const projectRefreshPi = "/opt/homebrew/lib/node_modules/@earendil-works/pi-coding-agent"

// This fixture creates its own cluster, not a schema in an existing database.
// It never consults testenv/.env.local, host credentials, or a running service.
type projectRefreshEnv struct {
	st           *store.Store
	project      uuid.UUID
	agents       [2]store.Agent
	account      store.ProjectCredentialRecord
	initial      modelcredential.Credential
	repo, root   string
	clock        atomic.Int64
	calls        atomic.Int32
	invalid      atomic.Bool
	responseLoss atomic.Bool
	wrongAccount atomic.Bool
	server       *httptest.Server
	helper       *Helper
	entered      chan struct{}
	release      chan struct{}
	once         sync.Once
}

func projectRefreshFixture(t *testing.T, held bool) *projectRefreshEnv {
	t.Helper()
	for _, path := range []string{projectRefreshNode, filepath.Join(projectRefreshPi, "dist/bundle/cli.js"), "/usr/bin/sandbox-exec", "/opt/homebrew/opt/postgresql@18/bin/initdb"} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("required fixed offline dependency unavailable: %s", path)
		}
	}
	e := &projectRefreshEnv{entered: make(chan struct{}), release: make(chan struct{})}
	e.repo = filepath.Clean(filepath.Join("..", "..", ".."))
	e.repo, _ = filepath.Abs(e.repo)
	var err error
	e.root, err = os.MkdirTemp("/private/tmp", "ln-project-refresh-")
	if err != nil {
		t.Fatal(err)
	}
	data, socket := filepath.Join(e.root, "pg"), filepath.Join(e.root, "socket")
	if err = os.Mkdir(socket, 0700); err != nil {
		t.Fatal(err)
	}
	pgCommand := func(args ...string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/opt/homebrew/opt/postgresql@18/bin/"+args[0], args[1:]...)
		cmd.Env = []string{"HOME=" + e.root, "PATH=/usr/bin:/bin", "LC_ALL=C"}
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		return cmd.Run()
	}
	if err = pgCommand("initdb", "-D", data, "-U", "fixture", "-A", "trust", "--no-locale", "-E", "UTF8"); err != nil {
		os.RemoveAll(e.root)
		t.Fatal("self-owned PG init failed", err)
	}
	if err = pgCommand("pg_ctl", "-D", data, "-l", filepath.Join(e.root, "pg.log"), "-o", "-F -h '' -k "+socket+" -p 55432", "-w", "-t", "10", "start"); err != nil {
		os.RemoveAll(e.root)
		t.Fatal("self-owned PG start failed", err)
	}
	t.Cleanup(func() {
		if e.st != nil {
			e.st.Close()
		}
		if err := pgCommand("pg_ctl", "-D", data, "-m", "immediate", "-w", "-t", "10", "stop"); err != nil {
			t.Errorf("self-owned PG cleanup failed: %v", err)
			return
		}
		if err := os.RemoveAll(e.root); err != nil {
			t.Error("fixture cleanup failed")
		}
	})
	e.st, err = store.Open(context.Background(), "postgres://fixture@/postgres?host="+url.QueryEscape(socket)+"&port=55432&sslmode=disable", store.OpenOptions{})
	if err != nil {
		t.Fatal("own PG connection failed")
	}
	if err = e.st.Migrate(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	defaults, err := e.st.EnsureDefaults(context.Background(), "fixture-org", "fixture-project")
	if err != nil {
		t.Fatal(err)
	}
	e.project = defaults.Project.ID
	for i := range e.agents {
		e.agents[i], err = e.st.CreateAgent(context.Background(), store.ProjectScope(e.project), fmt.Sprintf("fixture-agent-%d", i), "openai-codex", "gpt-6.1-sol", "")
		if err != nil {
			t.Fatal(err)
		}
	}
	e.clock.Store(time.Now().UnixMilli())
	e.initial = oauth(t, "proof-account-A", "seed", time.Now().Add(-time.Hour))
	e.account, err = e.st.CreateProjectCredential(context.Background(), e.project, store.PurposeModel, "openai-codex", "shared", e.write(t, e.initial))
	if err != nil {
		t.Fatal(err)
	}
	if !held {
		close(e.release)
	}
	var barrierMu sync.Mutex
	arrivals := 0
	barrier := make(chan struct{})
	e.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/probe" {
			io.WriteString(w, "ok")
			return
		}
		if r.URL.Path == "/expire" {
			barrierMu.Lock()
			arrivals++
			if arrivals == 2 {
				e.clock.Add(int64((2 * time.Hour) / time.Millisecond))
				close(barrier)
			}
			barrierMu.Unlock()
			select {
			case <-barrier:
				io.WriteString(w, "ok")
			case <-r.Context().Done():
			}
			return
		}
		if r.URL.Path != "/refresh" || r.Method != "POST" {
			w.WriteHeader(404)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 65536)
		if r.ParseForm() != nil || r.Form.Get("grant_type") != "refresh_token" {
			e.invalid.Store(true)
			w.WriteHeader(400)
			return
		}
		wave := e.calls.Add(1)
		expected := "refresh-seed"
		if wave > 1 {
			expected = "proof-refresh-" + strconv.Itoa(int(wave-1))
		}
		if r.Form.Get("refresh_token") != expected {
			e.invalid.Store(true)
			w.WriteHeader(400)
			return
		}
		e.once.Do(func() { close(e.entered) })
		select {
		case <-e.release:
		case <-r.Context().Done():
			return
		}
		if e.responseLoss.Load() {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		account := "proof-account-A"
		if e.wrongAccount.Load() {
			account = "other-account"
		}
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"` + account + `"},"extra":"࠾࠿"}`))
		json.NewEncoder(w).Encode(map[string]any{"access_token": "proof-access-" + strconv.Itoa(int(wave)) + "." + payload + ".x", "refresh_token": "proof-refresh-" + strconv.Itoa(int(wave)), "expires_in": float64(e.clock.Load()+int64(time.Hour/time.Millisecond)-time.Now().UnixMilli()) / 1000})
	}))
	t.Cleanup(e.server.Close)
	// Same profile before every actual helper/Pi launch, tested against a second
	// self-owned listener. Never probe a real external address for isolation.
	deniedHits := atomic.Int32{}
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { deniedHits.Add(1); w.WriteHeader(200) }))
	defer denied.Close()
	allowedURL, _ := url.Parse(e.server.URL)
	deniedURL, _ := url.Parse(denied.URL)
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer probeCancel()
	probeCmd := exec.CommandContext(probeCtx, "/usr/bin/sandbox-exec", "-p", e.profile(), projectRefreshNode, filepath.Join(e.repo, "auth-helper/test/sandbox-port-probe.mjs"), allowedURL.Port(), deniedURL.Port())
	probeCmd.Env = []string{"HOME=" + e.root, "TMPDIR=" + e.root, "PATH=/usr/bin:/bin", "NODE_ENV=production"}
	probeOutput, probeErr := probeCmd.Output()
	var probe struct {
		Allowed struct{ Status int }
		Denied  struct{ ErrorCode string }
	}
	if probeErr != nil || json.Unmarshal(probeOutput, &probe) != nil || probe.Allowed.Status != 200 || probe.Denied.ErrorCode != "EPERM" || deniedHits.Load() != 0 {
		t.Fatal("process sandbox preflight failed; no actual Pi/helper launched")
	}
	assetEntry, _, err := authhelper.Install(filepath.Join(e.root, "assets"))
	if err != nil {
		t.Fatal(err)
	}
	gate, _ := json.Marshal(e.server.URL + "/refresh")
	ipc, _ := json.Marshal("file://" + filepath.Join(filepath.Dir(assetEntry), "ipc.mjs"))
	entry := filepath.Join(e.root, "offline-helper.mjs")
	// Actual Helper.Refresh -> runIPC -> installed lazyOAuth.refresh. Only the
	// fixed token request is mapped to this self-owned loopback fixture. No other
	// fetch is allowed, and the OS sandbox denies every other outbound socket.
	script := fmt.Sprintf(`import {runIPC} from %s;
const nativeFetch=globalThis.fetch;
globalThis.fetch=(input,init)=>{
 if(String(input)!=='https://auth.openai.com/oauth/token'||init?.method!=='POST')throw new Error('offline request rejected');
 const gate=%s;
 return nativeFetch(gate,{...init,redirect:'error'}).then(response=>{
  if(response.url!==gate||response.redirected)throw new Error('offline final URL rejected');
  return response;
 });
};runIPC();`, ipc, gate)
	if err = os.WriteFile(entry, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(e.root, "sandbox-node")
	profile := e.profile()
	wrapperBody := "#!/bin/sh\nexec /usr/bin/sandbox-exec -p '" + profile + "' " + projectRefreshNode + " \"$@\"\n"
	if err = os.WriteFile(wrapper, []byte(wrapperBody), 0700); err != nil {
		t.Fatal(err)
	}
	e.helper = &Helper{Options: HelperOptions{NodeBin: wrapper, Entry: entry, WorkRoot: e.root, BasePath: "/usr/bin:/bin", PiPackageRoot: projectRefreshPi, Timeout: 5 * time.Second}}
	return e
}
func (e *projectRefreshEnv) profile() string {
	u, _ := url.Parse(e.server.URL)
	return `(version 1) (allow default) (deny network*) (allow network-outbound (remote ip "localhost:` + u.Port() + `"))`
}
func (e *projectRefreshEnv) write(t *testing.T, c modelcredential.Credential) store.ProjectCredentialWrite {
	t.Helper()
	plain, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return store.ProjectCredentialWrite{AuthMethod: "oauth", Secret: plain}
}
func (e *projectRefreshEnv) coordinator(t *testing.T, st RefreshStore) *Coordinator {
	t.Helper()
	c := NewCoordinator(st, e.helper, CoordinatorOptions{Now: func() time.Time { return time.UnixMilli(e.clock.Load()) }, PollInterval: 2 * time.Millisecond, RefreshTimeout: 8 * time.Second, WaitTimeout: 10 * time.Second, RecordTimeout: time.Second}, nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := c.Close(ctx); err != nil {
			t.Error("coordinator drain failed")
		}
	})
	return c
}
func (e *projectRefreshEnv) capture(t *testing.T) [2]modelcredential.Binding {
	t.Helper()
	ctx := context.Background()
	var out [2]modelcredential.Binding
	normal, err := e.st.CaptureExecutionIdentity(ctx, e.agents[0].ID)
	if err != nil || normal.ModelAuth == nil {
		t.Fatal("normal capture failed", err)
	}
	out[0] = normal.ModelAuth.Clone()
	tx, err := e.st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	// Match caller lock order: project before Agent, then capture in this Tx.
	var prompt string
	var version int64
	if err = tx.QueryRow(ctx, `SELECT prompt,prompt_version FROM projects WHERE id=$1 FOR SHARE`, e.project).Scan(&prompt, &version); err != nil {
		t.Fatal(err)
	}
	task, err := e.st.CaptureTaskAgent(ctx, tx, e.agents[1].ID, uuid.New(), prompts.ProjectRules{ID: e.project, Prompt: prompt, Version: version})
	if err != nil || task.ModelAuth == nil {
		t.Fatal("task capture failed", err)
	}
	out[1] = task.ModelAuth.Clone()
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, b := range out {
		if b.OwnerKind != "project" || b.VersionID != e.account.Model.ID || *b.AccountID != e.account.View.ID || *b.ProjectID != e.project {
			t.Fatal("capture did not bind shared project version")
		}
	}
	return out
}

type projectRefreshPiChild struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	done   chan error
	mu     sync.Mutex
	output bytes.Buffer
	stderr bytes.Buffer
	home   string
	exited atomic.Bool
}

type projectRefreshLockedWriter struct{ child *projectRefreshPiChild }

func (w projectRefreshLockedWriter) Write(p []byte) (int, error) {
	w.child.mu.Lock()
	defer w.child.mu.Unlock()
	return w.child.stderr.Write(p)
}

func (e *projectRefreshEnv) pi(t *testing.T, b modelcredential.Binding, c *Coordinator, index int) *projectRefreshPiChild {
	t.Helper()
	bridge, err := NewRuntimeBridge(b, c, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bridge.Close() })
	home := filepath.Join(e.root, fmt.Sprintf("pi-%d", index))
	agent := filepath.Join(home, "agent")
	if err = os.MkdirAll(agent, 0700); err != nil {
		t.Fatal(err)
	}
	auth, err := e.initial.AuthFile("openai-codex")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(agent, "auth.json"), auth, 0600); err != nil {
		t.Fatal(err)
	}
	clear(auth)
	report := filepath.Join(home, "network-report")
	if err = os.WriteFile(report, nil, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-p", e.profile(), projectRefreshNode, "--import", filepath.Join(e.repo, "auth-helper/test/deny-network.mjs"), filepath.Join(projectRefreshPi, "dist/bundle/cli.js"), "--mode", "rpc", "--no-extensions", "-e", filepath.Join(e.repo, "auth-helper/runtime-refresh.mjs"), "-e", filepath.Join(e.repo, "auth-helper/test/rpc-model.mjs"), "--no-skills", "--no-prompt-templates", "--no-themes", "--no-mcp", "--no-session", "--provider", "openai-codex", "--model", "gpt-6.1-sol", "--thinking", "off", "--tools", "proof_expire"}
	p := &projectRefreshPiChild{cmd: exec.Command("/usr/bin/sandbox-exec", args...), done: make(chan error, 1), home: home}
	p.cmd.Dir = home
	p.cmd.Env = []string{"HOME=" + home, "TMPDIR=" + home, "PATH=/usr/bin:/bin", "NODE_ENV=production", "PI_CODING_AGENT_DIR=" + agent, "PI_OFFLINE=1", "PI_TELEMETRY=0", "AUTH_PROOF_GATE=" + e.server.URL + "/expire", "AUTH_PROOF_NETWORK_REPORT=" + report, "LOOP_NOW_AUTH_REFRESH_TIMEOUT_MS=8000"}
	p.cmd.ExtraFiles = bridge.ChildFiles()
	p.stdin, err = p.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := p.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p.cmd.Stderr = projectRefreshLockedWriter{p}
	if err = p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	bridge.ChildStarted()
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		scan := bufio.NewScanner(stdout)
		scan.Buffer(make([]byte, 4096), MaxFrame)
		for scan.Scan() {
			p.mu.Lock()
			p.output.Write(scan.Bytes())
			p.output.WriteByte('\n')
			p.mu.Unlock()
		}
	}()
	go func() { err := p.cmd.Wait(); readers.Wait(); p.exited.Store(true); p.done <- err }()
	t.Cleanup(func() {
		p.cmd.Process.Kill()
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
			t.Error("owned Pi did not exit")
		}
	})
	return p
}
func (p *projectRefreshPiChild) events() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.output.String()
}

func TestProjectRefreshActualPiSharedLongRound(t *testing.T) {
	e := projectRefreshFixture(t, false)
	refs := e.capture(t)
	ctx := context.Background()
	// Advance the current account before dispatch. Captured normal/task bindings
	// must still refresh V1, never read current V2 to repair the old snapshot.
	w := e.write(t, oauth(t, "other-account", "current", time.Now().Add(24*time.Hour)))
	current, err := e.st.MutateProjectCredential(ctx, e.project, e.account.View.ID, e.account.View.Revision, nil, &w, false)
	if err != nil {
		t.Fatal(err)
	}
	first, second := e.coordinator(t, e.st), e.coordinator(t, e.st)
	children := []*projectRefreshPiChild{e.pi(t, refs[0], first, 0), e.pi(t, refs[1], second, 1)}
	for _, p := range children {
		if _, err = fmt.Fprintln(p.stdin, `{"id":"round","type":"prompt","message":"offline shared long round"}`); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(children[0].events(), `"type":"agent_end"`) && strings.Contains(children[1].events(), `"type":"agent_end"`) {
			break
		}
		if children[0].exited.Load() || children[1].exited.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, p := range children {
		events := p.events()
		p.mu.Lock()
		stderr := p.stderr.String()
		p.mu.Unlock()
		if !strings.Contains(events, "offline authenticated") || !strings.Contains(events, `"type":"agent_end"`) {
			t.Fatal("actual Pi did not finish authenticated model/tool/model round (raw output withheld)")
		}
		if strings.Contains(events+stderr, "proof-access-") || strings.Contains(events+stderr, "proof-refresh-") || strings.Contains(events+stderr, "refresh-seed") {
			t.Fatal("secret entered RPC events")
		}
		data, err := os.ReadFile(filepath.Join(p.home, "agent/auth.json"))
		if err != nil {
			t.Fatal(err)
		}
		var auth map[string]json.RawMessage
		if json.Unmarshal(data, &auth) != nil {
			t.Fatal("invalid isolated auth cache")
		}
		value, err := modelcredential.Decode(auth["openai-codex"])
		clear(data)
		if err != nil || !value.SameAccount(e.initial) {
			t.Fatal("Pi cache switched account")
		}
		var wire struct{ Refresh string }
		json.Unmarshal(auth["openai-codex"], &wire)
		if wire.Refresh != "proof-refresh-2" {
			t.Fatal("Pi cache did not consume second shared generation")
		}
		info, err := os.Stat(filepath.Join(p.home, "agent/auth.json"))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("unsafe isolated auth permissions")
		}
		report, err := os.ReadFile(filepath.Join(p.home, "network-report"))
		if err != nil || len(report) != 0 {
			t.Fatal("unexpected provider network attempt")
		}
	}
	old, err := e.st.GetModelAuthVersion(ctx, refs[0].VersionID)
	if err != nil || old.Generation != 3 || old.RefreshState != "ready" || e.calls.Load() != 2 || e.invalid.Load() {
		t.Fatal("PG claim/helper/commit did not serialize two actual Pi expiry waves")
	}
	latest, err := e.st.GetProjectCredential(ctx, e.project, e.account.View.ID)
	if err != nil || latest.Model.ID != current.Model.ID || latest.Model.Generation != 1 {
		t.Fatal("old captured refresh modified current V2")
	}
	t.Log("actual Pi 1.1.0: two Agents, normal/task captures, two model/tool/model expiry waves; two real helper refreshes; V1 generation 1->3; current V2 untouched; provider attempts zero")
}

func TestProjectRefreshPrivateIPCCancellationAndSelectors(t *testing.T) {
	e := projectRefreshFixture(t, true)
	refs := e.capture(t)
	first, second := e.coordinator(t, e.st), e.coordinator(t, e.st)
	bridge, err := NewRuntimeBridge(refs[0], first, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	child := bridge.ChildFiles()
	if _, err = fmt.Fprintln(child[1], `{"id":"cancelled","method":"refresh","provider":"openai-codex"}`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-e.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("actual helper refresh did not start")
	}
	// Closing the runtime pipe cancels only its waiter, not the independent worker.
	bridge.Close()
	close(e.release)
	waitAuth(t, func() bool {
		v, err := e.st.GetModelAuthVersion(context.Background(), refs[0].VersionID)
		return err == nil && v.Generation == 2 && v.RefreshState == "ready"
	})
	value, err := second.Resolve(context.Background(), refs[1])
	if err != nil || !value.SameAccount(e.initial) || e.calls.Load() != 1 {
		t.Fatal("runtime cancellation discarded or replayed persisted rotation")
	}
	for _, field := range []string{"projectId", "accountId", "versionId", "agentId", "taskId", "grantId"} {
		t.Run(field, func(t *testing.T) {
			b, err := NewRuntimeBridge(refs[1], second, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			f := b.ChildFiles()
			f[0].SetReadDeadline(time.Now().Add(time.Second))
			f[1].SetWriteDeadline(time.Now().Add(time.Second))
			if _, err = fmt.Fprintf(f[1], "{\"id\":\"escape\",\"method\":\"refresh\",\"provider\":\"openai-codex\",%q:%q}\n", field, uuid.NewString()); err != nil {
				t.Fatal("illegal selector request was not delivered")
			}
			data, readErr := bufio.NewReader(f[0]).ReadBytes('\n')
			if os.IsTimeout(readErr) || len(data) != 0 || (!errors.Is(readErr, io.EOF) && !errors.Is(readErr, os.ErrClosed)) {
				t.Fatal("illegal selector must close private IPC with EOF/closed error, not timeout or response")
			}
		})
	}
	if e.calls.Load() != 1 || e.invalid.Load() {
		t.Fatal("invalid private frame reached refresh provider")
	}
	for _, mutate := range []func(*modelcredential.Binding){
		func(b *modelcredential.Binding) { id := uuid.New(); b.ProjectID = &id }, func(b *modelcredential.Binding) { id := uuid.New(); b.AccountID = &id }, func(b *modelcredential.Binding) { b.AgentID = uuid.New() }, func(b *modelcredential.Binding) { b.OwnerKind = "agent"; b.ProjectID = nil; b.AccountID = nil },
	} {
		b := refs[0].Clone()
		mutate(&b)
		if _, err = second.Resolve(context.Background(), b); !errors.Is(err, ErrCredentialUnavailable) {
			t.Fatal("forged project binding authorized")
		}
	}
	t.Log("real helper rotation persisted after private runtime waiter cancellation; selectors and forged owners rejected; one refresh only")
}

// Only the persistence acknowledgement boundary is injected. Claim/fail/owner
// validation stay on real PG; the Refresher is the real Helper, not a Go mock.
type projectRefreshCommitBoundary struct {
	*store.Store
	mode  string
	calls atomic.Int32
}

func (s *projectRefreshCommitBoundary) CommitModelAuthRefresh(ctx context.Context, id, attempt uuid.UUID, secret []byte) (bool, error) {
	s.calls.Add(1)
	if s.mode == "write_failed" {
		return false, errors.New("synthetic persistence unavailable")
	}
	ok, err := s.Store.CommitModelAuthRefresh(ctx, id, attempt, secret)
	if err != nil {
		return ok, err
	}
	return ok, errors.New("synthetic acknowledgement lost")
}

func TestProjectRefreshActualHelperFailureBoundaries(t *testing.T) {
	for _, mode := range []string{"write_failed", "ack_lost", "response_lost", "same_account_rejected"} {
		t.Run(mode, func(t *testing.T) {
			e := projectRefreshFixture(t, false)
			refs := e.capture(t)
			boundary := &projectRefreshCommitBoundary{Store: e.st, mode: mode}
			var persistence RefreshStore = e.st
			if mode == "write_failed" || mode == "ack_lost" {
				persistence = boundary
			}
			e.responseLoss.Store(mode == "response_lost")
			e.wrongAccount.Store(mode == "same_account_rejected")
			coordinator := e.coordinator(t, persistence)
			b, err := NewRuntimeBridge(refs[0], coordinator, 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			files := b.ChildFiles()
			files[0].SetReadDeadline(time.Now().Add(12 * time.Second))
			files[1].SetWriteDeadline(time.Now().Add(time.Second))
			if _, err = fmt.Fprintln(files[1], `{"id":"failure","method":"refresh","provider":"openai-codex"}`); err != nil {
				t.Fatal(err)
			}
			data, err := bufio.NewReader(files[0]).ReadBytes('\n')
			if err != nil {
				t.Fatal("bounded real helper/PG bridge did not respond")
			}
			var response struct {
				OK         bool
				Code       string
				Credential json.RawMessage
			}
			if json.Unmarshal(data, &response) != nil {
				t.Fatal("invalid bridge response")
			}
			clear(data)
			row, err := e.st.GetModelAuthVersion(context.Background(), refs[0].VersionID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "ack_lost" {
				if !response.OK || row.Generation != 2 || row.RefreshState != "ready" || boundary.calls.Load() != 1 {
					t.Fatal("commit acknowledgement loss discarded durable rotation")
				}
				value, err := modelcredential.Decode(response.Credential)
				if err != nil || !value.SameAccount(e.initial) {
					t.Fatal("durable rotation account changed")
				}
			} else {
				if response.OK || response.Code != "auth_unavailable" || len(response.Credential) != 0 || row.Generation != 1 || row.RefreshState != "unavailable" {
					t.Fatal("unknown/failed rotation did not fail closed")
				}
			}
			// Another Agent/Coordinator must not consume the old refresh after ambiguity.
			other := e.coordinator(t, e.st)
			_, resolveErr := other.Resolve(context.Background(), refs[1])
			if (mode == "ack_lost") != (resolveErr == nil) || e.calls.Load() != 1 || e.invalid.Load() {
				t.Fatal("uncertain old refresh replayed or durable generation ignored")
			}
			if mode == "write_failed" && boundary.calls.Load() != 1 {
				t.Fatal("actual helper did not reach commit failure boundary")
			}
		})
	}
}
