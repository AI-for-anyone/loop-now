package relay_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/relay"
	"loop-now/internal/server/relay/relaytest"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuffer) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

type accountEnv struct {
	t       *testing.T
	fake    *relaytest.Fake
	acc     *relay.Account
	logs    *lockedBuffer
	changes chan struct{}
}

func newAccount(t *testing.T) *accountEnv {
	t.Helper()
	fake := relaytest.New(t)
	logs := &lockedBuffer{}
	e := &accountEnv{t: t, fake: fake, logs: logs, changes: make(chan struct{}, 10)}
	e.acc = &relay.Account{Client: &relay.Client{BaseURL: fake.URL}, Path: filepath.Join(t.TempDir(), "relay", "host.json"),
		HostName: func() string { return "Studio Mac" }, Log: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		OnChange: func() { e.changes <- struct{}{} }}
	if err := e.acc.Load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.acc.Close)
	return e
}

// browse is the system browser following a redirect back to the server: it returns the status and the page.
func browse(t *testing.T, target string) (int, string) {
	t.Helper()
	resp, err := http.Get(target)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (e *accountEnv) signIn(account string) relay.SignIn {
	e.t.Helper()
	in, err := e.acc.StartSignIn()
	if err != nil {
		e.t.Fatal(err)
	}
	back, err := e.fake.Approve(in.URL, account)
	if err != nil {
		e.t.Fatal(err)
	}
	if status, body := browse(e.t, back); status != 200 || !strings.Contains(body, "Signed in") {
		e.t.Fatalf("callback answered %d: %s", status, body)
	}
	return in
}

func readFile(t *testing.T, path string) relay.File {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f relay.File
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSignInWithPKCEStoresThePrivateCredential(t *testing.T) {
	e := newAccount(t)
	in, err := e.acc.StartSignIn()
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(in.URL)
	q := u.Query()
	redirect, _ := url.Parse(q.Get("redirect_uri"))
	if u.Path != "/connect" || q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43 || len(q.Get("state")) < 43 ||
		redirect.Scheme != "http" || redirect.Hostname() != "127.0.0.1" || redirect.Path != relay.CallbackPath || q.Get("host_name") != "Studio Mac" {
		t.Fatalf("connect URL breaks the contract: %s", in.URL)
	}
	if d := time.Until(in.ExpiresAt); d < 9*time.Minute || d > 10*time.Minute {
		t.Fatalf("the sign-in waits 10 minutes, expires in %v", d)
	}
	if st := e.acc.Status(); st.SignIn == nil || st.SignIn.URL != in.URL || st.SignedIn {
		t.Fatalf("status while waiting: %+v", st)
	}
	back, err := e.fake.Approve(in.URL, "acct-1")
	if err != nil {
		t.Fatal(err)
	}
	status, body := browse(t, back)
	if status != 200 || !strings.Contains(body, "Signed in") || !strings.Contains(body, "已登录") { // check-english: zh (the page shows both languages)
		t.Fatalf("callback page %d: %s", status, body)
	}
	select {
	case <-e.changes:
	case <-time.After(2 * time.Second):
		t.Fatal("OnChange was not called after the sign-in")
	}
	st := e.acc.Status()
	if !st.SignedIn || st.AccountID == nil || *st.AccountID != "acct-1" || st.SignedInAt == nil || st.SignIn != nil || st.Error != nil || st.RelayURL != e.fake.URL {
		t.Fatalf("status after sign-in: %+v", st)
	}
	f := readFile(t, e.acc.Path)
	if !strings.HasPrefix(f.Credential, "lnh_") || f.AccountID != "acct-1" || f.RelayURL != e.fake.URL || f.ClientID == "" {
		t.Fatalf("file: %+v", f)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(e.acc.Path)
		dir, _ := os.Stat(filepath.Dir(e.acc.Path))
		if info.Mode().Perm() != 0o600 || dir.Mode().Perm() != 0o700 {
			t.Fatalf("file mode %v, folder mode %v; want 0600 and 0700", info.Mode().Perm(), dir.Mode().Perm())
		}
	}
	if strings.Contains(e.logs.String(), f.Credential) {
		t.Fatal("the credential was logged")
	}
	if e.acc.Credential() != f.Credential {
		t.Fatal("Credential() differs from the file")
	}
	// The callback listened once: the port is closed now.
	waitClosed(t, back)
}

func waitClosed(t *testing.T, target string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := http.Get(target)
		if err != nil {
			return
		}
		resp.Body.Close()
		if time.Now().After(deadline) {
			t.Fatalf("the sign-in's listener still answers %s (%d)", target, resp.StatusCode)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSignInRefusesAWrongStateAndKeepsWaiting(t *testing.T) {
	e := newAccount(t)
	in, _ := e.acc.StartSignIn()
	back, _ := e.fake.Approve(in.URL, "acct-1")
	u, _ := url.Parse(back)
	q := u.Query()
	q.Set("state", "someone-elses-state")
	u.RawQuery = q.Encode()
	if status, _ := browse(t, u.String()); status != http.StatusBadRequest {
		t.Fatalf("a wrong state answered %d", status)
	}
	if st := e.acc.Status(); st.SignedIn || st.SignIn == nil {
		t.Fatalf("after a wrong state the sign-in still waits: %+v", st)
	}
	if status, _ := browse(t, back); status != 200 {
		t.Fatalf("the right return answered %d", status)
	}
	if !e.acc.Status().SignedIn {
		t.Fatal("not signed in after the right return")
	}
	if e.fake.Count("POST /v1/auth/token") != 1 {
		t.Fatalf("token calls: %v", e.fake.Calls())
	}
}

func TestSignInCancelledInTheBrowser(t *testing.T) {
	e := newAccount(t)
	in, _ := e.acc.StartSignIn()
	back, _ := e.fake.Deny(in.URL)
	if status, body := browse(t, back); status != 200 || !strings.Contains(body, "cancelled") {
		t.Fatalf("cancel page %d: %s", status, body)
	}
	st := e.acc.Status()
	if st.SignedIn || st.SignIn != nil || st.Error == nil || !strings.Contains(*st.Error, "cancelled") {
		t.Fatalf("status after cancel: %+v", st)
	}
	if e.fake.Count("POST /v1/auth/token") != 0 {
		t.Fatal("a cancelled sign-in exchanged a code")
	}
	waitClosed(t, back)
}

func TestSignInTimesOut(t *testing.T) {
	e := newAccount(t)
	e.acc.SignInTTL = 100 * time.Millisecond
	in, _ := e.acc.StartSignIn()
	back, _ := e.fake.Approve(in.URL, "acct-1")
	deadline := time.Now().Add(3 * time.Second)
	for e.acc.Status().SignIn != nil {
		if time.Now().After(deadline) {
			t.Fatal("the sign-in did not time out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	st := e.acc.Status()
	if st.SignedIn || st.Error == nil || !strings.Contains(*st.Error, "timed out") {
		t.Fatalf("status after the timeout: %+v", st)
	}
	waitClosed(t, back)
}

func TestSignInWithARefusedCode(t *testing.T) {
	e := newAccount(t)
	in, _ := e.acc.StartSignIn()
	back, _ := e.fake.Approve(in.URL, "acct-1")
	e.fake.Fail("POST /v1/auth/token", 400, "invalid_grant")
	if status, body := browse(t, back); status != http.StatusBadGateway || !strings.Contains(body, "failed") {
		t.Fatalf("failure page %d: %s", status, body)
	}
	st := e.acc.Status()
	if st.SignedIn || st.Error == nil || !strings.Contains(*st.Error, "invalid_grant") {
		t.Fatalf("status after a refused code: %+v", st)
	}
}

func TestCancelAndRestartReplaceTheWaitingSignIn(t *testing.T) {
	e := newAccount(t)
	first, _ := e.acc.StartSignIn()
	firstBack, _ := e.fake.Approve(first.URL, "acct-1")
	second, err := e.acc.StartSignIn()
	if err != nil {
		t.Fatal(err)
	}
	waitClosed(t, firstBack)
	e.acc.CancelSignIn()
	secondBack, _ := e.fake.Approve(second.URL, "acct-1")
	waitClosed(t, secondBack)
	if st := e.acc.Status(); st.SignIn != nil || st.SignedIn {
		t.Fatalf("after cancel: %+v", st)
	}
}

func TestStartSignInWhileSignedInIsRefused(t *testing.T) {
	e := newAccount(t)
	e.signIn("acct-1")
	if _, err := e.acc.StartSignIn(); !apperr.Is(err, apperr.CodeRelaySignedIn) {
		t.Fatalf("got %v, want relay_signed_in", err)
	}
}

func TestSignOutRevokesAndDeletesTheCredential(t *testing.T) {
	e := newAccount(t)
	e.signIn("acct-1")
	if err := e.acc.SetTunnelEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err := e.acc.SetHost("host-1", "abc.loop-now.test"); err != nil {
		t.Fatal(err)
	}
	cred := e.acc.Credential()
	clientID := e.acc.ClientID()
	e.acc.SignOut(context.Background())
	if !e.fake.Revoked(cred) {
		t.Fatal("the relay did not revoke the credential")
	}
	f := readFile(t, e.acc.Path)
	if f.Credential != "" || f.AccountID != "" || f.SignedInAt != nil || f.TunnelEnabled || f.ClientID != clientID || f.HostID != "host-1" {
		t.Fatalf("file after sign-out: %+v", f)
	}
	if e.acc.Status().SignedIn {
		t.Fatal("still signed in")
	}
}

func TestSignOutWhenTheRelayIsDownStillForgets(t *testing.T) {
	e := newAccount(t)
	e.signIn("acct-1")
	e.fake.Close()
	e.acc.SignOut(context.Background())
	if f := readFile(t, e.acc.Path); f.Credential != "" {
		t.Fatal("the credential was kept")
	}
}

func TestForgetOnlyTheRefusedCredential(t *testing.T) {
	e := newAccount(t)
	e.signIn("acct-1")
	<-e.changes
	_ = e.acc.SetTunnelEnabled(true)
	e.acc.Forget("lnh_some-older-credential")
	if !e.acc.Status().SignedIn {
		t.Fatal("an older credential's refusal signed the newer one out")
	}
	e.acc.Forget(e.acc.Credential())
	st := e.acc.Status()
	if st.SignedIn || st.Error == nil || !e.acc.TunnelEnabled() {
		t.Fatalf("after Forget (the switch stays on): %+v", st)
	}
	select {
	case <-e.changes:
	case <-time.After(time.Second):
		t.Fatal("OnChange was not called after Forget")
	}
}

func TestAFileOfAnotherRelayReadsAsSignedOut(t *testing.T) {
	e := newAccount(t)
	e.signIn("acct-1")
	_ = e.acc.SetHost("host-1", "abc.loop-now.test")
	clientID := e.acc.ClientID()
	other := &relay.Account{Client: &relay.Client{BaseURL: "https://relay.example.test"}, Path: e.acc.Path}
	if err := other.Load(); err != nil {
		t.Fatal(err)
	}
	if other.Status().SignedIn || other.Credential() != "" || other.ClientID() != clientID {
		t.Fatalf("another relay sees: %+v", other.Status())
	}
	if id, _ := other.Host(); id != "" {
		t.Fatal("another relay sees the host")
	}
}

func TestLoadMakesTheFilePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes")
	}
	e := newAccount(t)
	_ = os.Chmod(e.acc.Path, 0o644)
	if err := e.acc.Load(); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(e.acc.Path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
}

func TestChallengeOfIsS256(t *testing.T) {
	// RFC 7636 appendix B.
	if got := relay.ChallengeOf("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("got %s", got)
	}
}
