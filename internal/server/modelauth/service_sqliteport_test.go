//go:build sqliteport

// SQLITE-PORT(G4): 依赖 PostgreSQL 专有机制（行锁/咨询锁/plpgsql/自建连接池/schema），SQLite 下需改写。
// 从 service_test.go 移出，默认不编译（go test -tags sqliteport 可单独查看）；改写后移回原文件并删掉本文件。
// 移出的函数：metadataTracer.TraceQueryStart、metadataTracer.TraceQueryEnd、TestStateNeverCombinesOldMetadataWithNewSuccessfulLogin、TestLoginCommitDeadlineUsesFreshFailureContext

package modelauth

import (
	"bytes"
	"context"
	"log/slog"
	"loop-now/internal/server/modelcredential"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"loop-now/internal/server/db"
)

func (t *metadataTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if len(d.Args) > 0 && d.Args[0] == t.version && strings.Contains(d.SQL, "FROM model_auth_versions") {
		return context.WithValue(ctx, metadataQueryKey{}, true)
	}
	return ctx
}

func (t *metadataTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if ctx.Value(metadataQueryKey{}) == true && t.once.CompareAndSwap(false, true) {
		close(t.entered)
		select {
		case <-t.release:
		case <-ctx.Done():
		}
	}
}

func TestStateNeverCombinesOldMetadataWithNewSuccessfulLogin(t *testing.T) {
	st, a := authFixture(t)
	ctx := context.Background()
	old := grant(t, st, a.ID, oauth(t, "account-A", "A", time.Now().Add(time.Hour)))
	originalPool := st.Pool
	trace := &metadataTracer{version: old.ID, entered: make(chan struct{}), release: make(chan struct{})}
	cfg := originalPool.Config()
	cfg.ConnConfig.Tracer = trace
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	st.Pool = pool
	t.Cleanup(func() { st.Pool = originalPool; pool.Close() })
	releaseLogin := make(chan struct{})
	svc := NewService(st, loginFunc(func(ctx context.Context, _ uuid.UUID, notify func(string) error) (modelcredential.Credential, error) {
		<-releaseLogin
		return oauth(t, "account-B", "B", time.Now().Add(time.Hour)), nil
	}), nil, catalog(), ServiceOptions{}, nil)
	defer closeAuth(t, svc)
	agent, e := st.GetAgent(ctx, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	op, e := svc.Start(ctx, a.ID, LoginInput{Provider: "openai-codex", ExpectedConfigVersion: agent.ConfigVersion})
	if e != nil {
		t.Fatal(e)
	}
	result := make(chan State, 1)
	errs := make(chan error, 1)
	go func() { state, e := svc.State(ctx, a.ID); result <- state; errs <- e }()
	select {
	case <-trace.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("metadata query not reached")
	}
	close(releaseLogin)
	waitAuth(t, func() bool {
		op, e := st.GetModelAuthOperation(ctx, a.ID, op.ID)
		return e == nil && op.Status == "succeeded"
	})
	close(trace.release)
	state := <-result
	if e = <-errs; e != nil {
		t.Fatal(e)
	}
	if state.Operation.Status == "succeeded" && state.Credential.VersionID != nil && *state.Credential.VersionID == old.ID {
		t.Fatal("success paired with pre-commit account metadata")
	}
	fresh, e := svc.State(ctx, a.ID)
	if e != nil || fresh.Operation.Status != "succeeded" || fresh.Credential.VersionID == nil || *fresh.Credential.VersionID == old.ID {
		t.Fatal("UI cannot observe new account", e)
	}
}

func TestLoginCommitDeadlineUsesFreshFailureContext(t *testing.T) {
	st, a := authFixture(t)
	ctx := context.Background()
	old := grant(t, st, a.ID, oauth(t, "old", "old", time.Now().Add(time.Hour)))
	// The helper result arrives while the Agent row is held, so the commit outlives
	// its record deadline. The failure write locks only the operation and must
	// use a fresh context.
	locked := make(chan *db.Tx, 1)
	var failureLogs bytes.Buffer
	svc := NewService(st, loginFunc(func(context.Context, uuid.UUID, func(string) error) (modelcredential.Credential, error) {
		lock, e := st.Pool.Begin(ctx)
		if e != nil {
			return modelcredential.Credential{}, e
		}
		locked <- lock
		if _, e = lock.Exec(ctx, `SELECT id FROM agents WHERE id=$1 FOR UPDATE`, a.ID); e != nil {
			return modelcredential.Credential{}, e
		}
		return oauth(t, "new", "new", time.Now().Add(time.Hour)), nil
	}), nil, catalog(), ServiceOptions{RecordTimeout: 100 * time.Millisecond}, slog.New(slog.NewJSONHandler(&failureLogs, nil)))
	defer closeAuth(t, svc)
	agent, e := st.GetAgent(ctx, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	op, e := svc.Start(ctx, a.ID, LoginInput{Provider: "openai-codex", ExpectedConfigVersion: agent.ConfigVersion})
	if e != nil {
		t.Fatal(e)
	}
	var lock *db.Tx
	select {
	case lock = <-locked:
	case <-time.After(4 * time.Second):
		t.Fatal("login helper did not run")
	}
	defer lock.Rollback(ctx)
	waitAuth(t, func() bool {
		row, e := st.GetModelAuthOperation(ctx, a.ID, op.ID)
		return e == nil && row.Status == "failed"
	})
	if e = lock.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	pointer, _, e := st.CurrentModelAuthVersion(ctx, a.ID)
	if e != nil || pointer.ID != old.ID {
		t.Fatal("deadline replaced original grant", e)
	}
	closeAuth(t, svc)
	if !strings.Contains(failureLogs.String(), `"stage":"credential_commit"`) {
		t.Fatal("commit failure stage missing")
	}
}
