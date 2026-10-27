package dbcli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCapturedDiscoverySelectionAndHostTrap(t *testing.T) {
	home := t.TempDir()
	native := filepath.Join(t.TempDir(), "native")
	// This fake executable is intentionally local; native format tests are separate.
	if e := os.WriteFile(native, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\nexit 7\n"), 0700); e != nil {
		t.Fatal(e)
	}
	for _, cli := range []string{"mysql", "psql"} {
		t.Run(cli, func(t *testing.T) {
			f := Fields{Host: "127.0.0.1", Port: "3306", User: "synthetic-user", Password: " a:b\\\"# ", Database: "synthetic-db", SSLMode: "DISABLED"}
			if cli == "psql" {
				f.Port = "5432"
				f.SSLMode = "disable"
			}
			project := uuid.New()
			one, two := Connection{ProjectID: &project, AccountID: uuid.New(), Name: "A", VersionID: uuid.New(), Fields: f}, Connection{ProjectID: &project, AccountID: uuid.New(), Name: "B", VersionID: uuid.New(), Fields: f}
			b, _ := json.Marshal([]Connection{one, two})
			if e := privateWrite(filepath.Join(home, ConfigPath(cli)), b); e != nil {
				t.Fatal(e)
			}
			invoke := func(args ...string) (int, string, string) {
				var out, err bytes.Buffer
				code := Run(context.Background(), append([]string{cli, native}, args...), Options{Getenv: func(k string) string {
					if k == "HOME" {
						return home
					}
					return "host-trap"
				}, Stdin: strings.NewReader("SELECT 1"), Stdout: &out, Stderr: &err})
				return code, out.String(), err.String()
			}
			code, out, err := invoke("--loop-list-connections")
			if code != 0 || err != "" || strings.Contains(out, f.Password) || strings.Contains(out, f.Host) {
				t.Fatal("unsafe discovery")
			}
			var info []Info
			if json.Unmarshal([]byte(out), &info) != nil || len(info) != 2 || info[1].VersionID != two.VersionID {
				t.Fatal("not captured versions")
			}
			for _, args := range [][]string{{}, {"--loop-connection-id", uuid.NewString()}, {"--loop-connection-id", one.AccountID.String(), "--host=external"}, {"--loop-connection-id", one.AccountID.String(), "-psecret"}, {"--loop-list-connections", "--loop-connection-id", one.AccountID.String()}} {
				code, out, _ = invoke(args...)
				if code == 0 || out != "" {
					t.Fatal("invalid/ambiguous invoked native")
				}
			}
			code, out, err = invoke("--loop-connection-id=" + two.AccountID.String())
			if code != 7 || err != "" || strings.Contains(out, f.Password) {
				t.Fatal("native exit/config leaked")
			}
			b, _ = json.Marshal([]Connection{one})
			privateWrite(filepath.Join(home, ConfigPath(cli)), b)
			code, _, _ = invoke()
			if code != 7 {
				t.Fatal("unique direct invocation failed")
			}
			b, _ = json.Marshal([]Connection{})
			privateWrite(filepath.Join(home, ConfigPath(cli)), b)
			code, out, _ = invoke("--loop-list-connections")
			if code != 0 || strings.TrimSpace(out) != "[]" {
				t.Fatal("empty discovery")
			}
			code, out, _ = invoke()
			if code == 0 || out != "" {
				t.Fatal("empty invoked native")
			}
		})
	}
}
func TestDatabaseConfigurationValidation(t *testing.T) {
	f := Fields{Host: "db.example", Port: "5432", User: "u", Password: "synthetic", Database: "db", SSLMode: "disable"}
	for _, mutate := range []func(*Fields){func(f *Fields) { f.Host = "/tmp/socket" }, func(f *Fields) { f.Host = "a,b" }, func(f *Fields) { f.Port = "05432" }, func(f *Fields) { f.SSLMode = "prefer" }, func(f *Fields) { f.SSLMode = "verify-full" }, func(f *Fields) { f.Password = "x\ny" }, func(f *Fields) { f.ClientCert = "fake" }} {
		bad := f
		mutate(&bad)
		if Validate("psql", bad) == nil {
			t.Fatal("invalid config accepted")
		}
	}
	if Validate("psql", f) != nil {
		t.Fatal("valid static TCP rejected")
	}
}
