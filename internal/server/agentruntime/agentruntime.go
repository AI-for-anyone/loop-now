// Package agentruntime is the registry of Agent runtimes: which programs can run an Agent (the bundled Pi, and the Claude
// Code, Codex, Grok and Antigravity installed on the user's computer), which model providers each one runs, and what each one can do. The runtime
// is not stored separately; the model provider decides it.
//
// Code outside this package and the executor's driver files asks the registry about a capability and never compares a
// runtime id (a test enforces this), so adding a runtime means one registry entry and one driver.
package agentruntime

import (
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// Runtime ids, matching AgentRuntime in the OpenAPI spec.
const (
	Pi          = "pi"
	ClaudeCode  = "claude-code"
	Codex       = "codex"
	Grok        = "grok"
	Antigravity = "antigravity"
)

// ClaudeCodeProvider is the model provider id of the Claude Code runtime: models come from the claude installed on the
// user's computer, using its own login.
const ClaudeCodeProvider = "claude-code"

// CodexProvider is the model provider id of the Codex runtime: models come from the codex installed on the user's
// computer, using its own login (the openai-codex provider is a different thing, run by Pi with an account the
// platform keeps).
const CodexProvider = "codex-cli"

// GrokProvider and AntigravityProvider are the model provider ids of the Grok and Antigravity runtimes: models come from
// the grok and agy installed on the user's computer, using their own logins.
const (
	GrokProvider        = "grok-cli"
	AntigravityProvider = "antigravity-cli"
)

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
	// StopEndsBackgroundTasks: stopping a reply ends the runtime's process, and with it the background tasks its CLI
	// started.
	StopEndsBackgroundTasks bool `json:"stopEndsBackgroundTasks"`
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
	// Setup gives the commands a person runs to install the runtime and log in on an operating system (runtime.GOOS
	// values); nil for a runtime that needs no setup.
	Setup func(goos string) *Setup
	// MinVersion is the oldest version of the runtime's CLI the platform supports (the version it was tested with);
	// empty when there is no minimum. The status probe reports an older CLI as unsupported.
	MinVersion string
	// Command is set for a runtime that runs a local ACP command a person added (see LocalCommandRuntime).
	Command *LocalCommand
	// CommandID is that command's id.
	CommandID uuid.UUID
	// TextGeneration says how the runtime writes short texts with its local login ("Write with AI", such as a Prompt
	// draft); nil for a runtime that does not write them.
	TextGeneration *TextGeneration
}

// TextGeneration is how Automatic uses a runtime that writes texts: the order it is tried in and the small model it
// writes with, chosen from the runtime's live model list.
type TextGeneration struct {
	// Rank orders the runtimes Automatic tries, lowest first; the local ACP commands come after the built-in runtimes,
	// in their own order.
	Rank int
	// Models are parts of the id or name (ignoring case) of the models Automatic prefers, in order: the first listed
	// model that matches the first part, else the second, and so on; none, or no match, means the first listed model.
	Models []string
	// Level is the thinking level Automatic uses when the model offers it: a level, or LowestLevel for the model's
	// lowest; empty means the model's default level.
	Level string
}

// LowestLevel as TextGeneration.Level means the model's lowest thinking level.
const LowestLevel = "lowest"

// localCommandWriterRank puts the local ACP commands after the built-in runtimes in Automatic's order.
const localCommandWriterRank = 100

// Writers returns the runtimes that write texts, in the order Automatic tries them.
func Writers() []Runtime {
	var out []Runtime
	for _, r := range All() {
		if r.TextGeneration != nil {
			out = append(out, r)
		}
	}
	slices.SortStableFunc(out, func(a, b Runtime) int { return a.TextGeneration.Rank - b.TextGeneration.Rank })
	return out
}

// LocalCommand is a program on this computer that speaks the Agent Client Protocol over stdio, added by a person: an
// executable (a name on the login shell's PATH or an absolute path), its arguments passed as is, and environment
// variables added for it.
type LocalCommand struct {
	Command string
	Args    []string
	Env     map[string]string
}

// LocalCommandPrefix starts the id of a runtime that runs a local ACP command.
const LocalCommandPrefix = "acp-"

// LocalCommandRuntimeID is the id of the runtime (and of its one model provider) that runs the local ACP command id.
func LocalCommandRuntimeID(id uuid.UUID) string { return LocalCommandPrefix + id.String() }

// LocalCommandRuntime is the registry entry of a local ACP command: it runs the provider of its own id, with the
// program's own login on this computer; it cannot copy a session for a task.
func LocalCommandRuntime(id uuid.UUID, name string, cmd LocalCommand) Runtime {
	rid := LocalCommandRuntimeID(id)
	return Runtime{ID: rid, Name: name, Providers: []string{rid}, Capabilities: Capabilities{Accounts: AccountsLocal, RealHome: true},
		Command: &cmd, CommandID: id, TextGeneration: &TextGeneration{Rank: localCommandWriterRank}}
}

var (
	localMu sync.RWMutex
	local   []Runtime
)

// SetLocalCommands replaces the runtimes of the local ACP commands (after the registry's own, in the given order).
func SetLocalCommands(runtimes []Runtime) {
	out := make([]Runtime, 0, len(runtimes))
	for _, r := range runtimes {
		if r.Command != nil && strings.HasPrefix(r.ID, LocalCommandPrefix) {
			out = append(out, r.clone())
		}
	}
	localMu.Lock()
	local = out
	localMu.Unlock()
}

// entries is the registry followed by the local ACP commands.
func entries() []Runtime {
	localMu.RLock()
	defer localMu.RUnlock()
	return append(slices.Clone(registry), local...)
}

// SetupOn returns how to set the runtime up on the operating system goos; nil when it needs no setup.
func (r Runtime) SetupOn(goos string) *Setup {
	if r.Setup == nil {
		return nil
	}
	return r.Setup(goos)
}

// codexSetup is Codex's install through npm (the same on every system) and its login, from its documentation.
func codexSetup(string) *Setup {
	install, login, docs := "npm install -g @openai/codex", "codex login", "https://developers.openai.com/codex/cli"
	return &Setup{Install: &install, Login: &login, DocsURL: &docs}
}

// grokSetup and antigravitySetup give the commands that log in; the platform does not name an installer for them.
func grokSetup(string) *Setup {
	login := "grok login"
	return &Setup{Login: &login}
}

func antigravitySetup(string) *Setup {
	login := "agy acp --login"
	return &Setup{Login: &login}
}

// claudeCodeSetup is Claude Code's native installer and login, from its setup documentation.
func claudeCodeSetup(goos string) *Setup {
	install := "curl -fsSL https://claude.ai/install.sh | bash"
	if goos == "windows" {
		install = "irm https://claude.ai/install.ps1 | iex" // PowerShell
	}
	login, docs := "claude auth login", "https://code.claude.com/docs/en/setup"
	return &Setup{Install: &install, Login: &login, DocsURL: &docs}
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
		ID:             ClaudeCode,
		Name:           "Claude Code",
		Providers:      []string{ClaudeCodeProvider},
		Capabilities:   Capabilities{Accounts: AccountsLocal, RealHome: true, TaskFork: true},
		Setup:          claudeCodeSetup,
		TextGeneration: &TextGeneration{Rank: 1, Models: []string{"haiku"}},
	},
	{
		// Codex runs as `codex app-server` with the user's Codex home (CODEX_HOME) and the Agent's own HOME: its login
		// is found through CODEX_HOME alone. It reads AGENTS.md in the working directory itself.
		ID:        Codex,
		Name:      "Codex",
		Providers: []string{CodexProvider},
		Capabilities: Capabilities{Accounts: AccountsLocal, SessionCommands: []string{CommandCompact}, TaskFork: true,
			ReadsEntryFile: true},
		Setup:          codexSetup,
		MinVersion:     "0.167.0",
		TextGeneration: &TextGeneration{Rank: 2, Models: []string{"luna", "-mini"}, Level: "low"},
	},
	{
		// Grok runs as `grok agent ... stdio`, an Agent Client Protocol agent, with this computer's login (kept under the
		// real HOME). The model and reasoning effort are given when it starts.
		ID:             Grok,
		Name:           "Grok",
		Providers:      []string{GrokProvider},
		Capabilities:   Capabilities{Accounts: AccountsLocal, RealHome: true, StopEndsBackgroundTasks: true},
		Setup:          grokSetup,
		MinVersion:     "1.0.88",
		TextGeneration: &TextGeneration{Rank: 4},
	},
	{
		// Antigravity runs as `agy acp`, an Agent Client Protocol agent, with this computer's login (kept under the real
		// HOME). It lists a model once per thinking level (gemini-4-neon-high); the catalog has one model with those
		// levels (see modelcatalog.MergeLevelModels).
		ID:             Antigravity,
		Name:           "Antigravity",
		Providers:      []string{AntigravityProvider},
		Capabilities:   Capabilities{Accounts: AccountsLocal, RealHome: true, StopEndsBackgroundTasks: true},
		Setup:          antigravitySetup,
		MinVersion:     "1.5.14",
		TextGeneration: &TextGeneration{Rank: 3, Models: []string{"gemini-4-neon", "flash"}, Level: LowestLevel},
	},
}

// All returns every runtime, the default one first, then the local ACP commands. The result is a copy.
func All() []Runtime {
	all := entries()
	out := make([]Runtime, len(all))
	for i, r := range all {
		out[i] = r.clone()
	}
	return out
}

// ByID returns the runtime whose id is id; ok is false when there is none.
func ByID(id string) (Runtime, bool) {
	for _, r := range entries() {
		if r.ID == id {
			return r.clone(), true
		}
	}
	return Runtime{}, false
}

// ForProvider returns the runtime that runs an Agent using the model provider named provider: the runtime that lists
// it, otherwise the default runtime.
func ForProvider(provider string) Runtime {
	for _, r := range entries() {
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
	if r.TextGeneration != nil {
		tg := *r.TextGeneration
		tg.Models = slices.Clone(tg.Models)
		r.TextGeneration = &tg
	}
	if r.Command != nil {
		c := *r.Command
		c.Args, c.Env = slices.Clone(c.Args), maps.Clone(c.Env)
		r.Command = &c
	}
	return r
}
