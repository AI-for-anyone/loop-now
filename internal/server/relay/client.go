// Package relay talks to the Loop Now relay (relay.loop-now.app), which signs this computer in to a Loop Now account
// and hands out its tunnel. The contract is the relay's "Relay API v1" (docs/api.md in loop-now-backend); this package
// relies on nothing else.
package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultURL is the relay Loop Now uses unless LOOP_NOW_RELAY_URL says otherwise.
const DefaultURL = "https://relay.loop-now.app"

// Relay error codes this server acts on (the contract's error table).
const (
	CodeInvalidGrant     = "invalid_grant"
	CodeUnauthorized     = "unauthorized"
	CodeHostLimitReached = "host_limit_reached"
	CodeNotFound         = "not_found"
	CodeBusy             = "busy"
)

// Error is an error answer of the relay, or a failure to reach it (Status 0).
type Error struct {
	Status  int
	Code    string // the relay's error.code; empty when the answer was not a relay error or there was none
	Message string
	// Limit is the account's host cap for host_limit_reached.
	Limit int
	// RetryAfter is the Retry-After of a busy answer; 0 when none was given.
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	switch {
	case e.Status == 0:
		return "the Loop Now relay could not be reached: " + e.Message
	case e.Code != "":
		return fmt.Sprintf("the Loop Now relay answered %d %s: %s", e.Status, e.Code, e.Message)
	default:
		return fmt.Sprintf("the Loop Now relay answered %d: %s", e.Status, e.Message)
	}
}

// Code returns the relay error code of err; empty when err is not a relay error answer.
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Retryable reports whether a call that failed with err may succeed later without a change: the relay could not be
// reached, was busy, or failed on its side.
func Retryable(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Status == 0 || e.Code == CodeBusy || e.Status >= 500 || e.Status == http.StatusTooManyRequests
}

// Client calls the relay's host endpoints.
type Client struct {
	// BaseURL is the relay's origin, such as https://relay.loop-now.app (no trailing slash).
	BaseURL string
	// HTTP sends the requests; nil means a client with a 30-second timeout.
	HTTP *http.Client
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// ConnectURL is the relay's sign-in page for the system browser (GET /connect).
func (c *Client) ConnectURL(redirectURI, state, codeChallenge, hostName string) string {
	q := url.Values{}
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	if hostName != "" {
		q.Set("host_name", hostName)
	}
	return strings.TrimRight(c.BaseURL, "/") + "/connect?" + q.Encode()
}

// Token is the answer of a code exchange.
type Token struct {
	Credential   string `json:"credential"`
	CredentialID string `json:"credentialId"`
	AccountID    string `json:"accountId"`
	TokenType    string `json:"tokenType"`
}

// ExchangeCode trades a sign-in code for the host credential (POST /v1/auth/token).
func (c *Client) ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI string) (Token, error) {
	var out Token
	err := c.do(ctx, http.MethodPost, "/v1/auth/token", "", map[string]string{
		"grantType": "authorization_code", "code": code, "codeVerifier": codeVerifier, "redirectUri": redirectURI,
	}, &out)
	if err != nil {
		return Token{}, err
	}
	if out.Credential == "" {
		return Token{}, &Error{Status: http.StatusOK, Message: "the answer carries no credential"}
	}
	return out, nil
}

// Revoke signs out: the relay revokes the presented credential (POST /v1/auth/revoke).
func (c *Client) Revoke(ctx context.Context, credential string) error {
	return c.do(ctx, http.MethodPost, "/v1/auth/revoke", credential, nil, nil)
}

// Host is a host object of the relay.
type Host struct {
	ID         string     `json:"id"`
	Hostname   string     `json:"hostname"`
	URL        string     `json:"url"`
	Name       string     `json:"name"`
	TunnelID   *string    `json:"tunnelId"`
	RemotePort *int       `json:"remotePort"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastSeenAt *time.Time `json:"lastSeenAt"`
}

// Register registers this computer, or finds it again by clientID (POST /v1/hosts).
func (c *Client) Register(ctx context.Context, credential, clientID, name string) (Host, error) {
	body := map[string]string{"clientId": clientID}
	if name != "" {
		body["name"] = name
	}
	var out struct {
		Host Host `json:"host"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/hosts", credential, body, &out); err != nil {
		return Host{}, err
	}
	if out.Host.ID == "" || out.Host.Hostname == "" {
		return Host{}, &Error{Status: http.StatusOK, Message: "the answer carries no host"}
	}
	return out.Host, nil
}

// BringUp brings the host's tunnel up, pointing at remotePort on 127.0.0.1, and returns the host and the tunnel's run
// token (POST /v1/hosts/{id}/tunnel). The token is a secret.
func (c *Client) BringUp(ctx context.Context, credential, hostID string, remotePort int) (Host, string, error) {
	var out struct {
		Host        Host   `json:"host"`
		TunnelToken string `json:"tunnelToken"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/hosts/"+url.PathEscape(hostID)+"/tunnel", credential, map[string]int{"remotePort": remotePort}, &out); err != nil {
		return Host{}, "", err
	}
	if out.TunnelToken == "" {
		return Host{}, "", &Error{Status: http.StatusOK, Message: "the answer carries no tunnel token"}
	}
	return out.Host, out.TunnelToken, nil
}

// Release deletes the host's tunnel, DNS record and address (DELETE /v1/hosts/{id}).
func (c *Client) Release(ctx context.Context, credential, hostID string) error {
	return c.do(ctx, http.MethodDelete, "/v1/hosts/"+url.PathEscape(hostID), credential, nil, nil)
}

// maxAnswer is how much of an answer is read; the relay's bodies are at most 16 KiB.
const maxAnswer = 64 << 10

func (c *Client) do(ctx context.Context, method, path, credential string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Error{Message: errorText(err)}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return &Error{Message: errorText(err)}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return answerError(resp, data)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return &Error{Status: resp.StatusCode, Message: "the answer is not the JSON the contract describes"}
	}
	return nil
}

func answerError(resp *http.Response, data []byte) error {
	e := &Error{Status: resp.StatusCode}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Limit   int    `json:"limit"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &body) == nil && body.Error.Code != "" {
		e.Code, e.Message, e.Limit = body.Error.Code, body.Error.Message, body.Error.Limit
	} else {
		e.Message = http.StatusText(resp.StatusCode)
	}
	if s, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && s > 0 {
		e.RetryAfter = time.Duration(s) * time.Second
	}
	return e
}

// errorText is a transport error without the request URL, which Go puts in front of it.
func errorText(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}
