package larkauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func localProtocolClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	local, _ := url.Parse(srv.URL)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	t.Cleanup(transport.CloseIdleConnections)
	return NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || (r.URL.Host != "accounts.feishu.cn" && r.URL.Host != "open.feishu.cn") {
			t.Error("unexpected production endpoint")
			return nil, &SafeError{Code: "invalid_response"}
		}
		clone := r.Clone(r.Context())
		u := *r.URL
		u.Scheme = local.Scheme
		u.Host = local.Host
		clone.URL = &u
		clone.Host = local.Host
		return transport.RoundTrip(clone)
	})})
}
func TestLarkDeviceFlowProtocolAndMissingRefresh(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "missing_refresh_rejected"}[missing], func(t *testing.T) {
			var requested, tokenCalls, userCalls atomic.Int32
			c := localProtocolClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/oauth/v1/device_authorization":
					id, secret, ok := r.BasicAuth()
					if !ok || id != "app-fixture" || secret != "fixture-app-secret" {
						t.Error("wrong application auth")
					}
					r.ParseForm()
					if r.Form.Get("scope") != RequestedScopes || len(r.Form) != 2 {
						t.Error("request did not use exact fixed scopes")
					}
					requested.Add(1)
					json.NewEncoder(w).Encode(map[string]any{"device_code": "synthetic-device-secret", "verification_uri_complete": "https://accounts.feishu.cn/oauth/v1/device/verify?flow_id=public-flow", "expires_in": 60, "interval": 1})
				case "/open-apis/authen/v2/oauth/token":
					r.ParseForm()
					if r.Form.Get("device_code") != "synthetic-device-secret" || r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
						t.Error("wrong device exchange")
					}
					tokenCalls.Add(1)
					refresh := "synthetic-refresh"
					if missing {
						refresh = ""
					}
					json.NewEncoder(w).Encode(map[string]any{"access_token": "synthetic-access", "refresh_token": refresh, "expires_in": 7200, "refresh_token_expires_in": 86400})
				case "/open-apis/authen/v1/user_info":
					if r.Header.Get("Authorization") != "Bearer synthetic-access" {
						t.Error("identity verification did not use issued token")
					}
					userCalls.Add(1)
					json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"open_id": "ou-fixture", "name": "核实用户"}})
				default:
					t.Error("unexpected protocol path")
					http.Error(w, "fixture", 404)
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var visible Authorization
			result, e := c.Login(ctx, Application{"feishu", "app-fixture", "fixture-app-secret"}, func(a Authorization) error { visible = a; return nil })
			if visible.UserCode != nil || !ValidAuthorizationURL("feishu", visible.URL) {
				t.Fatal("optional user code or safe authorization URL lost")
			}
			if requested.Load() != 1 || tokenCalls.Load() != 1 {
				t.Fatal("unexpected protocol retry")
			}
			if missing {
				if e == nil || strings.Contains(e.Error(), "synthetic") {
					t.Fatal("missing refresh accepted or leaked")
				}
				if userCalls.Load() != 0 {
					t.Fatal("invalid credential reached identity stage")
				}
			} else {
				if e != nil || !result.Valid() || result.Identity.GrantedScopes != nil || userCalls.Load() != 1 {
					t.Fatal("verified credential not produced")
				}
			}
		})
	}
}
func TestLarkAuthorizationURLNegativeFixtures(t *testing.T) {
	base := "https://accounts.feishu.cn/oauth/v1/device/verify"
	for _, s := range []string{base, base + "?flow_id=public&user_code=visible"} {
		if !ValidAuthorizationURL("feishu", s) {
			t.Fatal("valid URL rejected")
		}
	}
	for _, s := range []string{"http://accounts.feishu.cn/oauth/v1/device/verify", "https://accounts.feishu.cn.evil.test/oauth/v1/device/verify", base + "?device_code=secret", base + "?access_token=secret", base + "?client_secret=secret", base + "?redirect_uri=https://evil.test", base + "#fragment", base + "?flow_id=a&flow_id=b", "https://user@accounts.feishu.cn/oauth/v1/device/verify", "https://accounts.feishu.cn:443/oauth/v1/device/verify", base + "?flow_id=%0a"} {
		if ValidAuthorizationURL("feishu", s) {
			t.Fatal("unsafe URL accepted")
		}
	}
	if ValidAuthorizationURL("lark", base) {
		t.Fatal("wrong brand accepted")
	}
}

func TestLarkDeviceSecretCannotBecomeVisibleInteraction(t *testing.T) {
	for _, field := range []string{"userCode", "urlDevice", "urlAppSecret"} {
		t.Run(field, func(t *testing.T) {
			c := localProtocolClient(t, func(w http.ResponseWriter, r *http.Request) {
				out := map[string]any{"device_code": "synthetic-private-device", "verification_uri_complete": "https://accounts.feishu.cn/oauth/v1/device/verify?flow_id=public", "expires_in": 60, "interval": 1}
				switch field {
				case "userCode":
					out["user_code"] = "prefix-synthetic-private-device"
				case "urlDevice":
					out["verification_uri_complete"] = "https://accounts.feishu.cn/oauth/v1/device/verify?flow_id=prefix-synthetic-private-device"
				case "urlAppSecret":
					out["verification_uri_complete"] = "https://accounts.feishu.cn/oauth/v1/device/verify?flow_id=fixture-app-secret"
				}
				json.NewEncoder(w).Encode(out)
			})
			var visible atomic.Int32
			_, e := c.Login(context.Background(), Application{"feishu", "app-fixture", "fixture-app-secret"}, func(Authorization) error { visible.Add(1); return nil })
			if e == nil || visible.Load() != 0 {
				t.Fatal("secret reached user-visible authorization callback")
			}
		})
	}
}

func TestLarkDeviceAbsentTimingUsesPinnedDefaults(t *testing.T) {
	c := localProtocolClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"device_code": "synthetic-device-secret", "verification_uri": "https://accounts.feishu.cn/oauth/v1/device/verify"})
	})
	seen := false
	_, e := c.Login(context.Background(), Application{"feishu", "app-fixture", "fixture-app-secret"}, func(a Authorization) error {
		seen = true
		remaining := time.Until(a.ExpiresAt)
		if remaining < 239*time.Second || remaining > 241*time.Second {
			t.Error("device deadline did not use pinned 240-second default")
		}
		return context.Canceled
	})
	if !seen || e == nil {
		t.Fatal("optional device timing rejected or callback cancellation ignored")
	}
}
