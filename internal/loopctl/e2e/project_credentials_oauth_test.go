package e2e_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"loop-now/internal/server/app"
	"loop-now/internal/server/modelauth"
)

// Real modelauth.Helper private fd protocol; the child imports only local fs and
// readline. It cannot initiate OAuth/network or read host login state. All token
// strings are synthetic, and expires is a canonical JSON int64, not 1.23e+12.
type offlineProjectLogin struct {
	root   string
	helper *modelauth.Helper
}

func newOfflineProjectLogin(t *testing.T) *offlineProjectLogin {
	t.Helper()
	const node = "/Users/ljl/.local/node-v24.21.0/bin/node"
	if _, err := os.Stat(node); err != nil {
		t.Skip("offline login fixture needs pinned Node 24.21.0")
	}
	root := t.TempDir()
	entry := filepath.Join(root, "login-fixture.mjs")
	code := `import fs from 'node:fs'; import readline from 'node:readline';
 const root=new URL('.',import.meta.url);
 const event=s=>fs.appendFileSync(new URL('events',root),s+'\n');
 const send=v=>fs.writeSync(4,JSON.stringify(v)+'\n');
 const credential={type:'oauth',access:'synthetic-project-oauth-access',refresh:'synthetic-project-oauth-refresh',expires:1893456000000,accountId:'synthetic-shared-grant'};
 let operation,done=false;
 const result=(status)=>{if(done)return;done=true;event('result:'+status);send({type:'result',id:operation.id,status,credential:status==='succeeded'?credential:undefined,code:status==='failed'?'auth_failed':undefined});};
 send({type:'ready',protocolVersion:1,expectedPiVersion:'1.1.0'});
 readline.createInterface({input:fs.createReadStream('',{fd:3})}).on('line',line=>{
  const frame=JSON.parse(line);
  if(frame.type==='abort'){event('abort');if(operation)result('succeeded');return;}
  if(frame.type!=='login'){throw new Error('fixture_only_login');}
  operation=frame;event('login');
  send({type:'notify',id:frame.id,event:{type:'auth_url',url:'https://auth.openai.com/oauth/authorize?state=offline-project-fixture&code_challenge=fixture&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback'}});
 });
 setInterval(()=>{if(!operation||done)return;const mode=fs.readFileSync(new URL('mode',root),'utf8');if(mode==='success')result('succeeded');if(mode==='failure')result('failed');},10);`
	if err := os.WriteFile(entry, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	f := &offlineProjectLogin{root: root, helper: &modelauth.Helper{Options: modelauth.HelperOptions{NodeBin: node, Entry: entry, WorkRoot: root, BasePath: "/usr/bin:/bin", PiPackageRoot: root, Timeout: 10 * time.Second, StopGrace: time.Second}}}
	f.mode(t, "hold")
	return f
}
func (f *offlineProjectLogin) mode(t *testing.T, mode string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.root, "mode"), []byte(mode), 0600); err != nil {
		t.Fatal(err)
	}
}
func (f *offlineProjectLogin) count(event string) int {
	b, _ := os.ReadFile(filepath.Join(f.root, "events"))
	return strings.Count(string(b), event+"\n")
}
func waitProjectLogin(t *testing.T, cli *loopctl, project, id, status string) map[string]any {
	t.Helper()
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
		state := cli.run("project", "model-account", "login-status", project, id).ok(t)
		op := state["operation"].(map[string]any)
		if op["status"] == status {
			return state
		}
		if op["status"] == "failed" || op["status"] == "expired" || op["status"] == "cancelled" {
			t.Fatalf("operation reached %v before %s (code=%v)", op["status"], status, op["errorCode"])
		}
	}
	t.Fatal("offline login did not reach " + status)
	return nil
}
func newProjectOAuthWorld(t *testing.T) (*world, *server, *loopctl, *offlineProjectLogin) {
	t.Helper()
	fixture := newOfflineProjectLogin(t)
	w := newWorld(t, true)
	w.appOptions = func(o *app.Options) { o.AuthLoginHelper = fixture.helper }
	s := w.start(nil)
	t.Cleanup(s.stop)
	_, _, token := s.issueToken("", "project-oauth-cli", nil)
	external := newLoopctl(t, map[string]string{"LOOPCTL_URL": s.ext, "LOOPCTL_TOKEN": token, "LOOPCTL_CA_FILE": w.caFile})
	t.Logf("offline OAuth resources: schema=%s redisPrefix=%s consoleAddr=%s tlsAddr=%s", w.schema, w.cfg.RedisKeyPrefix, w.addr, w.extAddr)
	return w, s, external, fixture
}
func TestProjectCredentialOAuthCancelWithApp(t *testing.T) {
	_, s, external, fixture := newProjectOAuthWorld(t)
	project := s.defaultProject()
	args := []string{"project", "model-account", "login", project, "--provider", "openai-codex", "--name", "offline new login"}
	first := external.run(args...).ok(t)
	id := first["operation"].(map[string]any)["id"].(string)
	active := waitProjectLogin(t, external, project, id, "awaiting_authorization")["operation"].(map[string]any)
	url, ok := active["authorizationUrl"].(string)
	if !ok || !modelauth.ValidAuthorizationURL(url) {
		t.Fatal("missing safe current authorization URL")
	}
	// A new CLI invocation discovers the active operation without knowing accountId.
	restored := newLoopctl(t, external.vars)
	operations := restored.run("project", "model-account", "login-list", project, "--provider", "openai-codex").ok(t)["operations"].([]any)
	if len(operations) != 1 || operations[0].(map[string]any)["id"] != id {
		t.Fatal("active new login not discoverable")
	}
	// Explicit second invocation tests server dedupe, not automatic CLI replay.
	duplicate := external.run(args...).ok(t)
	if duplicate["operation"].(map[string]any)["id"] != id || fixture.count("login") != 1 {
		t.Fatal("same input started another helper")
	}
	conflict := external.run("project", "model-account", "login", project, "--provider", "openai-codex", "--name", "different input")
	conflict.fails(t, "model_auth_in_progress", 8)
	cancelled := restored.run("project", "model-account", "login-cancel", project, id, "--yes").ok(t)["operation"].(map[string]any)
	if cancelled["status"] != "cancelled" || cancelled["authorizationUrl"] != nil {
		t.Fatal("cancel did not clear URL")
	}
	// Abort deliberately emits a late success; neither account nor operation revives.
	for until := time.Now().Add(3 * time.Second); fixture.count("abort") == 0 && time.Now().Before(until); {
		time.Sleep(10 * time.Millisecond)
	}
	if fixture.count("abort") != 1 {
		t.Fatal("actual helper not aborted")
	}
	after := restored.run("project", "model-account", "login-status", project, id).ok(t)
	if op := after["operation"].(map[string]any); op["status"] != "cancelled" || op["authorizationUrl"] != nil || after["account"] != nil {
		t.Fatal("late success revived cancelled operation")
	}
	restored.run("project", "model-account", "login-cancel", project, id, "--yes").ok(t)
	if len(restored.run("project", "model-account", "login-list", project, "--provider", "openai-codex").ok(t)["operations"].([]any)) != 0 {
		t.Fatal("terminal login still active")
	}
	if len(restored.run("project", "model-account", "list", project, "--provider", "openai-codex").ok(t)["accounts"].([]any)) != 0 {
		t.Fatal("cancel/late result created account")
	}
	for _, r := range []result{conflict} {
		if strings.Contains(r.Stdout, "synthetic-project-oauth-") || strings.Contains(r.Stdout, "auth.openai.com") {
			t.Fatal("conflict exposed credential or URL")
		}
	}
	t.Log("actual TLS App + actual modelauth.Helper offline fd child: start/discover/get, same-input one helper, different-input409, cancel/terminal URL null/active empty, abort late success cannot create account; no real OAuth")
}

func TestProjectCredentialOAuthSuccessAndReloginWithApp(t *testing.T) {
	_, s, external, fixture := newProjectOAuthWorld(t)
	project := s.defaultProject()
	receipt := external.run("project", "model-account", "login", project, "--provider", "openai-codex", "--name", "offline account").ok(t)
	id := receipt["operation"].(map[string]any)["id"].(string)
	waitProjectLogin(t, external, project, id, "awaiting_authorization")
	fixture.mode(t, "success")
	succeeded := waitProjectLogin(t, external, project, id, "succeeded")
	op := succeeded["operation"].(map[string]any)
	account := succeeded["account"].(map[string]any)
	accountID := account["id"].(string)
	versionID := account["currentVersionId"]
	if op["accountId"] != accountID || op["authorizationUrl"] != nil || account["status"] != "logged_in" {
		t.Fatal("success account view inconsistent")
	}
	fixture.mode(t, "hold")
	relogin := external.run("project", "model-account", "login", project, "--provider", "openai-codex", "--account-id", accountID, "--expected-revision", fmt.Sprintf("%.0f", account["revision"]), "--yes").ok(t)
	secondID := relogin["operation"].(map[string]any)["id"].(string)
	waitProjectLogin(t, external, project, secondID, "awaiting_authorization")
	filtered := external.run("project", "model-account", "login-list", project, "--provider", "openai-codex", "--account-id", accountID).ok(t)["operations"].([]any)
	if len(filtered) != 1 || filtered[0].(map[string]any)["id"] != secondID {
		t.Fatal("account-filtered recovery missing relogin")
	}
	fixture.mode(t, "failure")
	failed := waitProjectLoginTerminal(t, external, project, secondID, "failed")
	if failed["operation"].(map[string]any)["authorizationUrl"] != nil || failed["account"].(map[string]any)["currentVersionId"] != versionID {
		t.Fatal("failed relogin changed account or retained URL")
	}
	stored := external.run("project", "model-account", "get", project, accountID).ok(t)
	if stored["currentVersionId"] != versionID || stored["revision"] != account["revision"] {
		t.Fatal("failed relogin mutated current revision")
	}
	cancelled := external.run("project", "model-account", "login-cancel", project, id, "--yes").ok(t)
	if cancelled["operation"].(map[string]any)["status"] != "succeeded" {
		t.Fatal("cancel rewrote succeeded operation")
	}
	if fixture.count("login") != 2 {
		t.Fatal("unexpected helper invocation count")
	}
	for _, state := range []map[string]any{succeeded, failed, stored} {
		text := fmt.Sprint(state)
		if strings.Contains(text, "synthetic-project-oauth-") {
			t.Fatal("safe view exposed token")
		}
	}
	t.Log("actual TLS App + actual Helper synthetic success, consistent account, relogin discovery, failed relogin preserves version/revision, terminal cancel preserves success; no provider/browser calls")
}
func waitProjectLoginTerminal(t *testing.T, cli *loopctl, project, id, status string) map[string]any {
	t.Helper()
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
		state := cli.run("project", "model-account", "login-status", project, id).ok(t)
		if state["operation"].(map[string]any)["status"] == status {
			return state
		}
	}
	t.Fatal("offline login terminal state missing")
	return nil
}
