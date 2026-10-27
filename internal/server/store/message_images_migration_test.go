package store_test

import (
	"bytes"
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"image"
	"image/png"
	"loop-now/internal/server/messageimage"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
	"strings"
	"testing"
)

func TestMessageImages023MigrationAndDownProtection(t *testing.T) {
	ctx := context.Background()
	conn, e := pgx.Connect(ctx, pgtest.URL(t))
	if e != nil {
		t.Fatal(e)
	}
	schema := "image_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = conn.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	defer func() {
		_, e := conn.Exec(ctx, `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`)
		if e != nil {
			t.Error(e)
		}
		conn.Close(ctx)
	}()
	st, e := store.Open(ctx, pgtest.URL(t), store.OpenOptions{SearchPath: schema})
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	// 026 refuses Down, so this historical Down check stops at 025.
	if e = st.MigrateUpTo(ctx, 25); e != nil {
		t.Fatal(e)
	}
	defaults, e := st.EnsureDefaults(ctx, "org", "project")
	if e != nil {
		t.Fatal(e)
	}
	agent, e := st.CreateAgent(ctx, store.ProjectScope(defaults.Project.ID), "legacy", "fixture", "model", "")
	if e != nil {
		t.Fatal(e)
	}
	if e = st.MigrateDownTo(ctx, 22); e != nil {
		t.Fatal("empty image migration Down", e)
	}
	if _, e = st.Pool.Exec(ctx, `INSERT INTO inbox_messages(id,thread_id,from_kind,to_kind,text) VALUES($1,$1,'console','console','old text')`, uuid.New()); e != nil {
		t.Fatal(e)
	}
	if e = st.MigrateUpTo(ctx, 25); e != nil {
		t.Fatal(e)
	}
	var oldImages string
	if e = st.Pool.QueryRow(ctx, `SELECT images::text FROM inbox_messages WHERE text='old text'`).Scan(&oldImages); e != nil || oldImages != "[]" {
		t.Fatal("old text changed", oldImages, e)
	}
	var buf bytes.Buffer
	if e = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 3))); e != nil {
		t.Fatal(e)
	}
	data := buf.Bytes()
	ref, e := messageimage.Inspect(data, "image/png", 0, 0)
	if e != nil {
		t.Fatal(e)
	}
	id := uuid.New()
	if _, e = st.SaveMessageImage(ctx, id, store.ImageOwner{AgentID: agent.ID}, ref, data); e != nil {
		t.Fatal(e)
	}
	if e = st.MigrateDownTo(ctx, 22); e == nil {
		t.Fatal("Down erased uploaded image")
	}
	v, e := st.MessageImage(ctx, id)
	if e != nil || v.AgentID != agent.ID {
		t.Fatal("failed Down lost row", e)
	}
	var version int
	if e = st.Pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); e != nil || version != 23 {
		t.Fatal("Down changed ledger", version, e)
	}
}
