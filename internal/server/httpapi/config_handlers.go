package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/modelauth"
)

// ---- 上下文、健康、目录 ----

func (s *Server) getContext(w http.ResponseWriter, r *http.Request) {
	var c apitypes.Context
	c.Organization.ID, c.Organization.Name = s.O.Defaults.Organization.ID, s.O.Defaults.Organization.Name
	c.Project.ID, c.Project.OrganizationID, c.Project.Name = s.O.Defaults.Project.ID, s.O.Defaults.Project.OrganizationID, s.O.Defaults.Project.Name
	c.Limits = s.O.Limits
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) getHealth(w http.ResponseWriter, r *http.Request) {
	check := func(fn func(context.Context) error) apitypes.DependencyHealth {
		if err := fn(r.Context()); err != nil {
			msg := err.Error()
			return apitypes.DependencyHealth{OK: false, Error: &msg}
		}
		return apitypes.DependencyHealth{OK: true}
	}
	h := apitypes.Health{Postgres: check(s.Store.Ping), Redis: check(s.Coord.Ping)}
	h.OK = h.Postgres.OK && h.Redis.OK
	status := http.StatusOK
	if !h.OK {
		status = http.StatusServiceUnavailable
	}
	if p, _ := auth.From(r.Context()); p.Kind == auth.KindExternal {
		writeJSON(w, status, h.Redacted()) // 对外只表明各依赖是否可用，不含 error 字段
		return
	}
	writeJSON(w, status, h)
}

func (s *Server) listModelProviders(w http.ResponseWriter, r *http.Request) {
	out := apitypes.ModelProviderList{PiVersion: s.Models.PiVersion, CatalogGeneratedAt: s.Models.GeneratedAt, Providers: []apitypes.ModelProvider{}}
	for _, p := range s.Models.Providers {
		out.Providers = append(out.Providers, apitypes.ModelProvider{ID: p.ID, APIKeyEnvVars: p.APIKeyEnvVars, AuthMethods: p.AuthMethods, ModelCount: len(p.Models)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	p, ok := s.Models.Provider(r.PathValue("providerId"))
	if !ok {
		writeError(w, apperr.NotFound("提供方"))
		return
	}
	out := apitypes.ModelList{ProviderID: p.ID, Models: []apitypes.Model{}}
	for _, m := range p.Models {
		out.Models = append(out.Models, apitypes.Model{ID: m.ID, Name: m.Name, Reasoning: m.Reasoning, ContextWindow: m.ContextWindow, MaxTokens: m.MaxTokens, Input: m.Input, ThinkingLevels: m.ThinkingLevels, DefaultThinkingLevel: m.DefaultThinkingLevel})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listCliTemplates(w http.ResponseWriter, r *http.Request) {
	out := []apitypes.CliTemplate{}
	for _, t := range s.Templates.All() {
		status, msg := s.Installer.Status(t)
		var errMsg *string
		if msg != "" {
			errMsg = &msg
		}
		out = append(out, apitypes.CliTemplate{Name: t.Name, Version: t.Version, Fields: t.Fields, Injection: t.Injection,
			Install: apitypes.CliInstall{Status: status, Error: errMsg}, Warnings: t.PlatformWarnings()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": out})
}

// ---- Agent ----

func (s *Server) writeAgent(w http.ResponseWriter, r *http.Request, status int, id uuid.UUID) {
	d, err := s.Config.GetAgentDetail(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, status, apitypes.FromAgentDetail(d))
}

type modelInput struct {
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

func (s *Server) getAgent(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeAgent(w, r, http.StatusOK, id)
}

func (s *Server) updateAgent(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Name          *string         `json:"name"`
		ThinkingLevel json.RawMessage `json:"thinkingLevel"`
		ServiceTier   json.RawMessage `json:"serviceTier"`
		Model         *modelInput     `json:"model"`
		Prompt        json.RawMessage `json:"prompt"`
		Expected      json.RawMessage `json:"expectedPromptVersion"`
	}
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	text, err := promptText(in.Prompt)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	version, err := promptVersion(in.Expected, text != nil)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := agentconfig.AgentPatch{Name: in.Name, Prompt: text, ExpectedPromptVersion: version}
	if len(in.ThinkingLevel) > 0 {
		p.ThinkingLevelSet = true
		if err := json.Unmarshal(in.ThinkingLevel, &p.ThinkingLevel); err != nil {
			s.fail(w, r, apperr.Invalid("thinkingLevel", "须为合法思考等级或null"))
			return
		}
	}
	if len(in.ServiceTier) > 0 {
		p.ServiceTierSet = true
		if err := json.Unmarshal(in.ServiceTier, &p.ServiceTier); err != nil {
			s.fail(w, r, apperr.Invalid("serviceTier", "须为速度档位或null"))
			return
		}
	}
	if in.Model != nil {
		p.Provider, p.ModelID = &in.Model.Provider, &in.Model.ModelID
	}
	if _, err := s.Config.UpdateAgent(r.Context(), id, p); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeAgent(w, r, http.StatusOK, id)
}

func (s *Server) deleteAgent(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err == nil {
		err = s.Config.DeleteAgent(r.Context(), id)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- 可用 CLI 与凭证 ----

func (s *Server) addAgentCli(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.Config.AddCLI(r.Context(), id, r.PathValue("cli"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromCLI(v))
}

func (s *Server) removeAgentCli(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err == nil {
		err = s.Config.RemoveCLI(r.Context(), id, r.PathValue("cli"))
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) putCliCredential(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Fields map[string]string `json:"fields"`
	}
	if err := readJSON(w, r, s.O.Limits.MaxCredential*2, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.Config.PutCLICredential(r.Context(), id, r.PathValue("cli"), in.Fields)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromCredential(v))
}

func (s *Server) deleteCliCredential(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err == nil {
		err = s.Config.DeleteCLICredential(r.Context(), id, r.PathValue("cli"))
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) putModelCredential(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in modelauth.KeyInput
	if err := readJSON(w, r, s.O.Limits.MaxCredential*2, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	writer, ok := s.ModelAuth.(modelauth.KeyController)
	if !ok {
		writeError(w, &apperr.Error{Status: 503, Code: apperr.CodeInternal, Message: "模型账号认证组件尚未装配"})
		return
	}
	v, err := writer.PutKey(r.Context(), id, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) deleteModelCredential(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err == nil {
		writer, ok := s.ModelAuth.(modelauth.KeyController)
		if !ok {
			writeError(w, &apperr.Error{Status: 503, Code: apperr.CodeInternal, Message: "模型账号认证组件尚未装配"})
			return
		}
		err = writer.Clear(r.Context(), id)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Skills 模板库 ----

func (s *Server) listSkillTemplates(w http.ResponseWriter, r *http.Request) {
	ts, err := s.Config.ListSkillTemplates(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]apitypes.SkillTemplateSummary, 0, len(ts))
	for _, t := range ts {
		out = append(out, apitypes.FromTemplateSummary(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": out})
}

// readArchive 读取 multipart 中名为 archive 的 zip 文件。
func (s *Server) readArchive(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, s.O.Limits.MaxSkillArchive+(1<<20))
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, apperr.TooLarge("压缩包超过上限")
		}
		return nil, apperr.Invalid("archive", "需要 multipart/form-data 中名为 archive 的 zip 文件")
	}
	f, _, err := r.FormFile("archive")
	if err != nil {
		return nil, apperr.Invalid("archive", "缺少名为 archive 的文件")
	}
	defer f.Close()
	return io.ReadAll(f)
}

func (s *Server) createSkillTemplate(w http.ResponseWriter, r *http.Request) {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	var t agentconfig.TemplateDetail
	var err error
	if strings.HasPrefix(mt, "multipart/") {
		var data []byte
		if data, err = s.readArchive(w, r); err == nil {
			t, err = s.Config.CreateSkillFromZip(r.Context(), data)
		}
	} else {
		var in struct {
			SkillMD string `json:"skillMd"`
		}
		if err = readJSON(w, r, s.O.Limits.MaxUploadFile+1024, &in); err == nil {
			t, err = s.Config.CreateSkillFromMarkdown(r.Context(), in.SkillMD)
		}
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, apitypes.FromTemplate(t))
}

func (s *Server) getSkillTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "skillId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	t, err := s.Config.GetSkillTemplate(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromTemplate(t))
}

func (s *Server) replaceSkillTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "skillId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data, err := s.readArchive(w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	t, err := s.Config.ReplaceSkillFromZip(r.Context(), id, data)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromTemplate(t))
}

func (s *Server) deleteSkillTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "skillId")
	if err == nil {
		err = s.Config.DeleteSkillTemplate(r.Context(), id)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) updateSkillTemplateMarkdown(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "skillId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Content string `json:"content"`
	}
	if err := readJSON(w, r, s.O.Limits.MaxUploadFile+1024, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	t, err := s.Config.UpdateSkillTemplateMarkdown(r.Context(), id, in.Content)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromTemplate(t))
}

// ---- Agent 的 Skill 副本 ----

func agentAndSkill(r *http.Request) (uuid.UUID, uuid.UUID, error) {
	a, err := pathUUID(r, "agentId")
	if err != nil {
		return a, uuid.Nil, err
	}
	k, err := pathUUID(r, "agentSkillId")
	return a, k, err
}

func (s *Server) listAgentSkills(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ks, err := s.Config.ListAgentSkills(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]apitypes.AgentSkillSummary, 0, len(ks))
	for _, k := range ks {
		out = append(out, apitypes.FromAgentSkillSummary(k))
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": out})
}

func (s *Server) addAgentSkill(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		TemplateID uuid.UUID `json:"templateId"`
	}
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	k, err := s.Config.AddAgentSkill(r.Context(), id, in.TemplateID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, apitypes.FromAgentSkill(k))
}

func (s *Server) getAgentSkill(w http.ResponseWriter, r *http.Request) {
	a, k, err := agentAndSkill(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.Config.GetAgentSkill(r.Context(), a, k)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromAgentSkill(out))
}

func (s *Server) removeAgentSkill(w http.ResponseWriter, r *http.Request) {
	a, k, err := agentAndSkill(r)
	if err == nil {
		err = s.Config.RemoveAgentSkill(r.Context(), a, k)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) updateAgentSkillMarkdown(w http.ResponseWriter, r *http.Request) {
	a, k, err := agentAndSkill(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Content string `json:"content"`
	}
	if err := readJSON(w, r, s.O.Limits.MaxUploadFile+1024, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.Config.UpdateAgentSkillMarkdown(r.Context(), a, k, in.Content)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromAgentSkill(out))
}

func (s *Server) recopyAgentSkill(w http.ResponseWriter, r *http.Request) {
	a, k, err := agentAndSkill(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.Config.RecopyAgentSkill(r.Context(), a, k)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromAgentSkill(out))
}
