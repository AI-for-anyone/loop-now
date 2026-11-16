// Package conn handles loopctl's identity selection, connection settings and HTTP client (tokens, TLS, version
// headers, no redirects, retries for read requests).
package conn

import (
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"loop-now/internal/loopctl/config"
	"loop-now/internal/loopctl/output"
)

// Environment variables.
const (
	EnvAgentURL         = "LOOPCTL_AGENT_URL"
	EnvAgentToken       = "LOOPCTL_AGENT_TOKEN"
	EnvProfile          = "LOOPCTL_PROFILE"
	EnvURL              = "LOOPCTL_URL"
	EnvToken            = "LOOPCTL_TOKEN"
	EnvCAFile           = "LOOPCTL_CA_FILE"
	EnvTimeout          = "LOOPCTL_TIMEOUT"
	EnvMaxResponse      = "LOOPCTL_MAX_RESPONSE"
	EnvRetryMax         = "LOOPCTL_RETRY_MAX"
	EnvRetryInitial     = "LOOPCTL_RETRY_INITIAL"
	EnvRetryMaxInterval = "LOOPCTL_RETRY_MAX_INTERVAL"
)

// Identities. Same values as cli.Identity.
const (
	IdentityAgent    = "agent"
	IdentityExternal = "external"
)

// Token prefixes.
const (
	AgentTokenPrefix    = "lna_"
	ExternalTokenPrefix = "lne_"
)

// Defaults.
const (
	DefaultTimeout          = 60 * time.Second
	DefaultMaxResponse      = 64 << 20
	DefaultRetryMax         = 2
	DefaultRetryInitial     = 500 * time.Millisecond
	DefaultRetryMaxInterval = 5 * time.Second
)

// EnvDoc describes one environment variable for the schema output.
type EnvDoc struct {
	Name        string `json:"name"`
	Identity    string `json:"identity,omitempty"` // empty means both identities use it
	Default     string `json:"default,omitempty"`
	Description string `json:"description"`
}

// EnvDocs returns every environment variable loopctl reads.
func EnvDocs() []EnvDoc {
	return []EnvDoc{
		{EnvAgentURL, IdentityAgent, "", "Base address of the Agent API, injected by the server, for example http://127.0.0.1:<port>/agent/v1"},
		{EnvAgentToken, IdentityAgent, "", "Agent token (starting with lna_), injected by the server; when set, the Agent identity is used and the config file is not read"},
		{EnvProfile, IdentityExternal, config.DefaultProfile, "Choose a profile from the config file"},
		{EnvURL, IdentityExternal, "", "Root address of the external port (https://); takes precedence over the profile"},
		{EnvToken, IdentityExternal, "", "External token (starting with lne_); takes precedence over the profile"},
		{EnvCAFile, IdentityExternal, "", "CA certificate in PEM format; when set, only the CAs in it are trusted; takes precedence over the profile"},
		{EnvTimeout, "", DefaultTimeout.String(), "Timeout for a single request"},
		{EnvMaxResponse, "", "64MiB", "Size limit for JSON responses; file downloads are not subject to it"},
		{EnvRetryMax, "", strconv.Itoa(DefaultRetryMax), "Maximum number of retries for read requests after a connection failure, a timeout or 502/503/504; 0 means no retries"},
		{EnvRetryInitial, "", DefaultRetryInitial.String(), "Wait before the first retry; it doubles for each later retry"},
		{EnvRetryMaxInterval, "", DefaultRetryMaxInterval.String(), "Longest wait between two retries"},
	}
}

// DetectIdentity determines the identity from the environment alone: if there is an Agent token it is the Agent
// identity, otherwise the external identity.
func DetectIdentity(getenv func(string) string) string {
	if strings.TrimSpace(getenv(EnvAgentToken)) != "" {
		return IdentityAgent
	}
	return IdentityExternal
}

// Options is the input for resolving connection settings. An empty string means not given.
type Options struct {
	Getenv     func(string) string
	ConfigPath string
	Profile    string        // --profile
	Server     string        // --server
	CAFile     string        // --ca-file
	Timeout    time.Duration // --timeout; 0 means not given
}

// RetryPolicy holds the retry parameters for read requests.
type RetryPolicy struct {
	Max         int
	Initial     time.Duration
	MaxInterval time.Duration
}

// Settings are the resolved connection settings. The token is not exported so that it cannot accidentally end up
// in the output.
type Settings struct {
	Identity    string
	Base        *url.URL // API base address: LOOPCTL_AGENT_URL for the Agent, <root address>/api/v1 for external
	Root        *url.URL // root address of the server for the external identity
	Profile     string   // name of the profile when one from the config file was used
	CAFile      string
	Timeout     time.Duration
	MaxResponse int64
	Retry       RetryPolicy
	token       string
	roots       *x509.CertPool
}

// String hides the token so that formatting Settings does not leak it.
func (s *Settings) String() string {
	return fmt.Sprintf("{Identity:%s Base:%v Profile:%s CAFile:%s Timeout:%s token:<hidden>}", s.Identity, s.Base, s.Profile, s.CAFile, s.Timeout)
}

// GoString is the same as String.
func (s *Settings) GoString() string { return s.String() }

// Resolve selects the identity and resolves the connection settings.
func Resolve(o Options) (*Settings, *output.Error) {
	s := &Settings{}
	if e := runtimeParams(o, s); e != nil {
		return nil, e
	}
	if DetectIdentity(o.Getenv) == IdentityAgent {
		return s, resolveAgent(o, s)
	}
	return s, resolveExternal(o, s)
}

func resolveAgent(o Options, s *Settings) *output.Error {
	s.Identity = IdentityAgent
	var conflicts []string
	for _, name := range []string{EnvURL, EnvToken, EnvCAFile, EnvProfile} {
		if o.Getenv(name) != "" {
			conflicts = append(conflicts, name)
		}
	}
	for flag, v := range map[string]string{"--server": o.Server, "--ca-file": o.CAFile, "--profile": o.Profile} {
		if v != "" {
			conflicts = append(conflicts, flag)
		}
	}
	if len(conflicts) > 0 {
		sortStrings(conflicts)
		return output.Errorf(output.CodeIdentityConflict, "%s is set in the environment, so the Agent identity is used. The Agent identity cannot also use external identity parameters: %s", EnvAgentToken, strings.Join(conflicts, ", ")).
			WithDetails(map[string]any{"conflicts": conflicts})
	}
	tok := strings.TrimSpace(o.Getenv(EnvAgentToken))
	if !strings.HasPrefix(tok, AgentTokenPrefix) {
		return output.Errorf(output.CodeTokenKindMismatch, "%s does not hold an Agent token (it should start with %s).", EnvAgentToken, AgentTokenPrefix)
	}
	raw := strings.TrimSpace(o.Getenv(EnvAgentURL))
	if raw == "" {
		return output.Errorf(output.CodeConnectionNotConfigured, "%s is set, but %s is not.", EnvAgentToken, EnvAgentURL)
	}
	u, e := ParseServerURL(raw, false)
	if e != nil {
		return e
	}
	s.Base, s.token = u, tok
	return nil
}

func resolveExternal(o Options, s *Settings) *output.Error {
	s.Identity = IdentityExternal
	name, explicit := config.DefaultProfile, ""
	if o.Profile != "" {
		name, explicit = o.Profile, "--profile"
	} else if v := o.Getenv(EnvProfile); v != "" {
		name, explicit = v, EnvProfile
	}
	f, _, e := config.Load(o.ConfigPath)
	if e != nil {
		return e
	}
	p, ok := f.Profiles[name]
	if !ok && explicit != "" {
		return output.Errorf(output.CodeProfileNotFound, "The config file has no profile %q (chosen with %s).", name, explicit)
	}
	if ok {
		s.Profile = name
	}
	tok := firstNonEmpty(strings.TrimSpace(o.Getenv(EnvToken)), p.Token)
	server := firstNonEmpty(o.Server, o.Getenv(EnvURL), p.Server)
	ca := firstNonEmpty(o.CAFile, o.Getenv(EnvCAFile), p.CAFile)
	if tok == "" {
		return output.Errorf(output.CodeConnectionNotConfigured,
			"No token is available. An Agent running in the platform needs %s; an external identity needs %s or a token saved to the config file with loopctl config set <profile> --token-stdin.", EnvAgentToken, EnvToken)
	}
	if !strings.HasPrefix(tok, ExternalTokenPrefix) {
		return output.Errorf(output.CodeTokenKindMismatch, "An external identity's token must start with %s.", ExternalTokenPrefix)
	}
	if server == "" {
		return output.Errorf(output.CodeConnectionNotConfigured, "No server address is set. Set it with --server, %s or the profile's server.", EnvURL)
	}
	root, e := ParseServerURL(server, true)
	if e != nil {
		return e
	}
	base := *root
	base.Path = root.Path + "/api/v1"
	s.Root, s.Base, s.token = root, &base, tok
	if ca != "" {
		pool, e := LoadCA(ca)
		if e != nil {
			return e
		}
		s.CAFile, s.roots = ca, pool
	}
	return nil
}

// runtimeParams resolves the timeout, the response size limit and the retry parameters; they apply to both
// identities.
func runtimeParams(o Options, s *Settings) *output.Error {
	s.Timeout = DefaultTimeout
	if o.Timeout > 0 {
		s.Timeout = o.Timeout
	} else if v := o.Getenv(EnvTimeout); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return output.Errorf(output.CodeConfigInvalid, "%s must be a positive duration, for example 30s.", EnvTimeout)
		}
		s.Timeout = d
	}
	s.MaxResponse = DefaultMaxResponse
	if v := o.Getenv(EnvMaxResponse); v != "" {
		n, ok := ParseSize(v)
		if !ok || n <= 0 {
			return output.Errorf(output.CodeConfigInvalid, "%s must be a positive number of bytes, for example 64MiB.", EnvMaxResponse)
		}
		s.MaxResponse = n
	}
	s.Retry = RetryPolicy{DefaultRetryMax, DefaultRetryInitial, DefaultRetryMaxInterval}
	if v := o.Getenv(EnvRetryMax); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return output.Errorf(output.CodeConfigInvalid, "%s must be an integer of at least 0.", EnvRetryMax)
		}
		s.Retry.Max = n
	}
	for _, x := range []struct {
		name string
		dst  *time.Duration
	}{{EnvRetryInitial, &s.Retry.Initial}, {EnvRetryMaxInterval, &s.Retry.MaxInterval}} {
		if v := o.Getenv(x.name); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				return output.Errorf(output.CodeConfigInvalid, "%s must be a positive duration, for example 500ms.", x.name)
			}
			*x.dst = d
		}
	}
	return nil
}

var sizePattern = regexp.MustCompile(`^(\d+)(B|KiB|MiB|GiB)?$`)

// ParseSize parses a byte count; the suffixes B, KiB, MiB and GiB are supported.
func ParseSize(v string) (int64, bool) {
	m := sizePattern.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, false
	}
	shift := map[string]uint{"": 0, "B": 0, "KiB": 10, "MiB": 20, "GiB": 30}[m[2]]
	if n > (1<<62)>>shift {
		return 0, false
	}
	return n << shift, true
}

// ParseServerURL checks a server address: only http and https are allowed; http is only allowed for loopback
// hosts; with requireHTTPS only https is allowed.
// Error messages do not echo the original value, so that any credentials it may contain are not leaked.
func ParseServerURL(raw string, requireHTTPS bool) (*url.URL, *output.Error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.Opaque != "" {
		return nil, output.Errorf(output.CodeInvalidServerURL, "The server address is not a valid http(s) address.")
	}
	if u.User != nil {
		return nil, output.Errorf(output.CodeInvalidServerURL, "The server address must not contain a user name or secret.")
	}
	if u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return nil, output.Errorf(output.CodeInvalidServerURL, "The server address must not contain query parameters or a fragment.")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		if requireHTTPS {
			return nil, output.Errorf(output.CodeInvalidServerURL, "An external identity must use an https:// address (the external port serves only TLS).")
		}
		if !IsLoopbackHost(u.Hostname()) {
			return nil, output.Errorf(output.CodeInvalidServerURL, "http:// is allowed only for loopback addresses, and %s is not a loopback address.", u.Hostname())
		}
	default:
		return nil, output.Errorf(output.CodeInvalidServerURL, "The server address must be an https:// address.")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u, nil
}

// IsLoopbackHost reports whether the host name is a loopback address.
func IsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// LoadCA reads CA certificates in PEM format; there must be at least one.
func LoadCA(path string) (*x509.CertPool, *output.Error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, output.Errorf(output.CodeCAFileUnreadable, "Could not read CA file %s: %v", path, unwrapPath(err))
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(b) {
		return nil, output.Errorf(output.CodeCAFileUnreadable, "CA file %s contains no certificate in PEM format.", path)
	}
	return pool, nil
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
