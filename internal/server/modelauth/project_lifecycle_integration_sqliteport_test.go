//go:build sqliteport

// SQLITE-PORT(G4): 依赖 PostgreSQL 专有机制（行锁/咨询锁/plpgsql/自建连接池/schema），SQLite 下需改写。
// 从 project_lifecycle_integration_test.go 移出，默认不编译（go test -tags sqliteport 可单独查看）；改写后移回原文件并删掉本文件。
// 移出的函数：TestProjectLifecycleRecordDeadlineFreshTerminalContext、TestProjectLifecycleSuccessMetadataAfterActualCommit

package modelauth

import (
	"context"
	"fmt"
	cm "loop-now/internal/server/credentialmodel"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestProjectLifecycleRecordDeadlineFreshTerminalContext(t *testing.T) {
	e := newLifecycleEnv(t, ServiceOptions{RecordTimeout: 200 * time.Millisecond})
	old := e.seedAccount()
	op := e.start(&old)
	tx, err := e.st.Pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), `SELECT id FROM projects WHERE id=$1 FOR UPDATE`, e.project); err != nil {
		t.Fatal(err)
	}
	e.release()
	e.waitDone(op.ID)
	// Still holding the project lock: the commit record timed out, while a new
	// context can independently lock the operation and persist its failed state.
	status, url, code := e.rawOperation(op.ID)
	if status != "failed" || url != nil || code == nil {
		t.Fatal("expired commit context poisoned terminal write")
	}
	e.assertNoSeal()
	if err = tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.assertOld(old)
	if !strings.Contains(e.logs.String(), `"stage":"credential_commit"`) {
		t.Fatal("deadline did not occur at credential commit")
	}
}

func TestProjectLifecycleSuccessMetadataAfterActualCommit(t *testing.T) {
	e := newLifecycleEnv(t, ServiceOptions{RecordTimeout: 3 * time.Second})
	old := e.seedAccount()
	op := e.start(&old)
	ctx := context.Background()
	lock, err := e.st.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	var key int64
	if err = lock.QueryRow(ctx, `SELECT hashtextextended($1,0)`, old.ID.String()).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if _, err = lock.Exec(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			_, _ = lock.Exec(ctx, `SELECT pg_advisory_unlock($1)`, key)
		}
	}()
	// The deferred trigger blocks the actual COMMIT after account and operation
	// updates, so a green result cannot be manufactured before PG visibility.
	query := fmt.Sprintf(`CREATE FUNCTION lifecycle_block_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(%d::bigint); RETURN NEW; END $$;
 CREATE CONSTRAINT TRIGGER lifecycle_block_commit AFTER INSERT ON model_auth_versions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION lifecycle_block_commit()`, key)
	if _, err = e.st.Pool.Exec(ctx, query); err != nil {
		t.Fatal(err)
	}
	e.release()
	waitAuth(t, func() bool {
		var waiting bool
		err := e.st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND classid::bigint=$1 AND objid::bigint=$2)`, int64(uint64(key)>>32), int64(uint32(key))).Scan(&waiting)
		return err == nil && waiting
	})
	status, _, _ := e.rawOperation(op.ID)
	if status != "awaiting_authorization" {
		t.Fatal("uncommitted success visible")
	}
	var current uuid.UUID
	if err = e.st.Pool.QueryRow(ctx, `SELECT current_model_version_id FROM project_credential_accounts WHERE id=$1`, old.ID).Scan(&current); err != nil || current != *old.CurrentVersionID {
		t.Fatal("uncommitted account pointer visible", err)
	}
	type readResult struct {
		state cm.LoginState
		err   error
	}
	reads := make(chan readResult, 8)
	for i := 0; i < 8; i++ {
		go func() { state, err := e.p.GetLogin(ctx, e.project, op.ID); reads <- readResult{state, err} }()
	}
	if _, err = lock.Exec(ctx, `SELECT pg_advisory_unlock($1)`, key); err != nil {
		t.Fatal(err)
	}
	released = true
	e.waitDone(op.ID)
	for i := 0; i < 8; i++ {
		select {
		case read := <-reads:
			if read.err != nil || read.state.Operation.Status != "succeeded" || read.state.Operation.AccountID == nil || *read.state.Operation.AccountID != old.ID || read.state.Account == nil || read.state.Account.CurrentVersionID == nil || *read.state.Account.CurrentVersionID == *old.CurrentVersionID || read.state.Account.Revision != old.Revision+1 || read.state.Operation.AuthorizationURL != nil {
				t.Fatal("success paired with stale/precommit metadata", read.err)
			}
		case <-time.After(4 * time.Second):
			t.Fatal("concurrent success projection blocked")
		}
	}
	if e.writes() != 1 {
		t.Fatal("success was written more than once")
	}
}
