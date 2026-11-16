// Package cli holds loopctl's command registry, parser, schema and execution flow.
// The command definitions are the single source of truth: parsing, schema, --help and the built-in Skill are all
// generated from them.
package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Identity is the identity a call is made with.
type Identity string

// The two identities.
const (
	IdentityAgent    Identity = "agent"
	IdentityExternal Identity = "external"
)

// FlagType is the type of an option.
type FlagType string

// Option types.
const (
	TypeString      FlagType = "string"
	TypeInt         FlagType = "int"
	TypeBool        FlagType = "bool"
	TypeDuration    FlagType = "duration"
	TypeTime        FlagType = "time" // RFC 3339, for example 2026-10-13T00:00:00+08:00
	TypeEnum        FlagType = "enum"
	TypePath        FlagType = "path"
	TypePathOrStdin FlagType = "path-or-stdin"
)

// Arg is a positional argument. Positional arguments are always required; Variadic may only be set on the last
// argument and means zero or more values.
type Arg struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Variadic    bool   `json:"variadic,omitempty"`
}

// Flag is an option; its name is stored without the -- prefix.
type Flag struct {
	Name        string   `json:"name"`
	Type        FlagType `json:"type"`
	Required    bool     `json:"required"`
	Repeatable  bool     `json:"repeatable"`
	Default     string   `json:"default,omitempty"`
	Values      []string `json:"values,omitempty"`
	Min         *int     `json:"min,omitempty"`
	Max         *int     `json:"max,omitempty"`
	Description string   `json:"description"`
}

// ConstraintKind is a kind of constraint between options.
type ConstraintKind string

// Constraint kinds.
const (
	ExactlyOne ConstraintKind = "exactlyOne" // exactly one of them must be given
	AtMostOne  ConstraintKind = "atMostOne"  // at most one of them may be given
	AtLeastOne ConstraintKind = "atLeastOne" // at least one of them must be given
	Together   ConstraintKind = "together"   // either all of them or none of them
)

// Constraint constrains a group of options.
type Constraint struct {
	Kind  ConstraintKind `json:"kind"`
	Flags []string       `json:"flags"`
	Hint  string         `json:"hint,omitempty"` // hint appended to the message when the constraint is not met
}

// RunFunc runs the command and returns data; when the error is an *output.Error, the process exits according to
// its category.
type RunFunc func(ctx context.Context, inv *Invocation) (any, error)

// Command is the complete definition of one command.
type Command struct {
	Path        []string
	Summary     string
	Identities  []Identity // empty means no identity is needed (version, schema)
	Network     bool       // whether it connects to the server
	Args        []Arg
	Flags       []Flag
	Constraints []Constraint
	Destructive bool   // deletes or overwrites; must be given --yes
	Stdin       bool   // may read standard input (when explicitly asked for with - or --token-stdin)
	Retry       bool   // whether a failed request is retried automatically
	Output      string // output description: an OpenAPI schema name, or a description of a local structure
	Run         RunFunc
}

// Name returns the command path joined with spaces.
func (c *Command) Name() string { return strings.Join(c.Path, " ") }

// AllowedFor reports whether the identity may use the command.
func (c *Command) AllowedFor(id Identity) bool {
	if len(c.Identities) == 0 {
		return true
	}
	for _, x := range c.Identities {
		if x == id {
			return true
		}
	}
	return false
}

// flag looks up an option declared by this command by name (global options are not included).
func (c *Command) flag(name string) (Flag, bool) {
	for _, f := range c.Flags {
		if f.Name == name {
			return f, true
		}
	}
	return Flag{}, false
}

// YesFlag is the confirmation option added automatically to commands that delete or overwrite.
var YesFlag = Flag{Name: "yes", Type: TypeBool, Description: "Confirm a deletion or overwrite; without it no request is sent"}

// HelpFlag is available on every command.
var HelpFlag = Flag{Name: "help", Type: TypeBool, Description: "Print this command's description (same as schema <command path>) without running it"}

// ConnectionFlags are the global options accepted by commands that connect to the server.
var ConnectionFlags = []Flag{
	{Name: "profile", Type: TypeString, Description: "External identity: choose a profile from the config file; defaults to LOOPCTL_PROFILE, then default"},
	{Name: "server", Type: TypeString, Description: "External identity: root address of the external port (https://); takes precedence over LOOPCTL_URL and the profile"},
	{Name: "ca-file", Type: TypePath, Description: "External identity: CA certificate in PEM format; when given, only the CAs in it are trusted; takes precedence over LOOPCTL_CA_FILE and the profile"},
	{Name: "timeout", Type: TypeDuration, Description: "Timeout for a single request, for example 30s; takes precedence over LOOPCTL_TIMEOUT; default 60s"},
}

// Registry is the command tree.
type Registry struct {
	commands []*Command
	groups   map[string]string // command group path -> summary
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return &Registry{groups: map[string]string{}} }

// Group declares the summary of a command group.
func (r *Registry) Group(path, summary string) { r.groups[path] = summary }

// Add registers a command. It panics when the path is a duplicate, when a command has the same name as a command
// group, or when a parent path is neither a command nor a command group; tests catch these.
func (r *Registry) Add(c *Command) {
	name := c.Name()
	for _, x := range r.commands {
		if x.Name() == name {
			panic("重复的命令：" + name)
		}
	}
	if _, ok := r.groups[name]; ok {
		panic("命令与命令组同名：" + name)
	}
	for i := 1; i < len(c.Path); i++ {
		g := strings.Join(c.Path[:i], " ")
		if _, ok := r.groups[g]; !ok && r.Find(c.Path[:i]...) == nil {
			panic(fmt.Sprintf("命令 %s 缺少命令组 %s 的说明", name, g))
		}
	}
	if c.Destructive {
		if _, ok := c.flag(YesFlag.Name); !ok {
			c.Flags = append(c.Flags, YesFlag)
		}
	}
	r.commands = append(r.commands, c)
}

// Commands returns all commands, sorted by path.
func (r *Registry) Commands() []*Command {
	out := append([]*Command(nil), r.commands...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Find looks up a command by its full path.
func (r *Registry) Find(path ...string) *Command {
	name := strings.Join(path, " ")
	for _, c := range r.commands {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

// isGroup reports whether the path is a command group.
func (r *Registry) isGroup(path []string) bool {
	_, ok := r.groups[strings.Join(path, " ")]
	return ok
}

// children returns the names of the direct children of a command group (sub-groups and commands).
func (r *Registry) children(path []string) []string {
	seen := map[string]bool{}
	for _, c := range r.commands {
		if len(c.Path) > len(path) && equalPrefix(c.Path, path) {
			seen[c.Path[len(path)]] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func equalPrefix(p, prefix []string) bool {
	if len(p) < len(prefix) {
		return false
	}
	for i := range prefix {
		if p[i] != prefix[i] {
			return false
		}
	}
	return true
}

// effectiveFlags returns the options a command actually accepts: its own options, the connection options (only
// for commands that connect to the server) and --help.
func effectiveFlags(c *Command) []Flag {
	out := append([]Flag(nil), c.Flags...)
	if c.Network {
		out = append(out, ConnectionFlags...)
	}
	return append(out, HelpFlag)
}
