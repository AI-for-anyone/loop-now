// 桌面壳测试用的假服务端：和 loop-server 一样按 LOOP_NOW_LISTEN_ADDR 监听、把 {url,pid,version} 原子地写进 LOOP_NOW_READY_FILE、
// 回答 GET /healthz，收到 Interrupt／SIGTERM 时删掉就绪文件退出。没有任何业务。
// 桌面壳的单测（src/server.test.ts）用 go build 把它编出来拉起；CI 里 Windows 的 NSIS 打包也拿它充当 loop-server.exe。
//
//	LOOP_NOW_FAKE_EXIT_CODE=<n>          启动后不监听，直接以 n 退出（测试就绪前退出的失败路径）。
//	LOOP_NOW_STOP_ON_STDIN_CLOSE=1      和真服务端一样：读 stdin，读到 EOF 就走优雅停机（Windows 上壳靠这个停它）。
//	LOOP_NOW_EXTERNAL_LISTEN_ADDR=<地址> 和真服务端一样再监听一个对外端口（这里不做 TLS，只占着端口），
//	                                    就绪文件里多一项 externalUrl（https://<实际地址>）；LOOP_NOW_EXTERNAL_TLS_SELF_SIGNED
//	                                    指定的目录会建出来并放一个占位的 cert.pem。
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
