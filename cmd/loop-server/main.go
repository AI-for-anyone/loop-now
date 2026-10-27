// loop-server 是 Loop Now 的服务端。配置全部来自环境变量，见 internal/server/config。
// 带管理子命令时（version、identity、token，见 internal/server/admincli）只执行子命令，不启动服务。
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"loop-now/internal/server/admincli"
	"loop-now/internal/server/app"
	"loop-now/internal/server/config"
)

func main() {
	// 管理子命令（外部身份、令牌、版本）直接读写数据库，不启动服务。
	if len(os.Args) > 1 && admincli.IsCommand(os.Args[1]) {
		os.Exit(admincli.Run(context.Background(), os.Args[1:], admincli.Options{Getenv: os.Getenv, Stdout: os.Stdout, Stderr: os.Stderr}))
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "配置有误，拒绝启动：")
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	// 日志只写 stderr，保存与轮转交给启动服务端的方式。
	log := config.NewLogger(os.Stderr, cfg.LogFormat, cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.Build(ctx, cfg, log, app.Options{})
	if err != nil {
		log.Error("启动失败", "err", err)
		os.Exit(1)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- a.Serve() }()

	select {
	case <-ctx.Done():
		log.Info("收到停止信号，开始优雅退出")
	case err := <-serveErr:
		if err != nil {
			log.Error("HTTP 服务异常退出", "err", err)
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := a.Shutdown(shutdownCtx); err != nil {
		log.Error("停止过程中出错", "err", err)
		os.Exit(1)
	}
}
