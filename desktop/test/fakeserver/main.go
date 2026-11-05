// 桌面壳测试用的假服务端：和 loop-server 一样按 LOOP_NOW_LISTEN_ADDR 监听、把 {url,pid,version} 原子地写进 LOOP_NOW_READY_FILE、
// 回答 GET /healthz，收到 Interrupt／SIGTERM 时删掉就绪文件退出。没有任何业务。
// 桌面壳的单测（src/server.test.ts）用 go build 把它编出来拉起；CI 里 Windows 的 NSIS 打包也拿它充当 loop-server.exe。
//
//	LOOP_NOW_FAKE_EXIT_CODE=<n>  启动后不监听，直接以 n 退出（测试就绪前退出的失败路径）。
package main

import (
	"context"
	"encoding/json"
	"fmt"
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
		if err := writeReady(ready, url); err != nil {
			fmt.Fprintln(os.Stderr, "ready file:", err)
			os.Exit(1)
		}
	}
	fmt.Fprintln(os.Stderr, "fake loop-server listening on", url)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	if ready != "" {
		_ = os.Remove(ready)
	}
}

func writeReady(path, url string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(map[string]any{"url": url, "pid": os.Getpid(), "version": "fake"})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
