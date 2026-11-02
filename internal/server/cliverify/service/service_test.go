package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/cliverify"
	"loop-now/internal/server/store"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type fixture struct {
	s       *Service
	st      *store.Store
	project uuid.UUID
	root    string
}

func (f fixture) account(t *testing.T) (uuid.UUID, uuid.UUID) {
	t.Helper()
	plain, e := json.Marshal(clitemplate.Payload{Env: map[string]string{"GH_TOKEN": "synthetic-captured-token"}, Files: []clitemplate.PayloadFile{}})
	if e != nil {
		t.Fatal(e)
	}
	r, e := f.st.CreateProjectCredential(context.Background(), f.project, "cli", "gh", "fixture", store.ProjectCredentialWrite{AuthMethod: "static", TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1"}, Secret: plain})
	clear(plain)
	if e != nil {
		t.Fatal(e)
	}
	return r.View.ID, *r.View.CurrentVersionID
}
func terminal(t *testing.T, f fixture, a, id uuid.UUID) cliverify.Operation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	for {
		op, e := f.st.GetCLIVerification(ctx, f.project, a, id)
		if e != nil {
			t.Fatal(e)
		}
		if op.Status != "pending" && op.Status != "running" {
			return op
		}
		select {
		case <-ctx.Done():
			t.Fatal("worker did not settle")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestProcessBoundsOutputAndReapsGroup(t *testing.T) {
	for _, kind := range []string{"overflow", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			script := "while :; do printf 'raw-secret-sentinel-0123456789'; done"
			if kind == "cancel" {
				script = "/bin/sleep 30 & wait"
			}
			start := time.Now()
			out := runProcess(ctx, CommandSpec{Executable: "/bin/sh", Args: []string{"-c", script}, Home: t.TempDir(), Environment: []string{"PATH=/usr/bin:/bin"}}, 30*time.Millisecond, nil)
			defer clear(out.Stdout)
			defer clear(out.Stderr)
			if time.Since(start) > 2*time.Second {
				t.Fatal("unbounded process termination")
			}
			if len(out.Stdout) > 65536 || len(out.Stderr) > 65536 {
				t.Fatal("output overflow retained")
			}
			if kind == "overflow" && !out.Overflow {
				t.Fatal("output cap did not stop process")
			}
			if kind == "cancel" && !out.Cancelled {
				t.Fatal("deadline did not stop process group")
			}
		})
	}
}

func TestProcessParentExitDoesNotLeaveTermResistantChild(t *testing.T) {
	home := t.TempDir()
	readyR, readyW, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = readyR.Close() }()
	defer func() { _ = readyW.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("parent not reaped during cleanup")
		}
	}()
	script := `trap 'exit 0' TERM; /bin/sh -c 'trap "" TERM; printf "ready\n" >&3; exec /bin/sleep 30' >/dev/null 2>&1 & wait`
	go func() {
		out := runProcess(ctx, CommandSpec{Executable: "/bin/sh", Args: []string{"-c", script}, Home: home, Environment: []string{"PATH=/usr/bin:/bin"}}, 80*time.Millisecond, func(_ context.Context, spec CommandSpec) (*exec.Cmd, error) {
			cmd := exec.Command(spec.Executable, spec.Args...)
			cmd.ExtraFiles = []*os.File{readyW}
			return cmd, nil
		})
		clear(out.Stdout)
		clear(out.Stderr)
		close(done)
	}()

	if e = readyR.SetReadDeadline(time.Now().Add(2 * time.Second)); e != nil {
		t.Fatal(e)
	}
	reader := bufio.NewReader(readyR)
	line, e := reader.ReadString('\n')
	if e != nil {
		t.Fatal("child start not observed:", e)
	}
	if line != "ready\n" {
		t.Fatalf("unexpected child readiness %q", line)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("parent not reaped")
	}
	// Wait for cmd.Start to finish before closing the parent's ExtraFiles copy.
	// EOF then proves that neither the parent nor its TERM-resistant child retains
	// the inherited descriptor.
	if e = readyW.Close(); e != nil {
		t.Fatal(e)
	}
	if e = readyR.SetReadDeadline(time.Now().Add(2 * time.Second)); e != nil {
		t.Fatal(e)
	}
	if _, e = reader.ReadString('\n'); !errors.Is(e, io.EOF) {
		t.Fatalf("TERM-resistant child survived parent completion: %v", e)
	}
}

func mustGuardPath(t *testing.T) string {
	t.Helper()
	p, e := filepath.Abs("../../../../auth-helper/cli_verify_guard.mjs")
	if e != nil {
		t.Fatal(e)
	}
	return p
}

// testNode 优先用固定版本的 Node；没有时用 PATH 里的 node（CI 装的是 Node 24），都没有就跳过。
func testNode(t *testing.T) string {
	t.Helper()
	const pinned = "/Users/ljl/.local/node-v24.21.0/bin/node"
	if _, e := os.Stat(pinned); e == nil {
		return pinned
	}
	p, e := exec.LookPath("node")
	if e != nil {
		t.Skip("本机没有 node")
	}
	abs, e := filepath.Abs(p)
	if e != nil {
		t.Fatal(e)
	}
	return abs
}

type verificationTransport func(*http.Request) (*http.Response, error)

func (f verificationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
