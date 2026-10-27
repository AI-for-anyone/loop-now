package authhelper

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func options(t *testing.T) (ProcessOptions, *os.File, *os.File) {
	t.Helper()
	a, b, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	c, d, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(); b.Close(); c.Close(); d.Close() })
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	return ProcessOptions{NodeBin: "/Users/ljl/.local/node-v24.21.0/bin/node", Entry: filepath.Join(t.TempDir(), "probe.cjs"), Home: home, BasePath: "/usr/bin:/bin", Input: a, Output: d}, b, c
}
func TestIsolatedProcessEnvironmentAndPrivatePipes(t *testing.T) {
	o, write, read := options(t)
	t.Setenv("LOOP_NOW_MASTER_KEY", "master-private-marker")
	t.Setenv("OPENAI_API_KEY", "model-private-marker")
	t.Setenv("NODE_OPTIONS", "invalid-parent-option")
	script := `const fs=require('node:fs');fs.writeSync(1,'access-private-marker');fs.writeSync(2,'refresh-private-marker');fs.writeSync(4,JSON.stringify({env:process.env,args:process.argv,cwd:process.cwd()})+'\n');`
	if err := os.WriteFile(o.Entry, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd, err := Command(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Stdout != io.Discard || cmd.Stderr != io.Discard || len(cmd.Env) != 4 || len(cmd.Args) != 2 {
		t.Fatal("unsafe command settings")
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	o.Output.Close()
	o.Input.Close()
	write.Close()
	data, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"master-private-marker", "model-private-marker", "invalid-parent-option"} {
		if strings.Contains(string(data), marker) {
			t.Fatal("server environment inherited")
		}
	}
	var report struct {
		Env  map[string]string
		Args []string
		Cwd  string
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	canonicalHome, err := filepath.EvalSymlinks(o.Home)
	if err != nil {
		t.Fatal(err)
	}
	if report.Env["HOME"] != o.Home || report.Env["TMPDIR"] != filepath.Join(o.Home, "tmp") || report.Env["NODE_ENV"] != "production" || report.Cwd != canonicalHome || report.Args[1] != o.Entry {
		t.Fatal("child isolation mismatch")
	}
}
func TestProcessRejectsUnsafeDirectoriesAndOptions(t *testing.T) {
	o, _, _ := options(t)
	positive := o
	positive.DrainTimeout = 80 * time.Millisecond
	cmd, err := Command(context.Background(), positive)
	if err != nil || !strings.Contains(strings.Join(cmd.Env, "\n"), "LOOP_NOW_AUTH_HELPER_DRAIN_MS=80") {
		t.Fatal("drain budget not propagated")
	}
	for _, change := range []func(*ProcessOptions){func(o *ProcessOptions) { o.NodeBin = "node" }, func(o *ProcessOptions) { o.Entry = "relative" }, func(o *ProcessOptions) { o.Home = "relative" }, func(o *ProcessOptions) { o.Input = nil }, func(o *ProcessOptions) { o.Output = nil }, func(o *ProcessOptions) { o.BasePath = "" }} {
		bad := o
		change(&bad)
		if _, err := Command(context.Background(), bad); err == nil {
			t.Fatal("invalid command accepted")
		}
	}
	for _, budget := range []time.Duration{-1, time.Nanosecond, time.Duration(2147483648) * time.Millisecond} {
		bad := o
		bad.DrainTimeout = budget
		if _, err := Command(context.Background(), bad); err == nil {
			t.Fatal("invalid drain budget accepted")
		}
	}
	if err := os.Chmod(o.Home, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Command(context.Background(), o); err == nil {
		t.Fatal("public HOME accepted")
	}
	if err := os.Chmod(o.Home, 0700); err != nil {
		t.Fatal(err)
	}
	alias, _, _ := options(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(alias.Home, "tmp")); err != nil {
		t.Fatal(err)
	}
	if _, err := Command(context.Background(), alias); err == nil {
		t.Fatal("temporary directory alias accepted")
	}
}
