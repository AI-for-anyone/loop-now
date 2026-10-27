package tasks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/executor/local"
)

func TestLarkSwitchHandoffAbsentProcessesRetainBothGates(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	marker := filepath.Join(root, "retained-artifact")
	if err := os.WriteFile(marker, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	// Real local executor, deliberately no Source: no snapshot, Pi, Prepare,
	// credentials or provider may be consulted for this absent-process case.
	ex := local.New(local.Options{WorkRoot: root}, nil, nil, nil, nil)
	t.Cleanup(func() {
		if err := ex.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	ids := []uuid.UUID{uuid.New(), uuid.New()}
	lease, err := acquireTaskLarkHandoff(ctx, ex, ids)
	if err != nil || lease == nil {
		t.Fatal("real absent-process handoff", err)
	}
	for _, id := range ids {
		if other, err := ex.AcquireRuntimeHandoff(ctx, []uuid.UUID{id}); !errors.Is(err, local.ErrRuntimeBusy) || other != nil {
			t.Fatal("phase B released absent runtime gate", err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "retained-artifact" {
		t.Fatal("handoff prepared or removed runtime directories", err)
	}
	if raw, err := os.ReadFile(marker); err != nil || string(raw) != "original" {
		t.Fatal("handoff changed artifact", err)
	}
	// Only the owner who has determined final transaction outcome releases it.
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := ex.AcquireRuntimeHandoff(ctx, ids)
	if err != nil {
		t.Fatal("confirmed close did not release both gates", err)
	}
	if err = next.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLarkSwitchHandoffRejectsInvalidGroupWithoutGate(t *testing.T) {
	ctx := context.Background()
	ex := local.New(local.Options{}, nil, nil, nil, nil)
	t.Cleanup(func() {
		if err := ex.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	a, b := uuid.New(), uuid.New()
	for _, ids := range [][]uuid.UUID{nil, {a}, {a, b, uuid.New()}, {a, a}, {a, uuid.Nil}} {
		if lease, err := acquireTaskLarkHandoff(ctx, ex, ids); err == nil || lease != nil {
			t.Fatal("invalid group acquired a gate")
		}
	}
	lease, err := ex.AcquireRuntimeHandoff(ctx, []uuid.UUID{a, b})
	if err != nil {
		t.Fatal("invalid group left partial gate", err)
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
}
