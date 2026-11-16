// loop-server is the Loop Now server. All configuration comes from environment variables; see
// internal/server/config.
// When given an admin subcommand (version, identity, token; see internal/server/admincli), it only runs the
// subcommand and does not start the server.
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
	// Admin subcommands (external identities, tokens, version) read and write the database directly without
	// starting the server.
	if len(os.Args) > 1 && admincli.IsCommand(os.Args[1]) {
		os.Exit(admincli.Run(context.Background(), os.Args[1:], admincli.Options{Getenv: os.Getenv, Stdout: os.Stdout, Stderr: os.Stderr}))
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "配置有误，拒绝启动：")
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	// Logs go only to stderr; keeping and rotating them is left to whatever starts the server.
	log := config.NewLogger(os.Stderr, cfg.LogFormat, cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if cfg.StopOnStdinClose {
		// The shell closing the write end of standard input is equivalent to sending SIGTERM: Windows has no
		// termination signal that can be delivered, so the desktop shell relies on this for a graceful stop.
		ctx = stopOnStdinClose(ctx, os.Stdin, func() { log.Info("stdin closed, shutting down gracefully") })
	}

	a, err := app.Build(ctx, cfg, log, app.Options{})
	if err != nil {
		log.Error("startup failed", "err", err)
		os.Exit(app.ExitCode(err))
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- a.Serve() }()

	select {
	case <-ctx.Done():
		log.Info("stop signal received, shutting down gracefully")
	case err := <-serveErr:
		if err != nil {
			log.Error("HTTP server exited unexpectedly", "err", err)
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := a.Shutdown(shutdownCtx); err != nil {
		log.Error("error while stopping", "err", err)
		os.Exit(1)
	}
}
