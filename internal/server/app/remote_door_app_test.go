package app_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loop-now/internal/server/config"
)

type remoteState struct {
	Enabled bool    `json:"enabled"`
	State   string  `json:"state"`
	URL     *string `json:"url"`
}

// Real server: the remote door is off at first; turned on at the local door it serves the console, refuses the API
// without a session, pairs a device, and after a restart opens again at the same address with the device still paired.
func TestRemoteDoorOnRealServer(t *testing.T) {
	w := newWorld(t)
	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("<title>loop now console</title>"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := w.start(func(c *config.Config) { c.WebDir = web })
	if st := decode[remoteState](t, s.must(200, "GET", "/remote-access", nil)); st.Enabled || st.State != "off" || st.URL != nil {
		t.Fatalf("the remote door starts off: %+v", st)
	}
	st := decode[remoteState](t, s.must(200, "PUT", "/remote-access", map[string]any{"enabled": true}))
	if st.State != "listening" || st.URL == nil || !strings.HasPrefix(*st.URL, "http://127.0.0.1:") {
		t.Fatalf("on: %+v", st)
	}
	base := *st.URL
	send := func(method, path, cookie string, body any) (int, []byte, http.Header) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			j, _ := json.Marshal(body)
			rd = bytes.NewReader(j)
		}
		req, _ := http.NewRequest(method, base+path, rd)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", base)
		}
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b, resp.Header
	}
	if code, b, _ := send("GET", "/pair", "", nil); code != 200 || !strings.Contains(string(b), "loop now console") {
		t.Fatalf("the console's files are served at the remote door: %d %s", code, b)
	}
	if code, _, _ := send("GET", "/api/v1/context", "", nil); code != 401 {
		t.Fatalf("the API needs a session: %d", code)
	}
	pairing := decode[struct{ Code, URL string }](t, s.must(201, "POST", "/device-pairings", nil))
	if pairing.URL != base+"/pair#"+pairing.Code {
		t.Fatalf("pairing link: %q", pairing.URL)
	}
	code, b, hdr := send("POST", "/api/v1/device-sessions", "", map[string]any{"code": pairing.Code})
	if code != 201 {
		t.Fatalf("pair: %d %s", code, b)
	}
	cookie := strings.SplitN(hdr.Get("Set-Cookie"), ";", 2)[0]
	if code, b, _ := send("GET", "/api/v1/context", cookie, nil); code != 200 || !strings.Contains(string(b), `"door":"remote"`) {
		t.Fatalf("paired: %d %s", code, b)
	}
	s.stop()
	if _, err := http.Get(base + "/"); err == nil {
		t.Fatal("the remote door closes with the server")
	}

	s = w.start(func(c *config.Config) { c.WebDir = web })
	defer s.stop()
	if st := decode[remoteState](t, s.must(200, "GET", "/remote-access", nil)); st.State != "listening" || st.URL == nil || *st.URL != base {
		t.Fatalf("after a restart the remote door opens at the same address %s: %+v", base, st)
	}
	if code, _, _ := send("GET", "/api/v1/context", cookie, nil); code != 200 {
		t.Fatalf("the device is still paired after a restart: %d", code)
	}
	if devices := decode[struct{ Devices []any }](t, s.must(200, "GET", "/devices", nil)); len(devices.Devices) != 1 {
		t.Fatalf("devices: %+v", devices)
	}
}
