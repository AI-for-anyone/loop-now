package store

import "context"

// TextGenerationSelection is the runtime model a person chose to write with AI ("Write with AI"); ThinkingLevel nil
// means the model's default.
type TextGenerationSelection struct {
	Provider      string
	ModelID       string
	ThinkingLevel *string
}

// GetTextGenerationSelection reads who writes with AI; nil is Automatic.
func (s *Store) GetTextGenerationSelection(ctx context.Context) (*TextGenerationSelection, error) {
	var provider, model, level *string
	if err := s.Pool.QueryRow(ctx, `SELECT provider, model_id, thinking_level FROM text_generation_settings WHERE singleton`).Scan(&provider, &model, &level); err != nil {
		return nil, err
	}
	if provider == nil || model == nil {
		return nil, nil
	}
	return &TextGenerationSelection{Provider: *provider, ModelID: *model, ThinkingLevel: level}, nil
}

// PutTextGenerationSelection saves who writes with AI; nil goes back to Automatic.
func (s *Store) PutTextGenerationSelection(ctx context.Context, sel *TextGenerationSelection) error {
	var provider, model, level *string
	if sel != nil {
		provider, model, level = &sel.Provider, &sel.ModelID, sel.ThinkingLevel
	}
	_, err := s.Pool.Exec(ctx, `UPDATE text_generation_settings SET provider = $1, model_id = $2, thinking_level = $3, updated_at = now() WHERE singleton`,
		provider, model, level)
	return err
}
