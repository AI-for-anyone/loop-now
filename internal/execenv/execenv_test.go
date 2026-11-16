package execenv

import (
	"runtime"
	"testing"
)

func TestPathKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		if PathKey(`C:\WINDOWS\System32\`) != PathKey(`c:\windows\system32`) {
			t.Fatal("on Windows directories should be case-insensitive and ignore a trailing separator")
		}
		return
	}
	if PathKey("/usr/bin/") != PathKey("/usr/bin") || PathKey("/Usr/bin") == PathKey("/usr/bin") {
		t.Fatal("on Unix directories should only be cleaned and stay case-sensitive")
	}
}
