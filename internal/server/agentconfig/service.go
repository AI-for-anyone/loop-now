// Package agentconfig is the business layer for Agent configuration: input validation, model catalog and CLI template
// validation, saving credentials, and telling the executor to release an Agent when it is deleted. The console API
// calls this package and never accesses store directly.
package agentconfig

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/content"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/naming"
	"loop-now/internal/server/store"
)

// Releaser stops an Agent's Pi process and deletes its own HOME (called when an Agent is deleted).
type Releaser interface {
	Release(ctx context.Context, agentID uuid.UUID) error
}

// Limits are the limits used by the business layer, taken from the config.
type Limits struct {
	MaxUploadFile   int64
	MaxSkillArchive int64
	MaxSkillEntries int
	MaxCredential   int64
	Text            content.TextRules
}

// Service is the business layer for Agent configuration.
type Service struct {
	// PluginAssets returns what the Agent's granted plugins hand to Pi; when nil, no plugins are mounted.
	PluginAssets           func(context.Context, uuid.UUID) (PluginAssets, error)
	CancelProjectCLILogins func(context.Context, uuid.UUID) error
	ModelAuth              *modelauth.Service
	Store                  *store.Store
	Models                 modelcatalog.Reader
	Templates              *clitemplate.Catalog
	Releaser               Releaser
	OrgID                  uuid.UUID // default organization holding all projects (the first version has no other)
	Limits                 Limits
	Logger                 *slog.Logger
	// AgentChanged is called after an Agent is created or changed and AgentRemoved after it is deleted, to push the
	// overview; both may be nil.
	AgentChanged func(id uuid.UUID)
	AgentRemoved func(id uuid.UUID)
	// LocalAccess returns what local mode hands to this Agent's Pi; when nil, local mode is not enabled.
	LocalAccess func(context.Context, uuid.UUID) (LocalAccess, error)
	// ProviderState returns the state directory of a plugin provider subscription account and the paths that travel
	// with the account (plugins/SPEC.md §13).
	ProviderState ProviderStateFunc
}

var (
	cliNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// ---- Agent ----

func (s *Service) validateName(name string) *apperr.FieldError {
	if msg := naming.Validate("Agent name", name); msg != "" {
		f := apperr.Field("name", msg)
		return &f
	}
	return nil
}

func (s *Service) validateModel(provider, modelID string) *apperr.FieldError {
	if _, ok := s.Models.Provider(provider); !ok {
		f := apperr.Field("model.provider", "The model catalog has no provider "+provider)
		return &f
	}
	if !s.Models.HasModel(provider, modelID) {
		f := apperr.Field("model.modelId", "Provider "+provider+" has no model "+modelID)
		return &f
	}
	return nil
}

// pluginProvider returns the plugin that registered the model provider named provider; ok is false when it is not a
// plugin provider.
func (s *Service) pluginProvider(provider string) (string, bool) {
	if s.Models == nil {
		return "", false
	}
	p, ok := s.Models.Provider(provider)
	if !ok || p.Source != "plugin" || p.PluginID == nil {
		return "", false
	}
	return *p.PluginID, true
}

// PluginProviderUnusable reports whether the Agent can use the plugin-registered model provider named provider now:
// reason is empty when it is not a plugin provider or it can be used; otherwise reason is one of store.PluginUnusable*
// and plugin is the plugin that registered it.
func (s *Service) PluginProviderUnusable(ctx context.Context, agent uuid.UUID, provider string) (plugin, reason string, err error) {
	plugin, ok := s.pluginProvider(provider)
	if !ok {
		return "", "", nil
	}
	reason, err = s.Store.AgentPluginUsability(ctx, agent, plugin)
	return plugin, reason, err
}

// validatePluginProvider requires an Agent that picks a plugin-registered model provider to be able to use that
// plugin: a project Agent needs the plugin enabled in its project and granted to it; a platform Agent only needs the
// grant. A nil agent means a new Agent, which has no grants yet and so cannot pick a plugin provider.
func (s *Service) validatePluginProvider(ctx context.Context, agent *uuid.UUID, provider string) error {
	plugin, ok := s.pluginProvider(provider)
	if !ok {
		return nil
	}
	if agent == nil {
		return apperr.Invalid("model.provider", "Model provider "+provider+" comes from plugin "+plugin+". Create the Agent first and grant it the plugin (a project Agent also needs the plugin enabled in its project), then choose this provider.")
	}
	_, reason, err := s.PluginProviderUnusable(ctx, *agent, provider)
	if err != nil {
		return err
	}
	switch reason {
	case store.PluginUnusableNotEnabled:
		return apperr.Invalid("model.provider", "Model provider "+provider+" comes from plugin "+plugin+". Enable the plugin in the project first.")
	case store.PluginUnusableNotGranted:
		return apperr.Invalid("model.provider", "Model provider "+provider+" comes from plugin "+plugin+". Grant the plugin to this Agent first.")
	}
	return nil
}

func collect(errs ...*apperr.FieldError) error {
	var out []apperr.FieldError
	for _, e := range errs {
		if e != nil {
			out = append(out, *e)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return apperr.Validation(out...)
}

// CreateAgent creates an Agent in a project or on the platform; where it belongs cannot change afterwards.
func (s *Service) CreateAgent(ctx context.Context, scope store.Scope, name, provider, modelID, prompt string, modelOptions ...*string) (store.Agent, error) {
	if err := collect(s.validateName(name), s.validateModel(provider, modelID)); err != nil {
		return store.Agent{}, err
	}
	if err := s.validatePluginProvider(ctx, nil, provider); err != nil {
		return store.Agent{}, err
	}
	var level *string
	if len(modelOptions) > 0 {
		level = modelOptions[0]
	}
	if level != nil {
		if _, err := s.Models.ResolveThinking(provider, modelID, level); err != nil {
			return store.Agent{}, apperr.Invalid("thinkingLevel", err.Error())
		}
	}
	var tier *string
	if len(modelOptions) > 1 {
		tier = modelOptions[1]
	}
	if _, err := ResolveServiceTier(provider, tier); err != nil {
		return store.Agent{}, err
	}
	a, err := s.Store.CreateAgent(ctx, scope, name, provider, modelID, prompt, level, tier)
	if err == nil && s.AgentChanged != nil {
		s.AgentChanged(a.ID)
	}
	return a, err
}

// AgentPatch is the input for changing an Agent; nil means no change. A model must be given as provider and model ID
// together.
type AgentPatch struct {
	ServiceTier           *string
	ServiceTierSet        bool
	ThinkingLevel         *string
	ThinkingLevelSet      bool
	ExpectedPromptVersion *int64
	Name                  *string
	Provider              *string
	ModelID               *string
	Prompt                *string
}

// UpdateAgent changes an Agent.
func (s *Service) UpdateAgent(ctx context.Context, id uuid.UUID, p AgentPatch) (store.Agent, error) {
	var errs []*apperr.FieldError
	if p.Name != nil {
		errs = append(errs, s.validateName(*p.Name))
	}
	if (p.Provider == nil) != (p.ModelID == nil) {
		f := apperr.Field("model", "Change provider and modelId together.")
		errs = append(errs, &f)
	} else if p.Provider != nil {
		errs = append(errs, s.validateModel(*p.Provider, *p.ModelID))
	}
	if err := collect(errs...); err != nil {
		return store.Agent{}, err
	}
	if p.Provider != nil {
		if err := s.validatePluginProvider(ctx, &id, *p.Provider); err != nil {
			return store.Agent{}, err
		}
	}
	patch := store.AgentPatch{Name: p.Name, ModelProvider: p.Provider, ModelID: p.ModelID, Prompt: p.Prompt, ExpectedPromptVersion: p.ExpectedPromptVersion, ThinkingLevel: p.ThinkingLevel, ThinkingLevelSet: p.ThinkingLevelSet, ServiceTier: p.ServiceTier, ServiceTierSet: p.ServiceTierSet}
	if p.Provider != nil || p.ThinkingLevelSet || p.ServiceTierSet {
		patch.Validate = func(a store.Agent) error {
			if _, err := ResolveServiceTier(a.ModelProvider, a.ServiceTier); err != nil {
				return err
			}
			if a.ThinkingLevel != nil {
				if _, err := s.Models.ResolveThinking(a.ModelProvider, a.ModelID, a.ThinkingLevel); err != nil {
					return apperr.Invalid("thinkingLevel", err.Error())
				}
			}
			return nil
		}
	}
	a, err := s.Store.UpdateAgent(ctx, id, patch)
	if err == nil && s.AgentChanged != nil {
		s.AgentChanged(id)
	}
	return a, err
}

// DeleteAgent soft-deletes an idle Agent and tells the executor to stop its process and delete its own HOME. A failed
// release in the executor is only logged: the Agent is already deleted in the database, and the executor also cleans up
// the HOME of deleted Agents when it starts.
func (s *Service) DeleteAgent(ctx context.Context, id uuid.UUID) error {
	if err := s.Store.SoftDeleteAgent(ctx, id); err != nil {
		return err
	}
	s.cancelDeletedAgentLogins(ctx, []uuid.UUID{id})
	if s.Releaser != nil {
		if err := s.Releaser.Release(ctx, id); err != nil {
			s.Logger.Error("failed to release the run resources of a deleted Agent", "agentId", id, "err", err)
		}
	}
	if s.AgentRemoved != nil {
		s.AgentRemoved(id)
	}
	return nil
}

// AgentDetail is the detail of an Agent.
type AgentDetail struct {
	Agent           store.Agent
	Busy            bool
	BackgroundTasks int // how many background tasks run on the Agent's runtime process
	Session         store.AgentSession
	CLIs            []CLIView
	ModelCredential *CredentialView
}

// CLIView is the display information of an available CLI.
type CLIView struct {
	Name        string
	HasTemplate bool
	Credential  *CredentialView
}

// CredentialView is credential metadata, without plaintext.
type CredentialView struct {
	ProjectID, AccountID               *uuid.UUID
	Provider, AuthMethod, AccountLabel *string
	VersionID                          *uuid.UUID
	Status                             string // set / unavailable / needs_reentry
	UpdatedAt                          string
	FieldNames                         []string
	EnvNames                           []string
	FilePaths                          []string
	Warnings                           []clitemplate.Warning
	row                                store.CredentialRow
}

// GetAgentDetail returns the detail of an Agent.
func (s *Service) GetAgentDetail(ctx context.Context, id uuid.UUID) (AgentDetail, error) {
	var d AgentDetail
	var err error
	if d.Agent, err = s.Store.GetAgent(ctx, id); err != nil {
		return d, err
	}
	if d.Session, err = s.Store.GetAgentSession(ctx, id); err != nil {
		return d, err
	}
	if d.Busy, err = s.Store.AgentBusy(ctx, id); err != nil {
		return d, err
	}
	if d.BackgroundTasks, err = s.Store.BackgroundTaskCount(ctx, id); err != nil {
		return d, err
	}
	d.CLIs, d.ModelCredential, err = s.credentialViews(ctx, id)
	return d, err
}

func (s *Service) credentialViews(ctx context.Context, agentID uuid.UUID) ([]CLIView, *CredentialView, error) {
	names, err := s.Store.ListAgentCLIs(ctx, agentID)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.Store.ListCredentials(ctx, agentID)
	if err != nil {
		return nil, nil, err
	}
	byCLI := map[string]*CredentialView{}
	var model *CredentialView
	for _, r := range rows {
		v := s.view(r)
		if r.Purpose == store.PurposeModel {
			model = v
		} else {
			byCLI[r.CLIName] = v
		}
	}
	clis := make([]CLIView, 0, len(names))
	for _, n := range names {
		_, has := s.Templates.Get(n)
		if has && s.ModelAuth != nil && s.ModelAuth.Project != nil {
			a, e := s.Store.GetAgent(ctx, agentID)
			if e != nil {
				return nil, nil, e
			}
			if a.ProjectID != nil {
				meta, e := s.ModelAuth.Project.CurrentCLIMetadata(ctx, agentID, n)
				if e != nil {
					return nil, nil, e
				}
				byCLI[n] = nil
				if meta != nil {
					byCLI[n] = &CredentialView{ProjectID: meta.ProjectID, AccountID: meta.AccountID, Status: meta.Status, UpdatedAt: meta.UpdatedAt, FieldNames: meta.FieldNames, EnvNames: meta.EnvNames, FilePaths: meta.FilePaths, Warnings: meta.Warnings, VersionID: meta.VersionID}
				}
			}
		}
		clis = append(clis, CLIView{Name: n, HasTemplate: has, Credential: byCLI[n]})
	}
	if s.ModelAuth != nil {
		meta, e := s.ModelAuth.Metadata(ctx, agentID)
		if e != nil {
			return nil, nil, e
		}
		model = nil
		if meta != nil {
			model = &CredentialView{ProjectID: meta.ProjectID, AccountID: meta.AccountID, Status: meta.Status, UpdatedAt: meta.UpdatedAt, FieldNames: meta.FieldNames, EnvNames: meta.EnvNames, FilePaths: meta.FilePaths, Warnings: meta.Warnings, Provider: meta.Provider, AuthMethod: meta.AuthMethod, AccountLabel: meta.AccountLabel, VersionID: meta.VersionID}
		}
	}
	return clis, model, nil
}

func (s *Service) view(r store.CredentialRow) *CredentialView {
	v := &CredentialView{
		Status:     "set",
		UpdatedAt:  r.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		FieldNames: nonNil(r.FieldNames),
		EnvNames:   nonNil(r.EnvNames),
		FilePaths:  nonNil(r.FilePaths),
		Warnings:   []clitemplate.Warning{},
		row:        r,
	}
	if r.Purpose == store.PurposeCLI {
		if t, ok := s.Templates.Get(r.CLIName); ok {
			v.Warnings = t.PlatformWarnings()
			if r.TemplateSource != "builtin" || r.TemplateVersion != t.CredentialFormat() {
				v.Status = "needs_reentry"
			}
		} else {
			v.Status = "unavailable"
		}
	}
	return v
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ---- Available CLIs and credentials ----

func validateCLIName(name string) error {
	if !cliNamePattern.MatchString(name) {
		return apperr.Invalid("cli", "A CLI name may contain only lowercase letters, digits, dots, underscores and hyphens, must start with a letter or digit, and must be at most 64 characters.")
	}
	return nil
}

// AddCLI adds a CLI to the available list and returns its display information.
func (s *Service) AddCLI(ctx context.Context, agentID uuid.UUID, name string) (CLIView, error) {
	if err := validateCLIName(name); err != nil {
		return CLIView{}, err
	}
	if err := s.Store.AddAgentCLI(ctx, agentID, name); err != nil {
		return CLIView{}, err
	}
	clis, _, err := s.credentialViews(ctx, agentID)
	if err != nil {
		return CLIView{}, err
	}
	for _, c := range clis {
		if c.Name == name {
			return c, nil
		}
	}
	return CLIView{}, apperr.NotFound("Allowed CLI " + name)
}

// RemoveCLI removes a CLI from the available list and deletes its credential.
func (s *Service) RemoveCLI(ctx context.Context, agentID uuid.UUID, name string) error {
	if err := validateCLIName(name); err != nil {
		return err
	}
	return s.Store.RemoveAgentCLI(ctx, agentID, name)
}

// PutCLICredential renders a CLI credential from the template and saves it.
func (s *Service) PutCLICredential(ctx context.Context, agentID uuid.UUID, cli string, fields map[string]string) (*CredentialView, error) {
	if err := validateCLIName(cli); err != nil {
		return nil, err
	}
	t, ok := s.Templates.Get(cli)
	if !ok {
		return nil, apperr.Invalid("cli", cli+" has no built-in template. It is only a marker and cannot take credentials.")
	}
	payload, names, err := t.Render(fields)
	if err != nil {
		return nil, err
	}
	meta := store.CredentialMeta{TemplateVersion: t.CredentialFormat(), FieldNames: names, EnvNames: payload.EnvNames(), FilePaths: payload.FilePaths()}
	return s.putCredential(ctx, agentID, store.PurposeCLI, cli, payload, meta)
}

// PutModelCredential saves a model account credential (environment variables).
func (s *Service) PutModelCredential(ctx context.Context, agentID uuid.UUID, env map[string]string) (*CredentialView, error) {
	if len(env) == 0 {
		return nil, apperr.Invalid("env", "At least one environment variable is required.")
	}
	var errs []apperr.FieldError
	for k, v := range env {
		if !envNamePattern.MatchString(k) {
			errs = append(errs, apperr.Field("env."+k, "Must be a valid environment variable name (letters, digits and underscores, not starting with a digit)."))
		} else if executor.IsReservedEnv(k) {
			errs = append(errs, apperr.Field("env."+k, k+" is controlled by the executor and cannot be a credential."))
		} else if tmpl, ok := s.Templates.ReservedEnvBy(k); ok {
			errs = append(errs, apperr.Field("env."+k, k+" is read by the CLI "+tmpl+" and cannot be a model account credential."))
		} else if v == "" {
			errs = append(errs, apperr.Field("env."+k, "The value must not be empty."))
		}
	}
	if len(errs) > 0 {
		return nil, apperr.Validation(errs...)
	}
	payload := clitemplate.Payload{Env: env, Files: []clitemplate.PayloadFile{}}
	meta := store.CredentialMeta{EnvNames: payload.EnvNames()}
	return s.putCredential(ctx, agentID, store.PurposeModel, "", payload, meta)
}

func (s *Service) putCredential(ctx context.Context, agentID uuid.UUID, purpose, cli string, payload clitemplate.Payload, meta store.CredentialMeta) (*CredentialView, error) {
	plain, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	if int64(len(plain)) > s.Limits.MaxCredential {
		return nil, apperr.TooLarge("The credential exceeds the size limit.")
	}
	row, err := s.Store.PutCredential(ctx, agentID, purpose, cli, meta, plain)
	if err != nil {
		return nil, err
	}
	return s.view(row), nil
}

// DeleteCLICredential deletes a CLI credential (the CLI stays in the available list).
func (s *Service) DeleteCLICredential(ctx context.Context, agentID uuid.UUID, cli string) error {
	if err := validateCLIName(cli); err != nil {
		return err
	}
	return s.Store.DeleteCredential(ctx, agentID, store.PurposeCLI, cli)
}

// DeleteModelCredential deletes the model account credential.
func (s *Service) DeleteModelCredential(ctx context.Context, agentID uuid.UUID) error {
	return s.Store.DeleteCredential(ctx, agentID, store.PurposeModel, "")
}
