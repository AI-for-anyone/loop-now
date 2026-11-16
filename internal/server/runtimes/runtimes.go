// Package runtimes reports whether the Agent runtimes are available on this machine (GET /runtimes): the bundled Pi is
// always available; for Claude Code it checks whether claude is on the login shell's PATH, whether its version can be
// read, and the login state as answered by claude itself (the platform does not read its credentials, it only takes
// whether it is logged in, the login method and the subscription type).
package runtimes

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/claudecli"
)

// cacheFor is how long check results are cached; refresh checks again.
const cacheFor = 30 * time.Second

// Reasons for being unavailable.
const (
	ReasonNotFound      = "not_found"
	ReasonVersionFailed = "version_failed"
)

// Login states.
const (
	LoginLoggedIn  = "logged_in"
	LoginLoggedOut = "logged_out"
	LoginUnknown   = "unknown"
)

// Status corresponds to RuntimeStatus in the OpenAPI spec.
type Status struct {
	ID                string    `json:"id"`
	Available         bool      `json:"available"`
	Version           *string   `json:"version"`
	BinaryPath        *string   `json:"binaryPath"`
	UnavailableReason *string   `json:"unavailableReason"`
	Login             *Login    `json:"login"`
	CheckedAt         time.Time `json:"checkedAt"`
}

// Login is the login state reported by Claude Code itself; it has no email, organization or similar details.
type Login struct {
	State            string  `json:"state"`
	AuthMethod       *string `json:"authMethod"`
	SubscriptionType *string `json:"subscriptionType"`
}

// Checker checks each runtime. The zero value is not usable: PiVersion must be set; when UserPath or RealHome is nil,
// Claude Code is reported as not found.
type Checker struct {
	PiVersion    func() string
	UserPath     func(context.Context) []string
	RealHome     func() (string, error)
	ExecBasePath string
	// HelperEnv is the environment for running claude's own short commands (the same base variables the executor
	// gives the Agent's Claude Code process, including USER);
	// when nil, there are only HOME and PATH.
	HelperEnv func(ctx context.Context, home string) []string
	// ClaudeBin is the claude executable (for tests); when empty it is looked up in UserPath.
	ClaudeBin string
	Now       func() time.Time

	probe  claudecli.Probe
	mu     sync.Mutex
	cached []Status
	at     time.Time
}

// List returns the status of each runtime; when refresh is false, results within the cache duration are reused.
func (c *Checker) List(ctx context.Context, refresh bool) []Status {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	c.mu.Lock()
	if !refresh && c.cached != nil && now().Sub(c.at) < cacheFor {
		out := append([]Status(nil), c.cached...)
		c.mu.Unlock()
		return out
	}
	c.mu.Unlock()
	at := now().UTC()
	out := []Status{c.pi(at), c.claude(ctx, at)}
	c.mu.Lock()
	c.cached, c.at = out, now()
	c.mu.Unlock()
	return append([]Status(nil), out...)
}

func (c *Checker) pi(at time.Time) Status {
	s := Status{ID: agentruntime.Pi, Available: true, CheckedAt: at}
	if c.PiVersion != nil {
		if v := c.PiVersion(); v != "" {
			s.Version = &v
		}
	}
	return s
}

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
