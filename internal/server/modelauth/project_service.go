package modelauth

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/clitemplate"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/larkauth"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/store"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ProjectService owns project credentials, distinct from private Agent OAuth.
type ProjectService struct {
	Auth            *Service
	Templates       *clitemplate.Catalog
	MaxCredential   int64
	CancelCLILogins func(context.Context, uuid.UUID, *uuid.UUID) error
}

func NewProjectService(auth *Service, templates *clitemplate.Catalog, max int64) *ProjectService {
	if max <= 0 {
		max = modelcredential.MaxBytes
	}
	p := &ProjectService{Auth: auth, Templates: templates, MaxCredential: max}
	auth.Project = p
	return p
}
func validateAccountName(name string) error {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 200 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return apperr.Invalid("name", "名称须为1–200字符，首尾无空白或控制字符")
	}
	return nil
}
func (p *ProjectService) ValidateSlot(purpose string, provider, cli *string) (string, error) {
	if purpose == store.PurposeModel && provider != nil && cli == nil && *provider != "" {
		if _, ok := p.Auth.Models.Provider(*provider); !ok {
			return "", apperr.Invalid("provider", "未知模型提供方")
		}
		return *provider, nil
	}
	if purpose == store.PurposeCLI && cli != nil && provider == nil && *cli != "" {
		if _, ok := p.Templates.Get(*cli); !ok {
			return "", apperr.Invalid("cli", "没有可录入凭证的CLI模板")
		}
		return *cli, nil
	}
	return "", apperr.Invalid("purpose", "用途与provider/cli定位不匹配")
}
func (p *ProjectService) prepare(ctx context.Context, purpose, slot string, input cm.CredentialInput) (store.ProjectCredentialWrite, func(), error) {
	var plain []byte
	w := store.ProjectCredentialWrite{}
	release := func() { clear(plain) }
	if purpose == store.PurposeModel {
		if input.Type != "api_key" {
			return w, release, apperr.Invalid("credential.type", "模型录入只接受API Key")
		}
		provider, ok := p.Auth.Models.Provider(slot)
		if !ok || !slices.Contains(provider.AuthMethods, "api_key") {
			return w, release, apperr.Invalid("provider", "该提供方不支持API Key录入")
		}
		credential, err := modelcredential.APIKey(input.Key)
		if err != nil {
			return w, release, apperr.Invalid("credential.key", "模型key格式无效")
		}
		plain, err = credential.Encode()
		if err != nil {
			return w, release, apperr.Invalid("credential.key", "模型key格式无效")
		}
		w.AuthMethod = "api_key"
	} else {
		if input.Type != "cli" {
			return w, release, apperr.Invalid("credential.type", "CLI须提供模板字段")
		}
		template, ok := p.Templates.Get(slot)
		if !ok {
			return w, release, apperr.Invalid("cli", "没有CLI模板")
		}
		for key := range input.Fields {
			if !slices.ContainsFunc(template.Fields, func(f clitemplate.Field) bool { return f.Name == key }) {
				return w, release, apperr.Invalid("credential.fields", "包含模板未声明的录入项")
			}
		}
		payload, names, err := template.Render(input.Fields)
		if err != nil {
			return w, release, err
		}
		plain, err = json.Marshal(payload)
		if err != nil {
			return w, release, apperr.Invalid("credential.fields", "模板凭证格式无效")
		}
		w.Meta = store.CredentialMeta{TemplateVersion: template.CredentialFormat(), FieldNames: names, EnvNames: payload.EnvNames(), FilePaths: payload.FilePaths()}
		w.TemplateSource = "builtin"
		if slot == "gcloud" {
			w.AuthMethod = "service_account"
		}
	}
	if int64(len(plain)) > p.MaxCredential {
		release()
		return w, func() {}, apperr.TooLarge("凭证内容超过上限")
	}
	w.Secret = plain
	return w, release, nil
}
func (p *ProjectService) accountView(ctx context.Context, r store.ProjectCredentialRecord) cm.Account {
	v := r.View
	if v.DeletedAt != nil {
		return v
	}
	var plain []byte
	var err error
	if r.Model != nil {
		plain = r.Model.Secret
		var c modelcredential.Credential
		c, err = modelcredential.Decode(plain)
		if err == nil && c.Kind() != r.Model.AuthMethod {
			err = ErrCredentialUnavailable
		}
	} else if r.CLI != nil {
		plain = r.CLI.Secret
		template, ok := p.Templates.Get(r.CLI.CLIName)
		if !ok || r.TemplateSource != "builtin" {
			err = ErrCredentialUnavailable
			if v.Template != nil {
				v.Template.Status = "template_removed"
			}
		} else {
			v.Template.Warnings = template.PlatformWarnings()
			if r.CLI.TemplateVersion != template.CredentialFormat() {
				err = ErrCredentialUnavailable
				v.Template.Status = "incompatible"
			}
		}
		if err == nil {
			if v.CLI != nil && *v.CLI == "gcloud" && v.AuthMethod != nil && *v.AuthMethod == "oauth" {
				c, e := clicredential.DecodeGoogle(plain)
				err = e
				if err == nil && (v.AccountLabel == nil || *v.AccountLabel != c.Account) {
					err = ErrCredentialUnavailable
				}
			} else if v.CLI != nil && *v.CLI == "lark-cli" && v.AuthMethod != nil && *v.AuthMethod == "oauth" {
				c, e := clicredential.DecodeLark(plain)
				err = e
				if err == nil && (v.LarkIdentity == nil || c.Identity.Brand != v.LarkIdentity.Brand || c.Identity.AppID != v.LarkIdentity.AppID || c.Identity.OpenID != v.LarkIdentity.OpenID) {
					err = ErrCredentialUnavailable
				}
			} else {
				var payload clitemplate.Payload
				if json.Unmarshal(plain, &payload) != nil {
					err = ErrCredentialUnavailable
				}
				if v.CLI != nil && *v.CLI == "gh" && v.AuthMethod != nil && *v.AuthMethod == "oauth" && payload.Env["GH_TOKEN"] == "" {
					err = ErrCredentialUnavailable
				}
				if v.CLI != nil && *v.CLI == "lark-cli" && err == nil {
					app, e := larkauth.DecodeApplication(plain)
					err = e
					if e == nil {
						v.LarkApplication = &cm.LarkApplication{Brand: app.Brand, AppID: app.AppID}
					}
				}
			}
		}
	} else {
		err = ErrCredentialUnavailable
	}
	if err != nil {
		v.Status = "unavailable"
	}
	return v
}
func (p *ProjectService) viewCaptured(ctx context.Context, v cm.Account) cm.Account {
	// Load the captured version, not today's account pointer. This preserves the
	// configuration read baseline even when a replacement commits afterwards.
	if v.DeletedAt != nil || v.CurrentVersionID == nil {
		return v
	}
	r := store.ProjectCredentialRecord{View: v}
	if v.Purpose == store.PurposeModel {
		model, e := p.Auth.Store.GetModelAuthVersion(ctx, *v.CurrentVersionID)
		if e != nil || model.ProjectAccountID == nil || *model.ProjectAccountID != v.ID {
			v.Status = "unavailable"
			return v
		}
		r.Model = &model
	} else {
		row, source, e := p.Auth.Store.GetProjectCLIVersion(ctx, v.ID, *v.CurrentVersionID)
		if e != nil {
			v.Status = "unavailable"
			return v
		}
		r.CLI = &row
		r.TemplateSource = source
	}
	return p.accountView(ctx, r)
}
func (p *ProjectService) changed(ctx context.Context, project uuid.UUID) {
	if p.Auth.Changed == nil {
		return
	}
	agents, err := p.Auth.Store.ListAgents(ctx, store.ProjectScope(project))
	if err != nil {
		return
	}
	for _, a := range agents {
		p.Auth.Changed(a.ID)
	}
}
func (p *ProjectService) List(ctx context.Context, project uuid.UUID, purpose, slot string) (cm.AccountList, error) {
	rows, rev, e := p.Auth.Store.ListProjectCredentials(ctx, project, purpose, slot)
	out := cm.AccountList{ProjectID: project, CredentialsRevision: rev, Accounts: []cm.Account{}}
	if e != nil {
		return out, e
	}
	for _, r := range rows {
		out.Accounts = append(out.Accounts, p.accountView(ctx, r))
	}
	return out, nil
}
func (p *ProjectService) Get(ctx context.Context, project, id uuid.UUID) (cm.Account, error) {
	r, e := p.Auth.Store.GetProjectCredential(ctx, project, id)
	if e != nil {
		return cm.Account{}, e
	}
	return p.accountView(ctx, r), nil
}
func (p *ProjectService) Create(ctx context.Context, project uuid.UUID, in cm.Create) (cm.Account, error) {
	if e := validateAccountName(in.Name); e != nil {
		return cm.Account{}, e
	}
	slot, e := p.ValidateSlot(in.Purpose, in.Provider, in.CLI)
	if e != nil {
		return cm.Account{}, e
	}
	w, release, e := p.prepare(ctx, in.Purpose, slot, in.Credential)
	defer release()
	if e != nil {
		return cm.Account{}, e
	}
	r, e := p.Auth.Store.CreateProjectCredential(ctx, project, in.Purpose, slot, in.Name, w)
	if e != nil {
		return cm.Account{}, e
	}
	p.changed(ctx, project)
	return p.accountView(ctx, r), nil
}
func (p *ProjectService) Rename(ctx context.Context, project, id uuid.UUID, in cm.Rename) (cm.Account, error) {
	if e := validateAccountName(in.Name); e != nil {
		return cm.Account{}, e
	}
	r, e := p.Auth.Store.MutateProjectCredential(ctx, project, id, in.ExpectedRevision, &in.Name, nil, false)
	if e != nil {
		return cm.Account{}, e
	}
	p.changed(ctx, project)
	return p.accountView(ctx, r), nil
}
func (p *ProjectService) Replace(ctx context.Context, project, id uuid.UUID, in cm.Replace) (cm.Account, error) {
	r, e := p.Auth.Store.GetProjectCredential(ctx, project, id)
	if e != nil {
		return cm.Account{}, e
	}
	slot, e := p.ValidateSlot(r.View.Purpose, r.View.Provider, r.View.CLI)
	if e != nil {
		return cm.Account{}, e
	}
	w, release, e := p.prepare(ctx, r.View.Purpose, slot, in.Credential)
	defer release()
	if e != nil {
		return cm.Account{}, e
	}
	r, e = p.Auth.Store.MutateProjectCredential(ctx, project, id, in.ExpectedRevision, nil, &w, false)
	if e != nil {
		return cm.Account{}, e
	}
	p.changed(ctx, project)
	return p.accountView(ctx, r), nil
}
func (p *ProjectService) Delete(ctx context.Context, project, id uuid.UUID, expected int64) (cm.Account, error) {
	r, e := p.Auth.Store.MutateProjectCredential(ctx, project, id, expected, nil, nil, true)
	if e != nil {
		return cm.Account{}, e
	}
	if e = p.Auth.CancelProjectLogins(ctx, project, &id); e != nil && p.Auth.Log != nil {
		p.Auth.Log.Warn("项目账号登录资源清理超时", "projectId", project, "accountId", id, "code", "auth_cleanup_timeout")
	}
	if p.CancelCLILogins != nil {
		if e = p.CancelCLILogins(ctx, project, &id); e != nil && p.Auth.Log != nil {
			p.Auth.Log.Warn("CLI登录资源清理超时", "stage", "deleted_account_login_cleanup", "accountId", id.String())
		}
	}
	p.changed(ctx, project)
	return r.View, nil
}
func (p *ProjectService) Selections(ctx context.Context, agent uuid.UUID) (cm.Selections, error) {
	out, e := p.Auth.Store.GetCredentialSelections(ctx, agent)
	if e != nil {
		return out, e
	}
	filtered := []cm.Selection{}
	for _, v := range out.Selections {
		if v.Purpose == store.PurposeCLI {
			if _, ok := p.Templates.Get(*v.CLI); !ok {
				continue
			}
		}
		for i := range v.Candidates {
			v.Candidates[i] = p.viewCaptured(ctx, v.Candidates[i])
			if v.ResolvedAccountID != nil && v.Candidates[i].ID == *v.ResolvedAccountID && (v.Candidates[i].Status == "unavailable" || v.Candidates[i].Status == "requires_login") {
				v.Status = v.Candidates[i].Status
			}
		}
		filtered = append(filtered, v)
	}
	out.Selections = filtered
	return out, nil
}
func (p *ProjectService) Select(ctx context.Context, agent uuid.UUID, in cm.SelectionPut) (cm.Selections, error) {
	if _, e := p.ValidateSlot(in.Purpose, in.Provider, in.CLI); e != nil {
		return cm.Selections{}, e
	}
	out, e := p.Auth.Store.PutCredentialSelection(ctx, agent, in)
	if e != nil {
		return out, e
	}
	if p.Auth.Changed != nil {
		p.Auth.Changed(agent)
	}
	return p.Selections(ctx, agent)
}
func (p *ProjectService) Impact(ctx context.Context, project uuid.UUID, purpose, slot string, account *uuid.UUID) (cm.Impact, error) {
	return p.Auth.Store.ProjectCredentialImpact(ctx, project, purpose, slot, account)
}
func (p *ProjectService) CurrentModelMetadata(ctx context.Context, agent uuid.UUID) (*Metadata, error) {
	selections, e := p.Selections(ctx, agent)
	if e != nil {
		return nil, e
	}
	a, e := p.Auth.Store.GetAgent(ctx, agent)
	if e != nil {
		return nil, e
	}
	for _, v := range selections.Selections {
		if v.Purpose != store.PurposeModel || v.Provider == nil || *v.Provider != a.ModelProvider {
			continue
		}
		if v.ResolvedAccountID == nil {
			return nil, nil
		}
		for _, account := range v.Candidates {
			if account.ID != *v.ResolvedAccountID {
				continue
			}
			status := "set"
			if account.Status == "unavailable" || account.Status == "requires_login" {
				status = "unavailable"
			}
			fields := []string{}
			if account.AuthMethod != nil && *account.AuthMethod == "api_key" {
				fields = []string{"key"}
			}
			return &Metadata{ProjectID: &selections.ProjectID, AccountID: &account.ID, Provider: account.Provider, AuthMethod: account.AuthMethod, VersionID: account.CurrentVersionID, AccountLabel: account.AccountLabel, Status: status, UpdatedAt: account.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z07:00"), FieldNames: fields, EnvNames: []string{}, FilePaths: []string{}, Warnings: []clitemplate.Warning{}}, nil
		}
	}
	return nil, nil
}

// CurrentCLIMetadata adapts the shared account into the old safe Agent read
// shape. Resolve once, then read that captured version, never a newer pointer.
func (p *ProjectService) CurrentCLIMetadata(ctx context.Context, agent uuid.UUID, cli string) (*Metadata, error) {
	selections, err := p.Selections(ctx, agent)
	if err != nil {
		return nil, err
	}
	for _, choice := range selections.Selections {
		if choice.Purpose != store.PurposeCLI || choice.CLI == nil || *choice.CLI != cli || choice.ResolvedAccountID == nil || choice.ResolvedVersionID == nil {
			continue
		}
		row, source, err := p.Auth.Store.GetProjectCLIVersion(ctx, *choice.ResolvedAccountID, *choice.ResolvedVersionID)
		if err != nil {
			return nil, err
		}
		status := "set"
		warnings := []clitemplate.Warning{}
		template, ok := p.Templates.Get(cli)
		if !ok || source != "builtin" {
			status = "unavailable"
		} else {
			warnings = template.PlatformWarnings()
			if row.TemplateVersion != template.CredentialFormat() {
				status = "needs_reentry"
			}
		}
		return &Metadata{ProjectID: &selections.ProjectID, AccountID: choice.ResolvedAccountID, VersionID: choice.ResolvedVersionID, Status: status, UpdatedAt: row.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z07:00"), FieldNames: nonNilMetadataStrings(row.FieldNames), EnvNames: nonNilMetadataStrings(row.EnvNames), FilePaths: nonNilMetadataStrings(row.FilePaths), Warnings: warnings}, nil
	}
	return nil, nil
}

func nonNilMetadataStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
