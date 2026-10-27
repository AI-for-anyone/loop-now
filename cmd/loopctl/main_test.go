package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// loopctl 二进制只依赖标准库与本 module，不引用服务端的包。
func TestDependencies(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if !strings.HasPrefix(pkg, "loop-now/") {
			t.Errorf("引用了第三方包 %s", pkg)
		}
		if strings.HasPrefix(pkg, "loop-now/internal/server") || strings.HasPrefix(pkg, "loop-now/internal/testutil") || pkg == "loop-now/internal/loopctl/clitest" {
			t.Errorf("引用了不应进入 loopctl 的包 %s", pkg)
		}
	}
}

// 真实进程：每次调用只向标准输出写一个 JSON 对象，标准错误为空，退出码与类别一致。
func TestProcessOutput(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "loopctl")
	if out, err := exec.Command("go", "build", "-ldflags", "-X loop-now/internal/buildinfo.version=9.9.9-test", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("构建失败：%v\n%s", err, out)
	}
	home := t.TempDir()
	cases := []struct {
		args []string
		exit int
	}{
		{[]string{"version"}, 0},
		{nil, 0},
		{[]string{"schema", "inbox", "list"}, 0},
		{[]string{"agent", "delete", "x", "--help"}, 0},
		{[]string{"agent", "delete", "x"}, 2},
		{[]string{"inbox"}, 2},
		{[]string{"nope"}, 2},
		{[]string{"whoami"}, 3},
	}
	for _, c := range cases {
		cmd := exec.Command(bin, c.args...)
		cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if code != c.exit {
			t.Errorf("%v：退出码 %d，应为 %d：%s", c.args, code, c.exit, stdout.String())
		}
		if stderr.Len() != 0 {
			t.Errorf("%v：标准错误不为空：%s", c.args, stderr.String())
		}
		var v struct {
			OK   bool `json:"ok"`
			Meta struct {
				ClientVersion string `json:"clientVersion"`
			} `json:"meta"`
		}
		if strings.Count(stdout.String(), "\n") != 1 || json.Unmarshal(stdout.Bytes(), &v) != nil || v.OK != (c.exit == 0) {
			t.Errorf("%v：标准输出应恰好是一个 JSON 对象：%q", c.args, stdout.String())
		}
		if v.Meta.ClientVersion != "9.9.9-test" {
			t.Errorf("%v：版本应来自 ldflags 注入：%s", c.args, v.Meta.ClientVersion)
		}
	}
}
