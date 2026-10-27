package app_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/app"
	"loop-now/internal/server/config"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/larkauth/larktest"
)

func TestLarkPendingCrashAppChild(t *testing.T) {
	if os.Getenv("LOOP_LARK_CRASH_CHILD") != "1" {
		return
	}
	pipe := os.NewFile(3, "private-test-config")
	var in githubCrashInput
	if json.NewDecoder(io.LimitReader(pipe, 1<<20)).Decode(&in) != nil {
		os.Exit(61)
	}
	pipe.Close()
	p := larktest.New(t)
	p.Add("ou-not-authorized", "pending", nil, "")
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		os.Exit(62)
	}
	a, e := app.Build(context.Background(), &in.Config, config.NewLogger(io.Discard, "json", 0), app.Options{SearchPath: in.Schema, Listener: listener, HomeDirs: []string{"/nonexistent-home"}, LarkLoginHelper: p.Client})
	if e != nil {
		os.Exit(63)
	}
	go a.Serve()
	ready, _ := json.Marshal(map[string]string{"api": "http://" + a.Addr() + "/api/v1"})
	if os.WriteFile(in.Ready, ready, 0600) != nil {
		os.Exit(64)
	}
	select {}
}

func TestLarkActualAppSIGKILLPendingFlowIsNotResumed(t *testing.T) {
	w := githubAppWorld(t)
	var err error
	w.cfg.NodeBin, err = exec.LookPath("node")
	if err != nil {
		t.Fatal("fixture Node unavailable")
	}
	seed := w.start(nil)
	project, base, _, source := larkAppSource(t, seed)
	row, _, err := seed.a.Store.GetProjectCLIVersion(context.Background(), source.ID, *source.CurrentVersionID)
	if err != nil {
		t.Fatal(err)
	}
	original := sha256.Sum256(row.Secret)
	seed.stop()
	input := githubCrashInput{Config: w.cfg, Schema: w.schema, Ready: filepath.Join(t.TempDir(), "ready.json")}
	r, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer wr.Close()
	child := exec.Command(os.Args[0], "-test.run=^TestLarkPendingCrashAppChild$")
	child.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LOOP_LARK_CRASH_CHILD=1"}
	child.ExtraFiles = []*os.File{r}
	child.Stdout = io.Discard
	child.Stderr = io.Discard
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	reaped := false
	defer func() {
		if !reaped {
			child.Process.Kill()
			child.Wait()
		}
	}()
	if err = json.NewEncoder(wr).Encode(input); err != nil {
		t.Fatal("private TEST config transfer failed")
	}
	wr.Close()
	r.Close()
	var ready struct {
		API string `json:"api"`
	}
	githubWaitFile(t, input.Ready, &ready)
	remote := &server{t: t, url: ready.API}
	id := uuid.New()
	remote.must(202, "POST", base, map[string]any{"operationId": id, "cli": "lark-cli", "authType": "lark_user", "sourceAccountId": source.ID, "expectedSourceRevision": source.Revision})
	waitLarkApp(t, remote, base, id, "awaiting_authorization")
	if err = child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = child.Wait()
	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatal("child did not die from signal")
	}
	status, ok := exit.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatal("child did not receive SIGKILL")
	}
	reaped = true
	// No synthetic operation row is inserted: startup must recover the actual
	// pending Go Device Flow produced over HTTP in the killed App process.
	s := w.start(nil)
	defer s.stop()
	out := decode[cm.CLIAuthState](t, s.must(200, "GET", base+"/"+id.String(), nil))
	if out.Operation.Status != "failed" || out.Operation.ErrorCode == nil || *out.Operation.ErrorCode != "server_restarted" || out.Operation.AuthorizationURL != nil || out.Operation.UserCode != nil || out.Account != nil {
		t.Fatal("hard restart resumed pending device authorization")
	}
	row, _, err = s.a.Store.GetProjectCLIVersion(context.Background(), source.ID, *source.CurrentVersionID)
	if err != nil || sha256.Sum256(row.Secret) != original {
		t.Fatal("hard restart changed source application credential")
	}
	var operations, grants, runs int
	if err = s.a.Store.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM project_cli_auth_operations WHERE project_id=$1),(SELECT count(*) FROM lark_cli_grants),(SELECT count(*) FROM runs)`, project).Scan(&operations, &grants, &runs); err != nil || operations != 1 || grants+runs != 0 {
		t.Fatal("hard restart created duplicate operation/grant/model work")
	}
	// Shutdown can drain immediately: recovery must not create a new worker
	// from the old operation or current source application.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = s.a.CLIAuth.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
