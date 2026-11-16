package cli

import (
	"strings"

	"loop-now/internal/loopctl/conn"
	"loop-now/internal/loopctl/output"
)

// CommandSchema describes one command in the schema.
type CommandSchema struct {
	Path        string       `json:"path"`
	Summary     string       `json:"summary"`
	Identities  []Identity   `json:"identities"`
	Network     bool         `json:"network"`
	Args        []Arg        `json:"args"`
	Flags       []Flag       `json:"flags"`
	Constraints []Constraint `json:"constraints,omitempty"`
	Destructive bool         `json:"destructive"`
	Stdin       bool         `json:"stdin"`
	Retry       bool         `json:"retry"`
	Output      string       `json:"output"`
}

// GroupSchema describes one command group in the schema.
type GroupSchema struct {
	Path     string `json:"path"`
	Summary  string `json:"summary"`
	Commands []any  `json:"commands"` // direct children: a CommandSchema, or a GroupRef for a sub-group
}

// GroupRef is a sub-group inside a command group.
type GroupRef struct {
	Path    string `json:"path"`
	Summary string `json:"summary"`
	Group   bool   `json:"group"`
}

// IdentitySchema describes one identity.
type IdentitySchema struct {
	Name        Identity `json:"name"`
	Description string   `json:"description"`
	TokenPrefix string   `json:"tokenPrefix"`
}

// FullSchema is the complete output of loopctl schema.
type FullSchema struct {
	Program     string            `json:"program"`
	Version     string            `json:"version"`
	Envelope    map[string]any    `json:"envelope"`
	ExitCodes   []output.ExitInfo `json:"exitCodes"`
	ErrorCodes  []output.CodeInfo `json:"errorCodes"`
	Identities  []IdentitySchema  `json:"identities"`
	Environment []conn.EnvDoc     `json:"environment"`
	GlobalFlags []GlobalFlag      `json:"globalFlags"`
	Commands    []CommandSchema   `json:"commands"`
}

// GlobalFlag is a global option together with the commands it applies to.
type GlobalFlag struct {
	Flag
	AppliesTo string `json:"appliesTo"`
}

// Describe returns the description of one command.
func Describe(c *Command) CommandSchema {
	ids := c.Identities
	if ids == nil {
		ids = []Identity{}
	}
	args := c.Args
	if args == nil {
		args = []Arg{}
	}
	flags := c.Flags
	if flags == nil {
		flags = []Flag{}
	}
	return CommandSchema{
		Path: c.Name(), Summary: c.Summary, Identities: ids, Network: c.Network,
		Args: args, Flags: flags, Constraints: c.Constraints,
		Destructive: c.Destructive, Stdin: c.Stdin, Retry: c.Retry, Output: c.Output,
	}
}

// schemaFor returns the full description, or the description of one command or one command group. When filter is
// not empty, only the commands that identity may use are kept.
func schemaFor(r *Registry, sys *System, path []string, filter Identity) (any, *output.Error) {
	if len(path) == 0 {
		return Full(r, sys.Version, filter), nil
	}
	if c := r.Find(path...); c != nil {
		if filter != "" && !c.AllowedFor(filter) {
			return nil, output.Errorf(output.CodeUnknownCommand, "The %s identity has no %s command.", filter, c.Name())
		}
		return Describe(c), nil
	}
	if !r.isGroup(path) {
		return nil, output.Errorf(output.CodeUnknownCommand, "There is no command or command group %q. Run loopctl schema to see all commands.", strings.Join(path, " "))
	}
	g := GroupSchema{Path: strings.Join(path, " "), Summary: r.groups[strings.Join(path, " ")], Commands: []any{}}
	for _, name := range r.children(path) {
		child := append(append([]string(nil), path...), name)
		if c := r.Find(child...); c != nil {
			if filter == "" || c.AllowedFor(filter) {
				g.Commands = append(g.Commands, Describe(c))
			}
			continue
		}
		if filter == "" || r.groupHas(child, filter) {
			p := strings.Join(child, " ")
			g.Commands = append(g.Commands, GroupRef{Path: p, Summary: r.groups[p], Group: true})
		}
	}
	return g, nil
}

// groupHas reports whether the command group contains a command the identity may use.
func (r *Registry) groupHas(path []string, id Identity) bool {
	for _, c := range r.commands {
		if equalPrefix(c.Path, path) && c.AllowedFor(id) {
			return true
		}
	}
	return false
}

// Full returns the full description.
func Full(r *Registry, version string, filter Identity) FullSchema {
	s := FullSchema{
		Program: "loopctl",
		Version: version,
		Envelope: map[string]any{
			"description": "Each call writes exactly one newline-terminated JSON object to standard output; nothing is written to standard error",
			"success":     `{"ok":true,"data":<JSON>,"warnings":[{code,message,details?}],"meta":{...}}`,
			"failure":     `{"ok":false,"error":{code,category,message,httpStatus?,details?,fields?},"warnings":[...],"meta":{...}}`,
			"meta":        "clientVersion is always present; identity when an identity was selected; profile when a profile from the config file was used; serverVersion, requestId and attempts when a request was sent",
			"data":        "For commands that call a server API directly, data is the server's response body, or null for 204",
			"error":       "When the server returns an error, code, message, details and fields are taken from the server as is, and httpStatus is added; the category follows the HTTP status code",
		},
		ExitCodes:   output.ExitTable(),
		ErrorCodes:  output.LocalCodes(),
		Environment: conn.EnvDocs(),
		Identities: []IdentitySchema{
			{IdentityAgent, "Agent running in the platform: the server injects " + conn.EnvAgentURL + " and " + conn.EnvAgentToken + "; can use only the Agent commands and does not read the config file", conn.AgentTokenPrefix},
			{IdentityExternal, "External identity: takes the server address, token and CA file from the command-line options, then the environment variables, then a profile in the config file; can use the management commands", conn.ExternalTokenPrefix},
		},
		Commands: []CommandSchema{},
	}
	for _, f := range ConnectionFlags {
		s.GlobalFlags = append(s.GlobalFlags, GlobalFlag{f, "Commands that connect to the server (network is true)"})
	}
	s.GlobalFlags = append(s.GlobalFlags, GlobalFlag{HelpFlag, "All commands"})
	for _, c := range r.Commands() {
		if filter == "" || c.AllowedFor(filter) {
			s.Commands = append(s.Commands, Describe(c))
		}
	}
	return s
}

// Schema is called by the schema command.
func Schema(inv *Invocation, path []string, filter Identity) (any, *output.Error) {
	return schemaFor(inv.Registry, inv.Sys, path, filter)
}
