package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"log/slog"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/cliverify"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fixture struct {
	s       *Service
	st      *store.Store
	project uuid.UUID
	root    string
}

func newFixture(t *testing.T, factory CommandFactory, timeout time.Duration) fixture {
	t.Helper()
	ctx := context.Background()
	st, e := store.Open(ctx, pgtest.URL(t), store.OpenOptions{SearchPath: pgtest.NewSchema(t)})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(st.Close)
	catalog, e := clitemplate.Load("")
	if e != nil {
		t.Fatal(e)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if e = st.Migrate(ctx, log); e != nil {
		t.Fatal(e)
	}
	defs, e := st.EnsureDefaults(ctx, "fixture", "default")
	if e != nil {
		t.Fatal(e)
	}
	project, e := st.CreateProject(ctx, defs.Organization.ID, "verification")
	if e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "tools/gh/2.100.0/bin")
	if e = os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	// The explicit command fixture runs a real child in place of the native
	// executable; native protocol acceptance is independently covered by harness.
	if e = os.WriteFile(filepath.Join(dir, "gh"), []byte("fixture-not-executable"), 0600); e != nil {
		t.Fatal(e)
	}
	s, e := New(ctx, st, catalog, clitemplate.NewInstaller(root, time.Second, nil), nil, Options{WorkRoot: root, NodeBin: testNode(t), GuardEntry: mustGuardPath(t), ProbeTimeout: timeout, RecordTimeout: time.Second, StopGrace: 50 * time.Millisecond, CommandFactory: factory}, log)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if e = s.Close(c); e != nil {
			t.Error(e)
		}
	})
	return fixture{s, st, project.ID, root}
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
func TestWorkerPersistsFixedProbeAndDoesNotReplay(t *testing.T) {
	var calls atomic.Int32
	factory := func(ctx context.Context, spec CommandSpec) (*exec.Cmd, error) {
		if spec.Phase == "version" {
			return exec.Command("/bin/sh", "-c", "printf 'gh version 2.100.0\\n'"), nil
		}
		calls.Add(1)
		if strings.Join(spec.Args, " ") != "api --hostname github.com --method GET --include user" {
			t.Error("nonfixed command")
		}
		found := false
		for _, v := range spec.Environment {
			if v == "GH_TOKEN=synthetic-captured-token" {
				found = true
			}
		}
		if !found {
			t.Error("missing captured credential")
		}
		return exec.Command("/bin/sh", "-c", `printf 'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{"id":123,"login":"fixture"}\n'`), nil
	}
	f := newFixture(t, factory, time.Second)
	a, v := f.account(t)
	ctx := auth.With(context.Background(), auth.Console())
	in := cliverify.Start{OperationID: uuid.New(), ExpectedVersionID: v}
	if _, e := f.s.Start(ctx, f.project, a, in); e != nil {
		t.Fatal(e)
	}
	op := terminal(t, f, a, in.OperationID)
	if op.Status != "succeeded" || op.ToolVersion == nil || *op.ToolVersion != "2.100.0" || op.Result == nil || op.Result.VerifiedAt == nil {
		t.Fatal("real worker did not persist fixed result", op.Status)
	}
	if _, e := f.s.Start(ctx, f.project, a, in); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 1 {
		t.Fatal("replay repeated probe")
	}
	if _, e := f.s.Start(auth.With(context.Background(), auth.Principal{Kind: auth.KindAgent, ID: uuid.New()}), f.project, a, in); e == nil {
		t.Fatal("Agent management allowed")
	}
	if _, e := os.Stat(filepath.Join(f.s.Options.WorkRoot, ".cli-verification", f.s.InstanceID.String(), in.OperationID.String())); !os.IsNotExist(e) {
		t.Fatal("secret HOME retained")
	}
	leases, e := os.ReadDir(filepath.Join(f.s.Options.WorkRoot, ".cli-verification-locks"))
	if e != nil || len(leases) != 0 {
		t.Fatal("operation lease retained", e)
	}
}
func TestWorkerCancellationRollbackAndTargetChange(t *testing.T) {
	entered := make(chan struct{}, 1)
	factory := func(_ context.Context, spec CommandSpec) (*exec.Cmd, error) {
		if spec.Phase == "version" {
			return exec.Command("/bin/sh", "-c", "printf 'gh version 2.100.0\\n'"), nil
		}
		entered <- struct{}{}
		return exec.Command("/bin/sh", "-c", "exec /bin/sleep 30"), nil
	}
	f := newFixture(t, factory, time.Second)
	a, v := f.account(t)
	ctx := auth.With(context.Background(), auth.Console())
	id := uuid.New()
	if _, e := f.s.Start(ctx, f.project, a, cliverify.Start{OperationID: id, ExpectedVersionID: v}); e != nil {
		t.Fatal(e)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("probe not launched")
	}
	// A failed whole transaction cannot stop a running probe or end its operation.
	// A nil credential is rejected inside the replacement transaction, which rolls back.
	bad := store.ProjectCredentialWrite{AuthMethod: "static", TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1"}}
	if _, e := f.st.MutateProjectCredential(ctx, f.project, a, 1, nil, &bad, false); e == nil {
		t.Fatal("rollback fixture committed")
	}
	op, e := f.st.GetCLIVerification(ctx, f.project, a, id)
	if e != nil || op.Status != "running" {
		t.Fatal("rollback cancelled original", e)
	}
	if _, e = f.st.MutateProjectCredential(ctx, f.project, a, 1, nil, nil, true); e != nil {
		t.Fatal(e)
	}
	op = terminal(t, f, a, id)
	if op.Status != "failed" || op.Result == nil || op.Result.ErrorCode == nil || *op.Result.ErrorCode != "target_changed" {
		t.Fatal("committed delete did not terminate", op.Status)
	}
	f.s.mu.Lock()
	j := f.s.jobs[id]
	f.s.mu.Unlock()
	if j != nil {
		select {
		case <-j.done:
		case <-time.After(f.s.Options.StopGrace + 2*time.Second):
			t.Fatal("committed delete failed to reap child")
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

func TestWorkerUnreadableCredentialCannotExecute(t *testing.T) {
	var probes atomic.Int32
	f := newFixture(t, func(_ context.Context, spec CommandSpec) (*exec.Cmd, error) {
		if spec.Phase == "probe" {
			probes.Add(1)
		}
		return exec.Command("/bin/sh", "-c", "printf 'gh version 2.100.0\\n'"), nil
	}, time.Second)
	a, v := f.account(t)
	if _, e := f.st.Pool.Exec(context.Background(), `UPDATE project_cli_versions SET secret=decode('00010203','hex') WHERE id=$1`, v); e != nil {
		t.Fatal(e)
	}
	id := uuid.New()
	if _, e := f.s.Start(auth.With(context.Background(), auth.Console()), f.project, a, cliverify.Start{OperationID: id, ExpectedVersionID: v}); e != nil {
		t.Fatal(e)
	}
	op := terminal(t, f, a, id)
	if op.Status != "failed" || op.Result == nil || op.Result.ErrorCode == nil || *op.Result.ErrorCode != "configuration_invalid" || probes.Load() != 0 {
		t.Fatal("unreadable credential misclassified or native probe launched")
	}
}

func TestWorkerRecordCommitFailureDoesNotClaimSuccessOrReplay(t *testing.T) {
	var probes atomic.Int32
	f := newFixture(t, func(_ context.Context, spec CommandSpec) (*exec.Cmd, error) {
		if spec.Phase == "version" {
			return exec.Command("/bin/sh", "-c", "printf 'gh version 2.100.0\\n'"), nil
		}
		probes.Add(1)
		return exec.Command("/bin/sh", "-c", `printf 'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{"id":123,"login":"fixture"}\n'`), nil
	}, time.Second)
	a, v := f.account(t)
	ctx := auth.With(context.Background(), auth.Console())
	id := uuid.New()
	if _, e := f.st.Pool.Exec(ctx, `CREATE FUNCTION reject_success_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='succeeded' THEN RAISE EXCEPTION 'synthetic record commit rejected'; END IF; RETURN NEW; END $$; CREATE CONSTRAINT TRIGGER fail_record_commit AFTER UPDATE ON cli_credential_verifications DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_success_commit()`); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.Start(ctx, f.project, a, cliverify.Start{OperationID: id, ExpectedVersionID: v}); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.s.mu.Lock()
		j := f.s.jobs[id]
		f.s.mu.Unlock()
		if j == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("failed result write hung worker")
		}
		time.Sleep(time.Millisecond)
	}
	op, e := f.st.GetCLIVerification(ctx, f.project, a, id)
	if e != nil || op.Status != "running" || op.Result != nil {
		t.Fatal("commit failure fabricated success", e)
	}
	if _, e = f.s.Start(ctx, f.project, a, cliverify.Start{OperationID: id, ExpectedVersionID: v}); e != nil {
		t.Fatal(e)
	}
	if probes.Load() != 1 {
		t.Fatal("record failure replayed external probe")
	}
	if e = f.st.RecoverCLIVerifications(ctx); e != nil {
		t.Fatal(e)
	}
	op, e = f.st.GetCLIVerification(ctx, f.project, a, id)
	if e != nil || op.Status != "failed" || op.Result == nil || op.Result.ErrorCode == nil || *op.Result.ErrorCode != "server_restarted" {
		t.Fatal("restart did not safely end unknown result")
	}
}

func TestRecoveryRespectsHomeLeaseBetweenCommands(t *testing.T) {
	f := newFixture(t, func(context.Context, CommandSpec) (*exec.Cmd, error) { return nil, errors.New("must not execute") }, time.Second)
	op := uuid.New()
	home, held, e := f.s.operationHome(op)
	if e != nil {
		t.Fatal(e)
	}
	var next Service
	next.Options = f.s.Options
	next.InstanceID = uuid.New()
	if e = next.privateRoots(); e != nil {
		t.Fatal(e)
	}
	if e = next.recoverHomes(); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(home); e != nil {
		t.Fatal("startup deleted still leased HOME", e)
	}
	// Equivalent to the original Go owner disappearing between commands: no
	// active guard, but its inherited lifetime lock is only released now.
	if e = held.Close(); e != nil {
		t.Fatal(e)
	}
	if e = next.recoverHomes(); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(home); !os.IsNotExist(e) {
		t.Fatal("unlocked old HOME was not recovered")
	}
	if _, e = os.Stat(held.Name()); !os.IsNotExist(e) {
		t.Fatal("old external lease was not recovered")
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

func TestNon24NodeRejectsOnlyNewStartBeforeRegistrationOrTools(t *testing.T) {
	var commands atomic.Int32
	f := newFixture(t, func(_ context.Context, spec CommandSpec) (*exec.Cmd, error) {
		commands.Add(1)
		if spec.Phase == "version" {
			return exec.Command("/bin/sh", "-c", "printf 'gh version 2.100.0\\n'"), nil
		}
		return exec.Command("/bin/sh", "-c", `printf 'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{"id":123,"login":"fixture"}\n'`), nil
	}, time.Second)
	a, v := f.account(t)
	ctx := auth.With(context.Background(), auth.Console())
	old := uuid.New()
	if _, e := f.s.Start(ctx, f.project, a, cliverify.Start{OperationID: old, ExpectedVersionID: v}); e != nil {
		t.Fatal(e)
	}
	if op := terminal(t, f, a, old); op.Status != "succeeded" {
		t.Fatal("positive24 did not work")
	}
	commands.Store(0)
	node := filepath.Join(t.TempDir(), "node25")
	if e := os.WriteFile(node, []byte("#!/bin/sh\nprintf 'v25.9.0\\n'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	options := f.s.Options
	options.NodeBin = node
	next, e := New(context.Background(), f.st, f.s.Templates, f.s.Installer, nil, options, nil)
	if e != nil {
		t.Fatal("unsupported component blocked whole service construction", e)
	}
	defer next.Close(context.Background())
	if _, e = next.Start(ctx, f.project, a, cliverify.Start{OperationID: uuid.New(), ExpectedVersionID: v}); !apperr.Is(e, apperr.CodeVerificationUnsupported) {
		t.Fatal("non24 did not return formal unsupported", e)
	}
	if op, e := next.Get(ctx, f.project, a, old); e != nil || op.Status != "succeeded" {
		t.Fatal("runtime unavailable hid history", e)
	}
	if _, e = next.State(ctx, f.project, a); e != nil {
		t.Fatal(e)
	}
	if _, e = next.Cancel(ctx, f.project, a, old); e != nil {
		t.Fatal(e)
	}
	if _, e = next.Start(ctx, f.project, a, cliverify.Start{OperationID: old, ExpectedVersionID: v}); e != nil {
		t.Fatal("historical same-id recovery required ready runtime", e)
	}
	var count int
	if e = f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM cli_credential_verifications`).Scan(&count); e != nil || count != 1 || commands.Load() != 0 {
		t.Fatal("non24 registered or prepared/probed", e)
	}
}

func TestRegistryRejectsSameNameOverrideBeforeRegistration(t *testing.T) {
	var calls atomic.Int32
	f := newFixture(t, func(context.Context, CommandSpec) (*exec.Cmd, error) { calls.Add(1); return nil, io.EOF }, time.Second)
	a, v := f.account(t)
	template, _ := f.s.Templates.Get("gh")
	template.Env = map[string]string{"GH_PROMPT_DISABLED": "1", "BASH_ENV": "/must-not-execute"}
	_, e := f.s.Start(auth.With(context.Background(), auth.Console()), f.project, a, cliverify.Start{OperationID: uuid.New(), ExpectedVersionID: v})
	if !apperr.Is(e, apperr.CodeVerificationUnsupported) {
		t.Fatal("same-name template override accepted", e)
	}
	var count int
	if e = f.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM cli_credential_verifications`).Scan(&count); e != nil || count != 0 || calls.Load() != 0 {
		t.Fatal("untrusted template reached registration/tool")
	}
}
func TestWorkerWrongActualVersionAndRawFailureStaySafe(t *testing.T) {
	for _, kind := range []string{"version", "provider_error"} {
		t.Run(kind, func(t *testing.T) {
			var probes atomic.Int32
			f := newFixture(t, func(_ context.Context, spec CommandSpec) (*exec.Cmd, error) {
				if spec.Phase == "version" {
					version := "2.100.0"
					if kind == "version" {
						version = "99.0.0"
					}
					return exec.Command("/bin/sh", "-c", "printf 'gh version "+version+"\\n'"), nil
				}
				probes.Add(1)
				return exec.Command("/bin/sh", "-c", `printf 'raw-secret-sentinel\n' >&2; exit 1`), nil
			}, time.Second)
			var logs bytes.Buffer
			f.s.log = slog.New(slog.NewTextHandler(&logs, nil))
			a, v := f.account(t)
			ctx := auth.With(context.Background(), auth.Console())
			id := uuid.New()
			if _, e := f.s.Start(ctx, f.project, a, cliverify.Start{OperationID: id, ExpectedVersionID: v}); e != nil {
				t.Fatal(e)
			}
			op := terminal(t, f, a, id)
			expected := "probe_failed"
			if kind == "version" {
				expected = "tool_unsupported"
			}
			if op.Status != "failed" || op.Result == nil || op.Result.ErrorCode == nil || *op.Result.ErrorCode != expected {
				t.Fatal("wrong fixed failure category")
			}
			raw, e := json.Marshal(op)
			if e != nil {
				t.Fatal(e)
			}
			if bytes.Contains(raw, []byte("raw-secret-sentinel")) || strings.Contains(logs.String(), "raw-secret-sentinel") {
				t.Fatal("native raw failure leaked")
			}
			if kind == "version" && (probes.Load() != 0 || op.ToolVersion != nil) {
				t.Fatal("wrong actual version reached online probe")
			}
			account, e := f.st.GetProjectCredential(ctx, f.project, a)
			if e != nil || account.View.Revision != 1 || account.View.Status != "saved" || *account.View.CurrentVersionID != v {
				t.Fatal("probe changed account authority", e)
			}
		})
	}
}
func TestWorkerProbeDeadlinePersistsTimeout(t *testing.T) {
	f := newFixture(t, func(_ context.Context, spec CommandSpec) (*exec.Cmd, error) {
		if spec.Phase == "version" {
			return exec.Command("/bin/sh", "-c", "printf 'gh version 2.100.0\\n'"), nil
		}
		return exec.Command("/bin/sleep", "30"), nil
	}, 80*time.Millisecond)
	a, v := f.account(t)
	id := uuid.New()
	if _, e := f.s.Start(auth.With(context.Background(), auth.Console()), f.project, a, cliverify.Start{OperationID: id, ExpectedVersionID: v}); e != nil {
		t.Fatal(e)
	}
	op := terminal(t, f, a, id)
	if op.Status != "failed" || op.Result == nil || op.Result.ErrorCode == nil || *op.Result.ErrorCode != "timeout" {
		t.Fatal("native deadline was not safely persisted")
	}
}

type verificationTransport func(*http.Request) (*http.Response, error)

func (f verificationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestWorkerToolDigestFailureNeverFallsBackToHost(t *testing.T) {
	var commands, downloads atomic.Int32
	f := newFixture(t, func(context.Context, CommandSpec) (*exec.Cmd, error) { commands.Add(1); return nil, io.EOF }, time.Second)
	if e := os.RemoveAll(filepath.Join(f.s.Options.WorkRoot, "tools")); e != nil {
		t.Fatal(e)
	}
	f.s.Installer = clitemplate.NewInstaller(f.s.Options.WorkRoot, time.Second, &http.Client{Transport: verificationTransport(func(r *http.Request) (*http.Response, error) {
		downloads.Add(1)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("synthetic-wrong-package-sha")), Request: r}, nil
	})})
	trap := t.TempDir()
	marker := filepath.Join(trap, "executed")
	if e := os.WriteFile(filepath.Join(trap, "gh"), []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", trap+":/usr/bin:/bin")
	a, v := f.account(t)
	id := uuid.New()
	if _, e := f.s.Start(auth.With(context.Background(), auth.Console()), f.project, a, cliverify.Start{OperationID: id, ExpectedVersionID: v}); e != nil {
		t.Fatal(e)
	}
	op := terminal(t, f, a, id)
	if op.Status != "failed" || op.Result == nil || op.Result.ErrorCode == nil || *op.Result.ErrorCode != "tool_prepare_failed" || downloads.Load() != 1 || commands.Load() != 0 {
		t.Fatal("tampered package reached native or wrong failure")
	}
	if _, e := os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("host CLI fallback ran")
	}
	template, _ := f.s.Templates.Get("gh")
	status, _ := f.s.Installer.Status(template)
	if status == clitemplate.StateInstalled {
		t.Fatal("tampered archive published installed")
	}
}
func TestWorkerDecryptedUnknownEnvironmentCannotExecute(t *testing.T) {
	var probes atomic.Int32
	f := newFixture(t, func(_ context.Context, spec CommandSpec) (*exec.Cmd, error) {
		if spec.Phase == "probe" {
			probes.Add(1)
		}
		return exec.Command("/bin/sh", "-c", "printf 'gh version 2.100.0\\n'"), nil
	}, time.Second)
	raw, _ := json.Marshal(clitemplate.Payload{Env: map[string]string{"GH_TOKEN": "synthetic", "BASH_ENV": "/must-not-execute"}, Files: []clitemplate.PayloadFile{}})
	r, e := f.st.CreateProjectCredential(context.Background(), f.project, "cli", "gh", "bad fixture payload", store.ProjectCredentialWrite{AuthMethod: "static", TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1"}, Secret: raw})
	clear(raw)
	if e != nil {
		t.Fatal(e)
	}
	id := uuid.New()
	if _, e = f.s.Start(auth.With(context.Background(), auth.Console()), f.project, r.View.ID, cliverify.Start{OperationID: id, ExpectedVersionID: *r.View.CurrentVersionID}); e != nil {
		t.Fatal(e)
	}
	op := terminal(t, f, r.View.ID, id)
	if op.Status != "failed" || op.Result == nil || op.Result.ErrorCode == nil || *op.Result.ErrorCode != "configuration_invalid" || probes.Load() != 0 {
		t.Fatal("unknown payload environment reached execution")
	}
}
