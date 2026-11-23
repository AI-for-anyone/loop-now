package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/app"
	"loop-now/internal/server/config"
	"loop-now/internal/server/preflight"
	"loop-now/internal/testutil/fakepibin"
	"loop-now/internal/testutil/loopctlfake"
)

// world is a set of external state that can be shared across several server instances: database schema, Redis prefix,
// work root directory.
type world struct {
	t       *testing.T
	cfg     config.Config
	piAIDir string
	logTo   io.Writer // logs are discarded when nil
}

func newWorld(t *testing.T) *world {
	t.Helper()
	real, err := preflight.Resolve(context.Background(), preflight.Options{PiBin: "pi", PiVersion: "1.4.0", NodeBin: "node",
		WorkRoot: filepath.Join(t.TempDir(), "probe"), HomeDirs: []string{"/nonexistent-home"}})
	if err != nil {
		t.Skipf("no usable Pi 1.4.0 on this machine (needed to read the model catalog): %v", err)
	}
	return &world{t: t, piAIDir: real.PiAIDir, cfg: config.Config{
		TaskMaxSteps: 100, TaskMaxEdges: 300, TaskMaxVisits: 1000, TaskMaxRequest: 1 << 20, TaskMaxArtifact: 20 << 20, TaskMaxSession: 100 << 20, TaskReconcileInterval: 100 * time.Millisecond, TaskEventPoll: 10 * time.Millisecond, TaskRecordTimeout: time.Second,
		DataDir:    t.TempDir(),
		ListenAddr: "127.0.0.1:0", AllowedHosts: []string{"localhost", "127.0.0.1", "::1"}, ConsoleOrigins: []string{"http://localhost:5173"},
		WorkRoot: filepath.Join(t.TempDir(), "work"), DefaultOrgName: "Default org", DefaultProjectName: "Default project",
		PiBin: fakepibin.Path(t), PiVersion: "1.4.0", PiMaxProcesses: 2, PiIdleTimeout: time.Minute, PiStartTimeout: 10 * time.Second,
		PiStopGrace: 2 * time.Second, AbortTimeout: 3 * time.Second, GitCloneTimeout: 30 * time.Second, CLIInstallTimeout: time.Minute,
		NodeBin: "node", PiAIDir: real.PiAIDir, ReconcileInterval: 500 * time.Millisecond,
		SSEKeepalive: time.Second, EventStreamMaxLen: 1000, ShutdownTimeout: 10 * time.Second,
		MaxUploadFile: 1 << 20, MaxSkillArchive: 1 << 20, MaxSkillEntries: 50, MaxCredential: 4096, MaxMessage: 1 << 16,
		TextEditExtensions: []string{".md"}, TextEditMax: 1 << 16, MaxInboxMessage: 1 << 16, InboxPreviewChars: 200, InboxPageMax: 100,
		LoopctlDistDir: loopctlfake.Dir(t),
	}}
}

type server struct {
	t   *testing.T
	a   *app.App
	url string
}

func (w *world) start(mutate func(c *config.Config)) *server {
	w.t.Helper()
	cfg := w.cfg
	if mutate != nil {
		mutate(&cfg)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		w.t.Fatal(err)
	}
	logTo := w.logTo
	if logTo == nil {
		logTo = io.Discard
	}
	a, err := app.Build(context.Background(), &cfg, config.NewLogger(logTo, cfg.LogFormat, cfg.LogLevel),
		app.Options{Listener: ln, HomeDirs: []string{"/nonexistent-home"}})
	if err != nil {
		w.t.Fatalf("failed to start the server: %v", err)
	}
	go func() { _ = a.Serve() }()
	return &server{t: w.t, a: a, url: "http://" + a.Addr() + "/api/v1"}
}

func (s *server) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.a.Shutdown(ctx); err != nil {
		s.t.Errorf("error stopping the server: %v", err)
	}
}

func (s *server) call(method, path string, body any) (int, []byte) {
	s.t.Helper()
	var r io.Reader
	ct := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		r, ct = bytes.NewReader(b), "application/octet-stream"
	default:
		j, _ := json.Marshal(b)
		r, ct = bytes.NewReader(j), "application/json"
	}
	req, _ := http.NewRequest(method, s.url+path, r)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func (s *server) must(status int, method, path string, body any) []byte {
	s.t.Helper()
	got, out := s.call(method, path, body)
	if got != status {
		s.t.Fatalf("%s %s: want %d, got %d: %s", method, path, status, got, out)
	}
	return out
}

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("failed to parse the response: %v: %s", err, b)
	}
	return v
}

func (s *server) createAgent(name string) (string, apitypes.Agent) {
	s.t.Helper()
	providers := decode[apitypes.ModelProviderList](s.t, s.must(200, "GET", "/model-providers", nil))
	provider := ""
	for _, p := range providers.Providers {
		if p.ID == "anthropic" {
			provider = p.ID
			break
		}
	}
	if provider == "" {
		s.t.Fatal("installed Pi fixture requires Anthropic catalog metadata")
	}
	models := decode[apitypes.ModelList](s.t, s.must(200, "GET", "/model-providers/"+provider+"/models", nil))
	ctx := decode[apitypes.Context](s.t, s.must(200, "GET", "/context", nil))
	ag := decode[apitypes.Agent](s.t, s.must(201, "POST", "/projects/"+ctx.Project.ID.String()+"/agents", map[string]any{"name": name,
		"model": map[string]string{"provider": provider, "modelId": models.Models[0].ID}, "prompt": "You are a test assistant"}))
	return "/agents/" + ag.ID.String(), ag
}

// waitIdle waits until the Agent is fully idle: no running or queued runs, and the busy lock has been handed over and
// released (there is a short window between a run writing its end to the database and handing over the lock; a message
// sent right after checking only the status may be judged busy).
func (s *server) waitIdle(a string) apitypes.AgentStatus {
	s.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st := decode[apitypes.AgentStatus](s.t, s.must(200, "GET", a+"/status", nil))
		owner, err := s.a.Coord.LockOwner(context.Background(), strings.TrimPrefix(a, "/agents/"))
		if !st.Busy && st.Queued.Interrupt == 0 && st.Queued.Normal == 0 && err == nil && owner == "" {
			return st
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.t.Fatal("timed out waiting for idle")
	return apitypes.AgentStatus{}
}

func (s *server) waitRunning(a string) apitypes.Run {
	s.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		st := decode[apitypes.AgentStatus](s.t, s.must(200, "GET", a+"/status", nil))
		if st.CurrentRun != nil && st.CurrentRun.Status == "running" {
			return *st.CurrentRun
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.t.Fatal("timed out waiting for the run to start")
	return apitypes.Run{}
}

func (s *server) replies(a string) []apitypes.Message {
	s.t.Helper()
	page := decode[apitypes.MessagePage](s.t, s.must(200, "GET", a+"/messages?limit=200", nil))
	var out []apitypes.Message
	for _, m := range page.Messages {
		if m.Role == "assistant" {
			out = append(out, m)
		}
	}
	return out
}

func fakepiAlive(t *testing.T, workRoot string) bool {
	out, _ := exec.Command("pgrep", "-f", workRoot).Output()
	return strings.TrimSpace(string(out)) != ""
}

// TestEndToEndWithFakePi is the scripted acceptance test for task 13.1.
func TestEndToEndWithFakePi(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	defer s.stop()

	a, ag := s.createPlatformAgent("acceptance assistant")
	tmpl := decode[apitypes.SkillTemplate](t, s.must(201, "POST", "/skills", map[string]any{"skillMd": "---\nname: notes\ndescription: Take notes\n---\n"}))
	s.must(201, "POST", a+"/skills", map[string]any{"templateId": tmpl.ID})
	s.must(200, "PUT", a+"/folder/content?path=docs/guide/a.md", []byte("reference material"))
	s.must(200, "PUT", a+"/clis/jq", nil)
	s.must(200, "PUT", a+"/model-credential", map[string]any{"provider": "anthropic", "key": "sk-test"})

	s.must(202, "POST", a+"/messages", map[string]any{"text": "hello"})
	st := s.waitIdle(a)
	if r := s.replies(a); len(r) != 1 || !strings.Contains(r[0].Text, "hello") || r[0].Run.Status != "completed" {
		t.Fatalf("unexpected first reply: %+v", r)
	}
	agentRoot := filepath.Join(w.cfg.WorkRoot, "agents", ag.ID.String())
	if b, err := os.ReadFile(filepath.Join(agentRoot, "workspace", "docs", "guide", "a.md")); err != nil || string(b) != "reference material" {
		t.Fatalf("the uploaded file should be written straight into the Agent folder: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(agentRoot, "skills", "notes", "SKILL.md")); err != nil {
		t.Fatal("the Skill should be synced")
	}
	if st.Session.State != "active" {
		t.Fatalf("the session should be recorded once its file is written: %+v", st.Session)
	}

	// Queue and interrupt while busy.
	s.must(202, "POST", a+"/messages", map[string]any{"text": "!sleep=1s a very long reply"})
	s.waitRunning(a)
	s.must(202, "POST", a+"/messages", map[string]any{"text": "queued", "whenBusy": "queue"})
	s.must(202, "POST", a+"/messages", map[string]any{"text": "interrupting", "whenBusy": "interrupt"})
	s.waitIdle(a)
	r := s.replies(a)
	if len(r) != 4 || r[1].Run.Status != "interrupted" || !strings.Contains(r[2].Text, "interrupting") || !strings.Contains(r[3].Text, "queued") {
		var b strings.Builder
		for _, m := range r {
			fmt.Fprintf(&b, "%s|%s; ", m.Run.Status, m.Text)
		}
		t.Fatalf("wrong order of interrupt and queue: %s", b.String())
	}

	s.must(204, "DELETE", a, nil)
	if _, err := os.Stat(filepath.Join(agentRoot, "home")); !os.IsNotExist(err) {
		t.Fatal("the Agent's own HOME should be removed after deleting the Agent")
	}
	if _, err := os.Stat(filepath.Join(agentRoot, "workspace", "docs")); err != nil {
		t.Fatal("the workspace should be kept after deleting the Agent")
	}
}

// TestRestartGraceful covers task 11.2 (normal stop).
func TestRestartGraceful(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	a, _ := s.createAgent("restart test")
	s.must(202, "POST", a+"/messages", map[string]any{"text": "!sleep=10s very long"})
	run := s.waitRunning(a)
	time.Sleep(300 * time.Millisecond)
	// Reserved for multiple instances: the lock value, run records and pi.pid all carry this instance's ID.
	inst := s.a.InstanceID.String()
	agentID := strings.TrimPrefix(a, "/agents/")
	if owner, _ := s.a.Coord.LockOwner(context.Background(), agentID); owner != run.ID.String()+"|"+inst {
		t.Fatalf("the lock value should be <run ID>|<instance ID>: %s", owner)
	}
	var runInst string
	if err := s.a.Store.Pool.QueryRow(context.Background(), `SELECT instance_id FROM runs WHERE id = $1`, run.ID).Scan(&runInst); err != nil || runInst != inst {
		t.Fatalf("runs.instance_id should be this instance: %s %v", runInst, err)
	}
	pidJSON, err := os.ReadFile(filepath.Join(w.cfg.WorkRoot, "agents", agentID, "pi.pid"))
	if err != nil || !strings.Contains(string(pidJSON), inst) {
		t.Fatalf("pi.pid should carry this instance ID: %s %v", pidJSON, err)
	}
	s.must(202, "POST", a+"/messages", map[string]any{"text": "queued", "whenBusy": "queue"})
	s.stop()
	if fakepiAlive(t, w.cfg.WorkRoot) {
		t.Fatal("no Pi process should be left after a normal stop")
	}

	s2 := w.start(nil)
	defer s2.stop()
	s2.waitIdle(a)
	r := s2.replies(a)
	if len(r) != 2 || r[0].Run.ID != run.ID || r[0].Run.Status != "interrupted" || *r[0].Run.EndReason != "server_restart" || r[0].Text == "" {
		t.Fatalf("the original reply should be marked as interrupted by a server restart and keep its text: %+v", r)
	}
	if !strings.Contains(r[1].Text, "queued") || r[1].Run.Status != "completed" {
		t.Fatalf("the queued message should be handled as usual: %+v", r[1])
	}
}

// TestRestartAfterCrash covers task 11.2 (forced kill): leftover Pi processes are ended at the next startup.
func TestRestartAfterCrash(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	a, _ := s.createAgent("crash test")
	s.must(202, "POST", a+"/messages", map[string]any{"text": "!sleep=30s very long"})
	s.waitRunning(a)
	// Simulate a forced kill: no graceful exit and no ending of Pi processes; only stop conversation coordination and
	// HTTP, and release the instance locks as a process exit would.
	_ = s.a.Conv.Stop(context.Background())
	s.a.ReleaseInstanceLocks()
	if !fakepiAlive(t, w.cfg.WorkRoot) {
		t.Fatal("the Pi process should still be running during the simulated crash")
	}
	s2 := w.start(nil)
	defer s2.stop()
	s2.waitIdle(a)
	if fakepiAlive(t, w.cfg.WorkRoot) {
		// The new instance might have started a process for a later message; there is no later message here, so there
		// should be no Pi process at all.
		t.Fatal("the next start should end leftover Pi processes")
	}
	if r := s2.replies(a); len(r) != 1 || *r[0].Run.EndReason != "server_restart" {
		t.Fatalf("the leftover run should be marked as interrupted by a server restart: %+v", r)
	}
	_ = s.a.Executor.Shutdown(context.Background())
}

// TestSessionFiles covers task 11.4.
func TestSessionFiles(t *testing.T) {
	w := newWorld(t)
	s := w.start(func(c *config.Config) { c.PiIdleTimeout = 300 * time.Millisecond })
	defer s.stop()
	a, _ := s.createAgent("session test")

	// The first message is rejected by Pi and no session file is written: this does not count as lost later.
	s.must(202, "POST", a+"/messages", map[string]any{"text": "!rejectprompt !nosession"})
	st := s.waitIdle(a)
	if st.Session.State != "none" {
		t.Fatalf("a session never written to disk should not be recorded: %+v", st.Session)
	}
	time.Sleep(800 * time.Millisecond) // wait for the idle exit
	s.must(202, "POST", a+"/messages", map[string]any{"text": "second message"})
	st = s.waitIdle(a)
	if r := s.replies(a); r[len(r)-1].Run.Status != "completed" || st.Session.State != "active" {
		t.Fatalf("should reply normally from a new session: %+v %+v", r[len(r)-1], st.Session)
	}

	// A session file already written to disk is deleted: it is judged lost, and recovers after confirming a reopen.
	time.Sleep(800 * time.Millisecond)
	matches, _ := filepath.Glob(filepath.Join(w.cfg.WorkRoot, "agents", "*", "sessions", "*.jsonl"))
	for _, m := range matches {
		_ = os.Remove(m)
	}
	s.must(202, "POST", a+"/messages", map[string]any{"text": "third message"})
	st = s.waitIdle(a)
	if r := s.replies(a); *r[len(r)-1].Run.ErrorCode != "session_missing" || st.Session.State != "missing" {
		t.Fatalf("the session should be judged missing after its file is deleted: %+v", st.Session)
	}
	if code, _ := s.call("POST", a+"/messages", map[string]any{"text": "fourth message"}); code != http.StatusConflict {
		t.Fatalf("new messages should be refused while the session is missing: %d", code)
	}
	sess := decode[apitypes.SessionInfo](t, s.must(200, "POST", a+"/session/reset", nil))
	if sess.Epoch != 2 {
		t.Fatalf("the session epoch should go up by one: %+v", sess)
	}
	s.must(202, "POST", a+"/messages", map[string]any{"text": "fifth message"})
	s.waitIdle(a)
	if r := s.replies(a); r[len(r)-1].Run.Status != "completed" || r[len(r)-1].SessionEpoch != 2 {
		t.Fatalf("should reply normally after the reset: %+v", r[len(r)-1])
	}
}

// Project layer: after the default project is renamed and the server restarts, the name is kept (not reverted to the
// config or created anew); deleting a project stops the Pi processes of its Agents and keeps the working folders.
func TestProjectsWithFakePi(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	ctx := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	s.must(200, "PATCH", "/projects/"+ctx.Project.ID.String(), map[string]any{"name": "Main project"})

	p := decode[apitypes.Project](t, s.must(201, "POST", "/projects", map[string]any{"name": "Scratch"}))
	providers := decode[apitypes.ModelProviderList](t, s.must(200, "GET", "/model-providers", nil))
	models := decode[apitypes.ModelList](t, s.must(200, "GET", "/model-providers/"+providers.Providers[0].ID+"/models", nil))
	ag := decode[apitypes.Agent](t, s.must(201, "POST", "/projects/"+p.ID.String()+"/agents", map[string]any{"name": "Agent A",
		"model": map[string]string{"provider": providers.Providers[0].ID, "modelId": models.Models[0].ID}}))
	a := "/agents/" + ag.ID.String()
	s.must(202, "POST", a+"/messages", map[string]any{"text": "hello"})
	s.waitIdle(a)
	agentRoot := filepath.Join(w.cfg.WorkRoot, "agents", ag.ID.String())
	if !fakepiAlive(t, agentRoot) {
		t.Fatal("Pi should stay running after the reply")
	}
	s.must(204, "DELETE", "/projects/"+p.ID.String(), nil)
	deadline := time.Now().Add(5 * time.Second)
	for fakepiAlive(t, agentRoot) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if fakepiAlive(t, agentRoot) {
		t.Fatal("deleting the project should stop the Pi processes of its Agents")
	}
	if _, err := os.Stat(filepath.Join(agentRoot, "workspace")); err != nil {
		t.Fatalf("working folders should be kept after deleting the project: %v", err)
	}
	s.stop()

	s2 := w.start(nil)
	defer s2.stop()
	if got := decode[apitypes.Context](t, s2.must(200, "GET", "/context", nil)); got.Project.ID != ctx.Project.ID || got.Project.Name != "Main project" {
		t.Fatalf("after the restart the default project should be the same one and keep its new name: %+v", got.Project)
	}
	if l := decode[apitypes.ProjectList](t, s2.must(200, "GET", "/projects", nil)); len(l.Projects) != 1 {
		t.Fatalf("no extra project should appear after the restart: %+v", l)
	}
}

func (s *server) createPlatformAgent(name string) (string, apitypes.Agent) {
	s.t.Helper()
	providers := decode[apitypes.ModelProviderList](s.t, s.must(200, "GET", "/model-providers", nil))
	provider := ""
	for _, p := range providers.Providers {
		if p.ID == "anthropic" {
			provider = p.ID
			break
		}
	}
	if provider == "" {
		s.t.Fatal("installed Pi fixture requires Anthropic catalog metadata")
	}
	models := decode[apitypes.ModelList](s.t, s.must(200, "GET", "/model-providers/"+provider+"/models", nil))

	ag := decode[apitypes.Agent](s.t, s.must(201, "POST", "/platform/agents", map[string]any{"name": name,
		"model": map[string]string{"provider": provider, "modelId": models.Models[0].ID}, "prompt": "You are a test assistant"}))
	return "/agents/" + ag.ID.String(), ag
}
