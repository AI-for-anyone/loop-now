package cliauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"loop-now/internal/server/clicredential"
)

func TestGoogleActualHelperPGCancelledRotation(t *testing.T)    { runGoogleNativeHelperPG(t, false) }
func TestGoogleActualHelperPGProviderResponseLost(t *testing.T) { runGoogleNativeHelperPG(t, true) }
func runGoogleNativeHelperPG(t *testing.T, responseLost bool) {
	runGoogleNativeHelperPrincipalPG(t, responseLost, false)
}
func TestGoogleManagementActualHelperCancelledRotation(t *testing.T) {
	runGoogleNativeHelperPrincipalPG(t, false, true)
}
func TestGoogleManagementActualHelperResponseLost(t *testing.T) {
	runGoogleNativeHelperPrincipalPG(t, true, true)
}
func runGoogleNativeHelperPrincipalPG(t *testing.T, responseLost, management bool) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("reviewed private Python/SDK is darwin-arm64 only")
	}
	f := newGoogleFixture(t)
	ctx := context.Background()
	old := syntheticGoogle()
	rapt := "original-rapt"
	old.RAPTToken = &rapt
	r, e := f.st.CreateProjectCredential(ctx, f.project, "cli", "gcloud", "SDK原生", f.write(t, old))
	if e != nil {
		t.Fatal(e)
	}
	binding := clicredential.Binding{VersionID: *r.View.CurrentVersionID, AgentID: f.agent, ProjectID: f.project, AccountID: r.View.ID, CLI: "gcloud"}
	var managementBinding clicredential.VerificationGrantBinding
	if management {
		managementBinding = managementOperation(t, f, r.View.ID, *r.View.CurrentVersionID)
	}
	var posts atomic.Int64
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/token" {
			t.Error("unexpected fixture request")
			http.Error(w, "fixture", 400)
			return
		}
		if e := r.ParseForm(); e != nil || r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != old.RefreshToken || r.Form.Get("rapt") != rapt {
			t.Error("native refresh request mismatch")
			http.Error(w, "fixture", 400)
			return
		}
		if posts.Add(1) == 1 {
			close(entered)
		}
		<-release
		w.Header().Set("Content-Type", "application/json")
		if responseLost {
			_, _ = w.Write([]byte(`{"access_token":`))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "synthetic-native-new-access", "refresh_token": "synthetic-native-rotated", "expires_in": 3600, "token_type": "Bearer", "scope": strings.Join(old.Scopes, " "), "rapt_token": "synthetic-native-new-rapt"})
	}))
	defer server.Close()
	repo, e := filepath.Abs("../../..")
	if e != nil {
		t.Fatal(e)
	}
	roots := HelperRuntime{SDKRoot: filepath.Join(repo, ".local-build/cli-installation-proof/unpacked/google-cloud-sdk"), PythonRoot: filepath.Join(repo, ".local-build/cli-installation-proof/unpacked/private-python/Python.framework"), Script: filepath.Join(repo, "auth-helper/google_auth.py")}
	for _, p := range []string{roots.SDKRoot, roots.PythonRoot, roots.Script} {
		if _, e = os.Stat(p); e != nil {
			t.Fatal("reviewed fixture absent")
		}
	}
	_, port, e := net.SplitHostPort(server.Listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	helper := &ProcessHelper{Options: HelperOptions{WorkRoot: t.TempDir(), StopGrace: 100 * time.Millisecond, Runtime: func(context.Context) (HelperRuntime, error) { return roots, nil }, Command: func(ctx context.Context, dir string, r HelperRuntime) *exec.Cmd {
		// Endpoint/HTTP allowance exists only in this explicit test launcher. The
		// production helper file and Go protocol have no fixture knobs.
		code := "import sys,os; sys.path.insert(0," + strconv.Quote(filepath.Dir(r.Script)) + "); import google_auth as g; g.TOKEN_URI=" + strconv.Quote(server.URL+"/token") + "; original=g.isolated_environment; g.isolated_environment=lambda:(original(),os.environ.update(OAUTHLIB_INSECURE_TRANSPORT='1')); raise SystemExit(g.main())"
		r.Script = "-c"
		cmd := privatePythonCommand(r, code)
		profile := filepath.Join(dir, "deny-network.sb")
		body := fmt.Sprintf("(version 1)(allow default)(deny network*)(allow network-outbound (remote tcp \"localhost:%s\"))(deny file-read* (subpath \"/Users/ljl/.config\") (subpath \"/Users/ljl/Library/Keychains\"))", port)
		if e := os.WriteFile(profile, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
		return exec.Command("/usr/bin/sandbox-exec", append([]string{"-f", profile}, cmd.Args...)...)
	}}}
	o := CoordinatorOptions{RefreshTimeout: 10 * time.Second, RecordTimeout: time.Second, WaitTimeout: 15 * time.Second, PollInterval: time.Millisecond}
	one, two := NewCoordinator(f.st, helper, o, nil), NewCoordinator(f.st, helper, o, nil)
	t.Cleanup(func() { one.Close(ctx); two.Close(ctx) })
	caller, cancel := context.WithCancel(ctx)
	first := make(chan error, 1)
	go func() {
		var e error
		if management {
			_, e = one.ResolveVerification(caller, managementBinding)
		} else {
			_, e = one.Resolve(caller, binding)
		}
		first <- e
	}()
	select {
	case <-entered:
	case e := <-first:
		t.Fatal("actual helper failed before fixture", e)
	case <-time.After(10 * time.Second):
		t.Fatal("native helper did not reach only allowed endpoint")
	}
	cancel()
	<-first
	if management {
		if _, e = f.st.CancelCLIVerification(ctx, f.project, r.View.ID, managementBinding.OperationID); e != nil {
			t.Fatal(e)
		}
	}
	second := make(chan error, 1)
	go func() {
		c, e := two.Resolve(ctx, binding)
		if e == nil && (c.RefreshToken != "synthetic-native-rotated" || c.RAPTToken == nil || *c.RAPTToken != "synthetic-native-new-rapt") {
			t.Error("native rotation not persisted")
		}
		second <- e
	}()
	close(release)
	e = <-second
	if responseLost {
		if e == nil {
			t.Fatal("unknown provider response reused old grant")
		}
	} else if e != nil {
		t.Fatal("native helper PG rotation failed", e)
	}
	row, e := f.st.GetGoogleCLIGrant(ctx, binding)
	expectedGeneration := int64(2)
	expectedState := "ready"
	if responseLost {
		expectedGeneration = 1
		expectedState = "unavailable"
	}
	if e != nil || row.Generation != expectedGeneration || row.RefreshState != expectedState || posts.Load() != 1 {
		t.Fatal("native rotation commit/one POST mismatch")
	}
	if responseLost {
		if _, e = two.Resolve(ctx, binding); e == nil || posts.Load() != 1 {
			t.Fatal("unknown provider response caused refresh replay")
		}
	}
}
