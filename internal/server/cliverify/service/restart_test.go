package service

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"log/slog"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/cliverify"
	"loop-now/internal/server/store"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestVerificationCrashChild(t *testing.T) {
	if os.Getenv("LOOP_VERIFY_CRASH_FIXTURE") != "1" {
		return
	}
	ctx := context.Background()
	st, e := store.Open(ctx, os.Getenv("LOOP_VERIFY_TEST_URL"), store.OpenOptions{SearchPath: os.Getenv("LOOP_VERIFY_TEST_SCHEMA")})
	if e != nil {
		os.Exit(31)
	}
	catalog, e := clitemplate.Load("")
	if e != nil {
		os.Exit(33)
	}
	root := os.Getenv("LOOP_VERIFY_TEST_ROOT")
	factory := func(_ context.Context, spec CommandSpec) (*exec.Cmd, error) {
		if spec.Phase == "version" {
			return exec.Command("/bin/sh", "-c", "printf 'gh version 2.100.0\\n'"), nil
		}
		code := `import os,socket,json,time,sys; s=socket.socket(); s.bind(('127.0.0.1',0)); s.listen(); open(sys.argv[1],'w').write(json.dumps({'pid':os.getpid(),'port':s.getsockname()[1],'home':os.getcwd()})); time.sleep(60)`
		return exec.Command("/usr/bin/python3", "-S", "-c", code, os.Getenv("LOOP_VERIFY_TEST_READY")), nil
	}
	svc, e := New(ctx, st, catalog, clitemplate.NewInstaller(root, time.Second, nil), nil, Options{WorkRoot: root, NodeBin: os.Getenv("LOOP_VERIFY_TEST_NODE"), GuardEntry: os.Getenv("LOOP_VERIFY_TEST_GUARD"), ProbeTimeout: time.Minute, RecordTimeout: time.Second, StopGrace: 50 * time.Millisecond, CommandFactory: factory}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if e != nil {
		os.Exit(34)
	}
	p, a, v, id := uuid.MustParse(os.Getenv("LOOP_VERIFY_TEST_PROJECT")), uuid.MustParse(os.Getenv("LOOP_VERIFY_TEST_ACCOUNT")), uuid.MustParse(os.Getenv("LOOP_VERIFY_TEST_VERSION")), uuid.MustParse(os.Getenv("LOOP_VERIFY_TEST_OPERATION"))
	if _, e = svc.Start(auth.With(ctx, auth.Console()), p, a, cliverify.Start{OperationID: id, ExpectedVersionID: v}); e != nil {
		os.Exit(35)
	}
	select {}
}
func TestWorkerActualParentSIGKILLThenRecoveryDoesNotReplay(t *testing.T) {
	var probes atomic.Int32
	factory := func(context.Context, CommandSpec) (*exec.Cmd, error) { probes.Add(1); return nil, io.EOF }
	f := newFixture(t, factory, time.Second)
	a, v := f.account(t)
	op := uuid.New()
	ready := filepath.Join(t.TempDir(), "ready.json")
	cmd := exec.Command(os.Args[0], "-test.run=^TestVerificationCrashChild$")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LOOP_VERIFY_CRASH_FIXTURE=1", "LOOP_VERIFY_TEST_URL=" + f.st.Pool.Config().ConnString(), "LOOP_VERIFY_TEST_SCHEMA=" + f.st.Pool.Config().ConnConfig.RuntimeParams["search_path"], "LOOP_VERIFY_TEST_ROOT=" + f.s.Options.WorkRoot, "LOOP_VERIFY_TEST_READY=" + ready, "LOOP_VERIFY_TEST_NODE=" + f.s.Options.NodeBin, "LOOP_VERIFY_TEST_GUARD=" + f.s.Options.GuardEntry, "LOOP_VERIFY_TEST_PROJECT=" + f.project.String(), "LOOP_VERIFY_TEST_ACCOUNT=" + a.String(), "LOOP_VERIFY_TEST_VERSION=" + v.String(), "LOOP_VERIFY_TEST_OPERATION=" + op.String()}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	var waitErr error
	waitDone := make(chan struct{})
	go func() { waitErr = cmd.Wait(); close(waitDone) }()
	defer func() { _ = cmd.Process.Kill(); <-waitDone }()
	var info struct {
		PID, Port int
		Home      string
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case <-waitDone:
			t.Fatal("crash fixture exited before native child was ready:", waitErr)
		default:
		}
		b, e := os.ReadFile(ready)
		if e == nil && json.Unmarshal(b, &info) == nil && info.PID > 0 && info.Port > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("guarded native child did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if e := cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	<-waitDone
	deadline = time.Now().Add(4 * time.Second)
	for {
		_, homeErr := os.Stat(info.Home)
		conn, e := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(info.Port)), 20*time.Millisecond)
		if conn != nil {
			conn.Close()
		}
		if syscall.Kill(info.PID, 0) != nil && os.IsNotExist(homeErr) && e != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("parent SIGKILL left native process/listener/private HOME")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Native exit and HOME removal can precede the EOF guard closing its inherited
	// lease. Wait for that release; startup recovery must then remove the orphan.
	leasePath := filepath.Join(f.s.Options.WorkRoot, ".cli-verification-locks", op.String()+".lock")
	deadline = time.Now().Add(4 * time.Second)
	for {
		lease, openErr := os.OpenFile(leasePath, os.O_RDWR|syscall.O_NOFOLLOW, 0600)
		if openErr == nil {
			lockErr := syscall.Flock(int(lease.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
			_ = lease.Close()
			if lockErr == nil {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("EOF guard did not release orphan lease")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// New real Store/Service startup recovers the original durable operation;
	// no CommandFactory/credentials/probe work can be triggered by its GET.
	next, e := New(context.Background(), f.st, f.s.Templates, f.s.Installer, nil, f.s.Options, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close(context.Background())
	recovered, e := next.Get(auth.With(context.Background(), auth.Console()), f.project, a, op)
	if e != nil || recovered.Status != "failed" || recovered.Result == nil || recovered.Result.ErrorCode == nil || *recovered.Result.ErrorCode != "server_restarted" || probes.Load() != 0 {
		t.Fatal("restart fabricated result or replayed probe", e)
	}
	if _, e = os.Stat(leasePath); !os.IsNotExist(e) {
		t.Fatal("EOF orphan lease not recovered")
	}
}
