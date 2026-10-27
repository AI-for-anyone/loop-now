package larkauth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"loop-now/internal/server/clicredential"
)

func TestLarkOfficialProtocolRejectsOversizeRedirectAndMalformedIdentity(t *testing.T) {
	for _, scenario := range []string{"device_oversize", "device_redirect", "device_null_expiry", "device_null_interval", "device_null_alias", "token_oversize", "token_http_failure", "identity_missing_code", "identity_wrong_shape", "identity_control"} {
		t.Run(scenario, func(t *testing.T) {
			var device, token, user, redirect atomic.Int32
			c := localProtocolClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/oauth/v1/device_authorization":
					device.Add(1)
					switch scenario {
					case "device_oversize":
						w.Write([]byte(strings.Repeat("x", clicredential.MaxBytes+1)))
						return
					case "device_redirect":
						w.Header().Set("Location", "https://accounts.feishu.cn/redirect-target")
						w.WriteHeader(302)
						return
					}
					out := map[string]any{"device_code": "synthetic-device", "verification_uri": "https://accounts.feishu.cn/oauth/v1/device/verify", "expires_in": 60, "interval": 1}
					if scenario == "device_null_expiry" {
						out["expires_in"] = nil
					}
					if scenario == "device_null_interval" {
						out["interval"] = nil
					}
					if scenario == "device_null_alias" {
						delete(out, "expires_in")
						out["Expires_In"] = nil
					}
					json.NewEncoder(w).Encode(out)
				case "/open-apis/authen/v2/oauth/token":
					token.Add(1)
					if scenario == "token_oversize" {
						w.Write([]byte(strings.Repeat("x", clicredential.MaxBytes+1)))
						return
					}
					if scenario == "token_http_failure" {
						w.WriteHeader(500)
						json.NewEncoder(w).Encode(map[string]any{"error": "synthetic-raw-provider-message", "access_token": "synthetic-access"})
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"code": 0, "access_token": "synthetic-access", "refresh_token": "synthetic-refresh", "expires_in": 7200, "refresh_token_expires_in": 86400})
				case "/open-apis/authen/v1/user_info":
					user.Add(1)
					out := map[string]any{"code": 0, "data": map[string]any{"open_id": "ou-fixture", "name": "valid"}}
					switch scenario {
					case "identity_missing_code":
						delete(out, "code")
					case "identity_wrong_shape":
						out["data"] = []string{"not-an-object"}
					case "identity_control":
						out["data"].(map[string]any)["open_id"] = "ou\ninvalid"
					}
					json.NewEncoder(w).Encode(out)
				default:
					redirect.Add(1)
					w.WriteHeader(500)
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			visible := 0
			got, err := c.Login(ctx, Application{"feishu", "app-fixture", "synthetic-app-secret"}, func(Authorization) error { visible++; return nil })
			if err == nil || got.AccessToken != "" || strings.Contains(err.Error(), "synthetic") || device.Load() != 1 || redirect.Load() != 0 {
				t.Fatal("invalid response accepted, leaked or retried")
			}
			if strings.HasPrefix(scenario, "device_") {
				if visible != 0 || token.Load() != 0 || user.Load() != 0 {
					t.Fatal("invalid device response crossed next stage")
				}
			} else if strings.HasPrefix(scenario, "token_") {
				if visible != 1 || token.Load() != 1 || user.Load() != 0 {
					t.Fatal("invalid token response reached identity or retried")
				}
			} else if visible != 1 || token.Load() != 1 || user.Load() != 1 {
				t.Fatal("identity validation protocol stages wrong")
			}
		})
	}
}
