package runtimes

import (
	"context"
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
	switch cli.login(ctx, bin, env) {
	case acpcli.LoggedIn:
		state = LoginLoggedIn
	case acpcli.LoggedOut:
		state = LoginLoggedOut
	}
	s.Login = &Login{State: state}
	return s
}
