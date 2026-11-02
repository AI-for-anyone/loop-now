package modelauth

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/modelcredential"
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
