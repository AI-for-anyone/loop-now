package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"loop-now/internal/server/contracttest"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/redistest"
)

const safetyAccess = "safety-private-access-sentinel"
const safetyRefresh = "safety-private-refresh-sentinel"
const safetyKey = "safety-private-key-sentinel"
const safetyCLI = "safety-private-cli-sentinel"
const safetyRaw = "safety-raw-provider-sentinel"
const safetyURL = "https://auth.openai.com/oauth/authorize?state=channel-state-sentinel&code_challenge=channel-challenge-sentinel"

type safetyLogs struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *safetyLogs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *safetyLogs) Snapshot() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]byte(nil), l.b.Bytes()...)
}

// Only self-owned PG/Redis instances, no .env.local fallback or existing sockets.
func safetyResources(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/private/tmp", "ln-safety-")
	if err != nil {
		t.Fatal(err)
	}
	command := func(name string, args ...string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		c := exec.CommandContext(ctx, "/opt/homebrew/opt/postgresql@18/bin/"+name, args...)
		c.Env = []string{"HOME=" + root, "PATH=/usr/bin:/bin", "LC_ALL=C"}
		c.Stdout = io.Discard
		c.Stderr = io.Discard
		return c.Run()
	}
	data, socket := filepath.Join(root, "pg"), filepath.Join(root, "socket")
	if err = os.Mkdir(socket, 0700); err != nil {
		t.Fatal(err)
	}
	if err = command("initdb", "-D", data, "-U", "fixture", "-A", "trust", "--no-locale", "-E", "UTF8"); err != nil {
		os.RemoveAll(root)
		t.Fatal("own PG init failed")
	}
	if err = command("pg_ctl", "-D", data, "-l", filepath.Join(root, "pg.log"), "-o", "-F -h '' -k "+socket+" -p 55434", "-w", "-t", "10", "start"); err != nil {
		os.RemoveAll(root)
		t.Fatal("own PG start failed")
	}
	t.Cleanup(func() {
		if err := command("pg_ctl", "-D", data, "-m", "immediate", "-w", "-t", "10", "stop"); err != nil {
			t.Error("own PG stop failed")
			return
		}
		os.RemoveAll(root)
	})
	if err = command("createdb", "-h", socket, "-p", "55434", "-U", "fixture", "loop_now_test"); err != nil {
		t.Fatal("own test DB creation failed")
	}
	t.Setenv(pgtest.EnvTestDatabaseURL, "postgres://fixture@/loop_now_test?host="+url.QueryEscape(socket)+"&port=55434&sslmode=disable")
	redisSocket := filepath.Join(root, "redis.sock")
	redis := exec.Command("/opt/homebrew/bin/redis-server", "--port", "0", "--unixsocket", redisSocket, "--unixsocketperm", "700", "--save", "", "--appendonly", "no", "--dir", root)
	redis.Env = []string{"HOME=" + root, "PATH=/usr/bin:/bin"}
	redis.Stdout = io.Discard
	redis.Stderr = io.Discard
	if err = redis.Start(); err != nil {
		t.Fatal("own Redis start failed")
	}
	done := make(chan error, 1)
	go func() { done <- redis.Wait() }()
	t.Cleanup(func() {
		redis.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			redis.Process.Kill()
			<-done
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err = os.Stat(redisSocket); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err = os.Stat(redisSocket); err != nil {
		t.Fatal("own Redis socket unavailable")
	}
	t.Setenv(redistest.EnvTestRedisURL, "unix://"+redisSocket+"?db=10")
	return root
}

func safetyNoSecrets(t *testing.T, data []byte, allowAuthorizationURL bool) {
	t.Helper()
	for _, marker := range []string{safetyAccess, safetyRefresh, safetyKey, safetyCLI, safetyRaw, "unsafe-channel-sentinel", "lne_", "lna_"} {
		if bytes.Contains(data, []byte(marker)) {
			t.Fatal("secret sentinel reached an ordinary channel (content withheld)")
		}
	}
	lower := strings.ToLower(string(data))
	for _, key := range []string{`"access":`, `"refresh":`, `"access_token":`, `"refresh_token":`, `"ciphertext":`, `"wrapped_dek":`, `"wrappeddek":`, `"nonce":`} {
		if strings.Contains(lower, key) {
			t.Fatal("secret/cipher field reached ordinary channel (content withheld)")
		}
	}
	if !allowAuthorizationURL && (bytes.Contains(data, []byte("oauth/authorize")) || bytes.Contains(data, []byte("channel-state-sentinel")) || bytes.Contains(data, []byte("channel-challenge-sentinel"))) {
		t.Fatal("authorization channel reached ordinary/terminal view (content withheld)")
	}
}

func safetyRequest(t *testing.T, c caller, status int, method, path string, body any, allowURL bool, headers ...string) []byte {
	t.Helper()
	got, data, _ := c.do(method, path, body, headers...)
	safetyNoSecrets(t, data, allowURL)
	if got != status {
		t.Fatalf("HTTP safety sample status: want %d got %d (body withheld)", status, got)
	}
	return data
}
func safetyDecode[T any](t *testing.T, data []byte) T {
	t.Helper()
	var v T
	if json.Unmarshal(data, &v) != nil {
		t.Fatal("invalid typed HTTP response (body withheld)")
	}
	return v
}

// Real modelauth.Helper transports fd3/4; its child imports fs/readline only.
// No SDK, provider, browser, auth callback or network exists in this fixture.
func safetyHelper(t *testing.T, root string) *modelauth.Helper {
	t.Helper()
	const node = "/Users/ljl/.local/node-v24.21.0/bin/node"
	wrapper := filepath.Join(root, "node-offline")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec /usr/bin/sandbox-exec -p '(version 1) (allow default) (deny network*)' "+node+" \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(root, "helper.mjs")
	script := `import fs from 'node:fs';import readline from 'node:readline';
 const config=JSON.parse(fs.readFileSync(new URL('./scenario.json',import.meta.url),'utf8'));
 const send=v=>fs.writeSync(4,JSON.stringify(v)+'\n');
 const credential={type:'oauth',access:'` + safetyAccess + `',refresh:'` + safetyRefresh + `',expires:Date.now()+3600000,accountId:'synthetic-safe-account'};
 send({type:'ready',protocolVersion:1,expectedPiVersion:'1.1.0'});
 let poll;
 readline.createInterface({input:fs.createReadStream('',{fd:3})}).on('line',line=>{
  const f=JSON.parse(line);
  if(f.type==='abort'){clearInterval(poll);send({type:'result',id:f.id,status:'cancelled'});return;}
  console.log('` + safetyRaw + `');console.error('` + safetyAccess + `');
  send({type:'notify',id:f.id,event:{type:'auth_url',url:config.url}});
  if(config.mode==='failure'){
   send({type:'diagnostic',id:f.id,stage:'exchange_http',category:'http_error',httpStatus:401});
   send({type:'diagnostic',id:f.id,stage:'` + safetyRaw + `',category:'` + safetyRaw + `',httpStatus:999,message:'` + safetyRaw + `',url:config.url});
   send({type:'result',id:f.id,status:'failed',code:'` + safetyRaw + `',message:'` + safetyRaw + `',body:'` + safetyAccess + `'});return;
  }
  if(config.mode==='success')poll=setInterval(()=>{if(fs.existsSync(new URL('./release',import.meta.url))){clearInterval(poll);send({type:'result',id:f.id,status:'succeeded',credential});}},5);
 });setInterval(()=>{},1000);`
	if err := os.WriteFile(entry, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	return &modelauth.Helper{Options: modelauth.HelperOptions{NodeBin: wrapper, Entry: entry, WorkRoot: root, BasePath: "/usr/bin:/bin", PiPackageRoot: root, Timeout: 5 * time.Second, StopGrace: time.Second}}
}

func TestProjectCredentialSecretChannels(t *testing.T) {
	root := safetyResources(t)
	e := newEnv(t)
	ctx := context.Background()
	logs := &safetyLogs{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	e.api.Log = logger
	e.api.Config.Logger = logger
	e.api.Config.ModelAuth.Log = logger
	e.api.Config.ModelAuth.Helper = safetyHelper(t, root)
	e.api.Config.ModelAuth.Changed = e.conv.EmitAgentStatus
	console := e.console()
	_, token := e.identity("safety-admin")
	external := e.external(token)
	base := "/projects/" + e.dc.Project.ID.String()
	accounts := base + "/credential-accounts"
	operations := base + "/model-auth/operations"
	agent, err := e.st.CreateAgent(ctx, store.ProjectScope(e.dc.Project.ID), "安全使用者", "openai-codex", "gpt-6.1-sol", "")
	if err != nil {
		t.Fatal(err)
	}
	agentPath := "/agents/" + agent.ID.String()
	agentStream, stopAgent := readSSE(t, e, agentPath, "")
	defer stopAgent()
	overviewStream, stopOverview := readSSEPath(t, e, "/overview/events", "")
	defer stopOverview()
	checkSSE := func(agentEvent, overviewEvent sseEvent) {
		safetyNoSecrets(t, []byte(agentEvent.data), false)
		safetyNoSecrets(t, []byte(overviewEvent.data), false)
		e.v.ValidateSSE(agentEvent.event, []byte(agentEvent.data))
		e.v.ValidateStreamSSE(contracttest.OverviewEventsPath, overviewEvent.event, []byte(overviewEvent.data))
	}
	checkSSE(next(t, agentStream), next(t, overviewStream))
	checkCurrentSSE := func() {
		ch, stop := readSSE(t, e, agentPath, "")
		defer stop()
		e.conv.EmitAgentStatus(agent.ID)
		checkSSE(next(t, ch), next(t, overviewStream))
	}
	// Legitimate stored API-key and CLI secrets exercise request bodies/audits and
	// ordinary account projections. OAuth success below exercises fd-only secrets.
	keyAccount := safetyDecode[cm.Account](t, safetyRequest(t, external, 201, "POST", accounts, map[string]any{"purpose": "model", "provider": "anthropic", "name": "safe-key", "credential": map[string]string{"type": "api_key", "key": safetyKey}}, false))
	cliAccount := safetyDecode[cm.Account](t, safetyRequest(t, external, 201, "POST", accounts, map[string]any{"purpose": "cli", "cli": "gh", "name": "safe-cli", "credential": map[string]any{"type": "cli", "fields": map[string]string{"token": safetyCLI}}}, false))
	safetyRequest(t, console, 422, "POST", accounts, map[string]any{"purpose": "model", "provider": "anthropic", "name": "bad", "credential": map[string]string{"type": "api_key", "key": safetyKey, "metadata": safetyRaw}}, false, "X-Contract-Invalid-Request", "1")
	for _, id := range []string{keyAccount.ID.String(), cliAccount.ID.String()} {
		safetyRequest(t, console, 200, "GET", accounts+"/"+id, nil, false)
	}
	configure := func(mode, url string) {
		t.Helper()
		os.Remove(filepath.Join(root, "release"))
		b, _ := json.Marshal(map[string]string{"mode": mode, "url": url})
		if err := os.WriteFile(filepath.Join(root, "scenario.json"), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	start := func(name string) cm.LoginState {
		return safetyDecode[cm.LoginState](t, safetyRequest(t, external, 202, "POST", operations, map[string]string{"provider": "openai-codex", "name": name}, true))
	}
	state := func(id string) cm.LoginState {
		return safetyDecode[cm.LoginState](t, safetyRequest(t, console, 200, "GET", operations+"/"+id, nil, true))
	}
	waitState := func(id, want string) cm.LoginState {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			s := state(id)
			if s.Operation.Status == want {
				return s
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("login state not reached (body withheld)")
		return cm.LoginState{}
	}
	ordinary := func() {
		for _, path := range []string{accounts, agentPath, agentPath + "/credential-selections", base + "/credential-account-impact?purpose=model&provider=anthropic"} {
			safetyRequest(t, console, 200, "GET", path, nil, false)
		}
	}
	t.Run("active_only_then_success_terminal_clear", func(t *testing.T) {
		configure("success", safetyURL)
		op := start("safe-oauth")
		id := op.Operation.ID.String()
		active := waitState(id, "awaiting_authorization")
		if active.Operation.AuthorizationURL == nil || *active.Operation.AuthorizationURL != safetyURL {
			t.Fatal("active authorization channel missing")
		}
		list := safetyDecode[cm.Operations](t, safetyRequest(t, external, 200, "GET", operations, nil, true))
		if len(list.Operations) != 1 || list.Operations[0].AuthorizationURL == nil {
			t.Fatal("active recovery channel missing")
		}
		ordinary()
		checkCurrentSSE()
		if err := os.WriteFile(filepath.Join(root, "release"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		final := waitState(id, "succeeded")
		data := safetyRequest(t, external, 200, "GET", operations+"/"+id, nil, false)
		if final.Operation.AuthorizationURL != nil || final.Account == nil || final.Account.CurrentVersionID == nil {
			t.Fatal("success did not clear URL/commit account")
		}
		safetyNoSecrets(t, data, false)
		ordinary()
		safetyRequest(t, console, 200, "GET", accounts+"/"+final.Account.ID.String(), nil, false)
		terminalList := safetyDecode[cm.Operations](t, safetyRequest(t, console, 200, "GET", operations, nil, false))
		if len(terminalList.Operations) != 0 {
			t.Fatal("terminal operation remained in active discovery")
		}
		checkCurrentSSE()
	})
	t.Run("raw_provider_failure_fixed_diagnostics", func(t *testing.T) {
		configure("failure", safetyURL)
		op := start("safe-failure")
		final := waitState(op.Operation.ID.String(), "failed")
		if final.Operation.AuthorizationURL != nil || final.Operation.ErrorCode == nil || *final.Operation.ErrorCode != "helper_unavailable" {
			t.Fatal("unsafe/incorrect failed projection")
		}
		safetyRequest(t, console, 200, "GET", operations+"/"+op.Operation.ID.String(), nil, false)
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if bytes.Contains(logs.Snapshot(), []byte(`"providerStage":"exchange_http","category":"http_error","httpStatus":401`)) {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		safe := logs.Snapshot()
		safetyNoSecrets(t, safe, false)
		if !bytes.Contains(safe, []byte(`"stage":"helper_result_failed"`)) || !bytes.Contains(safe, []byte(`"providerStage":"exchange_http","category":"http_error","httpStatus":401`)) {
			t.Fatal("fixed failure diagnostic missing (logs withheld)")
		}
		if bytes.Contains(safe, []byte(`"httpStatus":999`)) {
			t.Fatal("untrusted HTTP status reached logs")
		}
	})
	t.Run("active_cancel_terminal_clear", func(t *testing.T) {
		configure("hold", safetyURL)
		op := start("safe-cancel")
		waitState(op.Operation.ID.String(), "awaiting_authorization")
		final := safetyDecode[cm.LoginState](t, safetyRequest(t, external, 200, "POST", operations+"/"+op.Operation.ID.String()+"/cancel", nil, false))
		if final.Operation.Status != "cancelled" || final.Operation.AuthorizationURL != nil {
			t.Fatal("cancel retained authorization channel")
		}
	})
	for name, unsafe := range map[string]string{
		"issuer":        "https://example.invalid/oauth/authorize?state=unsafe-channel-sentinel&code_challenge=x",
		"http":          "http://auth.openai.com/oauth/authorize?state=unsafe-channel-sentinel&code_challenge=x",
		"path":          "https://auth.openai.com/not-authorize?state=unsafe-channel-sentinel&code_challenge=x",
		"code":          safetyURL + "&code=unsafe-channel-sentinel",
		"access_token":  safetyURL + "&access_token=unsafe-channel-sentinel",
		"refresh_token": safetyURL + "&refresh_token=unsafe-channel-sentinel",
		"id_token":      safetyURL + "&id_token=unsafe-channel-sentinel",
		"verifier":      safetyURL + "&code_verifier=unsafe-channel-sentinel",
	} {
		t.Run("unsafe_url_"+name, func(t *testing.T) {
			configure("hold", unsafe)
			op := start("bad-" + name)
			final := waitState(op.Operation.ID.String(), "failed")
			if final.Operation.AuthorizationURL != nil || final.Account != nil {
				t.Fatal("unsafe URL saved or account created")
			}
			safetyRequest(t, console, 200, "GET", operations+"/"+op.Operation.ID.String(), nil, false)
		})
	}
	ordinary()
	safetyNoSecrets(t, logs.Snapshot(), false)
	stopAgent()
	stopOverview()
	rows, err := e.st.Pool.Query(ctx, `SELECT row_to_json(a)::text FROM audit_log a`)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	seen := map[string]bool{}
	for rows.Next() {
		var data []byte
		if rows.Scan(&data) != nil {
			t.Fatal("audit read failed")
		}
		safetyNoSecrets(t, data, false)
		var a struct {
			Operation string `json:"operation"`
		}
		json.Unmarshal(data, &a)
		seen[a.Operation] = true
		count++
	}
	rows.Close()
	if rows.Err() != nil || count == 0 || !seen["createProjectCredentialAccount"] || !seen["startProjectModelAuthLogin"] || !seen["cancelProjectModelAuthLogin"] {
		t.Fatal("audit coverage empty/incomplete")
	}
	var stored int
	if err = e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM model_auth_versions WHERE project_account_id IS NOT NULL AND octet_length(secret)>0`).Scan(&stored); err != nil || stored != 2 {
		t.Fatal("failure/unsafe URL wrote OAuth secrets")
	}
	for _, v := range []*contracttest.Validator{e.v, e.extV} {
		if v != nil && len(v.Failures()) != 0 {
			t.Fatal("formal HTTP/SSE validator failures (details withheld)")
		}
	}
	t.Logf("formal HTTP, safe logs, %d audit rows, Agent/overview snapshot+status SSE; 8 unsafe URLs rejected; stored models=2; no provider/network requests", count)
}
