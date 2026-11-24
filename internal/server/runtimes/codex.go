package runtimes

import (
	"context"
	"time"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/codexcli"
)

// codex checks the Codex on this computer: codex on the login shell's PATH, its version (older than the runtime's
// minimum is unsupported), and the login state as codex login status answers it. That answer comes from Codex's local
// files: a login its service has since revoked still shows as logged in.
func (c *Checker) codex(ctx context.Context, at time.Time) Status {
	s := Status{ID: agentruntime.Codex, CheckedAt: at}
	reason := func(r string) Status {
		s.UnavailableReason = &r
		return s
	}
	bin := c.CodexBin
	if bin == "" {
		var dirs []string
		if c.UserPath != nil {
			dirs = c.UserPath(ctx)
		}
		var err error
		if bin, err = codexcli.Find(dirs); err != nil {
			return reason(ReasonNotFound)
		}
	}
	var env []string
	if c.CodexEnv != nil {
		env = c.CodexEnv(ctx)
	}
	if env == nil {
		return reason(ReasonNotFound)
	}
	s.BinaryPath = &bin
	version, err := codexcli.Version(ctx, bin, env)
	if err != nil {
		return reason(ReasonVersionFailed)
	}
	s.Version = &version
	if rt, _ := agentruntime.ByID(agentruntime.Codex); !codexcli.AtLeast(version, rt.MinVersion) {
		return reason(ReasonVersionUnsupported)
	}
	s.Available = true
	login := &Login{State: LoginUnknown}
	if l, err := codexcli.LoginStatus(ctx, bin, env); err == nil {
		login.State = LoginLoggedOut
		if l.LoggedIn {
			login.State = LoginLoggedIn
		}
		if l.Method != "" {
			login.AuthMethod = &l.Method
		}
	}
	s.Login = login
	return s
}
