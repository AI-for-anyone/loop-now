// Package githubauth implements only the pinned GitHub CLI application's device
// grant. It has no subprocess, browser, filesystem or host-authentication access.
package githubauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"loop-now/internal/server/clitemplate"
	cm "loop-now/internal/server/credentialmodel"
)

const (
	ClientID        = "178c6fc778ccc68e1d6a"
	VerificationURL = "https://github.com/login/device"
	deviceEndpoint  = "https://github.com/login/device/code"
	tokenEndpoint   = "https://github.com/login/oauth/access_token"
	userEndpoint    = "https://api.github.com/user"
	maxResponse     = 64 << 10
)

var userCodePattern = regexp.MustCompile(`^[A-Z0-9]{4}-[A-Z0-9]{4}$`)
var userIDPattern = regexp.MustCompile(`^[1-9][0-9]*$`)

// SafeError never contains provider text, URL parameters, codes or tokens.
type SafeError struct {
	Code       string
	Stage      string
	HTTPStatus int
}

func (e *SafeError) Error() string { return "GitHub授权失败: " + e.Code }

type Authorization struct {
	URL       string
	UserCode  string
	ExpiresAt time.Time
}

func (Authorization) String() string { return "GitHubAuthorization[redacted]" }

type Credential struct {
	Identity cm.GitHubIdentity
	token    string
}

func (Credential) String() string   { return "GitHubCredential[redacted]" }
func (Credential) GoString() string { return "GitHubCredential[redacted]" }
func (Credential) MarshalJSON() ([]byte, error) {
	return nil, errors.New("GitHub秘密仅可显式编码")
}

// Encode uses the unchanged static CLI payload path, not a Google grant.
func (c Credential) Encode() ([]byte, error) {
	if !safeText(c.token, maxResponse) || c.Identity.Host != "github.com" || !userIDPattern.MatchString(c.Identity.UserID) || !safeText(c.Identity.Login, 200) || c.Identity.VerifiedAt.IsZero() {
		return nil, invalid("credential")
	}
	return json.Marshal(clitemplate.Payload{Env: map[string]string{"GH_TOKEN": c.token}, Files: []clitemplate.PayloadFile{}})
}

type Client struct {
	http *http.Client
	wait func(context.Context, time.Duration) error
}

// The transport seam is for an isolated fixture. URLs and redirect policy are
// fixed here, and are never supplied by a login request or provider response.
func NewClient(transport http.RoundTripper) *Client {
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &Client{http: &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect forbidden") }}, wait: wait}
}

func invalid(stage string) *SafeError { return &SafeError{Code: "invalid_response", Stage: stage} }

func wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func safeText(s string, max int) bool {
	return s != "" && len(s) <= max && utf8.ValidString(s) && strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) < 0
}

func text(m map[string]json.RawMessage, key string) (string, bool) {
	var value string
	if len(m[key]) == 0 || json.Unmarshal(m[key], &value) != nil || bytes.Equal(m[key], []byte("null")) {
		return "", false
	}
	return value, true
}

func seconds(m map[string]json.RawMessage, key string) (time.Duration, bool) {
	var value int64
	if json.Unmarshal(m[key], &value) != nil || value <= 0 || value > int64((1<<63-1)/time.Second) {
		return 0, false
	}
	return time.Duration(value) * time.Second, true
}

func object(raw []byte) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return nil, invalid("json")
	}
	m := map[string]json.RawMessage{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || m[key] != nil {
			return nil, invalid("json")
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, invalid("json")
		}
		m[key] = value
	}
	if _, err = d.Token(); err != nil {
		return nil, invalid("json")
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, invalid("json")
	}
	return m, nil
}

func (c *Client) request(ctx context.Context, endpoint string, form url.Values, token string) (map[string]json.RawMessage, http.Header, error) {
	method := http.MethodPost
	var body io.Reader
	if form == nil {
		method = http.MethodGet
	} else {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, nil, invalid("request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Loop-Now-GitHub-device-login")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, &SafeError{Code: "network_error", Stage: "http"}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	defer clear(raw)
	if err != nil {
		return nil, nil, &SafeError{Code: "network_error", Stage: "body", HTTPStatus: resp.StatusCode}
	}
	if len(raw) > maxResponse {
		return nil, nil, invalid("body_limit")
	}
	if resp.StatusCode != http.StatusOK {
		// Device-grant errors may use RFC 8628's HTTP 400 envelope. Never
		// accept a successful token on a non-200 response.
		if endpoint == tokenEndpoint && resp.StatusCode == http.StatusBadRequest {
			m, parseErr := object(raw)
			if parseErr == nil {
				if code, valid := text(m, "error"); valid && code != "" {
					if _, hasToken := m["access_token"]; !hasToken {
						return m, resp.Header, nil
					}
				}
			}
		}
		return nil, nil, &SafeError{Code: "oauth_failed", Stage: "http_status", HTTPStatus: resp.StatusCode}
	}
	m, err := object(raw)
	return m, resp.Header, err
}

func scopes(m map[string]json.RawMessage, headers http.Header) ([]string, error) {
	var raw string
	if v, present := m["scope"]; present {
		if bytes.Equal(v, []byte("null")) {
			return nil, nil
		}
		if json.Unmarshal(v, &raw) != nil {
			return nil, invalid("scopes")
		}
	} else if values, present := headers["X-Oauth-Scopes"]; present && len(values) == 1 {
		raw = values[0]
	} else {
		return nil, nil
	}
	out := []string{}
	seen := map[string]bool{}
	for _, s := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }) {
		if !safeText(s, 200) {
			return nil, invalid("scopes")
		}
		if !seen[s] {
			out = append(out, s)
			seen[s] = true
		}
	}
	sort.Strings(out)
	return out, nil
}

func (c *Client) Login(ctx context.Context, onAuthorization func(Authorization) error) (result Credential, err error) {
	var cancel context.CancelFunc
	if _, bounded := ctx.Deadline(); bounded {
		ctx, cancel = context.WithCancel(ctx)
	} else {
		ctx, cancel = context.WithTimeout(ctx, 5*time.Minute)
	}
	defer cancel()
	result, err = c.login(ctx, onAuthorization)
	if err != nil {
		result = Credential{}
		if ctx.Err() != nil {
			err = contextError(ctx)
		}
	}
	return result, err
}

func contextError(ctx context.Context) error {
	code := "auth_cancelled"
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		code = "auth_timeout"
	}
	return &SafeError{Code: code, Stage: "context"}
}

func (c *Client) login(ctx context.Context, onAuthorization func(Authorization) error) (result Credential, err error) {
	started := time.Now()
	device, _, err := c.request(ctx, deviceEndpoint, url.Values{"client_id": {ClientID}, "scope": {"repo read:org"}}, "")
	if err != nil {
		return result, err
	}
	deviceCode, ok := text(device, "device_code")
	userCode, userOK := text(device, "user_code")
	uri, uriOK := text(device, "verification_uri")
	lifetime, expiryOK := seconds(device, "expires_in")
	interval, intervalOK := seconds(device, "interval")
	if !ok || !safeText(deviceCode, maxResponse) || !userOK || !userCodePattern.MatchString(userCode) || !uriOK || uri != VerificationURL || !expiryOK || !intervalOK {
		return result, invalid("device")
	}
	deadline := started.Add(lifetime)
	if parent, ok := ctx.Deadline(); ok && parent.Before(deadline) {
		deadline = parent
	}
	ctx, deviceCancel := context.WithDeadline(ctx, deadline)
	defer deviceCancel()
	defer func() {
		// Normalize before our own cancellation, so provider failure is not
		// accidentally reclassified as a caller cancellation during cleanup.
		if err != nil && ctx.Err() != nil {
			err = contextError(ctx)
		}
	}()
	if ctx.Err() != nil {
		return result, &SafeError{Code: "auth_timeout", Stage: "device"}
	}
	if onAuthorization == nil {
		return result, invalid("callback")
	}
	if err = onAuthorization(Authorization{URL: uri, UserCode: userCode, ExpiresAt: deadline}); err != nil {
		return result, &SafeError{Code: "auth_cancelled", Stage: "publication"}
	}
	for {
		if err = c.wait(ctx, interval); err != nil {
			return result, err
		}
		response, headers, err := c.request(ctx, tokenEndpoint, url.Values{"client_id": {ClientID}, "device_code": {deviceCode}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}, "")
		if err != nil {
			return result, err
		}
		if _, present := response["error"]; present {
			code, valid := text(response, "error")
			if _, hasToken := response["access_token"]; !valid || code == "" || hasToken {
				return result, invalid("token_error")
			}
			switch code {
			case "authorization_pending":
				continue
			case "slow_down":
				// The bounded operation deadline remains authoritative.
				if interval > (1<<63-1)-5*time.Second {
					return result, invalid("interval")
				}
				interval += 5 * time.Second
				if _, exists := response["interval"]; exists {
					v, valid := seconds(response, "interval")
					if !valid {
						return result, invalid("interval")
					}
					if v > interval {
						interval = v
					}
				}
				continue
			case "expired_token", "token_expired":
				return result, &SafeError{Code: "auth_timeout", Stage: "token"}
			default:
				return result, &SafeError{Code: "oauth_failed", Stage: "token"}
			}
		}
		for _, name := range []string{"expires_in", "refresh_token", "refresh_token_expires_in"} {
			if _, exists := response[name]; exists {
				return result, invalid("unsupported_grant")
			}
		}
		accessToken, tokenOK := text(response, "access_token")
		kind, kindOK := text(response, "token_type")
		if !tokenOK || !safeText(accessToken, maxResponse) || !kindOK || !strings.EqualFold(kind, "bearer") {
			return result, invalid("token")
		}
		granted, err := scopes(response, headers)
		if err != nil {
			return result, err
		}
		user, userHeaders, err := c.request(ctx, userEndpoint, nil, accessToken)
		if err != nil {
			return result, err
		}
		id := string(user["id"])
		login, loginOK := text(user, "login")
		if !userIDPattern.MatchString(id) || !loginOK || !safeText(login, 200) {
			return result, invalid("identity")
		}
		if _, supplied := response["scope"]; !supplied && granted == nil {
			granted, err = scopes(nil, userHeaders)
			if err != nil {
				return result, err
			}
		}
		return Credential{token: accessToken, Identity: cm.GitHubIdentity{Host: "github.com", UserID: id, Login: login, GrantedScopes: granted, VerifiedAt: time.Now().UTC()}}, nil
	}
}
