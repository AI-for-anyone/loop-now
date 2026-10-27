package service

import "testing"

func TestActualMySQLVersionAbsoluteArgv0(t *testing.T) {
	for _, in := range []string{"mysql Ver 8.4.9 for macos15 on arm64 (MySQL Community Server - GPL)", "/private/tmp/owned tools/runtime/bin/mysql  Ver 8.4.9 for macos15 on arm64 (MySQL Community Server - GPL)"} {
		v, ok := actualVersion("mysql", []byte(in))
		if !ok || v != "8.4.9" {
			t.Fatal("official native argv0 shape rejected")
		}
	}
	for _, in := range []string{"other mysql Ver 8.4.9", "/private/tmp/notmysql Ver 8.4.9", "unexpected\nmysql Ver8.4.9"} {
		if _, ok := actualVersion("mysql", []byte(in)); ok {
			t.Fatal("noncanonical version accepted")
		}
	}
}
