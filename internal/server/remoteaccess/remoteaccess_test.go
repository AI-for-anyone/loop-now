package remoteaccess_test

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"loop-now/internal/server/remoteaccess"
	"loop-now/internal/testutil/sqlitetest"
)

// handler answers /hello, and /stream keeps the response open until the request ends.
func handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "remote") })
	m.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "open\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	return m
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func newDoor(t *testing.T) (*remoteaccess.Door, func() *remoteaccess.Door) {
	t.Helper()
	st := sqlitetest.NewStore(t)
	mk := func() *remoteaccess.Door {
		d := &remoteaccess.Door{Store: st, Handler: handler(), Log: slog.New(slog.DiscardHandler)}
		t.Cleanup(func() { _ = d.Close() })
		return d
	}
	return mk(), mk
}

func TestDoorStartsOffAndOpensOnLoopback(t *testing.T) {
	ctx := context.Background()
	d, again := newDoor(t)
	if err := d.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if s := d.State(); s.Enabled || s.State != remoteaccess.StateOff || s.URL != nil || s.Error != nil || s.Port != nil {
		t.Fatalf("the remote door starts off: %+v", s)
	}
	s, err := d.Apply(ctx, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Enabled || s.State != remoteaccess.StateListening || s.URL == nil || !strings.HasPrefix(*s.URL, "http://127.0.0.1:") || s.Port != nil {
		t.Fatalf("on with a port the server picks, on loopback: %+v", s)
	}
	if code, body := get(t, *s.URL+"/hello"); code != 200 || body != "remote" {
		t.Fatalf("the remote door serves its handler: %d %q", code, body)
	}
	picked := *s.URL

	// An open stream on it closes when the door is turned off.
	resp, err := http.Get(picked + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	if line, _ := br.ReadString('\n'); line != "open\n" {
		t.Fatalf("stream: %q", line)
	}
	closed := make(chan error, 1)
	go func() { _, err := io.ReadAll(br); closed <- err }()
	if s, err := d.Apply(ctx, false, nil); err != nil || s.State != remoteaccess.StateOff || s.URL != nil {
		t.Fatalf("off: %+v %v", s, err)
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("turning the door off closes its open connections")
	}
	if _, err := http.Get(picked + "/hello"); err == nil {
		t.Fatal("nothing listens after turning it off")
	}

	// The picked port is kept: on again, and after a restart, the address is the same.
	if s, err := d.Apply(ctx, true, nil); err != nil || s.URL == nil || *s.URL != picked {
		t.Fatalf("on again keeps the picked port %s: %+v %v", picked, s, err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := again()
	if err := restarted.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if s := restarted.State(); s.State != remoteaccess.StateListening || s.URL == nil || *s.URL != picked {
		t.Fatalf("a restart opens the saved door at the same address %s: %+v", picked, s)
	}
}

func TestDoorExplicitPortAndFailure(t *testing.T) {
	ctx := context.Background()
	d, _ := newDoor(t)
	if err := d.Start(ctx); err != nil {
		t.Fatal(err)
	}
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port
	s, err := d.Apply(ctx, true, &port)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Enabled || s.State != remoteaccess.StateFailed || s.Error == nil || s.URL != nil || s.Port == nil || *s.Port != port {
		t.Fatalf("a port in use: on, failed, with the reason: %+v", s)
	}
	busy.Close()
	free := freePort(t)
	s, err = d.Apply(ctx, true, &free)
	if err != nil || s.State != remoteaccess.StateListening || s.URL == nil || *s.URL != "http://127.0.0.1:"+strconv.Itoa(free) {
		t.Fatalf("a free port that was set: %+v %v", s, err)
	}
	// Applying the same setting again keeps the listener (and its connections).
	before := *s.URL
	resp, err := http.Get(before + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	_, _ = br.ReadString('\n')
	if s, err := d.Apply(ctx, true, &free); err != nil || s.State != remoteaccess.StateListening {
		t.Fatalf("same setting: %+v %v", s, err)
	}
	done := make(chan struct{})
	go func() { _, _ = io.ReadAll(br); close(done) }()
	select {
	case <-done:
		t.Fatal("the same setting must not drop open connections")
	case <-time.After(200 * time.Millisecond):
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}
