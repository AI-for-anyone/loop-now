package app_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"loop-now/internal/server/app"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/testenv"
)

type credentialStartupHelper struct{ calls atomic.Int32 }

func (h *credentialStartupHelper) Login(context.Context, uuid.UUID, func(string) error) (modelcredential.Credential, error) {
	h.calls.Add(1)
	return modelcredential.Credential{}, modelauth.ErrHelperUnavailable
}

// A database below 012 that still holds pre-plaintext credentials cannot be
// upgraded directly: startup fails before listening, helper or auth recovery,
// and the refusal names the supported path (old release to 025, conversion).
func TestProjectCredentialMigrationRefusalBeforeListeningOrRecovery(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	st, err := store.Open(ctx, w.cfg.DatabaseURL, store.OpenOptions{SearchPath: w.schema})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	db := stdlib.OpenDBFromPool(st.Pool)
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS(filepath.Join(testenv.RepoRoot(), "internal/server/store/migrations")), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 11); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	dc, err := st.EnsureDefaults(ctx, w.cfg.DefaultOrgName, w.cfg.DefaultProjectName)
	if err != nil {
		t.Fatal(err)
	}
	// Schema 11 predates later Agent columns; insert the row at that shape.
	agentID := uuid.New()
	if _, err = st.Pool.Exec(ctx, `INSERT INTO agents(id,project_id,name,model_provider,model_id)VALUES($1,$2,'旧凭证startup','openai-codex','fixture')`, agentID, dc.Project.ID); err != nil {
		t.Fatal(err)
	}
	// Opaque bytes stand in for a row sealed by the removed master key.
	id := uuid.New()
	opaque := []byte("synthetic-startup-secret")
	if _, err = st.Pool.Exec(ctx, `INSERT INTO model_auth_versions(id,agent_id,provider,auth_method,alg,key_id,wrapped_dek,nonce,ciphertext)VALUES($1,$2,'openai-codex','oauth','A256GCM-envelope-v1','local-fixture',$3,$4,$5)`, id, agentID, []byte("wrapped"), []byte("nonce-12byte"), opaque); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO agent_model_auth(agent_id,version_id)VALUES($1,$2)`, agentID, id); err != nil {
		t.Fatal(err)
	}
	op := uuid.New()
	if _, err = st.Pool.Exec(ctx, `INSERT INTO model_auth_operations(id,agent_id,provider,config_version,expires_at)VALUES($1,$2,'openai-codex',1,now()+interval '1 hour')`, op, agentID); err != nil {
		t.Fatal(err)
	}
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := w.cfg
	cfg.ListenAddr = reserved.Addr().String()
	reserved.Close()
	helper := &credentialStartupHelper{}
	var log bytes.Buffer
	_, err = app.Build(ctx, &cfg, slog.New(slog.NewJSONHandler(io.MultiWriter(&log), nil)), app.Options{SearchPath: w.schema, HomeDirs: []string{"/nonexistent-home"}, AuthLoginHelper: helper})
	if err == nil {
		t.Fatal("startup migrated legacy credentials without conversion")
	}
	if !strings.Contains(err.Error(), "025") || !strings.Contains(err.Error(), "明文凭证转换") {
		t.Fatal("refusal did not name the supported upgrade path", err)
	}
	if bytes.Contains(log.Bytes(), []byte("synthetic-startup-secret")) {
		t.Fatal("startup logged credential bytes")
	}
	if conn, err := net.DialTimeout("tcp", cfg.ListenAddr, 100*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("failed migration opened listener")
	}
	if helper.calls.Load() != 0 {
		t.Fatal("failed migration started helper")
	}
	if _, err = os.Stat(filepath.Join(cfg.WorkRoot, "platform", "auth-helper-v1.1.0")); !os.IsNotExist(err) {
		t.Fatal("helper installed before migration success")
	}
	var status string
	if err = st.Pool.QueryRow(ctx, `SELECT status FROM model_auth_operations WHERE id=$1`, op).Scan(&status); err != nil || status != "pending" {
		t.Fatal("failed migration ran auth recovery", err)
	}
	var upgraded bool
	if err = st.Pool.QueryRow(ctx, `SELECT to_regclass('project_credential_accounts') IS NOT NULL OR EXISTS(SELECT 1 FROM goose_db_version WHERE version_id=12 AND is_applied)`).Scan(&upgraded); err != nil || upgraded {
		t.Fatal("startup failure partially migrated", err)
	}
	var kept []byte
	if err = st.Pool.QueryRow(ctx, `SELECT ciphertext FROM model_auth_versions WHERE id=$1 AND agent_id=$2`, id, agentID).Scan(&kept); err != nil || !bytes.Equal(kept, opaque) {
		t.Fatal("refused migration changed the legacy row", err)
	}
}
