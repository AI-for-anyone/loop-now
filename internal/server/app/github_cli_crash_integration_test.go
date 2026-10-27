package app_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/app"
	"loop-now/internal/server/cliauth"
	"loop-now/internal/server/config"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/githubauth"
	"loop-now/internal/server/githubauth/githubtest"
	"loop-now/internal/server/store"
)

type githubCrashInput struct {
	Config                        config.Config
	Schema, Ready, Barrier, Phase string
}

// githubCrashBeforeCommit holds the verified device/identity result before the
// login worker reaches its commit transaction.
type githubCrashBeforeCommit struct {
	cliauth.GitHubLoginHelper
	barrier func()
}

func (w githubCrashBeforeCommit) Login(ctx context.Context, notify func(githubauth.Authorization) error) (githubauth.Credential, error) {
	c, e := w.GitHubLoginHelper.Login(ctx, notify)
	if e != nil {
		return c, e
	}
	w.barrier()
	<-ctx.Done()
	return githubauth.Credential{}, ctx.Err()
}

// This child is selected explicitly and receives only TEST configuration over
// an inherited private pipe. No argv/file contains a DSN, key or credential.
func TestGitHubCrashAppChild(t *testing.T) {
	if os.Getenv("LOOP_GITHUB_CRASH_CHILD") != "1" {
		return
	}
	pipe := os.NewFile(3, "private-test-config")
	var in githubCrashInput
	if json.NewDecoder(io.LimitReader(pipe, 1<<20)).Decode(&in) != nil {
		os.Exit(51)
	}
	pipe.Close()
	provider := githubtest.New(t)
	attempt := provider.Add("202", "bob", nil, "")
	attempt.Release()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(52)
	}
	a, err := app.Build(context.Background(), &in.Config, config.NewLogger(io.Discard, "json", 0), app.Options{SearchPath: in.Schema, Listener: listener, HomeDirs: []string{"/nonexistent-home"}})
	if err != nil {
		os.Exit(53)
	}
	a.CLIAuth.GitHub = provider.Client
	barrier := func() {
		b, _ := json.Marshal(map[string]any{"phase": in.Phase, "deviceCalls": provider.DeviceCalls.Load(), "userCalls": provider.UserCalls.Load(), "deniedDials": provider.DeniedDials.Load()})
		if os.WriteFile(in.Barrier, b, 0600) != nil {
			os.Exit(54)
		}
	}
	if in.Phase == "before_commit" {
		a.CLIAuth.GitHub = githubCrashBeforeCommit{GitHubLoginHelper: provider.Client, barrier: barrier}
	} else {
		accountView := a.CLIAuth.Account
		a.CLIAuth.Account = func(ctx context.Context, p, id uuid.UUID) (cm.Account, error) {
			account, e := accountView(ctx, p, id)
			if e == nil {
				barrier()
				<-ctx.Done()
				return cm.Account{}, ctx.Err()
			}
			return account, e
		}
	}
	go a.Serve()
	ready, _ := json.Marshal(map[string]string{"api": "http://" + a.Addr() + "/api/v1"})
	if os.WriteFile(in.Ready, ready, 0600) != nil {
		os.Exit(56)
	}
	select {}
}

func githubWaitFile(t *testing.T, path string, out any) {
	t.Helper()
	for deadline := time.Now().Add(12 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if b, e := os.ReadFile(path); e == nil && json.Unmarshal(b, out) == nil {
			return
		}
	}
	t.Fatal("owned GitHub child did not reach safe barrier")
}

func TestGitHubActualAppSIGKILLCommitBoundary(t *testing.T) {
	for _, phase := range []string{"before_commit", "after_commit_before_response"} {
		t.Run(phase, func(t *testing.T) {
			w := githubAppWorld(t)
			w.cfg.NodeBin = "/Users/ljl/.local/node-v24.21.0/bin/node"
			w.cfg.AuthLoginTimeout = time.Minute
			w.cfg.AuthRecordTimeout = 30 * time.Second
			seed := w.start(nil)
			project := decode[apitypes.Context](t, seed.must(200, "GET", "/context", nil)).Project.ID
			accounts := "/projects/" + project.String() + "/credential-accounts"
			old := decode[cm.Account](t, seed.must(201, "POST", accounts, map[string]any{"purpose": "cli", "cli": "gh", "name": "retained remark", "credential": map[string]any{"type": "cli", "fields": map[string]string{"token": "synthetic-precrash-PAT"}}}))
			oldRow, _, e := seed.a.Store.GetProjectCLIVersion(context.Background(), old.ID, *old.CurrentVersionID)
			if e != nil {
				t.Fatal(e)
			}
			oldHash := sha256.Sum256(oldRow.Secret)
			seed.stop()
			observer, e := store.Open(context.Background(), w.cfg.DatabaseURL, store.OpenOptions{SearchPath: w.schema})
			if e != nil {
				t.Fatal(e)
			}
			defer observer.Close()
			dir := t.TempDir()
			input := githubCrashInput{Config: w.cfg, Schema: w.schema, Ready: filepath.Join(dir, "api.json"), Barrier: filepath.Join(dir, "barrier.json"), Phase: phase}
			read, write, e := os.Pipe()
			if e != nil {
				t.Fatal(e)
			}
			defer read.Close()
			defer write.Close()
			child := exec.Command(os.Args[0], "-test.run=^TestGitHubCrashAppChild$")
			child.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LOOP_GITHUB_CRASH_CHILD=1"}
			child.ExtraFiles = []*os.File{read}
			child.Stdout = io.Discard
			child.Stderr = io.Discard
			if e = child.Start(); e != nil {
				t.Fatal(e)
			}
			reaped := false
			defer func() {
				if !reaped {
					_ = child.Process.Kill()
					_ = child.Wait()
				}
			}()
			if e = json.NewEncoder(write).Encode(input); e != nil {
				t.Fatal("private TEST child transfer failed")
			}
			write.Close()
			read.Close()
			var ready struct {
				API string `json:"api"`
			}
			githubWaitFile(t, input.Ready, &ready)
			remote := &server{t: t, url: ready.API}
			base := "/projects/" + project.String() + "/cli-auth/operations"
			id := githubAppStart(t, remote, base, &old)
			var unknown <-chan error
			if phase == "after_commit_before_response" {
				committed := false
				for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
					op, e := observer.GetProjectCLIAuthOperation(context.Background(), project, id)
					if e != nil {
						t.Fatal(e)
					}
					if op.Status == "succeeded" {
						committed = true
						break
					}
				}
				if !committed {
					t.Fatal("child did not commit its actual OAuth result")
				}
				done := make(chan error, 1)
				unknown = done
				go func() {
					client := &http.Client{Timeout: 15 * time.Second}
					response, e := client.Get(ready.API + base + "/" + id.String())
					if e == nil {
						response.Body.Close()
					}
					done <- e
				}()
			}
			var barrier struct {
				Phase       string `json:"phase"`
				DeviceCalls int    `json:"deviceCalls"`
				UserCalls   int    `json:"userCalls"`
				Denied      int    `json:"deniedDials"`
			}
			githubWaitFile(t, input.Barrier, &barrier)
			if barrier.Phase != phase || barrier.DeviceCalls != 1 || barrier.UserCalls != 1 || barrier.Denied != 0 {
				t.Fatal("crash barrier bypassed actual bounded device/identity protocol")
			}
			// The first phase has not committed at its barrier; the second phase
			// has committed in PG but its original HTTP success projection is withheld.
			if e = child.Process.Kill(); e != nil {
				t.Fatal(e)
			}
			e = child.Wait()
			exit, ok := e.(*exec.ExitError)
			if !ok {
				t.Fatal("child did not actually die from SIGKILL")
			}
			status, ok := exit.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
				t.Fatal("child exit was not SIGKILL")
			}
			reaped = true
			if unknown != nil {
				select {
				case err := <-unknown:
					if err == nil {
						t.Fatal("success reached caller before intended crash")
					}
				case <-time.After(2 * time.Second):
					t.Fatal("crashed HTTP response did not terminate")
				}
			}
			recovered := w.start(nil)
			defer recovered.stop()
			state := decode[cm.CLIAuthState](t, recovered.must(200, "GET", base+"/"+id.String(), nil))
			current := decode[cm.Account](t, recovered.must(200, "GET", accounts+"/"+old.ID.String(), nil))
			if state.Operation.UserCode != nil || state.Operation.AuthorizationURL != nil || current.Name != old.Name {
				t.Fatal("crash recovery retained interaction or changed remark")
			}
			if phase == "before_commit" {
				if state.Operation.Status != "failed" || state.Operation.ErrorCode == nil || *state.Operation.ErrorCode != "server_restarted" || *current.CurrentVersionID != *old.CurrentVersionID || current.GitHubIdentity != nil || current.Revision != old.Revision {
					t.Fatal("uncommitted OAuth changed saved PAT")
				}
			} else if state.Operation.Status != "succeeded" || state.Account == nil || current.GitHubIdentity == nil || current.GitHubIdentity.UserID != "202" || *current.CurrentVersionID == *old.CurrentVersionID || current.Revision != old.Revision+1 {
				t.Fatal("committed original operation lost or reapplied")
			}
			row, _, e := recovered.a.Store.GetProjectCLIVersion(context.Background(), old.ID, *old.CurrentVersionID)
			if e != nil || sha256.Sum256(row.Secret) != oldHash {
				t.Fatal("restart rewrote original PAT", e)
			}
			var versions, identities, runs, turns int
			e = recovered.a.Store.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM project_cli_versions WHERE account_id=$1),(SELECT count(*) FROM github_cli_identities WHERE account_id=$1),(SELECT count(*) FROM runs),(SELECT count(*) FROM task_turns)`, old.ID).Scan(&versions, &identities, &runs, &turns)
			want := 1
			if phase != "before_commit" {
				want = 2
			}
			if e != nil || versions != want || identities != want-1 || runs+turns != 0 {
				t.Fatal("crash left partial/duplicate result or started model work", e)
			}
		})
	}
}
