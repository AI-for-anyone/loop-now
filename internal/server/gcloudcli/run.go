// Package gcloudcli is the execution-scoped native gcloud entry point.
package gcloudcli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Options struct {
	Getenv         func(string) string
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func Allowed(args []string) bool {
	group := ""
	track := false
	valueFlags := map[string]bool{"--project": true, "--billing-project": true, "--verbosity": true, "--format": true, "--filter": true, "--flatten": true}
	boolFlags := map[string]bool{"--quiet": true, "-q": true, "--help": true, "-h": true, "--version": true, "--user-output-enabled": true, "--no-user-output-enabled": true}
	for n := 0; n < len(args); n++ {
		arg := args[n]
		parts := strings.SplitN(arg, "=", 2)
		key := parts[0]
		switch key {
		case "--account", "--configuration", "--credential-file-override", "--access-token-file", "--impersonate-service-account", "--flags-file", "--log-http", "--trace-token", "--password-file", "--authorization-token-file", "--authority-selector":
			return false
		}
		// Consume the actual value, rather than treating info/project names as the
		// command. This also handles flags between beta/alpha and the command group.
		if valueFlags[key] {
			value := ""
			if len(parts) == 2 {
				value = parts[1]
			} else {
				n++
				if n >= len(args) {
					return false
				}
				value = args[n]
			}
			if value == "" || strings.HasPrefix(value, "-") {
				return false
			}
			if key == "--verbosity" && value != "error" && value != "warning" && value != "info" && value != "none" {
				return false
			}
			continue
		}
		if group == "" {
			if strings.HasPrefix(arg, "-") {
				if !boolFlags[key] || len(parts) > 1 {
					return false
				}
				continue
			}
			if !track && (arg == "alpha" || arg == "beta") {
				track = true
				continue
			}
			group = arg
		}
	}
	return group != "auth" && group != "config" && group != "components" && group != "init" && group != "interactive"
}

func Run(ctx context.Context, args []string, o Options) int {
	fail := func() int { fmt.Fprintln(o.Stderr, "Google CLI authorization or arguments unavailable."); return 2 }
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.Stdout == nil {
		o.Stdout = io.Discard
	}
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
	if len(args) < 1 || !filepath.IsAbs(args[0]) || !Allowed(args[1:]) {
		return fail()
	}
	root := filepath.Clean(args[0])
	sdk := filepath.Join(root, "google-cloud-sdk")
	fw := filepath.Join(root, "Python.framework")
	home := o.Getenv("HOME")
	if !filepath.IsAbs(home) {
		return fail()
	}
	config := filepath.Join(home, ".loop-now/gcloud/config")
	if e := os.MkdirAll(config, 0700); e != nil {
		return fail()
	}
	env := []string{"HOME=" + home, "CLOUDSDK_CONFIG=" + config, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "PYTHONNOUSERSITE=1", "CLOUDSDK_CORE_DISABLE_USAGE_REPORTING=true", "CLOUDSDK_COMPONENT_MANAGER_DISABLE_UPDATE_CHECK=true", "CLOUDSDK_CORE_DISABLE_PROMPTS=true"}
	entry := filepath.Join(sdk, "lib/gcloud.py")
	extra := []string{}
	grant := o.Getenv("LOOP_NOW_GCLOUD_GRANT_VERSION")
	if grant != "" {
		for _, key := range []string{"LOOP_NOW_GCLOUD_BRIDGE_SOCKET", "LOOP_NOW_GCLOUD_BRIDGE_CAPABILITY", "LOOP_NOW_GCLOUD_GRANT_VERSION"} {
			value := o.Getenv(key)
			if value == "" {
				return fail()
			}
			env = append(env, key+"="+value)
		}
		env = append(env, "CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE="+grant)
		entry = o.Getenv("LOOP_NOW_GCLOUD_RUNTIME_ENTRY")
		if !filepath.IsAbs(entry) {
			return fail()
		}
		extra = []string{"--sdk-root", sdk, "--"}
	} else {
		credential := filepath.Join(home, ".loop-now/gcloud/service-account.json")
		fi, e := os.Lstat(credential)
		if e != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0600 {
			return fail()
		}
		env = append(env, "CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE="+credential)
	}
	if b, e := os.ReadFile(filepath.Join(home, ".loop-now/gcloud/default-project")); e == nil {
		project := string(b)
		if project != "" && !strings.ContainsAny(project, "\x00\r\n") {
			env = append(env, "CLOUDSDK_CORE_PROJECT="+project)
		}
	}
	script := "export DYLD_FRAMEWORK_PATH=" + quote(filepath.Dir(fw)) + " DYLD_LIBRARY_PATH=" + quote(filepath.Join(fw, "Versions/3.13/lib")) + " PYTHONHOME=" + quote(filepath.Join(fw, "Versions/3.13")) + "; exec " + quote(filepath.Join(fw, "Versions/3.13/bin/python3.13")) + " -S " + quote(entry)
	for _, arg := range append(extra, args[1:]...) {
		script += " " + quote(arg)
	}
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	cmd.Env = env
	cmd.Dir = home
	cmd.Stdin = o.Stdin
	cmd.Stdout = o.Stdout
	cmd.Stderr = o.Stderr
	if e := cmd.Run(); e != nil {
		if exit, ok := e.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		return fail()
	}
	return 0
}
