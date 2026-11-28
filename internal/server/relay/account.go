package relay

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
)

// SignInTTL is how long the server waits for the browser to come back from the relay's sign-in page.
const SignInTTL = 10 * time.Minute

// CallbackPath is where the relay sends the browser back, on the sign-in's own loopback listener.
const CallbackPath = "/relay/callback"

// exchangeTimeout bounds the code exchange the browser waits for.
const exchangeTimeout = 20 * time.Second

// File is what the server keeps about the relay, in the data folder, readable only by its user (mode 0600): the
// contract's "what the host keeps" plus the switch for access from phones. The credential is a secret: never log it.
type File struct {
	// ClientID names this computer at the relay; made once, kept across sign-outs, so signing in to the same account
	// again finds the same host (and address).
	ClientID string `json:"clientId"`
	// RelayURL is the relay the credential and the host belong to; a file of another relay is read as signed out.
	RelayURL     string     `json:"relayUrl,omitempty"`
	Credential   string     `json:"credential,omitempty"`
	CredentialID string     `json:"credentialId,omitempty"`
	AccountID    string     `json:"accountId,omitempty"`
	SignedInAt   *time.Time `json:"signedInAt,omitempty"`
	HostID       string     `json:"hostId,omitempty"`
	Hostname     string     `json:"hostname,omitempty"`
	// TunnelEnabled is the switch "access from phones".
	TunnelEnabled bool `json:"tunnelEnabled"`
}

// Status mirrors the RelayAccount schema.
type Status struct {
	RelayURL   string     `json:"relayUrl"`
	SignedIn   bool       `json:"signedIn"`
	AccountID  *string    `json:"accountId"`
	SignedInAt *time.Time `json:"signedInAt"`
	SignIn     *SignIn    `json:"signIn"`
	Error      *string    `json:"error"`
}

// SignIn mirrors the RelaySignIn schema.
type SignIn struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Account signs this computer in to a Loop Now account and keeps the relay's file in the data folder.
type Account struct {
	Client *Client
	// Path is the file, such as <data>/relay/host.json.
	Path string
	// HostName is this computer's name, shown on the relay's page and in its host list.
	HostName func() string
	Log      *slog.Logger
	Now      func() time.Time
	// SignInTTL overrides SignInTTL (for tests).
	SignInTTL time.Duration
	// OnChange is called, without any lock held, after a sign-in finished or the credential was forgotten.
	OnChange func()

	mu      sync.Mutex
	file    File
	pending *pendingSignIn
	lastErr string
	closed  bool
}

type pendingSignIn struct {
	SignIn
	state, verifier, redirectURI string
	srv                          *http.Server
	timer                        *time.Timer
	// done is set once the sign-in ended (a matching callback, a timeout, a cancel); later callbacks are refused.
	done bool
}

func (a *Account) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Account) log() *slog.Logger {
	if a.Log != nil {
		return a.Log
	}
	return slog.New(slog.DiscardHandler)
}

// Load reads the file (a missing one is a fresh computer) and makes the clientId on first use.
func (a *Account) Load() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	data, err := os.ReadFile(a.Path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		a.file = File{}
	case err != nil:
		return fmt.Errorf("read %s: %w", a.Path, err)
	default:
		var f File
		if err := json.Unmarshal(data, &f); err != nil {
			return fmt.Errorf("read %s: %w", a.Path, err)
		}
		a.file = f
		if info, err := os.Stat(a.Path); err == nil && info.Mode().Perm()&0o077 != 0 {
			a.log().Warn("the relay file was readable by other users; making it private", "path", a.Path)
			_ = os.Chmod(a.Path, 0o600)
		}
	}
	if a.file.RelayURL != "" && a.file.RelayURL != a.Client.BaseURL {
		// Never send a credential to a relay that did not issue it, nor bring up a host it does not know.
		a.log().Info("the relay file belongs to another relay; signed out of it here", "fileRelay", a.file.RelayURL, "relay", a.Client.BaseURL)
		a.file = File{ClientID: a.file.ClientID}
	}
	if a.file.ClientID == "" {
		a.file.ClientID = uuid.NewString()
		return a.saveLocked()
	}
	return nil
}

// saveLocked writes the file atomically, readable only by this user.
func (a *Account) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(a.Path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(a.file, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(a.Path), ".host-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), a.Path)
}

// Status reports the account and a sign-in in progress.
func (a *Account) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := Status{RelayURL: a.Client.BaseURL, SignedIn: a.file.Credential != ""}
	if s.SignedIn {
		id := a.file.AccountID
		s.AccountID, s.SignedInAt = &id, a.file.SignedInAt
	}
	if p := a.pending; p != nil && !p.done {
		in := p.SignIn
		s.SignIn = &in
	}
	if a.lastErr != "" {
		msg := a.lastErr
		s.Error = &msg
	}
	return s
}

// Credential returns the host credential; empty when signed out.
func (a *Account) Credential() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.file.Credential
}

// ClientID returns this computer's clientId.
func (a *Account) ClientID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.file.ClientID
}

// Host returns the host id and hostname from the last registration; empty when there is none.
func (a *Account) Host() (id, hostname string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.file.HostID, a.file.Hostname
}

// SetHost keeps the host the relay registered (empty strings forget it).
func (a *Account) SetHost(id, hostname string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.file.HostID == id && a.file.Hostname == hostname {
		return nil
	}
	a.file.HostID, a.file.Hostname = id, hostname
	if id != "" {
		a.file.RelayURL = a.Client.BaseURL
	}
	return a.saveLocked()
}

// TunnelEnabled reads the switch "access from phones".
func (a *Account) TunnelEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.file.TunnelEnabled
}

// SetTunnelEnabled saves the switch "access from phones".
func (a *Account) SetTunnelEnabled(on bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.file.TunnelEnabled == on {
		return nil
	}
	a.file.TunnelEnabled = on
	return a.saveLocked()
}

func randomString() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ChallengeOf is the S256 PKCE challenge of a verifier: BASE64URL(SHA-256(verifier)) without padding.
func ChallengeOf(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func signedIn() error {
	return apperr.Conflict(apperr.CodeRelaySignedIn, "This computer is already signed in to a Loop Now account. Sign out first.")
}

// StartSignIn starts a sign-in: it listens once on 127.0.0.1 for the browser's return and gives the relay's page to
// open in the system browser. A sign-in still waiting is replaced.
func (a *Account) StartSignIn() (SignIn, error) {
	verifier, err := randomString()
	if err != nil {
		return SignIn{}, err
	}
	state, err := randomString()
	if err != nil {
		return SignIn{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return SignIn{}, apperr.Conflict(apperr.CodeRelaySignedOut, "The server is shutting down.")
	}
	if a.file.Credential != "" {
		return SignIn{}, signedIn()
	}
	a.endPendingLocked()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return SignIn{}, fmt.Errorf("listen for the sign-in's return: %w", err)
	}
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", ln.Addr().(*net.TCPAddr).Port, CallbackPath)
	hostName := ""
	if a.HostName != nil {
		hostName = a.HostName()
	}
	ttl := a.SignInTTL
	if ttl <= 0 {
		ttl = SignInTTL
	}
	p := &pendingSignIn{
		SignIn: SignIn{URL: a.Client.ConnectURL(redirectURI, state, ChallengeOf(verifier), hostName), ExpiresAt: a.now().Add(ttl).UTC()},
		state:  state, verifier: verifier, redirectURI: redirectURI,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+CallbackPath, func(w http.ResponseWriter, r *http.Request) { a.callback(p, w, r) })
	p.srv = &http.Server{Handler: callbackHeaders(mux), ReadHeaderTimeout: 10 * time.Second}
	p.timer = time.AfterFunc(ttl, func() { a.expire(p) })
	a.pending, a.lastErr = p, ""
	go func() { _ = p.srv.Serve(ln) }()
	a.log().Info("sign in to a Loop Now account: open this address in a browser on this computer", "url", p.URL)
	return p.SignIn, nil
}

func callbackHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// endPendingLocked stops waiting for the browser; the listener closes once a callback in progress has answered.
func (a *Account) endPendingLocked() {
	p := a.pending
	if p == nil {
		return
	}
	a.pending = nil
	p.done = true
	p.timer.Stop()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), exchangeTimeout+5*time.Second)
		defer cancel()
		if err := p.srv.Shutdown(ctx); err != nil {
			_ = p.srv.Close()
		}
	}()
}

// CancelSignIn stops waiting for the browser.
func (a *Account) CancelSignIn() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.endPendingLocked()
}

func (a *Account) expire(p *pendingSignIn) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pending != p {
		return
	}
	a.lastErr = "The sign-in timed out: the browser did not come back within 10 minutes. Start it again."
	a.endPendingLocked()
}

func (a *Account) callback(p *pendingSignIn, w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	a.mu.Lock()
	if p.done || a.pending != p {
		a.mu.Unlock()
		writePage(w, http.StatusGone, pageEnded)
		return
	}
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(p.state)) != 1 {
		// Not the browser this sign-in sent; keep waiting for that one.
		a.mu.Unlock()
		writePage(w, http.StatusBadRequest, pageWrongState)
		return
	}
	// The state matched: this is the one return the sign-in waited for, whatever it carries.
	a.endPendingLocked()
	code, errParam := q.Get("code"), q.Get("error")
	if code == "" {
		if errParam == "access_denied" {
			a.lastErr = "The sign-in was cancelled in the browser."
		} else {
			a.lastErr = "The relay did not sign this computer in (" + clip(errParam, 64) + "). Start the sign-in again."
		}
		a.mu.Unlock()
		writePage(w, http.StatusOK, pageCancelled)
		return
	}
	a.mu.Unlock()

	// Not the request's context: the sign-in finishes even when the tab is closed meanwhile.
	ctx, cancel := context.WithTimeout(context.Background(), exchangeTimeout)
	defer cancel()
	tok, err := a.Client.ExchangeCode(ctx, code, p.verifier, p.redirectURI)
	a.mu.Lock()
	if err == nil && a.file.Credential == "" {
		now := a.now().UTC()
		a.file.RelayURL, a.file.Credential, a.file.CredentialID, a.file.AccountID, a.file.SignedInAt = a.Client.BaseURL, tok.Credential, tok.CredentialID, tok.AccountID, &now
		if err = a.saveLocked(); err != nil {
			a.file.Credential, a.file.CredentialID, a.file.AccountID, a.file.SignedInAt = "", "", "", nil
			err = fmt.Errorf("could not keep the credential in the data folder: %w", err)
		}
	}
	if err != nil {
		a.lastErr = "The relay refused the sign-in: " + clip(err.Error(), 300) + " Start the sign-in again."
	}
	a.mu.Unlock()
	if err != nil {
		a.log().Warn("signing in to the Loop Now account failed", "err", err)
		writePage(w, http.StatusBadGateway, pageFailed)
		return
	}
	a.log().Info("signed in to the Loop Now account", "accountId", tok.AccountID)
	writePage(w, http.StatusOK, pageSignedIn)
	if a.OnChange != nil {
		a.OnChange()
	}
}

// Forget drops the credential after the relay refused it (401): the person must sign in again. credential is the one
// that was refused, so a newer sign-in is never undone by an older answer.
func (a *Account) Forget(credential string) {
	a.mu.Lock()
	if credential == "" || a.file.Credential != credential {
		a.mu.Unlock()
		return
	}
	a.dropCredentialLocked(false)
	a.lastErr = "The Loop Now account signed this computer out. Sign in again."
	a.mu.Unlock()
	a.log().Warn("the Loop Now relay no longer accepts this computer's credential; signed out")
	if a.OnChange != nil {
		a.OnChange()
	}
}

// dropCredentialLocked deletes the credential; turnOff also turns the switch "access from phones" off (signing out
// does; a credential the relay refused keeps it on, so the tunnel comes back after signing in again).
func (a *Account) dropCredentialLocked(turnOff bool) {
	a.file.Credential, a.file.CredentialID, a.file.AccountID, a.file.SignedInAt = "", "", "", nil
	if turnOff {
		a.file.TunnelEnabled = false
	}
	if err := a.saveLocked(); err != nil {
		a.log().Error("could not delete the credential from the data folder", "path", a.Path, "err", err)
	}
}

// SignOut asks the relay to revoke the credential and deletes it, even when the relay cannot be reached; it also
// turns the switch "access from phones" off. The host and its address stay reserved at the relay.
func (a *Account) SignOut(ctx context.Context) {
	a.mu.Lock()
	a.endPendingLocked()
	credential := a.file.Credential
	if credential != "" {
		a.dropCredentialLocked(true)
	}
	a.lastErr = ""
	a.mu.Unlock()
	if credential == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := a.Client.Revoke(ctx, credential); err != nil && Code(err) != CodeUnauthorized {
		a.log().Warn("the relay could not revoke the credential; it was deleted here anyway", "err", err)
	}
	a.log().Info("signed out of the Loop Now account")
}

// Close stops waiting for a browser, for server shutdown.
func (a *Account) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	a.endPendingLocked()
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// The pages the browser sees when it comes back from the relay. check-english: zh (a page for the person, in both
// languages, English first)
var (
	pageSignedIn   = page{"Signed in", "This computer is now signed in to your Loop Now account. You can close this tab and go back to Loop Now.", "已登录", "这台电脑已经登录你的 Loop Now 账号。可以关闭这个标签页，回到 Loop Now。"}
	pageCancelled  = page{"Sign-in cancelled", "Loop Now was not signed in. You can close this tab.", "已取消登录", "Loop Now 没有登录。可以关闭这个标签页。"}
	pageFailed     = page{"Sign-in failed", "The Loop Now relay refused the sign-in. Go back to Loop Now and try again.", "登录失败", "Loop Now 中继拒绝了这次登录。请回到 Loop Now 再试一次。"}
	pageWrongState = page{"Not this sign-in", "This link does not belong to the sign-in Loop Now is waiting for. Start the sign-in again from Loop Now.", "不是这次登录", "这个链接不属于 Loop Now 正在等待的那次登录。请在 Loop Now 里重新开始登录。"}
	pageEnded      = page{"Sign-in ended", "Loop Now no longer waits for this sign-in. Start it again from Loop Now.", "登录已结束", "Loop Now 已经不再等待这次登录。请在 Loop Now 里重新开始。"}
)

type page struct{ title, body, titleZh, bodyZh string }

func writePage(w http.ResponseWriter, status int, p page) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	e := html.EscapeString
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Loop Now · %s</title>
<style>body{font:16px/1.6 system-ui,sans-serif;max-width:32rem;margin:15vh auto;padding:0 1.5rem;color:#222}h1{font-size:1.3rem;margin:0 0 .4rem}p{margin:0 0 1.4rem;color:#555}</style></head>
<body><h1>%s</h1><p>%s</p><h1 lang="zh-CN">%s</h1><p lang="zh-CN">%s</p></body></html>`, e(p.title), e(p.title), e(p.body), e(p.titleZh), e(p.bodyZh))
}

// MachineName is a short name for this computer, for the relay's page and host list: the host name without a
// trailing ".local", at most 64 characters.
func MachineName() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	name = strings.TrimSuffix(strings.TrimSuffix(name, "."), ".local")
	return clip(name, 63)
}
