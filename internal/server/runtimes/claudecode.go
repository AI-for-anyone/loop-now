package runtimes

import (
	"context"
	"path/filepath"
	"time"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/claudecli"
)

// claude checks the Claude Code on this computer: claude on the login shell's PATH, its version, and the login state
// as answered by claude itself.
func (c *Checker) claude(ctx context.Context, at time.Time) Status {
	s := Status{ID: agentruntime.ClaudeCode, CheckedAt: at}
	reason := func(r string) Status {
		s.UnavailableReason = &r
		return s
	}
	var dirs []string
	if c.UserPath != nil {
		dirs = c.UserPath(ctx)
	}
	bin := c.ClaudeBin
	if bin == "" {
		var err error
		if bin, err = claudecli.Find(dirs); err != nil {
			return reason(ReasonNotFound)
		}
	}
	home := ""
	if c.RealHome != nil {
		if h, err := c.RealHome(); err == nil && filepath.IsAbs(h) {
			home = h
		}
	}
	if home == "" {
		return reason(ReasonNotFound)
	}
	s.BinaryPath = &bin
	var env []string
	if c.HelperEnv != nil {
		env = c.HelperEnv(ctx, home)
	} else {
		env = claudecli.Env(claudecli.EnvOptions{Home: home, Path: append(filepath.SplitList(c.ExecBasePath), dirs...)})
	}
	version, err := c.probe.Version(ctx, bin, env)
	if err != nil {
		return reason(ReasonVersionFailed)
	}
	s.Available, s.Version = true, &version
	login := &Login{State: LoginUnknown}
	if a, err := claudecli.AuthStatus(ctx, bin, env); err == nil {
		login.State = LoginLoggedOut
		if a.LoggedIn {
			login.State = LoginLoggedIn
		}
		if a.AuthMethod != "" {
			login.AuthMethod = &a.AuthMethod
		}
		if a.SubscriptionType != "" {
			login.SubscriptionType = &a.SubscriptionType
		}
	}
	s.Login = login
	return s
}
