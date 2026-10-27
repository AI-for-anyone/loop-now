package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"loop-now/internal/server/app"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/cliverify"
	verifyservice "loop-now/internal/server/cliverify/service"
	"loop-now/internal/server/config"
	"loop-now/internal/server/store"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type verificationAppCrashConfig struct {
	Config                        config.Config
	Schema, APIReady, NativeReady string
}

func TestCLIVerificationAppCrashChild(t *testing.T) {
	if os.Getenv("LOOP_VERIFY_APP_CRASH_CHILD") != "1" {
		return
	}
	private := os.NewFile(3, "private-test-config")
	var input verificationAppCrashConfig
	if json.NewDecoder(io.LimitReader(private, 1<<20)).Decode(&input) != nil {
		os.Exit(41)
	}
	private.Close()
	factory := func(_ context.Context, spec verifyservice.CommandSpec) (*exec.Cmd, error) {
		if spec.Phase == "version" {
			return exec.Command("/bin/sh", "-c", "printf 'gh version 2.100.0\\n'"), nil
		}
		script := `import os,socket,time,json,sys; s=socket.socket(); s.bind(('127.0.0.1',0)); s.listen(); open(sys.argv[1],'w').write(json.dumps({'pid':os.getpid(),'port':s.getsockname()[1],'home':os.getcwd()})); time.sleep(60)`
		return exec.Command("/usr/bin/python3", "-S", "-c", script, input.NativeReady), nil
	}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		os.Exit(42)
	}
	a, e := app.Build(context.Background(), &input.Config, config.NewLogger(io.Discard, "json", 0), app.Options{SearchPath: input.Schema, Listener: ln, HomeDirs: []string{"/nonexistent-home"}, CLIVerificationCommand: factory})
	if e != nil {
		os.Exit(43)
	}
	go a.Serve()
	raw, _ := json.Marshal(map[string]string{"api": "http://" + a.Addr() + "/api/v1"})
	if os.WriteFile(input.APIReady, raw, 0600) != nil {
		os.Exit(44)
	}
	select {}
}
func TestCLIVerificationActualAppSIGKILLRecovery(t *testing.T) {
	w := newWorld(t)
	w.cfg.NodeBin = "/Users/ljl/.local/node-v24.21.0/bin/node"
	w.cfg.CLIProbeTimeout = time.Minute
	w.cfg.AuthRecordTimeout = time.Second
	w.cfg.CLIInstallTimeout = time.Second
	ctx := context.Background()
	st, e := store.Open(ctx, w.cfg.DatabaseURL, store.OpenOptions{SearchPath: w.schema})
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	if e = st.Migrate(ctx, config.NewLogger(io.Discard, "json", 0)); e != nil {
		t.Fatal(e)
	}
	defaults, e := st.EnsureDefaults(ctx, w.cfg.DefaultOrgName, w.cfg.DefaultProjectName)
	if e != nil {
		t.Fatal(e)
	}
	payload, _ := json.Marshal(clitemplate.Payload{Env: map[string]string{"GH_TOKEN": "synthetic-crash-only-token"}, Files: []clitemplate.PayloadFile{}})
	record, e := st.CreateProjectCredential(ctx, defaults.Project.ID, "cli", "gh", "recovery fixture", store.ProjectCredentialWrite{AuthMethod: "static", TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1"}, Secret: payload})
	clear(payload)
	if e != nil {
		t.Fatal(e)
	}
	bin := filepath.Join(w.cfg.WorkRoot, "tools/gh/2.100.0/bin")
	if e = os.MkdirAll(bin, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(bin, "gh"), []byte("test-only version/probe command fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	control := t.TempDir()
	input := verificationAppCrashConfig{Config: w.cfg, Schema: w.schema, APIReady: filepath.Join(control, "api.json"), NativeReady: filepath.Join(control, "native.json")}
	read, write, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer read.Close()
	defer write.Close()
	child := exec.Command(os.Args[0], "-test.run=^TestCLIVerificationAppCrashChild$")
	child.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LOOP_VERIFY_APP_CRASH_CHILD=1"}
	child.ExtraFiles = []*os.File{read}
	child.Stdout = io.Discard
	child.Stderr = io.Discard
	if e = child.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	// The TEST DSN travels only through this inherited private
	// pipe; never argv, ordinary file or diagnostic. This is not a live backup.
	if e = json.NewEncoder(write).Encode(input); e != nil {
		t.Fatal("private child config transfer failed")
	}
	write.Close()
	read.Close()
	var ready struct {
		API string `json:"api"`
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		b, e := os.ReadFile(input.APIReady)
		if e == nil && json.Unmarshal(b, &ready) == nil && ready.API != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual child App failed to start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	id := uuid.New()
	base := "/projects/" + defaults.Project.ID.String() + "/credential-accounts/" + record.View.ID.String() + "/verification-operations"
	body, _ := json.Marshal(cliverify.Start{OperationID: id, ExpectedVersionID: *record.View.CurrentVersionID})
	response, e := http.Post(ready.API+base, "application/json", bytes.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 202 {
		t.Fatal("child actual App did not accept operation")
	}
	var native struct {
		PID, Port int
		Home      string
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		b, e := os.ReadFile(input.NativeReady)
		if e == nil && json.Unmarshal(b, &native) == nil && native.PID > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual App guard/native did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if e = child.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	_ = child.Wait()
	deadline = time.Now().Add(4 * time.Second)
	for {
		_, homeError := os.Stat(native.Home)
		conn, dialError := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(native.Port)), 20*time.Millisecond)
		if conn != nil {
			conn.Close()
		}
		if syscall.Kill(native.PID, 0) != nil && os.IsNotExist(homeError) && dialError != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("killed App left native/listener/private HOME")
		}
		time.Sleep(5 * time.Millisecond)
	}
	var commands atomic.Int32
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	a, e := app.Build(ctx, &w.cfg, config.NewLogger(io.Discard, "json", 0), app.Options{SearchPath: w.schema, Listener: ln, HomeDirs: []string{"/nonexistent-home"}, CLIVerificationCommand: func(context.Context, verifyservice.CommandSpec) (*exec.Cmd, error) {
		commands.Add(1)
		return nil, io.EOF
	}})
	if e != nil {
		t.Fatal(e)
	}
	go a.Serve()
	srv := &server{t: t, a: a, url: "http://" + a.Addr() + "/api/v1"}
	defer srv.stop()
	raw := srv.must(200, "GET", base+"/"+id.String(), nil)
	var op cliverify.Operation
	if json.Unmarshal(raw, &op) != nil || op.Status != "failed" || op.Result == nil || op.Result.ErrorCode == nil || *op.Result.ErrorCode != "server_restarted" {
		t.Fatal("actual restarted App lost safe recovery")
	}
	srv.must(202, "POST", base, cliverify.Start{OperationID: id, ExpectedVersionID: *record.View.CurrentVersionID})
	if commands.Load() != 0 {
		t.Fatal("actual App restart replayed tool/credential probe")
	}
	var runs, turns int
	if e = st.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM runs),(SELECT count(*) FROM task_turns)`).Scan(&runs, &turns); e != nil || runs != 0 || turns != 0 {
		t.Fatal("verification restart dispatched Agent/task", e)
	}
	if strings.Contains(string(raw), "synthetic-crash-only-token") {
		t.Fatal("safe recovery leaked fixture secret")
	}
	t.Log("real temporary App SIGKILL->native PID/listener/HOME ended->same-schema App Build/HTTP original-ID recovery failed server_restarted; command/model/task0; no provider")
}
