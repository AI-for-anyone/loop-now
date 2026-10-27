package pirpc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"loop-now/internal/server/messageimage"
	"loop-now/internal/server/preflight"
)

// Installed Pi 1.4, in-process synthetic provider, every socket/fetch denied.
// This proves visual RPC/model/session plumbing, not real-provider image reasoning.
func TestInstalledPiImagesReachModelAndPersist(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	root := t.TempDir()
	r, e := preflight.Resolve(ctx, preflight.Options{PiBin: "pi", PiVersion: "1.4.0", NodeBin: "node", WorkRoot: filepath.Join(root, "work"), HomeDirs: []string{"/nonexistent"}})
	if e != nil {
		t.Fatal(e)
	}
	preload := filepath.Join(root, "deny.mjs")
	if e = os.WriteFile(preload, []byte(`import net from 'node:net';import tls from 'node:tls';import dgram from 'node:dgram';import {writeFileSync} from 'node:fs';let denied=0;const reject=()=>{denied++;throw Error('network denied by image fixture')};net.Socket.prototype.connect=reject;tls.connect=reject;dgram.createSocket=reject;globalThis.fetch=reject;process.on('exit',()=>writeFileSync(process.env.IMAGE_NETWORK_REPORT,JSON.stringify({denied})));`), 0600); e != nil {
		t.Fatal(e)
	}
	provider := filepath.Join(root, "provider.mjs")
	script := `import {pathToFileURL} from 'node:url';import {join} from 'node:path';import {writeFileSync} from 'node:fs';const {createAssistantMessageEventStream}=await import(pathToFileURL(join(process.env.IMAGE_PI_ROOT,'node_modules/@earendil-works/pi-ai/dist/utils/event-stream.js')).href);export default function(pi){let turns=0;pi.registerProvider('image-offline',{baseUrl:'http://127.0.0.1:1',api:'image-offline',apiKey:'synthetic-only',models:[{id:'fixture',name:'Image fixture',reasoning:false,input:['text','image'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:32000,maxTokens:128}],streamSimple(model,context){const messages=context.messages??context.transcript??[];const images=messages.filter(m=>m.role==='user').flatMap(m=>m.content??[]).filter(c=>c.type==='image');writeFileSync(process.env.IMAGE_MODEL_REPORT,JSON.stringify({turns:++turns,images}));const stream=createAssistantMessageEventStream();const message={role:'assistant',content:[{type:'text',text:'image-count='+images.length}],api:model.api,provider:model.provider,model:model.id,usage:{input:1,output:1,cacheRead:0,cacheWrite:0,totalTokens:2,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}},stopReason:'stop',timestamp:Date.now()};queueMicrotask(()=>{stream.push({type:'start',partial:message});stream.push({type:'done',reason:'stop',message});stream.end()});return stream}})};`
	if e = os.WriteFile(provider, []byte(script), 0600); e != nil {
		t.Fatal(e)
	}
	session := filepath.Join(root, "session.jsonl")
	report := filepath.Join(root, "model.json")
	network := filepath.Join(root, "network.json")
	cmd := exec.Command(r.NodeBin, "--import", preload, filepath.Join(r.PiPackageDir, "dist/bundle/cli.js"), "--mode", "rpc", "--session", session, "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--extension", provider, "--provider", "image-offline", "--model", "fixture")
	cmd.Dir = root
	cmd.Env = []string{"HOME=" + root, "PATH=/usr/bin:/bin", "PI_CODING_AGENT_DIR=" + filepath.Join(root, "agent"), "PI_OFFLINE=1", "PI_TELEMETRY=0", "IMAGE_PI_ROOT=" + r.PiPackageDir, "IMAGE_MODEL_REPORT=" + report, "IMAGE_NETWORK_REPORT=" + network}
	p, e := Start(cmd)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		_ = p.CloseStdin()
		select {
		case <-p.Done():
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-p.Done()
		}
		b, e := os.ReadFile(network)
		if e != nil {
			t.Error(e)
		} else if string(b) != `{"denied":0}` {
			t.Errorf("network report %s", b)
		}
	}()
	var pngBytes bytes.Buffer
	if e = png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2, 3))); e != nil {
		t.Fatal(e)
	}
	image := messageimage.Content{Type: "image", MimeType: "image/png", Data: base64.StdEncoding.EncodeToString(pngBytes.Bytes())}
	result, e := p.Prompt(ctx, "", nil, image)
	if e != nil || result.Outcome != OutcomeCompleted || result.Text != "image-count=1" {
		b, _ := os.ReadFile(report)
		t.Fatalf("result %+v err=%v stderr=%s model=%s", result, e, p.Stderr(), b)
	}
	result, e = p.Prompt(ctx, "what was attached?", nil)
	if e != nil || result.Text != "image-count=1" {
		t.Fatalf("history %+v %v", result, e)
	}
	raw, e := os.ReadFile(report)
	if e != nil {
		t.Fatal(e)
	}
	var got struct {
		Turns  int
		Images []messageimage.Content
	}
	if json.Unmarshal(raw, &got) != nil || got.Turns != 2 || len(got.Images) != 1 || got.Images[0] != image {
		t.Fatal("model image mismatch")
	}
	saved, e := os.ReadFile(session)
	if e != nil || strings.Count(string(saved), image.Data) != 1 {
		t.Fatal("session visual input not preserved", e)
	}
}
