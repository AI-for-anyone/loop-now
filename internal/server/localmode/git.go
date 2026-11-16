package localmode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"loop-now/internal/execenv"
	"loop-now/internal/procgroup"
)

// GitIdentityEnv returns the environment used by "Pull from Git" while "Use local git identity" is on: the real HOME of
// the user running the server process (plus the matching USERPROFILE, APPDATA etc. on Windows), SSH_AUTH_SOCK and
// XDG_CONFIG_HOME, so the user's git config, credential helper, SSH keys and known_hosts all work; PATH is the
// execution PATH followed by the user's PATH (so helpers installed in the user's directories are found).
//
// The pull runs in the background where nobody can answer prompts, so all interaction is turned off: keep
// GIT_TERMINAL_PROMPT=0; GIT_ASKPASS is set empty so git no longer falls back to core.askPass and SSH_ASKPASS;
// GCM_INTERACTIVE=never makes Git Credential Manager use only stored credentials; SSH runs in BatchMode, never asks for
// a passphrase and never confirms unknown hosts on the user's behalf (it fails right away with "Host key verification
// failed"). An ssh command the user configured (GIT_SSH_COMMAND or core.sshCommand) is used as is, with only -o
// BatchMode=yes appended.
// dir is the working directory for querying git config and, as when cloning, should not be inside any repository.
func GitIdentityEnv(ctx context.Context, gitBin, basePath string, userPath []string, dir string) ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("read the home directory of the server process user: %w", err)
	}
	dirs := filepath.SplitList(basePath)
	seen := map[string]bool{}
	for _, d := range dirs {
		seen[execenv.PathKey(d)] = true
	}
	for _, d := range userPath {
		if k := execenv.PathKey(d); !seen[k] {
			seen[k] = true
			dirs = append(dirs, d)
		}
	}
	env := execenv.Base(home, os.TempDir())
	env = append(env, "HOME="+home, "PATH="+strings.Join(dirs, string(os.PathListSeparator)),
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "GCM_INTERACTIVE=never")
	for _, k := range []string{"SSH_AUTH_SOCK", "XDG_CONFIG_HOME"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return append(env, "GIT_SSH_COMMAND="+sshCommand(ctx, gitBin, env, dir)+" -o BatchMode=yes"), nil
}

// sshCommand returns the ssh command the user configured for git: GIT_SSH_COMMAND in the server process environment,
// then core.sshCommand from git config, and ssh when neither is set.
func sshCommand(ctx context.Context, gitBin string, env []string, dir string) string {
	if v := strings.TrimSpace(os.Getenv("GIT_SSH_COMMAND")); v != "" {
		return v
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, gitBin, "config", "--get", "core.sshCommand")
	cmd.Env = env
	cmd.Dir = dir
	cmd.Cancel = procgroup.Cancel(cmd)
	cmd.WaitDelay = time.Second
	if out, err := procgroup.Output(cmd); err == nil {
		if v := strings.TrimSpace(string(out)); v != "" {
			return v
		}
	}
	return "ssh"
}
