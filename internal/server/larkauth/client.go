package larkauth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"loop-now/internal/server/clicredential"
	cm "loop-now/internal/server/credentialmodel"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const RequestedScopes = "offline_access im:message im:message.send_as_user im:message.group_msg:get_as_user"

type Application struct{ Brand, AppID, AppSecret string }

func (Application) String() string               { return "[Lark application]" }
func (Application) GoString() string             { return "[Lark application]" }
func (Application) MarshalJSON() ([]byte, error) { return nil, &SafeError{Code: "invalid_response"} }
func (a Application) Valid() bool {
	return (a.Brand == "feishu" || a.Brand == "lark") && a.AppID != "" && len(a.AppID) <= 200 && a.AppSecret != "" && len(a.AppSecret) <= 8192 && utf8.ValidString(a.AppID+a.AppSecret) && strings.IndexFunc(a.AppID+a.AppSecret, unicode.IsControl) < 0 && strings.IndexFunc(a.AppID+a.AppSecret, unicode.IsSpace) < 0
}

type Authorization struct {
	URL       string
	UserCode  *string
	ExpiresAt time.Time
}
type Client struct {
	HTTP *http.Client
	Now  func() time.Time
}

func NewClient(client *http.Client) *Client {
	if client == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.Proxy = nil
		client = &http.Client{Transport: tr}
	}
	h := *client
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	h.Timeout = 30 * time.Second
	return &Client{HTTP: &h, Now: time.Now}
}
func origins(brand string) (string, string) {
	if brand == "lark" {
		return "https://accounts.larksuite.com", "https://open.larksuite.com"
	}
	return "https://accounts.feishu.cn", "https://open.feishu.cn"
}

// The public browser URI is validated independently of the injectable HTTP
// transport. Tests can only route the fixed production endpoint to loopback.
func ValidAuthorizationURL(brand, raw string) bool {
	a, _ := origins(brand)
	u, e := url.Parse(raw)
	if (brand != "feishu" && brand != "lark") || e != nil || len(raw) > 4096 || u.Scheme != "https" || u.Host != strings.TrimPrefix(a, "https://") || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || u.Path != "/oauth/v1/device/verify" {
		return false
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil {
		return false
	}
	for k, v := range q {
		if (k != "flow_id" && k != "user_code") || len(v) != 1 || !publicText(v[0], 200) {
			return false
		}
	}
	return true
}
func publicText(v string, max int) bool {
	return v != "" && utf8.ValidString(v) && len(v) <= max && strings.TrimSpace(v) == v && strings.IndexFunc(v, unicode.IsControl) < 0
}
func (c *Client) request(ctx context.Context, method, endpoint, contentType string, body []byte, authorization string, out any) (int, error) {
	req, e := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if e != nil {
		return 0, &SafeError{Code: "invalid_response"}
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return 0, &SafeError{Code: "network_error"}
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, clicredential.MaxBytes+1))
	defer clear(b)
	if e != nil || len(b) > clicredential.MaxBytes {
		return resp.StatusCode, &SafeError{Code: "invalid_response", HTTPStatus: resp.StatusCode}
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return resp.StatusCode, &SafeError{Code: "oauth_failed", HTTPStatus: resp.StatusCode}
	}
	if json.Unmarshal(b, out) != nil {
		return resp.StatusCode, &SafeError{Code: "invalid_response", HTTPStatus: resp.StatusCode}
	}
	return resp.StatusCode, nil
}

type tokenResponse struct {
	Code             *int    `json:"code"`
	Error            string  `json:"error"`
	AccessToken      string  `json:"access_token"`
	RefreshToken     string  `json:"refresh_token"`
	ExpiresIn        int64   `json:"expires_in"`
	RefreshExpiresIn int64   `json:"refresh_token_expires_in"`
	Scope            *string `json:"scope"`
	TokenType        string  `json:"token_type"`
}

type deviceResponse struct {
	DeviceCode string `json:"device_code"`
	UserCode   string `json:"user_code"`
	URI        string `json:"verification_uri"`
	Complete   string `json:"verification_uri_complete"`
	ExpiresIn  int64  `json:"expires_in"`
	Interval   int64  `json:"interval"`
	Error      string `json:"error"`
}

// Missing timing fields use the pinned client's defaults. Explicit null is
// malformed and must not silently preserve an initialized nonzero default.
func (d *deviceResponse) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	for key, value := range fields {
		if (strings.EqualFold(key, "expires_in") || strings.EqualFold(key, "interval")) && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return &SafeError{Code: "invalid_response"}
		}
	}
	type wire deviceResponse
	return json.Unmarshal(raw, (*wire)(d))
}

func expiry(now time.Time, seconds int64) (int64, bool) {
	if seconds < 1 || seconds > 366*24*3600 {
		return 0, false
	}
	n := now.Add(time.Duration(seconds) * time.Second).UnixMilli()
	return n, n > 0 && n <= 253402300799999
}
func (c *Client) credential(ctx context.Context, a Application, tr tokenResponse, issued time.Time) (clicredential.Lark, error) {
	exp, ok := expiry(issued, tr.ExpiresIn)
	rexp, rok := expiry(issued, tr.RefreshExpiresIn)
	if !ok || !rok || tr.AccessToken == "" || tr.RefreshToken == "" || tr.Error != "" || tr.Code != nil && *tr.Code != 0 || tr.TokenType != "" && !strings.EqualFold(tr.TokenType, "Bearer") {
		return clicredential.Lark{}, &SafeError{Code: "invalid_response"}
	}
	_, open := origins(a.Brand)
	var user struct {
		Code *int `json:"code"`
		Data struct {
			OpenID string `json:"open_id"`
			Name   string `json:"name"`
		} `json:"data"`
	}
	status, e := c.request(ctx, "GET", open+"/open-apis/authen/v1/user_info", "", nil, "Bearer "+tr.AccessToken, &user)
	if e != nil {
		return clicredential.Lark{}, e
	}
	if status != 200 || user.Code == nil || *user.Code != 0 || user.Data.OpenID == "" {
		return clicredential.Lark{}, &SafeError{Code: "invalid_response", HTTPStatus: status}
	}
	name := user.Data.Name
	if name == "" {
		name = user.Data.OpenID
	}
	var scopes []string
	if tr.Scope != nil {
		scopes = append([]string{}, strings.Fields(*tr.Scope)...)
	}
	result := clicredential.Lark{Type: "lark_user", AppSecret: a.AppSecret, AccessToken: tr.AccessToken, RefreshToken: tr.RefreshToken, ExpiresAt: exp, RefreshExpiresAt: rexp, Identity: cm.LarkIdentity{Brand: a.Brand, AppID: a.AppID, OpenID: user.Data.OpenID, DisplayName: name, GrantedScopes: scopes, VerifiedAt: c.Now()}, RuntimeVariantVersion: clicredential.LarkUserVariant}
	if !result.Valid() {
		return clicredential.Lark{}, &SafeError{Code: "invalid_response"}
	}
	return result, nil
}
func (c *Client) Login(ctx context.Context, a Application, onAuth func(Authorization) error) (clicredential.Lark, error) {
	if !a.Valid() {
		return clicredential.Lark{}, &SafeError{Code: "invalid_response"}
	}
	accounts, open := origins(a.Brand)
	form := url.Values{"client_id": {a.AppID}, "scope": {RequestedScopes}}
	device := deviceResponse{ExpiresIn: 240, Interval: 5} // fixed official device_flow.go absent-field defaults
	// Basic authentication is required by the fixed official Device Flow.
	reqAuth := &http.Request{Header: http.Header{}}
	reqAuth.SetBasicAuth(a.AppID, a.AppSecret)
	status, e := c.request(ctx, "POST", accounts+"/oauth/v1/device_authorization", "application/x-www-form-urlencoded", []byte(form.Encode()), reqAuth.Header.Get("Authorization"), &device)
	if e != nil {
		return clicredential.Lark{}, e
	}
	if status != 200 || device.Error != "" || device.DeviceCode == "" || len(device.DeviceCode) > 8192 || strings.IndexFunc(device.DeviceCode, unicode.IsControl) >= 0 || device.ExpiresIn < 1 || device.ExpiresIn > 3600 || device.Interval < 1 || device.Interval > 60 {
		return clicredential.Lark{}, &SafeError{Code: "invalid_response", HTTPStatus: status}
	}
	u := device.Complete
	if u == "" {
		u = device.URI
	}
	if !ValidAuthorizationURL(a.Brand, u) {
		return clicredential.Lark{}, &SafeError{Code: "invalid_response"}
	}
	parsed, _ := url.Parse(u)
	for _, vals := range parsed.Query() {
		for _, v := range vals {
			if strings.Contains(v, device.DeviceCode) || strings.Contains(v, a.AppSecret) {
				return clicredential.Lark{}, &SafeError{Code: "invalid_response"}
			}
		}
	}
	var code *string
	if device.UserCode != "" {
		if !publicText(device.UserCode, 200) || strings.Contains(device.UserCode, device.DeviceCode) || strings.Contains(device.UserCode, a.AppSecret) {
			return clicredential.Lark{}, &SafeError{Code: "invalid_response"}
		}
		code = &device.UserCode
	}
	deadline := c.Now().Add(time.Duration(device.ExpiresIn) * time.Second)
	if onAuth == nil {
		return clicredential.Lark{}, &SafeError{Code: "invalid_response"}
	}
	if e = onAuth(Authorization{u, code, deadline}); e != nil {
		return clicredential.Lark{}, e
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	interval := time.Duration(device.Interval) * time.Second
	for {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return clicredential.Lark{}, ctx.Err()
		case <-timer.C:
		}
		issued := c.Now()
		form = url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "client_id": {a.AppID}, "client_secret": {a.AppSecret}, "device_code": {device.DeviceCode}}
		var tr tokenResponse
		status, e = c.request(ctx, "POST", open+"/open-apis/authen/v2/oauth/token", "application/x-www-form-urlencoded", []byte(form.Encode()), "", &tr)
		if e != nil {
			return clicredential.Lark{}, e
		}
		if status != 200 && status != 400 {
			return clicredential.Lark{}, &SafeError{Code: "oauth_failed", HTTPStatus: status}
		}
		switch tr.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			if interval > 60*time.Second {
				interval = 60 * time.Second
			}
			continue
		case "access_denied":
			return clicredential.Lark{}, &SafeError{Code: "auth_cancelled"}
		case "expired_token", "invalid_grant":
			return clicredential.Lark{}, &SafeError{Code: "auth_timeout"}
		}
		if status != 200 {
			return clicredential.Lark{}, &SafeError{Code: "oauth_failed", HTTPStatus: status}
		}
		return c.credential(ctx, a, tr, issued)
	}
}
func (c *Client) Refresh(ctx context.Context, old clicredential.Lark) (clicredential.Lark, error) {
	if !old.Valid() || old.RefreshExpiresAt <= c.Now().UnixMilli() {
		return clicredential.Lark{}, &SafeError{Code: "reauth_required"}
	}
	a := Application{old.Identity.Brand, old.Identity.AppID, old.AppSecret}
	_, open := origins(a.Brand)
	b, _ := json.Marshal(map[string]string{"grant_type": "refresh_token", "client_id": a.AppID, "client_secret": a.AppSecret, "refresh_token": old.RefreshToken})
	defer clear(b)
	var tr tokenResponse
	issued := c.Now()
	status, e := c.request(ctx, "POST", open+"/open-apis/authen/v2/oauth/token", "application/json", b, "", &tr)
	if e != nil {
		return clicredential.Lark{}, e
	}
	if tr.Error == "invalid_grant" {
		return clicredential.Lark{}, &SafeError{Code: "reauth_required"}
	}
	if tr.Code == nil {
		return clicredential.Lark{}, &SafeError{Code: "invalid_response", HTTPStatus: status}
	}
	if status != 200 {
		return clicredential.Lark{}, &SafeError{Code: "oauth_failed", HTTPStatus: status}
	}
	next, e := c.credential(ctx, a, tr, issued)
	if e != nil {
		return clicredential.Lark{}, e
	}
	if next.Identity.OpenID != old.Identity.OpenID {
		return clicredential.Lark{}, &SafeError{Code: "invalid_response"}
	}
	// Identity metadata belongs to the original verified version, not the next
	// account pointer or a newly inferred consent. Only secret generation rotates.
	next.Identity = old.Identity.Clone()
	return next, nil
}
