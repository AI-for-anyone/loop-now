// Package larktest supplies a loopback-only synthetic official-protocol fixture.
// It is never installed in the production composition root.
package larktest

import (
	"encoding/json"
	"github.com/google/uuid"
	"loop-now/internal/server/larkauth"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type Attempt struct {
	OpenID, Name                string
	Scopes                      []string
	Failure                     string
	released                    chan struct{}
	once                        sync.Once
	app, secret, device, access string
}

func (a *Attempt) Release()       { a.once.Do(func() { close(a.released) }) }
func (*Attempt) String() string   { return "[synthetic Lark attempt]" }
func (*Attempt) GoString() string { return "[synthetic Lark attempt]" }

type Provider struct {
	Client                                                        *larkauth.Client
	DeviceCalls, TokenCalls, UserCalls, RefreshCalls, DeniedDials atomic.Int32
	mu                                                            sync.Mutex
	queue                                                         []*Attempt
	devices, access, refresh                                      map[string]*Attempt
}
type localTransport struct {
	p     *Provider
	local *url.URL
	next  http.RoundTripper
}

func (t localTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" || (r.URL.Host != "accounts.feishu.cn" && r.URL.Host != "open.feishu.cn") {
		t.p.DeniedDials.Add(1)
		return nil, &larkauth.SafeError{Code: "invalid_response"}
	}
	clone := r.Clone(r.Context())
	u := *r.URL
	u.Scheme = t.local.Scheme
	u.Host = t.local.Host
	clone.URL = &u
	clone.Host = t.local.Host
	return t.next.RoundTrip(clone)
}
func New(t testing.TB) *Provider {
	t.Helper()
	p := &Provider{devices: map[string]*Attempt{}, access: map[string]*Attempt{}, refresh: map[string]*Attempt{}}
	srv := httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	t.Cleanup(tr.CloseIdleConnections)
	p.Client = larkauth.NewClient(&http.Client{Transport: localTransport{p, u, tr}})
	return p
}
func (p *Provider) Add(openID, name string, scopes []string, failure string) *Attempt {
	a := &Attempt{OpenID: openID, Name: name, Scopes: scopes, Failure: failure, released: make(chan struct{})}
	p.mu.Lock()
	p.queue = append(p.queue, a)
	p.mu.Unlock()
	return a
}
func (p *Provider) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	p.mu.Lock()
	defer p.mu.Unlock()
	send := func(v any) { json.NewEncoder(w).Encode(v) }
	switch r.URL.Path {
	case "/oauth/v1/device_authorization":
		p.DeviceCalls.Add(1)
		id, secret, ok := r.BasicAuth()
		r.ParseForm()
		if !ok || id == "" || secret == "" || r.Form.Get("scope") != larkauth.RequestedScopes || len(p.queue) == 0 {
			w.WriteHeader(400)
			send(map[string]string{"error": "invalid_client"})
			return
		}
		a := p.queue[0]
		p.queue = p.queue[1:]
		a.app = id
		a.secret = secret
		a.device = uuid.NewString()
		p.devices[a.device] = a
		send(map[string]any{"device_code": a.device, "verification_uri_complete": "https://accounts.feishu.cn/oauth/v1/device/verify?flow_id=" + uuid.NewString(), "expires_in": 60, "interval": 1})
	case "/open-apis/authen/v2/oauth/token":
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			p.RefreshCalls.Add(1)
			var in map[string]string
			json.NewDecoder(r.Body).Decode(&in)
			a := p.refresh[in["refresh_token"]]
			if a == nil || in["client_id"] != a.app || in["client_secret"] != a.secret || in["grant_type"] != "refresh_token" {
				w.WriteHeader(400)
				send(map[string]any{"code": 20026, "error": "invalid_grant"})
				return
			}
			delete(p.refresh, in["refresh_token"])
			p.issue(send, a)
			return
		}
		p.TokenCalls.Add(1)
		r.ParseForm()
		a := p.devices[r.Form.Get("device_code")]
		if a == nil || r.Form.Get("client_id") != a.app || r.Form.Get("client_secret") != a.secret || r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
			w.WriteHeader(400)
			send(map[string]string{"error": "invalid_grant"})
			return
		}
		select {
		case <-a.released:
		default:
			w.WriteHeader(400)
			send(map[string]string{"error": "authorization_pending"})
			return
		}
		if a.Failure != "" {
			w.WriteHeader(400)
			send(map[string]string{"error": a.Failure})
			return
		}
		delete(p.devices, a.device)
		p.issue(send, a)
	case "/open-apis/authen/v1/user_info":
		p.UserCalls.Add(1)
		a := p.access[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		if a == nil {
			w.WriteHeader(401)
			send(map[string]int{"code": 99991663})
			return
		}
		send(map[string]any{"code": 0, "data": map[string]string{"open_id": a.OpenID, "name": a.Name}})
	default:
		w.WriteHeader(404)
		send(map[string]int{"code": 404})
	}
}
func (p *Provider) issue(send func(any), a *Attempt) {
	a.access = "synthetic-access-" + uuid.NewString()
	refresh := "synthetic-refresh-" + uuid.NewString()
	p.access[a.access] = a
	p.refresh[refresh] = a
	out := map[string]any{"code": 0, "access_token": a.access, "refresh_token": refresh, "expires_in": 7200, "refresh_token_expires_in": 86400, "token_type": "Bearer"}
	if a.Scopes != nil {
		out["scope"] = strings.Join(a.Scopes, " ")
	}
	send(out)
}
