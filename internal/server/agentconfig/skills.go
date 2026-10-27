package agentconfig

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"loop-now/internal/loopctl/guide"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/content"
	"loop-now/internal/server/store"
)

// TemplateDetail 是模板详情。
type TemplateDetail struct {
	store.SkillTemplate
	SkillMD string
}

// AgentSkillDetail 是副本详情。
type AgentSkillDetail struct {
	store.AgentSkill
	SkillMD string
}

func (s *Service) extractZip(data []byte) ([]content.File, content.SkillMeta, error) {
	if int64(len(data)) > s.Limits.MaxSkillArchive {
		return nil, content.SkillMeta{}, apperr.TooLarge("压缩包超过上限")
	}
	files, meta, err := content.ExtractSkillZip(data, content.SkillArchiveLimits{MaxTotal: s.Limits.MaxSkillArchive, MaxEntries: s.Limits.MaxSkillEntries})
	if err != nil {
		if errors.Is(err, content.ErrTooLarge) {
			return nil, meta, apperr.TooLarge(err.Error())
		}
		return nil, meta, apperr.Invalid("archive", err.Error())
	}
	if err := checkSkillName(meta.Name); err != nil {
		return nil, meta, err
	}
	return files, meta, nil
}

// checkSkillName 拒绝内置 Skill 的名称：模板库与 Agent 的 Skill 副本都不能使用。
func checkSkillName(name string) error {
	if name == guide.SkillName {
		return apperr.Conflict(apperr.CodeNameConflict, guide.SkillName+" 是内置 Skill 的名称，不能使用")
	}
	return nil
}

func (s *Service) parseMarkdown(md string, field string) (content.SkillMeta, error) {
	if int64(len(md)) > s.Limits.MaxUploadFile {
		return content.SkillMeta{}, apperr.TooLarge("SKILL.md 超过上限")
	}
	meta, err := content.ParseSkillMarkdown([]byte(md))
	if err != nil {
		return meta, apperr.Invalid(field, err.Error())
	}
	return meta, checkSkillName(meta.Name)
}

func (s *Service) skillMD(ctx context.Context, files []store.SkillFile) (string, error) {
	for _, f := range files {
		if f.Path == content.SkillFileName {
			b, err := s.Store.ReadBlob(ctx, f.SHA256)
			return string(b), err
		}
	}
	return "", nil
}

// ListSkillTemplates 列出模板库。
func (s *Service) ListSkillTemplates(ctx context.Context) ([]store.SkillTemplate, error) {
	return s.Store.ListSkillTemplates(ctx)
}

// GetSkillTemplate 返回模板详情。
func (s *Service) GetSkillTemplate(ctx context.Context, id uuid.UUID) (TemplateDetail, error) {
	t, err := s.Store.GetSkillTemplate(ctx, id)
	if err != nil {
		return TemplateDetail{}, err
	}
	md, err := s.skillMD(ctx, t.Files)
	return TemplateDetail{SkillTemplate: t, SkillMD: md}, err
}

// CreateSkillFromZip 用 zip 压缩包创建模板。
func (s *Service) CreateSkillFromZip(ctx context.Context, data []byte) (TemplateDetail, error) {
	files, meta, err := s.extractZip(data)
	if err != nil {
		return TemplateDetail{}, err
	}
	t, err := s.Store.CreateSkillTemplate(ctx, meta, files)
	if err != nil {
		return TemplateDetail{}, err
	}
	return s.GetSkillTemplate(ctx, t.ID)
}

// CreateSkillFromMarkdown 只用一份 SKILL.md 新建模板。
func (s *Service) CreateSkillFromMarkdown(ctx context.Context, md string) (TemplateDetail, error) {
	meta, err := s.parseMarkdown(md, "skillMd")
	if err != nil {
		return TemplateDetail{}, err
	}
	t, err := s.Store.CreateSkillTemplate(ctx, meta, []content.File{{Path: content.SkillFileName, Data: []byte(md)}})
	if err != nil {
		return TemplateDetail{}, err
	}
	return s.GetSkillTemplate(ctx, t.ID)
}

// ReplaceSkillFromZip 用 zip 压缩包整体替换模板。
func (s *Service) ReplaceSkillFromZip(ctx context.Context, id uuid.UUID, data []byte) (TemplateDetail, error) {
	files, meta, err := s.extractZip(data)
	if err != nil {
		return TemplateDetail{}, err
	}
	if _, err := s.Store.ReplaceSkillTemplate(ctx, id, meta, files); err != nil {
		return TemplateDetail{}, err
	}
	return s.GetSkillTemplate(ctx, id)
}

// UpdateSkillTemplateMarkdown 在线编辑模板的 SKILL.md。
func (s *Service) UpdateSkillTemplateMarkdown(ctx context.Context, id uuid.UUID, md string) (TemplateDetail, error) {
	meta, err := s.parseMarkdown(md, "content")
	if err != nil {
		return TemplateDetail{}, err
	}
	if _, err := s.Store.UpdateSkillTemplateMarkdown(ctx, id, meta, []byte(md)); err != nil {
		return TemplateDetail{}, err
	}
	return s.GetSkillTemplate(ctx, id)
}

// DeleteSkillTemplate 删除模板。
func (s *Service) DeleteSkillTemplate(ctx context.Context, id uuid.UUID) error {
	return s.Store.DeleteSkillTemplate(ctx, id)
}

// ListAgentSkills 列出 Agent 的副本。
func (s *Service) ListAgentSkills(ctx context.Context, agentID uuid.UUID) ([]store.AgentSkill, error) {
	return s.Store.ListAgentSkills(ctx, agentID)
}

// GetAgentSkill 返回副本详情。
func (s *Service) GetAgentSkill(ctx context.Context, agentID, skillID uuid.UUID) (AgentSkillDetail, error) {
	k, err := s.Store.GetAgentSkill(ctx, agentID, skillID)
	if err != nil {
		return AgentSkillDetail{}, err
	}
	md, err := s.skillMD(ctx, k.Files)
	return AgentSkillDetail{AgentSkill: k, SkillMD: md}, err
}

// AddAgentSkill 把模板复制给 Agent。
func (s *Service) AddAgentSkill(ctx context.Context, agentID, templateID uuid.UUID) (AgentSkillDetail, error) {
	k, err := s.Store.AddAgentSkill(ctx, agentID, templateID)
	if err != nil {
		return AgentSkillDetail{}, err
	}
	return s.GetAgentSkill(ctx, agentID, k.ID)
}

// UpdateAgentSkillMarkdown 在线编辑副本的 SKILL.md。
func (s *Service) UpdateAgentSkillMarkdown(ctx context.Context, agentID, skillID uuid.UUID, md string) (AgentSkillDetail, error) {
	meta, err := s.parseMarkdown(md, "content")
	if err != nil {
		return AgentSkillDetail{}, err
	}
	if _, err := s.Store.UpdateAgentSkillMarkdown(ctx, agentID, skillID, meta, []byte(md)); err != nil {
		return AgentSkillDetail{}, err
	}
	return s.GetAgentSkill(ctx, agentID, skillID)
}

// RecopyAgentSkill 用来源模板的当前内容整体覆盖副本。
func (s *Service) RecopyAgentSkill(ctx context.Context, agentID, skillID uuid.UUID) (AgentSkillDetail, error) {
	if _, err := s.Store.RecopyAgentSkill(ctx, agentID, skillID); err != nil {
		return AgentSkillDetail{}, err
	}
	return s.GetAgentSkill(ctx, agentID, skillID)
}

// RemoveAgentSkill 从 Agent 上移除副本。
func (s *Service) RemoveAgentSkill(ctx context.Context, agentID, skillID uuid.UUID) error {
	return s.Store.RemoveAgentSkill(ctx, agentID, skillID)
}
