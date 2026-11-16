package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/pluginmanifest"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/pluginhost"
	"loop-now/internal/server/store"
)

// Service manages installed plugins. Installs, updates and uninstalls run one at a time.
type Service struct {
	Store *store.Store
	// Host is the plugin host supervisor; it is nil when the server has no plugin host entry point, and then platform
	// sides do not run.
	Host *pluginhost.Supervisor
	// Root is <work root>/platform/plugins: install directories are <Root>/<id>/<version>/ and the data directory is
	// <Root>/<id>/data/.
	Root string
	// OfficialDir is the official plugin directory shipped with the platform (one plugin per subdirectory); when empty
	// there is no builtin source.
	OfficialDir string
	// OfficialMarket is the address of the official market's loop-now-market.json; when empty only the markets in the
	// settings are used. MarketClient reads markets (http.DefaultClient by default).
	OfficialMarket  string
	MarketClient    *http.Client
	NodeBin, GitBin string
	// APIURL is the /api/v1 address that plugin tokens call (the loopback listener).
	APIURL          string
	PlatformVersion string
	FetchTimeout    time.Duration
	PreviewTTL      time.Duration
	// RequestTimeout is how long action and http requests wait for the plugin to respond; 30 seconds by default.
	RequestTimeout time.Duration
	Log            *slog.Logger
	// CaptureProviders uses the bundled Pi to load the extensions under the plugin directory dir (relative paths) and
	// reads the model providers they register; when nil, nothing is read. It runs plugin code, so it is only called
	// after the user has confirmed the install.
	CaptureProviders func(ctx context.Context, dir string, extensions []string) (modelcatalog.Capture, error)
	// Models is the model catalog: plugin providers are merged into it after an install, update or uninstall; when
	// nil, nothing is merged.
	Models *modelcatalog.Registry
	// DeleteProviderAccounts deletes the project subscription accounts of these model providers (when uninstalling
	// with data deletion); when nil, nothing is deleted.
	DeleteProviderAccounts func(ctx context.Context, providers []string) error
	// ManagedHomePaths returns the paths the platform manages in the Agent's HOME (the credential files of CLI
	// templates); on install it is used to point out state paths that overlap them.
	ManagedHomePaths func() []string

	mu       sync.Mutex
	previews map[uuid.UUID]*preview
	limiter  rateLimiter
}

type preview struct {
	view    Preview
	dir     string // plugin directory: the copy in the staging area, or the original directory for a local source
	staging string // staging directory; empty for a local source
	m       Manifest
	// providerState is the market entry's declaration (when installing from a market); when nil the manifest's
	// loopNow.providerState is used.
	providerState map[string]pluginmanifest.ProviderState
}

// Init cleans up the staging directories left over from the previous run.
func (s *Service) Init() error {
	if s.FetchTimeout <= 0 {
		s.FetchTimeout = 10 * time.Minute
	}
	if s.PreviewTTL <= 0 {
		s.PreviewTTL = 30 * time.Minute
	}
	if s.RequestTimeout <= 0 {
		s.RequestTimeout = DefaultRequestTimeout
	}
	if s.Log == nil {
		s.Log = slog.Default()
	}
	s.previews = map[uuid.UUID]*preview{}
	return os.RemoveAll(filepath.Join(s.Root, ".staging"))
}

// ---- API structures ----

// View is the API's Plugin.
type View struct {
	ID              string         `json:"id"`
	DisplayName     Text           `json:"displayName"`
	Description     Text           `json:"description"`
	Version         string         `json:"version"`
	Source          ResolvedSource `json:"source"`
	Status          string         `json:"status"`
	LastError       *string        `json:"lastError"`
	HasPlatform     bool           `json:"hasPlatform"`
	EnabledProjects int            `json:"enabledProjects"`
	GrantedAgents   int            `json:"grantedAgents"`
	InstalledAt     time.Time      `json:"installedAt"`
	UpdatedAt       time.Time      `json:"updatedAt"`
}

// Detail is the API's PluginDetail.
type Detail struct {
	Plugin     View           `json:"plugin"`
	Manifest   PublicManifest `json:"manifest"`
	Versions   []VersionView  `json:"versions"`
	InstallDir string         `json:"installDir"`
	DataDir    string         `json:"dataDir"`
	// Providers are the model providers registered by the current version's Pi extensions (read at install time);
	// ProvidersError is the reason the whole read failed.
	Providers      []ProviderView              `json:"providers"`
	ProviderErrors []modelcatalog.CaptureError `json:"providerErrors"`
	ProvidersError *string                     `json:"providersError"`
	// ProviderState is the providerState declaration in effect for the current version (the market entry first, then
	// the package's own loopNow.providerState).
	ProviderState map[string]pluginmanifest.ProviderState `json:"providerState"`
}

// ProviderView is the API's PluginModelProvider.
type ProviderView struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	Extension        string  `json:"extension"`
	OAuth            bool    `json:"oauth"`
	OverridesBuiltin bool    `json:"overridesBuiltin"`
	InCatalog        bool    `json:"inCatalog"`
	Error            *string `json:"error"`
	ModelCount       int     `json:"modelCount"`
}

// VersionView is the API's PluginVersion.
type VersionView struct {
	Version     string    `json:"version"`
	Digest      string    `json:"digest"`
	InstalledAt time.Time `json:"installedAt"`
	Current     bool      `json:"current"`
}

// Preview is the API's PluginPreview.
type Preview struct {
	PreviewID        uuid.UUID      `json:"previewId"`
	ExpiresAt        time.Time      `json:"expiresAt"`
	Source           ResolvedSource `json:"source"`
	Trusted          bool           `json:"trusted"`
	Manifest         PublicManifest `json:"manifest"`
	InstalledVersion *string        `json:"installedVersion"`
}

// ConfigView is the API's PluginConfig.
type ConfigView struct {
	Values     map[string]any `json:"values"`
	SecretsSet []string       `json:"secretsSet"`
	Revision   int64          `json:"revision"`
}

// ConfigInput is the API's PluginConfigUpdate.
type ConfigInput struct {
	Values           map[string]any     `json:"values"`
	Secrets          map[string]*string `json:"secrets"`
	ExpectedRevision int64              `json:"expectedRevision"`
}

// ProjectView is the API's ProjectPlugin.
type ProjectView struct {
	PluginID      string     `json:"pluginId"`
	DisplayName   Text       `json:"displayName"`
	Version       string     `json:"version"`
	Status        string     `json:"status"`
	Enabled       bool       `json:"enabled"`
	ProjectFields []string   `json:"projectFields"`
	Config        ConfigView `json:"config"`
}

// AccountView is the API's PluginAccount.
type AccountView struct {
	ID         uuid.UUID      `json:"id"`
	PluginID   string         `json:"pluginId"`
	ProjectID  uuid.UUID      `json:"projectId"`
	Kind       string         `json:"kind"`
	Name       string         `json:"name"`
	Values     map[string]any `json:"values"`
	SecretsSet []string       `json:"secretsSet"`
	Revision   int64          `json:"revision"`
}

// AgentView is the API's AgentPlugin.
type AgentView struct {
	PluginID     string        `json:"pluginId"`
	DisplayName  Text          `json:"displayName"`
	Available    bool          `json:"available"`
	Granted      bool          `json:"granted"`
	AccountIDs   []uuid.UUID   `json:"accountIds"`
	AccountKinds []AccountKind `json:"accountKinds"`
}

// HostView is the API's PluginHostStatus.
type HostView struct {
	Configured  bool              `json:"configured"`
	State       string            `json:"state"`
	PID         *int              `json:"pid"`
	StartedAt   *time.Time        `json:"startedAt"`
	HostVersion *string           `json:"hostVersion"`
	RecentExits []time.Time       `json:"recentExits"`
	LastCrash   *pluginhost.Crash `json:"lastCrash"`
	Loaded      []string          `json:"loaded"`
	Today       struct {
		Delivered int `json:"delivered"`
		Failed    int `json:"failed"`
	} `json:"today"`
}

// BuiltinSource is the API's BuiltinPluginSource.
type BuiltinSource struct {
	ID          string `json:"id"`
	DisplayName Text   `json:"displayName"`
	Description Text   `json:"description"`
	Version     string `json:"version"`
}

func view(p store.Plugin) (View, Manifest, error) {
	var m Manifest
	var src ResolvedSource
	if err := json.Unmarshal(p.Manifest, &m); err != nil {
		return View{}, m, err
	}
	if err := json.Unmarshal(p.Source, &src); err != nil {
		return View{}, m, err
	}
	return View{ID: p.ID, DisplayName: m.DisplayName, Description: m.Description, Version: p.Version, Source: src, Status: p.Status, LastError: p.LastError,
		HasPlatform: m.Platform != "", EnabledProjects: p.EnabledProjects, GrantedAgents: p.GrantedAgents, InstalledAt: p.InstalledAt, UpdatedAt: p.UpdatedAt}, m, nil
}

// get reads a plugin and its manifest.
func (s *Service) get(ctx context.Context, id string) (View, Manifest, error) {
	p, err := s.Store.GetPlugin(ctx, id)
	if err != nil {
		return View{}, Manifest{}, err
	}
	return view(p)
}

// ---- Managing external identities ----

// SetIdentityManager sets or clears the plugin channel that manages an external identity (plugins/SPEC.md §12): the
// plugin must be installed and the channel must declare manageIdentities in the manifest, otherwise 422.
func (s *Service) SetIdentityManager(ctx context.Context, identity uuid.UUID, m *store.IdentityManager) (store.ExternalIdentity, error) {
	if m != nil {
		_, manifest, err := s.get(ctx, m.PluginID)
		if store.IsNotFound(err) {
			return store.ExternalIdentity{}, apperr.Invalid("managedBy.pluginId", "Plugin "+m.PluginID+" is not installed.")
		}
		if err != nil {
			return store.ExternalIdentity{}, err
		}
		if !slices.Contains(manifest.ManagingChannels(), m.ChannelID) {
			return store.ExternalIdentity{}, apperr.Invalid("managedBy.channelId", "Plugin "+m.PluginID+"'s channel "+m.ChannelID+" does not declare that it can manage external identities.")
		}
	}
	return s.Store.SetIdentityManager(ctx, identity, m)
}

// ---- Install, update, uninstall ----

// List returns all installed plugins.
func (s *Service) List(ctx context.Context) ([]View, error) {
	ps, err := s.Store.ListPlugins(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(ps))
	for _, p := range ps {
		v, _, err := view(p)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Get returns a plugin's details.
func (s *Service) Get(ctx context.Context, id string) (Detail, error) {
	v, m, err := s.get(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	p, err := s.Store.GetPlugin(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	versions, err := s.Store.PluginVersions(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	d := Detail{Plugin: v, Manifest: m.Public(), Versions: []VersionView{}, InstallDir: p.InstallDir, DataDir: s.dataDir(id), Providers: []ProviderView{}, ProviderErrors: []modelcatalog.CaptureError{}}
	for _, ver := range versions {
		d.Versions = append(d.Versions, VersionView{Version: ver.Version, Digest: ver.Digest, InstalledAt: ver.InstalledAt, Current: ver.Version == p.Version})
		if ver.Version == p.Version {
			d.ProvidersError = ver.ProvidersError
			_ = json.Unmarshal(ver.ProviderState, &d.ProviderState)
			var c modelcatalog.Capture
			if json.Unmarshal(ver.Providers, &c) == nil {
				d.Providers, d.ProviderErrors = s.providerViews(id, c), append(d.ProviderErrors, c.Errors...)
			}
		}
	}
	if d.ProviderState == nil {
		d.ProviderState = map[string]pluginmanifest.ProviderState{}
	}
	return d, nil
}

func (s *Service) dataDir(id string) string { return filepath.Join(s.Root, id, "data") }

// PreviewRequest is the API's PluginPreviewRequest.
type PreviewRequest struct {
	Source   *Source `json:"source"`
	PluginID string  `json:"pluginId"`
}

// Preview fetches a plugin, reads its manifest and keeps it staged until it is installed or updated (valid for
// PreviewTTL).
func (s *Service) Preview(ctx context.Context, req PreviewRequest) (Preview, error) {
	s.expirePreviews()
	if (req.Source == nil) == (req.PluginID == "") {
		return Preview{}, apperr.Invalid("source", "Give exactly one of source and pluginId.")
	}
	src := req.Source
	if req.PluginID != "" {
		p, err := s.Store.GetPlugin(ctx, req.PluginID)
		if err != nil {
			return Preview{}, err
		}
		var rs ResolvedSource
		if err := json.Unmarshal(p.Source, &rs); err != nil {
			return Preview{}, err
		}
		src = &Source{Kind: rs.Kind, Location: rs.Location, Ref: rs.Ref}
		if rs.Market != nil {
			src = &Source{Kind: SourceMarket, Market: *rs.Market, ID: req.PluginID}
		}
	}
	fetchSrc, trusted, market := *src, false, (*string)(nil)
	var declared map[string]pluginmanifest.ProviderState
	var entry *marketEntry
	switch {
	case src.Kind == SourceMarket:
		var err error
		if fetchSrc, trusted, entry, err = s.resolveMarket(ctx, *src); err != nil {
			return Preview{}, err
		}
		declared = entry.ProviderState
		market = &src.Market
	case src.Location == "":
		return Preview{}, apperr.Invalid("source.location", "A source location is required.")
	}
	id := uuid.Must(uuid.NewV7())
	staging := filepath.Join(s.Root, ".staging", id.String())
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return Preview{}, err
	}
	dir, resolved, err := s.fetch(ctx, fetchSrc, filepath.Join(staging, "src"))
	if err == nil {
		resolved.Market = market
		var m Manifest
		if m, err = ReadManifest(dir, s.PlatformVersion); err == nil {
			// When installing from a market and the package does not set its own loopNow.displayName, use the market
			// entry's display name (instead of the npm package name); it is saved with the version and taken again from
			// the new entry on update.
			if entry != nil && !m.OwnDisplayName && entry.DisplayName.String() != "" {
				m.DisplayName = entry.DisplayName
			}
			switch {
			case req.PluginID != "" && m.ID != req.PluginID:
				err = apperr.Invalid("manifest", "The plugin id in the source has changed to "+m.ID+", so it cannot be an update of "+req.PluginID+".")
			case market != nil && m.ID != src.ID:
				err = apperr.Invalid("manifest", "Market entry "+src.ID+" has a source that contains plugin "+m.ID)
			default:
				return s.keepPreview(ctx, id, staging, dir, resolved, m, trusted, declared)
			}
		}
	}
	_ = os.RemoveAll(staging)
	return Preview{}, err
}

func (s *Service) keepPreview(ctx context.Context, id uuid.UUID, staging, dir string, resolved ResolvedSource, m Manifest, trusted bool, declared map[string]pluginmanifest.ProviderState) (Preview, error) {
	if resolved.Kind == SourceNPM {
		v := m.Version
		resolved.NpmVersion = &v
	}
	if resolved.Kind == SourceLocal {
		_ = os.RemoveAll(staging)
		staging = ""
	}
	pv := Preview{PreviewID: id, ExpiresAt: time.Now().Add(s.PreviewTTL), Source: resolved, Trusted: trusted || resolved.Kind == SourceBuiltin, Manifest: m.Public()}
	if p, err := s.Store.GetPlugin(ctx, m.ID); err == nil {
		v := p.Version
		pv.InstalledVersion = &v
	} else if !store.IsNotFound(err) {
		return Preview{}, err
	}
	s.mu.Lock()
	s.previews[id] = &preview{view: pv, dir: dir, staging: staging, m: m, providerState: declared}
	s.mu.Unlock()
	return pv, nil
}

func (s *Service) expirePreviews() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, pv := range s.previews {
		if now.After(pv.view.ExpiresAt) {
			if pv.staging != "" {
				_ = os.RemoveAll(pv.staging)
			}
			delete(s.previews, id)
		}
	}
}

// InstallInput is the API's PluginInstall.
type InstallInput struct {
	PreviewID uuid.UUID `json:"previewId"`
	Confirm   bool      `json:"confirm"`
}

// Install installs a new plugin from a preview; when update is not empty it is an update of the installed plugin
// update. A preview is used up once used; it is kept only when the confirmation is missing, so the request can be
// retried after confirming.
func (s *Service) Install(ctx context.Context, in InstallInput, update string) (View, error) {
	s.expirePreviews()
	s.mu.Lock()
	defer s.mu.Unlock()
	pv := s.previews[in.PreviewID]
	if pv == nil {
		return View{}, apperr.NotFound("Preview")
	}
	if !pv.view.Trusted && !in.Confirm {
		return View{}, apperr.Conflict(apperr.CodePluginConfirmationRequired, "The plugin comes from an unofficial source. Confirm before installing.")
	}
	defer func() {
		if pv.staging != "" {
			_ = os.RemoveAll(pv.staging)
		}
		delete(s.previews, in.PreviewID)
	}()
	return s.install(ctx, pv, update)
}

func (s *Service) install(ctx context.Context, pv *preview, update string) (View, error) {
	m := pv.m
	existing, err := s.Store.GetPlugin(ctx, m.ID)
	switch {
	case err != nil && !store.IsNotFound(err):
		return View{}, err
	case update == "" && err == nil:
		return View{}, apperr.Conflict(apperr.CodeNameConflict, "Plugin "+m.ID+" is already installed. Update it instead.")
	case update != "" && update != m.ID:
		return View{}, apperr.Invalid("previewId", "The previewed plugin is "+m.ID+", not "+update)
	case update != "" && err != nil:
		return View{}, err
	}
	sum, err := digest(pv.dir)
	if err != nil {
		return View{}, err
	}
	installDir, codeDir, target, reuse := pv.dir, pv.dir, "", false
	if pv.view.Source.Kind != SourceLocal {
		target = filepath.Join(s.Root, m.ID, m.Version)
		if update != "" {
			versions, err := s.Store.PluginVersions(ctx, m.ID)
			if err != nil {
				return View{}, err
			}
			for _, v := range versions {
				if v.Version == m.Version && v.Digest != sum {
					return View{}, apperr.Conflict(apperr.CodePluginVersionConflict, "Plugin "+m.ID+" already has version "+m.Version+" with different content. Use a different version number.")
				}
				reuse = reuse || v.Version == m.Version
			}
		}
		if reuse {
			codeDir = target
		} else if pv.view.Source.Kind == SourceGit || pv.view.Source.Kind == SourceNPM {
			if err := s.installDeps(ctx, pv.dir); err != nil {
				return View{}, err
			}
		}
	}
	// Read the model providers after the dependencies are installed and before the files are moved into place: if one
	// has the same id as a provider registered by another plugin, do not install.
	providers, providersError := s.captureProviders(ctx, codeDir, m)
	if err := s.checkProviderConflicts(ctx, m.ID, providers); err != nil {
		return View{}, err
	}
	if target != "" {
		if !reuse {
			if err := os.RemoveAll(target); err != nil {
				return View{}, err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return View{}, err
			}
			if err := os.Rename(pv.dir, target); err != nil {
				return View{}, err
			}
		}
		installDir = target
	}
	if err := os.MkdirAll(s.dataDir(m.ID), 0o700); err != nil {
		return View{}, err
	}
	manifest, _ := json.Marshal(m)
	source, _ := json.Marshal(pv.view.Source)
	captured, _ := json.Marshal(providers)
	declared := pv.providerState
	if len(declared) == 0 {
		declared = m.ProviderState
	}
	if declared == nil {
		declared = map[string]pluginmanifest.ProviderState{}
	}
	state, _ := json.Marshal(declared)
	// State paths that overlap HOME paths managed by the platform would make the Agent fail at startup, so point them
	// out in the details at install time.
	if s.ManagedHomePaths != nil {
		managed := s.ManagedHomePaths()
		for _, provider := range slices.Sorted(maps.Keys(declared)) {
			if p, m, ok := pluginmanifest.OverlappingStatePath(declared[provider].Paths, managed); ok {
				providers.Errors = append(providers.Errors, modelcatalog.CaptureError{Path: "providerState." + provider,
					Error: "State path " + p + " overlaps the credential file " + m + " of a CLI template. Agents that use this provider will fail to start."})
			}
		}
		captured, _ = json.Marshal(providers)
	}
	extra := store.PluginVersionExtras{Providers: captured, ProvidersError: providersError, ProviderState: state}
	row := store.Plugin{ID: m.ID, DisplayName: m.DisplayName.String(), Source: source, Version: m.Version, Manifest: manifest, InstallDir: installDir}
	if update == "" {
		err = s.Store.InstallPlugin(ctx, row, sum, extra)
	} else {
		err = s.Store.UpdatePlugin(ctx, row, sum, extra)
		if err == nil {
			// When the new version no longer declares a managing channel, the external identity bindings on it are
			// cleared (plugins/SPEC.md §12).
			err = s.Store.PruneIdentityManagers(ctx, m.ID, m.ManagingChannels())
		}
	}
	if err != nil {
		return View{}, err
	}
	s.reloadHost(ctx, m.ID, m.Platform != "" || (update != "" && existing.ID != ""))
	s.publishProviders(ctx)
	v, _, err := s.get(ctx, m.ID)
	return v, err
}

// reloadHost makes the plugin host reload (or unload) one plugin according to the latest plugin list; when the host
// is not running, it takes effect on the next start.
func (s *Service) reloadHost(ctx context.Context, id string, touched bool) {
	if s.Host == nil || !touched {
		return
	}
	if err := s.Host.Reload(ctx, id); err != nil {
		s.Log.Warn("plugin host failed to reload the plugin", "plugin", id, "err", err)
	}
}

// Uninstall uninstalls a plugin: it unloads it in the plugin host and deletes the database records and the install
// directories of every version (directories of a local source are not deleted); with deleteData it also deletes the
// data directory.
func (s *Service) Uninstall(ctx context.Context, id string, deleteData bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.Store.GetPlugin(ctx, id)
	if err != nil {
		return err
	}
	var src ResolvedSource
	_ = json.Unmarshal(p.Source, &src)
	// When deleting data, also delete the project subscription accounts of the model providers it registered
	// (plugins/SPEC.md §13); by default they are kept and come back after a reinstall.
	if deleteData && s.DeleteProviderAccounts != nil {
		var providers []string
		if registered, err := s.installedProviders(ctx); err == nil {
			for _, r := range registered {
				if r.PluginID == id {
					providers = append(providers, r.Provider.ID)
				}
			}
		}
		if err := s.DeleteProviderAccounts(ctx, providers); err != nil {
			return err
		}
	}
	if s.Host != nil {
		if err := s.Host.Unload(ctx, id); err != nil {
			s.Log.Warn("plugin host failed to unload the plugin", "plugin", id, "err", err)
		}
	}
	dirs, err := s.Store.DeletePlugin(ctx, id)
	if err != nil {
		return err
	}
	for _, d := range dirs {
		// A local source is used in place and its directory belongs to the author; only delete the platform's own
		// install directories.
		if strings.HasPrefix(d, filepath.Join(s.Root, id)+string(filepath.Separator)) {
			if err := os.RemoveAll(d); err != nil {
				s.Log.Warn("failed to delete the plugin install directory", "plugin", id, "dir", d, "err", err)
			}
		}
	}
	if deleteData {
		if err := os.RemoveAll(s.dataDir(id)); err != nil {
			return err
		}
	}
	_ = os.Remove(filepath.Join(s.Root, id)) // only removed when already empty
	s.publishProviders(ctx)
	return nil
}

// BuiltinSources lists the plugins in the official plugin directory; entries whose manifest breaks the rules are
// skipped and logged.
func (s *Service) BuiltinSources() []BuiltinSource {
	out := []BuiltinSource{}
	if s.OfficialDir == "" {
		return out
	}
	entries, err := os.ReadDir(s.OfficialDir)
	if err != nil {
		s.Log.Warn("failed to read the official plugin directory", "dir", s.OfficialDir, "err", err)
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := ReadManifest(filepath.Join(s.OfficialDir, e.Name()), s.PlatformVersion)
		if err != nil || m.ID != e.Name() {
			s.Log.Warn("skipping an entry in the official plugin directory", "name", e.Name(), "err", err)
			continue
		}
		out = append(out, BuiltinSource{ID: m.ID, DisplayName: m.DisplayName, Description: m.Description, Version: m.Version})
	}
	return out
}

// ---- Config ----

func configView(c store.PluginConfigRow) ConfigView {
	values := c.Values
	if values == nil {
		values = map[string]any{}
	}
	return ConfigView{Values: values, SecretsSet: secretsSet(c.Secrets), Revision: c.Revision}
}

// Config returns the global config.
func (s *Service) Config(ctx context.Context, id string) (ConfigView, error) {
	if _, _, err := s.get(ctx, id); err != nil {
		return ConfigView{}, err
	}
	c, err := s.Store.PluginConfig(ctx, id, nil)
	return configView(c), err
}

// PutConfig saves the global config against the expected revision and hands the new config to the plugin in the
// plugin host.
func (s *Service) PutConfig(ctx context.Context, id string, in ConfigInput) (ConfigView, error) {
	_, m, err := s.get(ctx, id)
	if err != nil {
		return ConfigView{}, err
	}
	secrets := secretFields(m.ConfigSchema)
	if err := checkValueFields(in.Values, secrets, nil, "values"); err != nil {
		return ConfigView{}, err
	}
	saved, err := s.Store.SavePluginConfig(ctx, id, nil, store.PluginConfigUpdate{ExpectedRevision: in.ExpectedRevision, Apply: func(cur store.PluginConfigRow) (store.PluginConfigRow, error) {
		next, err := mergeSecrets(cur.Secrets, in.Secrets, secrets, "secrets")
		if err != nil {
			return cur, err
		}
		values := nonNilMap(in.Values)
		if err := validateAgainst(m.ConfigSchema, false, combine(values, next), "values"); err != nil {
			return cur, err
		}
		return store.PluginConfigRow{Values: values, Secrets: next}, nil
	}})
	if err != nil {
		return ConfigView{}, err
	}
	s.configChanged(ctx, id)
	return configView(saved), nil
}

// configChanged hands the plugin's whole config again to the plugin in the plugin host; it does nothing when the
// plugin is not loaded in the host.
func (s *Service) configChanged(ctx context.Context, id string) {
	if s.Host == nil {
		return
	}
	cfg, err := s.configPayload(ctx, id)
	if err == nil {
		err = s.Host.ConfigChanged(ctx, id, map[string]any{"config": cfg})
	}
	if err != nil && !errors.Is(err, pluginhost.ErrNotLoaded) {
		s.Log.Warn("failed to notify the plugin of a config change", "plugin", id, "err", err)
	}
}

type configRows struct {
	Values  map[string]any    `json:"values"`
	Secrets map[string]string `json:"secrets"`
}

type configPayload struct {
	configRows
	Projects map[string]configRows `json:"projects"`
}

func (s *Service) configPayload(ctx context.Context, id string) (configPayload, error) {
	global, projects, err := s.Store.PluginConfigs(ctx, id)
	if err != nil {
		return configPayload{}, err
	}
	out := configPayload{configRows: configRows{Values: nonNilMap(global.Values), Secrets: nonNilSecrets(global.Secrets)}, Projects: map[string]configRows{}}
	for pid, c := range projects {
		out.Projects[pid.String()] = configRows{Values: nonNilMap(c.Values), Secrets: nonNilSecrets(c.Secrets)}
	}
	return out, nil
}

// ---- Projects ----

// ProjectPlugins returns the list for a project's plugins page.
func (s *Service) ProjectPlugins(ctx context.Context, project uuid.UUID) ([]ProjectView, error) {
	rows, err := s.Store.ProjectPlugins(ctx, project)
	if err != nil {
		return nil, err
	}
	out := make([]ProjectView, 0, len(rows))
	for _, r := range rows {
		v, m, err := view(r.Plugin)
		if err != nil {
			return nil, err
		}
		out = append(out, ProjectView{PluginID: v.ID, DisplayName: v.DisplayName, Version: v.Version, Status: v.Status, Enabled: r.Enabled,
			ProjectFields: projectFields(m.ConfigSchema), Config: configView(r.Config)})
	}
	return out, nil
}

// ProjectInput is the API's ProjectPluginUpdate.
type ProjectInput struct {
	Enabled          bool               `json:"enabled"`
	Values           map[string]any     `json:"values"`
	Secrets          map[string]*string `json:"secrets"`
	ExpectedRevision int64              `json:"expectedRevision"`
}

// PutProject saves whether the plugin is enabled in the project and its project-level config.
func (s *Service) PutProject(ctx context.Context, project uuid.UUID, id string, in ProjectInput) (ProjectView, error) {
	_, m, err := s.get(ctx, id)
	if err != nil {
		return ProjectView{}, err
	}
	fields := projectFields(m.ConfigSchema)
	secrets := secretFields(m.ConfigSchema)
	var projectSecrets []string
	for _, f := range secrets {
		if slices.Contains(fields, f) {
			projectSecrets = append(projectSecrets, f)
		}
	}
	if err := checkValueFields(in.Values, secrets, fields, "values"); err != nil {
		return ProjectView{}, err
	}
	global, err := s.Store.PluginConfig(ctx, id, nil)
	if err != nil {
		return ProjectView{}, err
	}
	row, _, err := s.Store.SaveProjectPlugin(ctx, project, id, in.Enabled, store.PluginConfigUpdate{ExpectedRevision: in.ExpectedRevision, Apply: func(cur store.PluginConfigRow) (store.PluginConfigRow, error) {
		next, err := mergeSecrets(cur.Secrets, in.Secrets, projectSecrets, "secrets")
		if err != nil {
			return cur, err
		}
		values := nonNilMap(in.Values)
		effective := combine(global.Values, global.Secrets)
		maps.Copy(effective, combine(values, next))
		if err := validateAgainst(m.ConfigSchema, false, effective, "values"); err != nil {
			return cur, err
		}
		return store.PluginConfigRow{Values: values, Secrets: next}, nil
	}})
	if err != nil {
		return ProjectView{}, err
	}
	s.configChanged(ctx, id)
	v, _, err := view(row.Plugin)
	return ProjectView{PluginID: v.ID, DisplayName: v.DisplayName, Version: v.Version, Status: v.Status, Enabled: row.Enabled, ProjectFields: fields, Config: configView(row.Config)}, err
}

// ---- Accounts ----

func accountKind(m Manifest, kind string) (AccountKind, bool) {
	for _, k := range m.Accounts {
		if k.Kind == kind {
			return k, true
		}
	}
	return AccountKind{}, false
}

func accountView(a store.PluginAccount, k AccountKind) AccountView {
	secrets := secretFields(k.Fields)
	values := map[string]any{}
	set := []string{}
	for f, v := range a.Values {
		if slices.Contains(secrets, f) {
			set = append(set, f)
		} else {
			values[f] = v
		}
	}
	slices.Sort(set)
	return AccountView{ID: a.ID, PluginID: a.PluginID, ProjectID: a.ProjectID, Kind: a.Kind, Name: a.Name, Values: values, SecretsSet: set, Revision: a.Revision}
}

// Accounts returns this plugin's accounts in the project.
func (s *Service) Accounts(ctx context.Context, project uuid.UUID, id string) ([]AccountView, error) {
	_, m, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err := s.Store.GetProject(ctx, project); err != nil {
		return nil, err
	}
	rows, err := s.Store.PluginAccounts(ctx, project, id)
	if err != nil {
		return nil, err
	}
	out := make([]AccountView, 0, len(rows))
	for _, a := range rows {
		k, _ := accountKind(m, a.Kind)
		out = append(out, accountView(a, k))
	}
	return out, nil
}

// AccountCreate is the API's PluginAccountCreate.
type AccountCreate struct {
	Kind    string            `json:"kind"`
	Name    string            `json:"name"`
	Values  map[string]any    `json:"values"`
	Secrets map[string]string `json:"secrets"`
}

// CreateAccount adds an account.
func (s *Service) CreateAccount(ctx context.Context, project uuid.UUID, id string, in AccountCreate) (AccountView, error) {
	_, m, err := s.get(ctx, id)
	if err != nil {
		return AccountView{}, err
	}
	k, ok := accountKind(m, in.Kind)
	if !ok {
		return AccountView{}, apperr.Invalid("kind", "The plugin does not declare this account kind.")
	}
	secrets := secretFields(k.Fields)
	if err := checkValueFields(in.Values, secrets, nil, "values"); err != nil {
		return AccountView{}, err
	}
	patch := map[string]*string{}
	for f, v := range in.Secrets {
		patch[f] = &v
	}
	sec, err := mergeSecrets(nil, patch, secrets, "secrets")
	if err != nil {
		return AccountView{}, err
	}
	all := combine(nonNilMap(in.Values), sec)
	if err := validateAgainst(k.Fields, true, all, "values"); err != nil {
		return AccountView{}, err
	}
	a, err := s.Store.CreatePluginAccount(ctx, store.PluginAccount{PluginID: id, ProjectID: project, Kind: in.Kind, Name: strings.TrimSpace(in.Name), Values: all}, !k.Multiple)
	if err != nil {
		return AccountView{}, err
	}
	return accountView(a, k), nil
}

// AccountUpdate is the API's PluginAccountUpdate.
type AccountUpdate struct {
	Name             string             `json:"name"`
	Values           map[string]any     `json:"values"`
	Secrets          map[string]*string `json:"secrets"`
	ExpectedRevision int64              `json:"expectedRevision"`
}

// UpdateAccount changes an account against the expected revision.
func (s *Service) UpdateAccount(ctx context.Context, project uuid.UUID, id string, account uuid.UUID, in AccountUpdate) (AccountView, error) {
	_, m, err := s.get(ctx, id)
	if err != nil {
		return AccountView{}, err
	}
	var kind AccountKind
	a, err := s.Store.UpdatePluginAccount(ctx, project, id, account, in.ExpectedRevision, func(cur store.PluginAccount) (store.PluginAccount, error) {
		k, ok := accountKind(m, cur.Kind)
		if !ok {
			return cur, apperr.Invalid("kind", "The plugin no longer declares this account kind.")
		}
		kind = k
		secrets := secretFields(k.Fields)
		if err := checkValueFields(in.Values, secrets, nil, "values"); err != nil {
			return cur, err
		}
		current := map[string]string{}
		for _, f := range secrets {
			if v, ok := cur.Values[f].(string); ok {
				current[f] = v
			}
		}
		sec, err := mergeSecrets(current, in.Secrets, secrets, "secrets")
		if err != nil {
			return cur, err
		}
		all := combine(nonNilMap(in.Values), sec)
		if err := validateAgainst(k.Fields, true, all, "values"); err != nil {
			return cur, err
		}
		cur.Name, cur.Values = strings.TrimSpace(in.Name), all
		return cur, nil
	})
	if err != nil {
		return AccountView{}, err
	}
	return accountView(a, kind), nil
}

// ---- Agent grants ----

// AgentPlugins returns the plugins section of the Agent config page.
func (s *Service) AgentPlugins(ctx context.Context, agent uuid.UUID) ([]AgentView, error) {
	a, err := s.Store.GetAgent(ctx, agent)
	if err != nil {
		return nil, err
	}
	grants, err := s.Store.AgentPluginGrants(ctx, agent)
	if err != nil {
		return nil, err
	}
	ps, err := s.Store.ListPlugins(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AgentView, 0, len(ps))
	for _, p := range ps {
		v, m, err := view(p)
		if err != nil {
			return nil, err
		}
		item := AgentView{PluginID: v.ID, DisplayName: v.DisplayName, Available: true, AccountIDs: []uuid.UUID{}, AccountKinds: m.Accounts}
		if a.ProjectID != nil {
			if item.Available, err = s.Store.PluginEnabledIn(ctx, p.ID, *a.ProjectID); err != nil {
				return nil, err
			}
		}
		for _, g := range grants {
			if g.PluginID == p.ID {
				item.Granted, item.AccountIDs = true, g.AccountIDs
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// AgentInput is the API's AgentPluginUpdate.
type AgentInput struct {
	Granted    bool        `json:"granted"`
	AccountIDs []uuid.UUID `json:"accountIds"`
}

// PutAgentPlugin grants or revokes the plugin and returns the saved item.
func (s *Service) PutAgentPlugin(ctx context.Context, agent uuid.UUID, id string, in AgentInput) (AgentView, error) {
	if err := s.Store.SaveAgentPluginGrant(ctx, agent, id, in.Granted, in.AccountIDs); err != nil {
		return AgentView{}, err
	}
	items, err := s.AgentPlugins(ctx, agent)
	if err != nil {
		return AgentView{}, err
	}
	for _, it := range items {
		if it.PluginID == id {
			return it, nil
		}
	}
	return AgentView{}, apperr.NotFound("Plugin")
}

// ---- Settings and plugin host ----

// Settings returns the global settings.
func (s *Service) Settings(ctx context.Context) (store.PluginSettings, error) {
	return s.Store.GetPluginSettings(ctx)
}

// PutSettings saves the global settings; the "Disable all" switch stops or resumes the plugin host immediately.
func (s *Service) PutSettings(ctx context.Context, v store.PluginSettings) (store.PluginSettings, error) {
	for _, m := range v.Markets {
		if strings.TrimSpace(m) == "" {
			return v, apperr.Invalid("markets", "A market URL must not be empty.")
		}
	}
	if err := s.Store.PutPluginSettings(ctx, v); err != nil {
		return v, err
	}
	if s.Host != nil {
		s.Host.SetDisabled(v.Disabled)
	}
	return s.Store.GetPluginSettings(ctx)
}

// HostStatus returns the plugin host status and today's delivery results.
func (s *Service) HostStatus(ctx context.Context) (HostView, error) {
	out := HostView{State: pluginhost.StateIdle, RecentExits: []time.Time{}, Loaded: []string{}}
	if s.Host != nil {
		st := s.Host.Status()
		out.Configured, out.State, out.RecentExits, out.LastCrash, out.Loaded = true, st.State, st.RecentExits, st.LastCrash, st.Loaded
		if st.PID != 0 {
			pid := st.PID
			out.PID = &pid
		}
		out.StartedAt = st.StartedAt
		if st.HostVersion != "" {
			v := st.HostVersion
			out.HostVersion = &v
		}
	}
	if out.RecentExits == nil {
		out.RecentExits = []time.Time{}
	}
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	var err error
	out.Today.Delivered, out.Today.Failed, err = s.Store.PluginDeliveryCounts(ctx, midnight)
	return out, err
}

// RestartHost restarts the plugin host on manual request.
func (s *Service) RestartHost(ctx context.Context) (HostView, error) {
	if s.Host == nil {
		return HostView{}, apperr.Conflict(apperr.CodePluginNotEnabled, "The server has no plugin host configured.")
	}
	st, err := s.Store.GetPluginSettings(ctx)
	if err != nil {
		return HostView{}, err
	}
	if st.Disabled {
		return HostView{}, apperr.Conflict(apperr.CodePluginNotEnabled, "All plugins are disabled. Turn them back on in the global settings first.")
	}
	s.Host.Restart()
	return s.HostStatus(ctx)
}

// ---- Plugins for the plugin host to load ----

type loadManifest struct {
	Events       []string              `json:"events"`
	Permissions  []string              `json:"permissions"`
	FailClosed   bool                  `json:"failClosed"`
	ConfigSchema pluginmanifest.Schema `json:"configSchema"`
	Accounts     []AccountKind         `json:"accounts"`
	Actions      []Action              `json:"actions"`
	Channels     []Channel             `json:"channels"`
	Schedules    []Schedule            `json:"schedules"`
	HTTP         bool                  `json:"http"`
}

type loadPayload struct {
	ID       string        `json:"id"`
	Version  string        `json:"version"`
	Root     string        `json:"root"`
	Entry    string        `json:"entry"`
	DataDir  string        `json:"dataDir"`
	Manifest loadManifest  `json:"manifest"`
	Config   configPayload `json:"config"`
	Token    string        `json:"token"`
	APIURL   string        `json:"apiUrl"`
}

// Specs returns the plugins that have a platform side, sorted by install time (the order in which the plugin host
// loads them and makes synchronous calls). Each plugin's load payload is only assembled right before loading; at that
// point a new plugin token is issued and the old one revoked.
func (s *Service) Specs(ctx context.Context) ([]pluginhost.Spec, error) {
	ps, err := s.Store.ListPlugins(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(ps, func(a, b store.Plugin) int { return a.InstalledAt.Compare(b.InstalledAt) })
	var out []pluginhost.Spec
	for _, p := range ps {
		_, m, err := view(p)
		if err != nil {
			return nil, err
		}
		if m.Platform == "" {
			continue
		}
		p := p
		kinds := []string{}
		for _, a := range m.Accounts {
			kinds = append(kinds, a.Kind)
		}
		out = append(out, pluginhost.Spec{ID: p.ID, Events: m.Events, Permissions: m.Permissions, FailClosed: m.FailClosed, Schedules: m.Components.Schedules, AccountKinds: kinds,
			Prepare: func(ctx context.Context) (json.RawMessage, error) { return s.loadPayload(ctx, p, m) }})
	}
	return out, nil
}

func (s *Service) loadPayload(ctx context.Context, p store.Plugin, m Manifest) (json.RawMessage, error) {
	cfg, err := s.configPayload(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	tok, err := auth.NewToken(auth.PluginPrefix)
	if err != nil {
		return nil, err
	}
	if err := s.Store.IssuePluginToken(ctx, p.ID, tok.ID, tok.Hash, m.Permissions); err != nil {
		return nil, err
	}
	payload := loadPayload{ID: p.ID, Version: p.Version, Root: p.InstallDir, Entry: filepath.Join(p.InstallDir, filepath.Clean(m.Platform)), DataDir: s.dataDir(p.ID),
		Manifest: loadManifest{Events: m.Events, Permissions: m.Permissions, FailClosed: m.FailClosed, ConfigSchema: m.ConfigSchema, Accounts: m.Accounts,
			Actions: m.Components.Actions, Channels: m.Components.Channels, Schedules: m.Components.Schedules, HTTP: m.Components.HTTP},
		Config: cfg, Token: tok.Plain, APIURL: s.APIURL}
	return json.Marshal(payload)
}

// Hook hands a synchronous event to the plugins in the plugin host that subscribe to it (plugins/SPEC.md §3): an
// event inside a project only goes to plugins enabled in project. Without a plugin host it returns an empty result
// and the operation proceeds as usual.
func (s *Service) Hook(ctx context.Context, kind pluginhost.Kind, event map[string]any, project *uuid.UUID) (pluginhost.Outcome, error) {
	if s == nil || s.Host == nil {
		return pluginhost.Outcome{}, nil
	}
	var include func(string) bool
	if project != nil {
		include = func(id string) bool {
			on, err := s.Store.PluginEnabledIn(ctx, id, *project)
			if err != nil {
				s.Log.Warn("failed to read plugin enablement", "plugin", id, "err", err)
			}
			return on
		}
	}
	return s.Host.Call(ctx, kind, event, include)
}

// WakeOnCommit makes a transaction that wrote plugin events wake the delivery goroutine as soon as it commits
// (previously done with PostgreSQL's LISTEN/NOTIFY), and wakes it once right away for events written before delivery
// started. Redelivery is driven by the delivery goroutine's own periodic polling.
func (s *Service) WakeOnCommit() {
	if s.Host == nil {
		return
	}
	s.Store.SetPluginEventsCommitted(s.Host.Notify)
	s.Host.Notify()
}

func nonNilMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func nonNilSecrets(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
