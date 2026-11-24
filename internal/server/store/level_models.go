package store

import (
	"context"

	"loop-now/internal/server/db"
)

// LevelModel is a model at one thinking level.
type LevelModel struct{ ModelID, Level string }

// AdoptLevelModels moves the Agents of provider that still name a model at one thinking level by the id the CLI lists
// for it (ids maps such an id to its model and level; Antigravity's gemini-4-neon-xhigh is gemini-4-neon at xhigh) to
// that model and level. Only Agents with no level or off move: before the levels were merged that was the only level
// such a model had, so the Agent runs as before. The config version stays: the Agent runs the same model at the same
// level. It reports how many Agents moved; a second call with the same ids moves none.
func (s *Store) AdoptLevelModels(ctx context.Context, provider string, ids map[string]LevelModel) (int64, error) {
	var moved int64
	err := s.inTx(ctx, func(tx *db.Tx) error {
		for listed, m := range ids {
			tag, err := tx.Exec(ctx, `UPDATE agents SET model_id = $3, thinking_level = $4
				WHERE model_provider = $1 AND model_id = $2 AND (thinking_level IS NULL OR thinking_level = 'off')`, provider, listed, m.ModelID, m.Level)
			if err != nil {
				return err
			}
			moved += tag.RowsAffected()
		}
		return nil
	})
	return moved, err
}
