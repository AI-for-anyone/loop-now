package store

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
)

type ProjectPrompt struct {
	Prompt        string `json:"prompt"`
	PromptVersion int64  `json:"promptVersion"`
}

func (s *Store) GetProjectPrompt(ctx context.Context, id uuid.UUID) (ProjectPrompt, error) {
	var out ProjectPrompt
	err := s.Pool.QueryRow(ctx, `SELECT prompt,prompt_version FROM projects WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&out.Prompt, &out.PromptVersion)
	return out, notFoundIfNoRows(err, "项目")
}
func (s *Store) PutProjectPrompt(ctx context.Context, id uuid.UUID, prompt string, expected int64) (ProjectPrompt, error) {
	if expected < 1 {
		return ProjectPrompt{}, apperr.Invalid("expectedPromptVersion", "须提供正整数Prompt编辑版本")
	}
	var out ProjectPrompt
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var current ProjectPrompt
		err := tx.QueryRow(ctx, `SELECT prompt,prompt_version FROM projects WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&current.Prompt, &current.PromptVersion)
		if err != nil {
			return notFoundIfNoRows(err, "项目")
		}
		if current.PromptVersion != expected {
			return apperr.PreconditionFailed("项目Prompt已改变，请读取最新文本并核对")
		}
		if current.Prompt == prompt {
			out = current
			return nil
		}
		return tx.QueryRow(ctx, `UPDATE projects SET prompt=$2,prompt_version=prompt_version+1,updated_at=now() WHERE id=$1 RETURNING prompt,prompt_version`, id, prompt).Scan(&out.Prompt, &out.PromptVersion)
	})
	if err != nil {
		return ProjectPrompt{}, err
	}
	return out, nil
}
