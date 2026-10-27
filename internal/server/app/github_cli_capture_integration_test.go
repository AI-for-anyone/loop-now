package app_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/config"
	"loop-now/internal/server/githubauth/githubtest"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/taskruntime"
	"loop-now/internal/server/workflow"
	"loop-now/internal/testutil/testenv"
)

// A test-only child proves the sandbox allows exactly one owned Unix socket.
// It cannot run the suite or touch a platform runtime/account.
func init() {
	if os.Getenv("LOOP_NOW_GITHUB_SOCKET_GATE") != "1" {
		return
	}
	target := os.Getenv("LOOP_NOW_GITHUB_GATE_SOCKET")
	if !strings.HasPrefix(target, "/private/tmp/ln-gh-capture-") || !filepath.IsAbs(target) {
		os.Exit(97)
	}
	conn, err := net.DialTimeout("unix", target, time.Second)
	if err != nil {
		os.Exit(42)
	}
	_ = conn.Close()
	os.Exit(0)
}

func githubCapturedTask(t *testing.T, s *server, agent uuid.UUID) store.TaskInstance {
	t.Helper()
	raw, err := s.a.Tasks.Create(context.Background(), auth.Principal{Kind: auth.KindAgent, ID: agent}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "isolated GitHub capture", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "work", AgentID: agent, Inputs: map[string]workflow.Source{}, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}}}, Edges: []workflow.Edge{}}})
	if err != nil {
		t.Fatal("real Tasks.Create capture failed", err)
	}
	detail := decode[taskmodel.Detail](t, raw)
	instances, err := s.a.Store.TaskInstances(context.Background(), detail.Task.ID)
	if err != nil || len(instances) != 1 || instances[0].ID == detail.Task.ID {
		t.Fatal("task/runtime capture identity invalid", err)
	}
	return instances[0]
}

func githubTaskPayload(t *testing.T, s *server, inst store.TaskInstance) map[string]taskruntime.CredentialPayload {
	t.Helper()
	snapshot, err := taskruntime.Decode(inst.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := snapshot.Credentials()
	if err != nil {
		t.Fatal("original task snapshot credentials invalid", err)
	}
	return credentials.CLI
}

// This native fixture uses the fixed official go-gh socket transport, and an
// OS policy admitting only this Unix socket. No proxy forwarding/DNS/TCP exists.
func githubNativeCapturedCall(t *testing.T, binary, token, wantLogin string) {
	t.Helper()
	home, err := os.MkdirTemp("/private/tmp", "ln-gh-capture-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	socket, deniedSocket := filepath.Join(home, "api"), filepath.Join(home, "denied")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	denied, err := net.Listen("unix", deniedSocket)
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	defer denied.Close()
	var calls atomic.Int32
	var correct atomic.Bool
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		correct.Store(r.Method == "GET" && r.Host == "api.github.com" && r.URL.Path == "/user" && r.URL.RawQuery == "" && r.Header.Get("Authorization") == "token "+token)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 123, "login": wantLogin})
	})}
	go server.Serve(listener)
	defer server.Close()
	configDir := filepath.Join(home, ".config/gh")
	if err = os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(configDir, "config.yml"), []byte("http_unix_socket: "+socket+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if token != "" {
		if err = os.WriteFile(filepath.Join(configDir, "hosts.yml"), []byte("github.com:\n  oauth_token: synthetic-config-trap\n  user: wrong-user\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	hostHome, _ := os.UserHomeDir()
	policy := fmt.Sprintf(`(version 1)(allow default)(deny network*)(allow network-outbound (remote unix-socket (path-literal %q)))(deny file-read* (subpath %q) (subpath %q) (literal %q) (literal %q) (subpath "/Users/Shared/loop-now"))(deny process-exec (literal "/usr/bin/security"))`, socket, filepath.Join(hostHome, ".config/gh"), filepath.Join(hostHome, "Library/Keychains"), filepath.Join(hostHome, ".gitconfig"), filepath.Join(hostHome, ".netrc"))
	profile := filepath.Join(home, "scope.sb")
	if err = os.WriteFile(profile, []byte(policy), 0600); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "GH_CONFIG_DIR=" + configDir, "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1"}
	if token != "" {
		env = append(env, "GH_TOKEN="+token)
	}
	run := func(program string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		argv := append([]string{"-f", profile, program}, args...)
		cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", argv...)
		cmd.Env = env
		return cmd.CombinedOutput()
	}
	for _, gate := range []struct {
		path string
		pass bool
	}{{socket, true}, {deniedSocket, false}} {
		self, selfErr := os.Executable()
		if selfErr != nil {
			t.Fatal(selfErr)
		}
		base := env
		env = append(append([]string{}, base...), "LOOP_NOW_GITHUB_SOCKET_GATE=1", "LOOP_NOW_GITHUB_GATE_SOCKET="+gate.path)
		out, err := run(self)
		env = base
		deniedAsExpected := false
		if exit, ok := err.(*exec.ExitError); ok {
			deniedAsExpected = exit.ExitCode() == 42
		}
		if gate.pass && err != nil || !gate.pass && !deniedAsExpected {
			// Gate output comes only from test-child setup, before any credential
			// or HTTP request exists. Keep this bounded diagnostic actionable.
			if len(out) > 512 {
				out = out[:512]
			}
			t.Fatalf("native fixture Unix policy gate expectedAllowed=%t error=%v diagnostic=%q", gate.pass, err, out)
		}
		clear(out)
	}
	version, err := run(binary, "--version")
	validVersion := err == nil && bytes.Contains(version, []byte("2.100.0"))
	clear(version)
	if !validVersion {
		t.Fatal("fixed native gh version failed under network deny")
	}
	out, err := run(binary, "api", "--hostname", "github.com", "/user")
	defer clear(out)
	if token == "" {
		if err == nil || calls.Load() != 0 {
			t.Fatal("uncaptured identity accessed a provider or inherited auth")
		}
		return
	}
	var user struct{ Login string }
	if err != nil || json.Unmarshal(out, &user) != nil || user.Login != wantLogin || calls.Load() != 1 || !correct.Load() {
		t.Fatal("actual fixed gh did not use only the captured credential")
	}
}

func TestGitHubAppTaskFixedCaptureFakePiAndNative(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("native fixed darwin-arm64 proof only")
	}
	for _, binary := range []string{"/usr/bin/sandbox-exec"} {
		if _, err := os.Stat(binary); err != nil {
			t.Fatal("required offline native policy gate unavailable")
		}
	}
	native := filepath.Join(testenv.RepoRoot(), ".local-build/cli-verification-native-fixture/tools/gh")
	data, err := os.ReadFile(native)
	if err != nil {
		t.Fatal("previously verified official gh artifact unavailable; no download fallback")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != "51f1bd7ed1724774d2c1d91fb4efdb676d3eb200ce95c0021c8544fe7435bc13" {
		t.Fatal("native binary differs from verified official archive member")
	}
	w := githubAppWorld(t)
	bin := filepath.Join(w.cfg.WorkRoot, "tools/gh/2.100.0/bin/gh")
	if err = os.MkdirAll(filepath.Dir(bin), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(bin, data, 0700); err != nil {
		t.Fatal(err)
	}
	clear(data)
	dump := filepath.Join(t.TempDir(), "fakepi-private-dump.json")
	t.Setenv("FAKEPI_DUMP", dump)
	t.Setenv("GH_TOKEN", "synthetic-host-token-trap")
	t.Setenv("GITHUB_TOKEN", "synthetic-host-github-trap")
	t.Setenv("GH_CONFIG_DIR", filepath.Join(t.TempDir(), "host-config-trap"))
	provider := githubtest.New(t)
	s := w.start(func(c *config.Config) {
		c.ExecEnvPassthrough = []string{"FAKEPI_DUMP", "GH_TOKEN", "GITHUB_TOKEN", "GH_CONFIG_DIR"}
	})
	defer s.stop()
	s.a.CLIAuth.GitHub = provider.Client
	ctx := context.Background()
	if err = s.a.Tasks.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	project := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil)).Project.ID
	accounts := "/projects/" + project.String() + "/credential-accounts"
	base := "/projects/" + project.String() + "/cli-auth/operations"
	s.must(201, "POST", accounts, map[string]any{"purpose": "model", "provider": "anthropic", "name": "synthetic", "credential": map[string]string{"type": "api_key", "key": "synthetic-model"}})
	path, agent := s.createAgent("captured GitHub")
	s.must(200, "PUT", path+"/clis/gh", nil)
	s.must(200, "PUT", "/projects/"+project.String()+"/manager", map[string]any{"agentId": agent.ID})
	noAccount := githubCapturedTask(t, s, agent.ID)
	noAccountHash := sha256.Sum256(noAccount.Snapshot)
	if _, present := githubTaskPayload(t, s, noAccount)["gh"]; present {
		t.Fatal("tool-name-only task captured a non-existent credential")
	}
	a := provider.Add("101", "alice", nil, "")
	a.Release()
	firstID := githubAppStart(t, s, base, nil)
	first := githubAppWait(t, s, base, firstID, "succeeded")
	if first.Account == nil {
		t.Fatal("OAuth account missing")
	}
	s.must(202, "POST", path+"/messages", map[string]any{"text": "offline fakePi A"})
	s.waitIdle(path)
	firstEnv := dumpedEnv(t, dump)
	if firstEnv["GH_TOKEN"] != "synthetic-access-1" || firstEnv["GITHUB_TOKEN"] != "" || firstEnv["GH_CONFIG_DIR"] != "" {
		t.Fatal("real local executor injected host state instead of captured A")
	}
	taskA := githubCapturedTask(t, s, agent.ID)
	taskAHash := sha256.Sum256(taskA.Snapshot)
	if githubTaskPayload(t, s, taskA)["gh"].Env["GH_TOKEN"] != "synthetic-access-1" {
		t.Fatal("real Tasks.Create did not capture OAuth A")
	}
	b := provider.Add("202", "bob", nil, "")
	b.Release()
	secondID := githubAppStart(t, s, base, first.Account)
	second := githubAppWait(t, s, base, secondID, "succeeded")
	if second.Account == nil || second.Account.GitHubIdentity.UserID != "202" {
		t.Fatal("different-user OAuth B missing")
	}
	s.must(202, "POST", path+"/messages", map[string]any{"text": "offline fakePi B"})
	s.waitIdle(path)
	secondEnv := dumpedEnv(t, dump)
	if secondEnv["GH_TOKEN"] != "synthetic-access-2" {
		t.Fatal("next local dispatch did not capture B")
	}
	taskB := githubCapturedTask(t, s, agent.ID)
	if githubTaskPayload(t, s, taskB)["gh"].Env["GH_TOKEN"] != "synthetic-access-2" {
		t.Fatal("new task did not capture B")
	}
	for _, old := range []struct {
		inst store.TaskInstance
		hash [32]byte
	}{{noAccount, noAccountHash}, {taskA, taskAHash}} {
		current, err := s.a.Store.TaskInstance(ctx, old.inst.ID)
		if err != nil || sha256.Sum256(current.Snapshot) != old.hash {
			t.Fatal("account replacement retrofitted old task bytes", err)
		}
	}
	if githubTaskPayload(t, s, taskA)["gh"].Env["GH_TOKEN"] != "synthetic-access-1" {
		t.Fatal("old task substituted current B")
	}
	_, unallowed := s.createAgent("unallowed GitHub")
	unallowedPath := "/agents/" + unallowed.ID.String()
	s.must(202, "POST", unallowedPath+"/messages", map[string]any{"text": "offline no credentials"})
	s.waitIdle(unallowedPath)
	if env := dumpedEnv(t, dump); env["GH_TOKEN"] != "" || env["GITHUB_TOKEN"] != "" || env["GH_CONFIG_DIR"] != "" {
		t.Fatal("unallowed fakePi inherited host/project GitHub state")
	}
	platform, err := s.a.Store.CreateAgent(ctx, store.PlatformScope(), "private PAT", "anthropic", "metadata-only", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.a.Store.AddAgentCLI(ctx, platform.ID, "gh"); err != nil {
		t.Fatal(err)
	}
	privatePlain, _ := json.Marshal(clitemplate.Payload{Env: map[string]string{"GH_TOKEN": "synthetic-private-PAT"}, Files: []clitemplate.PayloadFile{}})
	private, err := s.a.Store.PutCredential(ctx, platform.ID, "cli", "gh", store.CredentialMeta{TemplateVersion: "1"}, privatePlain)
	clear(privatePlain)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(private.Secret, []byte("synthetic-private-PAT")) {
		t.Fatal("platform PAT not stored")
	}
	githubNativeCapturedCall(t, bin, firstEnv["GH_TOKEN"], "alice")
	githubNativeCapturedCall(t, bin, githubTaskPayload(t, s, taskA)["gh"].Env["GH_TOKEN"], "alice")
	githubNativeCapturedCall(t, bin, secondEnv["GH_TOKEN"], "bob")
	githubNativeCapturedCall(t, bin, "", "")
	if provider.DeniedDials.Load() != 0 {
		t.Fatal("device fixture attempted an unapproved destination")
	}
	t.Log("actual App/fakePi + fixed native gh2.100.0 under Unix-only OS network policy: normal A/new B, old task A, anonymous denial; old tool-only/task bytes unchanged; no real provider or repository")
}
