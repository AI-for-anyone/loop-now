//go:build sqliteport

// SQLITE-PORT(G4): 依赖 PostgreSQL 专有机制（行锁/咨询锁/plpgsql/自建连接池/schema），SQLite 下需改写。
// 从 service_test.go 移出，默认不编译（go test -tags sqliteport 可单独查看）；改写后移回原文件并删掉本文件。
// 移出的函数：newImportEnv、TestCLIImportServiceReplayLeaseAndDeepCopy、TestCLIImportServicePrepareFailureCancelAndSourceChange、TestCLIImportServiceScopeAndPreciseFailure、TestCLIImportServiceExpiryAndClose、TestCLIImportUsesConfiguredCredentialLimit、TestCLIImportProjectDeleteTxRollbackAndScopedCancel、TestCLIImportProjectCancelPreventsLateScanPublication

package cliimport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func newImportEnv(t *testing.T) *importEnv {
	t.Helper()
	ctx := context.Background()
	url := pgtest.URL(t)
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal("TEST PG unavailable")
	}
	schema := "cli_import_service_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer admin.Close(ctx)
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Error(err)
		}
		var exists bool
		if err := admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, schema).Scan(&exists); err != nil || exists {
			t.Error("schema cleanup failed")
		}
	})
	st, err := store.Open(ctx, url, store.OpenOptions{SearchPath: schema})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	catalog, err := clitemplate.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Migrate(ctx, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	dc, err := st.EnsureDefaults(ctx, "测试组织", "默认测试项目")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(ctx, dc.Organization.ID, "来源导入项目")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	return &importEnv{st: st, templates: catalog, home: home, project: p.ID}
}

func TestCLIImportServiceReplayLeaseAndDeepCopy(t *testing.T) {
	e := newImportEnv(t)
	e.files(t, false)
	ctx := context.Background()
	entered := make(chan struct{}, 1)
	gate := make(chan struct{})
	s := e.service(t, Options{}, func(r *controlledReader) { r.entered = entered; r.gate = gate })
	scan, err := s.Scan(ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Sources) != 5 {
		t.Fatal("missing explicit source summaries")
	}
	original := copyScan(scan)
	scan.Candidates[0].Label = "changed client view"
	in := selected(original)
	if _, err = s.Start(ctx, e.project, in); err != nil {
		t.Fatal(err)
	}
	waitEntered(t, entered)
	// Expire the scan now instead of waiting out a short TTL that Start could
	// also miss. Scan GC must not close the accepted operation's Reader.
	s.mu.Lock()
	s.scans[in.ScanID].view.ExpiresAt = time.Now()
	s.mu.Unlock()
	if _, err = s.Scan(ctx, e.project); err != nil {
		t.Fatal(err)
	}
	if e.closes.Load() != 0 {
		t.Fatal("TTL closed active Reader")
	}
	close(gate)
	op := awaitImport(t, s, e.project, in.OperationID)
	if op.Status != "succeeded" || op.Items[0].Label == "changed client view" {
		t.Fatal("failed import/client mutation")
	}
	calls, prepared := e.extracts.Load(), e.prepared.Load()
	if _, err = s.Start(ctx, e.project, in); err != nil {
		t.Fatal("TTL replay failed", err)
	}
	// New service has no ephemeral scan; sameOp still reads durable history.
	restarted := e.service(t, Options{Hasher: testHasher{"changed-key", "key-two"}}, nil)
	if _, err = restarted.Start(ctx, e.project, in); err != nil {
		t.Fatal("persistent sameOp failed", err)
	}
	if e.extracts.Load() != calls || e.prepared.Load() != prepared {
		t.Fatal("sameOp replay extracted/prepared credentials")
	}
	if a, r := e.counts(t); a != 1 || r != 1 {
		t.Fatal("duplicate saved")
	}
	newScan, err := restarted.Scan(ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	// Receipts under an earlier provenance key are kept but never match, so
	// the same source is imported again as a new account.
	if len(newScan.Candidates) != 1 || newScan.Candidates[0].PreviouslyImported {
		t.Fatal("old-key receipt matched new scan")
	}
	again := selected(newScan)
	if _, err = restarted.Start(ctx, e.project, again); err != nil {
		t.Fatal("changed key blocked import", err)
	}
	reimported := awaitImport(t, restarted, e.project, again.OperationID)
	if reimported.Status != "succeeded" || reimported.Items[0].Status != "imported" {
		t.Fatal("changed key did not import as new account")
	}
	if a, r := e.counts(t); a != 2 || r != 2 {
		t.Fatal("old-key receipt removed or new account not created", a, r)
	}
	var keys int
	if err = e.st.Pool.QueryRow(ctx, `SELECT count(DISTINCT provenance_key_id) FROM cli_credential_import_receipts WHERE project_id=$1`, e.project).Scan(&keys); err != nil || keys != 2 {
		t.Fatal("receipts not kept per provenance key", err)
	}
}

func TestCLIImportServicePrepareFailureCancelAndSourceChange(t *testing.T) {
	t.Run("second_prepare_rollback", func(t *testing.T) {
		e := newImportEnv(t)
		e.files(t, true)
		e.failAt.Store(2)
		s := e.service(t, Options{}, nil)
		scan, err := s.Scan(context.Background(), e.project)
		if err != nil {
			t.Fatal(err)
		}
		in := selected(scan)
		if _, err = s.Start(context.Background(), e.project, in); err != nil {
			t.Fatal(err)
		}
		op := awaitImport(t, s, e.project, in.OperationID)
		if op.Status != "failed" || *op.ErrorCode != "consumer_failed" {
			t.Fatal("preparation failure misclassified")
		}
		if a, r := e.counts(t); a != 0 || r != 0 {
			t.Fatal("partial batch saved")
		}
	})
	t.Run("cancel_before_extract", func(t *testing.T) {
		e := newImportEnv(t)
		e.files(t, false)
		entered := make(chan struct{}, 1)
		gate := make(chan struct{})
		s := e.service(t, Options{}, func(r *controlledReader) { r.entered = entered; r.gate = gate })
		scan, err := s.Scan(context.Background(), e.project)
		if err != nil {
			t.Fatal(err)
		}
		in := selected(scan)
		if _, err = s.Start(context.Background(), e.project, in); err != nil {
			t.Fatal(err)
		}
		waitEntered(t, entered)
		op, err := s.Cancel(context.Background(), e.project, in.OperationID)
		if err != nil || op.Status != "cancelled" {
			t.Fatal(err)
		}
		if e.prepared.Load() != 0 {
			t.Fatal("cancelled extraction prepared credential")
		}
		if a, r := e.counts(t); a != 0 || r != 0 {
			t.Fatal("cancelled saved")
		}
	})
	t.Run("post_extract_recheck", func(t *testing.T) {
		e := newImportEnv(t)
		e.files(t, false)
		s := e.service(t, Options{}, func(r *controlledReader) {
			r.after = func() {
				if err := os.WriteFile(filepath.Join(e.home, ".pgpass"), []byte("changed"), 0600); err != nil {
					t.Error(err)
				}
			}
		})
		scan, err := s.Scan(context.Background(), e.project)
		if err != nil {
			t.Fatal(err)
		}
		in := selected(scan)
		if _, err = s.Start(context.Background(), e.project, in); err != nil {
			t.Fatal(err)
		}
		op := awaitImport(t, s, e.project, in.OperationID)
		if op.Status != "failed" || *op.ErrorCode != "source_changed" {
			t.Fatal("commit recheck missing")
		}
		if a, r := e.counts(t); a != 0 || r != 0 {
			t.Fatal("changed source saved")
		}
	})
}

func TestCLIImportServiceScopeAndPreciseFailure(t *testing.T) {
	e := newImportEnv(t)
	e.files(t, false)
	s := e.service(t, Options{}, nil)
	ctx := context.Background()
	scan, err := s.Scan(ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	first := selected(scan)
	if _, err = s.Start(ctx, e.project, first); err != nil {
		t.Fatal(err)
	}
	if awaitImport(t, s, e.project, first.OperationID).Status != "succeeded" {
		t.Fatal("first save")
	}
	p, err := e.st.GetProject(ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	other, err := e.st.CreateProject(ctx, p.OrganizationID, "另一个导入项目")
	if err != nil {
		t.Fatal(err)
	}
	otherScan, err := s.Scan(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	cross := selected(otherScan)
	cross.OperationID = first.OperationID
	before := e.extracts.Load()
	if _, err = s.Start(ctx, other.ID, cross); !store.IsNotFound(err) {
		t.Fatal("cross-scope existence leaked", err)
	}
	if e.extracts.Load() != before {
		t.Fatal("cross-scope extracted")
	}
	e.files(t, true)
	pass, err := os.ReadFile(filepath.Join(e.home, ".pgpass"))
	if err != nil {
		t.Fatal(err)
	}
	pass = bytes.ReplaceAll(pass, []byte("synthetic-password-A"), []byte("synthetic-password-A-changed"))
	if err = os.WriteFile(filepath.Join(e.home, ".pgpass"), pass, 0600); err != nil {
		t.Fatal(err)
	}
	clear(pass)
	changedScan, err := s.Scan(ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	in := selected(changedScan)
	if _, err = s.Start(ctx, e.project, in); err != nil {
		t.Fatal(err)
	}
	op := awaitImport(t, s, e.project, in.OperationID)
	if op.Status != "failed" || *op.ErrorCode != "source_conflict" {
		t.Fatal("source version conflict not preserved")
	}
	failed, notCommitted := 0, 0
	for _, v := range op.Items {
		if v.Status == "failed" {
			failed++
			if !strings.Contains(v.Label, "alpha") || v.ErrorCode == nil || *v.ErrorCode != "source_conflict" {
				t.Fatal("wrong candidate failed")
			}
		}
		if v.Status == "not_committed" {
			notCommitted++
		}
		if v.AccountID != nil || v.VersionID != nil {
			t.Fatal("failed batch advertised saved item")
		}
	}
	if failed != 1 || notCommitted != 1 {
		t.Fatal("missing per-item rollback classification")
	}
	if a, r := e.counts(t); a != 1 || r != 1 {
		t.Fatal("mixed failure mutated old receipt")
	}
}

func TestCLIImportServiceExpiryAndClose(t *testing.T) {
	t.Run("operation_deadline", func(t *testing.T) {
		e := newImportEnv(t)
		e.files(t, false)
		entered := make(chan struct{}, 1)
		gate := make(chan struct{})
		s := e.service(t, Options{}, func(r *controlledReader) { r.entered = entered; r.gate = gate })
		// The deadline arrives once extraction has started, not on a short wall
		// clock timeout that could also expire Scan, Start or the phase write
		// before the Reader is entered.
		due := signalDeadline(s)
		scan, err := s.Scan(context.Background(), e.project)
		if err != nil {
			t.Fatal(err)
		}
		in := selected(scan)
		if _, err = s.Start(context.Background(), e.project, in); err != nil {
			t.Fatal(err)
		}
		waitEntered(t, entered)
		close(due)
		op := awaitImport(t, s, e.project, in.OperationID)
		if op.Status != "expired" || *op.ErrorCode != "timeout" || e.prepared.Load() != 0 {
			t.Fatal("expired work saved or did not record with fresh context")
		}
	})
	t.Run("close_discovery", func(t *testing.T) {
		e := newImportEnv(t)
		ctx := context.Background()
		entered := make(chan struct{})
		var factoryCalls atomic.Int64
		s, err := NewService(e.st, e.templates, Options{InstanceID: uuid.New(), Hasher: testHasher{"key", "id"}, ReaderFactory: func(ctx context.Context) (Reader, error) {
			factoryCalls.Add(1)
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		}})
		if err != nil {
			t.Fatal(err)
		}
		if factoryCalls.Load() != 0 {
			t.Fatal("constructor scanned host")
		}
		done := make(chan struct{})
		go func() { defer close(done); _, _ = s.Scan(ctx, e.project) }()
		waitEntered(t, entered)
		deadline, cancel := context.WithTimeout(ctx, settle)
		defer cancel()
		if err = s.Close(deadline); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
		default:
			t.Fatal("Close returned before discovery stopped")
		}
	})
}

func TestCLIImportUsesConfiguredCredentialLimit(t *testing.T) {
	e := newImportEnv(t)
	e.files(t, false)
	s := e.service(t, Options{MaxCredential: 1}, nil)
	scan, err := s.Scan(context.Background(), e.project)
	if err != nil {
		t.Fatal(err)
	}
	in := selected(scan)
	if _, err = s.Start(context.Background(), e.project, in); err != nil {
		t.Fatal(err)
	}
	op := awaitImport(t, s, e.project, in.OperationID)
	if op.Status != "failed" || e.prepared.Load() != 0 {
		t.Fatal("credential budget bypassed before preparation")
	}
	if a, r := e.counts(t); a != 0 || r != 0 {
		t.Fatal("oversize source saved")
	}
}

func TestCLIImportProjectDeleteTxRollbackAndScopedCancel(t *testing.T) {
	e := newImportEnv(t)
	e.files(t, false)
	ctx := context.Background()
	p, err := e.st.GetProject(ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	other, err := e.st.CreateProject(ctx, p.OrganizationID, "另项目保持")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 2)
	gate := make(chan struct{})
	s := e.service(t, Options{}, func(r *controlledReader) { r.entered = entered; r.gate = gate })
	scan, err := s.Scan(ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	in := selected(scan)
	if _, err = s.Start(ctx, e.project, in); err != nil {
		t.Fatal(err)
	}
	waitEntered(t, entered)
	otherScan, err := s.Scan(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	otherIn := selected(otherScan)
	if _, err = s.Start(ctx, other.ID, otherIn); err != nil {
		t.Fatal(err)
	}
	waitEntered(t, entered)
	s.mu.Lock()
	targetJob, otherJob := s.jobs[in.OperationID], s.jobs[otherIn.OperationID]
	s.mu.Unlock()
	deleteTx := func(commit bool) {
		t.Helper()
		tx, err := e.st.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		var locked uuid.UUID
		if err = tx.QueryRow(ctx, `SELECT id FROM projects WHERE id=$1 FOR UPDATE`, e.project).Scan(&locked); err != nil {
			t.Fatal(err)
		}
		if err = e.st.TerminateProjectCLIImportsTx(ctx, tx, e.project); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE projects SET deleted_at=clock_timestamp() WHERE id=$1`, e.project); err != nil {
			t.Fatal(err)
		}
		if commit {
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	deleteTx(false)
	op, err := s.Get(ctx, e.project, in.OperationID)
	if err != nil || op.Terminal() {
		t.Fatal("rollback changed operation", err)
	}
	select {
	case <-targetJob.done:
		t.Fatal("rollback cancelled worker")
	default:
	}
	deleteTx(true)
	stop, cancel := context.WithTimeout(ctx, settle)
	defer cancel()
	if err = s.CancelProject(stop, e.project); err != nil {
		t.Fatal(err)
	}
	select {
	case <-targetJob.done:
	default:
		t.Fatal("postcommit target worker did not stop")
	}
	select {
	case <-otherJob.done:
		t.Fatal("postcommit cancelled another project")
	default:
	}
	var status, code string
	if err = e.st.Pool.QueryRow(ctx, `SELECT status,error_code FROM cli_credential_import_operations WHERE id=$1`, in.OperationID).Scan(&status, &code); err != nil || status != "cancelled" || code != "project_deleted" {
		t.Fatal("delete terminal not atomic", err)
	}
	close(gate)
	otherOp := awaitImport(t, s, other.ID, otherIn.OperationID)
	if otherOp.Status != "succeeded" {
		t.Fatal("other project failed")
	}
	if a, r := e.counts(t); a != 0 || r != 0 {
		t.Fatal("deleted project late save")
	}
}

func TestCLIImportProjectCancelPreventsLateScanPublication(t *testing.T) {
	e := newImportEnv(t)
	ctx := context.Background()
	p, err := e.st.GetProject(ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	other, err := e.st.CreateProject(ctx, p.OrganizationID, "另项目可扫描")
	if err != nil {
		t.Fatal(err)
	}
	ready, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var readers atomic.Int64
	s := e.service(t, Options{}, func(r *controlledReader) {
		if readers.Add(1) == 1 {
			r.Reader = &publicationGateReader{Reader: r.Reader, ready: ready, cancelled: cancelled, release: release}
		}
	})
	scanDone := make(chan error, 1)
	go func() { _, err := s.Scan(ctx, e.project); scanDone <- err }()
	<-ready
	if _, err = e.st.DeleteProject(ctx, e.project); err != nil {
		t.Fatal(err)
	}
	stop, cancel := context.WithTimeout(ctx, settle)
	defer cancel()
	cancelDone := make(chan error, 1)
	go func() { cancelDone <- s.CancelProject(stop, e.project) }()
	<-cancelled
	close(release)
	if err = <-cancelDone; err != nil {
		t.Fatal(err)
	}
	if err = <-scanDone; !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled discovery published", err)
	}
	s.mu.Lock()
	for _, entry := range s.scans {
		if entry.view.ProjectID == e.project {
			t.Error("late target scan retained")
		}
	}
	s.mu.Unlock()
	if e.closes.Load() != 1 {
		t.Fatal("cancelled Reader was not closed")
	}
	if otherScan, err := s.Scan(ctx, other.ID); err != nil || otherScan.ProjectID != other.ID {
		t.Fatal("other project was cancelled", err)
	}
}
