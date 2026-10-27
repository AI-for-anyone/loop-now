package e2e_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/app"
	"loop-now/internal/server/cliauth"
	"loop-now/internal/server/credentialmodel"
)

// The actual Go ProcessHelper owns fd framing and process cleanup. Its child is
// a pure local fs fixture, not the official Google SDK or a successful function
// mock. This proves App/storage/CLI integration, never provider authentication.
type offlineGoogleLogin struct {
	root   string
	helper *cliauth.ProcessHelper
}

func newOfflineGoogleLogin(t *testing.T) *offlineGoogleLogin {
	t.Helper()
	const node = "/Users/ljl/.local/node-v24.21.0/bin/node"
	if _, err := os.Stat(node); err != nil {
		t.Skip("offline fd fixture requires pinned Node 24.21.0")
	}
	root := t.TempDir()
	entry := filepath.Join(root, "google-fixture.mjs")
	code := `import fs from 'node:fs';
const root=new URL('.',import.meta.url), event=s=>fs.appendFileSync(new URL('events',root),s+'\n');
const send=v=>fs.writeSync(4,JSON.stringify(v)+'\n');
const request=JSON.parse(fs.readFileSync(3,'utf8'));
if(request.protocol!==1||request.operation!=='login'||request.callbackPort!==8085||request.credential!==null)process.exit(2);
event('login');
const credential={type:'authorized_user',account:'synthetic-google-account',clientId:'synthetic-client',clientSecret:'synthetic-client-secret',refreshToken:'synthetic-google-refresh',accessToken:'synthetic-google-access',expiresAt:1893456000000,scopes:['https://www.googleapis.com/auth/cloud-platform'],raptToken:null};
const query=new URLSearchParams({client_id:'synthetic-client',response_type:'code',redirect_uri:'http://localhost:8085/',scope:'https://www.googleapis.com/auth/cloud-platform',state:'fixture',code_challenge:'fixture',code_challenge_method:'S256',access_type:'offline'});
send({type:'authorization_url',url:'https://accounts.google.com/o/oauth2/auth?'+query,callbackPort:8085});
process.on('SIGTERM',()=>{event('abort');try{send({type:'result',credential})}catch{}process.exit(0)});
setInterval(()=>{const mode=fs.readFileSync(new URL('mode',root),'utf8');if(mode!=='success'&&mode!=='failure')return;event(mode);if(mode==='success')send({type:'result',credential});else send({type:'error',code:'http'});},10);`
	if err := os.WriteFile(entry, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	f := &offlineGoogleLogin{root: root}
	f.helper = &cliauth.ProcessHelper{Options: cliauth.HelperOptions{WorkRoot: filepath.Join(root, "work"), StopGrace: time.Second, Runtime: func(context.Context) (cliauth.HelperRuntime, error) {
		return cliauth.HelperRuntime{SDKRoot: root, PythonRoot: root, Script: entry}, nil
	}, Command: func(context.Context, string, cliauth.HelperRuntime) *exec.Cmd { return exec.Command(node, entry) }}}
	f.mode(t, "hold")
	return f
}
func (f *offlineGoogleLogin) mode(t *testing.T, mode string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.root, "mode"), []byte(mode), 0600); err != nil {
		t.Fatal(err)
	}
}
func (f *offlineGoogleLogin) count(event string) int {
	b, _ := os.ReadFile(filepath.Join(f.root, "events"))
	return strings.Count(string(b), event+"\n")
}

func waitGoogleLogin(t *testing.T, ext *loopctl, project, id, status string) map[string]any {
	t.Helper()
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
		r := ext.run("project", "cli-account", "login-status", project, id)
		data := r.ok(t)
		if strings.Contains(r.Stdout, "synthetic-google-refresh") || strings.Contains(r.Stdout, "synthetic-client-secret") || strings.Contains(r.Stdout, "synthetic-google-access") {
			t.Fatal("Google grant leaked")
		}
		op := data["operation"].(map[string]any)
		if op["status"] == status {
			return data
		}
		if op["status"] == "failed" || op["status"] == "cancelled" || op["status"] == "expired" {
			t.Fatalf("Google fixture reached %v before %s (safeCode=%v)", op["status"], status, op["errorCode"])
		}
	}
	t.Fatal("Google fixture did not reach " + status)
	return nil
}

func TestGoogleOAuthCLIWithApp(t *testing.T) {
	f := newOfflineGoogleLogin(t)
	w := newWorld(t, true)
	w.appOptions = func(o *app.Options) { o.CLILoginHelper = f.helper }
	s := w.start(nil)
	defer func() { s.stop() }()
	t.Logf("Google OAuth CLI resources: schema=%s redisPrefix=%s consoleAddr=%s tlsAddr=%s", w.schema, w.cfg.RedisKeyPrefix, w.addr, w.extAddr)
	_, _, token := s.issueToken("", "google-oauth-cli", nil)
	ext := newLoopctl(t, map[string]string{"LOOPCTL_URL": s.ext, "LOOPCTL_TOKEN": token, "LOOPCTL_CA_FILE": w.caFile})
	project := s.defaultProject()
	start := func(id, name string) result {
		return ext.run("project", "cli-account", "login", project, "--cli", "gcloud", "--operation-id", id, "--name", name)
	}
	first := uuid.NewString()
	start(first, "Google cancel fixture").ok(t)
	waitGoogleLogin(t, ext, project, first, "awaiting_authorization")
	if len(ext.run("project", "cli-account", "login-list", project).ok(t)["operations"].([]any)) != 1 {
		t.Fatal("active new account operation undiscoverable")
	}
	start(first, "Google cancel fixture").ok(t)
	if f.count("login") != 1 {
		t.Fatal("stable start replay spawned another actual process")
	}
	start(first, "different body").fails(t, "cli_auth_in_progress", 8)
	ext.run("project", "cli-account", "login-cancel", project, first, "--yes").ok(t)
	cancelled := waitGoogleLogin(t, ext, project, first, "cancelled")
	if cancelled["operation"].(map[string]any)["authorizationUrl"] != nil || cancelled["account"] != nil {
		t.Fatal("cancelled operation leaked URL or created account")
	}
	if len(ext.run("project", "cli-account", "list", project, "--cli", "gcloud").ok(t)["accounts"].([]any)) != 0 {
		t.Fatal("late result after cancel created account")
	}
	if f.count("abort") != 1 {
		t.Fatal("actual private child not terminated/reaped")
	}
	// Success submits a synthetic grant through the actual fd parser and PG CAS.
	second := uuid.NewString()
	start(second, "Google success fixture").ok(t)
	waitGoogleLogin(t, ext, project, second, "awaiting_authorization")
	f.mode(t, "success")
	success := waitGoogleLogin(t, ext, project, second, "succeeded")
	a := success["account"].(map[string]any)
	account := a["id"].(string)
	version := a["currentVersionId"]
	if a["authMethod"] != "oauth" || a["cli"] != "gcloud" || success["operation"].(map[string]any)["authorizationUrl"] != nil {
		t.Fatal("successful Google metadata wrong")
	}
	count := f.count("login")
	start(second, "Google success fixture").ok(t)
	if f.count("login") != count {
		t.Fatal("terminal stable replay spawned helper")
	}
	f.mode(t, "hold")
	third := uuid.NewString()
	ext.run("project", "cli-account", "login", project, "--cli", "gcloud", "--operation-id", third, "--account-id", account, "--expected-revision", "1", "--yes").ok(t)
	waitGoogleLogin(t, ext, project, third, "awaiting_authorization")
	if len(ext.run("project", "cli-account", "login-list", project, "--account-id", account).ok(t)["operations"].([]any)) != 1 {
		t.Fatal("relogin filter missing")
	}
	f.mode(t, "failure")
	failed := waitGoogleLogin(t, ext, project, third, "failed")
	if failed["operation"].(map[string]any)["authorizationUrl"] != nil {
		t.Fatal("failed authorization URL retained")
	}
	got := ext.run("project", "cli-account", "get", project, account).ok(t)
	if got["currentVersionId"] != version || got["revision"] != a["revision"] {
		t.Fatal("failed relogin replaced prior grant")
	}
	// Preserve the same operation across an actual temporary App stop/restart.
	f.mode(t, "hold")
	fourth := uuid.NewString()
	start(fourth, "Google restart fixture").ok(t)
	waitGoogleLogin(t, ext, project, fourth, "awaiting_authorization")
	// A persisted pending row with no live worker models a pre-dispatch restart.
	// This is an explicit TEST storage fixture, not a killed-provider claim.
	pending := uuid.New()
	accountUUID, revision := uuid.MustParse(account), int64(1)
	if _, _, err := s.a.Store.NewProjectCLIAuthOperation(context.Background(), uuid.MustParse(project), credentialmodel.CLIAuthStart{OperationID: pending, CLI: "gcloud", AuthType: "google_user", AccountID: &accountUUID, ExpectedRevision: &revision}, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	s.stop()
	s = w.start(nil)
	// Graceful shutdown cancels its actual live helper; it is not a crash.
	waitGoogleLogin(t, ext, project, fourth, "cancelled")
	recovered := waitGoogleLogin(t, ext, project, pending.String(), "failed")
	if recovered["operation"].(map[string]any)["errorCode"] != "server_restarted" {
		t.Fatal("pending persisted operation did not recover with safe restart reason")
	}
	if recovered["operation"].(map[string]any)["authorizationUrl"] != nil || len(ext.run("project", "cli-account", "login-list", project).ok(t)["operations"].([]any)) != 0 {
		t.Fatal("restart retained active URL or operation")
	}
	if f.count("login") != 4 {
		t.Fatal("App recovery replayed provider source")
	}
	// A static SA replacement is a new version of the same account. The old
	// synthetic OAuth grant stays stored; this is not a task/runtime proof.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	sa, _ := json.Marshal(map[string]string{"type": "service_account", "project_id": "synthetic-owner", "private_key_id": "synthetic-id", "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "client_email": "synthetic@fixture.iam.gserviceaccount.com", "client_id": "1234567890", "token_uri": "https://oauth2.googleapis.com/token"})
	input, _ := json.Marshal(map[string]any{"type": "cli", "fields": map[string]string{"serviceAccountJson": string(sa)}})
	ext.stdin = string(input)
	replaced := ext.run("project", "cli-account", "replace", project, account, "--expected-revision", "1", "--from-file", "-", "--yes").ok(t)
	if replaced["id"] != account || replaced["authMethod"] != "service_account" || replaced["currentVersionId"] == version {
		t.Fatal("OAuth to SA replacement lost stable identity/version semantics")
	}
	var retained bool
	if err := s.a.Store.Pool.QueryRow(context.Background(), "SELECT EXISTS(SELECT 1 FROM google_cli_grants WHERE version_id=$1)", version).Scan(&retained); err != nil || !retained {
		t.Fatal("OAuth to SA replacement removed old grant")
	}
	if _, err := os.Stat(filepath.Join(f.root, "events")); err != nil {
		t.Fatal(err)
	}
	t.Log("actual App+Go ProcessHelper fd: stable start, active discovery, cancellation/late result, synthetic commit, terminal replay, failed relogin preserves version, temporary App restart; no official SDK/provider/model/runtime claims")
}
