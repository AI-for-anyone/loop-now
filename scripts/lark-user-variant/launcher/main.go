// Platform-owned private PATH bootstrap; not an official lark-cli release.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"loop-now/internal/server/larksidecar"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	code, err := larksidecar.RunLauncher(ctx, os.Getenv(larksidecar.ExecURLEnv), os.Getenv(larksidecar.ExecKeyEnv), os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		// Never print descriptor contents, filesystem paths, argv or provider errors.
		fmt.Fprintln(os.Stderr, "lark_user_launcher_rejected")
		os.Exit(2)
	}
	os.Exit(code)
}
