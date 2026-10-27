package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"loop-now/internal/server/apitypes"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/larkauth/larktest"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func larkAppSource(t *testing.T, s *server) (uuid.UUID, string, string, cm.Account) {
	t.Helper()
	project := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil)).Project.ID
	accounts := "/projects/" + project.String() + "/credential-accounts"
	base := "/projects/" + project.String() + "/cli-auth/operations"
	source := decode[cm.Account](t, s.must(201, "POST", accounts, map[string]any{"purpose": "cli", "cli": "lark-cli", "name": "离线应用", "credential": map[string]any{"type": "cli", "fields": map[string]string{"appId": "app-fixture", "appSecret": "fixture-app-secret", "brand": "feishu"}}}))
	return project, base, accounts, source
}
func TestLarkAppUnknownStartRealBeforeAndAfterCommit(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_login_commit", true: "after_login_commit"}[after], func(t *testing.T) {
			w := githubAppWorld(t)
			p := larktest.New(t)
			s := w.start(nil)
			defer s.stop()
			s.a.CLIAuth.Lark = p.Client
			_, base, accounts, source := larkAppSource(t, s)
			a := p.Add("ou-unknown", "未知响应用户", nil, "")
			if after {
				a.Release()
			}
			id := uuid.New()
			input, _ := json.Marshal(map[string]any{"operationId": id, "cli": "lark-cli", "authType": "lark_user", "sourceAccountId": source.ID, "expectedSourceRevision": source.Revision})
			tr := http.DefaultTransport.(*http.Transport).Clone()
			tr.Proxy = nil
			defer tr.CloseIdleConnections()
			client := &http.Client{Transport: tr, Timeout: 8 * time.Second}
			var posts atomic.Int32
			handlerDone := make(chan bool, 1)
			proxy := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" {
					rw.WriteHeader(405)
					return
				}
				posts.Add(1)
				req, e := http.NewRequestWithContext(r.Context(), "POST", s.url+base, r.Body)
				if e != nil {
					handlerDone <- false
					return
				}
				req.Header.Set("Content-Type", "application/json")
				resp, e := client.Do(req)
				if e != nil {
					handlerDone <- false
					return
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				ok := resp.StatusCode == 202
				if after {
					ok = false
					for until := time.Now().Add(5 * time.Second); time.Now().Before(until); time.Sleep(5 * time.Millisecond) {
						get, e := client.Get(s.url + base + "/" + id.String())
						if e != nil {
							break
						}
						var state cm.CLIAuthState
						e = json.NewDecoder(get.Body).Decode(&state)
						get.Body.Close()
						if e == nil && state.Operation.Status == "succeeded" {
							ok = true
							break
						}
					}
				}
				conn, _, e := rw.(http.Hijacker).Hijack()
				if e == nil {
					conn.Close()
				}
				handlerDone <- ok
			}))
			defer proxy.Close()
			response, e := client.Post(proxy.URL, "application/json", bytes.NewReader(input))
			if response != nil {
				response.Body.Close()
			}
			if e == nil {
				t.Fatal("fixture did not lose actual response")
			}
			if !<-handlerDone {
				t.Fatal("underlying actual App start/commit not reached")
			}
			// The caller never repeats POST. It queries the original stable ID,
			// including after the operation has already reached terminal state.
			original := decode[cm.CLIAuthState](t, s.must(200, "GET", base+"/"+id.String(), nil))
			if original.Operation.ID != id {
				t.Fatal("original ID lost")
			}
			if !after {
				a.Release()
			}
			result := waitLarkApp(t, s, base, id, "succeeded")
			list := decode[cm.AccountList](t, s.must(200, "GET", accounts+"?purpose=cli&cli=lark-cli", nil))
			if result.Account == nil || len(list.Accounts) != 2 || posts.Load() != 1 || p.DeviceCalls.Load() != 1 || p.UserCalls.Load() != 1 || p.DeniedDials.Load() != 0 {
				t.Fatal("unknown result caused duplicate authorization or account")
			}
		})
	}
}
func TestLarkAppRestartEndsActualPendingFlow(t *testing.T) {
	w := githubAppWorld(t)
	p := larktest.New(t)
	s := w.start(nil)
	defer func() { s.stop() }()
	s.a.CLIAuth.Lark = p.Client
	project, base, _, source := larkAppSource(t, s)
	p.Add("ou-never-authorized", "不授权", nil, "")
	id := uuid.New()
	s.must(202, "POST", base, map[string]any{"operationId": id, "cli": "lark-cli", "authType": "lark_user", "sourceAccountId": source.ID, "expectedSourceRevision": source.Revision})
	waitLarkApp(t, s, base, id, "awaiting_authorization")
	deviceCalls := p.DeviceCalls.Load()
	s.stop()
	s = w.start(nil)
	recovered := decode[cm.CLIAuthState](t, s.must(200, "GET", base+"/"+id.String(), nil))
	if recovered.Operation.Status != "failed" || recovered.Operation.ErrorCode == nil || *recovered.Operation.ErrorCode != "server_restarted" || recovered.Operation.AuthorizationURL != nil || recovered.Operation.UserCode != nil || p.DeviceCalls.Load() != deviceCalls {
		t.Fatal("restart resumed real pending DeviceFlow or kept short-lived data")
	}
	var grants, ops int
	if e := s.a.Store.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM lark_cli_grants),(SELECT count(*) FROM project_cli_auth_operations WHERE project_id=$1)`, project).Scan(&grants, &ops); e != nil || grants != 0 || ops != 1 {
		t.Fatal("pending restart created grant or duplicate operation")
	}
}
