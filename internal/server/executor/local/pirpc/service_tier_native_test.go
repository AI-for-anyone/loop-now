package pirpc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	authhelper "loop-now/auth-helper"
	"loop-now/internal/server/preflight"
)

// Uses installed Pi 1.4 and its actual Codex request builder. The only fetch is
// intercepted in-process and returns a deterministic 400: no provider call,
// OAuth, credentials from HOME or real model inference is involved.
func TestInstalledPi14CodexServiceTierRequests(t *testing.T) {
	if _, err := exec.LookPath("pi"); err != nil {
		t.Skip("本机没有 pi")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r, err := preflight.Resolve(ctx, preflight.Options{PiBin: "pi", PiVersion: "1.4.0", NodeBin: "node", WorkRoot: filepath.Join(root, "work"), HomeDirs: []string{"/nonexistent"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Pi", r.PiPackageDir, "Node", r.NodeBin)
	private := filepath.Join(root, "private")
	if err = os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	_, entry, err := authhelper.Install(filepath.Join(private, "helper"))
	if err != nil {
		t.Fatal(err)
	}
	extension := filepath.Join(filepath.Dir(entry), "codex-service-tier.mjs")
	preload := filepath.Join(root, "network-fixture.mjs")
	script := `import {zstdDecompressSync} from 'node:zlib';import net from 'node:net';import tls from 'node:tls';import dgram from 'node:dgram';import{appendFileSync,writeFileSync}from'node:fs';let denied=0,captured=0;const deny=()=>{denied++;if(denied===1)process.stderr.write(new Error('fixture socket blocked').stack+'\n');throw Error('socket forbidden by service-tier fixture')};net.Socket.prototype.connect=deny;tls.connect=deny;dgram.createSocket=deny;globalThis.WebSocket=class{constructor(){throw Error('fixture disables WebSocket before dialing')}};globalThis.__tierFixtureFetch=async(input,init)=>{const u=String(input);if(!(u.startsWith('https://chatgpt.com/backend-api/')||u.startsWith('https://api.openai.com/v1/')))deny();const encoding=new Headers(init.headers).get('content-encoding');const raw=encoding==='zstd'?zstdDecompressSync(init.body).toString():typeof init.body==='string'?init.body:Buffer.from(init.body).toString();const body=JSON.parse(raw);appendFileSync(process.env.TIER_REPORT,JSON.stringify({serviceTier:body.service_tier??null,model:body.model})+'\n');captured++;return new Response(JSON.stringify({error:{message:'fixture unsupported tier: no downgrade permitted',type:'invalid_request_error'}}),{status:400,headers:{'content-type':'application/json'}})};process.on('exit',()=>writeFileSync(process.env.TIER_NETWORK,JSON.stringify({denied,captured})));`
	if err = os.WriteFile(preload, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	transport := filepath.Join(root, "transport.mjs")
	if err = os.WriteFile(transport, []byte(`export default function(){globalThis.fetch=globalThis.__tierFixtureFetch;}`), 0600); err != nil {
		t.Fatal(err)
	}
	token := "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"TEST-only"}}`)) + ".TEST"
	session := filepath.Join(root, "session.jsonl")
	for i, tier := range []string{"default", "priority", "ultrafast", "default", "legacy", "noncodex"} {
		t.Run(tier+string(rune('0'+i)), func(t *testing.T) {
			home := filepath.Join(root, tier+string(rune('0'+i)))
			if err := os.Mkdir(home, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(`{"transport":"sse"}`), 0600); err != nil {
				t.Fatal(err)
			}
			auth := map[string]any{"openai-codex": map[string]any{"type": "oauth", "refresh": "TEST-only", "access": token, "expires": time.Now().Add(time.Hour).UnixMilli(), "accountId": "TEST-only"}, "openai": map[string]any{"type": "api_key", "key": "TEST-only"}}
			raw, _ := json.Marshal(auth)
			if err := os.WriteFile(filepath.Join(home, "auth.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			report, network := filepath.Join(home, "requests.jsonl"), filepath.Join(home, "network.json")
			provider, model := "openai-codex", "gpt-6.1-sol"
			if tier == "noncodex" {
				provider, model = "openai", "gpt-4o"
			}
			args := []string{"--import", preload, filepath.Join(r.PiPackageDir, "dist/bundle/cli.js"), "--mode", "rpc", "--offline", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--provider", provider, "--model", model, "--session", session, "-e", transport}
			if tier != "legacy" && tier != "noncodex" {
				args = append(args, "-e", extension)
			}
			cmd := exec.Command(r.NodeBin, args...)
			cmd.Dir = root
			cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "PI_CODING_AGENT_DIR=" + home, "PI_TELEMETRY=0", "PI_OFFLINE=1", "TIER_REPORT=" + report, "TIER_NETWORK=" + network}
			if tier != "legacy" && tier != "noncodex" {
				cmd.Env = append(cmd.Env, "LOOP_NOW_CODEX_SERVICE_TIER="+tier)
			}
			client, err := Start(cmd)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = client.CloseStdin()
				select {
				case <-client.Done():
				case <-time.After(3 * time.Second):
					_ = cmd.Process.Kill()
					<-client.Done()
				}
				raw, err := os.ReadFile(network)
				if err != nil {
					t.Error(err)
					return
				}
				var stats struct{ Denied, Captured int }
				if json.Unmarshal(raw, &stats) != nil || stats.Denied != 0 || stats.Captured != 1 {
					t.Error("network boundary", string(raw))
				}
			}()
			result, err := client.Prompt(ctx, "TEST service tier request", nil)
			if err != nil || result.Outcome != OutcomeFailed {
				t.Fatalf("expected fixture 400 failure without downgrade: %+v %v stderr=%s", result, err, client.Stderr())
			}
			raw, err = os.ReadFile(report)
			if err != nil {
				t.Fatalf("no actual request: %v stderr=%s result=%+v", err, client.Stderr(), result)
			}
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			if len(lines) != 1 {
				t.Fatal("request was replayed/downgraded", string(raw))
			}
			var request struct {
				ServiceTier *string
				Model       string
			}
			if err = json.Unmarshal([]byte(lines[0]), &request); err != nil {
				t.Fatal(err)
			}
			if tier == "legacy" || tier == "noncodex" {
				if request.ServiceTier != nil {
					t.Fatal("legacy/non-Codex tier injected")
				}
			} else if request.ServiceTier == nil || *request.ServiceTier != tier {
				t.Fatal("actual request tier mismatch", string(raw))
			}
		})
	}
}
