package store

import (
	"context"
	"time"

	"loop-now/internal/server/apperr"
)

// ModelSettings stores the native models.json text the user edits.
type ModelSettings struct {
	ModelsJSON string
	Revision   int64
	UpdatedAt  time.Time
}

func (s *Store) GetModelSettings(ctx context.Context) (ModelSettings, error) {
	var out ModelSettings
	err := s.Pool.QueryRow(ctx, `SELECT models_json,revision,updated_at FROM model_settings WHERE singleton=true`).Scan(&out.ModelsJSON, &out.Revision, &out.UpdatedAt)
	return out, err
}

// CompareAndSwapModelSettings uses the revision to keep two editors from overwriting each other.
func (s *Store) CompareAndSwapModelSettings(ctx context.Context, modelsJSON string, expected int64) (ModelSettings, error) {
	if expected < 1 {
		return ModelSettings{}, apperr.Invalid("expectedRevision", "Must be a positive integer.")
	}
	var out ModelSettings
	err := s.Pool.QueryRow(ctx, `UPDATE model_settings SET models_json=$1,revision=revision+1,updated_at=$3 WHERE singleton AND revision=$2 RETURNING models_json,revision,updated_at`, modelsJSON, expected, s.Pool.Now()).Scan(&out.ModelsJSON, &out.Revision, &out.UpdatedAt)
	if IsNotFound(err) {
		return ModelSettings{}, apperr.PreconditionFailed("The model configuration was changed elsewhere. Reload it, then save again.")
	}
	return out, err
}
