package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/config"
	"loop-now/internal/server/larkauth/larktest"
)

type nativeRefreshFixture struct {
	clock    *atomic.Int64
	calls    atomic.Int32
	mu       sync.Mutex
	consumed map[string]bool
}

func (f *nativeRefreshFixture) Refresh(_ context.Context, old clicredential.Lark) (clicredential.Lark, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.consumed[old.RefreshToken] {
		return clicredential.Lark{}, errors.New("synthetic refresh consumed twice")
	}
	f.consumed[old.RefreshToken] = true
	n := f.calls.Add(1)
	next := old.Clone()
	next.AccessToken = fmt.Sprintf("synthetic-native-rotation-%d", n)
	next.RefreshToken = fmt.Sprintf("synthetic-native-refresh-%d", n)
	next.ExpiresAt = time.UnixMilli(f.clock.Load()).Add(time.Hour).UnixMilli()
	return next, nil
}

func TestLarkActualAppTwoNativeConsumersTwoPGRefreshWaves(t *testing.T) {
	bundle := unpackLarkVariant(t)
	var clock atomic.Int64
	clock.Store(time.Now().UnixMilli())
	f := &nativeRefreshFixture{clock: &clock, consumed: map[string]bool{}}
	var mu sync.Mutex
	arrived := [2]int{}
	release := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	var pages, denied atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/open-apis/authen/v1/user_info" {
			io.WriteString(w, `{"code":0,"data":{"open_id":"ou-shared-native","name":"Synthetic"}}`)
			return
		}
		if r.URL.Path != "/open-apis/im/v1/messages" {
			t.Error("unexpected native request")
			w.WriteHeader(400)
			return
		}
		page := 0
		switch r.URL.Query().Get("page_token") {
		case "one":
			page = 1
		case "two":
			page = 2
		case "":
		default:
			t.Error("unexpected page")
			w.WriteHeader(400)
			return
		}
		auth := r.Header.Get("Authorization")
		if page == 0 {
			if !strings.HasPrefix(auth, "Bearer synthetic-access-") {
				t.Error("initial captured token mismatch")
			}
		} else if auth != fmt.Sprintf("Bearer synthetic-native-rotation-%d", page) {
			t.Error("page did not use committed refresh generation")
		}
		pages.Add(1)
		if page < 2 {
			mu.Lock()
			arrived[page]++
			if arrived[page] == 2 {
				clock.Add(int64(3 * time.Hour / time.Millisecond))
				close(release[page])
			}
			mu.Unlock()
			select {
			case <-release[page]:
			case <-r.Context().Done():
				return
			}
			next := "one"
			if page == 1 {
				next = "two"
			}
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"items": []any{map[string]int{"page": page}}, "has_more": true, "page_token": next}})
			return
		}
		io.WriteString(w, `{"code":0,"data":{"items":[{"page":2}],"has_more":false}}`)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	tr := upstream.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.TLSClientConfig = tr.TLSClientConfig.Clone()
	tr.TLSClientConfig.ServerName = "127.0.0.1"
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == "open.feishu.cn:443" {
			return (&net.Dialer{}).DialContext(ctx, "tcp", target.Host)
		}
		host, _, e := net.SplitHostPort(address)
		ip := net.ParseIP(host)
		if e != nil || ip == nil || !ip.IsLoopback() {
			denied.Add(1)
			return nil, errors.New("non-loopback denied")
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	old := http.DefaultTransport
	http.DefaultTransport = tr
	defer func() { tr.CloseIdleConnections(); http.DefaultTransport = old }()
	w := githubAppWorld(t)
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	wrapper := filepath.Join(t.TempDir(), "native-pages-fakepi")
	script := "#!/bin/sh\nset -eu\nif [ \"${1-}\" = --version ]; then exec " + quote(w.cfg.PiBin) + " \"$@\"; fi\nlark-cli api GET /open-apis/im/v1/messages --params '{\"container_id_type\":\"chat\",\"container_id\":\"oc_synthetic\"}' --page-all --page-delay 50 --page-limit 3 --as user > native-output.json 2> native-error.txt\nexec " + quote(w.cfg.PiBin) + " \"$@\"\n"
	if e := os.WriteFile(wrapper, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	w.cfg.PiBin = wrapper
	s := w.start(func(c *config.Config) { c.LarkUserBundleDir = bundle })
	defer s.stop()
	provider := larktest.New(t)
	s.a.CLIAuth.Lark = provider.Client
	s.a.CLIAuth.LarkCoordinator.Options.Now = func() time.Time { return time.UnixMilli(clock.Load()) }
	s.a.CLIAuth.LarkCoordinator.Refresher = f
	_, base, accounts, source := larkAppSource(t, s)
	a := provider.Add("ou-shared-native", "Synthetic", nil, "")
	a.Release()
	op := uuid.New()
	s.must(202, "POST", base, map[string]any{"operationId": op, "cli": "lark-cli", "authType": "lark_user", "sourceAccountId": source.ID, "expectedSourceRevision": source.Revision})
	user := waitLarkApp(t, s, base, op, "succeeded").Account
	s.must(201, "POST", accounts, map[string]any{"purpose": "model", "provider": "anthropic", "name": "synthetic shared native", "credential": map[string]string{"type": "api_key", "key": "synthetic-model"}})
	paths := make([]string, 2)
	for i := range paths {
		path, agent := s.createAgent(fmt.Sprintf("native shared %d", i))
		paths[i] = path
		s.must(200, "PUT", path+"/clis/lark-cli", nil)
		current := decode[apitypes.Agent](t, s.must(200, "GET", path, nil))
		s.must(200, "PUT", path+"/credential-selections", map[string]any{"purpose": "cli", "cli": "lark-cli", "accountId": user.ID, "expectedConfigVersion": current.ConfigVersion})
		_ = agent
	}
	for _, path := range paths {
		s.must(202, "POST", path+"/messages", map[string]any{"text": "shared native pages"})
	}
	for _, path := range paths {
		s.waitIdle(path)
	}
	var generation int64
	var state string
	if e := s.a.Store.Pool.QueryRow(context.Background(), `SELECT generation,refresh_state FROM lark_cli_grants WHERE version_id=$1`, *user.CurrentVersionID).Scan(&generation, &state); e != nil {
		t.Fatal(e)
	}
	if pages.Load() != 6 || f.calls.Load() != 2 || generation != 3 || state != "ready" || denied.Load() != 0 {
		t.Fatalf("two-consumer refresh result pages=%d refreshes=%d generation=%d state=%s denied=%d", pages.Load(), f.calls.Load(), generation, state, denied.Load())
	}
	var completed int
	if e := s.a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM runs WHERE status='completed'`).Scan(&completed); e != nil || completed != 2 {
		t.Fatal("native consumers did not complete")
	}
}
