//go:build darwin

package local

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
)

func recoveryExecutor(t *testing.T, h *harness) *Executor {
	t.Helper()
	o := h.e.o
	o.InstanceID = uuid.New()
	e := New(o, h.src, h.e.tmpl, h.e.inst, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = e.Shutdown(ctx)
	})
	return e
}
func recoveryRecord(t *testing.T, h *harness, ids [2]uuid.UUID) (PendingRuntimeHandoff, RuntimeRecoveryRecord) {
	t.Helper()
	pending := PendingRuntimeHandoff{ActionID: uuid.New(), RuntimeIDs: ids}
	lease, err := h.e.AcquireRuntimeHandoff(context.Background(), ids[:])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	record, err := lease.CaptureRecovery(context.Background(), pending.ActionID)
	if err != nil {
		t.Fatal("capture failed", err)
	}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var persisted RuntimeRecoveryRecord
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	return pending, persisted
}
func assertRecoveryUntouched(t *testing.T, h *harness, ids ...uuid.UUID) {
	t.Helper()
	for _, id := range ids {
		if syscall.Kill(h.pid(id), 0) != nil {
			t.Fatal("unrelated/mismatched process was signalled")
		}
	}
}

func TestPendingRuntimeRecoveryExactPairPreservesArtifacts(t *testing.T) {
	h := newHarness(t, nil, "")
	a, b, other := h.agent(nil), h.agent(nil), h.agent(nil)
	for _, id := range []uuid.UUID{a, b, other} {
		h.exec(id, "owned process")
	}
	pending, record := recoveryRecord(t, h, [2]uuid.UUID{a, b})
	saved := map[string][]byte{}
	for _, id := range []uuid.UUID{a, b} {
		for _, dir := range []string{h.e.dirs(id).workspace, h.e.dirs(id).sessions, h.e.dirs(id).home} {
			path := filepath.Join(dir, "preserved.txt")
			data := []byte("synthetic preserved result")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			saved[path] = data
		}
	}
	e := recoveryExecutor(t, h)
	held, receipt, err := e.RecoverPendingHandoff(context.Background(), pending, record)
	if err != nil || !receipt.GroupAbsent[0] || !receipt.GroupAbsent[1] || receipt.ActionID != pending.ActionID {
		t.Fatal("exact pair not proven dead", err)
	}
	for _, m := range record.Members {
		if syscall.Kill(-m.Process.PGID, 0) != syscall.ESRCH {
			t.Fatal("receipt without actual group death")
		}
	}
	assertRecoveryUntouched(t, h, other)
	for p, want := range saved {
		got, err := os.ReadFile(p)
		if err != nil || string(got) != string(want) {
			t.Fatal("recovery changed preserved files")
		}
	}
	if next, err := e.AcquireRuntimeHandoff(context.Background(), pending.RuntimeIDs[:]); !errors.Is(err, ErrRuntimeBusy) || next != nil {
		t.Fatal("successful proof prematurely released gate")
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	// A second recovery can confirm an already absent recorded group without
	// reading a deleted pid file, resetting a directory or issuing a new signal.
	held, _, err = e.RecoverPendingHandoff(context.Background(), pending, record)
	if err != nil {
		t.Fatal("repeat absence proof failed", err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPendingRuntimeRecoveryAbsentRequiresOriginalRecord(t *testing.T) {
	h := newHarness(t, nil, "")
	ids := [2]uuid.UUID{uuid.New(), uuid.New()}
	pending, record := recoveryRecord(t, h, ids)
	e := recoveryExecutor(t, h)
	held, receipt, err := e.RecoverPendingHandoff(context.Background(), pending, record)
	if err != nil || !receipt.GroupAbsent[0] || !receipt.GroupAbsent[1] {
		t.Fatal("explicit original absence rejected", err)
	}
	if err = held.Close(); err != nil {
		t.Fatal(err)
	}
	e2 := recoveryExecutor(t, h)
	held, receipt, err = e2.RecoverPendingHandoff(context.Background(), pending, RuntimeRecoveryRecord{})
	if !errors.Is(err, ErrRuntimeStopUnconfirmed) || held == nil || receipt.ActionID != uuid.Nil {
		t.Fatal("missing record fabricated death proof")
	}
	if !errors.Is(held.Close(), ErrRuntimeStopUnconfirmed) {
		t.Fatal("unknown recovery released gate")
	}
}

func TestPendingRuntimeRecoveryMismatchDoesNotSignalEitherMember(t *testing.T) {
	for _, kind := range []string{"birth", "uid", "host", "boot", "root", "pair", "duplicate_process", "pgid"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t, nil, "")
			a, b := h.agent(nil), h.agent(nil)
			h.exec(a, "first")
			h.exec(b, "second")
			pending, record := recoveryRecord(t, h, [2]uuid.UUID{a, b})
			switch kind {
			case "birth":
				record.Members[1].Process.StartSeconds++
			case "uid":
				record.Members[1].Process.UID++
			case "host":
				record.Hostname += "-different"
			case "boot":
				record.BootID += "-different"
			case "root":
				record.WorkRoot += "-different"
			case "pair":
				record.Members[1].RuntimeID = uuid.New()
			case "duplicate_process":
				record.Members[1].Process = record.Members[0].Process
			case "pgid":
				record.Members[1].Process.PGID++
			}
			e := recoveryExecutor(t, h)
			held, _, err := e.RecoverPendingHandoff(context.Background(), pending, record)
			if !errors.Is(err, ErrRuntimeStopUnconfirmed) || held == nil {
				t.Fatal("mismatch accepted")
			}
			if !errors.Is(held.Close(), ErrRuntimeStopUnconfirmed) {
				t.Fatal("mismatch released gate")
			}
			assertRecoveryUntouched(t, h, a, b)
		})
	}
}

// The helper is a test-owned process group with an exact readiness pipe. It
// ignores TERM, proving both the KILL branch and context timeout without sleeps
// used to guess whether signal handlers have been installed.
func TestRuntimeRecoveryGroupChild(t *testing.T) {
	if os.Getenv("LOOP_TEST_RECOVERY_CHILD") != "1" {
		return
	}
	signal.Ignore(syscall.SIGTERM)
	ready := os.NewFile(3, "ready")
	_, _ = ready.Write([]byte{1})
	_ = ready.Close()
	for {
		time.Sleep(time.Hour)
	}
}
func ownedRecoveryGroup(t *testing.T) (RuntimeProcessIdentity, <-chan error) {
	t.Helper()
	ready, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer ready.Close()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^TestRuntimeRecoveryGroupChild$")
	cmd.Env = []string{"LOOP_TEST_RECOVERY_CHILD=1", "PATH=/usr/bin:/bin", "HOME=" + t.TempDir()}
	cmd.ExtraFiles = []*os.File{writer}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = cmd.Start(); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	writer.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("owned child not reaped")
		}
	})
	_ = ready.SetReadDeadline(time.Now().Add(3 * time.Second))
	var one [1]byte
	if _, err = io.ReadFull(ready, one[:]); err != nil {
		t.Fatal("ready barrier", err)
	}
	identity, err := readRecoveryProcess(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	return identity, done
}
func TestRuntimeRecoveryGroupKillAndDeadline(t *testing.T) {
	t.Run("kill", func(t *testing.T) {
		p, _ := ownedRecoveryGroup(t)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := stopRecoveryGroup(ctx, p, 30*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		if syscall.Kill(-p.PGID, 0) != syscall.ESRCH {
			t.Fatal("kill did not prove group absent")
		}
	})
	t.Run("deadline", func(t *testing.T) {
		p, _ := ownedRecoveryGroup(t)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		if err := stopRecoveryGroup(ctx, p, time.Second); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("stop ignored deadline", err)
		}
		if syscall.Kill(-p.PGID, 0) != nil {
			t.Fatal("deadline fabricated process exit")
		}
	})
}

func TestCleanupStaleExceptPendingPreservesExactEvidence(t *testing.T) {
	h := newHarness(t, nil, "")
	ids := [2]uuid.UUID{uuid.New(), uuid.New()}
	pending := PendingRuntimeHandoff{ActionID: uuid.New(), RuntimeIDs: ids}
	h.src.deleted = ids[:]
	for _, id := range ids {
		d := h.e.dirs(id)
		if err := d.ensure(); err != nil {
			t.Fatal(err)
		}
		// Invalid/legacy evidence must be preserved rather than erased or trusted.
		if err := os.WriteFile(d.pidFile, []byte("invalid synthetic evidence"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d.home, "preserved"), []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.e.CleanupStaleExceptPending(context.Background(), []PendingRuntimeHandoff{pending}); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, err := os.Stat(h.e.dirs(id).pidFile); err != nil {
			t.Fatal("pending pid evidence erased")
		}
		if _, err := os.Stat(filepath.Join(h.e.dirs(id).home, "preserved")); err != nil {
			t.Fatal("pending HOME touched")
		}
	}
}
