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

// TemplateDetail is the detail of a template.
type TemplateDetail struct {
	store.SkillTemplate
	SkillMD string
}

// AgentSkillDetail is the detail of a copy.
type AgentSkillDetail struct {
	store.AgentSkill
	SkillMD string
}

func (s *Service) extractZip(data []byte) ([]content.File, content.SkillMeta, error) {
	if int64(len(data)) > s.Limits.MaxSkillArchive {
		return nil, content.SkillMeta{}, apperr.TooLarge("The archive exceeds the size limit.")
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

// checkSkillName rejects the name of the built-in Skill: neither the template library nor an Agent's Skill copies may
// use it.
func checkSkillName(name string) error {
	if name == guide.SkillName {
		return apperr.Conflict(apperr.CodeNameConflict, guide.SkillName+" is the name of the built-in Skill and cannot be used.")
	}
	return nil
}

func (s *Service) parseMarkdown(md string, field string) (content.SkillMeta, error) {
	if int64(len(md)) > s.Limits.MaxUploadFile {
		return content.SkillMeta{}, apperr.TooLarge("SKILL.md exceeds the size limit.")
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

// ListSkillTemplates lists the template library.
func (s *Service) ListSkillTemplates(ctx context.Context) ([]store.SkillTemplate, error) {
	return s.Store.ListSkillTemplates(ctx)
}

// GetSkillTemplate returns the detail of a template.
func (s *Service) GetSkillTemplate(ctx context.Context, id uuid.UUID) (TemplateDetail, error) {
	t, err := s.Store.GetSkillTemplate(ctx, id)
	if err != nil {
		return TemplateDetail{}, err
	}
	md, err := s.skillMD(ctx, t.Files)
	return TemplateDetail{SkillTemplate: t, SkillMD: md}, err
}

// CreateSkillFromZip creates a template from a zip archive.
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

// CreateSkillFromMarkdown creates a template from a single SKILL.md.
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

// ReplaceSkillFromZip replaces a template entirely with a zip archive.
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

// UpdateSkillTemplateMarkdown edits a template's SKILL.md online.
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

// DeleteSkillTemplate deletes a template.
func (s *Service) DeleteSkillTemplate(ctx context.Context, id uuid.UUID) error {
	return s.Store.DeleteSkillTemplate(ctx, id)
}

// ListAgentSkills lists the Agent's copies.
func (s *Service) ListAgentSkills(ctx context.Context, agentID uuid.UUID) ([]store.AgentSkill, error) {
	return s.Store.ListAgentSkills(ctx, agentID)
}

// GetAgentSkill returns the detail of a copy.
func (s *Service) GetAgentSkill(ctx context.Context, agentID, skillID uuid.UUID) (AgentSkillDetail, error) {
	k, err := s.Store.GetAgentSkill(ctx, agentID, skillID)
	if err != nil {
		return AgentSkillDetail{}, err
	}
	md, err := s.skillMD(ctx, k.Files)
	return AgentSkillDetail{AgentSkill: k, SkillMD: md}, err
}

// AddAgentSkill copies a template to an Agent.
func (s *Service) AddAgentSkill(ctx context.Context, agentID, templateID uuid.UUID) (AgentSkillDetail, error) {
	k, err := s.Store.AddAgentSkill(ctx, agentID, templateID)
	if err != nil {
		return AgentSkillDetail{}, err
	}
	return s.GetAgentSkill(ctx, agentID, k.ID)
}

// UpdateAgentSkillMarkdown edits a copy's SKILL.md online.
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

// RecopyAgentSkill overwrites a copy entirely with the current content of its source template.
func (s *Service) RecopyAgentSkill(ctx context.Context, agentID, skillID uuid.UUID) (AgentSkillDetail, error) {
	if _, err := s.Store.RecopyAgentSkill(ctx, agentID, skillID); err != nil {
		return AgentSkillDetail{}, err
	}
	return s.GetAgentSkill(ctx, agentID, skillID)
}

// RemoveAgentSkill removes a copy from an Agent.
func (s *Service) RemoveAgentSkill(ctx context.Context, agentID, skillID uuid.UUID) error {
	return s.Store.RemoveAgentSkill(ctx, agentID, skillID)
}
