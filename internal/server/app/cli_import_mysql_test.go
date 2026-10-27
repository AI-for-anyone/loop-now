package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"loop-now/internal/server/cliimport/source"
	"loop-now/internal/server/clitemplate"
)

func TestImportMySQLMissingOrForgedBundleNeverExecutesHost(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	if e := os.WriteFile(filepath.Join(home, ".mylogin.cnf"), []byte("synthetic unreadable login bytes"), 0600); e != nil {
		t.Fatal(e)
	}
	catalog, e := clitemplate.Load("")
	if e != nil {
		t.Fatal(e)
	}
	marker := filepath.Join(work, "host-was-executed")
	native := filepath.Join(work, "tools/mysql/8.4.9/runtime/mysql-8.4.9-macos15-arm64/bin")
	if e = os.MkdirAll(native, 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"mysql_config_editor", "my_print_defaults"} {
		if e = os.WriteFile(filepath.Join(native, name), []byte("#!/bin/sh\ntouch '"+marker+"'\nprintf 'Ver 8.4.9\\n'\n"), 0700); e != nil {
			t.Fatal(e)
		}
	}
	for _, scenario := range []string{"missing_marker", "forged_marker"} {
		t.Run(scenario, func(t *testing.T) {
			if scenario == "forged_marker" {
				if e := os.WriteFile(filepath.Join(work, "tools/mysql/8.4.9/.bundle-ready.json"), []byte(`{"sha256":"5e1e11782219f81e2246f64f5515af451c36c1167c8158760e17a6854e6e8971","version":"8.4.9","path":"mysql-8.4.9-macos15-arm64/bin/mysql"}`), 0600); e != nil {
					t.Fatal(e)
				}
			}
			r, e := source.New(home, catalog)
			if e != nil {
				t.Fatal(e)
			}
			defer r.Close()
			configureImportMySQLTools(context.Background(), r, home, work, catalog)
			candidates, e := r.Discover(context.Background())
			if e != nil {
				t.Fatal("discovery failed")
			}
			if len(candidates) != 1 || candidates[0].Reason != "tool_unavailable" {
				t.Fatal("missing/bad trusted tools were silently supported")
			}
			if _, e := os.Stat(marker); !os.IsNotExist(e) {
				t.Fatal("forged native tool executed")
			}
		})
	}
}
