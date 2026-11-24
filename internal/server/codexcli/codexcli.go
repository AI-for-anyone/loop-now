// Package codexcli finds the Codex CLI installed on the user's computer and runs its own short commands (--version,
// login status). The platform uses only Codex's local login: it never reads Codex's credential files, and it passes
// Codex no OpenAI or Codex variables of its own, only CODEX_HOME pointing at the user's Codex home.
package codexcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"loop-now/internal/execenv"
	"loop-now/internal/procgroup"
)

// ErrNotFound means codex is not on the PATH of the user's login shell.
var ErrNotFound = errors.New("codex is not on the login shell's PATH")

// commandTimeout is the time limit for codex's own short commands.
const commandTimeout = 30 * time.Second

// names are the executable names to look for: on Windows a native install is codex.exe and a global npm install is
// codex.cmd.
func names() []string {
	if runtime.GOOS == "windows" {
		return []string{"codex.exe", "codex.cmd"}
	}
	return []string{"codex"}
}

// Find looks for codex in dirs (the PATH of the user's login shell) in order and returns its absolute path with
// symlinks resolved.
func Find(dirs []string) (string, error) {
	for _, dir := range dirs {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		for _, name := range names() {
			p := filepath.Join(dir, name)
			info, err := os.Stat(p)
			if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
				continue
			}
			real, err := filepath.EvalSymlinks(p)
			if err != nil {
				continue
			}
			return real, nil
		}
	}
	return "", ErrNotFound
}

// Home is the user's Codex home: CODEX_HOME from the server's own environment when it is an absolute path, otherwise
// .codex in the user's real home directory. Codex keeps the login and the conversations there.
func Home(getenv func(string) string, realHome string) string {
	if h := getenv("CODEX_HOME"); h != "" && filepath.IsAbs(h) {
		return h
	}
	return filepath.Join(realHome, ".codex")
}

// forbiddenEnv reports whether a variable must not be passed to codex: no OpenAI or Codex variable (API key, base URL,
// another home) may change its authentication or configuration. Names are case-insensitive on Windows.
func forbiddenEnv(name string) bool {
	if runtime.GOOS == "windows" {
		name = strings.ToUpper(name)
	}
	return strings.HasPrefix(name, "OPENAI_") || strings.HasPrefix(name, "CODEX_")
}

// StripEnv removes the variables that must not be passed to codex from env (a KEY=VALUE list).
func StripEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if !forbiddenEnv(k) {
			out = append(out, kv)
		}
	}
	return out
}

// WithHome returns env without OpenAI and Codex variables, plus CODEX_HOME=codexHome.
func WithHome(env []string, codexHome string) []string {
	return append(StripEnv(env), "CODEX_HOME="+codexHome)
}

// EnvOptions is the environment for running codex's own commands outside an Agent's process (--version, login status,
// a short-lived app-server for the model list).
type EnvOptions struct {
	// Home is HOME (the user's real home) and CodexHome is CODEX_HOME.
	Home, CodexHome string
	User            string
	Path            []string
	// Extra are other base variables (K=V), such as the passed-through LANG; they do not override the fields above.
	Extra []string
}

// Env is the environment for running codex's own commands: the system base variables, the passed-through ones, HOME,
// USER/LOGNAME, the given PATH and CODEX_HOME, without other OpenAI or Codex variables.
func Env(o EnvOptions) []string {
	env := execenv.Base(o.Home, "")
	for _, kv := range o.Extra {
		switch k, _, _ := strings.Cut(kv, "="); k {
		case "HOME", "PATH", "USER", "LOGNAME":
		default:
			env = append(env, kv)
		}
	}
	env = append(env, "HOME="+o.Home, "PATH="+strings.Join(o.Path, string(os.PathListSeparator)))
	if o.User != "" {
		env = append(env, "USER="+o.User, "LOGNAME="+o.User)
	}
	return WithHome(env, o.CodexHome)
}

// run runs one short codex command and returns its combined output; a non-zero exit status is an error that carries
// only the status, never the output (login status may name the account).
func run(ctx context.Context, bin string, env []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	cmd.Cancel = procgroup.Cancel(cmd)
	cmd.WaitDelay = 5 * time.Second
	procgroup.Configure(cmd)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && ctx.Err() == nil {
			return out.Bytes(), fmt.Errorf("codex %s failed: %s", args[0], exit.ProcessState.String())
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("codex %s timed out", args[0])
		}
		return nil, fmt.Errorf("codex %s could not run", args[0])
	}
	return out.Bytes(), nil
}

var versionPattern = regexp.MustCompile(`\d+\.\d+\.\d+[0-9A-Za-z.+-]*`)

// Version runs codex --version and returns the version number (codex-cli 0.167.0 gives 0.167.0).
func Version(ctx context.Context, bin string, env []string) (string, error) {
	out, err := run(ctx, bin, env, "--version")
	if err != nil {
		return "", err
	}
	v := versionPattern.FindString(strings.TrimSpace(string(out)))
	if v == "" {
		return "", errors.New("the output of codex --version has no version number")
	}
	return v, nil
}

// AtLeast reports whether version is min or newer, comparing the numeric major.minor.patch parts (a suffix such as
// -alpha.1 is ignored).
func AtLeast(version, min string) bool {
	a, b := parts(version), parts(min)
	for i := range 3 {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return true
}

func parts(v string) [3]int {
	var out [3]int
	core, _, _ := strings.Cut(versionPattern.FindString(v), "-")
	for i, p := range strings.SplitN(core, ".", 3) {
		n, _ := strconv.Atoi(strings.TrimRightFunc(p, func(r rune) bool { return r < '0' || r > '9' }))
		out[i] = n
	}
	return out
}

// Login is what codex login status says; it holds no account details.
type Login struct {
	LoggedIn bool
	// Method is chatgpt or api_key when logged in; empty when unknown.
	Method string
}

// ErrUnknownLogin means the output of login status was not recognized.
var ErrUnknownLogin = errors.New("the output of codex login status was not recognized")

// LoginStatus runs codex login status. Codex answers from its local files: a login its service has since revoked
// still shows as logged in, and runs then fail as unauthorized.
func LoginStatus(ctx context.Context, bin string, env []string) (Login, error) {
	out, err := run(ctx, bin, env, "login", "status")
	text := strings.ToLower(string(out))
	switch {
	case strings.Contains(text, "not logged in"):
		return Login{}, nil
	case strings.Contains(text, "logged in using chatgpt"):
		return Login{LoggedIn: true, Method: "chatgpt"}, nil
	case strings.Contains(text, "logged in using an api key"), strings.Contains(text, "logged in using api key"):
		return Login{LoggedIn: true, Method: "api_key"}, nil
	case strings.Contains(text, "logged in"):
		return Login{LoggedIn: true}, nil
	}
	if err != nil {
		return Login{}, err
	}
	return Login{}, ErrUnknownLogin
}
