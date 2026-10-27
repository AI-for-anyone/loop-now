package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/google/uuid"
	"log/slog"
	"loop-now/internal/server/cliauth"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/cliverify"
	"loop-now/internal/server/store"
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
)

// This uses the actual pinned SDK and private Python, a real version-bound Go
// broker and TEST PG. The only endpoint substitution is in the trusted Go
// command fixture; production has no HTTP/env endpoint override.
func TestGoogleNativeVerificationCRMGuard(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("reviewed native platform darwin-arm64")
	}
	repo, e := filepath.Abs("../../../..")
	if e != nil {
		t.Fatal(e)
	}
	sdk := filepath.Join(repo, ".local-build/cli-installation-proof/unpacked/google-cloud-sdk")
	fw := filepath.Join(repo, ".local-build/cli-installation-proof/unpacked/private-python/Python.framework")
	python := filepath.Join(fw, "Versions/3.13/bin/python3.13")
	if _, e = os.Stat(python); e != nil {
		t.Fatal("reviewed native fixture missing")
	}
	for _, test := range []struct {
		name, method string
		status       int
	}{
		{"oauth_empty", "oauth", 200}, {"oauth_api_disabled", "oauth", 403}, {"oauth_rate_limit", "oauth", 429}, {"oauth_redirect", "oauth", 302}, {"oauth_bad_json", "oauth", 200}, {"oauth_cancel", "oauth", 200}, {"service_account", "service_account", 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			status := test.status
			entered := make(chan struct{}, 1)
			var tokens atomic.Int32
			var reads, writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if test.method == "service_account" && r.Method == "POST" && r.URL.Path == "/token" {
					if e := r.ParseForm(); e != nil || r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || r.Form.Get("assertion") == "" {
						t.Error("SA native token request invalid")
					}
					tokens.Add(1)
					w.Header().Set("Content-Type", "application/json")
					ioWrite(w, `{"access_token":"synthetic-native-access","expires_in":3600,"token_type":"Bearer"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != "/v1/projects" {
					writes.Add(1)
					w.WriteHeader(500)
					return
				}
				reads.Add(1)
				if test.name == "oauth_cancel" {
					entered <- struct{}{}
					<-r.Context().Done()
					return
				}
				if test.name == "oauth_redirect" {
					w.Header().Set("Location", serverRedirectTarget())
					w.WriteHeader(302)
					return
				}
				if test.name == "oauth_bad_json" {
					w.Header().Set("Content-Type", "application/json")
					ioWrite(w, `{"projects":"invalid-typed-list"}`)
					return
				}
				if r.Header.Get("Authorization") != "Bearer synthetic-native-access" {
					t.Error("original broker credential not used")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				if status == 200 {
					ioWrite(w, `{"projects":[]}`)
				} else if status == 403 {
					ioWrite(w, `{"error":{"code":403,"message":"Cloud Resource Manager API has not been used or is disabled","status":"PERMISSION_DENIED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"SERVICE_DISABLED","domain":"googleapis.com","metadata":{"service":"cloudresourcemanager.googleapis.com","consumer":"projects/123"}}]}}`)
				} else {
					ioWrite(w, `{"error":{"code":429,"message":"synthetic rate limit","status":"RESOURCE_EXHAUSTED"}}`)
				}
			}))
			defer server.Close()
			_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
			factory := func(_ context.Context, spec CommandSpec) (*exec.Cmd, error) {
				profile := filepath.Join(spec.Home, "probe.sb")
				socket := ""
				for _, item := range spec.Environment {
					if strings.HasPrefix(item, "LOOP_NOW_GCLOUD_BRIDGE_SOCKET=") {
						socket = strings.TrimPrefix(item, "LOOP_NOW_GCLOUD_BRIDGE_SOCKET=")
					}
				}
				body := fmt.Sprintf(`(version 1)(allow default)(deny network*)(allow network-outbound (remote tcp "localhost:%s") (remote unix-socket (path %s)))(deny file-read* (subpath "/Users/ljl/.config") (subpath "/Users/ljl/Library/Keychains"))`, port, strconv.Quote(socket))
				if e = os.WriteFile(profile, []byte(body), 0600); e != nil {
					return nil, e
				}
				code := "import sys,os; sys.path.insert(0," + strconv.Quote(filepath.Join(repo, "auth-helper")) + "); import google_verify as v; v.CRM_ORIGIN=" + strconv.Quote(server.URL) + "; os.environ['CLOUDSDK_API_ENDPOINT_OVERRIDES_CLOUDRESOURCEMANAGER']=" + strconv.Quote(server.URL+"/") + "; sys.argv=['google_verify','--sdk-root'," + strconv.Quote(sdk) + "]; raise SystemExit(v.main())"

				if test.method == "service_account" {
					code = strings.Replace(code, "sys.argv=", "import json; from pathlib import Path; cp=Path(os.environ['CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE']); cv=json.loads(cp.read_text()); cv['token_uri']="+strconv.Quote(server.URL+"/token")+"; cp.write_text(json.dumps(cv)); os.environ['CLOUDSDK_AUTH_TOKEN_HOST']="+strconv.Quote(server.URL+"/token")+"; v.TOKEN_URL="+strconv.Quote(server.URL+"/token")+"; sys.argv=", 1)
				}

				quote := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\\''") + "'" }
				shell := "export DYLD_FRAMEWORK_PATH=" + quote(filepath.Dir(fw)) + " DYLD_LIBRARY_PATH=" + quote(filepath.Join(fw, "Versions/3.13/lib")) + " PYTHONHOME=" + quote(filepath.Join(fw, "Versions/3.13")) + "; exec " + quote(python) + " -S -c " + quote(code)
				cmd := exec.Command("/usr/bin/sandbox-exec", "-f", profile, "/bin/sh", "-c", shell)
				cmd.Env = []string{}
				for _, v := range spec.Environment {
					if !strings.HasPrefix(v, "PYTHONHOME=") && !strings.HasPrefix(v, "DYLD_") {
						cmd.Env = append(cmd.Env, v)
					}
				}
				cmd.Env = append(cmd.Env, "PYTHONHOME="+filepath.Join(fw, "Versions/3.13"), "DYLD_FRAMEWORK_PATH="+filepath.Dir(fw), "DYLD_LIBRARY_PATH="+filepath.Join(fw, "Versions/3.13/lib"))
				return cmd, nil
			}
			f := newFixture(t, factory, 10*time.Second)
			var diagnostics bytes.Buffer
			f.s.log = slog.New(slog.NewTextHandler(&diagnostics, nil))
			t.Cleanup(func() { t.Log(diagnostics.String()) })
			c := clicredential.Google{Type: "authorized_user", Account: "synthetic@example.invalid", ClientID: "synthetic-client", ClientSecret: "synthetic-secret", RefreshToken: "synthetic-never-consumed", AccessToken: "synthetic-native-access", ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"}}
			plain, e := c.Encode()
			method := test.method
			if method == "service_account" {
				key, err := rsa.GenerateKey(rand.Reader, 2048)
				if err != nil {
					t.Fatal(err)
				}
				der, err := x509.MarshalPKCS8PrivateKey(key)
				if err != nil {
					t.Fatal(err)
				}
				sa, err := json.Marshal(map[string]string{"type": "service_account", "project_id": "synthetic-project", "private_key_id": "fixture-id", "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "client_email": "fixture@synthetic-project.iam.gserviceaccount.com", "client_id": "fixture-client", "token_uri": "https://oauth2.googleapis.com/token"})
				clear(der)
				if err != nil {
					t.Fatal(err)
				}
				template, _ := f.s.Templates.Get("gcloud")
				payload, _, err := template.Render(map[string]string{"serviceAccountJson": string(sa), "defaultProject": "must-not-be-used"})
				clear(sa)
				if err != nil {
					t.Fatal(err)
				}
				plain, e = json.Marshal(payload)
				for _, file := range payload.Files {
					clear(file.Content)
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			record, e := f.st.CreateProjectCredential(context.Background(), f.project, "cli", "gcloud", "native", store.ProjectCredentialWrite{AuthMethod: method, AccountLabel: &c.Account, TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1"}, Secret: plain})
			clear(plain)
			if e != nil {
				t.Fatal(e)
			}
			d := f.s.registry.trusted["gcloud"]
			op, _, e := f.st.BeginCLIVerification(context.Background(), f.project, record.View.ID, cliverify.Start{OperationID: uuid.New(), ExpectedVersionID: *record.View.CurrentVersionID}, store.VerificationMetadata{TemplateSource: "builtin", CredentialFormat: "1", TemplateFingerprint: d.Fingerprint, AdapterRevision: d.Revision, ProbeKind: d.Kind}, "console", nil, time.Now().Add(time.Minute))
			if e != nil {
				t.Fatal(e)
			}
			op, _, e = f.st.ClaimCLIVerification(context.Background(), f.project, record.View.ID, op.ID)
			if e != nil {
				t.Fatal(e)
			}
			co := cliauth.NewCoordinator(f.st, nil, cliauth.CoordinatorOptions{}, nil)
			defer co.Close(context.Background())
			f.s.Google = co
			f.s.Options.GoogleEntry = filepath.Join(repo, "auth-helper/google_verify.py")
			row, _, e := f.st.GetProjectCLIVersion(context.Background(), op.AccountID, op.VersionID)
			if e != nil {
				t.Fatal(e)
			}
			home, lease, e := f.s.operationHome(op.ID)
			if e != nil {
				t.Fatal(e)
			}
			defer f.s.releaseHome(home, lease)
			env := baseEnvironment(home, d.Template)
			probeCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			if test.name == "oauth_cancel" {
				go func() {
					select {
					case <-entered:
						stop()
					case <-probeCtx.Done():
					}
				}()
			}
			result := f.s.googleProbe(probeCtx, op, d, home, env, row, lease)
			if status == 200 && test.name != "oauth_bad_json" && test.name != "oauth_cancel" {
				if result.Outcome != "passed" {
					t.Fatalf("native CRM success not accepted: code=%v reads=%d writes=%d", safeCode(result), reads.Load(), writes.Load())
				}
			} else {
				expected := "probe_failed"
				if status == 403 {
					expected = "provider_policy"
				}
				if test.name == "oauth_cancel" {
					expected = "timeout"
				}
				if status == 429 {
					expected = "rate_limited"
				}
				if result.ErrorCode == nil || *result.ErrorCode != expected {
					t.Fatalf("fixed HTTP category mismatch: code=%v reads=%d writes=%d", safeCode(result), reads.Load(), writes.Load())
				}
			}
			if reads.Load() != 1 || writes.Load() != 0 {
				t.Fatal("probe did not stay one fixed CRM read / enable0")
			}
			if test.method == "service_account" {
				if tokens.Load() != 2 {
					t.Fatalf("SA fixed token flow count=%d", tokens.Load())
				}
				if _, err := os.Stat(filepath.Join(home, ".loop-now/gcloud/default-project")); !os.IsNotExist(err) {
					t.Fatal("business default project materialized")
				}
				return
			}
			var generation int64
			if e = f.st.Pool.QueryRow(context.Background(), `SELECT generation FROM google_cli_grants WHERE version_id=$1`, op.VersionID).Scan(&generation); e != nil || generation != 1 {
				t.Fatal("ready grant unexpectedly rotated", e)
			}
		})
	}
}
func ioWrite(w http.ResponseWriter, s string) { _, _ = w.Write([]byte(s)) }
func safeCode(r cliverify.Result) string {
	if r.ErrorCode == nil {
		return "none"
	}
	return *r.ErrorCode
}

func serverRedirectTarget() string { return "https://forbidden.invalid/never-follow" }
