package execenv

import (
	"runtime"
	"testing"
)

func TestPathKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		if PathKey(`C:\WINDOWS\System32\`) != PathKey(`c:\windows\system32`) {
			t.Fatal("Windows 上的目录应不分大小写、忽略结尾分隔符")
		}
		return
	}
	if PathKey("/usr/bin/") != PathKey("/usr/bin") || PathKey("/Usr/bin") == PathKey("/usr/bin") {
		t.Fatal("Unix 上应只规范化、区分大小写")
	}
}
