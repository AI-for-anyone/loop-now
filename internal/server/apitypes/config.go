package apitypes

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/store"
)

// ErrorBody mirrors the Error schema.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail is Error.error.
type ErrorDetail struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Details any          `json:"details,omitempty"`
	Fields  []FieldError `json:"fields,omitempty"`
}

// FieldError mirrors the FieldError schema.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Context mirrors the Context schema.
type Context struct {
	Organization struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	} `json:"organization"`
	Project struct {
		ID             uuid.UUID `json:"id"`
		OrganizationID uuid.UUID `json:"organizationId"`
		Name           string    `json:"name"`
	} `json:"project"`
	Limits Limits `json:"limits"`
	// PathPicker says whether POST /path-picks can show this computer's own dialog; false for an external identity.
	PathPicker bool `json:"pathPicker"`
}

// PathPick mirrors the PathPick schema: the chosen path, or null when the person cancelled.
type PathPick struct {
	Path *string `json:"path"`
}

// Limits mirrors the Limits schema.
type Limits struct {
	MaxMessageImages      int      `json:"maxMessageImages"`
	MaxMessageImageBytes  int64    `json:"maxMessageImageBytes"`
	MaxMessageImagePixels int64    `json:"maxMessageImagePixels"`
	MaxUploadFile         int64    `json:"maxUploadFile"`
	MaxFolderUpload       int64    `json:"maxFolderUpload"`
	MaxMessage            int64    `json:"maxMessage"`
	MaxInboxMessage       int64    `json:"maxInboxMessage"`
	MaxSkillArchive       int64    `json:"maxSkillArchive"`
	MaxSkillEntries       int      `json:"maxSkillEntries"`
	MaxCredential         int64    `json:"maxCredential"`
	TextEdit              TextEdit `json:"textEdit"`
}

// TextEdit is Limits.textEdit.
type TextEdit struct {
	MaxBytes   int64    `json:"maxBytes"`
	Extensions []string `json:"extensions"`
}

// Health mirrors the Health schema.
type Health struct {
	OK       bool           `json:"ok"`
	Database DatabaseHealth `json:"database"`
}

// DatabaseHealth mirrors the DatabaseHealth schema.
type DatabaseHealth struct {
	OK         bool     `json:"ok"`
	Error      *string  `json:"error"`
	WALBytes   WALBytes `json:"walBytes"`
	WALWarning bool     `json:"walWarning"`
}

// WALBytes is DatabaseHealth.walBytes.
type WALBytes struct {
	Data   int64 `json:"data"`
	Events int64 `json:"events"`
}

// RedactedHealth is the health status shown to external identities: same structure, but the database part only says
// whether it is available.
type RedactedHealth struct {
	OK       bool               `json:"ok"`
	Database RedactedDependency `json:"database"`
}

// RedactedDependency only says whether a dependency is available.
type RedactedDependency struct {
	OK bool `json:"ok"`
}

// Redacted returns the health status without the raw error text and the WAL information.
func (h Health) Redacted() RedactedHealth {
	return RedactedHealth{OK: h.OK, Database: RedactedDependency{OK: h.Database.OK}}
}

// ModelProviderList mirrors the ModelProviderList schema.
type ModelProviderList struct {
	PiVersion          string          `json:"piVersion"`
	CatalogGeneratedAt *time.Time      `json:"catalogGeneratedAt"`
	Providers          []ModelProvider `json:"providers"`
}

// ModelProvider mirrors the ModelProvider schema.
type ModelProvider struct {
	ID       string  `json:"id"`
	Name     *string `json:"name"`
	Source   string  `json:"source"`
	Runtime  string  `json:"runtime"`
	PluginID *string `json:"pluginId"`
	// PluginDisplayName is the display name of the plugin that registered the provider (verbatim from the manifest, may
	// be per language); UnavailableReason is only given when the query includes agentId.
	PluginDisplayName json.RawMessage `json:"pluginDisplayName"`
	UnavailableReason *string         `json:"unavailableReason"`
	APIKeyEnvVars     []string        `json:"apiKeyEnvVars"`
	AuthMethods       []string        `json:"authMethods"`
	ModelCount        int             `json:"modelCount"`
	// Usable says whether the Agent, project or new platform Agent named in the query can run the provider without
	// entering an account first; null when the query names none.
	Usable *bool `json:"usable"`
	// Models is set only with include=models.
	Models []Model `json:"models,omitempty"`
}

// ModelList mirrors the ModelList schema.
type ModelList struct {
	ProviderID string  `json:"providerId"`
	Models     []Model `json:"models"`
}

// Model mirrors the Model schema.
type Model struct {
	ThinkingLevels       []string `json:"thinkingLevels,omitempty"`
	DefaultThinkingLevel string   `json:"defaultThinkingLevel,omitempty"`
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Reasoning            bool     `json:"reasoning"`
	ContextWindow        int      `json:"contextWindow"`
	MaxTokens            int      `json:"maxTokens"`
	Input                []string `json:"input"`
}

// CliTemplate mirrors the CliTemplate schema.
type CliTemplate struct {
	Name      string                `json:"name"`
	Version   string                `json:"version"`
	Fields    []clitemplate.Field   `json:"fields"`
	Injection string                `json:"injection"`
	Install   CliInstall            `json:"install"`
	Warnings  []clitemplate.Warning `json:"warnings"`
}

// CliInstall is CliTemplate.install.
type CliInstall struct {
	Status string  `json:"status"`
	Error  *string `json:"error"`
}

// ModelRef mirrors the ModelRef schema.
type ModelRef struct {
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

// AgentListItem mirrors the AgentListItem schema.
type AgentListItem struct {
	ServiceTier   *string            `json:"serviceTier"`
	ThinkingLevel *string            `json:"thinkingLevel"`
	Sleep         *sleepmodel.Record `json:"sleep"`
	SleepRevision int64              `json:"sleepRevision"`
	ID            uuid.UUID          `json:"id"`
	Scope         string             `json:"scope"`
	ProjectID     *uuid.UUID         `json:"projectId"`
	IsManager     bool               `json:"isManager"`
	Name          string             `json:"name"`
	Address       *string            `json:"address"`
	Model         ModelRef           `json:"model"`
	Runtime       string             `json:"runtime"`
	ConfigVersion int64              `json:"configVersion"`
	Busy          bool               `json:"busy"`
	// BackgroundTaskCount is how many background tasks run on the Agent's runtime process.
	BackgroundTaskCount int       `json:"backgroundTaskCount"`
	SessionState        string    `json:"sessionState"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

// Agent mirrors the Agent schema.
type Agent struct {
	ServiceTier   *string    `json:"serviceTier"`
	ThinkingLevel *string    `json:"thinkingLevel"`
	ID            uuid.UUID  `json:"id"`
	Scope         string     `json:"scope"`
	ProjectID     *uuid.UUID `json:"projectId"`
	IsManager     bool       `json:"isManager"`
	Name          string     `json:"name"`
	Address       *string    `json:"address"`
	Model         ModelRef   `json:"model"`
	Runtime       string     `json:"runtime"`
	Prompt        string     `json:"prompt"`
	PromptVersion int64      `json:"promptVersion"`
	ConfigVersion int64      `json:"configVersion"`
	Busy          bool       `json:"busy"`
	// BackgroundTaskCount is how many background tasks run on the Agent's runtime process.
	BackgroundTaskCount int             `json:"backgroundTaskCount"`
	Session             SessionInfo     `json:"session"`
	Clis                []AgentCli      `json:"clis"`
	ModelCredential     *CredentialMeta `json:"modelCredential"`
	CreatedAt           time.Time       `json:"createdAt"`
	UpdatedAt           time.Time       `json:"updatedAt"`
}

// CredentialMeta mirrors the CredentialMeta schema.
type CredentialMeta struct {
	ProjectID    *uuid.UUID            `json:"projectId,omitempty"`
	AccountID    *uuid.UUID            `json:"accountId,omitempty"`
	Provider     *string               `json:"provider,omitempty"`
	AuthMethod   *string               `json:"authMethod,omitempty"`
	VersionID    *uuid.UUID            `json:"versionId,omitempty"`
	AccountLabel *string               `json:"accountLabel,omitempty"`
	Status       string                `json:"status"`
	UpdatedAt    string                `json:"updatedAt"`
	FieldNames   []string              `json:"fieldNames"`
	EnvNames     []string              `json:"envNames"`
	FilePaths    []string              `json:"filePaths"`
	Warnings     []clitemplate.Warning `json:"warnings"`
}

// AgentCli mirrors the AgentCli schema.
type AgentCli struct {
	Name        string          `json:"name"`
	HasTemplate bool            `json:"hasTemplate"`
	Credential  *CredentialMeta `json:"credential"`
}

// SkillFile mirrors the SkillFile schema.
type SkillFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// SkillTemplateSummary mirrors the SkillTemplateSummary schema.
type SkillTemplateSummary struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	FileCount   int       `json:"fileCount"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// SkillTemplate mirrors the SkillTemplate schema.
type SkillTemplate struct {
	ID          uuid.UUID   `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Files       []SkillFile `json:"files"`
	SkillMD     string      `json:"skillMd"`
	CreatedAt   time.Time   `json:"createdAt"`
	UpdatedAt   time.Time   `json:"updatedAt"`
}

// AgentSkillSummary mirrors the AgentSkillSummary schema.
type AgentSkillSummary struct {
	ID               uuid.UUID  `json:"id"`
	Name             string     `json:"name"`
	Description      string     `json:"description"`
	SourceTemplateID *uuid.UUID `json:"sourceTemplateId"`
	FileCount        int        `json:"fileCount"`
	CopiedAt         time.Time  `json:"copiedAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

// AgentSkill mirrors the AgentSkill schema.
type AgentSkill struct {
	ID               uuid.UUID   `json:"id"`
	Name             string      `json:"name"`
	Description      string      `json:"description"`
	SourceTemplateID *uuid.UUID  `json:"sourceTemplateId"`
	Files            []SkillFile `json:"files"`
	SkillMD          string      `json:"skillMd"`
	CopiedAt         time.Time   `json:"copiedAt"`
	UpdatedAt        time.Time   `json:"updatedAt"`
}

// MessagePage mirrors the MessagePage schema.
type MessagePage struct {
	Messages     []Message `json:"messages"`
	HasMore      bool      `json:"hasMore"`
	HasMoreAfter bool      `json:"hasMoreAfter"`
	TurnsBefore  int64     `json:"turnsBefore"`
	TurnsTotal   int64     `json:"turnsTotal"`
}

// SendMessageResult mirrors the SendMessageResult schema.
type SendMessageResult struct {
	Message Message `json:"message"`
	Run     Run     `json:"run"`
}

// ---- Conversions ----

// AgentAddress returns the Agent's address; nil when the name does not satisfy the naming rules.
func AgentAddress(a store.Agent) *string {
	if addr, ok := a.Address(); ok {
		return &addr
	}
	return nil
}

// FromAgentListItem converts a list item.
func FromAgentListItem(a store.AgentListItem) AgentListItem {
	return AgentListItem{Sleep: a.Sleep, SleepRevision: a.SleepRevision, ID: a.ID, Scope: ScopeOf(a.ProjectID), ProjectID: a.ProjectID, IsManager: a.IsManager,
		Name: a.Name, Address: AgentAddress(a.Agent), Model: ModelRef{a.ModelProvider, a.ModelID}, Runtime: agentruntime.ForProvider(a.ModelProvider).ID, ConfigVersion: a.ConfigVersion, ThinkingLevel: a.ThinkingLevel, ServiceTier: a.ServiceTier,
		Busy: a.Busy, BackgroundTaskCount: a.BackgroundTasks, SessionState: a.SessionState, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}
}

// FromAgentDetail converts Agent details.
func FromAgentDetail(d agentconfig.AgentDetail) Agent {
	out := Agent{ID: d.Agent.ID, Scope: ScopeOf(d.Agent.ProjectID), ProjectID: d.Agent.ProjectID, IsManager: d.Agent.IsManager, Name: d.Agent.Name, Address: AgentAddress(d.Agent), Model: ModelRef{d.Agent.ModelProvider, d.Agent.ModelID}, Runtime: agentruntime.ForProvider(d.Agent.ModelProvider).ID,
		Prompt: d.Agent.Prompt, PromptVersion: d.Agent.PromptVersion, ConfigVersion: d.Agent.ConfigVersion, Busy: d.Busy, BackgroundTaskCount: d.BackgroundTasks, ThinkingLevel: d.Agent.ThinkingLevel, ServiceTier: d.Agent.ServiceTier,
		Session: SessionInfo{State: d.Session.State, Epoch: d.Session.Epoch}, Clis: []AgentCli{},
		ModelCredential: FromCredential(d.ModelCredential), CreatedAt: d.Agent.CreatedAt, UpdatedAt: d.Agent.UpdatedAt}
	for _, c := range d.CLIs {
		out.Clis = append(out.Clis, FromCLI(c))
	}
	return out
}

// FromCLI converts an available CLI.
func FromCLI(c agentconfig.CLIView) AgentCli {
	return AgentCli{Name: c.Name, HasTemplate: c.HasTemplate, Credential: FromCredential(c.Credential)}
}

// FromCredential converts credential metadata.
func FromCredential(v *agentconfig.CredentialView) *CredentialMeta {
	if v == nil {
		return nil
	}
	return &CredentialMeta{ProjectID: v.ProjectID, AccountID: v.AccountID, Provider: v.Provider, AuthMethod: v.AuthMethod, VersionID: v.VersionID, AccountLabel: v.AccountLabel, Status: v.Status, UpdatedAt: v.UpdatedAt, FieldNames: v.FieldNames, EnvNames: v.EnvNames, FilePaths: v.FilePaths, Warnings: v.Warnings}
}

func skillFiles(files []store.SkillFile) []SkillFile {
	out := make([]SkillFile, 0, len(files))
	for _, f := range files {
		out = append(out, SkillFile{Path: f.Path, Size: f.Size, SHA256: f.SHA256})
	}
	return out
}

// FromTemplateSummary converts a template list item.
func FromTemplateSummary(t store.SkillTemplate) SkillTemplateSummary {
	return SkillTemplateSummary{ID: t.ID, Name: t.Name, Description: t.Description, FileCount: t.FileCount, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}
}

// FromTemplate converts template details.
func FromTemplate(t agentconfig.TemplateDetail) SkillTemplate {
	return SkillTemplate{ID: t.ID, Name: t.Name, Description: t.Description, Files: skillFiles(t.Files), SkillMD: t.SkillMD, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}
}

// FromAgentSkillSummary converts a list item of an Agent's Skill copy.
func FromAgentSkillSummary(k store.AgentSkill) AgentSkillSummary {
	return AgentSkillSummary{ID: k.ID, Name: k.Name, Description: k.Description, SourceTemplateID: k.SourceTemplateID, FileCount: k.FileCount, CopiedAt: k.CopiedAt, UpdatedAt: k.UpdatedAt}
}

// FromAgentSkill converts the details of an Agent's Skill copy.
func FromAgentSkill(k agentconfig.AgentSkillDetail) AgentSkill {
	return AgentSkill{ID: k.ID, Name: k.Name, Description: k.Description, SourceTemplateID: k.SourceTemplateID, Files: skillFiles(k.Files), SkillMD: k.SkillMD, CopiedAt: k.CopiedAt, UpdatedAt: k.UpdatedAt}
}
