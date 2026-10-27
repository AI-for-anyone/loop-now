package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"loop-now/internal/server/executor/local"
	"loop-now/internal/server/larksidecar"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/store"
)

func TestLarkPendingHandoffsProtectUnknownEvidence(t *testing.T) {
	a := store.TaskLarkRecoveryAction{TaskID: uuid.New(), ActionID: uuid.New(), ActorKey: "console", RuntimeIDs: []uuid.UUID{uuid.New(), uuid.New()}}
	got, err := larkPendingHandoffs([]store.TaskLarkRecoveryAction{a})
	if err != nil || len(got) != 1 || got[0].ActionID != a.ActionID || got[0].RuntimeIDs[0] != a.RuntimeIDs[0] || got[0].RuntimeIDs[1] != a.RuntimeIDs[1] {
		t.Fatalf("lost exact unknown handoff protection: %v", err)
	}
	for _, name := range []string{"missing-member", "duplicate-member", "zero-action", "overlap"} {
		t.Run(name, func(t *testing.T) {
			b := a
			b.RuntimeIDs = append([]uuid.UUID(nil), a.RuntimeIDs...)
			rows := []store.TaskLarkRecoveryAction{b}
			switch name {
			case "missing-member":
				rows[0].RuntimeIDs = rows[0].RuntimeIDs[:1]
			case "duplicate-member":
				rows[0].RuntimeIDs[1] = rows[0].RuntimeIDs[0]
			case "zero-action":
				rows[0].ActionID = uuid.Nil
			case "overlap":
				b.ActionID = uuid.New()
				rows = append(rows, b)
			}
			if got, err := larkPendingHandoffs(rows); err == nil || got != nil {
				t.Fatal("unsafe membership accepted")
			}
		})
	}
}

func TestLarkRecoveryAdapterRetainsUnknownGate(t *testing.T) {
	for _, raw := range []string{"null", "{", "{}"} {
		t.Run(raw, func(t *testing.T) {
			manager := larksidecar.NewManager()
			runtime := &larkExecution{manager: manager}
			ids := [2]uuid.UUID{uuid.New(), uuid.New()}
			consumed := false
			held, err := runtime.RecoverPendingHandoff(context.Background(), uuid.New(), ids, json.RawMessage(raw), func(context.Context) error { consumed = true; return nil })
			if err == nil || held == nil || consumed {
				t.Fatalf("invalid evidence admitted or gate lost: err=%v held=%v consumed=%v", err, held != nil, consumed)
			}
			if err = held.Seal(context.Background()); err == nil {
				t.Fatal("unknown gate sealed")
			}
			held.Release()
			if _, err = manager.AcquireRuntimeGate(context.Background(), ids[:]); !errors.Is(err, larksidecar.ErrNativeBusy) {
				t.Fatalf("unknown gate released: %v", err)
			}
		})
	}
}

func TestLarkStartupMissingNativeProofKeepsBothGates(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ids := [2]uuid.UUID{uuid.New(), uuid.New()}
	action := uuid.New()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	original := local.New(local.Options{WorkRoot: root, InstanceID: uuid.New()}, nil, nil, nil, logger)
	oldGate, err := original.AcquireRuntimeHandoff(ctx, ids[:])
	if err != nil {
		t.Fatal(err)
	}
	record, err := oldGate.CaptureRecovery(ctx, action)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(struct {
		Local  local.RuntimeRecoveryRecord `json:"local"`
		Native any                         `json:"native"`
	}{record, nil})
	if err != nil {
		t.Fatal(err)
	}
	executor := local.New(local.Options{WorkRoot: root, InstanceID: uuid.New()}, nil, nil, nil, logger)
	manager := larksidecar.NewManager()
	a := &App{Executor: executor}
	// No Store is installed: unknown native evidence must never consume PG.
	err = a.recoverPendingLarkSwitch(ctx, store.TaskLarkRecoveryAction{TaskID: uuid.New(), ActionID: action, ActorKey: "console", RuntimeIDs: ids[:], ProcessRecord: raw}, &larkExecution{manager: manager})
	if err == nil {
		t.Fatal("missing native proof recovered")
	}
	if _, err = executor.AcquireRuntimeHandoff(ctx, ids[:]); !errors.Is(err, local.ErrRuntimeBusy) {
		t.Fatalf("local gate released after unknown native: %v", err)
	}
	if _, err = manager.AcquireRuntimeGate(ctx, ids[:]); !errors.Is(err, larksidecar.ErrNativeBusy) {
		t.Fatalf("native gate released after unknown proof: %v", err)
	}
}
