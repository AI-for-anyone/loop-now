// Package agentconfig 是 Agent 配置的业务层：输入校验、模型目录与 CLI 模板校验、凭证保存，
// 以及删除 Agent 时通知执行器释放。控制台接口调用这里，不直接访问 store。
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

// Releaser 停止 Agent 的 Pi 进程并删除其独立 HOME（删除 Agent 时调用）。
type Releaser interface {
	Release(ctx context.Context, agentID uuid.UUID) error
}

// Limits 是业务层使用的上限，取自配置。
type Limits struct {
	MaxUploadFile   int64
	MaxSkillArchive int64
	MaxSkillEntries int
	MaxCredential   int64
	Text            content.TextRules
}

// Service 是 Agent 配置的业务层。
type Service struct {
	// PluginAssets 返回 Agent 已授权插件交给 Pi 的东西；为空时不挂载插件。
	PluginAssets           func(context.Context, uuid.UUID) (PluginAssets, error)
	CancelProjectCLILogins func(context.Context, uuid.UUID) error
	ModelAuth              *modelauth.Service
	Store                  *store.Store
	Models                 *modelcatalog.Catalog
	Templates              *clitemplate.Catalog
	Releaser               Releaser
	OrgID                  uuid.UUID // 默认组织：项目都建在这里（第一版只有默认组织）
	Limits                 Limits
	Logger                 *slog.Logger
	// AgentChanged 在 Agent 新建或修改后调用，AgentRemoved 在删除后调用，用于推送概览；都可为空。
	AgentChanged func(id uuid.UUID)
	AgentRemoved func(id uuid.UUID)
}

var (
	cliNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// ---- Agent ----

func (s *Service) validateName(name string) *apperr.FieldError {
	if msg := naming.Validate("Agent 名称", name); msg != "" {
		f := apperr.Field("name", msg)
		return &f
	}
	return nil
}

func (s *Service) validateModel(provider, modelID string) *apperr.FieldError {
	if _, ok := s.Models.Provider(provider); !ok {
		f := apperr.Field("model.provider", "模型目录中没有提供方 "+provider)
		return &f
	}
	if !s.Models.HasModel(provider, modelID) {
		f := apperr.Field("model.modelId", "提供方 "+provider+" 下没有模型 "+modelID)
		return &f
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

// CreateAgent 在项目或平台中创建 Agent；归属之后不能改变。
func (s *Service) CreateAgent(ctx context.Context, scope store.Scope, name, provider, modelID, prompt string, modelOptions ...*string) (store.Agent, error) {
	if err := collect(s.validateName(name), s.validateModel(provider, modelID)); err != nil {
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

// AgentPatch 是修改 Agent 的输入；nil 表示不改。模型须提供方与模型 ID 一起给出。
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

// UpdateAgent 修改 Agent。
func (s *Service) UpdateAgent(ctx context.Context, id uuid.UUID, p AgentPatch) (store.Agent, error) {
	var errs []*apperr.FieldError
	if p.Name != nil {
		errs = append(errs, s.validateName(*p.Name))
	}
	if (p.Provider == nil) != (p.ModelID == nil) {
		f := apperr.Field("model", "提供方与模型 ID 必须一起修改")
		errs = append(errs, &f)
	} else if p.Provider != nil {
		errs = append(errs, s.validateModel(*p.Provider, *p.ModelID))
	}
	if err := collect(errs...); err != nil {
		return store.Agent{}, err
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

// DeleteAgent 软删除空闲的 Agent，并通知执行器停止进程、删除独立 HOME。
// 执行器释放失败只记录日志：数据库中已删除，执行器启动时还会清理已删除 Agent 的 HOME。
func (s *Service) DeleteAgent(ctx context.Context, id uuid.UUID) error {
	if err := s.Store.SoftDeleteAgent(ctx, id); err != nil {
		return err
	}
	s.cancelDeletedAgentLogins(ctx, []uuid.UUID{id})
	if s.Releaser != nil {
		if err := s.Releaser.Release(ctx, id); err != nil {
			s.Logger.Error("释放已删除 Agent 的执行资源失败", "agentId", id, "err", err)
		}
	}
	if s.AgentRemoved != nil {
		s.AgentRemoved(id)
	}
	return nil
}

// AgentDetail 是 Agent 详情。
type AgentDetail struct {
	Agent           store.Agent
	Busy            bool
	Session         store.AgentSession
	CLIs            []CLIView
	ModelCredential *CredentialView
}

// CLIView 是可用 CLI 的展示信息。
type CLIView struct {
	Name        string
	HasTemplate bool
	Credential  *CredentialView
}

// CredentialView 是凭证元数据，不含明文。
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

// GetAgentDetail 返回 Agent 详情。
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

// ---- 可用 CLI 与凭证 ----

func validateCLIName(name string) error {
	if !cliNamePattern.MatchString(name) {
		return apperr.Invalid("cli", "CLI 名称只能由小写字母、数字、点、下划线和连字符组成，最长 64 个字符")
	}
	return nil
}

// AddCLI 把 CLI 加入可用列表，返回它的展示信息。
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
	return CLIView{}, apperr.NotFound("可用 CLI " + name)
}

// RemoveCLI 从可用列表移除 CLI，并删除它的凭证。
func (s *Service) RemoveCLI(ctx context.Context, agentID uuid.UUID, name string) error {
	if err := validateCLIName(name); err != nil {
		return err
	}
	return s.Store.RemoveAgentCLI(ctx, agentID, name)
}

// PutCLICredential 按模板渲染并保存 CLI 凭证。
func (s *Service) PutCLICredential(ctx context.Context, agentID uuid.UUID, cli string, fields map[string]string) (*CredentialView, error) {
	if err := validateCLIName(cli); err != nil {
		return nil, err
	}
	t, ok := s.Templates.Get(cli)
	if !ok {
		return nil, apperr.Invalid("cli", cli+" 没有内置模板，只作标记，不能录入凭证")
	}
	payload, names, err := t.Render(fields)
	if err != nil {
		return nil, err
	}
	meta := store.CredentialMeta{TemplateVersion: t.CredentialFormat(), FieldNames: names, EnvNames: payload.EnvNames(), FilePaths: payload.FilePaths()}
	return s.putCredential(ctx, agentID, store.PurposeCLI, cli, payload, meta)
}

// PutModelCredential 保存模型账号凭证（环境变量）。
func (s *Service) PutModelCredential(ctx context.Context, agentID uuid.UUID, env map[string]string) (*CredentialView, error) {
	if len(env) == 0 {
		return nil, apperr.Invalid("env", "至少需要一个环境变量")
	}
	var errs []apperr.FieldError
	for k, v := range env {
		if !envNamePattern.MatchString(k) {
			errs = append(errs, apperr.Field("env."+k, "环境变量名不合法"))
		} else if executor.IsReservedEnv(k) {
			errs = append(errs, apperr.Field("env."+k, k+" 由执行器控制，不能作为凭证"))
		} else if tmpl, ok := s.Templates.ReservedEnvBy(k); ok {
			errs = append(errs, apperr.Field("env."+k, k+" 会被 CLI "+tmpl+" 读取，不能作为模型账号凭证"))
		} else if v == "" {
			errs = append(errs, apperr.Field("env."+k, "值不能为空"))
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
		return nil, apperr.TooLarge("凭证内容超过上限")
	}
	row, err := s.Store.PutCredential(ctx, agentID, purpose, cli, meta, plain)
	if err != nil {
		return nil, err
	}
	return s.view(row), nil
}

// DeleteCLICredential 删除 CLI 凭证（CLI 仍在可用列表中）。
func (s *Service) DeleteCLICredential(ctx context.Context, agentID uuid.UUID, cli string) error {
	if err := validateCLIName(cli); err != nil {
		return err
	}
	return s.Store.DeleteCredential(ctx, agentID, store.PurposeCLI, cli)
}

// DeleteModelCredential 删除模型账号凭证。
func (s *Service) DeleteModelCredential(ctx context.Context, agentID uuid.UUID) error {
	return s.Store.DeleteCredential(ctx, agentID, store.PurposeModel, "")
}
