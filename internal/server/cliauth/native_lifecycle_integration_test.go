package cliauth

import (
	"context"
	"github.com/google/uuid"
	"loop-now/internal/server/agentconfig"
	cm "loop-now/internal/server/credentialmodel"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestGoogleActualPendingHelperProjectDeleteRollbackAndCleanup(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("reviewed private runtime is darwin-arm64 only")
	}
	f := newGoogleFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	repo, _ := filepath.Abs("../../..")
	roots := HelperRuntime{SDKRoot: filepath.Join(repo, ".local-build/cli-installation-proof/unpacked/google-cloud-sdk"), PythonRoot: filepath.Join(repo, ".local-build/cli-installation-proof/unpacked/private-python/Python.framework"), Script: filepath.Join(repo, "auth-helper/google_auth.py")}
	work := t.TempDir()
	helper := &ProcessHelper{Options: HelperOptions{WorkRoot: work, StopGrace: 100 * time.Millisecond, Runtime: func(context.Context) (HelperRuntime, error) { return roots, nil }, Command: func(ctx context.Context, dir string, r HelperRuntime) *exec.Cmd {
		// No callback or token endpoint fixture is used. The real helper only waits
		// on its own callback listener; every outbound network request is denied.
		profile := filepath.Join(dir, "offline.sb")
		os.WriteFile(profile, []byte(`(version 1)(allow default)(deny network*)(allow network-bind (local tcp "localhost:8085"))(allow network-inbound (local tcp "localhost:8085"))`), 0600)
		cmd := privatePythonCommand(r)
		return exec.Command("/usr/bin/sandbox-exec", append([]string{"-f", profile}, cmd.Args...)...)
	}}}
	service := NewService(f.st, helper, nil, ServiceOptions{LoginTimeout: 10 * time.Second, RecordTimeout: time.Second}, nil)
	defer service.Close(context.Background())
	name := "仅等待回调"
	id := uuid.New()
	if _, e := service.Start(ctx, f.project, cm.CLIAuthStart{OperationID: id, CLI: "gcloud", AuthType: "google_user", Name: &name}); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		state, e := service.Get(ctx, f.project, id)
		if e != nil {
			t.Fatal(e)
		}
		if state.Operation.Status == "awaiting_authorization" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual helper did not bind callback")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, e := f.st.Pool.Exec(ctx, `CREATE FUNCTION block_google_project_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.deleted_at IS NOT NULL THEN RAISE EXCEPTION 'synthetic delete rollback'; END IF; RETURN NEW; END $$; CREATE TRIGGER block_google_project_delete BEFORE UPDATE ON projects FOR EACH ROW EXECUTE FUNCTION block_google_project_delete()`); e != nil {
		t.Fatal(e)
	}
	config := &agentconfig.Service{Store: f.st, CancelProjectCLILogins: service.CancelProject}
	if e := config.DeleteProject(ctx, f.project); e == nil {
		t.Fatal("fixture deletion did not roll back")
	}
	state, e := service.Get(ctx, f.project, id)
	if e != nil || state.Operation.Status != "awaiting_authorization" || state.Operation.AuthorizationURL == nil {
		t.Fatal("rollback cancelled pending operation")
	}
	if ln, e := net.Listen("tcp", "127.0.0.1:8085"); e == nil {
		ln.Close()
		t.Fatal("rollback released active helper port")
	}
	if _, e = f.st.Pool.Exec(ctx, `DROP TRIGGER block_google_project_delete ON projects; DROP FUNCTION block_google_project_delete()`); e != nil {
		t.Fatal(e)
	}
	if e = config.DeleteProject(ctx, f.project); e != nil {
		t.Fatal("committed project delete failed", e)
	}
	var status string
	var hasURL bool
	var versions int
	if e = f.st.Pool.QueryRow(ctx, `SELECT status,authorization_url IS NOT NULL,(SELECT count(*) FROM project_cli_versions) FROM project_cli_auth_operations WHERE id=$1`, id).Scan(&status, &hasURL, &versions); e != nil || status != "cancelled" || hasURL || versions != 0 {
		t.Fatal("project delete did not converge pending login")
	}
	ln, e := net.Listen("tcp", "127.0.0.1:8085")
	if e != nil {
		t.Fatal("committed cleanup did not release callback port")
	}
	ln.Close()
	entries, e := os.ReadDir(work)
	if e != nil || len(entries) != 0 {
		t.Fatal("helper private operation directory not removed")
	}
}
