package modelauth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/modelcredential"
)

// Executes only a synthetic fd helper (fs/readline); no Pi/provider imports.
func fakeGoHelper(t *testing.T, body string) *Helper {
	t.Helper()
	const node = "/Users/ljl/.local/node-v24.21.0/bin/node"
	if _, err := os.Stat(node); err != nil {
		t.Skip("requires pinned Node")
	}
	root := t.TempDir()
	entry := filepath.Join(root, "fixture.mjs")
	code := `import fs from 'node:fs'; import readline from 'node:readline';
 const send=value=>fs.writeSync(4,JSON.stringify(value)+'\n');
 const credential={type:'oauth',access:'fixture-access',refresh:'fixture-rotated',expires:Date.now()+3600000,accountId:'account-A'};
 send({type:'ready',protocolVersion:1,expectedPiVersion:process.env.LOOP_NOW_AUTH_PI_VERSION});
 let operation;
 readline.createInterface({input:fs.createReadStream('',{fd:3})}).on('line',line=>{
 const frame=JSON.parse(line);
 if(frame.type==='abort'){` + body + `;return;}
 operation=frame;
 if(Object.keys(process.env).some(key=>key.includes('TOKEN')||key.includes('API_KEY')||key==='LOOP_NOW_MASTER_KEY')) {send({type:'result',id:frame.id,status:'failed'});return;}
 if(frame.type==='login')send({type:'notify',id:frame.id,event:{type:'auth_url',url:'https://auth.openai.com/oauth/authorize?state=fixture&code_challenge=fixture'}});
 ` + body + `
 }); setInterval(()=>{},1000);`
	if err := os.WriteFile(entry, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	return &Helper{Options: HelperOptions{NodeBin: node, Entry: entry, WorkRoot: root, BasePath: "/usr/bin:/bin", PiPackageRoot: root, Timeout: time.Second, StopGrace: time.Second}}
}
func TestGoHelperPrivateLoginAndHostSecretIsolation(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "host-secret-sentinel")
	t.Setenv("LOOP_NOW_MASTER_KEY", "host-master-sentinel")
	helper := fakeGoHelper(t, `if(frame.type!=='abort')send({type:'result',id:frame.id,status:'succeeded',credential});`)
	notified := false
	value, err := helper.Login(context.Background(), uuid.New(), func(url string) error { notified = ValidAuthorizationURL(url); return nil })
	if err != nil || !notified || value.Kind() != "oauth" {
		t.Fatal("isolated helper login failed", err)
	}
}
func TestGoHelperCancellationDrainsSuccessfulRefresh(t *testing.T) {
	helper := fakeGoHelper(t, `if(frame.type==='abort')send({type:'result',id:operation.id,status:'succeeded',credential,cancelRequested:true});else fs.writeFileSync(new URL('./started',import.meta.url),'started');`)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	old := oauth(t, "account-A", "old", time.Now())
	type result struct {
		value modelcredential.Credential
		err   error
	}
	done := make(chan result, 1)
	go func() { value, err := helper.Refresh(ctx, old); done <- result{value, err} }()
	waitAuth(t, func() bool {
		_, err := os.Stat(filepath.Join(filepath.Dir(helper.Options.Entry), "started"))
		return err == nil
	})
	cancel()
	got := <-done
	if got.err != nil || !old.SameAccount(got.value) || got.value.Kind() != "oauth" {
		t.Fatal("caller cancellation discarded rotating token", got.err)
	}
	if ctx.Err() == nil {
		t.Fatal("refresh test never cancelled caller")
	}
}
func TestGoHelperBoundsMissingCompletion(t *testing.T) {
	helper := fakeGoHelper(t, ``)
	helper.Options.StopGrace = 25 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := helper.Refresh(ctx, oauth(t, "account-A", "old", time.Now()))
	if !errors.Is(err, ErrRefreshUncertain) || time.Since(started) > 2*time.Second {
		t.Fatal("lost helper response hung refresh", err)
	}
}
func TestAuthorizationURLRejectsSecretsAndForeignCallback(t *testing.T) {
	base := "https://auth.openai.com/oauth/authorize?state=fixture&code_challenge=fixture"
	if !ValidAuthorizationURL(base) {
		t.Fatal("authorization URL unavailable")
	}
	for _, url := range []string{base + "&code=secret", base + "&access_token=secret", base + "&refresh_token=secret", base + "&redirect_uri=https%3A%2F%2Fevil.example", base + "&state=duplicate", "http://auth.openai.com/oauth/authorize?state=x&code_challenge=x", "https://auth.openai.com/other?state=x&code_challenge=x"} {
		if ValidAuthorizationURL(url) {
			t.Fatal("unsafe authorization URL accepted")
		}
	}
}

func TestGoHelperFailureStagesAreFixedAndSecretSafe(t *testing.T) {
	for _, tc := range []struct{ name, body, stage string }{
		{"fractional-expiry", `credential.expires=Date.now()+0.5;send({type:'result',id:frame.id,status:'succeeded',credential});`, "result_credential_decode"},
		{"bad-json", `fs.writeSync(4,'secret-sentinel-not-json\n');process.exit(0);`, "ipc_frame_json"},
		{"lost-result", `process.exit(0);`, "ipc_eof"},
		{"oversize", `fs.writeSync(4,'x'.repeat(1024*1024+2)+'\n');process.exit(0);`, "ipc_frame_limit"},
		{"unknown-provider-error", `send({type:'result',id:frame.id,status:'failed',code:'secret-sentinel-provider-body'});`, "helper_result_failed"},
		{"helper-invalid-credential", `send({type:'result',id:frame.id,status:'failed',code:'invalid_credential'});`, "helper_invalid_credential"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := fakeGoHelper(t, tc.body)
			_, err := h.Login(context.Background(), uuid.New(), nil)
			if !errors.Is(err, ErrHelperUnavailable) || helperDiagnostic(err) != tc.stage {
				t.Fatal("wrong fixed diagnostic", err)
			}
			if strings.Contains(err.Error(), "secret-sentinel") {
				t.Fatal("provider error escaped")
			}
		})
	}
}
func TestGoHelperDiagnosticWhitelistAndResultDrain(t *testing.T) {
	for _, embedded := range []bool{false, true} {
		t.Run(fmt.Sprint(embedded), func(t *testing.T) {
			body := `const diagnostic={stage:'exchange_http',httpStatus:401,category:'http_error'};
 send({type:'diagnostic',id:frame.id,...diagnostic});
 send({type:'diagnostic',id:frame.id,stage:'secret-sentinel-stage',category:'secret-sentinel-category',httpStatus:999});
 send({type:'result',id:frame.id,status:'failed',code:'auth_failed'` + map[bool]string{false: "", true: ",diagnostic"}[embedded] + `});process.exit(0);`
			h := fakeGoHelper(t, body)
			_, err := h.Login(context.Background(), uuid.New(), nil)
			var failure *helperFailure
			if !errors.As(err, &failure) || failure.provider.Stage != "exchange_http" || failure.provider.HTTPStatus != 401 || failure.provider.Category != "http_error" {
				t.Fatal("safe diagnostic lost", err)
			}
			if strings.Contains(err.Error(), "secret-sentinel") {
				t.Fatal("unsafe diagnostic escaped")
			}
		})
	}
	h := fakeGoHelper(t, `send({type:'result',id:frame.id,status:'succeeded',credential});process.exit(0);`)
	c, err := h.Login(context.Background(), uuid.New(), nil)
	if err != nil || c.Kind() != "oauth" {
		t.Fatal("EOF discarded already flushed success", err)
	}
}
