package store_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io"
	"log/slog"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
	"testing"
)

func TestQuestion010UpgradeAndDownProtection(t *testing.T) {
	ctx := context.Background()
	schema := pgtest.NewSchema(t)
	st, err := store.Open(ctx, pgtest.URL(t), store.OpenOptions{SearchPath: schema})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.MigrateUpTo(ctx, 9); err != nil {
		t.Fatal(err)
	}
	dc, err := st.EnsureDefaults(ctx, "组织", "项目")
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.CreateAgent(ctx, store.ProjectScope(dc.Project.ID), "旧Agent", "fixture", "model", "kept")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if _, err = st.Pool.Exec(ctx, `INSERT INTO inbox_messages(id,thread_id,from_kind,to_kind,to_agent_id,text,when_busy)VALUES($1,$1,'console','agent',$2,'old question','queue')`, id, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO inbox_questions(message_id,options,expires_at)VALUES($1,'[{"id":"1","label":"one"}]',now()+interval '7 days')`, id); err != nil {
		t.Fatal(err)
	}
	if err = st.Migrate(ctx, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = st.Pool.QueryRow(ctx, `SELECT state FROM inbox_questions WHERE message_id=$1`, id).Scan(&state); err != nil || state != "open" {
		t.Fatal("010 changed old question", err)
	}
	// A clean Down must succeed, retaining old compatible question data.
	if err = st.MigrateDownTo(ctx, 9); err != nil {
		t.Fatal(err)
	}
	if err = st.Migrate(ctx, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	if err = st.InboxTx(ctx, func(tx pgx.Tx) error { _, err := store.CloseInboxQuestion(ctx, tx, id, "task_retry"); return err }); err != nil {
		t.Fatal(err)
	}
	if err = st.MigrateDownTo(ctx, 9); err == nil {
		t.Fatal("Down discarded closed history")
	}
	if err = st.Pool.QueryRow(ctx, `SELECT state FROM inbox_questions WHERE message_id=$1`, id).Scan(&state); err != nil || state != "closed" {
		t.Fatal("failed Down lost data", err)
	}
}
