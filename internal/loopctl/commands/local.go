package commands

import (
	"bufio"
	"context"
	"path/filepath"
	"strings"

	"loop-now/internal/loopctl/cli"
	"loop-now/internal/loopctl/config"
	"loop-now/internal/loopctl/conn"
	"loop-now/internal/loopctl/output"
)

func addLocal(r *cli.Registry) {
	r.Add(&cli.Command{
		Path:    []string{"version"},
		Summary: "Print the local loopctl version, operating system, architecture and Go version; sends no request",
		Output:  "{version, os, arch, goVersion}",
		Run: func(_ context.Context, inv *cli.Invocation) (any, error) {
			s := inv.Sys
			return map[string]string{"version": s.Version, "os": s.GOOS, "arch": s.GOARCH, "goVersion": s.GoVersion}, nil
		},
	})
	r.Add(&cli.Command{
		Path:    []string{"schema"},
		Summary: "Print a machine-readable description of the commands: all commands, error categories and exit codes, identities and connection parameters, and global options; needs no connection configuration and sends no request",
		Args:    []cli.Arg{{Name: "path", Description: "Command path, for example inbox list; when the path is a command group, prints its direct subcommands; when omitted, prints everything", Variadic: true}},
		Flags: []cli.Flag{
			{Name: "identity", Type: cli.TypeEnum, Values: []string{"agent", "external"}, Description: "Print only the commands available to this identity"},
		},
		Output: "Full description {program, version, envelope, exitCodes, errorCodes, identities, environment, globalFlags, commands}; with a path, the description of that command or command group",
		Run: func(_ context.Context, inv *cli.Invocation) (any, error) {
			data, e := cli.Schema(inv, inv.Args, cli.Identity(inv.String("identity")))
			if e != nil {
				return nil, e
			}
			return data, nil
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"whoami"},
		Summary:    "Show the type, address and name of the current identity",
		Identities: both,
		Network:    true,
		Retry:      true,
		Output:     "{kind, address, name}; the server version is in meta.serverVersion",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, nil, "whoami")
		},
	})

	r.Add(&cli.Command{
		Path:       []string{"config", "set"},
		Summary:    "Create or change a profile, changing at least one item; the token can only be written from standard input with --token-stdin; does not connect to the server",
		Identities: externalOnly,
		Stdin:      true,
		Args:       []cli.Arg{{Name: "profile", Description: "Profile name: starts with a letter or digit, may contain . _ -, at most 64 characters"}},
		Flags: []cli.Flag{
			{Name: "server", Type: cli.TypeString, Description: "Root address of the external port; must be https://"},
			{Name: "ca-file", Type: cli.TypePath, Description: "CA certificate in PEM format containing at least one certificate; saved as an absolute path"},
			{Name: "clear-ca", Type: cli.TypeBool, Description: "Clear the saved CA file and use the system root certificates"},
			{Name: "token-stdin", Type: cli.TypeBool, Description: "Read the first line of standard input as the external token (starting with lne_)"},
		},
		Constraints: []cli.Constraint{
			{Kind: cli.AtLeastOne, Flags: []string{"server", "ca-file", "clear-ca", "token-stdin"}},
			{Kind: cli.AtMostOne, Flags: []string{"ca-file", "clear-ca"}},
		},
		Output: "Profile summary {name, server, caFile, tokenSet, tokenId, tokenPrefix}, without the secret part of the token",
		Run:    configSet,
	})
	r.Add(&cli.Command{
		Path:       []string{"config", "list"},
		Summary:    "List summaries of all profiles and mark which one the current options and environment variables would select, and why; does not connect to the server",
		Identities: externalOnly,
		Flags: []cli.Flag{
			{Name: "profile", Type: cli.TypeString, Description: "Compute the selection with this profile, the same as --profile of the connecting commands"},
		},
		Output: "{path, exists, profiles: [profile summary], selected: {name, by, exists}, envOverrides: [environment variable name]}",
		Run:    configList,
	})
	r.Add(&cli.Command{
		Path:        []string{"config", "delete"},
		Summary:     "Delete a profile; does not connect to the server",
		Identities:  externalOnly,
		Destructive: true,
		Args:        []cli.Arg{{Name: "profile", Description: "Profile name"}},
		Output:      "{name, deleted}",
		Run:         configDelete,
	})
}

// profileSummary is the public summary of a profile; it only contains the token ID and display prefix.
type profileSummary struct {
	Name        string  `json:"name"`
	Server      string  `json:"server"`
	CAFile      string  `json:"caFile"`
	TokenSet    bool    `json:"tokenSet"`
	TokenID     *string `json:"tokenId"`
	TokenPrefix *string `json:"tokenPrefix"`
}

func summarize(name string, p config.Profile) profileSummary {
	s := profileSummary{Name: name, Server: p.Server, CAFile: p.CAFile, TokenSet: p.Token != ""}
	if id, prefix, ok := conn.TokenID(p.Token); ok {
		s.TokenID, s.TokenPrefix = &id, &prefix
	}
	return s
}

func loadConfig(inv *cli.Invocation) (string, *config.File, bool, error) {
	path, e := inv.Sys.ConfigPath()
	if e != nil {
		return "", nil, false, e
	}
	f, exists, e := config.Load(path)
	if e != nil {
		return "", nil, false, e
	}
	return path, f, exists, nil
}

func saveConfig(path string, f *config.File) error {
	if err := config.Save(path, f); err != nil {
		return output.Errorf(output.CodeOutputUnwritable, "Could not write config file %s: %v", path, cause(err))
	}
	return nil
}

func profileArg(inv *cli.Invocation) (string, error) {
	name := inv.Args[0]
	if !config.ValidProfileName(name) {
		return "", output.Errorf(output.CodeInvalidArgument, "Profile name %q is invalid. It must start with a letter or digit, may contain . _ -, and must be at most 64 characters.", name)
	}
	return name, nil
}

func configSet(_ context.Context, inv *cli.Invocation) (any, error) {
	name, err := profileArg(inv)
	if err != nil {
		return nil, err
	}
	// Check all input first and only then read and write the config file, so the file stays unchanged on failure.
	var server, ca, token string
	if inv.Has("server") {
		u, e := conn.ParseServerURL(inv.String("server"), true)
		if e != nil {
			return nil, e
		}
		server = u.String()
	}
	if inv.Has("ca-file") {
		abs, err := filepath.Abs(inv.String("ca-file"))
		if err != nil {
			return nil, output.Errorf(output.CodeCAFileUnreadable, "Could not determine the absolute path of the CA file: %v", err)
		}
		if _, e := conn.LoadCA(abs); e != nil {
			return nil, e
		}
		ca = abs
	}
	if inv.Bool("token-stdin") {
		if token, err = readToken(inv); err != nil {
			return nil, err
		}
	}
	path, f, _, err := loadConfig(inv)
	if err != nil {
		return nil, err
	}
	p := f.Profiles[name]
	if server != "" {
		p.Server = server
	}
	if ca != "" {
		p.CAFile = ca
	}
	if inv.Bool("clear-ca") {
		p.CAFile = ""
	}
	if token != "" {
		p.Token = token
	}
	f.Profiles[name] = p
	if err := saveConfig(path, f); err != nil {
		return nil, err
	}
	return summarize(name, p), nil
}

// readToken reads the first line of standard input as the external token. Error messages do not include what was
// read.
func readToken(inv *cli.Invocation) (string, error) {
	line, err := bufio.NewReaderSize(inv.Sys.Stdin, 4096).ReadString('\n')
	if err != nil && line == "" {
		return "", output.Errorf(output.CodeInvalidArgument, "There is no token on standard input.")
	}
	tok := strings.TrimSpace(line)
	switch {
	case tok == "":
		return "", output.Errorf(output.CodeInvalidArgument, "There is no token on standard input.")
	case strings.HasPrefix(tok, conn.AgentTokenPrefix):
		return "", output.Errorf(output.CodeTokenKindMismatch, "Standard input holds an Agent token. The config file stores only external tokens (starting with %s).", conn.ExternalTokenPrefix)
	case !strings.HasPrefix(tok, conn.ExternalTokenPrefix):
		return "", output.Errorf(output.CodeInvalidArgument, "Standard input does not hold an external token (external tokens start with %s).", conn.ExternalTokenPrefix)
	}
	if _, _, ok := conn.TokenID(tok); !ok {
		return "", output.Errorf(output.CodeInvalidArgument, "The external token has the wrong format. Expected %s<32 hexadecimal digit ID>_<secret>.", conn.ExternalTokenPrefix)
	}
	return tok, nil
}

func configList(_ context.Context, inv *cli.Invocation) (any, error) {
	path, f, exists, err := loadConfig(inv)
	if err != nil {
		return nil, err
	}
	name, by := config.DefaultProfile, "default"
	if inv.Has("profile") {
		name, by = inv.String("profile"), "--profile"
	} else if v := inv.Sys.Getenv(conn.EnvProfile); v != "" {
		name, by = v, conn.EnvProfile
	}
	_, found := f.Profiles[name]
	profiles := []profileSummary{}
	for _, n := range f.Names() {
		profiles = append(profiles, summarize(n, f.Profiles[n]))
	}
	overrides := []string{}
	for _, env := range []string{conn.EnvURL, conn.EnvToken, conn.EnvCAFile} {
		if inv.Sys.Getenv(env) != "" {
			overrides = append(overrides, env)
		}
	}
	return map[string]any{
		"path":         path,
		"exists":       exists,
		"profiles":     profiles,
		"selected":     map[string]any{"name": name, "by": by, "exists": found},
		"envOverrides": overrides,
	}, nil
}

func configDelete(_ context.Context, inv *cli.Invocation) (any, error) {
	name, err := profileArg(inv)
	if err != nil {
		return nil, err
	}
	path, f, _, err := loadConfig(inv)
	if err != nil {
		return nil, err
	}
	if _, ok := f.Profiles[name]; !ok {
		return nil, output.Errorf(output.CodeProfileNotFound, "The config file has no profile %q.", name)
	}
	delete(f.Profiles, name)
	if err := saveConfig(path, f); err != nil {
		return nil, err
	}
	return map[string]any{"name": name, "deleted": true}, nil
}
