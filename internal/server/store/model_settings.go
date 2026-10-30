package store

import (
	"context"
	"time"

	"loop-now/internal/server/apperr"
)

// ModelSettings 保存用户编辑的原生 models.json 文本。
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

// CompareAndSwapModelSettings 以修订号防止两个编辑器相互覆盖。
func (s *Store) CompareAndSwapModelSettings(ctx context.Context, modelsJSON string, expected int64) (ModelSettings, error) {
	if expected < 1 {
		return ModelSettings{}, apperr.Invalid("expectedRevision", "必须提供正整数版本")
	}
	var out ModelSettings
	err := s.Pool.QueryRow(ctx, `UPDATE model_settings SET models_json=$1,revision=revision+1,updated_at=now() WHERE singleton=true AND revision=$2 RETURNING models_json,revision,updated_at`, modelsJSON, expected).Scan(&out.ModelsJSON, &out.Revision, &out.UpdatedAt)
	if IsNotFound(err) {
		return ModelSettings{}, apperr.PreconditionFailed("模型配置已被修改，请重新读取后再保存")
	}
	return out, err
}
