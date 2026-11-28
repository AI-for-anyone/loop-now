package httpapi_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/cloudflared"
	"loop-now/internal/server/relay"
	"loop-now/internal/server/relay/relaytest"
	"loop-now/internal/server/remoteaccess"
	"loop-now/internal/server/tunnel"
)

// stubRelay is a tunnel at a fixed public address, online or not.
type stubRelay struct {
	origin string
	online bool
	door   []remoteaccess.State
}

func (s *stubRelay) AccountStatus() relay.Status                            { return relay.Status{RelayURL: relay.DefaultURL} }
func (s *stubRelay) StartSignIn() (relay.SignIn, error)                     { return relay.SignIn{}, nil }
func (s *stubRelay) CancelSignIn()                                          {}
func (s *stubRelay) SignOut(context.Context)                                {}
func (s *stubRelay) State() tunnel.State                                    { return tunnel.State{State: tunnel.StateOff} }
func (s *stubRelay) SetEnabled(context.Context, bool) (tunnel.State, error) { return s.State(), nil }
func (s *stubRelay) Release(context.Context) error                          { return nil }
func (s *stubRelay) DoorChanged(d remoteaccess.State)                       { s.door = append(s.door, d) }
func (s *stubRelay) PublicOrigin() string                                   { return s.origin }
func (s *stubRelay) OnlineURL() (string, bool)                              { return s.origin, s.online && s.origin != "" }

const publicOrigin = "https://k3v9q2x7m4tb.loop-now.test"

// TestRemoteDoorOriginComesFromTheRelayNotTheHost: through a tunnel the page's origin is https://<hostname> from the
// relay, whatever Host the tunnel passes on; a forged Host does not make a foreign origin the door's own.
func TestRemoteDoorOriginComesFromTheRelayNotTheHost(t *testing.T) {
	r := newRemoteEnv(t)
	r.api.Relay = &stubRelay{origin: publicOrigin}
	host := strings.TrimPrefix(publicOrigin, "https://")
	for _, c := range []struct {
		name   string
		hdr    []string
		status int
	}{
		{"the tunnel keeps the public Host", []string{"Origin", publicOrigin, "Host", host}, 201},
		{"the tunnel passes the loopback Host", []string{"Origin", publicOrigin}, 201},
		{"case does not matter", []string{"Origin", strings.ToUpper(publicOrigin)}, 201},
		{"the door's own loopback page", []string{"Origin", r.base}, 201},
		{"a foreign site with a matching Host", []string{"Origin", "https://evil.example", "Host", "evil.example"}, 403},
		{"https on the loopback address", []string{"Origin", "https://" + strings.TrimPrefix(r.base, "http://")}, 403},
		{"a sibling subdomain", []string{"Origin", "https://other.loop-now.test"}, 403},
		{"http on the public name", []string{"Origin", "http://" + host}, 403},
	} {
		status, body, _ := r.phone().do("POST", "/device-sessions", map[string]any{"code": r.newCode()}, c.hdr...)
		if status != c.status {
			t.Errorf("%s: %d %s", c.name, status, body)
		}
	}
	// Without a tunnel address, the public name is not the door's own.
	r.api.Relay = &stubRelay{}
	if status, _, _ := r.phone().do("POST", "/device-sessions", map[string]any{"code": r.newCode()}, "Origin", publicOrigin); status != 403 {
		t.Errorf("no tunnel address: %d", status)
	}
}

func TestPairingLinkUsesThePublicAddressWhileTheTunnelIsOnline(t *testing.T) {
	r := newRemoteEnv(t)
	stub := &stubRelay{origin: publicOrigin}
	r.api.Relay = stub
	p := decode[apitypes.DevicePairing](t, r.expect(201, "POST", "/device-pairings", nil))
	if !strings.HasPrefix(p.URL, r.base+"/pair#") {
		t.Fatalf("offline tunnel: %s", p.URL)
	}
	stub.online = true
	p = decode[apitypes.DevicePairing](t, r.expect(201, "POST", "/device-pairings", nil))
	if p.URL != publicOrigin+"/pair#"+p.Code {
		t.Fatalf("online tunnel: %s", p.URL)
	}
}

// TestPairingStatusNamesThisCodesDevice: the add-device dialog asks about its own code, so another device pairing
// meanwhile does not show up as "paired", and a used code can be hidden.
func TestPairingStatusNamesThisCodesDevice(t *testing.T) {
	r := newRemoteEnv(t)
	mine := decode[apitypes.DevicePairing](t, r.expect(201, "POST", "/device-pairings", nil))
	st := decode[apitypes.DevicePairingStatus](t, r.expect(200, "GET", "/device-pairings/"+mine.ID.String(), nil))
	if st.State != "open" || st.Device != nil || st.ID != mine.ID {
		t.Fatalf("open: %+v", st)
	}
	r.phone().pair() // another code, another device
	if st := decode[apitypes.DevicePairingStatus](t, r.expect(200, "GET", "/device-pairings/"+mine.ID.String(), nil)); st.State != "open" {
		t.Fatalf("another device's pairing changed this code: %+v", st)
	}
	ph := r.phone()
	ph.ua = "Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Mobile Safari/537.36"
	d := decode[apitypes.Device](t, ph.expect(201, "POST", "/device-sessions", map[string]any{"code": mine.Code}))
	st = decode[apitypes.DevicePairingStatus](t, r.expect(200, "GET", "/device-pairings/"+mine.ID.String(), nil))
	if st.State != "used" || st.Device == nil || st.Device.ID != d.ID || st.Device.Name != "Android · Chrome" {
		t.Fatalf("used: %+v", st)
	}
	r.expect(404, "GET", "/device-pairings/01a221e1-0000-7000-8000-000000000000", nil)
	// Only the person at the computer may ask.
	if status, _, _ := ph.do("GET", "/device-pairings/"+mine.ID.String(), nil); status != 403 {
		t.Fatalf("at the remote door: %d", status)
	}
}

func TestTurningTheDoorOffIsPassedToTheTunnel(t *testing.T) {
	r := newRemoteEnv(t)
	stub := &stubRelay{}
	r.api.Relay = stub
	r.expect(200, "PUT", "/remote-access", map[string]any{"enabled": false})
	if len(stub.door) != 1 || stub.door[0].Enabled {
		t.Fatalf("door changes: %+v", stub.door)
	}
}

type unsupportedInstaller struct{}

func (unsupportedInstaller) Ensure(context.Context, cloudflared.Progress) (cloudflared.Binary, error) {
	return cloudflared.Binary{}, fmt.Errorf("%w: none here", cloudflared.ErrUnsupported)
}

// TestRelayAccountAndTunnelEndpoints walks the account and the switch through the console API against a fake relay:
// every answer is checked against the contract.
func TestRelayAccountAndTunnelEndpoints(t *testing.T) {
	r := newRemoteEnv(t)
	fake := relaytest.New(t)
	acc := &relay.Account{Client: &relay.Client{BaseURL: fake.URL}, Path: filepath.Join(t.TempDir(), "relay", "host.json"), HostName: func() string { return "Studio Mac" }}
	if err := acc.Load(); err != nil {
		t.Fatal(err)
	}
	m := &tunnel.Manager{Account: acc, Client: acc.Client, Door: r.door, Installer: unsupportedInstaller{}, Log: slog.New(slog.DiscardHandler)}
	acc.OnChange = m.AccountChanged
	t.Cleanup(func() { m.Close(); acc.Close() })
	r.api.Relay = m

	st := decode[relay.Status](t, r.expect(200, "GET", "/relay-account", nil))
	if st.SignedIn || st.RelayURL != fake.URL || st.SignIn != nil {
		t.Fatalf("signed out: %+v", st)
	}
	r.expect(409, "PUT", "/tunnel", map[string]any{"enabled": true})
	r.expect(422, "PUT", "/tunnel", map[string]any{}, "X-Contract-Invalid-Request", "1")
	off := decode[tunnel.State](t, r.expect(200, "GET", "/tunnel", nil))
	if off.Enabled || off.State != "off" {
		t.Fatalf("tunnel before: %+v", off)
	}

	in := decode[relay.SignIn](t, r.expect(201, "POST", "/relay-account/sign-in", nil))
	if st := decode[relay.Status](t, r.expect(200, "GET", "/relay-account", nil)); st.SignIn == nil || st.SignIn.URL != in.URL {
		t.Fatalf("waiting: %+v", st)
	}
	back, err := fake.Approve(in.URL, "acct-1")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(back)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	st = decode[relay.Status](t, r.expect(200, "GET", "/relay-account", nil))
	if !st.SignedIn || st.AccountID == nil || *st.AccountID != "acct-1" || st.SignIn != nil {
		t.Fatalf("signed in: %+v", st)
	}
	r.expect(409, "POST", "/relay-account/sign-in", nil)

	// Off by default: on turns the remote door on too (it is on here already) and starts in the background.
	r.expect(200, "PUT", "/remote-access", map[string]any{"enabled": false})
	on := decode[tunnel.State](t, r.expect(200, "PUT", "/tunnel", map[string]any{"enabled": true}))
	if !on.Enabled {
		t.Fatalf("on: %+v", on)
	}
	if door := r.door.State(); door.State != remoteaccess.StateListening {
		t.Fatalf("the remote door: %+v", door)
	}
	var failed tunnel.State
	deadline := time.Now().Add(5 * time.Second)
	for {
		failed = decode[tunnel.State](t, r.expect(200, "GET", "/tunnel", nil))
		if failed.State == "failed" || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if failed.ErrorCode == nil || *failed.ErrorCode != "cloudflared_unavailable" || failed.RetryAt != nil || failed.Hostname == nil || failed.URL == nil {
		t.Fatalf("no cloudflared for this platform: %+v", failed)
	}
	if len(fake.Hosts()) != 1 || fake.Hosts()[0].RemotePort == 0 {
		t.Fatalf("relay hosts: %+v", fake.Hosts())
	}

	r.expect(204, "DELETE", "/tunnel/address", nil)
	if len(fake.Hosts()) != 0 {
		t.Fatal("not released")
	}
	released := decode[tunnel.State](t, r.expect(200, "GET", "/tunnel", nil))
	if released.Enabled || released.Hostname != nil || released.State != "off" {
		t.Fatalf("after release: %+v", released)
	}
	r.expect(204, "DELETE", "/relay-account", nil)
	if decode[relay.Status](t, r.expect(200, "GET", "/relay-account", nil)).SignedIn || fake.Credentials() != 0 {
		t.Fatal("still signed in")
	}
	r.expect(204, "DELETE", "/relay-account", nil)
	r.expect(201, "POST", "/relay-account/sign-in", nil)
	r.expect(204, "DELETE", "/relay-account/sign-in", nil)
	if decode[relay.Status](t, r.expect(200, "GET", "/relay-account", nil)).SignIn != nil {
		t.Fatal("the cancelled sign-in still waits")
	}
}

func TestRelayEndpointsWithoutARelay(t *testing.T) {
	e := newEnv(t)
	if st := decode[relay.Status](t, e.expect(200, "GET", "/relay-account", nil)); st.SignedIn || st.RelayURL != relay.DefaultURL {
		t.Fatalf("%+v", st)
	}
	e.expect(200, "GET", "/tunnel", nil)
	e.expect(409, "PUT", "/tunnel", map[string]any{"enabled": true})
	e.expect(200, "PUT", "/tunnel", map[string]any{"enabled": false})
	e.expect(409, "POST", "/relay-account/sign-in", nil)
	e.expect(204, "DELETE", "/tunnel/address", nil)
	e.expect(204, "DELETE", "/relay-account", nil)
}
