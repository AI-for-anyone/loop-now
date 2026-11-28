package tunnel_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/cloudflared"
	"loop-now/internal/server/relay"
	"loop-now/internal/server/relay/relaytest"
	"loop-now/internal/server/remoteaccess"
	"loop-now/internal/server/tunnel"
)

// fakeDoor is a remote door that listens on a pretend port.
type fakeDoor struct {
	mu      sync.Mutex
	enabled bool
	port    int
}

func (d *fakeDoor) State() remoteaccess.State {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.enabled {
		return remoteaccess.State{State: remoteaccess.StateOff}
	}
	u := fmt.Sprintf("http://127.0.0.1:%d", d.port)
	return remoteaccess.State{Enabled: true, State: remoteaccess.StateListening, URL: &u}
}

func (d *fakeDoor) Apply(_ context.Context, enabled bool, _ *int) (remoteaccess.State, error) {
	d.mu.Lock()
	d.enabled = enabled
	d.mu.Unlock()
	return d.State(), nil
}

func (d *fakeDoor) move(port int) remoteaccess.State {
	d.mu.Lock()
	d.port = port
	d.mu.Unlock()
	return d.State()
}

// fakeInstaller hands out the test binary as cloudflared.
type fakeInstaller struct {
	err   atomic.Pointer[error]
	calls atomic.Int32
	// first runs at the first Ensure, after the tunnel was brought up and before cloudflared starts.
	first func()
}

func (i *fakeInstaller) Ensure(_ context.Context, progress cloudflared.Progress) (cloudflared.Binary, error) {
	if i.calls.Add(1) == 1 && i.first != nil {
		i.first()
	}
	if e := i.err.Load(); e != nil {
		return cloudflared.Binary{}, *e
	}
	if progress != nil {
		progress(10, 100)
	}
	return cloudflared.Binary{Path: os.Args[0], Version: cloudflared.Version, Source: cloudflared.SourceDownloaded}, nil
}

type env struct {
	t     *testing.T
	fake  *relaytest.Fake
	acc   *relay.Account
	door  *fakeDoor
	inst  *fakeInstaller
	m     *tunnel.Manager
	dir   string // the stand-in cloudflared's folder
	path  string // the account file
	stops []func()
}

func newEnv(t *testing.T) *env {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in cloudflared stops on SIGTERM")
	}
	dir := t.TempDir()
	t.Setenv(fakeDirEnv, dir)
	e := &env{t: t, fake: relaytest.New(t), door: &fakeDoor{port: 53158}, inst: &fakeInstaller{}, dir: dir, path: filepath.Join(t.TempDir(), "host.json")}
	e.m = e.manager()
	return e
}

// manager makes a Manager over the env's account file, as a server start does.
func (e *env) manager() *tunnel.Manager {
	acc := &relay.Account{Client: &relay.Client{BaseURL: e.fake.URL}, Path: e.path, Log: slog.New(slog.DiscardHandler)}
	if err := acc.Load(); err != nil {
		e.t.Fatal(err)
	}
	m := &tunnel.Manager{Account: acc, Client: acc.Client, Door: e.door, Installer: e.inst, HostName: func() string { return "Studio Mac" },
		Log: slog.New(slog.DiscardHandler), BackoffBase: 20 * time.Millisecond, BackoffMax: 100 * time.Millisecond, ReadyInterval: 30 * time.Millisecond,
		NotReadyRestart: 5 * time.Second, StableUptime: time.Hour}
	acc.OnChange = m.AccountChanged
	e.acc = acc
	e.t.Cleanup(func() { m.Close(); acc.Close() })
	return m
}

func (e *env) signIn(account string) {
	e.t.Helper()
	in, err := e.acc.StartSignIn()
	if err != nil {
		e.t.Fatal(err)
	}
	back, err := e.fake.Approve(in.URL, account)
	if err != nil {
		e.t.Fatal(err)
	}
	resp, err := http.Get(back)
	if err != nil {
		e.t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if !e.acc.Status().SignedIn {
		e.t.Fatal("not signed in")
	}
}

func (e *env) mode(m string) {
	if err := os.WriteFile(filepath.Join(e.dir, "mode"), []byte(m), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) starts() []fakeStart {
	f, err := os.Open(filepath.Join(e.dir, "starts"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []fakeStart
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var s fakeStart
		if json.Unmarshal(sc.Bytes(), &s) == nil {
			out = append(out, s)
		}
	}
	return out
}

func (e *env) stopped(pid int) bool {
	_, err := os.Stat(filepath.Join(e.dir, fmt.Sprintf("stopped-%d", pid)))
	return err == nil
}

// wait waits until cond holds for the Manager's state.
func (e *env) wait(what string, cond func(tunnel.State) bool) tunnel.State {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := e.m.State()
		if cond(st) {
			return st
		}
		if time.Now().After(deadline) {
			b, _ := json.Marshal(st)
			e.t.Fatalf("waited for %s; state %s", what, b)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func online(st tunnel.State) bool { return st.State == tunnel.StateOnline }

func failedWith(code string) func(tunnel.State) bool {
	return func(st tunnel.State) bool {
		return st.State == tunnel.StateFailed && st.ErrorCode != nil && *st.ErrorCode == code
	}
}

func TestOnRegistersBringsTheTunnelUpAndGoesOnline(t *testing.T) {
	e := newEnv(t)
	e.signIn("acct-1")
	st, err := e.m.SetEnabled(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Enabled || st.State == tunnel.StateOff {
		t.Fatalf("right after turning on: %+v", st)
	}
	if !e.door.State().Enabled {
		t.Fatal("turning the tunnel on did not turn the remote door on")
	}
	st = e.wait("online", online)
	hosts := e.fake.Hosts()
	if len(hosts) != 1 || hosts[0].RemotePort != 53158 || hosts[0].Name != "Studio Mac" || hosts[0].ClientID != e.acc.ClientID() {
		t.Fatalf("relay hosts: %+v", hosts)
	}
	if st.Hostname == nil || *st.Hostname != hosts[0].Hostname || st.URL == nil || *st.URL != "https://"+hosts[0].Hostname || st.Error != nil || st.Cloudflared == nil {
		t.Fatalf("online state: %+v", st)
	}
	if u, ok := e.m.OnlineURL(); !ok || u != *st.URL || e.m.PublicOrigin() != *st.URL {
		t.Fatalf("OnlineURL %q %v, PublicOrigin %q", u, ok, e.m.PublicOrigin())
	}
	starts := e.starts()
	if len(starts) != 1 {
		t.Fatalf("starts: %+v", starts)
	}
	if starts[0].Token != relaytest.TunnelToken(hosts[0].TunnelID) || !slices.Equal(starts[0].Args, tunnel.Args) {
		t.Fatalf("cloudflared ran with %+v", starts[0])
	}
	for _, a := range starts[0].Args {
		if strings.Contains(a, "token-for-") {
			t.Fatal("the run token is on the command line")
		}
	}
	if id, hostname := e.acc.Host(); id != hosts[0].ID || hostname != hosts[0].Hostname || !e.acc.TunnelEnabled() {
		t.Fatalf("the account file keeps host %q %q, switch %v", id, hostname, e.acc.TunnelEnabled())
	}
}

func TestOnNeedsASignedInAccount(t *testing.T) {
	e := newEnv(t)
	if _, err := e.m.SetEnabled(context.Background(), true); !apperr.Is(err, apperr.CodeRelaySignedOut) {
		t.Fatalf("got %v", err)
	}
	if e.door.State().Enabled || e.acc.TunnelEnabled() {
		t.Fatal("a refused switch changed something")
	}
}

func TestOffStopsCloudflaredAndKeepsTheAddress(t *testing.T) {
	e := newEnv(t)
	e.signIn("acct-1")
	_, _ = e.m.SetEnabled(context.Background(), true)
	e.wait("online", online)
	st, err := e.m.SetEnabled(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if st.Enabled || st.State != tunnel.StateOff || st.URL == nil {
		t.Fatalf("off: %+v", st)
	}
	if pid := e.starts()[0].PID; !e.stopped(pid) {
		t.Fatal("cloudflared was not stopped with SIGTERM")
	}
	if !e.door.State().Enabled {
		t.Fatal("turning the tunnel off closed the remote door")
	}
	if len(e.fake.Hosts()) != 1 {
		t.Fatal("turning off released the host")
	}
}

func TestCloudflaredThatExitsIsRestartedAfterBringingTheTunnelUpAgain(t *testing.T) {
	e := newEnv(t)
	e.signIn("acct-1")
	e.mode("exit")
	_, _ = e.m.SetEnabled(context.Background(), true)
	st := e.wait("a failure with a retry", failedWith(tunnel.ErrCloudflaredFailed))
	if st.RetryAt == nil || st.Error == nil || !strings.Contains(*st.Error, "something broke") {
		t.Fatalf("failed state: %+v", st)
	}
	e.mode("ok")
	e.wait("online", online)
	if n := len(e.starts()); n < 2 {
		t.Fatalf("starts: %d", n)
	}
	if n := e.fake.Count("POST /v1/hosts/{id}/tunnel"); n != len(e.starts()) {
		t.Fatalf("every start brings the tunnel up first: %d bring-ups, %d starts", n, len(e.starts()))
	}
	if n := e.fake.Count("POST /v1/hosts"); n != 1 {
		t.Fatalf("registered %d times", n)
	}
}

func TestARecycledTunnelIsBroughtUpAgain(t *testing.T) {
	e := newEnv(t)
	e.signIn("acct-1")
	// The relay recycles the tunnel right after it was brought up; cloudflared is then refused by Cloudflare.
	e.inst.first = func() { id, _ := e.acc.Host(); e.fake.Recycle(id) }
	e.mode("rejected-once")
	_, _ = e.m.SetEnabled(context.Background(), true)
	e.wait("online", online)
	starts := e.starts()
	if len(starts) != 2 || !e.stopped(starts[0].PID) {
		t.Fatalf("the refused cloudflared was not stopped and started again: %+v", starts)
	}
	if starts[1].Token == starts[0].Token || e.fake.Hosts()[0].Tunnels != 2 {
		t.Fatalf("not restarted on a new tunnel: %+v", starts)
	}
}

func TestTheSwitchBringsTheTunnelBackAtServerStart(t *testing.T) {
	e := newEnv(t)
	e.signIn("acct-1")
	_, _ = e.m.SetEnabled(context.Background(), true)
	e.wait("online", online)
	e.m.Close()
	if !e.stopped(e.starts()[0].PID) {
		t.Fatal("Close did not stop cloudflared")
	}
	e.m = e.manager()
	if st := e.m.State(); !st.Enabled || st.State != tunnel.StateOff {
		t.Fatalf("before Start: %+v", st)
	}
	e.m.Start()
	e.wait("online again", online)
	if e.fake.Count("POST /v1/hosts") != 1 || len(e.fake.Hosts()) != 1 {
		t.Fatal("the restart registered again")
	}
}

func TestReleaseDeletesTheHostAndForgetsIt(t *testing.T) {
	e := newEnv(t)
	e.signIn("acct-1")
	_, _ = e.m.SetEnabled(context.Background(), true)
	e.wait("online", online)
	if err := e.m.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(e.fake.Hosts()) != 0 {
		t.Fatal("the relay still has the host")
	}
	st := e.m.State()
	if st.Enabled || st.State != tunnel.StateOff || st.Hostname != nil || e.m.PublicOrigin() != "" {
		t.Fatalf("after release: %+v", st)
	}
	if !e.stopped(e.starts()[0].PID) {
		t.Fatal("release did not stop cloudflared")
	}
	if err := e.m.Release(context.Background()); err != nil {
		t.Fatalf("releasing nothing: %v", err)
	}
	// On again: a new address.
	_, _ = e.m.SetEnabled(context.Background(), true)
	e.wait("online", online)
	if e.fake.Count("POST /v1/hosts") != 2 {
		t.Fatal("not registered again")
	}
}

func TestReleaseFailuresKeepTheAddress(t *testing.T) {
	e := newEnv(t)
	e.signIn("acct-1")
	_, _ = e.m.SetEnabled(context.Background(), true)
	e.wait("online", online)
	e.fake.Fail("DELETE /v1/hosts/{id}", 502, "upstream_error")
	if err := e.m.Release(context.Background()); !apperr.Is(err, apperr.CodeRelayFailed) {
		t.Fatalf("got %v", err)
	}
	if id, _ := e.acc.Host(); id == "" {
		t.Fatal("a failed release forgot the host")
	}
	e.acc.SignOut(context.Background())
	if err := e.m.Release(context.Background()); !apperr.Is(err, apperr.CodeRelaySignedOut) {
		t.Fatalf("signed out: %v", err)
	}
}

func TestTheHostLimitWaitsForThePerson(t *testing.T) {
	e := newEnv(t)
	e.signIn("acct-1")
	e.fake.HostLimit = 1
	e.fake.AddHost("acct-1", "another-computer-0001")
	_, _ = e.m.SetEnabled(context.Background(), true)
	st := e.wait("host limit", failedWith(tunnel.ErrHostLimitReached))
	if st.RetryAt != nil || !strings.Contains(*st.Error, "(1)") {
		t.Fatalf("host limit: %+v", st)
	}
	time.Sleep(150 * time.Millisecond)
	if n := e.fake.Count("POST /v1/hosts"); n != 1 {
		t.Fatalf("retried a host limit: %d registers", n)
	}
}

func TestARefusedCredentialSignsOutAndTheTunnelComesBackAfterSigningIn(t *testing.T) {
	e := newEnv(t)
	e.signIn("acct-1")
	e.mode("exit")
	_, _ = e.m.SetEnabled(context.Background(), true)
	e.wait("a failure", failedWith(tunnel.ErrCloudflaredFailed))
	e.fake.RevokeAll("acct-1")
	st := e.wait("signed out", failedWith(tunnel.ErrSignedOut))
	if st.RetryAt != nil || !st.Enabled || e.acc.Status().SignedIn {
		t.Fatalf("after the refusal: %+v, signed in %v", st, e.acc.Status().SignedIn)
	}
	e.mode("ok")
	e.signIn("acct-1")
	e.wait("online after signing in again", online)
	if e.fake.Count("POST /v1/hosts") != 1 {
		t.Fatal("the same account found another host")
	}
}

func TestAnUnknownHostIsRegisteredAgain(t *testing.T) {
	e := newEnv(t)
	e.signIn("acct-1")
	if err := e.acc.SetHost("00000000-0000-4000-8000-999999999999", "gone.loop-now.test"); err != nil {
		t.Fatal(err)
	}
	_, _ = e.m.SetEnabled(context.Background(), true)
	st := e.wait("online", online)
	if *st.Hostname == "gone.loop-now.test" || len(e.fake.Hosts()) != 1 {
		t.Fatalf("not registered again: %+v", st)
	}
}

func TestANewDoorPortBringsTheTunnelUpAgain(t *testing.T) {
	e := newEnv(t)
	e.signIn("acct-1")
	_, _ = e.m.SetEnabled(context.Background(), true)
	e.wait("online", online)
	e.m.DoorChanged(e.door.move(53999))
	e.wait("the tunnel at the new port", func(st tunnel.State) bool { return online(st) && e.fake.Hosts()[0].RemotePort == 53999 })
	if !e.stopped(e.starts()[0].PID) {
		t.Fatal("the old cloudflared was not stopped")
	}
}

func TestTurningTheDoorOffTurnsTheTunnelOff(t *testing.T) {
	e := newEnv(t)
	e.signIn("acct-1")
	_, _ = e.m.SetEnabled(context.Background(), true)
	e.wait("online", online)
	st, _ := e.door.Apply(context.Background(), false, nil)
	e.m.DoorChanged(st)
	if s := e.m.State(); s.Enabled || s.State != tunnel.StateOff || !e.stopped(e.starts()[0].PID) {
		t.Fatalf("after the door closed: %+v", s)
	}
}

func TestNoCloudflaredForThisPlatformWaitsForThePerson(t *testing.T) {
	e := newEnv(t)
	e.signIn("acct-1")
	err := fmt.Errorf("%w: none for plan9/386", cloudflared.ErrUnsupported)
	e.inst.err.Store(&err)
	_, _ = e.m.SetEnabled(context.Background(), true)
	st := e.wait("unavailable", failedWith(tunnel.ErrCloudflaredUnavailable))
	if st.RetryAt != nil || !strings.Contains(*st.Error, "plan9") {
		t.Fatalf("got %+v", st)
	}
}

func TestADownloadFailureIsRetriedLater(t *testing.T) {
	e := newEnv(t)
	e.signIn("acct-1")
	err := errors.New("the cloudflared download does not match its pinned SHA-256; refused")
	e.inst.err.Store(&err)
	_, _ = e.m.SetEnabled(context.Background(), true)
	st := e.wait("unavailable", failedWith(tunnel.ErrCloudflaredUnavailable))
	if st.RetryAt == nil || time.Until(*st.RetryAt) < 20*time.Second {
		t.Fatalf("a download failure waits at least 30 s: %+v", st)
	}
}

func TestSignOutStopsTheTunnelAndTurnsItOff(t *testing.T) {
	e := newEnv(t)
	e.signIn("acct-1")
	_, _ = e.m.SetEnabled(context.Background(), true)
	e.wait("online", online)
	cred := e.acc.Credential()
	e.m.SignOut(context.Background())
	if st := e.m.State(); st.Enabled || st.State != tunnel.StateOff || !e.stopped(e.starts()[0].PID) || !e.fake.Revoked(cred) {
		t.Fatalf("after sign-out: %+v", st)
	}
	if len(e.fake.Hosts()) != 1 {
		t.Fatal("signing out released the host")
	}
}

func TestTheRelayBeingDownIsRetried(t *testing.T) {
	e := newEnv(t)
	e.signIn("acct-1")
	e.fake.Fail("POST /v1/hosts", 503, "not_configured")
	_, _ = e.m.SetEnabled(context.Background(), true)
	e.wait("online after the retry", online)
	if e.fake.Count("POST /v1/hosts") != 2 {
		t.Fatalf("calls: %v", e.fake.Calls())
	}
}
