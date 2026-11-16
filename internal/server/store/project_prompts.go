package store

import (
	"context"
	"github.com/google/uuid"
	"loop-now/internal/server/apperr"

	"loop-now/internal/server/db"
)

type ProjectPrompt struct {
	Prompt        string `json:"prompt"`
	PromptVersion int64  `json:"promptVersion"`
}

func (s *Store) GetProjectPrompt(ctx context.Context, id uuid.UUID) (ProjectPrompt, error) {
	var out ProjectPrompt
	err := s.Pool.QueryRow(ctx, `SELECT prompt,prompt_version FROM projects WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&out.Prompt, &out.PromptVersion)
	return out, notFoundIfNoRows(err, "Project")
}
func (s *Store) PutProjectPrompt(ctx context.Context, id uuid.UUID, prompt string, expected int64) (ProjectPrompt, error) {
	if expected < 1 {
		return ProjectPrompt{}, apperr.Invalid("expectedPromptVersion", "Must be a positive integer.")
	}
	var out ProjectPrompt
	err := s.inTx(ctx, func(tx *db.Tx) error {
		var current ProjectPrompt
		err := tx.QueryRow(ctx, `SELECT prompt,prompt_version FROM projects WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&current.Prompt, &current.PromptVersion)
		if err != nil {
			return notFoundIfNoRows(err, "Project")
		}
		if current.PromptVersion != expected {
			return apperr.PreconditionFailed("The project prompt has changed. Read the latest text and review it.")
		}
		if current.Prompt == prompt {
			out = current
			return nil
		}
		return tx.QueryRow(ctx, `UPDATE projects SET prompt=$2,prompt_version=prompt_version+1,updated_at=$3 WHERE id=$1 RETURNING prompt,prompt_version`, id, prompt, tx.Now()).Scan(&out.Prompt, &out.PromptVersion)
	})
	if err != nil {
		return ProjectPrompt{}, err
	}
	return out, nil
}
