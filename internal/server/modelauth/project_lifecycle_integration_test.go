package modelauth

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"loop-now/internal/server/clitemplate"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/store"

	"github.com/google/uuid"

	"loop-now/internal/testutil/sqlitetest"
)

// Only credential version writes are counted, by a nontransactional PG sequence.
// Real PG transactions, ProjectService and actual Helper fd subprocess remain in use.
const lifecycleOldCredentialJSON = `{"type":"oauth","access":"synthetic-old-access","refresh":"synthetic-old-refresh","expires":1893456000000,"accountId":"old-grant"}`

type lifecycleEnv struct {
	t       *testing.T
	st      *store.Store
	p       *ProjectService
	svc     *Service
	project uuid.UUID
	helper  *Helper
	seeded  int64
	logs    bytes.Buffer
	// attempts 数尝试写入凭证版本的语句（回滚的也算），由语句钩子计数。
	attempts atomic.Int64
}

func newLifecycleEnv(t *testing.T, options ServiceOptions) *lifecycleEnv {
	t.Helper()
	ctx := context.Background()
	st := sqlitetest.NewStore(t)
	defaults, err := st.EnsureDefaults(ctx, "认证生命周期组织", "默认项目")
	if err != nil {
		t.Fatal(err)
	}
	project, err := st.CreateProject(ctx, defaults.Organization.ID, "认证生命周期项目")
	if err != nil {
		t.Fatal(err)
	}
	templates, err := clitemplate.Load("")
	if err != nil {
		t.Fatal(err)
	}
	// fakeGoHelper creates a pure fs/readline child with the real private fd ABI.
	// A gate controls real success; abort deliberately emits late success.
	helper := fakeGoHelper(t, `if(frame.type==='abort'){fs.appendFileSync(new URL('./events',import.meta.url),'abort\n');send({type:'result',id:operation.id,status:'succeeded',credential});}else{fs.appendFileSync(new URL('./events',import.meta.url),'login\n');const timer=setInterval(()=>{if(fs.existsSync(new URL('./release',import.meta.url))){clearInterval(timer);fs.appendFileSync(new URL('./events',import.meta.url),'result\n');send({type:'result',id:operation.id,status:'succeeded',credential});}},5);}`)
	helper.Options.Timeout = 10 * time.Second
	e := &lifecycleEnv{t: t, st: st, project: project.ID, helper: helper}
	st.Pool.SetQueryHook(func(_ context.Context, q string) {
		if strings.Contains(q, "INSERT INTO model_auth_versions") {
			e.attempts.Add(1)
		}
	})
	if options.LoginTimeout <= 0 {
		options.LoginTimeout = 8 * time.Second
	}
	if options.RecordTimeout <= 0 {
		options.RecordTimeout = time.Second
	}
	e.svc = NewService(st, helper, nil, catalog(), options, slog.New(slog.NewJSONHandler(&e.logs, nil)))
	e.p = NewProjectService(e.svc, templates, 0)
	t.Cleanup(func() {
		closeAuth(t, e.svc)
		if strings.Contains(e.logs.String(), "fixture-access") || strings.Contains(e.logs.String(), "fixture-rotated") || strings.Contains(e.logs.String(), "https://") {
			t.Error("lifecycle diagnostic leaked secret/URL")
		}
	})
	t.Logf("isolated SQLite store project=%s; actual Helper fd fixture, no provider/network", project.ID)
	return e
}
func (e *lifecycleEnv) totalWrites() int64 { return e.attempts.Load() }

// writes counts credential version writes attempted by logins after seeding.
func (e *lifecycleEnv) writes() int64 { return e.totalWrites() - e.seeded }
func (e *lifecycleEnv) event(name string) int {
	b, _ := os.ReadFile(filepath.Join(filepath.Dir(e.helper.Options.Entry), "events"))
	return strings.Count(string(b), name+"\n")
}
func (e *lifecycleEnv) release() {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(filepath.Dir(e.helper.Options.Entry), "release"), []byte("release"), 0600); err != nil {
		e.t.Fatal(err)
	}
}
func (e *lifecycleEnv) start(account *cm.Account) cm.Operation {
	e.t.Helper()
	in := cm.LoginStart{Provider: "openai-codex"}
	name := "生命周期新账号"
	if account == nil {
		in.Name = &name
	} else {
		in.AccountID = &account.ID
		in.ExpectedRevision = &account.Revision
	}
	state, err := e.p.Start(context.Background(), e.project, in)
	if err != nil {
		e.t.Fatal(err)
	}
	waitAuth(e.t, func() bool {
		op, err := e.st.GetProjectModelAuthOperation(context.Background(), e.project, state.Operation.ID)
		return err == nil && op.Status == "awaiting_authorization" && op.AuthorizationURL != nil && e.event("login") == 1
	})
	return state.Operation
}
func (e *lifecycleEnv) waitDone(id uuid.UUID) {
	e.t.Helper()
	e.svc.mu.Lock()
	worker := e.svc.operations[id]
	e.svc.mu.Unlock()
	if worker == nil {
		return
	}
	select {
	case <-worker.done:
	case <-time.After(4 * time.Second):
		e.t.Fatal("lifecycle helper did not drain")
	}
}
func (e *lifecycleEnv) seedAccount() cm.Account {
	e.t.Helper()
	credential, err := modelcredential.Decode([]byte(lifecycleOldCredentialJSON))
	if err != nil {
		e.t.Fatal(err)
	}
	plain, err := credential.Encode()
	if err != nil {
		e.t.Fatal(err)
	}
	defer clear(plain)
	record, err := e.st.CreateProjectCredential(context.Background(), e.project, store.PurposeModel, "openai-codex", "原账号", store.ProjectCredentialWrite{AuthMethod: "oauth", Secret: plain})
	if err != nil {
		e.t.Fatal(err)
	}
	e.seeded = e.totalWrites()
	return record.View
}
func (e *lifecycleEnv) rawOperation(id uuid.UUID) (string, *string, *string) {
	e.t.Helper()
	var status string
	var url, code *string
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT status,authorization_url,error_code FROM project_model_auth_operations WHERE id=$1`, id).Scan(&status, &url, &code); err != nil {
		e.t.Fatal(err)
	}
	return status, url, code
}
func (e *lifecycleEnv) assertOld(account cm.Account) {
	e.t.Helper()
	current, err := e.st.GetProjectCredential(context.Background(), e.project, account.ID)
	if err != nil || current.View.CurrentVersionID == nil || *current.View.CurrentVersionID != *account.CurrentVersionID || current.View.Revision != account.Revision {
		e.t.Fatal("failed login changed account/version/revision", err)
	}
	if current.Model == nil {
		e.t.Fatal("old model secret record missing")
	}
	plain := current.Model.Secret
	expected, err := modelcredential.Decode([]byte(lifecycleOldCredentialJSON))
	if err != nil {
		e.t.Fatal("invalid old credential fixture")
	}
	expectedPlain, err := expected.Encode()
	if err != nil {
		e.t.Fatal("invalid old credential encoding")
	}
	defer clear(expectedPlain)
	if !bytes.Equal(plain, expectedPlain) {
		e.t.Fatal("old grant secret changed across failed lifecycle operation")
	}
	var count int
	if err = e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM model_auth_versions WHERE project_account_id=$1`, account.ID).Scan(&count); err != nil || count != 1 {
		e.t.Fatal("rolled-back login left extra version", err)
	}
}
func (e *lifecycleEnv) assertNoSeal() {
	e.t.Helper()
	if n := e.writes(); n != 0 {
		e.t.Fatalf("late/invalid login attempted credential write %d times", n)
	}
}

func TestProjectLifecycleExpiryLateResultSealZero(t *testing.T) {
	e := newLifecycleEnv(t, ServiceOptions{LoginTimeout: 750 * time.Millisecond})
	op := e.start(nil)
	waitAuth(t, func() bool {
		state, err := e.p.GetLogin(context.Background(), e.project, op.ID)
		return err == nil && state.Operation.Status == "expired"
	})
	e.waitDone(op.ID)
	e.assertNoSeal()
	status, url, code := e.rawOperation(op.ID)
	if status != "expired" || url != nil || code == nil || *code != "auth_timeout" || e.event("abort") != 1 {
		t.Fatal("deadline/late result not closed")
	}
	list, err := e.p.List(context.Background(), e.project, store.PurposeModel, "openai-codex")
	if err != nil || len(list.Accounts) != 0 {
		t.Fatal("expiry created account")
	}
}
func TestProjectLifecycleRestartRecoveryLateResultSealZero(t *testing.T) {
	e := newLifecycleEnv(t, ServiceOptions{})
	old := e.seedAccount()
	op := e.start(&old)
	// Exercise the real persistence recovery boundary used by App startup while
	// retaining a controlled old child solely to challenge the late-result fence.
	if err := e.st.RecoverModelAuth(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := NewService(e.st, e.helper, nil, catalog(), ServiceOptions{}, nil)
	defer closeAuth(t, second)
	projected := NewProjectService(second, e.p.Templates, 0)
	state, err := projected.GetLogin(context.Background(), e.project, op.ID)
	if err != nil || state.Operation.Status != "failed" || state.Operation.ErrorCode == nil || *state.Operation.ErrorCode != "server_restarted" || state.Operation.AuthorizationURL != nil {
		t.Fatal("restart recovery projection wrong", err)
	}
	e.release()
	e.waitDone(op.ID)
	e.assertNoSeal()
	e.assertOld(old)
	if e.event("login") != 1 {
		t.Fatal("recovery replayed helper")
	}
	status, url, _ := e.rawOperation(op.ID)
	if status != "failed" || url != nil {
		t.Fatal("late pre-restart result revived operation")
	}
}
func TestProjectLifecycleAccountDeletedLateResultSealZero(t *testing.T) {
	e := newLifecycleEnv(t, ServiceOptions{})
	old := e.seedAccount()
	op := e.start(&old)
	deleted, err := e.p.Delete(context.Background(), e.project, old.ID, old.Revision)
	if err != nil || deleted.DeletedAt == nil {
		t.Fatal("account delete failed", err)
	}
	e.waitDone(op.ID)
	e.assertNoSeal()
	status, url, _ := e.rawOperation(op.ID)
	if status != "cancelled" || url != nil || e.event("abort") != 1 {
		t.Fatal("account deletion did not drain late helper")
	}
	// Deletion wiped the account's only version; the late result added none.
	row, err := e.st.GetProjectCredential(context.Background(), e.project, old.ID)
	if err != nil || row.View.DeletedAt == nil || row.View.CurrentVersionID != nil {
		t.Fatal("late helper revived/replaced deleted account", err)
	}
	var versions int
	if err = e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM model_auth_versions WHERE project_account_id=$1`, old.ID).Scan(&versions); err != nil || versions != 0 {
		t.Fatal("deleted account holds a model version", versions, err)
	}
}
func TestProjectLifecycleProjectDeletedLateResultSealZero(t *testing.T) {
	e := newLifecycleEnv(t, ServiceOptions{})
	_ = e.seedAccount()
	op := e.start(nil)
	if _, err := e.st.DeleteProject(context.Background(), e.project); err != nil {
		t.Fatal(err)
	}
	// Match the owner boundary: cancel/drain only after successful Store commit.
	if err := e.svc.CancelProjectLogins(context.Background(), e.project, nil); err != nil {
		t.Fatal(err)
	}
	e.waitDone(op.ID)
	e.assertNoSeal()
	status, url, _ := e.rawOperation(op.ID)
	if status != "cancelled" || url != nil || e.event("abort") != 1 {
		t.Fatal("deleted project operation/URL revived")
	}
	var versions int
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM model_auth_versions v JOIN project_credential_accounts a ON a.id=v.project_account_id WHERE a.project_id=$1`, e.project).Scan(&versions); err != nil || versions != 0 {
		t.Fatal("deleted project secret revived", err)
	}
}

func TestProjectLifecycleDeferredCommitFailurePreservesOld(t *testing.T) {
	e := newLifecycleEnv(t, ServiceOptions{})
	old := e.seedAccount()
	op := e.start(&old)
	// SQLite 没有延迟到提交时的触发器：在写入版本时失败，整个事务同样回滚。
	_, err := e.st.Pool.Exec(context.Background(), `CREATE TRIGGER lifecycle_fail_commit AFTER INSERT ON model_auth_versions BEGIN SELECT RAISE(ABORT, 'synthetic deferred commit failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	e.release()
	e.waitDone(op.ID)
	status, url, _ := e.rawOperation(op.ID)
	if status != "failed" || url != nil || e.writes() != 1 {
		t.Fatal("commit failure not injected after actual credential write")
	}
	e.assertOld(old)
	if !strings.Contains(e.logs.String(), `"stage":"credential_commit"`) || strings.Contains(e.logs.String(), "synthetic deferred commit failure") {
		t.Fatal("commit failure diagnostic missing or leaked raw PG error")
	}
}
func TestProjectLifecycleConcurrentRevisionChangeSealZero(t *testing.T) {
	e := newLifecycleEnv(t, ServiceOptions{})
	old := e.seedAccount()
	op := e.start(&old)
	changed, err := e.p.Rename(context.Background(), e.project, old.ID, cm.Rename{Name: "并发改名", ExpectedRevision: old.Revision})
	if err != nil {
		t.Fatal(err)
	}
	e.release()
	e.waitDone(op.ID)
	e.assertNoSeal()
	status, url, code := e.rawOperation(op.ID)
	if status != "failed" || url != nil || code == nil || *code != "configuration_changed" {
		t.Fatal("stale login committed after concurrent account change")
	}
	old.Revision = changed.Revision
	e.assertOld(old)
}
func TestProjectLifecycleProjectDeleteRollbackKeepsLiveHelper(t *testing.T) {
	e := newLifecycleEnv(t, ServiceOptions{})
	old := e.seedAccount()
	op := e.start(&old)
	_, err := e.st.Pool.Exec(context.Background(), `CREATE TRIGGER lifecycle_fail_delete BEFORE UPDATE ON projects WHEN NEW.deleted_at IS NOT NULL BEGIN SELECT RAISE(ABORT, 'synthetic project delete rollback'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.st.DeleteProject(context.Background(), e.project); err == nil {
		t.Fatal("delete rollback was not injected")
	}
	status, url, _ := e.rawOperation(op.ID)
	if status != "awaiting_authorization" || url == nil || e.event("abort") != 0 {
		t.Fatal("rolled-back delete cancelled live helper/URL")
	}
	e.assertNoSeal()
	e.assertOld(old)
	e.svc.mu.Lock()
	worker := e.svc.operations[op.ID]
	e.svc.mu.Unlock()
	if worker == nil {
		t.Fatal("rollback lost login worker")
	}
	// No postcommit cancellation is called on failure. The still-live actual child
	// can finish and commit against the rolled-back account and project.
	e.release()
	e.waitDone(op.ID)
	state, err := e.p.GetLogin(context.Background(), e.project, op.ID)
	if err != nil || state.Operation.Status != "succeeded" || state.Account == nil || state.Account.CurrentVersionID == nil || *state.Account.CurrentVersionID == *old.CurrentVersionID || e.writes() != 1 {
		t.Fatal("rollback did not leave usable live login", err)
	}
}
