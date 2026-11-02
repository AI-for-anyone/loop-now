package httpapi_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"loop-now/internal/server/modelauth"
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
	for _, path := range []string{"/usr/bin/sandbox-exec", node} {
		if _, err := os.Stat(path); err != nil {
			t.Skipf("required fixed offline dependency unavailable: %s", path)
		}
	}
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
