package larksidecar

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
)

type nativeRecoveryFixtureMessage struct {
	Phase     string
	Record    NativeRecoveryRecord
	Native    *NativeProcessIdentity // fixture cleanup only, NOT a recovery capture
	Workspace string
}

// Owned child server process. Both IPC descriptors are CLOEXEC before native
// launch; native receives neither descriptor, exec capability nor broker FD.
func TestNativeRecoveryOwnerProcess(t *testing.T) {
	if os.Getenv("LARK_RECOVERY_OWNED_CHILD") != "1" {
		return
	}
	syscall.CloseOnExec(3)
	syscall.CloseOnExec(4)
	out := os.NewFile(3, "owned-record")
	control := os.NewFile(4, "owned-control")
	defer out.Close()
	defer control.Close()
	cfg := managedConfig(t)
	cfg.HostHome = os.Getenv("LARK_RECOVERY_HOST_HOME")
	m := NewManager()
	binding := testBinding()
	absent := uuid.New()
	var scope *ManagedScope
	var once atomic.Bool
	source := TokenSourceFunc(func(context.Context) (string, error) {
		if once.CompareAndSwap(false, true) {
			if g, err := m.AcquireRuntimeGate(context.Background(), []uuid.UUID{uuid.MustParse(binding.RuntimeID), absent}); !errors.Is(err, ErrNativeBusy) || g != nil {
				t.Error("actual native activity was not busy")
			}
			m.mu.Lock()
			var native *NativeProcessIdentity
			for _, identity := range scope.processes {
				p := identity
				native = &p
			}
			m.mu.Unlock()
			if native == nil {
				t.Error("actual started native identity missing")
			}
			if json.NewEncoder(out).Encode(nativeRecoveryFixtureMessage{Phase: "active", Native: native}) != nil {
				t.Error("owned ready frame")
			}
			var b [1]byte
			_, _ = control.Read(b[:])
		}
		return "", errors.New("synthetic unavailable") // No upstream HTTP at all.
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	registered, err := m.OpenScope(ctx, binding, source, cfg, Limits{})
	if err != nil {
		t.Fatal("actual scope admission")
	}
	m.mu.Lock()
	scope = registered
	m.mu.Unlock()
	defer closeManaged(t, scope)
	code, _ := RunLauncher(ctx, scope.ExecURL(), scope.ExecCapability(), []string{"api", "GET", "/open-apis/authen/v1/user_info", "--as", "user"}, io.Discard, io.Discard)
	if code == 0 || !once.Load() {
		t.Fatal("synthetic rejection did not traverse real native RPC")
	}
	var gate *RuntimeGate
	for ctx.Err() == nil {
		gate, err = m.AcquireRuntimeGate(ctx, []uuid.UUID{uuid.MustParse(binding.RuntimeID), absent})
		if err == nil {
			break
		}
		if !errors.Is(err, ErrNativeBusy) {
			t.Fatal("original gate acquisition")
		}
		time.Sleep(time.Millisecond)
	}
	if gate == nil {
		t.Fatal("actual native did not drain")
	}
	action := uuid.MustParse(os.Getenv("LARK_RECOVERY_ACTION"))
	record, err := gate.CaptureRecovery(ctx, action)
	if err != nil {
		t.Fatal("original recovery capture")
	}
	if record.Members[0].Started == 0 || record.Members[0].Started != record.Members[0].Confirmed || !validNativeGroup(record.Members[0].LastNative) {
		t.Fatal("actual native Start/Wait/group proof missing")
	}
	marker := filepath.Join(cfg.WorkingDir, "synthetic-preserved")
	if os.WriteFile(marker, []byte("retained"), 0600) != nil {
		t.Fatal("owned marker")
	}
	if json.NewEncoder(out).Encode(nativeRecoveryFixtureMessage{Phase: "captured", Record: record, Workspace: cfg.WorkingDir}) != nil {
		t.Fatal("owned captured frame")
	}
	var b [1]byte
	_, _ = control.Read(b[:])
	os.Exit(0) // Simulated server crash: deliberately bypass managed Close/Wait.
}

func ownedNativeRecoveryRecord(t *testing.T) (NativeRecoveryRecord, func(), string) {
	t.Helper()
	cfg := managedConfig(t)
	host, err := os.UserHomeDir()
	if err != nil {
		t.Fatal("host path metadata")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("owned root")
	}
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal("owned result pipe")
	}
	controlRead, controlWrite, err := os.Pipe()
	if err != nil {
		read.Close()
		write.Close()
		t.Fatal("owned control pipe")
	}
	action := uuid.New()
	cmd := exec.Command(os.Args[0], "-test.run=^TestNativeRecoveryOwnerProcess$", "-test.timeout=20s")
	cmd.Env = []string{"LARK_RECOVERY_OWNED_CHILD=1", "LARK_RECOVERY_ACTION=" + action.String(), "LARK_RECOVERY_HOST_HOME=" + host, "LARK_USER_VARIANT_BINARY=" + cfg.Binary, "HOME=" + root, "TMPDIR=" + root, "PATH=/usr/bin:/bin"}
	cmd.ExtraFiles = []*os.File{write, controlRead}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if cmd.Start() != nil {
		read.Close()
		write.Close()
		controlRead.Close()
		controlWrite.Close()
		t.Fatal("owned server child")
	}
	write.Close()
	controlRead.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var native *NativeProcessIdentity
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		controlWrite.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			// Cleanup is restricted to this fixture's exact recorded native birth.
			if native != nil {
				if current, e := readNativeProcess(native.PID); e == nil && current == *native {
					_ = syscall.Kill(-native.PGID, syscall.SIGKILL)
				}
			}
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
		}
		read.Close()
	}
	t.Cleanup(stop)
	dec := json.NewDecoder(read)
	next := func() nativeRecoveryFixtureMessage {
		ch := make(chan nativeRecoveryFixtureMessage, 1)
		go func() {
			var msg nativeRecoveryFixtureMessage
			if dec.Decode(&msg) != nil {
				msg.Phase = "error"
			}
			ch <- msg
		}()
		select {
		case msg := <-ch:
			return msg
		case <-time.After(12 * time.Second):
			t.Fatal("owned child proof deadline")
			return nativeRecoveryFixtureMessage{}
		}
	}
	active := next()
	native = active.Native
	if active.Phase != "active" || !validNativeGroup(native) {
		t.Fatal("real native admission phase absent")
	}
	if _, err = controlWrite.Write([]byte{1}); err != nil {
		t.Fatal("owned continue")
	}
	captured := next()
	if captured.Phase != "captured" || captured.Record.ActionID != action {
		t.Fatal("owned captured record absent")
	}
	return captured.Record, stop, captured.Workspace
}

func nativePending(record NativeRecoveryRecord) PendingNativeHandoff {
	return PendingNativeHandoff{ActionID: record.ActionID, RuntimeIDs: [2]uuid.UUID{record.Members[0].RuntimeID, record.Members[1].RuntimeID}}
}

func TestNativeRecoveryOriginalOwnerMustDie(t *testing.T) {
	record, stop, workspace := ownedNativeRecoveryRecord(t)
	m := NewManager()
	pending := nativePending(record)
	var consumes atomic.Int32
	consume := func(context.Context) error { consumes.Add(1); return nil } // fixture pre-C transaction, not PG evidence
	g, receipt, err := m.RecoverPendingHandoff(context.Background(), pending, record, consume)
	if !errors.Is(err, ErrNativeRecoveryUnknown) || g == nil || receipt.OwnerAbsent || consumes.Load() != 0 {
		t.Fatal("live owner was called dead")
	}
	g.Release()
	if !errors.Is(g.Seal(context.Background()), ErrNativeRecoveryUnknown) {
		t.Fatal("Seal bypassed unknown recovery")
	}
	stop()
	g, receipt, err = m.RecoverPendingHandoff(context.Background(), pending, record, consume)
	if err != nil || !receipt.OwnerAbsent || receipt.GroupsAbsent != [2]bool{true, true} || consumes.Load() != 1 {
		t.Fatal("real owner/native absence did not recover")
	}
	if _, err := os.Stat(filepath.Join(workspace, "synthetic-preserved")); err != nil {
		t.Fatal("recovery changed workspace")
	}
	if other, err := m.AcquireRuntimeGate(context.Background(), pending.RuntimeIDs[:]); !errors.Is(err, ErrNativeBusy) || other != nil {
		t.Fatal("receipt released gate")
	}
	g.Release()
	g.Release()
	other, err := m.AcquireRuntimeGate(context.Background(), pending.RuntimeIDs[:])
	if err != nil {
		t.Fatal("confirmed recovery gate did not release")
	}
	other.Release()
}

func TestNativeRecoveryUnknownNeverConsumesOrReleases(t *testing.T) {
	record, stop, _ := ownedNativeRecoveryRecord(t)
	stop()
	pending := nativePending(record)
	other := exec.Command("/bin/sleep", "20")
	other.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if other.Start() != nil {
		t.Fatal("owned unrelated group")
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _ = other.Wait() })
	unrelated, err := readNativeProcess(other.Process.Pid)
	if err != nil {
		t.Fatal("owned unrelated identity")
	}
	cases := []struct {
		name         string
		change       func(*NativeRecoveryRecord)
		consumeError bool
	}{
		{"missing", func(r *NativeRecoveryRecord) { *r = NativeRecoveryRecord{} }, false},
		{"action", func(r *NativeRecoveryRecord) { r.ActionID = uuid.New() }, false},
		{"boot", func(r *NativeRecoveryRecord) { r.BootID = "synthetic-other-boot" }, false},
		{"uid", func(r *NativeRecoveryRecord) { r.Owner.UID++ }, false},
		{"live-pid-wrong-birth", func(r *NativeRecoveryRecord) { r.Owner.PID = os.Getpid() }, false},
		{"live-group", func(r *NativeRecoveryRecord) { p := unrelated; r.Members[0].LastNative = &p }, false},
		{"duplicate-runtime", func(r *NativeRecoveryRecord) { r.Members[1].RuntimeID = r.Members[0].RuntimeID }, false},
		{"missing-epoch", func(r *NativeRecoveryRecord) { r.Members[0].ScopeEpoch = uuid.Nil }, false},
		{"unconfirmed-group", func(r *NativeRecoveryRecord) { r.Members[0].Confirmed = 0 }, false},
		{"pre-C-not-proven", func(*NativeRecoveryRecord) {}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager()
			r := copyNativeRecord(record)
			tc.change(&r)
			calls := 0
			g, receipt, err := m.RecoverPendingHandoff(context.Background(), pending, r, func(context.Context) error { calls++; return errors.New("synthetic pre-C proof unavailable") })
			if !errors.Is(err, ErrNativeRecoveryUnknown) || g == nil || receipt.OwnerAbsent {
				t.Fatal("unknown proof produced receipt")
			}
			if (!tc.consumeError && calls != 0) || (tc.consumeError && calls != 1) {
				t.Fatal("invalid record reached consumption")
			}
			g.Release()
			if !errors.Is(g.Seal(context.Background()), ErrNativeRecoveryUnknown) {
				t.Fatal("unknown gate sealed")
			}
			if next, err := m.AcquireRuntimeGate(context.Background(), pending.RuntimeIDs[:]); !errors.Is(err, ErrNativeBusy) || next != nil {
				t.Fatal("unknown gate released")
			}
		})
	}
	if current, err := readNativeProcess(unrelated.PID); err != nil || current != unrelated {
		t.Fatal("recovery touched unrelated group")
	}
	t.Run("bounded-consumption-wait", func(t *testing.T) {
		m := NewManager()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		start := time.Now()
		g, receipt, err := m.RecoverPendingHandoff(ctx, pending, record, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
		if !errors.Is(err, ErrNativeRecoveryUnknown) || g == nil || receipt.OwnerAbsent || time.Since(start) > 500*time.Millisecond {
			t.Fatal("bounded consumption did not preserve unknown")
		}
		g.Release()
		if !errors.Is(g.Seal(context.Background()), ErrNativeRecoveryUnknown) {
			t.Fatal("cancelled consumption bypassed gate")
		}
	})
	t.Run("confirmed-consumption-late-cancel", func(t *testing.T) {
		m := NewManager()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		g, receipt, err := m.RecoverPendingHandoff(ctx, pending, record, func(context.Context) error { cancel(); return nil })
		if err != nil || g == nil || !receipt.OwnerAbsent {
			t.Fatal("late cancel discarded confirmed consumption")
		}
		g.Release()
	})
}
