package githubauth

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
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Both URL and socket destination are checked: the fixture can only dial its
// own httptest listener. No request is sent to a provider, DNS or host proxy.
func fixture(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != u.Host {
			return nil, errors.New("fixture network deny")
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	c := NewClient(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		good := r.URL.Scheme == "https" && r.URL.RawQuery == "" && (r.URL.Host == "github.com" && (r.URL.Path == "/login/device/code" || r.URL.Path == "/login/oauth/access_token") || r.URL.Host == "api.github.com" && r.URL.Path == "/user")
		if !good {
			return nil, errors.New("fixture endpoint deny")
		}
		copy := r.Clone(r.Context())
		cloneURL := *r.URL
		cloneURL.Scheme, cloneURL.Host = u.Scheme, u.Host
		copy.URL = &cloneURL
		return transport.RoundTrip(copy)
	}))
	c.wait = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return c
}

func device() map[string]any {
	return map[string]any{"device_code": "fixture-device-secret", "user_code": "ABCD-1234", "verification_uri": VerificationURL, "expires_in": 900, "interval": 1}
}

func token() map[string]any {
	return map[string]any{"access_token": "fixture-access-secret", "token_type": "bearer", "scope": "repo,read:org"}
}

func output(w http.ResponseWriter, value any) { _ = json.NewEncoder(w).Encode(value) }

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var safe *SafeError
	if !errors.As(err, &safe) || safe.Code != code {
		t.Fatalf("expected safe category %s, got %v", code, err)
	}
	for _, secret := range []string{"fixture-device-secret", "fixture-access-secret", "provider-raw-secret", "ABCD-1234"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("unsafe error text")
		}
	}
}

func TestDeviceFlowPendingSlowDownAndVerifiedIdentity(t *testing.T) {
	var polls atomic.Int32
	var waits []time.Duration
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			t.Error("missing JSON negotiation")
		}
		switch r.URL.Path {
		case "/login/device/code":
			_ = r.ParseForm()
			if r.Form.Get("client_id") != ClientID || r.Form.Get("scope") != "repo read:org" || len(r.Form) != 2 || r.Header.Get("Authorization") != "" {
				t.Error("app/scope or device request boundary changed")
			}
			output(w, device())
		case "/login/oauth/access_token":
			_ = r.ParseForm()
			if r.Form.Get("device_code") != "fixture-device-secret" || len(r.Form) != 3 || r.Header.Get("Authorization") != "" {
				t.Error("token request boundary changed")
			}
			switch polls.Add(1) {
			case 1:
				w.WriteHeader(http.StatusBadRequest)
				output(w, map[string]any{"error": "authorization_pending"})
			case 2:
				output(w, map[string]any{"error": "slow_down", "interval": 2})
			default:
				output(w, token())
			}
		case "/user":
			if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer fixture-access-secret" {
				t.Error("identity must use this grant")
			}
			_, _ = w.Write([]byte(`{"id":12345678901234567890,"login":"verified-user"}`))
		}
	})
	c.wait = func(ctx context.Context, d time.Duration) error { waits = append(waits, d); return ctx.Err() }
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	parentDeadline, _ := ctx.Deadline()
	credential, err := c.Login(ctx, func(a Authorization) error {
		if a.URL != VerificationURL || a.UserCode != "ABCD-1234" || a.ExpiresAt != parentDeadline {
			t.Fatal("authorization publication or deadline changed")
		}
		return nil
	})
	if err != nil || credential.Identity.UserID != "12345678901234567890" || credential.Identity.Login != "verified-user" || credential.Identity.Host != "github.com" {
		t.Fatal("verified identity not returned", err)
	}
	if len(waits) != 3 || waits[0] != time.Second || waits[1] != time.Second || waits[2] != 6*time.Second {
		t.Fatal("pending/slow_down pacing changed")
	}
	plain, err := credential.Encode()
	if err != nil || !strings.Contains(string(plain), `"GH_TOKEN":"fixture-access-secret"`) {
		t.Fatal("static payload encoding failed")
	}
	clear(plain)
	if _, err = json.Marshal(credential); err == nil || strings.Contains(fmt.Sprintf("%#v", credential), "fixture-access-secret") {
		t.Fatal("credential must not be implicitly printable/serializable")
	}
}

func TestDeviceInvalidPublicationCannotReachToken(t *testing.T) {
	cases := []struct {
		name, field string
		value       any
	}{
		{"http", "verification_uri", "http://github.com/login/device"},
		{"foreign", "verification_uri", "https://github.com.evil.test/login/device"},
		{"query", "verification_uri", VerificationURL + "?code=secret"},
		{"fragment", "verification_uri", VerificationURL + "#fragment"},
		{"code", "user_code", "abcd-1234"},
		{"device_null", "device_code", nil},
		{"expiry_null", "expires_in", nil},
		{"expiry_zero", "expires_in", 0},
		{"expiry_fraction", "expires_in", 1.5},
		{"expiry_overflow", "expires_in", int64(1 << 62)},
		{"interval_zero", "interval", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var tokenCalls atomic.Int32
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/login/device/code" {
					tokenCalls.Add(1)
				}
				value := device()
				value[tc.field] = tc.value
				output(w, value)
			})
			_, err := c.Login(context.Background(), func(Authorization) error { t.Error("invalid authorization published"); return nil })
			requireCode(t, err, "invalid_response")
			if tokenCalls.Load() != 0 {
				t.Fatal("invalid device response reached token endpoint")
			}
		})
	}
}

func TestUnexpectedRotatingGrantFieldPresenceFailsClosed(t *testing.T) {
	for _, field := range []string{"expires_in", "refresh_token", "refresh_token_expires_in"} {
		for _, value := range []any{nil, "", 0, "provider-raw-secret"} {
			t.Run(fmt.Sprintf("%s/%T-%v", field, value, value), func(t *testing.T) {
				var userCalls atomic.Int32
				c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/login/device/code":
						output(w, device())
					case "/login/oauth/access_token":
						v := token()
						v[field] = value
						output(w, v)
					case "/user":
						userCalls.Add(1)
					}
				})
				_, err := c.Login(context.Background(), func(Authorization) error { return nil })
				requireCode(t, err, "invalid_response")
				if userCalls.Load() != 0 {
					t.Fatal("unsupported grant reached identity lookup")
				}
			})
		}
	}
}

func TestProviderFailuresAndMalformedBodiesAreSafe(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"error":"access_denied","error_description":"provider-raw-secret"}`, "oauth_failed"},
		{`{"error":"expired_token"}`, "auth_timeout"},
		{`{"error":"slow_down","interval":0}`, "invalid_response"},
		{`{"access_token":"fixture-access-secret","token_type":"mac"}`, "invalid_response"},
		{`{"access_token":null,"token_type":"bearer"}`, "invalid_response"},
		{`{"access_token":"first","access_token":"second","token_type":"bearer"}`, "invalid_response"},
		{`null`, "invalid_response"},
		{`{} {}`, "invalid_response"},
		{`<html>provider-raw-secret</html>`, "invalid_response"},
		{strings.Repeat("x", maxResponse+1), "invalid_response"},
	} {
		t.Run(tc.want+fmt.Sprint(len(tc.body)), func(t *testing.T) {
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/login/device/code" {
					output(w, device())
				} else {
					_, _ = w.Write([]byte(tc.body))
				}
			})
			_, err := c.Login(context.Background(), func(Authorization) error { return nil })
			requireCode(t, err, tc.want)
		})
	}
}

func TestGrantScopesUnknownEmptyAndBadIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, user string
		scope      any
		omit       bool
		wantError  bool
		wantNil    bool
	}{
		{"unknown", `{"id":1,"login":"user"}`, nil, true, false, true},
		{"null", `{"id":1,"login":"user"}`, nil, false, false, true},
		{"empty", `{"id":1,"login":"user"}`, "", false, false, false},
		{"bad_id", `{"id":"1","login":"user"}`, "repo", false, true, false},
		{"zero_id", `{"id":0,"login":"user"}`, "repo", false, true, false},
		{"no_login", `{"id":1}`, "repo", false, true, false},
		{"bad_scope", `{"id":1,"login":"user"}`, []string{"repo"}, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/login/device/code":
					output(w, device())
				case "/login/oauth/access_token":
					v := token()
					if tc.omit {
						delete(v, "scope")
					} else {
						v["scope"] = tc.scope
					}
					output(w, v)
				case "/user":
					_, _ = w.Write([]byte(tc.user))
				}
			})
			credential, err := c.Login(context.Background(), func(Authorization) error { return nil })
			if tc.wantError {
				requireCode(t, err, "invalid_response")
			} else if err != nil || (credential.Identity.GrantedScopes == nil) != tc.wantNil {
				t.Fatal("scope unknown/empty distinction lost", err)
			}
		})
	}
}

func TestActualHTTPRequestCancellationAndRedirectDeny(t *testing.T) {
	t.Run("disconnected_provider", func(t *testing.T) {
		c := fixture(t, func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error("fixture hijack unavailable")
				return
			}
			_ = conn.Close()
		})
		_, err := c.Login(context.Background(), func(Authorization) error { t.Error("disconnect published"); return nil })
		requireCode(t, err, "network_error")
	})
	t.Run("provider_http_status", func(t *testing.T) {
		c := fixture(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			output(w, map[string]any{"error_description": "provider-raw-secret"})
		})
		_, err := c.Login(context.Background(), func(Authorization) error { t.Error("HTTP rejection published"); return nil })
		requireCode(t, err, "oauth_failed")
		var safe *SafeError
		if !errors.As(err, &safe) || safe.HTTPStatus != http.StatusForbidden {
			t.Fatal("safe status classification lost")
		}
	})
	t.Run("deadline_during_http", func(t *testing.T) {
		release := make(chan struct{})
		defer close(release)
		c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			select {
			case <-r.Context().Done():
			case <-release:
			}
		})
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()
		_, err := c.Login(ctx, func(Authorization) error { t.Error("timed-out device response published"); return nil })
		requireCode(t, err, "auth_timeout")
	})
	t.Run("cancel_during_http", func(t *testing.T) {
		entered, ended := make(chan struct{}), make(chan struct{})
		release := make(chan struct{})
		defer close(release)
		var once sync.Once
		c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/login/device/code" {
				output(w, device())
				return
			}
			// Drain the POST body so HTTP/1 can observe the peer closing the
			// connection while this handler deliberately waits for cancellation.
			_, _ = io.Copy(io.Discard, r.Body)
			once.Do(func() { close(entered) })
			select {
			case <-r.Context().Done():
			case <-release:
				return
			}
			close(ended)
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { _, err := c.Login(ctx, func(Authorization) error { return nil }); done <- err }()
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("token request not entered")
		}
		cancel()
		select {
		case err := <-done:
			requireCode(t, err, "auth_cancelled")
		case <-time.After(time.Second):
			t.Fatal("HTTP did not cancel")
		}
		select {
		case <-ended:
		case <-time.After(time.Second):
			t.Fatal("server request remained live")
		}
	})
	t.Run("redirect", func(t *testing.T) {
		c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://foreign.example.invalid/secret", http.StatusFound)
		})
		_, err := c.Login(context.Background(), func(Authorization) error { t.Error("redirect published"); return nil })
		requireCode(t, err, "network_error")
	})
	t.Run("publication_rejected", func(t *testing.T) {
		var polls atomic.Int32
		c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/login/device/code" {
				polls.Add(1)
			}
			output(w, device())
		})
		_, err := c.Login(context.Background(), func(Authorization) error { return errors.New("provider-raw-secret") })
		requireCode(t, err, "auth_cancelled")
		if polls.Load() != 0 {
			t.Fatal("rejected publication polled token")
		}
	})
}

func TestDeviceExpiryStartsBeforeResponseArrives(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1100 * time.Millisecond)
		v := device()
		v["expires_in"] = 1
		output(w, v)
	})
	_, err := c.Login(context.Background(), func(Authorization) error { t.Error("expired device response published"); return nil })
	requireCode(t, err, "auth_timeout")
}
