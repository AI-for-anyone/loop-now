package runtimes

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"loop-now/internal/server/acpcli"
	"loop-now/internal/server/agentruntime"
)

// acpCLIs are, per ACP runtime, its executable's name, the explicit executable (for tests) and how its login state is
// read: grok models says it in its first line; agy models fails without a login.
var acpCLIs = map[string]struct {
	name  string
	bin   func(*Checker) string
	login func(ctx context.Context, bin string, env []string) string
}{
	agentruntime.Grok:        {"grok", func(c *Checker) string { return c.GrokBin }, acpcli.GrokLogin},
	agentruntime.Antigravity: {"agy", func(c *Checker) string { return c.AntigravityBin }, acpcli.AntigravityLogin},
}

// acp checks an Agent Client Protocol runtime on this computer (Grok, Antigravity): its CLI on the login shell's PATH,
// its version (older than the runtime's minimum is unsupported), and the login state as the CLI answers it.
func (c *Checker) acp(ctx context.Context, at time.Time, id string) Status {
	s := Status{ID: id, CheckedAt: at}
	reason := func(r string) Status {
		s.UnavailableReason = &r
		return s
	}
	cli := acpCLIs[id]
	bin := cli.bin(c)
	if bin == "" {
		var dirs []string
		if c.UserPath != nil {
			dirs = c.UserPath(ctx)
		}
		var err error
		if bin, err = acpcli.Find(cli.name, dirs); err != nil {
			return reason(ReasonNotFound)
		}
	}
	var env []string
	if c.ACPEnv != nil {
		env = c.ACPEnv(ctx)
	}
	if env == nil {
		return reason(ReasonNotFound)
	}
	s.BinaryPath = &bin
	version, err := acpcli.Version(ctx, bin, env)
	if err != nil {
		return reason(ReasonVersionFailed)
	}
	s.Version = &version
	if rt, _ := agentruntime.ByID(id); !acpcli.AtLeast(version, rt.MinVersion) {
		return reason(ReasonVersionUnsupported)
	}
	s.Available = true
	state := LoginUnknown
	conclusion := ""
	if c.ACPLogin != nil {
		conclusion = c.ACPLogin(id) // kept by the background read of the model list: nothing goes online here
	} else {
		conclusion = cli.login(ctx, bin, env)
	}
	switch conclusion {
	case acpcli.LoggedIn:
		state = LoginLoggedIn
	case acpcli.LoggedOut:
		state = LoginLoggedOut
	}
	s.Login = &Login{State: state}
	return s
}

// localCommand checks a local ACP command a person added: available when its executable is there (an absolute path,
// or a name on the login shell's PATH). Its version and login are not checked: not every program answers --version,
// and ACP tells the login only once a session is open.
func (c *Checker) localCommand(ctx context.Context, at time.Time, rt agentruntime.Runtime) Status {
	s := Status{ID: rt.ID, CheckedAt: at}
	bin := rt.Command.Command
	if filepath.IsAbs(bin) {
		if info, err := os.Stat(bin); err != nil || !info.Mode().IsRegular() {
			r := ReasonNotFound
			s.UnavailableReason = &r
			return s
		}
	} else {
		var dirs []string
		if c.UserPath != nil {
			dirs = c.UserPath(ctx)
		}
		found, err := acpcli.Find(bin, dirs)
		if err != nil {
			r := ReasonNotFound
			s.UnavailableReason = &r
			return s
		}
		bin = found
	}
	s.BinaryPath, s.Available = &bin, true
	s.Login = &Login{State: LoginUnknown}
	return s
}
