package apitypes

import (
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/store"
)

// ErrorBody 对应 Error。
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail 是 Error.error。
type ErrorDetail struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Details any          `json:"details,omitempty"`
	Fields  []FieldError `json:"fields,omitempty"`
}

// FieldError 对应 FieldError。
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Context 对应 Context。
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
}

// Limits 对应 Limits。
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

// TextEdit 是 Limits.textEdit。
type TextEdit struct {
	MaxBytes   int64    `json:"maxBytes"`
	Extensions []string `json:"extensions"`
}

// Health 对应 Health。
type Health struct {
	OK       bool             `json:"ok"`
	Postgres DependencyHealth `json:"postgres"`
	Redis    DependencyHealth `json:"redis"`
}

// DependencyHealth 对应 DependencyHealth。
type DependencyHealth struct {
	OK    bool    `json:"ok"`
	Error *string `json:"error"`
}

// RedactedHealth 是给外部身份的健康状态：结构相同，但各依赖没有 error 字段。
type RedactedHealth struct {
	OK       bool               `json:"ok"`
	Postgres RedactedDependency `json:"postgres"`
	Redis    RedactedDependency `json:"redis"`
}

// RedactedDependency 只表明依赖是否可用。
type RedactedDependency struct {
	OK bool `json:"ok"`
}

// Redacted 返回去掉错误信息的健康状态。
func (h Health) Redacted() RedactedHealth {
	return RedactedHealth{OK: h.OK, Postgres: RedactedDependency{OK: h.Postgres.OK}, Redis: RedactedDependency{OK: h.Redis.OK}}
}

// ModelProviderList 对应 ModelProviderList。
type ModelProviderList struct {
	PiVersion          string          `json:"piVersion"`
	CatalogGeneratedAt *time.Time      `json:"catalogGeneratedAt"`
	Providers          []ModelProvider `json:"providers"`
}

// ModelProvider 对应 ModelProvider。
type ModelProvider struct {
	ID            string   `json:"id"`
	Source        string   `json:"source"`
	APIKeyEnvVars []string `json:"apiKeyEnvVars"`
	AuthMethods   []string `json:"authMethods"`
	ModelCount    int      `json:"modelCount"`
}

// ModelList 对应 ModelList。
type ModelList struct {
	ProviderID string  `json:"providerId"`
	Models     []Model `json:"models"`
}

// Model 对应 Model。
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

// CliTemplate 对应 CliTemplate。
type CliTemplate struct {
	Name      string                `json:"name"`
	Version   string                `json:"version"`
	Fields    []clitemplate.Field   `json:"fields"`
	Injection string                `json:"injection"`
	Install   CliInstall            `json:"install"`
	Warnings  []clitemplate.Warning `json:"warnings"`
}

// CliInstall 是 CliTemplate.install。
type CliInstall struct {
	Status string  `json:"status"`
	Error  *string `json:"error"`
}

// ModelRef 对应 ModelRef。
type ModelRef struct {
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

// AgentListItem 对应 AgentListItem。
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
	ConfigVersion int64              `json:"configVersion"`
	Busy          bool               `json:"busy"`
	SessionState  string             `json:"sessionState"`
	CreatedAt     time.Time          `json:"createdAt"`
	UpdatedAt     time.Time          `json:"updatedAt"`
}

// Agent 对应 Agent。
type Agent struct {
	ServiceTier     *string         `json:"serviceTier"`
	ThinkingLevel   *string         `json:"thinkingLevel"`
	ID              uuid.UUID       `json:"id"`
	Scope           string          `json:"scope"`
	ProjectID       *uuid.UUID      `json:"projectId"`
	IsManager       bool            `json:"isManager"`
	Name            string          `json:"name"`
	Address         *string         `json:"address"`
	Model           ModelRef        `json:"model"`
	Prompt          string          `json:"prompt"`
	PromptVersion   int64           `json:"promptVersion"`
	ConfigVersion   int64           `json:"configVersion"`
	Busy            bool            `json:"busy"`
	Session         SessionInfo     `json:"session"`
	Clis            []AgentCli      `json:"clis"`
	ModelCredential *CredentialMeta `json:"modelCredential"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
}

// CredentialMeta 对应 CredentialMeta。
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

// AgentCli 对应 AgentCli。
type AgentCli struct {
	Name        string          `json:"name"`
	HasTemplate bool            `json:"hasTemplate"`
	Credential  *CredentialMeta `json:"credential"`
}

// SkillFile 对应 SkillFile。
type SkillFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// SkillTemplateSummary 对应 SkillTemplateSummary。
type SkillTemplateSummary struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	FileCount   int       `json:"fileCount"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// SkillTemplate 对应 SkillTemplate。
type SkillTemplate struct {
	ID          uuid.UUID   `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Files       []SkillFile `json:"files"`
	SkillMD     string      `json:"skillMd"`
	CreatedAt   time.Time   `json:"createdAt"`
	UpdatedAt   time.Time   `json:"updatedAt"`
}

// AgentSkillSummary 对应 AgentSkillSummary。
type AgentSkillSummary struct {
	ID               uuid.UUID  `json:"id"`
	Name             string     `json:"name"`
	Description      string     `json:"description"`
	SourceTemplateID *uuid.UUID `json:"sourceTemplateId"`
	FileCount        int        `json:"fileCount"`
	CopiedAt         time.Time  `json:"copiedAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

// AgentSkill 对应 AgentSkill。
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

// MessagePage 对应 MessagePage。
type MessagePage struct {
	Messages     []Message `json:"messages"`
	HasMore      bool      `json:"hasMore"`
	HasMoreAfter bool      `json:"hasMoreAfter"`
}

// SendMessageResult 对应 SendMessageResult。
type SendMessageResult struct {
	Message Message `json:"message"`
	Run     Run     `json:"run"`
}

// ---- 转换 ----

// AgentAddress 返回 Agent 的地址；名称不满足命名规则时为 nil。
func AgentAddress(a store.Agent) *string {
	if addr, ok := a.Address(); ok {
		return &addr
	}
	return nil
}

// FromAgentListItem 转换列表项。
func FromAgentListItem(a store.AgentListItem) AgentListItem {
	return AgentListItem{Sleep: a.Sleep, SleepRevision: a.SleepRevision, ID: a.ID, Scope: ScopeOf(a.ProjectID), ProjectID: a.ProjectID, IsManager: a.IsManager,
		Name: a.Name, Address: AgentAddress(a.Agent), Model: ModelRef{a.ModelProvider, a.ModelID}, ConfigVersion: a.ConfigVersion, ThinkingLevel: a.ThinkingLevel, ServiceTier: a.ServiceTier,
		Busy: a.Busy, SessionState: a.SessionState, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}
}

// FromAgentDetail 转换 Agent 详情。
func FromAgentDetail(d agentconfig.AgentDetail) Agent {
	out := Agent{ID: d.Agent.ID, Scope: ScopeOf(d.Agent.ProjectID), ProjectID: d.Agent.ProjectID, IsManager: d.Agent.IsManager, Name: d.Agent.Name, Address: AgentAddress(d.Agent), Model: ModelRef{d.Agent.ModelProvider, d.Agent.ModelID},
		Prompt: d.Agent.Prompt, PromptVersion: d.Agent.PromptVersion, ConfigVersion: d.Agent.ConfigVersion, Busy: d.Busy, ThinkingLevel: d.Agent.ThinkingLevel, ServiceTier: d.Agent.ServiceTier,
		Session: SessionInfo{State: d.Session.State, Epoch: d.Session.Epoch}, Clis: []AgentCli{},
		ModelCredential: FromCredential(d.ModelCredential), CreatedAt: d.Agent.CreatedAt, UpdatedAt: d.Agent.UpdatedAt}
	for _, c := range d.CLIs {
		out.Clis = append(out.Clis, FromCLI(c))
	}
	return out
}

// FromCLI 转换可用 CLI。
func FromCLI(c agentconfig.CLIView) AgentCli {
	return AgentCli{Name: c.Name, HasTemplate: c.HasTemplate, Credential: FromCredential(c.Credential)}
}

// FromCredential 转换凭证元数据。
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

// FromTemplateSummary 转换模板列表项。
func FromTemplateSummary(t store.SkillTemplate) SkillTemplateSummary {
	return SkillTemplateSummary{ID: t.ID, Name: t.Name, Description: t.Description, FileCount: t.FileCount, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}
}

// FromTemplate 转换模板详情。
func FromTemplate(t agentconfig.TemplateDetail) SkillTemplate {
	return SkillTemplate{ID: t.ID, Name: t.Name, Description: t.Description, Files: skillFiles(t.Files), SkillMD: t.SkillMD, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}
}

// FromAgentSkillSummary 转换副本列表项。
func FromAgentSkillSummary(k store.AgentSkill) AgentSkillSummary {
	return AgentSkillSummary{ID: k.ID, Name: k.Name, Description: k.Description, SourceTemplateID: k.SourceTemplateID, FileCount: k.FileCount, CopiedAt: k.CopiedAt, UpdatedAt: k.UpdatedAt}
}

// FromAgentSkill 转换副本详情。
func FromAgentSkill(k agentconfig.AgentSkillDetail) AgentSkill {
	return AgentSkill{ID: k.ID, Name: k.Name, Description: k.Description, SourceTemplateID: k.SourceTemplateID, Files: skillFiles(k.Files), SkillMD: k.SkillMD, CopiedAt: k.CopiedAt, UpdatedAt: k.UpdatedAt}
}
