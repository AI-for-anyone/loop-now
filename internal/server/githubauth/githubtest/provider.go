// Package githubtest supplies an isolated synthetic device provider for server
// integration tests. Its URL and DialContext guards permit only its listener.
package githubtest

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
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"loop-now/internal/server/githubauth"
)

type Attempt struct {
	UserID, Login string
	Scopes        *string
	OAuthError    string
	Waiting       chan struct{}
	Cancelled     chan struct{}
	release       chan struct{}
	once          sync.Once
	waitOnce      sync.Once
	cancelOnce    sync.Once
	token         string
}

func (a *Attempt) Release() { a.once.Do(func() { close(a.release) }) }

type Provider struct {
	t           testing.TB
	Client      *githubauth.Client
	mu          sync.Mutex
	queue       []*Attempt
	devices     map[string]*Attempt
	tokens      map[string]*Attempt
	all         []*Attempt
	DeviceCalls atomic.Int32
	TokenCalls  atomic.Int32
	UserCalls   atomic.Int32
	DeniedDials atomic.Int32
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func New(t testing.TB) *Provider {
	t.Helper()
	p := &Provider{t: t, devices: map[string]*Attempt{}, tokens: map[string]*Attempt{}}
	srv := httptest.NewServer(http.HandlerFunc(p.serve))
	u, _ := url.Parse(srv.URL)
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr != u.Host {
			p.DeniedDials.Add(1)
			return nil, errors.New("fixture socket deny")
		}
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}
	p.Client = githubauth.NewClient(transportFunc(func(r *http.Request) (*http.Response, error) {
		good := r.URL.Scheme == "https" && r.URL.RawQuery == "" && (r.URL.Host == "github.com" && (r.URL.Path == "/login/device/code" || r.URL.Path == "/login/oauth/access_token") || r.URL.Host == "api.github.com" && r.URL.Path == "/user")
		if !good {
			p.DeniedDials.Add(1)
			return nil, errors.New("fixture endpoint deny")
		}
		clone := r.Clone(r.Context())
		uri := *r.URL
		uri.Scheme, uri.Host = u.Scheme, u.Host
		clone.URL = &uri
		return transport.RoundTrip(clone)
	}))
	t.Cleanup(func() {
		p.mu.Lock()
		for _, a := range p.all {
			a.Release()
		}
		p.mu.Unlock()
		transport.CloseIdleConnections()
		srv.Close()
	})
	return p
}

func (p *Provider) Add(user, login string, scopes *string, oauthError string) *Attempt {
	p.mu.Lock()
	defer p.mu.Unlock()
	a := &Attempt{UserID: user, Login: login, Scopes: scopes, OAuthError: oauthError, Waiting: make(chan struct{}), Cancelled: make(chan struct{}), release: make(chan struct{}), token: fmt.Sprintf("synthetic-access-%d", len(p.all)+1)}
	p.all = append(p.all, a)
	p.queue = append(p.queue, a)
	p.tokens[a.token] = a
	return a
}

func (p *Provider) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	send := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	if r.Header.Get("Accept") != "application/json" {
		p.t.Error("fixture expected JSON negotiation")
	}
	switch r.URL.Path {
	case "/login/device/code":
		p.DeviceCalls.Add(1)
		_ = r.ParseForm()
		if r.Method != "POST" || r.Form.Get("client_id") != githubauth.ClientID || r.Form.Get("scope") != "repo read:org" || len(r.Form) != 2 || r.Header.Get("Authorization") != "" {
			p.t.Error("fixture device request violated fixed grant")
		}
		p.mu.Lock()
		if len(p.queue) == 0 {
			p.mu.Unlock()
			p.t.Error("unexpected extra device start")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		a := p.queue[0]
		p.queue = p.queue[1:]
		code := fmt.Sprintf("synthetic-device-%d", len(p.devices)+1)
		p.devices[code] = a
		p.mu.Unlock()
		send(map[string]any{"device_code": code, "user_code": "ABCD-1234", "verification_uri": githubauth.VerificationURL, "expires_in": 900, "interval": 1})
	case "/login/oauth/access_token":
		p.TokenCalls.Add(1)
		_ = r.ParseForm()
		if r.Method != "POST" || r.Form.Get("client_id") != githubauth.ClientID || r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || len(r.Form) != 3 || r.Header.Get("Authorization") != "" {
			p.t.Error("fixture token request violated device protocol")
		}
		p.mu.Lock()
		a := p.devices[r.Form.Get("device_code")]
		p.mu.Unlock()
		if a == nil {
			p.t.Error("unknown synthetic device")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		a.waitOnce.Do(func() { close(a.Waiting) })
		select {
		case <-a.release:
		case <-r.Context().Done():
			a.cancelOnce.Do(func() { close(a.Cancelled) })
			return
		}
		if a.OAuthError != "" {
			send(map[string]any{"error": a.OAuthError, "error_description": "synthetic-provider-message-must-not-leak"})
			return
		}
		out := map[string]any{"access_token": a.token, "token_type": "bearer"}
		if a.Scopes != nil {
			out["scope"] = *a.Scopes
		}
		send(out)
	case "/user":
		p.UserCalls.Add(1)
		p.mu.Lock()
		a := p.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		p.mu.Unlock()
		if r.Method != "GET" || a == nil {
			p.t.Error("fixture identity did not use this operation's token")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		send(map[string]any{"id": json.Number(a.UserID), "login": a.Login})
	default:
		_, _ = io.Copy(io.Discard, r.Body)
		p.t.Error("unexpected fixture endpoint")
		w.WriteHeader(http.StatusNotFound)
	}
}
