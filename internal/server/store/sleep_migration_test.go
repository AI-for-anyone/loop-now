package store_test

import (
	"context"
	"github.com/google/uuid"
	"io"
	"log/slog"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
	"testing"
)

func TestSleep011MigrationAndHistoryProtection(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, pgtest.URL(t), store.OpenOptions{SearchPath: pgtest.NewSchema(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.MigrateUpTo(ctx, 10); err != nil {
		t.Fatal(err)
	}
	dc, err := s.EnsureDefaults(ctx, "org", "project")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAgent(ctx, store.ProjectScope(dc.Project.ID), "old", "fixture", "model", "")
	if err != nil {
		t.Fatal(err)
	}
	r := store.Run{ID: uuid.New()}
	if _, err = s.Pool.Exec(ctx, `INSERT INTO runs(id,agent_id,user_message_id,config_version,instance_id,priority,status) VALUES($1,$2,$3,1,$4,1,'starting')`, r.ID, a.ID, uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err = s.Migrate(ctx, log); err != nil {
		t.Fatal(err)
	}
	if err = s.MigrateDownTo(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx, log); err != nil {
		t.Fatal(err)
	}
	if old, err := s.GetRun(ctx, r.ID); err != nil || old.Origin != "console" || old.WakeSleepID != nil {
		t.Fatal("old run changed", err)
	}
	if err = s.SetRunRunning(ctx, r.ID, 1); err != nil {
		t.Fatal(err)
	}
	in := sleepmodel.Input{ActionID: uuid.New(), RunID: r.ID, Seconds: 1}
	receipt, err := s.RequestSleep(ctx, auth.Principal{Kind: auth.KindAgent, ID: a.ID}, in, 100)
	if err != nil {
		t.Fatal(err)
	}
	// A second active sleep and malformed normal scope combination are rejected by PG itself.
	for _, q := range []string{
		`INSERT INTO agent_sleeps SELECT gen_random_uuid(),scope_key,scope_kind,mode,agent_id,task_id,runtime_id,node_run_id,session_epoch,source_run_id,gen_random_uuid(),body_hash,receipt,state,seconds,reason,requested_at,entered_at,wake_at,ended_at,end_reason,gen_random_uuid(),wake_run_id,revision FROM agent_sleeps WHERE id=$1`,
		`UPDATE agent_sleeps SET session_epoch=NULL WHERE id=$1`,
		`UPDATE agent_sleeps SET seconds=2147483648 WHERE id=$1`,
	} {
		if _, err = s.Pool.Exec(ctx, q, receipt.Sleep.ID); err == nil {
			t.Fatal("constraint accepted", q)
		}
	}
	if err = s.MigrateDownTo(ctx, 10); err == nil {
		t.Fatal("Down erased sleep history")
	}
	var n int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM agent_sleeps WHERE id=$1`, receipt.Sleep.ID).Scan(&n); err != nil || n != 1 {
		t.Fatal("failed Down lost history", err)
	}
}
