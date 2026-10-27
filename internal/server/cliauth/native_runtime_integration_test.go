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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"loop-now/internal/server/clicredential"
)

func TestGoogleActualTwoProcessesPGTwoRefreshWaves(t *testing.T) { runGoogleNativeProcesses(t, false) }
func TestGoogleActualBrokerDisconnectFailsClosed(t *testing.T)   { runGoogleNativeProcesses(t, true) }
func runGoogleNativeProcesses(t *testing.T, disconnect bool) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("reviewed private runtime is darwin-arm64 only")
	}
	f := newGoogleFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	repo, _ := filepath.Abs("../../..")
	sdk := filepath.Join(repo, ".local-build/cli-installation-proof/unpacked/google-cloud-sdk")
	fw := filepath.Join(repo, ".local-build/cli-installation-proof/unpacked/private-python/Python.framework")
	entry := filepath.Join(repo, "auth-helper/google_runtime.py")
	root := t.TempDir()
	clock := filepath.Join(root, "clock")
	epoch := time.Now().Unix() + 60
	var now atomic.Int64
	now.Store(epoch)
	if e := os.WriteFile(clock, []byte(strconv.FormatInt(epoch, 10)), 0600); e != nil {
		t.Fatal(e)
	}
	old := syntheticGoogle()
	old.AccessToken = "synthetic-generation-0"
	old.ExpiresAt = (epoch + 500) * 1000
	row, e := f.st.CreateProjectCredential(ctx, f.project, "cli", "gcloud", "长命令", f.write(t, old))
	if e != nil {
		t.Fatal(e)
	}
	binding := clicredential.Binding{VersionID: *row.View.CurrentVersionID, AccountID: row.View.ID, ProjectID: f.project, AgentID: f.agent, CLI: "gcloud"}
	var brokers []*Broker
	var posts atomic.Int64
	var lock sync.Mutex
	counts := [3]int{}
	gates := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			if r.Method != "POST" {
				t.Error("non-POST token request")
				http.Error(w, "fixture", 400)
				return
			}
			generation := posts.Add(1)
			if e := r.ParseForm(); e != nil {
				t.Error("bad token body")
			}
			expected := old.RefreshToken
			if generation > 1 {
				expected = fmt.Sprintf("synthetic-rotation-%d", generation-1)
			}
			if r.Form.Get("refresh_token") != expected {
				t.Error("refresh replayed wrong generation")
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"access_token": fmt.Sprintf("synthetic-generation-%d", generation), "refresh_token": fmt.Sprintf("synthetic-rotation-%d", generation), "expires_in": epoch + generation*600 + 500 - time.Now().Unix(), "token_type": "Bearer", "scope": strings.Join(old.Scopes, " ")})
			return
		}
		if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/v1/projects") {
			t.Error("unexpected native fixture request")
			http.Error(w, "fixture", 400)
			return
		}
		page := 0
		switch r.URL.Query().Get("pageToken") {
		case "second":
			page = 1
		case "third":
			page = 2
		}
		if r.Header.Get("Authorization") != fmt.Sprintf("Bearer synthetic-generation-%d", page) {
			t.Error("native page used wrong captured generation")
		}
		lock.Lock()
		counts[page]++
		if page < 2 && counts[page] == 2 {
			next := epoch + int64(page+1)*600
			now.Store(next)
			if e := os.WriteFile(clock, []byte(strconv.FormatInt(next, 10)), 0600); e != nil {
				t.Error(e)
			}
			if disconnect && page == 0 {
				for _, broker := range brokers {
					closeCtx, stop := context.WithTimeout(context.Background(), time.Second)
					if err := broker.Close(closeCtx); err != nil {
						t.Error("broker close failed")
					}
					stop()
				}
			}
			close(gates[page])
		}
		lock.Unlock()
		if page < 2 {
			select {
			case <-gates[page]:
			case <-ctx.Done():
				return
			}
		}
		reply := map[string]any{"projects": []any{map[string]any{"projectId": fmt.Sprintf("fixture-%d", page), "name": "fixture", "lifecycleState": "ACTIVE"}}}
		if page < 2 {
			reply["nextPageToken"] = []string{"second", "third"}[page]
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(reply)
	}))
	defer fixture.Close()
	_, port, _ := net.SplitHostPort(fixture.Listener.Addr().String())
	helperRoots := HelperRuntime{SDKRoot: sdk, PythonRoot: fw, Script: filepath.Join(repo, "auth-helper/google_auth.py")}
	helper := &ProcessHelper{Options: HelperOptions{WorkRoot: root, Runtime: func(context.Context) (HelperRuntime, error) { return helperRoots, nil }, Command: func(ctx context.Context, dir string, r HelperRuntime) *exec.Cmd {
		code := "import sys,os;sys.path.insert(0," + strconv.Quote(filepath.Dir(r.Script)) + ");import google_auth as g;g.TOKEN_URI=" + strconv.Quote(fixture.URL+"/token") + ";original=g.isolated_environment;g.isolated_environment=lambda:(original(),os.environ.update(OAUTHLIB_INSECURE_TRANSPORT='1'));raise SystemExit(g.main())"
		r.Script = "-c"
		cmd := privatePythonCommand(r, code)
		profile := filepath.Join(dir, "offline.sb")
		os.WriteFile(profile, []byte(fmt.Sprintf("(version 1)(allow default)(deny network*)(allow network-outbound (remote tcp \"localhost:%s\"))", port)), 0600)
		return exec.Command("/usr/bin/sandbox-exec", append([]string{"-f", profile}, cmd.Args...)...)
	}}}
	o := CoordinatorOptions{RefreshTimeout: 5 * time.Second, RecordTimeout: time.Second, WaitTimeout: 10 * time.Second, PollInterval: time.Millisecond, Now: func() time.Time { return time.Unix(now.Load(), 0) }}
	coordinators := []*Coordinator{NewCoordinator(f.st, helper, o, nil), NewCoordinator(f.st, helper, o, nil)}
	brokers = []*Broker{}
	for _, c := range coordinators {
		b, e := NewBroker(binding, c)
		if e != nil {
			t.Fatal(e)
		}
		brokers = append(brokers, b)
	}
	t.Cleanup(func() {
		for _, b := range brokers {
			b.Close(context.Background())
		}
		for _, c := range coordinators {
			c.Close(context.Background())
		}
	})
	code := "import sys,datetime;sys.path.insert(0," + strconv.Quote(filepath.Dir(entry)) + ");import google_runtime as r;original=r.load_sdk\ndef load(path):\n sdk=original(path)\n from google.auth import _helpers\n from pathlib import Path\n _helpers.utcnow=lambda:datetime.datetime.fromtimestamp(int(Path(" + strconv.Quote(clock) + ").read_text()),datetime.timezone.utc).replace(tzinfo=None)\n return sdk\nr.load_sdk=load\nraise SystemExit(r.main(['--sdk-root'," + strconv.Quote(sdk) + ",'--','projects','list','--limit=3','--page-size=1','--format=json','--quiet']))"
	results := make(chan error, 2)
	for _, b := range brokers {
		go func(b *Broker) {
			home, e := os.MkdirTemp(root, "runtime-")
			if e != nil {
				results <- e
				return
			}
			profile := filepath.Join(home, "offline.sb")
			body := fmt.Sprintf("(version 1)(allow default)(deny network*)(allow network-outbound (remote tcp \"localhost:%s\") (remote unix-socket (path %s)))(deny file-read* (subpath \"/Users/ljl/.config\") (subpath \"/Users/ljl/Library/Keychains\"))", port, strconv.Quote(b.path))
			os.WriteFile(profile, []byte(body), 0600)
			r := HelperRuntime{PythonRoot: fw, Script: "-c"}
			native := privatePythonCommand(r, code)
			cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", append([]string{"-f", profile}, native.Args...)...)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "CLOUDSDK_CONFIG=" + home, "PATH=/usr/bin:/bin", "PYTHONNOUSERSITE=1", "CLOUDSDK_CORE_DISABLE_USAGE_REPORTING=true", "CLOUDSDK_COMPONENT_MANAGER_DISABLE_UPDATE_CHECK=true", "CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE=" + binding.VersionID.String(), "CLOUDSDK_API_ENDPOINT_OVERRIDES_CLOUDRESOURCEMANAGER=" + fixture.URL + "/"}
			for k, v := range b.Environment() {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
			out, e := cmd.Output()
			defer clear(out)
			if e == nil {
				var rows []any
				if json.Unmarshal(out, &rows) != nil || len(rows) != 3 {
					e = fmt.Errorf("native result page count mismatch")
				}
			}
			results <- e
		}(b)
	}
	for range brokers {
		e = <-results
		if disconnect {
			if e == nil {
				t.Fatal("disconnected native command succeeded")
			}
			if exit, ok := e.(*exec.ExitError); !ok || exit.ExitCode() == 0 {
				t.Fatal("disconnected native command did not fail bounded")
			}
		} else if e != nil {
			t.Fatal("actual production bootstrap failed", e)
		}
	}
	lock.Lock()
	defer lock.Unlock()
	if disconnect {
		if counts != [3]int{2, 0, 0} || posts.Load() != 0 {
			t.Fatal("disconnected native command retried/used another auth source")
		}
	} else if counts != [3]int{2, 2, 2} || posts.Load() != 2 {
		t.Fatal("native wave/page counts mismatch")
	}
	committed, e := f.st.GetGoogleCLIGrant(ctx, binding)
	expectedGeneration := int64(3)
	if disconnect {
		expectedGeneration = 1
	}
	if e != nil || committed.Generation != expectedGeneration || committed.RefreshState != "ready" {
		t.Fatal("two rotations not committed on same captured grant")
	}
}
