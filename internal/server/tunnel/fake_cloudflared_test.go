package tunnel_test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeDirEnv names the folder through which a test talks to the stand-in cloudflared: the test writes "mode" there,
// each start appends a JSON line to "starts", and a stop writes "stopped-<pid>".
const fakeDirEnv = "LOOP_NOW_TEST_FAKE_CLOUDFLARED_DIR"

func TestMain(m *testing.M) {
	if dir := os.Getenv(fakeDirEnv); dir != "" && len(os.Args) > 1 && os.Args[1] == "tunnel" {
		fakeCloudflared(dir)
		return
	}
	os.Exit(m.Run())
}

type fakeStart struct {
	PID   int      `json:"pid"`
	Args  []string `json:"args"`
	Token string   `json:"token"`
}

// fakeCloudflared plays cloudflared: modes ok (connects), exit (fails at once), rejected (the edge refuses the
// tunnel), rejected-once (rejected the first time, ok after), noready (never connects).
func fakeCloudflared(dir string) {
	mode := "ok"
	if b, err := os.ReadFile(filepath.Join(dir, "mode")); err == nil {
		mode = strings.TrimSpace(string(b))
	}
	start, _ := json.Marshal(fakeStart{PID: os.Getpid(), Args: os.Args[1:], Token: os.Getenv("TUNNEL_TOKEN")})
	f, _ := os.OpenFile(filepath.Join(dir, "starts"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	_, _ = f.Write(append(start, '\n'))
	_ = f.Close()
	if mode == "rejected-once" {
		mode = "ok"
		if _, err := os.Stat(filepath.Join(dir, "rejected-once")); err != nil {
			_ = os.WriteFile(filepath.Join(dir, "rejected-once"), nil, 0o600)
			mode = "rejected"
		}
	}
	if mode == "exit" {
		fmt.Fprintln(os.Stderr, `2026-11-28T10:00:00Z ERR Couldn't start tunnel error="something broke"`)
		os.Exit(1)
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, os.Interrupt)
	readyConns := 0
	if mode == "ok" {
		readyConns = 1
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(2)
	}
	go func() {
		_ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			status := http.StatusOK
			if readyConns == 0 {
				status = http.StatusServiceUnavailable
			}
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, `{"status":%d,"readyConnections":%d,"connectorId":"x"}`, status, readyConns)
		}))
	}()
	fmt.Fprintf(os.Stderr, "2026-11-28T10:00:00Z INF Starting metrics server on %s/metrics\n", ln.Addr())
	switch mode {
	case "ok":
		time.Sleep(30 * time.Millisecond)
		fmt.Fprintln(os.Stderr, `2026-11-28T10:00:00Z INF Registered tunnel connection connIndex=0 connection=abc event=0 ip=198.41.200.13 location=sjc protocol=quic`)
	case "rejected":
		for range 5 {
			fmt.Fprintln(os.Stderr, `2026-11-28T10:00:00Z ERR Register tunnel error from server side error="Unauthorized: Tunnel not found" connIndex=0`)
		}
	}
	<-stop
	_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("stopped-%d", os.Getpid())), nil, 0o600)
	os.Exit(0)
}
