package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/content"
)

// SkillFile 是 Skill 中的一个文件。
type SkillFile struct {
	Path   string
	Size   int64
	SHA256 string
}

// SkillTemplate 是模板库中的 Skill。
type SkillTemplate struct {
	ID          uuid.UUID
	Name        string
	Description string
	FileCount   int
	Files       []SkillFile // 仅详情查询填充
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// AgentSkill 是复制到 Agent 上的 Skill 副本。
type AgentSkill struct {
	ID               uuid.UUID
	AgentID          uuid.UUID
	Name             string
	Description      string
	SourceTemplateID *uuid.UUID
	FileCount        int
	Files            []SkillFile // 仅详情查询填充
	CopiedAt         time.Time
	UpdatedAt        time.Time
}

func nameConflict(name string) error {
	return apperr.Conflict(apperr.CodeNameConflict, "已有名为 "+name+" 的 Skill")
}

func insertFiles(ctx context.Context, tx pgx.Tx, table, idCol string, id uuid.UUID, files []content.File) error {
	for _, f := range files {
		sum, err := putBlob(ctx, tx, f.Data)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO `+table+` (`+idCol+`, path, blob_sha256, size) VALUES ($1, $2, $3, $4)`, id, f.Path, sum, len(f.Data)); err != nil {
			return err
		}
	}
	return nil
}

func listSkillFiles(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, table, idCol string, id uuid.UUID) ([]SkillFile, error) {
	rows, err := q.Query(ctx, `SELECT path, size, blob_sha256 FROM `+table+` WHERE `+idCol+` = $1 ORDER BY path`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (SkillFile, error) {
		var f SkillFile
		err := r.Scan(&f.Path, &f.Size, &f.SHA256)
		return f, err
	})
}

// ---- 模板库 ----

const templateColumns = `t.id, t.name, t.description, (SELECT count(*) FROM skill_template_files f WHERE f.template_id = t.id), t.created_at, t.updated_at`

func scanTemplate(row pgx.Row) (SkillTemplate, error) {
	var t SkillTemplate
	err := row.Scan(&t.ID, &t.Name, &t.Description, &t.FileCount, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

// ListSkillTemplates 列出项目的模板库。
func (s *Store) ListSkillTemplates(ctx context.Context) ([]SkillTemplate, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+templateColumns+` FROM skill_templates t ORDER BY t.name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (SkillTemplate, error) { return scanTemplate(r) })
}

// GetSkillTemplate 返回模板及其文件列表。
func (s *Store) GetSkillTemplate(ctx context.Context, id uuid.UUID) (SkillTemplate, error) {
	t, err := scanTemplate(s.Pool.QueryRow(ctx, `SELECT `+templateColumns+` FROM skill_templates t WHERE t.id = $1`, id))
	if err != nil {
		return t, notFoundIfNoRows(err, "Skill 模板")
	}
	t.Files, err = listSkillFiles(ctx, s.Pool, "skill_template_files", "template_id", id)
	return t, err
}

// CreateSkillTemplate 创建模板；名称在项目内重复时返回 name_conflict。
func (s *Store) CreateSkillTemplate(ctx context.Context, meta content.SkillMeta, files []content.File) (SkillTemplate, error) {
	id := uuid.Must(uuid.NewV7())
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO skill_templates (id, name, description) VALUES ($1, $2, $3)`,
			id, meta.Name, meta.Description); err != nil {
			if isUniqueViolation(err, "") {
				return nameConflict(meta.Name)
			}
			return err
		}
		return insertFiles(ctx, tx, "skill_template_files", "template_id", id, files)
	})
	if err != nil {
		return SkillTemplate{}, err
	}
	return s.GetSkillTemplate(ctx, id)
}

// ReplaceSkillTemplate 用新文件整体替换模板内容。
func (s *Store) ReplaceSkillTemplate(ctx context.Context, id uuid.UUID, meta content.SkillMeta, files []content.File) (SkillTemplate, error) {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := updateTemplateMeta(ctx, tx, id, meta); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM skill_template_files WHERE template_id = $1`, id); err != nil {
			return err
		}
		return insertFiles(ctx, tx, "skill_template_files", "template_id", id, files)
	})
	if err != nil {
		return SkillTemplate{}, err
	}
	return s.GetSkillTemplate(ctx, id)
}

// UpdateSkillTemplateMarkdown 只替换模板的 SKILL.md，保留其他文件。
func (s *Store) UpdateSkillTemplateMarkdown(ctx context.Context, id uuid.UUID, meta content.SkillMeta, md []byte) (SkillTemplate, error) {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := updateTemplateMeta(ctx, tx, id, meta); err != nil {
			return err
		}
		return upsertSkillMD(ctx, tx, "skill_template_files", "template_id", id, md)
	})
	if err != nil {
		return SkillTemplate{}, err
	}
	return s.GetSkillTemplate(ctx, id)
}

func updateTemplateMeta(ctx context.Context, tx pgx.Tx, id uuid.UUID, meta content.SkillMeta) error {
	tag, err := tx.Exec(ctx, `UPDATE skill_templates SET name = $2, description = $3, updated_at = now() WHERE id = $1`, id, meta.Name, meta.Description)
	if err != nil {
		if isUniqueViolation(err, "") {
			return nameConflict(meta.Name)
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("Skill 模板")
	}
	return nil
}

func upsertSkillMD(ctx context.Context, tx pgx.Tx, table, idCol string, id uuid.UUID, md []byte) error {
	sum, err := putBlob(ctx, tx, md)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO `+table+` (`+idCol+`, path, blob_sha256, size) VALUES ($1, $2, $3, $4)
		ON CONFLICT (`+idCol+`, path) DO UPDATE SET blob_sha256 = EXCLUDED.blob_sha256, size = EXCLUDED.size`,
		id, content.SkillFileName, sum, len(md))
	return err
}

// DeleteSkillTemplate 删除模板；已复制给 Agent 的副本保留，来源置空。
func (s *Store) DeleteSkillTemplate(ctx context.Context, id uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM skill_templates WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("Skill 模板")
	}
	return nil
}

// ---- Agent 的副本 ----

const agentSkillColumns = `k.id, k.agent_id, k.name, k.description, k.source_template_id, (SELECT count(*) FROM agent_skill_files f WHERE f.agent_skill_id = k.id), k.copied_at, k.updated_at`

func scanAgentSkill(row pgx.Row) (AgentSkill, error) {
	var k AgentSkill
	err := row.Scan(&k.ID, &k.AgentID, &k.Name, &k.Description, &k.SourceTemplateID, &k.FileCount, &k.CopiedAt, &k.UpdatedAt)
	return k, err
}

// ListAgentSkills 列出 Agent 的 Skill 副本。
func (s *Store) ListAgentSkills(ctx context.Context, agentID uuid.UUID) ([]AgentSkill, error) {
	if _, err := s.GetAgent(ctx, agentID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+agentSkillColumns+` FROM agent_skills k WHERE k.agent_id = $1 ORDER BY k.name`, agentID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AgentSkill, error) { return scanAgentSkill(r) })
}

// GetAgentSkill 返回副本及其文件列表。
func (s *Store) GetAgentSkill(ctx context.Context, agentID, skillID uuid.UUID) (AgentSkill, error) {
	if _, err := s.GetAgent(ctx, agentID); err != nil {
		return AgentSkill{}, err
	}
	k, err := scanAgentSkill(s.Pool.QueryRow(ctx, `SELECT `+agentSkillColumns+` FROM agent_skills k WHERE k.id = $1 AND k.agent_id = $2`, skillID, agentID))
	if err != nil {
		return k, notFoundIfNoRows(err, "Skill")
	}
	k.Files, err = listSkillFiles(ctx, s.Pool, "agent_skill_files", "agent_skill_id", skillID)
	return k, err
}

func copyTemplateFiles(ctx context.Context, tx pgx.Tx, templateID, agentSkillID uuid.UUID) error {
	_, err := tx.Exec(ctx, `INSERT INTO agent_skill_files (agent_skill_id, path, blob_sha256, size)
		SELECT $2, path, blob_sha256, size FROM skill_template_files WHERE template_id = $1`, templateID, agentSkillID)
	return err
}

func agentSkillNameConflict(ctx context.Context, tx pgx.Tx, agentID uuid.UUID, name string, exclude uuid.UUID) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM agent_skills WHERE agent_id = $1 AND name = $2 AND id <> $3)`, agentID, name, exclude).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return apperr.Conflict(apperr.CodeNameConflict, "Agent 上已有名为 "+name+" 的 Skill，请先移除")
	}
	return nil
}

// AddAgentSkill 把模板当前内容复制给 Agent。
func (s *Store) AddAgentSkill(ctx context.Context, agentID, templateID uuid.UUID) (AgentSkill, error) {
	id := uuid.Must(uuid.NewV7())
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := lockAgent(ctx, tx, agentID); err != nil {
			return err
		}
		var name, desc string
		if err := tx.QueryRow(ctx, `SELECT name, description FROM skill_templates WHERE id = $1 FOR SHARE`, templateID).Scan(&name, &desc); err != nil {
			return notFoundIfNoRows(err, "Skill 模板")
		}
		if err := agentSkillNameConflict(ctx, tx, agentID, name, uuid.Nil); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO agent_skills (id, agent_id, name, description, source_template_id) VALUES ($1, $2, $3, $4, $5)`,
			id, agentID, name, desc, templateID); err != nil {
			return err
		}
		if err := copyTemplateFiles(ctx, tx, templateID, id); err != nil {
			return err
		}
		return bumpConfig(ctx, tx, agentID)
	})
	if err != nil {
		return AgentSkill{}, err
	}
	return s.GetAgentSkill(ctx, agentID, id)
}

func lockAgentSkill(ctx context.Context, tx pgx.Tx, agentID, skillID uuid.UUID) (AgentSkill, error) {
	k, err := scanAgentSkill(tx.QueryRow(ctx, `SELECT `+agentSkillColumns+` FROM agent_skills k WHERE k.id = $1 AND k.agent_id = $2 FOR UPDATE`, skillID, agentID))
	return k, notFoundIfNoRows(err, "Skill")
}

// UpdateAgentSkillMarkdown 只替换副本的 SKILL.md，保留其他文件。
func (s *Store) UpdateAgentSkillMarkdown(ctx context.Context, agentID, skillID uuid.UUID, meta content.SkillMeta, md []byte) (AgentSkill, error) {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := lockAgent(ctx, tx, agentID); err != nil {
			return err
		}
		if _, err := lockAgentSkill(ctx, tx, agentID, skillID); err != nil {
			return err
		}
		if err := agentSkillNameConflict(ctx, tx, agentID, meta.Name, skillID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE agent_skills SET name = $2, description = $3, updated_at = now() WHERE id = $1`, skillID, meta.Name, meta.Description); err != nil {
			return err
		}
		if err := upsertSkillMD(ctx, tx, "agent_skill_files", "agent_skill_id", skillID, md); err != nil {
			return err
		}
		return bumpConfig(ctx, tx, agentID)
	})
	if err != nil {
		return AgentSkill{}, err
	}
	return s.GetAgentSkill(ctx, agentID, skillID)
}

// RecopyAgentSkill 用来源模板的当前内容整体覆盖副本；来源已删除时返回 template_deleted。
func (s *Store) RecopyAgentSkill(ctx context.Context, agentID, skillID uuid.UUID) (AgentSkill, error) {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := lockAgent(ctx, tx, agentID); err != nil {
			return err
		}
		k, err := lockAgentSkill(ctx, tx, agentID, skillID)
		if err != nil {
			return err
		}
		if k.SourceTemplateID == nil {
			return apperr.Conflict(apperr.CodeTemplateDeleted, "来源模板已删除，不能重新复制")
		}
		var name, desc string
		if err := tx.QueryRow(ctx, `SELECT name, description FROM skill_templates WHERE id = $1 FOR SHARE`, *k.SourceTemplateID).Scan(&name, &desc); err != nil {
			if err == pgx.ErrNoRows {
				return apperr.Conflict(apperr.CodeTemplateDeleted, "来源模板已删除，不能重新复制")
			}
			return err
		}
		if err := agentSkillNameConflict(ctx, tx, agentID, name, skillID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM agent_skill_files WHERE agent_skill_id = $1`, skillID); err != nil {
			return err
		}
		if err := copyTemplateFiles(ctx, tx, *k.SourceTemplateID, skillID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE agent_skills SET name = $2, description = $3, copied_at = now(), updated_at = now() WHERE id = $1`, skillID, name, desc); err != nil {
			return err
		}
		return bumpConfig(ctx, tx, agentID)
	})
	if err != nil {
		return AgentSkill{}, err
	}
	return s.GetAgentSkill(ctx, agentID, skillID)
}

// RemoveAgentSkill 从 Agent 上移除副本。
func (s *Store) RemoveAgentSkill(ctx context.Context, agentID, skillID uuid.UUID) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := lockAgent(ctx, tx, agentID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM agent_skills WHERE id = $1 AND agent_id = $2`, skillID, agentID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.NotFound("Skill")
		}
		return bumpConfig(ctx, tx, agentID)
	})
}
