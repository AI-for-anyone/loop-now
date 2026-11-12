// Package runtimes 报告 Agent 运行时在本机是否可用（GET /runtimes）：随包的 Pi 总是可用；Claude Code 看登录 shell 的 PATH 里
// 有没有 claude、能否读出版本，以及由 claude 自己回答的登录状态（平台不读它的凭证，只取是否登录、登录方式与订阅类型）。
package runtimes

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/claudecli"
)

// cacheFor 是检查结果的缓存时长；refresh 时重新检查。
const cacheFor = 30 * time.Second

// 不可用的原因。
const (
	ReasonNotFound      = "not_found"
	ReasonVersionFailed = "version_failed"
)

// 登录状态。
const (
	LoginLoggedIn  = "logged_in"
	LoginLoggedOut = "logged_out"
	LoginUnknown   = "unknown"
)

// Status 对应 OpenAPI 的 RuntimeStatus。
type Status struct {
	ID                string    `json:"id"`
	Available         bool      `json:"available"`
	Version           *string   `json:"version"`
	BinaryPath        *string   `json:"binaryPath"`
	UnavailableReason *string   `json:"unavailableReason"`
	Login             *Login    `json:"login"`
	CheckedAt         time.Time `json:"checkedAt"`
}

// Login 是 Claude Code 自己报告的登录状态；没有邮箱与组织等信息。
type Login struct {
	State            string  `json:"state"`
	AuthMethod       *string `json:"authMethod"`
	SubscriptionType *string `json:"subscriptionType"`
}

// Checker 检查各运行时。零值不可用：须给出 PiVersion；UserPath、RealHome 为空时 Claude Code 报找不到。
type Checker struct {
	PiVersion    func() string
	UserPath     func(context.Context) []string
	RealHome     func() (string, error)
	ExecBasePath string
	// HelperEnv 是运行 claude 自己的短命令时的环境（与执行器给 Agent 的 Claude Code 进程同一套基础变量，含 USER）；
	// 为空时只有 HOME 与 PATH。
	HelperEnv func(ctx context.Context, home string) []string
	// ClaudeBin 是 claude 可执行文件（测试用）；为空时在 UserPath 里找。
	ClaudeBin string
	Now       func() time.Time

	probe  claudecli.Probe
	mu     sync.Mutex
	cached []Status
	at     time.Time
}

// List 返回各运行时的状态；refresh 为假时用缓存时长内的结果。
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
