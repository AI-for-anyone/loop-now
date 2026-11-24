// Package agentruntime is the registry of Agent runtimes: which programs can run an Agent (the bundled Pi, the Claude
// Code installed on the user's computer), which model providers each one runs, and what each one can do. The runtime
// is not stored separately; the model provider decides it.
//
// Code outside this package and the executor's driver files asks the registry about a capability and never compares a
// runtime id (a test enforces this), so adding a runtime means one registry entry and one driver.
package agentruntime

import "slices"

// Runtime ids, matching AgentRuntime in the OpenAPI spec.
const (
	Pi         = "pi"
	ClaudeCode = "claude-code"
)

// ClaudeCodeProvider is the model provider id of the Claude Code runtime: models come from the claude installed on the
// user's computer, using its own login.
const ClaudeCodeProvider = "claude-code"

// Accounts says whose login runs the model.
type Accounts string

const (
	// AccountsManaged: the accounts and credentials the platform keeps for the project or the Agent.
	AccountsManaged Accounts = "managed"
	// AccountsLocal: the login of the runtime's own CLI on this computer, which the platform neither reads nor stores.
	AccountsLocal Accounts = "local"
)

// Session commands: the platform commands that work on the runtime's session.
const (
	CommandCompact = "compact"
	CommandExport  = "export"
	CommandResume  = "resume"
)

// SessionCommands are all the session commands, in the order the platform lists them.
var SessionCommands = []string{CommandCompact, CommandExport, CommandResume}

// Capabilities is what a runtime can do, one field per question the server or the console asks. The fields with a
// JSON name are published in RuntimeCapabilities; the others are only read by the server. Thinking levels and image
// input are per model, in the model catalog.
type Capabilities struct {
	Accounts Accounts `json:"accounts"`
	// RealHome: always runs with the user's real HOME (otherwise only when "Use local logins" is on).
	RealHome bool `json:"realHome"`
	// NativeCommands: lists its own slash commands.
	NativeCommands bool `json:"nativeCommands"`
	// PluginExtensions: loads the Agent-side extensions of granted plugins (and so plugin model providers and
	// extension dialogs).
	PluginExtensions bool `json:"pluginExtensions"`
	// Bundled: ships with the platform; no install or login check on this computer.
	Bundled bool `json:"bundled"`
	// SessionCommands lists which of SessionCommands work.
	SessionCommands []string `json:"-"`
	// TaskFork: a task instance can start from a copy of the regular session.
	TaskFork bool `json:"-"`
	// ReadsEntryFile: reads AGENTS.md in the working directory by itself.
	ReadsEntryFile bool `json:"-"`
}

// Setup is how a person sets a runtime up on this computer: commands for a terminal, shown by the console and never run
// by the platform. Nil fields mean there is nothing to show.
type Setup struct {
	Install *string `json:"install"`
	Login   *string `json:"login"`
	DocsURL *string `json:"docsUrl"`
}

// HasSessionCommand reports whether the session command name (without "/") works on this runtime.
func (c Capabilities) HasSessionCommand(name string) bool {
	return slices.Contains(c.SessionCommands, name)
}

// Runtime is one entry of the registry.
type Runtime struct {
	ID string
	// Name is the product name, shown as is in every language.
	Name string
	// Providers are the model providers this runtime runs. The default runtime has none: it runs every provider that
	// no other runtime lists.
	Providers    []string
	Capabilities Capabilities
}

// IsDefault reports whether this is the runtime that runs every provider no other runtime lists.
func (r Runtime) IsDefault() bool { return len(r.Providers) == 0 }

// registry lists the runtimes, the default one first.
var registry = []Runtime{
	{
		ID:   Pi,
		Name: "Pi",
		Capabilities: Capabilities{Accounts: AccountsManaged, NativeCommands: true, PluginExtensions: true, Bundled: true,
			SessionCommands: SessionCommands, TaskFork: true, ReadsEntryFile: true},
	},
	{
		ID:           ClaudeCode,
		Name:         "Claude Code",
		Providers:    []string{ClaudeCodeProvider},
		Capabilities: Capabilities{Accounts: AccountsLocal, RealHome: true, TaskFork: true},
	},
}

// All returns every runtime, the default one first. The result is a copy.
func All() []Runtime {
	out := make([]Runtime, len(registry))
	for i, r := range registry {
		out[i] = r.clone()
	}
	return out
}

// ByID returns the runtime whose id is id; ok is false when there is none.
func ByID(id string) (Runtime, bool) {
	for _, r := range registry {
		if r.ID == id {
			return r.clone(), true
		}
	}
	return Runtime{}, false
}

// ForProvider returns the runtime that runs an Agent using the model provider named provider: the runtime that lists
// it, otherwise the default runtime.
func ForProvider(provider string) Runtime {
	for _, r := range registry {
		if slices.Contains(r.Providers, provider) {
			return r.clone()
		}
	}
	return Default()
}

// Default returns the runtime that runs every provider no other runtime lists.
func Default() Runtime {
	for _, r := range registry {
		if r.IsDefault() {
			return r.clone()
		}
	}
	panic("agentruntime: no default runtime")
}

// Known reports whether id is a runtime in the registry.
func Known(id string) bool {
	_, ok := ByID(id)
	return ok
}

func (r Runtime) clone() Runtime {
	r.Providers = slices.Clone(r.Providers)
	r.Capabilities.SessionCommands = slices.Clone(r.Capabilities.SessionCommands)
	return r
}
