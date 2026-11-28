package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/contracttest"
	"loop-now/internal/server/devices"
	"loop-now/internal/server/httpapi"
	"loop-now/internal/server/remoteaccess"
	"loop-now/internal/server/webui"
)

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *testClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// remoteEnv is the remote door of an env, turned on through the local door's PUT /remote-access.
type remoteEnv struct {
	*env
	base  string // the remote door's own address, such as http://127.0.0.1:53124
	clock *testClock
	door  *remoteaccess.Door
	v     *contracttest.Validator
}

const phoneUA = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1"

func newRemoteEnv(t *testing.T) *remoteEnv {
	t.Helper()
	e := newEnv(t)
	e.api.O.Console = webui.Handler(fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><title>console</title>")},
		"manifest.webmanifest": {Data: []byte(`{"name":"Loop Now"}`)},
	})
	c := &testClock{t: time.Now().UTC().Truncate(time.Microsecond)}
	e.api.Devices = &devices.Service{Store: e.st, Now: c.now}
	door := &remoteaccess.Door{Store: e.st, Handler: e.api.RemoteHandler(), Log: slog.New(slog.DiscardHandler)}
	t.Cleanup(func() { _ = door.Close() })
	if err := door.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.api.RemoteAccess = door
	off := decode[remoteaccess.State](t, e.expect(200, "GET", "/remote-access", nil))
	if off.Enabled || off.State != "off" || off.URL != nil {
		t.Fatalf("the remote door starts off: %+v", off)
	}
	on := decode[remoteaccess.State](t, e.expect(200, "PUT", "/remote-access", map[string]any{"enabled": true}))
	if on.State != "listening" || on.URL == nil || !strings.HasPrefix(*on.URL, "http://127.0.0.1:") {
		t.Fatalf("turned on: %+v", on)
	}
	return &remoteEnv{env: e, base: *on.URL, clock: c, door: door, v: contracttest.New(t, *on.URL)}
}

// phone is a browser at the remote door, with its own cookies.
type phone struct {
	r      *remoteEnv
	client *http.Client
	ua     string
}

func (r *remoteEnv) phone() *phone {
	jar, _ := cookiejar.New(nil)
	c := r.v.Client()
	c.Jar = jar
	return &phone{r: r, client: c, ua: phoneUA}
}

// do sends a request to the remote door's /api/v1 (path) or to another path (rawPath starting with "!"). Writes carry
// the remote door's own Origin unless hdr gives one ("Origin", "" sends none).
func (p *phone) do(method, path string, body any, hdr ...string) (int, []byte, http.Header) {
	p.r.t.Helper()
	var rd io.Reader
	if body != nil {
		j, _ := json.Marshal(body)
		rd = bytes.NewReader(j)
	}
	target := p.r.base + "/api/v1" + path
	if raw, ok := strings.CutPrefix(path, "!"); ok {
		target = p.r.base + raw
	}
	req, _ := http.NewRequest(method, target, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", p.ua)
	if method != "GET" && method != "HEAD" {
		req.Header.Set("Origin", p.r.base)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		switch {
		case hdr[i] == "Host":
			req.Host = hdr[i+1]
		case hdr[i+1] == "":
			req.Header.Del(hdr[i])
		default:
			req.Header.Set(hdr[i], hdr[i+1])
		}
	}
	resp, err := p.client.Do(req)
	if err != nil {
		p.r.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out, resp.Header
}

func (p *phone) expect(status int, method, path string, body any, hdr ...string) []byte {
	p.r.t.Helper()
	got, out, _ := p.do(method, path, body, hdr...)
	if got != status {
		p.r.t.Fatalf("remote door %s %s: want %d, got %d: %s", method, path, status, got, out)
	}
	return out
}

func errorCode(t *testing.T, b []byte) string {
	t.Helper()
	return decode[apitypes.ErrorBody](t, b).Error.Code
}

// pair makes a code at the local door and pairs the phone with it.
func (p *phone) pair() apitypes.Device {
	p.r.t.Helper()
	code := p.r.newCode()
	return decode[apitypes.Device](p.r.t, p.expect(201, "POST", "/device-sessions", map[string]any{"code": code}))
}

func (r *remoteEnv) newCode() string {
	r.t.Helper()
	pairing := decode[apitypes.DevicePairing](r.t, r.expect(201, "POST", "/device-pairings", nil))
	return pairing.Code
}

// TestRemoteDoorRefusesWithoutSession: at the remote door every API route needs a device session, except pairing;
// the local-only routes are refused there even with one; the console's files are served to anyone.
func TestRemoteDoorRefusesWithoutSession(t *testing.T) {
	r := newRemoteEnv(t)
	plain := &http.Client{}
	send := func(method, target string) (int, string) {
		req, _ := http.NewRequest(method, target, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", r.base)
		resp, err := plain.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		code := ""
		var e apitypes.ErrorBody
		if json.Unmarshal(b, &e) == nil {
			code = e.Error.Code
		}
		return resp.StatusCode, code
	}
	param := strings.NewReplacer("{path...}", "a/b.txt")
	for _, rt := range r.api.Routes() {
		path := param.Replace(rt.Pattern)
		for strings.Contains(path, "{") {
			i, j := strings.Index(path, "{"), strings.Index(path, "}")
			path = path[:i] + uuid.NewString() + path[j+1:]
		}
		status, code := send(rt.Method, r.base+"/api/v1"+path)
		switch rt.Access {
		case httpapi.Pairing:
			if status == http.StatusUnauthorized || code == "forbidden" {
				t.Errorf("%s %s (pairing) should need no session: %d %s", rt.Method, rt.Pattern, status, code)
			}
		case httpapi.LocalOnly:
			if status != http.StatusForbidden || code != "forbidden" {
				t.Errorf("%s %s (local) should be refused at the remote door: %d %s", rt.Method, rt.Pattern, status, code)
			}
		default:
			if status != http.StatusUnauthorized || code != "unauthenticated" {
				t.Errorf("%s %s should need a session: %d %s", rt.Method, rt.Pattern, status, code)
			}
		}
	}
	for _, c := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/healthz", 401},
		{"GET", "/api/v1/no-such-endpoint", 401},
		{"POST", "/api/v1/plugins/p/http/x", 401},
		{"GET", "/agent/v1/whoami", 404},
		{"POST", "/hooks/p", 404},
		{"GET", "/", 200},
		{"GET", "/pair", 200},
		{"GET", "/manifest.webmanifest", 200},
	} {
		if status, _ := send(c.method, r.base+c.path); status != c.status {
			t.Errorf("%s %s: want %d, got %d", c.method, c.path, c.status, status)
		}
	}
	if (httpapi.Route{}).Access == httpapi.Pairing {
		t.Fatal("routes must not default to pairing")
	}
}

func TestRemoteDoorPairing(t *testing.T) {
	r := newRemoteEnv(t)
	ph := r.phone()

	// The local door makes a code; the link carries it in the fragment only.
	before := r.clock.now()
	pairing := decode[apitypes.DevicePairing](t, r.expect(201, "POST", "/device-pairings", nil))
	if pairing.URL != r.base+"/pair#"+pairing.Code {
		t.Fatalf("the link is <remote door>/pair#<code>: %q", pairing.URL)
	}
	u, err := url.Parse(pairing.URL)
	if err != nil || u.RawQuery != "" || strings.Contains(u.Path, pairing.Code) || u.Fragment != pairing.Code {
		t.Fatalf("the code is only in the fragment: %q", pairing.URL)
	}
	if !pairing.ExpiresAt.Equal(before.Add(10 * time.Minute)) {
		t.Fatalf("a code lasts 10 minutes: %v", pairing.ExpiresAt)
	}

	// Before pairing the phone is refused; pairing sets the session cookie.
	if b := ph.expect(401, "GET", "/context", nil); errorCode(t, b) != "unauthenticated" {
		t.Fatalf("unpaired: %s", b)
	}
	status, body, hdr := ph.do("POST", "/device-sessions", map[string]any{"code": pairing.Code})
	if status != 201 {
		t.Fatalf("pair: %d %s", status, body)
	}
	dev := decode[apitypes.Device](t, body)
	if dev.Name != "iPhone · Safari" || dev.LastAddress == nil || *dev.LastAddress != "127.0.0.1" {
		t.Fatalf("device: %+v", dev)
	}
	cookie := hdr.Get("Set-Cookie")
	for _, want := range []string{"loop_now_device=lnd_", "Path=/", "HttpOnly", "SameSite=Strict", "Max-Age=34560000"} {
		if !strings.Contains(cookie, want) {
			t.Errorf("the cookie should have %q: %s", want, cookie)
		}
	}
	if strings.Contains(cookie, "Secure") || strings.Contains(cookie, "__Host-") {
		t.Errorf("over plain HTTP the cookie is not Secure: %s", cookie)
	}

	// Paired: the whole console works, as the console operator; the door says remote; no path dialog there.
	ctxBody := decode[map[string]any](t, ph.expect(200, "GET", "/context", nil))
	if ctxBody["door"] != "remote" || ctxBody["pathPicker"] != false {
		t.Fatalf("context at the remote door: %v", ctxBody)
	}
	if who := decode[map[string]any](t, ph.expect(200, "GET", "/whoami", nil)); who["kind"] != "console" {
		t.Fatalf("a paired device acts as the console operator: %v", who)
	}
	created := decode[apitypes.Agent](t, ph.expect(201, "POST", r.agents(), map[string]any{"name": "from-phone", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	if _, _, h := ph.do("GET", "/agents/"+created.ID.String()+"/folder/raw/page.html", nil); h.Get("Content-Security-Policy") != httpapi.RawFileSandbox {
		t.Fatalf("raw file views keep their sandbox at the remote door: %v", h)
	}
	if b := ph.expect(409, "POST", "/path-picks", map[string]any{"kind": "directory"}); errorCode(t, b) != "path_picker_unavailable" {
		t.Fatalf("the path dialog would open on the computer, not the phone: %s", b)
	}

	// The code was used up.
	if b := r.phone().expect(403, "POST", "/device-sessions", map[string]any{"code": pairing.Code}); errorCode(t, b) != "pairing_code_invalid" {
		t.Fatalf("a used code fails: %s", b)
	}

	// Nothing at the remote door manages pairing, devices or the door itself, session or not.
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/device-pairings", nil},
		{"GET", "/devices", nil},
		{"PATCH", "/devices/" + dev.ID.String(), map[string]any{"name": "x"}},
		{"DELETE", "/devices/" + dev.ID.String(), nil},
		{"GET", "/remote-access", nil},
		{"PUT", "/remote-access", map[string]any{"enabled": false}},
	} {
		if b := ph.expect(403, c.method, c.path, c.body); errorCode(t, b) != "forbidden" {
			t.Errorf("%s %s at the remote door: %s", c.method, c.path, b)
		}
	}

	// The local door lists, renames and removes the device; pairing is refused there.
	list := decode[apitypes.DeviceList](t, r.expect(200, "GET", "/devices", nil))
	if len(list.Devices) != 1 || list.Devices[0].ID != dev.ID || !list.Devices[0].ExpiresAt.Equal(list.Devices[0].LastSeenAt.Add(30*24*time.Hour)) {
		t.Fatalf("devices: %+v", list)
	}
	renamed := decode[apitypes.Device](t, r.expect(200, "PATCH", "/devices/"+dev.ID.String(), map[string]any{"name": "  My phone "}))
	if renamed.Name != "My phone" {
		t.Fatalf("renamed: %+v", renamed)
	}
	for _, bad := range []string{" ", strings.Repeat("x", 65), "a\u0007b"} {
		r.expect(422, "PATCH", "/devices/"+dev.ID.String(), map[string]any{"name": bad}, "X-Contract-Invalid-Request", "1")
	}
	r.expect(404, "PATCH", "/devices/"+uuid.NewString(), map[string]any{"name": "x"})
	r.expect(404, "DELETE", "/devices/"+uuid.NewString(), nil)
	if b := r.expect(403, "POST", "/device-sessions", map[string]any{"code": r.newCode()}); errorCode(t, b) != "forbidden" {
		t.Fatalf("pairing happens at the remote door: %s", b)
	}
	if ctxLocal := decode[map[string]any](t, r.expect(200, "GET", "/context", nil)); ctxLocal["door"] != "local" {
		t.Fatalf("the local door needs no login and says local: %v", ctxLocal)
	}
	r.expect(204, "DELETE", "/devices/"+dev.ID.String(), nil)
	if b := ph.expect(401, "GET", "/context", nil); errorCode(t, b) != "unauthenticated" {
		t.Fatalf("a removed device is refused: %s", b)
	}
}

func TestRemoteDoorSessionExpiresAndCodesExpire(t *testing.T) {
	r := newRemoteEnv(t)
	ph := r.phone()
	ph.pair()
	r.clock.add(29 * 24 * time.Hour)
	ph.expect(200, "GET", "/context", nil)
	r.clock.add(29 * 24 * time.Hour)
	ph.expect(200, "GET", "/context", nil) // used 29 days ago: still live
	r.clock.add(30*24*time.Hour + time.Minute)
	ph.expect(401, "GET", "/context", nil)
	if list := decode[apitypes.DeviceList](t, r.expect(200, "GET", "/devices", nil)); len(list.Devices) != 0 {
		t.Fatalf("an expired device is not listed: %+v", list)
	}

	code := r.newCode()
	r.clock.add(10*time.Minute + time.Second)
	if b := r.phone().expect(403, "POST", "/device-sessions", map[string]any{"code": code}); errorCode(t, b) != "pairing_code_invalid" {
		t.Fatalf("an expired code fails: %s", b)
	}
}

func TestRemoteDoorPairingRateLimit(t *testing.T) {
	r := newRemoteEnv(t)
	code := r.newCode()
	for range 10 {
		r.phone().expect(403, "POST", "/device-sessions", map[string]any{"code": "wrong"})
	}
	if b := r.phone().expect(429, "POST", "/device-sessions", map[string]any{"code": code}); errorCode(t, b) != "rate_limited" {
		t.Fatalf("too many wrong codes: %s", b)
	}
	r.clock.add(time.Minute + time.Second)
	r.phone().expect(201, "POST", "/device-sessions", map[string]any{"code": code})
}

func TestRemoteDoorSecureCookie(t *testing.T) {
	r := newRemoteEnv(t)
	r.api.Relay = &stubRelay{origin: publicOrigin}
	for _, hdr := range [][]string{{"Origin", publicOrigin}, {"X-Forwarded-Proto", "https"}} {
		ph := r.phone()
		status, body, h := ph.do("POST", "/device-sessions", map[string]any{"code": r.newCode(), "name": "Tablet"}, hdr...)
		if status != 201 {
			t.Fatalf("%v: %d %s", hdr, status, body)
		}
		cookie := h.Get("Set-Cookie")
		if !strings.HasPrefix(cookie, "__Host-loop_now_device=lnd_") || !strings.Contains(cookie, "; Secure") || !strings.Contains(cookie, "HttpOnly") || !strings.Contains(cookie, "SameSite=Strict") {
			t.Errorf("%v: over HTTPS the cookie is __Host- and Secure: %s", hdr, cookie)
		}
		if d := decode[apitypes.Device](t, body); d.Name != "Tablet" {
			t.Errorf("a given name is used: %+v", d)
		}
		// The cookie jar does not send Secure cookies over plain HTTP, so send it by hand.
		token := strings.TrimPrefix(strings.SplitN(cookie, ";", 2)[0], "__Host-loop_now_device=")
		plain := r.phone()
		plain.client.Jar = nil
		plain.expect(200, "GET", "/context", nil, "Cookie", "__Host-loop_now_device="+token)
	}
}

func TestRemoteDoorOriginAndHeaders(t *testing.T) {
	r := newRemoteEnv(t)
	ph := r.phone()
	ph.pair()
	agent := map[string]any{"name": "o", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}
	for _, c := range []struct {
		name, method string
		hdr          []string
		status       int
		code         string
	}{
		{"foreign origin write", "POST", []string{"Origin", "https://evil.example"}, 403, "forbidden_origin"},
		{"sibling host write", "POST", []string{"Origin", "https://other.loop-now.app"}, 403, "forbidden_origin"},
		{"null origin write", "POST", []string{"Origin", "null"}, 403, "forbidden_origin"},
		{"write without origin", "POST", []string{"Origin", ""}, 403, "forbidden_origin"},
		{"foreign origin read", "GET", []string{"Origin", "https://evil.example"}, 403, "forbidden_origin"},
		{"null origin read", "GET", []string{"Origin", "null"}, 403, "forbidden_origin"},
		{"bearer token", "GET", []string{"Authorization", "Bearer lnp_x"}, 403, "forbidden"},
	} {
		path, body := "/context", any(nil)
		if c.method == "POST" {
			path, body = r.agents(), agent
		}
		status, out, _ := ph.do(c.method, path, body, append(c.hdr, "X-Contract-Invalid-Request", "1")...)
		if status != c.status || errorCode(t, out) != c.code {
			t.Errorf("%s: want %d %s, got %d %s", c.name, c.status, c.code, status, out)
		}
	}
	if list := decode[map[string][]any](t, r.expect(200, "GET", r.agents(), nil)); len(list["agents"]) != 0 {
		t.Fatal("refused writes write nothing")
	}
	// Same origin, as the console page sends it, passes; so does a read without Origin.
	ph.expect(201, "POST", r.agents(), agent)
	ph.expect(200, "GET", "/context", nil)
	if status, _, _ := ph.do("POST", "/device-sessions", map[string]any{"code": "x"}, "Origin", "https://evil.example", "X-Contract-Invalid-Request", "1"); status != 403 {
		t.Fatalf("pairing checks the Origin too: %d", status)
	}
}

// TestRemoteDoorRemoveClosesStreams: removing a device ends its open event streams at once.
func TestRemoteDoorRemoveClosesStreams(t *testing.T) {
	r := newRemoteEnv(t)
	ph := r.phone()
	dev := ph.pair()
	other := r.phone()
	other.pair()
	open := func(p *phone) (*bufio.Reader, func()) {
		req, _ := http.NewRequest("GET", r.base+"/api/v1/overview/events", nil)
		resp, err := p.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
			t.Fatalf("stream: %d %v", resp.StatusCode, resp.Header)
		}
		br := bufio.NewReader(resp.Body)
		if _, err := br.ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		return br, func() { resp.Body.Close() }
	}
	br, closeMine := open(ph)
	defer closeMine()
	otherBr, closeOther := open(other)
	defer closeOther()
	ended := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, br); close(ended) }()
	otherEnded := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, otherBr); close(otherEnded) }()
	r.expect(204, "DELETE", "/devices/"+dev.ID.String(), nil)
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("removing the device should close its open event stream")
	}
	select {
	case <-otherEnded:
		t.Fatal("another device keeps its stream")
	case <-time.After(300 * time.Millisecond):
	}
	ph.expect(401, "GET", "/context", nil)
	other.expect(200, "GET", "/context", nil)
}

func TestRemoteDoorPairingNeedsTheDoor(t *testing.T) {
	r := newRemoteEnv(t)
	off := decode[remoteaccess.State](t, r.expect(200, "PUT", "/remote-access", map[string]any{"enabled": false}))
	if off.State != "off" || off.Enabled {
		t.Fatalf("off: %+v", off)
	}
	if b := r.expect(409, "POST", "/device-pairings", nil); errorCode(t, b) != "remote_access_off" {
		t.Fatalf("no remote door, no code: %s", b)
	}
	r.expect(422, "PUT", "/remote-access", map[string]any{"enabled": true, "port": 0}, "X-Contract-Invalid-Request", "1")
	r.expect(422, "PUT", "/remote-access", map[string]any{"port": 8000}, "X-Contract-Invalid-Request", "1")
	again := decode[remoteaccess.State](t, r.expect(200, "PUT", "/remote-access", map[string]any{"enabled": true, "port": nil}))
	if again.URL == nil || *again.URL != r.base {
		t.Fatalf("on again at the port it picked before: %+v (was %s)", again, r.base)
	}
}

// The external port has no remote door rules and says so in its context; the local door's context says local.
func TestContextDoor(t *testing.T) {
	e := newEnv(t)
	_, token := e.identity("door-bot")
	c := decode[map[string]any](t, e.external(token).expect(200, "GET", "/context", nil))
	if c["door"] != "external" {
		t.Fatalf("external port: %v", c)
	}
}
