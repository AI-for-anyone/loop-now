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
	"loop-now/internal/server/pluginhost"
	"loop-now/internal/server/store"
)

// Service 管理已安装的插件。安装、更新与卸载串行执行。
type Service struct {
	Store *store.Store
	// Host 是宿主监护；服务端没有宿主入口时为空，平台半边不运行。
	Host *pluginhost.Supervisor
	// Root 是 <work root>/platform/plugins：安装目录 <Root>/<id>/<version>/，数据目录 <Root>/<id>/data/。
	Root string
	// OfficialDir 是随平台发布的官方插件目录（每个子目录一个插件）；为空时没有 builtin 来源。
	OfficialDir string
	// OfficialMarket 是官方市场的 loop-now-market.json 地址，为空时只有设置里的市场；MarketClient 读取市场（默认 http.DefaultClient）。
	OfficialMarket  string
	MarketClient    *http.Client
	NodeBin, GitBin string
	// APIURL 是插件令牌调用的 /api/v1 地址（回环监听器）。
	APIURL          string
	PlatformVersion string
	FetchTimeout    time.Duration
	PreviewTTL      time.Duration
	// RequestTimeout 是 action 与 http 请求等待插件回应的时长，默认 30 秒。
	RequestTimeout time.Duration
	Log            *slog.Logger

	mu       sync.Mutex
	previews map[uuid.UUID]*preview
	limiter  rateLimiter
}

type preview struct {
	view    Preview
	dir     string // 插件目录：暂存区里的副本，local 来源为原目录
	staging string // 暂存目录，local 来源为空
	m       Manifest
}

// Init 清理上次运行留下的暂存目录。
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

// ---- 接口里的结构 ----

// View 是接口里的 Plugin。
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

// Detail 是接口里的 PluginDetail。
type Detail struct {
	Plugin     View           `json:"plugin"`
	Manifest   PublicManifest `json:"manifest"`
	Versions   []VersionView  `json:"versions"`
	InstallDir string         `json:"installDir"`
	DataDir    string         `json:"dataDir"`
}

// VersionView 是接口里的 PluginVersion。
type VersionView struct {
	Version     string    `json:"version"`
	Digest      string    `json:"digest"`
	InstalledAt time.Time `json:"installedAt"`
	Current     bool      `json:"current"`
}

// Preview 是接口里的 PluginPreview。
type Preview struct {
	PreviewID        uuid.UUID      `json:"previewId"`
	ExpiresAt        time.Time      `json:"expiresAt"`
	Source           ResolvedSource `json:"source"`
	Trusted          bool           `json:"trusted"`
	Manifest         PublicManifest `json:"manifest"`
	InstalledVersion *string        `json:"installedVersion"`
}

// ConfigView 是接口里的 PluginConfig。
type ConfigView struct {
	Values     map[string]any `json:"values"`
	SecretsSet []string       `json:"secretsSet"`
	Revision   int64          `json:"revision"`
}

// ConfigInput 是接口里的 PluginConfigUpdate。
type ConfigInput struct {
	Values           map[string]any     `json:"values"`
	Secrets          map[string]*string `json:"secrets"`
	ExpectedRevision int64              `json:"expectedRevision"`
}

// ProjectView 是接口里的 ProjectPlugin。
type ProjectView struct {
	PluginID      string     `json:"pluginId"`
	DisplayName   Text       `json:"displayName"`
	Version       string     `json:"version"`
	Status        string     `json:"status"`
	Enabled       bool       `json:"enabled"`
	ProjectFields []string   `json:"projectFields"`
	Config        ConfigView `json:"config"`
}

// AccountView 是接口里的 PluginAccount。
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

// AgentView 是接口里的 AgentPlugin。
type AgentView struct {
	PluginID     string        `json:"pluginId"`
	DisplayName  Text          `json:"displayName"`
	Available    bool          `json:"available"`
	Granted      bool          `json:"granted"`
	AccountIDs   []uuid.UUID   `json:"accountIds"`
	AccountKinds []AccountKind `json:"accountKinds"`
}

// HostView 是接口里的 PluginHostStatus。
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

// BuiltinSource 是接口里的 BuiltinPluginSource。
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

// get 读取插件与它的清单。
func (s *Service) get(ctx context.Context, id string) (View, Manifest, error) {
	p, err := s.Store.GetPlugin(ctx, id)
	if err != nil {
		return View{}, Manifest{}, err
	}
	return view(p)
}

// ---- 代管外部身份 ----

// SetIdentityManager 设置或清空代管外部身份的插件渠道（plugins/SPEC.md §12）：插件须已安装，渠道须在清单里声明 manageIdentities，否则 422。
func (s *Service) SetIdentityManager(ctx context.Context, identity uuid.UUID, m *store.IdentityManager) (store.ExternalIdentity, error) {
	if m != nil {
		_, manifest, err := s.get(ctx, m.PluginID)
		if store.IsNotFound(err) {
			return store.ExternalIdentity{}, apperr.Invalid("managedBy.pluginId", "插件 "+m.PluginID+" 没有安装")
		}
		if err != nil {
			return store.ExternalIdentity{}, err
		}
		if !slices.Contains(manifest.ManagingChannels(), m.ChannelID) {
			return store.ExternalIdentity{}, apperr.Invalid("managedBy.channelId", "插件 "+m.PluginID+" 的渠道 "+m.ChannelID+" 没有声明能代管外部身份")
		}
	}
	return s.Store.SetIdentityManager(ctx, identity, m)
}

// ---- 安装、更新、卸载 ----

// List 返回全部已安装的插件。
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

// Get 返回插件详情。
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
	d := Detail{Plugin: v, Manifest: m.Public(), Versions: []VersionView{}, InstallDir: p.InstallDir, DataDir: s.dataDir(id)}
	for _, ver := range versions {
		d.Versions = append(d.Versions, VersionView{Version: ver.Version, Digest: ver.Digest, InstalledAt: ver.InstalledAt, Current: ver.Version == p.Version})
	}
	return d, nil
}

func (s *Service) dataDir(id string) string { return filepath.Join(s.Root, id, "data") }

// PreviewRequest 是接口里的 PluginPreviewRequest。
type PreviewRequest struct {
	Source   *Source `json:"source"`
	PluginID string  `json:"pluginId"`
}

// Preview 取来插件并读出清单，暂存到安装或更新为止（PreviewTTL 内有效）。
func (s *Service) Preview(ctx context.Context, req PreviewRequest) (Preview, error) {
	s.expirePreviews()
	if (req.Source == nil) == (req.PluginID == "") {
		return Preview{}, apperr.Invalid("source", "source 与 pluginId 须给出且只给出一个")
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
	switch {
	case src.Kind == SourceMarket:
		var err error
		if fetchSrc, trusted, err = s.resolveMarket(ctx, *src); err != nil {
			return Preview{}, err
		}
		market = &src.Market
	case src.Location == "":
		return Preview{}, apperr.Invalid("source.location", "须给出来源位置")
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
			switch {
			case req.PluginID != "" && m.ID != req.PluginID:
				err = apperr.Invalid("manifest", "来源里的插件 id 已变为 "+m.ID+"，不能作为 "+req.PluginID+" 的更新")
			case market != nil && m.ID != src.ID:
				err = apperr.Invalid("manifest", "市场条目 "+src.ID+" 的来源里是插件 "+m.ID)
			default:
				return s.keepPreview(ctx, id, staging, dir, resolved, m, trusted)
			}
		}
	}
	_ = os.RemoveAll(staging)
	return Preview{}, err
}

func (s *Service) keepPreview(ctx context.Context, id uuid.UUID, staging, dir string, resolved ResolvedSource, m Manifest, trusted bool) (Preview, error) {
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
	s.previews[id] = &preview{view: pv, dir: dir, staging: staging, m: m}
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

// InstallInput 是接口里的 PluginInstall。
type InstallInput struct {
	PreviewID uuid.UUID `json:"previewId"`
	Confirm   bool      `json:"confirm"`
}

// Install 按预览安装新插件；update 不为空时是对已安装插件 update 的更新。
// 预览用过即失效；只有缺少确认时保留，便于确认后重试。
func (s *Service) Install(ctx context.Context, in InstallInput, update string) (View, error) {
	s.expirePreviews()
	s.mu.Lock()
	defer s.mu.Unlock()
	pv := s.previews[in.PreviewID]
	if pv == nil {
		return View{}, apperr.NotFound("预览")
	}
	if !pv.view.Trusted && !in.Confirm {
		return View{}, apperr.Conflict(apperr.CodePluginConfirmationRequired, "插件来自非官方来源，确认后再安装")
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
		return View{}, apperr.Conflict(apperr.CodeNameConflict, "插件 "+m.ID+" 已安装，请用更新")
	case update != "" && update != m.ID:
		return View{}, apperr.Invalid("previewId", "预览的插件是 "+m.ID+"，不是 "+update)
	case update != "" && err != nil:
		return View{}, err
	}
	sum, err := digest(pv.dir)
	if err != nil {
		return View{}, err
	}
	installDir := pv.dir
	if pv.view.Source.Kind != SourceLocal {
		target := filepath.Join(s.Root, m.ID, m.Version)
		reuse := false
		if update != "" {
			versions, err := s.Store.PluginVersions(ctx, m.ID)
			if err != nil {
				return View{}, err
			}
			for _, v := range versions {
				if v.Version == m.Version && v.Digest != sum {
					return View{}, apperr.Conflict(apperr.CodePluginVersionConflict, "插件 "+m.ID+" 已装过版本 "+m.Version+"，内容不同；请改版本号")
				}
				reuse = reuse || v.Version == m.Version
			}
		}
		if !reuse {
			if pv.view.Source.Kind == SourceGit || pv.view.Source.Kind == SourceNPM {
				if err := s.installDeps(ctx, pv.dir); err != nil {
					return View{}, err
				}
			}
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
	row := store.Plugin{ID: m.ID, DisplayName: m.DisplayName.String(), Source: source, Version: m.Version, Manifest: manifest, InstallDir: installDir}
	if update == "" {
		err = s.Store.InstallPlugin(ctx, row, sum)
	} else {
		err = s.Store.UpdatePlugin(ctx, row, sum)
		if err == nil {
			// 新版本不再声明代管的渠道，它上面的外部身份绑定清空（plugins/SPEC.md §12）。
			err = s.Store.PruneIdentityManagers(ctx, m.ID, m.ManagingChannels())
		}
	}
	if err != nil {
		return View{}, err
	}
	s.reloadHost(ctx, m.ID, m.Platform != "" || (update != "" && existing.ID != ""))
	v, _, err := s.get(ctx, m.ID)
	return v, err
}

// reloadHost 让宿主按最新的插件列表重新加载（或卸载）一个插件；宿主不在时下次启动生效。
func (s *Service) reloadHost(ctx context.Context, id string, touched bool) {
	if s.Host == nil || !touched {
		return
	}
	if err := s.Host.Reload(ctx, id); err != nil {
		s.Log.Warn("宿主重新加载插件失败", "plugin", id, "err", err)
	}
}

// Uninstall 卸载插件：宿主内卸载，删除数据库记录与各版本的安装目录（local 来源的目录不删）；deleteData 时删数据目录。
func (s *Service) Uninstall(ctx context.Context, id string, deleteData bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.Store.GetPlugin(ctx, id)
	if err != nil {
		return err
	}
	var src ResolvedSource
	_ = json.Unmarshal(p.Source, &src)
	if s.Host != nil {
		if err := s.Host.Unload(ctx, id); err != nil {
			s.Log.Warn("宿主卸载插件失败", "plugin", id, "err", err)
		}
	}
	dirs, err := s.Store.DeletePlugin(ctx, id)
	if err != nil {
		return err
	}
	for _, d := range dirs {
		// local 来源原地使用，目录属于作者；只删平台自己的安装目录。
		if strings.HasPrefix(d, filepath.Join(s.Root, id)+string(filepath.Separator)) {
			if err := os.RemoveAll(d); err != nil {
				s.Log.Warn("删除插件安装目录失败", "plugin", id, "dir", d, "err", err)
			}
		}
	}
	if deleteData {
		if err := os.RemoveAll(s.dataDir(id)); err != nil {
			return err
		}
	}
	_ = os.Remove(filepath.Join(s.Root, id)) // 只在已空时删除
	return nil
}

// BuiltinSources 列出官方插件目录里的插件；清单不合规则的跳过并记日志。
func (s *Service) BuiltinSources() []BuiltinSource {
	out := []BuiltinSource{}
	if s.OfficialDir == "" {
		return out
	}
	entries, err := os.ReadDir(s.OfficialDir)
	if err != nil {
		s.Log.Warn("读取官方插件目录失败", "dir", s.OfficialDir, "err", err)
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := ReadManifest(filepath.Join(s.OfficialDir, e.Name()), s.PlatformVersion)
		if err != nil || m.ID != e.Name() {
			s.Log.Warn("跳过官方插件目录中的条目", "name", e.Name(), "err", err)
			continue
		}
		out = append(out, BuiltinSource{ID: m.ID, DisplayName: m.DisplayName, Description: m.Description, Version: m.Version})
	}
	return out
}

// ---- 配置 ----

func configView(c store.PluginConfigRow) ConfigView {
	values := c.Values
	if values == nil {
		values = map[string]any{}
	}
	return ConfigView{Values: values, SecretsSet: secretsSet(c.Secrets), Revision: c.Revision}
}

// Config 返回全局配置。
func (s *Service) Config(ctx context.Context, id string) (ConfigView, error) {
	if _, _, err := s.get(ctx, id); err != nil {
		return ConfigView{}, err
	}
	c, err := s.Store.PluginConfig(ctx, id, nil)
	return configView(c), err
}

// PutConfig 按版本保存全局配置，并把新配置交给宿主内的插件。
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

// configChanged 把插件的全部配置重新交给宿主内的插件；插件不在宿主里时不做事。
func (s *Service) configChanged(ctx context.Context, id string) {
	if s.Host == nil {
		return
	}
	cfg, err := s.configPayload(ctx, id)
	if err == nil {
		err = s.Host.ConfigChanged(ctx, id, map[string]any{"config": cfg})
	}
	if err != nil && !errors.Is(err, pluginhost.ErrNotLoaded) {
		s.Log.Warn("通知插件配置变化失败", "plugin", id, "err", err)
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

// ---- 项目 ----

// ProjectPlugins 返回项目插件页的列表。
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

// ProjectInput 是接口里的 ProjectPluginUpdate。
type ProjectInput struct {
	Enabled          bool               `json:"enabled"`
	Values           map[string]any     `json:"values"`
	Secrets          map[string]*string `json:"secrets"`
	ExpectedRevision int64              `json:"expectedRevision"`
}

// PutProject 保存项目的启用状态与项目级配置。
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

// ---- 账号 ----

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

// Accounts 返回项目里这个插件的账号。
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

// AccountCreate 是接口里的 PluginAccountCreate。
type AccountCreate struct {
	Kind    string            `json:"kind"`
	Name    string            `json:"name"`
	Values  map[string]any    `json:"values"`
	Secrets map[string]string `json:"secrets"`
}

// CreateAccount 添加账号。
func (s *Service) CreateAccount(ctx context.Context, project uuid.UUID, id string, in AccountCreate) (AccountView, error) {
	_, m, err := s.get(ctx, id)
	if err != nil {
		return AccountView{}, err
	}
	k, ok := accountKind(m, in.Kind)
	if !ok {
		return AccountView{}, apperr.Invalid("kind", "插件没有声明这个账号种类")
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

// AccountUpdate 是接口里的 PluginAccountUpdate。
type AccountUpdate struct {
	Name             string             `json:"name"`
	Values           map[string]any     `json:"values"`
	Secrets          map[string]*string `json:"secrets"`
	ExpectedRevision int64              `json:"expectedRevision"`
}

// UpdateAccount 按版本修改账号。
func (s *Service) UpdateAccount(ctx context.Context, project uuid.UUID, id string, account uuid.UUID, in AccountUpdate) (AccountView, error) {
	_, m, err := s.get(ctx, id)
	if err != nil {
		return AccountView{}, err
	}
	var kind AccountKind
	a, err := s.Store.UpdatePluginAccount(ctx, project, id, account, in.ExpectedRevision, func(cur store.PluginAccount) (store.PluginAccount, error) {
		k, ok := accountKind(m, cur.Kind)
		if !ok {
			return cur, apperr.Invalid("kind", "插件已不再声明这个账号种类")
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

// ---- Agent 授权 ----

// AgentPlugins 返回 Agent 配置页的插件段。
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

// AgentInput 是接口里的 AgentPluginUpdate。
type AgentInput struct {
	Granted    bool        `json:"granted"`
	AccountIDs []uuid.UUID `json:"accountIds"`
}

// PutAgentPlugin 授权或取消授权，返回保存后的这一项。
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
	return AgentView{}, apperr.NotFound("插件")
}

// ---- 设置与宿主 ----

// Settings 返回全局设置。
func (s *Service) Settings(ctx context.Context) (store.PluginSettings, error) {
	return s.Store.GetPluginSettings(ctx)
}

// PutSettings 保存全局设置；一键停用立即停止或恢复宿主。
func (s *Service) PutSettings(ctx context.Context, v store.PluginSettings) (store.PluginSettings, error) {
	for _, m := range v.Markets {
		if strings.TrimSpace(m) == "" {
			return v, apperr.Invalid("markets", "市场地址不能为空")
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

// HostStatus 返回宿主状态与今天的投递结果。
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

// RestartHost 人工重新启动宿主。
func (s *Service) RestartHost(ctx context.Context) (HostView, error) {
	if s.Host == nil {
		return HostView{}, apperr.Conflict(apperr.CodePluginNotEnabled, "服务端没有配置插件宿主")
	}
	st, err := s.Store.GetPluginSettings(ctx)
	if err != nil {
		return HostView{}, err
	}
	if st.Disabled {
		return HostView{}, apperr.Conflict(apperr.CodePluginNotEnabled, "已停用全部插件，先在全局设置里恢复")
	}
	s.Host.Restart()
	return s.HostStatus(ctx)
}

// ---- 宿主要加载的插件 ----

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

// Specs 返回有平台半边的插件，按安装时间排序（宿主加载与同步调用的顺序）。
// 每个插件的 load 载荷在加载前才组装，这时签发新的插件令牌并吊销旧的。
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

// Hook 把同步事件交给宿主里订阅了它的插件（plugins/SPEC.md §3）：项目内的事件只交给在 project 启用了插件的；
// 没有宿主时返回空结果，操作照常进行。
func (s *Service) Hook(ctx context.Context, kind pluginhost.Kind, event map[string]any, project *uuid.UUID) (pluginhost.Outcome, error) {
	if s == nil || s.Host == nil {
		return pluginhost.Outcome{}, nil
	}
	var include func(string) bool
	if project != nil {
		include = func(id string) bool {
			on, err := s.Store.PluginEnabledIn(ctx, id, *project)
			if err != nil {
				s.Log.Warn("读取插件启用状态失败", "plugin", id, "err", err)
			}
			return on
		}
	}
	return s.Host.Call(ctx, kind, event, include)
}

// Listen 等待数据库里新事件的通知，唤醒投递协程；ctx 结束时返回。
func (s *Service) Listen(ctx context.Context) {
	if s.Host == nil {
		return
	}
	for ctx.Err() == nil {
		if err := s.listenOnce(ctx); err != nil && ctx.Err() == nil {
			s.Log.Warn("监听插件事件通知失败，稍后重试", "err", err)
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
			}
		}
	}
}

func (s *Service) listenOnce(ctx context.Context) error {
	pc, err := s.Store.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	conn := pc.Hijack()
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, `LISTEN loop_now_plugin_events`); err != nil {
		return err
	}
	// 连上之前可能已有事件写入：先唤醒一次。
	s.Host.Notify()
	for {
		if _, err := conn.WaitForNotification(ctx); err != nil {
			return err
		}
		s.Host.Notify()
	}
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
