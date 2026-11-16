package plugins

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"loop-now/internal/pluginmanifest"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/modelcatalog"
)

// Model providers registered by plugins (plugins/SPEC.md §13): once an install or update is confirmed, the bundled Pi
// loads the plugin's Pi extensions to read them, and they are saved with the version. New providers that register an
// OAuth login and whose id differs from those in Pi's catalog are merged into the model catalog (source is plugin)
// and can be chosen once the Agent has been granted the plugin.

// captureProviders reads the model providers registered by the plugin's extensions; a failed read does not block the
// install, and the reason is saved with the version.
func (s *Service) captureProviders(ctx context.Context, dir string, m Manifest) (modelcatalog.Capture, *string) {
	empty := modelcatalog.Capture{Providers: []modelcatalog.CapturedProvider{}, Errors: []modelcatalog.CaptureError{}}
	if s.CaptureProviders == nil || len(m.Extensions) == 0 {
		return empty, nil
	}
	c, err := s.CaptureProviders(ctx, dir, m.Extensions)
	if err != nil {
		s.Log.Warn("failed to read plugin-registered model providers", "plugin", m.ID, "err", err)
		msg := err.Error()
		return empty, &msg
	}
	return c, nil
}

// checkProviderConflicts rejects providers with the same id as one registered by another installed plugin (only
// those that would go into the model catalog count).
func (s *Service) checkProviderConflicts(ctx context.Context, id string, c modelcatalog.Capture) error {
	mine := map[string]bool{}
	for _, p := range c.Providers {
		if p.CatalogEligible() {
			mine[p.ID] = true
		}
	}
	if len(mine) == 0 {
		return nil
	}
	others, err := s.installedProviders(ctx)
	if err != nil {
		return err
	}
	for _, o := range others {
		if o.PluginID != id && mine[o.Provider.ID] {
			return apperr.Conflict(apperr.CodeNameConflict, "插件 "+o.PluginID+" 已注册模型提供方 "+o.Provider.ID+"，不能再由插件 "+id+" 注册")
		}
	}
	return nil
}

// installedProviders returns the providers registered by the current version of each installed plugin that go into
// the model catalog.
func (s *Service) installedProviders(ctx context.Context) ([]modelcatalog.PluginProvider, error) {
	rows, err := s.Store.CurrentPluginProviders(ctx)
	if err != nil {
		return nil, err
	}
	var out []modelcatalog.PluginProvider
	for _, r := range rows {
		var c modelcatalog.Capture
		if json.Unmarshal(r.Providers, &c) != nil {
			continue
		}
		for _, p := range c.Providers {
			if p.CatalogEligible() {
				out = append(out, modelcatalog.PluginProvider{PluginID: r.PluginID, Provider: p})
			}
		}
	}
	return out, nil
}

// PublishProviders merges the providers registered by installed plugins into the model catalog; called at server
// startup and after an install, update or uninstall.
func (s *Service) PublishProviders(ctx context.Context) error {
	if s.Models == nil {
		return nil
	}
	providers, err := s.installedProviders(ctx)
	if err != nil {
		return err
	}
	s.Models.SetPluginProviders(providers)
	return nil
}

func (s *Service) publishProviders(ctx context.Context) {
	if err := s.PublishProviders(ctx); err != nil {
		s.Log.Warn("failed to merge plugin providers into the model catalog", "err", err)
	}
}

// ProviderPlugin returns the plugin that registered the model provider provider; ok is false when it is not a plugin
// provider.
func ProviderPlugin(models modelcatalog.Reader, provider string) (string, bool) {
	if models == nil {
		return "", false
	}
	p, ok := models.Provider(provider)
	if !ok || p.Source != "plugin" || p.PluginID == nil {
		return "", false
	}
	return *p.PluginID, true
}

// providerViews turns the captured providers into their API form; InCatalog means the provider is in the model
// catalog right now and belongs to this plugin.
func (s *Service) providerViews(id string, c modelcatalog.Capture) []ProviderView {
	out := []ProviderView{}
	for _, p := range c.Providers {
		in := false
		if s.Models != nil {
			owner, ok := ProviderPlugin(s.Models, p.ID)
			in = ok && owner == id
		}
		out = append(out, ProviderView{ID: p.ID, Name: p.Name, Extension: p.Extension, OAuth: p.OAuth, OverridesBuiltin: p.OverridesBuiltin, InCatalog: in, Error: p.Error, ModelCount: len(p.Models)})
	}
	return out
}

// PluginOfPath returns a function that tells which installed plugin a file belongs to (by the install directories
// of each version); ok is false when it belongs to no plugin.
func (s *Service) PluginOfPath(ctx context.Context) (func(string) (string, bool), error) {
	dirs, err := s.Store.PluginInstallDirs(ctx)
	if err != nil {
		return nil, err
	}
	return func(path string) (string, bool) {
		clean := filepath.Clean(path)
		for id, list := range dirs {
			for _, d := range list {
				if strings.HasPrefix(clean, filepath.Clean(d)+string(filepath.Separator)) {
					return id, true
				}
			}
		}
		return "", false
	}, nil
}

// Extensions returns the absolute paths of the Pi extensions of the plugin's current version (loaded for a plugin
// provider login); it returns not_found when the plugin does not exist, and an empty list when it has no extensions.
func (s *Service) Extensions(ctx context.Context, id string) ([]string, error) {
	p, err := s.Store.GetPlugin(ctx, id)
	if err != nil {
		return nil, err
	}
	_, m, err := view(p)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, e := range m.Extensions {
		out = append(out, filepath.Join(p.InstallDir, filepath.Clean(e)))
	}
	return out, nil
}

// ProviderStatePaths returns the HOME-relative paths that the plugin's current version declares to travel with the
// account for the model provider provider (plugins/SPEC.md §13); empty when nothing is declared or the plugin does
// not exist.
func (s *Service) ProviderStatePaths(ctx context.Context, plugin, provider string) ([]string, error) {
	rows, err := s.Store.CurrentPluginProviders(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.PluginID != plugin {
			continue
		}
		var decl map[string]pluginmanifest.ProviderState
		if err := json.Unmarshal(r.ProviderState, &decl); err != nil {
			return nil, err
		}
		return decl[provider].Paths, nil
	}
	return nil, nil
}
