// Package claudecli finds and queries the Claude Code (claude) installed on the user's machine: it only runs claude's
// own commands (--version, auth status, purge) and never reads, copies or stores its credentials (it touches neither
// the keychain nor the credential files in ~/.claude). What it uses is the user's own login on this machine.
package claudecli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"loop-now/internal/execenv"
	"loop-now/internal/procgroup"
)

// ErrNotFound means claude cannot be found in the given PATH.
var ErrNotFound = errors.New("claude is not on the login shell's PATH")

// commandTimeout is the time limit for claude's own short commands (--version, auth status, purge).
const commandTimeout = 30 * time.Second

// names are the executable names to look for: on Windows a native install is claude.exe and a global npm install is
// claude.cmd.
func names() []string {
	if runtime.GOOS == "windows" {
		return []string{"claude.exe", "claude.cmd"}
	}
	return []string{"claude"}
}

// Find looks for claude in dirs (the PATH of the user's login shell) in order and returns its absolute path with
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

// forbiddenEnv reports whether this environment variable must not be passed to claude: the platform only uses the
// local login and does not let any Anthropic/Claude Code variable (API key, token, config directory, nested-session
// marker and so on) change its authentication or configuration. Variable names are case-insensitive on Windows.
func forbiddenEnv(name string) bool {
	if runtime.GOOS == "windows" {
		name = strings.ToUpper(name)
	}
	return strings.HasPrefix(name, "ANTHROPIC_") || strings.HasPrefix(name, "CLAUDE")
}

// StripEnv removes the variables that must not be passed to claude from env (a KEY=VALUE list).
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

// EnvOptions is the environment for running claude's own short commands (--version, auth status, purge), with the
// same base variables as the Agent's Claude Code process.
type EnvOptions struct {
	Home string
	Tmp  string
	// User is USER and LOGNAME: claude uses it to find the local login; without it, auth status reports not logged in.
	User string
	Path []string
	// Extra are other base variables (K=V), such as the passed-through LANG, LC_ALL and TZ; they do not override the
	// fields above.
	Extra []string
}

// Env is the environment for running claude's own short commands: the system variables on Windows, the passed-through
// base variables, the real HOME, USER/LOGNAME and the given PATH, without any Anthropic/Claude variables.
func Env(o EnvOptions) []string {
	env := execenv.Base(o.Home, o.Tmp)
	for _, kv := range o.Extra {
		switch k, _, _ := strings.Cut(kv, "="); k {
		case "HOME", "PATH", "TMPDIR", "USER", "LOGNAME":
		default:
			env = append(env, kv)
		}
	}
	env = append(env, "HOME="+o.Home, "PATH="+strings.Join(o.Path, string(os.PathListSeparator)))
	if o.User != "" {
		env = append(env, "USER="+o.User, "LOGNAME="+o.User)
	}
	if o.Tmp != "" {
		env = append(env, "TMPDIR="+o.Tmp)
	}
	return StripEnv(env)
}

// run runs one short claude command and returns its standard output; on a non-zero exit status it also returns an
// error (the output is still returned: auth status exits with 1 when not logged in).
// The error only carries the exit status, never the output: the output of auth status contains the email and
// organization, which must not reach logs, events or error messages.
func run(ctx context.Context, bin string, env []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	cmd.Cancel = procgroup.Cancel(cmd)
	cmd.WaitDelay = 5 * time.Second
	procgroup.Configure(cmd)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && ctx.Err() == nil {
			return stdout.Bytes(), fmt.Errorf("claude %s failed: %s", args[0], exit.ProcessState.String())
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("claude %s timed out", args[0])
		}
		return nil, fmt.Errorf("claude %s could not run", args[0])
	}
	return stdout.Bytes(), nil
}

var versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+[0-9A-Za-z.+-]*`)

// Version runs claude --version and returns the version number (such as 2.1.322).
func Version(ctx context.Context, bin string, env []string) (string, error) {
	out, err := run(ctx, bin, env, "--version")
	if err != nil {
		return "", err
	}
	v := versionPattern.FindString(strings.TrimSpace(string(out)))
	if v == "" {
		return "", errors.New("the output of claude --version has no version number")
	}
	return v, nil
}

// Auth holds the fields of claude auth status --json that the platform uses. The other fields (email, orgId, orgName
// and so on) are dropped during decoding and never reach logs, events or error messages.
type Auth struct {
	LoggedIn          bool   `json:"loggedIn"`
	AuthMethod        string `json:"authMethod"`
	SubscriptionType  string `json:"subscriptionType"`
	LoginExpiresAt    string `json:"loginExpiresAt"`
	ProjectsDirectory string `json:"projectsDirectory"`
}

// AuthStatus runs claude auth status --json: claude itself decides whether it is logged in, and the platform never
// gets the token. When not logged in it exits with 1 but still prints JSON, so whenever the output is recognizable the
// result is based on the output; when it cannot run or the output is unrecognizable it returns an error (which callers
// treat as "unknown").
func AuthStatus(ctx context.Context, bin string, env []string) (Auth, error) {
	out, runErr := run(ctx, bin, env, "auth", "status", "--json")
	if len(bytes.TrimSpace(out)) == 0 && runErr != nil {
		return Auth{}, runErr
	}
	var raw struct {
		LoggedIn          *bool  `json:"loggedIn"`
		AuthMethod        string `json:"authMethod"`
		SubscriptionType  string `json:"subscriptionType"`
		LoginExpiresAt    string `json:"loginExpiresAt"`
		ProjectsDirectory string `json:"projectsDirectory"`
	}
	if json.Unmarshal(out, &raw) != nil || raw.LoggedIn == nil {
		clear(out)
		if runErr != nil {
			return Auth{}, runErr
		}
		return Auth{}, errors.New("the output of claude auth status is not the expected JSON")
	}
	clear(out)
	a := Auth{LoggedIn: *raw.LoggedIn, AuthMethod: raw.AuthMethod, SubscriptionType: raw.SubscriptionType, LoginExpiresAt: raw.LoginExpiresAt}
	if filepath.IsAbs(raw.ProjectsDirectory) {
		a.ProjectsDirectory = filepath.Clean(raw.ProjectsDirectory)
	}
	return a, nil
}

// Purge runs claude purge <path> -y: it deletes the session transcripts claude kept for this directory (only the data
// belonging to this path).
func Purge(ctx context.Context, bin string, env []string, path string) error {
	_, err := run(ctx, bin, env, "purge", path, "-y")
	return err
}

// DefaultProjectsDir is the directory where claude keeps session transcripts when CLAUDE_CONFIG_DIR is not set.
func DefaultProjectsDir(home string) string { return filepath.Join(home, ".claude", "projects") }

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// EncodeProjectDir is claude's rule for encoding a working directory path into a session transcript subdirectory
// name: every non-alphanumeric character becomes - (/a/b.c -> -a-b-c, C:\Users\x -> C--Users-x). Whether the path
// is resolved first is up to the caller; see ProjectDirNames.
func EncodeProjectDir(path string) string { return nonAlnum.ReplaceAllString(path, "-") }

// ProjectDirNames returns the session transcript subdirectory names the working directory cwd may map to: first the
// resolved one (symlinks; on Windows 8.3 short names are also expanded to long names), then the one as given; when
// the two are equal there is only one. On macOS claude was observed to use the resolved one (/tmp is recorded as
// /private/tmp); which one it uses on Windows has not been verified, so both are accepted. Callers only look at these
// one or two known paths and never scan.
func ProjectDirNames(cwd string) []string { return projectDirNames(cwd, filepath.EvalSymlinks) }

func projectDirNames(cwd string, resolve func(string) (string, error)) []string {
	raw := EncodeProjectDir(filepath.Clean(cwd))
	if real, err := resolve(cwd); err == nil {
		if resolved := EncodeProjectDir(filepath.Clean(real)); resolved != raw {
			return []string{resolved, raw}
		}
	}
	return []string{raw}
}

// ProjectDirName is the first of ProjectDirNames (the resolved one).
func ProjectDirName(cwd string) string { return ProjectDirNames(cwd)[0] }

// TranscriptPath returns the transcript of session id under working directory cwd in the projects directory: the
// first regular file that exists, in ProjectDirNames order, or empty when none exists. It only looks at these one or
// two files and never scans other directories.
func TranscriptPath(projects, cwd, id string) string {
	if !ValidSessionID(id) {
		return ""
	}
	for _, name := range ProjectDirNames(cwd) {
		p := filepath.Join(projects, name, id+".jsonl")
		if info, err := os.Lstat(p); err == nil && info.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

var sessionIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidSessionID reports whether id is a session ID the platform gives claude (a lowercase UUID).
func ValidSessionID(id string) bool { return sessionIDPattern.MatchString(id) }

// InProjects reports whether path is a <session ID>.jsonl in a direct subdirectory of the projects directory (where
// claude keeps session transcripts).
func InProjects(projects, path string) bool {
	rel, err := filepath.Rel(projects, path)
	if err != nil || filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
		return false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	return len(parts) == 2 && parts[0] != "." && parts[0] != ".." && strings.HasSuffix(parts[1], ".jsonl") && ValidSessionID(strings.TrimSuffix(parts[1], ".jsonl"))
}

// Probe caches claude's version (keyed by the executable's path, size and modification time) and its projects
// directory, so they are not run again on every start.
type Probe struct {
	mu       sync.Mutex
	versions map[string]string
	projects map[string]string
}

func fileKey(bin string) string {
	info, err := os.Stat(bin)
	if err != nil {
		return bin
	}
	return fmt.Sprintf("%s|%d|%d", bin, info.Size(), info.ModTime().UnixNano())
}

// Version returns the version number of bin, cached by file identity.
func (p *Probe) Version(ctx context.Context, bin string, env []string) (string, error) {
	key := fileKey(bin)
	p.mu.Lock()
	v, ok := p.versions[key]
	p.mu.Unlock()
	if ok {
		return v, nil
	}
	v, err := Version(ctx, bin, env)
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	if p.versions == nil {
		p.versions = map[string]string{}
	}
	p.versions[key] = v
	p.mu.Unlock()
	return v, nil
}

// ProjectsDir returns the directory where claude keeps session transcripts: the projectsDirectory reported by claude
// auth status wins, and when that is unavailable it is derived by the default rule.
func (p *Probe) ProjectsDir(ctx context.Context, bin string, env []string, home string) string {
	key := fileKey(bin) + "|" + home
	p.mu.Lock()
	dir, ok := p.projects[key]
	p.mu.Unlock()
	if ok {
		return dir
	}
	dir = DefaultProjectsDir(home)
	if a, err := AuthStatus(ctx, bin, env); err == nil && a.ProjectsDirectory != "" {
		dir = a.ProjectsDirectory
	}
	p.mu.Lock()
	if p.projects == nil {
		p.projects = map[string]string{}
	}
	p.projects[key] = dir
	p.mu.Unlock()
	return dir
}
