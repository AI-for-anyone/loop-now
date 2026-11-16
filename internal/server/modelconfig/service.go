// Package modelconfig manages the native models.json document and atomically publishes the catalog after validating it
// with the pinned Pi.
package modelconfig

import (
	"context"
	"fmt"
	"sync"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/store"
)

const DefaultModelsJSON = "{\"providers\":{}}"

type Settings struct {
	ModelsJSON string `json:"modelsJson"`
	Revision   int64  `json:"revision"`
	LoadError  string `json:"loadError,omitempty"`
}

const fallbackLoadError = "The saved models.json did not pass validation by the current Pi, so the built-in model catalog is in use for now. Fix it and save again."

// Store is the narrow persistence interface the settings service needs; the production implementation is store.Store.
type Store interface {
	GetModelSettings(context.Context) (store.ModelSettings, error)
	CompareAndSwapModelSettings(context.Context, string, int64) (store.ModelSettings, error)
}

type Service struct {
	Store     Store
	Models    *modelcatalog.Registry
	Options   modelcatalog.Options
	mu        sync.Mutex
	loadError string
}

// Reload rebuilds the current catalog from the database. When the user config is invalid, the original text is kept for
// fixing, but the built-in catalog is used first, so that a Pi upgrade or a schema change cannot stop the console
// itself from starting.
func (s *Service) Reload(ctx context.Context) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, err := s.Store.GetModelSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	settings := Settings{ModelsJSON: row.ModelsJSON, Revision: row.Revision}
	catalog, loadErr := s.load(ctx, row.ModelsJSON, row.Revision)
	if loadErr != nil {
		catalog, err = modelcatalog.Load(ctx, s.Options)
		if err != nil {
			return Settings{}, fmt.Errorf("fall back to the built-in model catalog: %w", err)
		}
		settings.LoadError = fallbackLoadError
	}
	s.Models.Replace(catalog)
	s.loadError = settings.LoadError
	return settings, nil
}

func (s *Service) Get(ctx context.Context) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, err := s.Store.GetModelSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	return Settings{ModelsJSON: row.ModelsJSON, Revision: row.Revision, LoadError: s.loadError}, nil
}

func (s *Service) Put(ctx context.Context, modelsJSON string, expected int64) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected < 1 {
		return Settings{}, apperr.Invalid("expectedRevision", "Must be a positive integer.")
	}
	current, err := s.Store.GetModelSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	if current.Revision != expected {
		return Settings{}, apperr.PreconditionFailed("The model configuration was changed elsewhere. Reload it, then save again.")
	}
	catalog, err := s.load(ctx, modelsJSON, current.Revision+1)
	if err != nil {
		message, safe := validationMessage(err)
		if !safe {
			message = "The config did not pass Pi validation. Check the providers, models and field values."
		}
		return Settings{}, apperr.Invalid("modelsJson", message)
	}
	row, err := s.Store.CompareAndSwapModelSettings(ctx, modelsJSON, expected)
	if err != nil {
		return Settings{}, err
	}
	s.Models.Replace(catalog)
	s.loadError = ""
	return Settings{ModelsJSON: row.ModelsJSON, Revision: row.Revision}, nil
}

func (s *Service) load(ctx context.Context, text string, revision int64) (*modelcatalog.Catalog, error) {
	if err := validateModelsJSON(text); err != nil {
		return nil, err
	}
	o := s.Options
	o.ModelsJSON, o.Revision = []byte(text), revision
	return modelcatalog.Load(ctx, o)
}
