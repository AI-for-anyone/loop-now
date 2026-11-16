// Fake server for the desktop shell tests: like loop-server, it listens on LOOP_NOW_LISTEN_ADDR, atomically writes {url,pid,version} to
// LOOP_NOW_READY_FILE, answers GET /healthz, and on Interrupt/SIGTERM deletes the ready file and exits. No business logic at all.
// The desktop shell's unit tests (src/server.test.ts) build it with go build and launch it; Windows NSIS packaging in CI also uses it as
// loop-server.exe.
//
//	LOOP_NOW_FAKE_EXIT_CODE=<n>          does not listen; exits with n right after starting (tests the failure path of exiting before ready).
//	LOOP_NOW_STOP_ON_STDIN_CLOSE=1       like the real server: reads stdin and shuts down gracefully on EOF (on Windows the shell stops it this way).
//	LOOP_NOW_EXTERNAL_LISTEN_ADDR=<addr> like the real server, also listens on an external port (no TLS here, the port is only held),
//	                                     and the ready file gets an extra externalUrl (https://<actual-addr>); the directory given by
//	                                     LOOP_NOW_EXTERNAL_TLS_SELF_SIGNED is created and gets a placeholder cert.pem.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func main() {
	if code := os.Getenv("LOOP_NOW_FAKE_EXIT_CODE"); code != "" {
		n, err := strconv.Atoi(code)
		if err != nil {
			n = 1
		}
		os.Exit(n)
	}
	addr := os.Getenv("LOOP_NOW_LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ready := os.Getenv("LOOP_NOW_READY_FILE")

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		os.Exit(4)
	}
	externalURL := ""
	if ext := os.Getenv("LOOP_NOW_EXTERNAL_LISTEN_ADDR"); ext != "" {
		extLn, err := net.Listen("tcp", ext)
		if err != nil {
			fmt.Fprintln(os.Stderr, "listen external:", err)
			os.Exit(4)
		}
		defer extLn.Close()
		externalURL = "https://" + extLn.Addr().String()
		if dir := os.Getenv("LOOP_NOW_EXTERNAL_TLS_SELF_SIGNED"); dir != "" {
			if err := os.MkdirAll(dir, 0o700); err == nil {
				_ = os.WriteFile(filepath.Join(dir, "cert.pem"), []byte("fake certificate\n"), 0o644)
			}
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<!doctype html><title>fake loop-server</title>"))
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()

	url := "http://" + ln.Addr().String()
	if ready != "" {
		if err := writeReady(ready, url, externalURL); err != nil {
			fmt.Fprintln(os.Stderr, "ready file:", err)
			os.Exit(1)
		}
	}
	fmt.Fprintln(os.Stderr, "fake loop-server listening on", url)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if os.Getenv("LOOP_NOW_STOP_ON_STDIN_CLOSE") == "1" {
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			fmt.Fprintln(os.Stderr, "stdin closed, shutting down")
			cancel()
		}()
	}
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	if ready != "" {
		_ = os.Remove(ready)
	}
}

func writeReady(path, url, externalURL string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := map[string]any{"url": url, "pid": os.Getpid(), "version": "fake"}
	if externalURL != "" {
		content["externalUrl"] = externalURL
	}
	data, err := json.Marshal(content)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
