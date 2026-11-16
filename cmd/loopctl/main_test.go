package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"loop-now/internal/testutil/testenv"
)

// The loopctl binary depends only on the standard library and this module and does not import server packages; the
// only exception is the JSON Schema validation library and its dependencies, pulled in by the manifest validation
// (internal/pluginmanifest) that the plugin commands share with the server.
func TestDependencies(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		// golang.org/x/sys is pulled in by internal/privatefs ("only the current user can access it" for the config
		// file requires reading and writing ACLs on Windows).
		if strings.HasPrefix(pkg, "github.com/santhosh-tekuri/jsonschema/v6") || strings.HasPrefix(pkg, "golang.org/x/text/") || strings.HasPrefix(pkg, "golang.org/x/sys") {
			continue
		}
		if !strings.HasPrefix(pkg, "loop-now/") {
			t.Errorf("imports third-party package %s", pkg)
		}
		if strings.HasPrefix(pkg, "loop-now/internal/server") || strings.HasPrefix(pkg, "loop-now/internal/testutil") || pkg == "loop-now/internal/loopctl/clitest" {
			t.Errorf("imports package %s, which must not be in loopctl", pkg)
		}
	}
}

// Real process: each call writes exactly one JSON object to standard output, standard error is empty, and the exit
// code matches the category.
func TestProcessOutput(t *testing.T) {
	bin := testenv.Exe(filepath.Join(t.TempDir(), "loopctl"))
	if out, err := exec.Command("go", "build", "-ldflags", "-X loop-now/internal/buildinfo.version=9.9.9-test", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
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
			t.Errorf("%v: exit code %d, want %d: %s", c.args, code, c.exit, stdout.String())
		}
		if stderr.Len() != 0 {
			t.Errorf("%v: stderr is not empty: %s", c.args, stderr.String())
		}
		var v struct {
			OK   bool `json:"ok"`
			Meta struct {
				ClientVersion string `json:"clientVersion"`
			} `json:"meta"`
		}
		if strings.Count(stdout.String(), "\n") != 1 || json.Unmarshal(stdout.Bytes(), &v) != nil || v.OK != (c.exit == 0) {
			t.Errorf("%v: stdout should be exactly one JSON object: %q", c.args, stdout.String())
		}
		if v.Meta.ClientVersion != "9.9.9-test" {
			t.Errorf("%v: version should come from ldflags: %s", c.args, v.Meta.ClientVersion)
		}
	}
}
