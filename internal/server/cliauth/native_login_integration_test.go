package cliauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/clicredential"
	cm "loop-now/internal/server/credentialmodel"
)

type offlineBrowser struct {
	helper *ProcessHelper
	onURL  func(string) error
}

func (h offlineBrowser) Login(ctx context.Context, id uuid.UUID, save func(string) error) (clicredential.Google, error) {
	return h.helper.Login(ctx, id, func(u string) error {
		if e := save(u); e != nil {
			return e
		}
		return h.onURL(u)
	})
}
func TestGoogleActualLoginHelperPG(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("reviewed private runtime is darwin-arm64 only")
	}
	for _, mode := range []string{"success", "missing_refresh", "wrong_state"} {
		t.Run(mode, func(t *testing.T) {
			f := newGoogleFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var mu sync.Mutex
			challenge, scope := "", ""
			posts := 0
			token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/token" {
					t.Error("unexpected fixture endpoint")
					http.Error(w, "fixture", 400)
					return
				}
				r.ParseForm()
				sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
				mu.Lock()
				posts++
				valid := base64.RawURLEncoding.EncodeToString(sum[:]) == challenge
				scopes := scope
				mu.Unlock()
				if !valid {
					t.Error("native PKCE mismatch")
					http.Error(w, "fixture", 400)
					return
				}
				idToken := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"email":"fixture@example.invalid"}`)) + "."
				reply := map[string]any{"access_token": "synthetic-login-access", "refresh_token": "synthetic-login-refresh", "expires_in": 3600.0001, "token_type": "Bearer", "scope": scopes, "id_token": idToken}
				if mode == "missing_refresh" {
					delete(reply, "refresh_token")
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(reply)
			}))
			defer token.Close()
			repo, _ := filepath.Abs("../../..")
			roots := HelperRuntime{SDKRoot: filepath.Join(repo, ".local-build/cli-installation-proof/unpacked/google-cloud-sdk"), PythonRoot: filepath.Join(repo, ".local-build/cli-installation-proof/unpacked/private-python/Python.framework"), Script: filepath.Join(repo, "auth-helper/google_auth.py")}
			_, port, _ := net.SplitHostPort(token.Listener.Addr().String())
			helper := &ProcessHelper{Options: HelperOptions{WorkRoot: t.TempDir(), StopGrace: 100 * time.Millisecond, Runtime: func(context.Context) (HelperRuntime, error) { return roots, nil }, Command: func(ctx context.Context, dir string, r HelperRuntime) *exec.Cmd {
				code := "import os,sys;sys.path.insert(0," + strconv.Quote(filepath.Dir(r.Script)) + ");import google_auth as g;g.TOKEN_URI=" + strconv.Quote(token.URL+"/token") + ";original=g.isolated_environment;g.isolated_environment=lambda:(original(),os.environ.update(OAUTHLIB_INSECURE_TRANSPORT='1'));raise SystemExit(g.main())"
				r.Script = "-c"
				cmd := privatePythonCommand(r, code)
				profile := filepath.Join(dir, "offline.sb")
				body := fmt.Sprintf("(version 1)(allow default)(deny network*)(allow network-outbound (remote tcp \"localhost:%s\"))(allow network-bind (local tcp \"localhost:8085\"))(allow network-inbound (local tcp \"localhost:8085\"))", port)
				os.WriteFile(profile, []byte(body), 0600)
				return exec.Command("/usr/bin/sandbox-exec", append([]string{"-f", profile}, cmd.Args...)...)
			}}}
			browser := offlineBrowser{helper, func(u string) error {
				parsed, e := url.Parse(u)
				if e != nil {
					return e
				}
				q := parsed.Query()
				mu.Lock()
				challenge, scope = q.Get("code_challenge"), q.Get("scope")
				mu.Unlock()
				state := q.Get("state")
				if mode == "wrong_state" {
					state = "synthetic-wrong-state"
				}
				// Synthetic browser callback only to the actually bound local helper; the
				// official issuer URL is never fetched or emitted to ordinary output.
				callback := "http://127.0.0.1:8085/?" + url.Values{"code": {"synthetic-code"}, "state": {state}}.Encode()
				go func() {
					req, _ := http.NewRequestWithContext(ctx, "GET", callback, nil)
					resp, e := http.DefaultClient.Do(req)
					if e == nil {
						resp.Body.Close()
					}
				}()
				return nil
			}}
			service := NewService(f.st, browser, nil, ServiceOptions{LoginTimeout: 5 * time.Second, RecordTimeout: time.Second}, nil)
			defer service.Close(context.Background())
			service.Account = func(ctx context.Context, project, id uuid.UUID) (cm.Account, error) {
				r, e := f.st.GetProjectCredential(ctx, project, id)
				return r.View, e
			}
			name := "原生离线登录"
			in := cm.CLIAuthStart{OperationID: uuid.New(), CLI: "gcloud", AuthType: "google_user", Name: &name}
			if _, e := service.Start(ctx, f.project, in); e != nil {
				t.Fatal(e)
			}
			var state cm.CLIAuthState
			for {
				var e error
				state, e = service.Get(ctx, f.project, in.OperationID)
				if e != nil {
					t.Fatal(e)
				}
				if state.Operation.Status != "pending" && state.Operation.Status != "awaiting_authorization" {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("actual login did not finish")
				case <-time.After(5 * time.Millisecond):
				}
			}
			var count int
			f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM google_cli_grants`).Scan(&count)
			if state.Operation.AuthorizationURL != nil {
				t.Fatal("terminal URL retained")
			}
			if mode == "success" {
				if state.Operation.Status != "succeeded" || state.Account == nil || state.Account.AccountLabel == nil || *state.Account.AccountLabel != "fixture@example.invalid" || count != 1 {
					t.Fatal("native success did not commit account")
				}
			} else if state.Operation.Status != "failed" || count != 0 {
				t.Fatal("invalid native result created a grant")
			}
			mu.Lock()
			actualPosts := posts
			mu.Unlock()
			expected := 1
			if mode == "wrong_state" {
				expected = 0
			}
			if actualPosts != expected {
				t.Fatal("native token POST count mismatch")
			}
			if conn, e := net.DialTimeout("tcp", "127.0.0.1:8085", 100*time.Millisecond); e == nil {
				conn.Close()
				t.Fatal("helper callback port not released")
			}
		})
	}
}
