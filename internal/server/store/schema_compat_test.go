package store_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
)

func TestSchemaCompatibility004Upgrade(t *testing.T) {
	ctx := context.Background()
	schema := pgtest.NewSchema(t)
	st, err := store.Open(ctx, pgtest.URL(t), store.OpenOptions{SearchPath: schema})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.MigrateUpTo(ctx, 4); err != nil {
		t.Fatal(err)
	}
	dc, err := st.EnsureDefaults(ctx, "旧组织", "旧项目")
	if err != nil {
		t.Fatal(err)
	}
	a := store.Agent{ID: uuid.New()}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO agents(id,project_id,name,model_provider,model_id)VALUES($1,$2,'旧Agent','anthropic','fixture')`, a.ID, dc.Project.ID); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO agent_clis(agent_id,cli_name)VALUES($1,'gh')`, a.ID)
	for _, purpose := range []string{"cli", "model"} {
		var cli *string
		if purpose == "cli" {
			name := "gh"
			cli = &name
		}
		exec(`INSERT INTO agent_credentials(id,agent_id,purpose,cli_name,alg,key_id,wrapped_dek,nonce,ciphertext)VALUES($1,$2,$3,$4,'fixture','fixture',decode('01','hex'),decode('02','hex'),decode('0304','hex'))`, uuid.New(), a.ID, purpose, cli)
	}
	oldIdentity := uuid.New()
	exec(`INSERT INTO external_identities(id,name)VALUES($1,'old-ci')`, oldIdentity)
	message := uuid.New()
	exec(`INSERT INTO inbox_messages(id,thread_id,from_kind,to_kind,to_agent_id,text,when_busy)VALUES($1,$1,'console','agent',$2,'old-inbox','queue')`, message, a.ID)
	if err = st.MigrateUpTo(ctx, 11); err != nil {
		t.Fatal(err)
	}
	var identityName string
	if err = st.Pool.QueryRow(ctx, `SELECT name FROM external_identities WHERE id=$1 AND deleted_at IS NULL`, oldIdentity).Scan(&identityName); err != nil || identityName != "old-ci" {
		t.Fatal("old identity changed during upgrade", err)
	}
	var good int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM agent_credentials WHERE agent_id=$1 AND ciphertext=decode('0304','hex') AND ((purpose='cli' AND template_source='builtin') OR (purpose='model' AND template_source IS NULL))`, a.ID).Scan(&good); err != nil || good != 2 {
		t.Fatal("old ciphertext/source changed", err)
	}
	var text string
	if err = st.Pool.QueryRow(ctx, `SELECT text FROM inbox_messages WHERE id=$1`, message).Scan(&text); err != nil || text != "old-inbox" {
		t.Fatal("old Inbox lost", err)
	}
	// The old model credential path must still write NULL CLI source. Current
	// Store code needs the full schema, so it runs on a separate current store.
	current := pgtest.NewStore(t)
	platform, err := current.CreateAgent(ctx, store.PlatformScope(), "私有旧CRUD", "anthropic", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = current.PutCredential(ctx, platform.ID, store.PurposeModel, "", store.CredentialMeta{}, []byte{3}); err != nil {
		t.Fatal("legacy model CRUD violated CLI-source constraint", err)
	}
	var source *string
	if err = current.Pool.QueryRow(ctx, `SELECT template_source FROM agent_credentials WHERE agent_id=$1 AND purpose='model'`, platform.ID).Scan(&source); err != nil || source != nil {
		t.Fatal("model gained CLI source", err)
	}
}
func TestSchemaCompatibility006PlatformConflict(t *testing.T) {
	ctx := context.Background()
	schema := pgtest.NewSchema(t)
	st, err := store.Open(ctx, pgtest.URL(t), store.OpenOptions{SearchPath: schema})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.MigrateUpTo(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO external_identities(id,name)VALUES($1,'platform')`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if err = st.MigrateUpTo(ctx, 6); err == nil {
		t.Fatal("live platform identity silently changed")
	}
	var max int
	if err = st.Pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&max); err != nil || max != 5 {
		t.Fatal("failed006 partially advanced", max, err)
	}
	var unchanged bool
	if err = st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM external_identities WHERE name='platform' AND deleted_at IS NULL)`).Scan(&unchanged); err != nil || !unchanged {
		t.Fatal("migration changed live identity", err)
	}
}
