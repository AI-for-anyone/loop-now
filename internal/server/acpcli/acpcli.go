// Package acpcli finds and checks the Agent Client Protocol CLIs installed on the user's computer (grok, agy): where the
// executable is, its version, whether it is logged in, and Antigravity's model list. It never reads their login files:
// the login state comes from the CLI's own answer.
package acpcli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"loop-now/internal/server/codexcli"
)

// ErrNotFound means the executable is not on the login shell's PATH.
var ErrNotFound = errors.New("not on the login shell's PATH")

const commandTimeout = 30 * time.Second

// Find returns the first executable named name (name.exe or name.cmd on Windows) in dirs, with symlinks resolved.
func Find(name string, dirs []string) (string, error) {
	names := []string{name}
	if runtime.GOOS == "windows" {
		names = []string{name + ".exe", name + ".cmd"}
	}
	for _, dir := range dirs {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		for _, n := range names {
			p := filepath.Join(dir, n)
			info, err := os.Stat(p)
			if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
				continue
			}
			if real, err := filepath.EvalSymlinks(p); err == nil {
				return real, nil
			}
		}
	}
	return "", ErrNotFound
}

// EnvOptions is what the environment of a helper run of the CLI holds.
type EnvOptions struct {
	// Home is the user's real HOME (where the CLI keeps its login); User is USER.
	Home, User string
	Path       []string
	// Extra are further NAME=value pairs.
	Extra []string
}

// Env builds a minimal environment from scratch for running the CLI outside an Agent, without the keys StripEnv removes.
func Env(o EnvOptions) []string {
	env := []string{"HOME=" + o.Home, "PATH=" + strings.Join(o.Path, string(os.PathListSeparator))}
	if o.User != "" {
		env = append(env, "USER="+o.User)
	}
	return StripEnv(append(env, o.Extra...))
}

// apiKeys are variables that would make a CLI use an API key instead of this computer's login.
var apiKeys = []string{"XAI_API_KEY", "GROK_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY"}

// StripEnv removes the API key variables, so the CLI uses only this computer's login.
func StripEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if !slicesContainsFold(apiKeys, k) {
			out = append(out, kv)
		}
	}
	return out
}

func slicesContainsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

func run(ctx context.Context, bin string, env []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	cmd.Dir = os.TempDir()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String() + "\n" + stdout.String()); msg != "" {
			return stdout.Bytes(), errors.New(msg)
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}

var versionPattern = regexp.MustCompile(`\d+\.\d+\.\d+[0-9A-Za-z.+-]*`)

// Version runs bin --version and returns the first version number in its output.
func Version(ctx context.Context, bin string, env []string) (string, error) {
	out, err := run(ctx, bin, env, "--version")
	if err != nil {
		return "", err
	}
	v := versionPattern.FindString(string(out))
	if v == "" {
		return "", errors.New("the output of --version has no version number")
	}
	return v, nil
}

// AtLeast reports whether version is min or newer.
func AtLeast(version, min string) bool { return min == "" || codexcli.AtLeast(version, min) }

// Login states.
const (
	LoggedIn  = "logged_in"
	LoggedOut = "logged_out"
	Unknown   = "unknown"
)

// GrokLogin asks grok models, whose first line says whether this computer is logged in.
func GrokLogin(ctx context.Context, bin string, env []string) string {
	out, err := run(ctx, bin, env, "models")
	first, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	switch {
	case strings.Contains(first, "You are logged in"):
		return LoggedIn
	case strings.Contains(strings.ToLower(first), "not logged in"):
		return LoggedOut
	case err != nil && strings.Contains(strings.ToLower(err.Error()), "not logged in"):
		return LoggedOut
	}
	return Unknown
}

// Model is one model agy models lists.
type Model struct{ ID, Name string }

// AntigravityModels runs agy models, which lists one model per line as id<TAB>name; it fails when agy is not logged in.
func AntigravityModels(ctx context.Context, bin string, env []string) ([]Model, error) {
	out, err := run(ctx, bin, env, "models")
	if err != nil {
		return nil, err
	}
	var models []Model
	for line := range strings.Lines(string(out)) {
		id, name, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || id == "" || strings.ContainsAny(id, " ") {
			continue
		}
		models = append(models, Model{ID: id, Name: strings.TrimSpace(name)})
	}
	return models, nil
}

// AntigravityLogin tells the login state from agy models: a list means logged in, a failure that says so means not.
func AntigravityLogin(ctx context.Context, bin string, env []string) string {
	return AntigravityLoginOf(AntigravityModels(ctx, bin, env))
}

// AntigravityLoginOf tells the login state from the answer of agy models.
func AntigravityLoginOf(models []Model, err error) string {
	switch {
	case err == nil && len(models) > 0:
		return LoggedIn
	case err != nil && strings.Contains(strings.ToLower(err.Error()), "log"):
		return LoggedOut
	}
	return Unknown
}
