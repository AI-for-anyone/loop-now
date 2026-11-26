package agentconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/dbcli"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/prompts"
	"loop-now/internal/server/store"
)

// ErrCredentialUnavailable means at least one credential of the Agent cannot be used right now (it must be entered
// again or has expired).
var ErrCredentialUnavailable = errors.New("credentials are unavailable")

// Snapshot is the config snapshot the executor needs to start Pi, including credential contents. It is used only in
// memory.
type Snapshot struct {
	// Resolved level for this round. Nil is reserved for legacy task snapshots.
	ThinkingLevel   *string
	ServiceTier     *string
	LarkBinding     *clicredential.LarkBinding
	GoogleBinding   *clicredential.Binding
	ModelCredential modelcredential.Credential
	ModelBinding    *modelcredential.Binding
	// ConfigurationAgentID and TaskID are internal log attribution for task runtimes.
	ConfigurationAgentID uuid.UUID
	TaskID               uuid.UUID
	AgentID              uuid.UUID
	ConfigVersion        int64
	CredentialIdentity   string
	ModelConfigIdentity  string
	ModelsJSON           []byte
	Provider             string
	ModelID              string
	PromptLayers         *prompts.Layers
	// ContextProjectID is the visible context layer: the project for a project Agent (including instances inside a
	// task), nil (global) for a platform Agent.
	ContextProjectID *uuid.UUID
	Prompt           string
	Skills           []SkillSnapshot
	Sources          []store.FolderSource
	CLIs             []string
	ModelEnv         map[string]string
	CLIPayloads      map[string]clitemplate.Payload
	Session          store.AgentSession
	// ForkSessionFile is used only on the first start of a task instance, or of an Agent imported from a conversation
	// (the copy taken at import); the platform produces it by a stable copy, and it never comes from user config.
	ForkSessionFile string
	// WorkingDirectory is the folder a regular Agent works in (its CLI's working directory); empty: its own folder. A
	// task step never works there: it keeps its own copy of the Agent's folder.
	WorkingDirectory string
	// TaskWorkspace is the git worktree of the task a task step belongs to, set only while that worktree is active:
	// the step's CLI works there (every runtime), and UseLocalConfig applies to the step as to a regular Agent.
	TaskWorkspace string
	// RegularWorkingDirectory is, for a step of a task with a worktree, the base Agent's working folder in its regular
	// conversation (empty: its own folder); Claude Code keys its auto memory by it, so the step shares that memory.
	RegularWorkingDirectory string
	// UseLocalConfig: the runtime loads the person's own CLI configuration, as in their terminal (runtimes with the
	// localConfig capability).
	UseLocalConfig bool
	// PermissionMode is the Agent's permission mode (a task instance's: the one its Agent had when the task started);
	// empty is full (task snapshots from before modes). A mode the runtime lacks runs as supervised.
	PermissionMode string
	// Plugins is what the granted plugins hand to Pi; it is read when Pi starts.
	Plugins PluginAssets
	// ProviderState is the state of a plugin provider subscription account (plugins/SPEC.md §13); present only when the
	// model credential is plugin_state.
	ProviderState *ProviderState
	// Local is what the personal edition's "local mode" hands to Pi: read when Pi starts, from the current settings of
	// the base Agent and its project (the same for instances inside a task; it does not go into the task snapshot);
	// when its Identity changes, Pi restarts at the next dispatch.
	Local LocalAccess
}

// LocalAccess is the part of local mode that takes effect when Pi starts.
type LocalAccess struct {
	// Directories are the effective local directories (empty when "Bind local directories" is off): the project's
	// first, then the Agent's own, each path only once. Whether a directory exists is decided when the system prompt is
	// written, and only existing ones are listed.
	Directories []LocalDirectory
	// UserPath, when true, appends the user's PATH after the execution PATH ("Use local CLIs").
	UserPath bool
	// Logins, when true, makes the HOME of Pi and its child processes (plus USERPROFILE, APPDATA and LOCALAPPDATA on
	// Windows) the real one of the server's user ("Use local logins"); CLIs for which Loop Now configured an account
	// for the Agent still run with the Agent's own HOME.
	Logins bool `json:"logins,omitempty"`
}

// LocalDirectory is one effective local directory.
type LocalDirectory struct {
	Path      string `json:"path"`
	Note      string `json:"note,omitempty"`
	Inherited bool   `json:"inherited,omitempty"` // from the project's list
}

// Identity is the fingerprint that decides whether Pi needs a restart: directories (including notes and origin) and the
// PATH and logins switches, but not whether the directories exist right now.
func (l LocalAccess) Identity() string {
	if len(l.Directories) == 0 && !l.UserPath && !l.Logins {
		return ""
	}
	b, _ := json.Marshal(l)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// PluginAssets is the agent side of the Agent's granted plugins (plugins/SPEC.md §2.1); all paths are absolute. It is
// empty when no plugin is granted or plugins are disabled globally.
type PluginAssets struct {
	IDs        []string                   // mounted plugins
	Extensions []string                   // one -e each, after the platform's own -e
	Skills     []string                   // one --skill each
	MCPServers map[string]json.RawMessage // merged into mcp.json in the Pi directory, plus -e builtin:mcp
	Bin        []string                   // added to PATH
	Env        map[string]string          // plugin config and accounts
}

// ProviderState is one state version of a plugin provider subscription account: before Pi starts, the Agent's auth.json
// is linked to the auth.json in Dir, and Paths (relative paths under HOME, / separated) are linked to the same-named
// paths in Dir; while "Use local logins" is on, no links are created under HOME.
type ProviderState struct {
	PluginID  string
	VersionID uuid.UUID
	Dir       string
	Paths     []string
}

// Identity is the process identity of the state: a change of version or of paths requires restarting Pi.
func (p *ProviderState) Identity() string {
	if p == nil {
		return ""
	}
	b, _ := json.Marshal(p)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ProviderStateFunc returns the state when the model credential is plugin_state: plugin is the plugin the credential
// belongs to, version is the bound version.
type ProviderStateFunc func(ctx context.Context, plugin, provider string, version uuid.UUID) (*ProviderState, error)

// SkillSnapshot is the file list of one Skill copy.
type SkillSnapshot struct {
	Name  string
	Files []store.SkillFile
}

// ExecutionSnapshot reads the Agent's execution config. When any credential is unavailable it returns
// ErrCredentialUnavailable and never returns only some of the credentials.
func (s *Service) ExecutionSnapshot(ctx context.Context, agentID uuid.UUID) (Snapshot, error) {
	captured, err := s.Store.CaptureExecutionIdentity(ctx, agentID)
	if err != nil {
		var failure *apperr.Error
		if errors.As(err, &failure) && (failure.Code == apperr.CodeCredentialSelectionRequired || failure.Code == apperr.CodeCredentialAccountDeleted || failure.Code == apperr.CodeCredentialScopeMismatch || failure.Code == apperr.CodeValidationFailed && len(failure.Fields) == 1 && failure.Fields[0].Field == "credentials") {
			return Snapshot{}, ErrCredentialUnavailable
		}
		return Snapshot{}, err
	}
	a := captured.Agent
	snap := Snapshot{ContextProjectID: a.ProjectID, AgentID: a.ID, ConfigVersion: a.ConfigVersion, CredentialIdentity: captured.CredentialIdentity, Provider: a.ModelProvider, ModelID: a.ModelID, Prompt: a.Prompt, PromptLayers: prompts.Clone(captured.Prompts),
		ModelEnv: map[string]string{}, CLIPayloads: map[string]clitemplate.Payload{}}
	models := s.Models.Snapshot()
	level, err := models.ResolveThinking(a.ModelProvider, a.ModelID, a.ThinkingLevel)
	if err != nil {
		return Snapshot{}, err
	}
	snap.ModelsJSON, snap.ModelConfigIdentity = models.ModelsJSONFor(a.ModelProvider)
	snap.ThinkingLevel = &level
	snap.ServiceTier, err = ResolveServiceTier(a.ModelProvider, a.ServiceTier)
	if err != nil {
		return Snapshot{}, err
	}
	snap.Prompt, err = captured.Prompts.Compose()
	if err != nil {
		return Snapshot{}, err
	}
	snap.Session = captured.Session
	rt := agentruntime.ForProvider(a.ModelProvider)
	if a.WorkingDirectory != nil {
		snap.WorkingDirectory = *a.WorkingDirectory
	}
	snap.UseLocalConfig = a.UseLocalConfig
	snap.PermissionMode = a.PermissionMode
	// The person's own configuration: this computer's logins and PATH, and no project accounts.
	ownConfig := a.UseLocalConfig && rt.Capabilities.LocalConfig
	// An imported Agent's first turn continues the copy taken at import, on the runtime of the conversation.
	if f := a.ImportedFrom; f != nil && f.Capture != "" && snap.Session.State == "none" && snap.Session.Epoch == 1 {
		if src, ok := agentruntime.ForImportedCLI(f.CLI); ok && src.ID == rt.ID {
			snap.ForkSessionFile = f.Capture
		}
	}
	skills, err := s.Store.ListAgentSkills(ctx, agentID)
	if err != nil {
		return Snapshot{}, err
	}
	for _, k := range skills {
		full, err := s.Store.GetAgentSkill(ctx, agentID, k.ID)
		if err != nil {
			return Snapshot{}, err
		}
		snap.Skills = append(snap.Skills, SkillSnapshot{Name: full.Name, Files: full.Files})
	}
	// Folder sources are retired: the folder of a regular or platform Agent is its working directory, and no source is
	// synced at startup any more.
	snap.CLIs = captured.CLIs
	if ownConfig {
		snap.CLIs = nil
	}
	managed := captured.ModelAuthManaged
	if managed {
		snap.ModelBinding = captured.ModelAuth
		if snap.ModelBinding != nil {
			if s.ModelAuth == nil || s.ModelAuth.Coordinator == nil {
				return Snapshot{}, ErrCredentialUnavailable
			}
			snap.ModelCredential, err = s.ModelAuth.Coordinator.Resolve(ctx, *snap.ModelBinding)
			if err != nil {
				return Snapshot{}, ErrCredentialUnavailable
			}
		}
	}
	rows := captured.CredentialRows
	dbEntries := map[string][]clitemplate.DatabaseEntry{}
	for _, r := range rows {
		if managed && r.Purpose == store.PurposeModel {
			continue
		}
		if ownConfig && r.Purpose != store.PurposeModel {
			continue // project accounts do not apply to an Agent using the person's own configuration
		}
		if r.LarkBinding != nil {
			if !r.LarkBinding.Valid() || r.LarkBinding.AgentID != agentID || a.ProjectID == nil || r.LarkBinding.ProjectID != *a.ProjectID {
				return Snapshot{}, ErrCredentialUnavailable
			}
			c, e := clicredential.DecodeLark(r.Secret)
			if e != nil || !c.Matches(*r.LarkBinding) {
				return Snapshot{}, ErrCredentialUnavailable
			}
			b := *r.LarkBinding
			snap.LarkBinding = &b
			continue
		}
		if r.GoogleBinding != nil {
			if !r.GoogleBinding.Valid() || r.GoogleBinding.AgentID != agentID || a.ProjectID == nil || r.GoogleBinding.ProjectID != *a.ProjectID {
				return Snapshot{}, ErrCredentialUnavailable
			}
			if _, e := clicredential.DecodeGoogle(r.Secret); e != nil {
				return Snapshot{}, ErrCredentialUnavailable
			}
			binding := *r.GoogleBinding
			snap.GoogleBinding = &binding
			continue
		}
		if s.view(r).Status != "set" {
			return Snapshot{}, ErrCredentialUnavailable
		}
		var p clitemplate.Payload
		if err := json.Unmarshal(r.Secret, &p); err != nil {
			return Snapshot{}, ErrCredentialUnavailable
		}
		if r.Purpose == store.PurposeModel {
			snap.ModelEnv = p.Env
		} else {
			if r.CLIName == "psql" || r.CLIName == "mysql" {
				item := dbcli.Connection{ProjectID: captured.Agent.ProjectID, AccountID: r.ID, VersionID: r.ID, Name: r.CLIName}
				if r.DBConnection != nil {
					item.AccountID = r.DBConnection.AccountID
					item.Name = r.DBConnection.Name
					if r.DBConnection.VersionID != nil {
						item.VersionID = *r.DBConnection.VersionID
					}
				}
				dbEntries[r.CLIName] = append(dbEntries[r.CLIName], clitemplate.DatabaseEntry{Connection: item, Payload: p})
			} else {
				snap.CLIPayloads[r.CLIName] = p
			}
		}
	}
	for _, cli := range snap.CLIs {
		if cli == "psql" || cli == "mysql" {
			p, e := clitemplate.AggregateDatabase(cli, dbEntries[cli])
			if e != nil {
				return Snapshot{}, ErrCredentialUnavailable
			}
			snap.CLIPayloads[cli] = p
		}
	}
	if !managed {
		snap.ModelCredential, err = modelcredential.LegacySingleKey(snap.Provider, snap.ModelEnv)
		if err != nil {
			return Snapshot{}, ErrCredentialUnavailable
		}
	}
	if s.PluginAssets != nil {
		if snap.Plugins, err = s.PluginAssets(ctx, agentID); err != nil {
			return Snapshot{}, err
		}
	}
	if s.LocalAccess != nil {
		if snap.Local, err = s.LocalAccess(ctx, agentID); err != nil {
			return Snapshot{}, err
		}
	}
	if ownConfig {
		snap.Local.UserPath, snap.Local.Logins = true, true
	}
	if snap.ProviderState, err = ResolveProviderState(ctx, s.ProviderState, snap); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

// ResolveProviderState returns the state directory and the paths that travel with the account when the model credential
// is plugin_state; nil for any other credential.
func ResolveProviderState(ctx context.Context, fn ProviderStateFunc, snap Snapshot) (*ProviderState, error) {
	if snap.ModelCredential.Kind() != modelcredential.PluginState {
		return nil, nil
	}
	if fn == nil || snap.ModelBinding == nil {
		return nil, ErrCredentialUnavailable
	}
	return fn(ctx, snap.ModelCredential.Plugin(), snap.Provider, snap.ModelBinding.VersionID)
}

// ReadBlob reads content.
func (s *Service) ReadBlob(ctx context.Context, sha string) ([]byte, error) {
	return s.Store.ReadBlob(ctx, sha)
}

// SourceFilesForSync returns all file records of a source (including those pending deletion).
func (s *Service) SourceFilesForSync(ctx context.Context, sourceID uuid.UUID) ([]store.SyncFile, error) {
	return s.Store.ListSourceFilesForSync(ctx, sourceID)
}

// MarkSourceSynced records that a source has been synced and clears the processed records of manual edits.
func (s *Service) MarkSourceSynced(ctx context.Context, sourceID uuid.UUID, processed []store.SyncFile) error {
	return s.Store.MarkSourceSynced(ctx, sourceID, processed)
}

// RecordSessionFile records a session file that the given runtime has written to disk (see
// store.RecordRuntimeSessionFile).
func (s *Service) RecordSessionFile(ctx context.Context, agentID uuid.UUID, runtime, path string) error {
	return s.Store.RecordRuntimeSessionFile(ctx, agentID, runtime, path)
}

// MarkSessionMissing marks the session as lost; runID is the run that found it missing.
func (s *Service) MarkSessionMissing(ctx context.Context, agentID uuid.UUID, runID *uuid.UUID) error {
	return s.Store.MarkSessionMissing(ctx, agentID, runID)
}

// DeletedAgentIDs returns the deleted Agents.
func (s *Service) DeletedAgentIDs(ctx context.Context) ([]uuid.UUID, error) {
	return s.Store.DeletedAgentIDs(ctx)
}
