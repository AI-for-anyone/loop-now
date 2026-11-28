// Package relaytest is a fake Loop Now relay for tests: it follows the relay's v1 contract (sign-in codes bound to a
// PKCE challenge and a redirect URI, one-use codes whose replay revokes the credential, hosts by clientId, idempotent
// tunnel bring-up, release) and lets a test act as the browser and break things on purpose.
package relaytest

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// Host is a host the fake knows.
type Host struct {
	ID, ClientID, Account, Hostname, Name string
	TunnelID                              string // empty while it has no tunnel
	RemotePort                            int
	Tunnels                               int // how many tunnels were made for it
}

type code struct {
	challenge, redirectURI, account string
	expires                         time.Time
	used                            bool
	credential                      string // issued by the first use
}

// Fake is a running fake relay.
type Fake struct {
	URL string
	// HostLimit is the cap of hosts per account (3 by default).
	HostLimit int

	srv *httptest.Server
	mu  sync.Mutex
	// codes by their value; credentials -> account; hosts by id.
	codes   map[string]*code
	creds   map[string]string
	hosts   map[string]*Host
	calls   []string
	fail    map[string][]failure
	serial  int
	revoked []string
}

type failure struct {
	status     int
	code       string
	retryAfter int
}

// New starts a fake relay, closed when the test ends.
func New(t testing.TB) *Fake {
	f := &Fake{HostLimit: 3, codes: map[string]*code{}, creds: map[string]string{}, hosts: map[string]*Host{}, fail: map[string][]failure{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	f.URL = f.srv.URL
	t.Cleanup(f.srv.Close)
	return f
}

// Close stops the fake, so later calls fail to connect.
func (f *Fake) Close() { f.srv.Close() }

func random() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Approve plays the person who signs in to account on the /connect page and presses Continue: it checks the page's
// query like the relay does and returns where the browser is sent (the redirect URI with code and state).
func (f *Fake) Approve(connectURL, account string) (string, error) {
	q, err := f.connectQuery(connectURL)
	if err != nil {
		return "", err
	}
	c := random()
	f.mu.Lock()
	f.codes[c] = &code{challenge: q.Get("code_challenge"), redirectURI: q.Get("redirect_uri"), account: account, expires: time.Now().Add(5 * time.Minute)}
	f.mu.Unlock()
	return withQuery(q.Get("redirect_uri"), "code", c, q.Get("state")), nil
}

// Deny plays the person who presses Cancel on the /connect page.
func (f *Fake) Deny(connectURL string) (string, error) {
	q, err := f.connectQuery(connectURL)
	if err != nil {
		return "", err
	}
	return withQuery(q.Get("redirect_uri"), "error", "access_denied", q.Get("state")), nil
}

func withQuery(redirectURI, key, value, state string) string {
	u, _ := url.Parse(redirectURI)
	q := u.Query()
	q.Set(key, value)
	q.Set("state", state)
	u.RawQuery = q.Encode()
	return u.String()
}

func (f *Fake) connectQuery(connectURL string) (url.Values, error) {
	u, err := url.Parse(connectURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme+"://"+u.Host != f.URL || u.Path != "/connect" {
		return nil, fmt.Errorf("not this relay's /connect: %s", connectURL)
	}
	q := u.Query()
	r, err := url.Parse(q.Get("redirect_uri"))
	switch {
	case err != nil || r.Scheme != "http" || r.Hostname() != "127.0.0.1" || r.Port() == "" || r.User != nil || r.Fragment != "":
		return nil, fmt.Errorf("redirect_uri breaks the contract: %q", q.Get("redirect_uri"))
	case q.Get("state") == "" || len(q.Get("state")) > 512:
		return nil, fmt.Errorf("state breaks the contract")
	case len(q.Get("code_challenge")) != 43:
		return nil, fmt.Errorf("code_challenge is not 43 characters")
	case q.Get("code_challenge_method") != "S256":
		return nil, fmt.Errorf("code_challenge_method is not S256")
	case len([]rune(q.Get("host_name"))) > 64:
		return nil, fmt.Errorf("host_name is longer than 64 characters")
	}
	return q, nil
}

// Calls returns the calls made so far, such as "POST /v1/hosts".
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// Count returns how often call (such as "POST /v1/hosts/{id}/tunnel") was made; host ids are written {id}.
func (f *Fake) Count(call string) int {
	n := 0
	for _, c := range f.Calls() {
		if c == call {
			n++
		}
	}
	return n
}

// Fail makes the next call of call (such as "POST /v1/hosts/{id}/tunnel") answer status with code.
func (f *Fake) Fail(call string, status int, errCode string) {
	f.FailRetryAfter(call, status, errCode, 0)
}

// FailRetryAfter is Fail with a Retry-After header in seconds.
func (f *Fake) FailRetryAfter(call string, status int, errCode string, retryAfter int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail[call] = append(f.fail[call], failure{status: status, code: errCode, retryAfter: retryAfter})
}

// RevokeAll revokes every credential of account, as signing out elsewhere or a leaked code would.
func (f *Fake) RevokeAll(account string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for c, a := range f.creds {
		if a == account {
			delete(f.creds, c)
			f.revoked = append(f.revoked, c)
		}
	}
}

// Revoked reports whether credential was revoked.
func (f *Fake) Revoked(credential string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.revoked {
		if c == credential {
			return true
		}
	}
	return false
}

// Credentials is how many live credentials there are.
func (f *Fake) Credentials() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.creds)
}

// Hosts returns copies of the hosts.
func (f *Fake) Hosts() []Host {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Host
	for _, h := range f.hosts {
		out = append(out, *h)
	}
	return out
}

// Recycle deletes a host's tunnel, as the relay's offline recycle does; the next bring-up makes a new one.
func (f *Fake) Recycle(hostID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if h := f.hosts[hostID]; h != nil {
		h.TunnelID = ""
	}
}

// AddHost registers a host for account directly, as another computer would.
func (f *Fake) AddHost(account, clientID string) Host {
	f.mu.Lock()
	defer f.mu.Unlock()
	h := f.newHostLocked(account, clientID, "")
	return *h
}

func (f *Fake) newHostLocked(account, clientID, name string) *Host {
	f.serial++
	h := &Host{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", f.serial), ClientID: clientID, Account: account, Name: name,
		Hostname: fmt.Sprintf("h%011d.loop-now.test", f.serial)}
	f.hosts[h.ID] = h
	return h
}

// TunnelToken is the run token the fake hands out for a tunnel.
func TunnelToken(tunnelID string) string { return "token-for-" + tunnelID }

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	call := r.Method + " " + r.URL.Path
	var hostID string
	if rest, ok := strings.CutPrefix(r.URL.Path, "/v1/hosts/"); ok {
		hostID, _, _ = strings.Cut(rest, "/")
		call = r.Method + " " + strings.Replace(r.URL.Path, hostID, "{id}", 1)
	}
	f.mu.Lock()
	f.calls = append(f.calls, call)
	if q := f.fail[call]; len(q) > 0 {
		fl := q[0]
		f.fail[call] = q[1:]
		f.mu.Unlock()
		if fl.retryAfter > 0 {
			w.Header().Set("Retry-After", fmt.Sprint(fl.retryAfter))
		}
		writeError(w, fl.status, fl.code, "injected by the test")
		return
	}
	f.mu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	switch call {
	case "POST /v1/auth/token":
		f.token(w, r)
		return
	}
	account, credential, ok := f.auth(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, http.StatusUnauthorized, "unauthorized", "No valid host credential.")
		return
	}
	switch call {
	case "POST /v1/auth/revoke":
		f.mu.Lock()
		delete(f.creds, credential)
		f.revoked = append(f.revoked, credential)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case "POST /v1/hosts":
		f.register(w, r, account)
	case "POST /v1/hosts/{id}/tunnel":
		f.bringUp(w, r, account, hostID)
	case "DELETE /v1/hosts/{id}":
		f.mu.Lock()
		h := f.hosts[hostID]
		if h == nil || h.Account != account {
			f.mu.Unlock()
			writeError(w, http.StatusNotFound, "not_found", "No such host.")
			return
		}
		delete(f.hosts, hostID)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusNotFound, "not_found", "No such path.")
	}
}

func (f *Fake) auth(r *http.Request) (account, credential string, ok bool) {
	credential, ok = strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return "", "", false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	account, ok = f.creds[credential]
	return account, credential, ok
}

func (f *Fake) token(w http.ResponseWriter, r *http.Request) {
	var in struct{ GrantType, Code, CodeVerifier, RedirectURI string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Code == "" || in.CodeVerifier == "" || in.RedirectURI == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "A field is missing.")
		return
	}
	if in.GrantType != "authorization_code" {
		writeError(w, http.StatusBadRequest, "unsupported_grant_type", "Unsupported grantType.")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.codes[in.Code]
	sum := sha256.Sum256([]byte(in.CodeVerifier))
	switch {
	case c == nil || time.Now().After(c.expires):
		writeError(w, http.StatusBadRequest, "invalid_grant", "Unknown or expired code.")
		return
	case c.used:
		delete(f.creds, c.credential)
		f.revoked = append(f.revoked, c.credential)
		writeError(w, http.StatusBadRequest, "invalid_grant", "The code was already used.")
		return
	case base64.RawURLEncoding.EncodeToString(sum[:]) != c.challenge || in.RedirectURI != c.redirectURI:
		writeError(w, http.StatusBadRequest, "invalid_grant", "The verifier or redirectUri does not match.")
		return
	}
	c.used = true
	c.credential = "lnh_" + random()
	f.creds[c.credential] = c.account
	writeJSON(w, http.StatusOK, map[string]string{"credential": c.credential, "credentialId": "cred-" + c.account, "accountId": c.account, "tokenType": "Bearer"})
}

func (f *Fake) register(w http.ResponseWriter, r *http.Request, account string) {
	var in struct{ ClientID, Name string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.ClientID) < 16 || len(in.ClientID) > 64 || len([]rune(in.Name)) > 64 {
		writeError(w, http.StatusBadRequest, "invalid_request", "clientId or name breaks the rules.")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, h := range f.hosts {
		if h.Account != account {
			continue
		}
		if h.ClientID == in.ClientID {
			if in.Name != "" {
				h.Name = in.Name
			}
			writeJSON(w, http.StatusOK, map[string]any{"host": hostJSON(h)})
			return
		}
		n++
	}
	if n >= f.HostLimit {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "host_limit_reached", "message": fmt.Sprintf("This account already has %d hosts.", f.HostLimit), "limit": f.HostLimit}})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"host": hostJSON(f.newHostLocked(account, in.ClientID, in.Name))})
}

func (f *Fake) bringUp(w http.ResponseWriter, r *http.Request, account, hostID string) {
	var in struct{ RemotePort int }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.RemotePort < 1 || in.RemotePort > 65535 {
		writeError(w, http.StatusBadRequest, "invalid_request", "remotePort breaks the rules.")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	h := f.hosts[hostID]
	if h == nil || h.Account != account {
		writeError(w, http.StatusNotFound, "not_found", "No such host.")
		return
	}
	if h.TunnelID == "" {
		h.Tunnels++
		h.TunnelID = fmt.Sprintf("tunnel-%s-%d", h.ID[len(h.ID)-4:], h.Tunnels)
	}
	h.RemotePort = in.RemotePort
	writeJSON(w, http.StatusOK, map[string]any{"host": hostJSON(h), "tunnelToken": TunnelToken(h.TunnelID)})
}

func hostJSON(h *Host) map[string]any {
	var tunnel, port any
	if h.TunnelID != "" {
		tunnel = h.TunnelID
	}
	if h.RemotePort != 0 {
		port = h.RemotePort
	}
	return map[string]any{"id": h.ID, "hostname": h.Hostname, "url": "https://" + h.Hostname, "name": h.Name, "tunnelId": tunnel,
		"remotePort": port, "createdAt": "2026-11-28T07:30:00.000Z", "lastSeenAt": nil}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, errCode, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": errCode, "message": message}})
}
