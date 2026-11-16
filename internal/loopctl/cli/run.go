package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"strconv"
	"time"

	"loop-now/internal/loopctl/config"
	"loop-now/internal/loopctl/conn"
	"loop-now/internal/loopctl/output"
)

// System is everything loopctl touches in the outside world; tests can replace it as a whole.
type System struct {
	Stdin     io.Reader
	Stdout    io.Writer
	Getenv    func(string) string
	HomeDir   func() (string, error)
	Version   string
	GOOS      string
	GOARCH    string
	GoVersion string
	Dial      conn.DialFunc                                    // when nil, dials for real
	Sleep     func(ctx context.Context, d time.Duration) error // when nil, really waits
}

// OSSystem returns a System that uses the real process environment.
func OSSystem(version string) *System {
	return &System{
		Stdin:     os.Stdin,
		Stdout:    os.Stdout,
		Getenv:    os.Getenv,
		HomeDir:   os.UserHomeDir,
		Version:   version,
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
		GoVersion: runtime.Version(),
	}
}

// ConfigPath returns the path of the local connection config file.
func (s *System) ConfigPath() (string, *output.Error) {
	home, err := s.HomeDir()
	if err != nil || home == "" {
		return "", output.Errorf(output.CodeConfigInvalid, "Could not determine the user's home directory, so the local connection config cannot be found.")
	}
	return config.Path(home), nil
}

// Invocation is one command call: the parsed arguments, the environment it runs in and the client that connects
// to the server.
type Invocation struct {
	Cmd          *Command
	Args         []string
	Registry     *Registry
	Sys          *System
	Identity     Identity     // identity determined from the environment
	Client       *conn.Client // only set for commands that connect to the server
	Meta         *output.Meta // commands may add to meta (for example the profile a local command selected)
	flags        map[string]string
	repeated     map[string][]string
	accessed     map[string]bool
	secretOutput *secretOutput
}

// Has reports whether the option appeared on the command line.
func (inv *Invocation) Has(name string) bool {
	inv.accessed[name] = true
	_, ok := inv.flags[name]
	return ok
}

// String returns the option's value, or its declared default when it did not appear.
func (inv *Invocation) String(name string) string {
	inv.accessed[name] = true
	if v, ok := inv.flags[name]; ok {
		return v
	}
	if f, ok := inv.Cmd.flag(name); ok {
		return f.Default
	}
	return ""
}

// Strings returns all explicit values in order, or the declared default if absent.
// Return a copy so command handlers cannot mutate parsed option values.
func (inv *Invocation) Strings(name string) []string {
	inv.accessed[name] = true
	if values, ok := inv.repeated[name]; ok {
		return append([]string(nil), values...)
	}
	if value, ok := inv.flags[name]; ok {
		return []string{value}
	}
	if f, ok := inv.Cmd.flag(name); ok && f.Default != "" {
		return []string{f.Default}
	}
	return nil
}

// Bool returns a boolean option.
func (inv *Invocation) Bool(name string) bool { return inv.String(name) == "true" }

// Int returns an integer option; its format was already checked during parsing.
func (inv *Invocation) Int(name string) int {
	n, _ := strconv.Atoi(inv.String(name))
	return n
}

// Accessed returns the names of the options read while running, so tests can check that every declared option
// is used.
func (inv *Invocation) Accessed() map[string]bool { return inv.accessed }

// Hook is called after a command has run so tests can check call details; it is nil in production code.
var Hook func(inv *Invocation)

// Run executes one loopctl call, writes one JSON object to sys.Stdout and returns the exit code.
func Run(ctx context.Context, r *Registry, argv []string, sys *System) (code int) {
	meta := output.Meta{ClientVersion: sys.Version}
	jsonOutput := &secretOutput{Writer: sys.Stdout}
	var client *conn.Client
	fail := func(e *output.Error) int {
		var warnings []output.Warning
		if client != nil {
			warnings = clientMeta(client, &meta)
		}
		_ = output.WriteFailure(jsonOutput, e, warnings, meta)
		return output.ExitCode(e.Category)
	}
	defer func() {
		if p := recover(); p != nil {
			code = fail(output.Errorf(output.CodeInternalError, "loopctl internal error: %v", p))
		}
	}()

	p, e := parse(r, argv)
	if e != nil {
		return fail(e)
	}
	if p.help {
		data, e := schemaFor(r, sys, p.path, "")
		if e != nil {
			return fail(e)
		}
		_ = output.WriteSuccess(jsonOutput, data, nil, meta)
		return 0
	}
	c := p.cmd
	inv := &Invocation{Cmd: c, Args: p.args, Registry: r, Sys: sys, Meta: &meta, flags: p.flags, repeated: p.repeated, accessed: map[string]bool{}, secretOutput: jsonOutput}
	if len(c.Identities) > 0 {
		inv.Identity = Identity(conn.DetectIdentity(sys.Getenv))
		if !c.AllowedFor(inv.Identity) {
			return fail(output.Errorf(output.CodeCommandNotAllowed, "The %s identity cannot use the %s command.", inv.Identity, c.Name()).
				WithDetails(map[string]any{"identity": inv.Identity, "allowed": c.Identities}))
		}
	}
	if c.Destructive && !inv.Bool(YesFlag.Name) {
		return fail(output.Errorf(output.CodeConfirmationRequired, "%s deletes or overwrites data, and loopctl never asks interactively. Confirm with --yes.", c.Name()))
	}
	if c.Network {
		s, e := resolve(inv)
		if e != nil {
			return fail(e)
		}
		meta.Identity, meta.Profile = s.Identity, s.Profile
		client = conn.NewClient(s, conn.ClientOptions{Version: sys.Version, GOOS: sys.GOOS, GOARCH: sys.GOARCH, Dial: sys.Dial, Sleep: sys.Sleep})
		inv.Client = client
	}
	data, err := c.Run(ctx, inv)
	if Hook != nil {
		Hook(inv)
	}
	if err != nil {
		var oe *output.Error
		if !errors.As(err, &oe) {
			oe = output.Errorf(output.CodeInternalError, "%v", err)
		}
		return fail(oe)
	}
	var warnings []output.Warning
	if client != nil {
		warnings = clientMeta(client, &meta)
	}
	if err := output.WriteSuccess(jsonOutput, data, warnings, meta); err != nil {
		return fail(output.Errorf(output.CodeInternalError, "Could not encode the output: %v", err))
	}
	return 0
}

// Check parses without running: it checks that the command line can be parsed and that the identity may use the
// command. It is used by the tests of the built-in Skill.
func Check(r *Registry, argv []string, id Identity) *output.Error {
	p, e := parse(r, argv)
	if e != nil {
		return e
	}
	if p.help || p.cmd == nil {
		return output.Errorf(output.CodeUnknownCommand, "This is not a runnable command.")
	}
	if !p.cmd.AllowedFor(id) {
		return output.Errorf(output.CodeCommandNotAllowed, "The %s identity cannot use the %s command.", id, p.cmd.Name())
	}
	return nil
}

// resolve resolves the connection settings.
func resolve(inv *Invocation) (*conn.Settings, *output.Error) {
	o := conn.Options{
		Getenv:  inv.Sys.Getenv,
		Profile: inv.String("profile"),
		Server:  inv.String("server"),
		CAFile:  inv.String("ca-file"),
	}
	if v := inv.String("timeout"); v != "" {
		o.Timeout, _ = time.ParseDuration(v)
	}
	if conn.DetectIdentity(inv.Sys.Getenv) == conn.IdentityExternal {
		path, e := inv.Sys.ConfigPath()
		if e != nil {
			return nil, e
		}
		o.ConfigPath = path
	}
	return conn.Resolve(o)
}

func clientMeta(c *conn.Client, meta *output.Meta) []output.Warning {
	v, id, attempts, responded := c.Meta()
	if attempts > 0 {
		meta.Attempts = attempts
	}
	if responded {
		meta.ServerVersion, meta.RequestID = v, id
	}
	return c.Warnings()
}

// Errorf is shorthand for output.Errorf, for use by commands.
func Errorf(code, format string, args ...any) *output.Error {
	return output.Errorf(code, format, args...)
}
