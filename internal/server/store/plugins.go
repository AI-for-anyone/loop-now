package store

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/db"
)

// Installation, configuration, enablement, grants, accounts, tokens and global settings for version 2 plugins
// (plugins/SPEC.md §6, §8).

// Plugin is an installed plugin. Manifest is the manifest stored by the plugin service (a JSON object).
type Plugin struct {
	ID              string
	DisplayName     string
	Source          json.RawMessage
	Version         string
	Manifest        json.RawMessage
	InstallDir      string
	Status          string
	LastError       *string
	InstalledAt     time.Time
	UpdatedAt       time.Time
	EnabledProjects int
	GrantedAgents   int
}

// PluginVersion is one version that has been installed.
type PluginVersion struct {
	Version     string
	Manifest    json.RawMessage
	InstallDir  string
	Digest      string
	InstalledAt time.Time
	// Providers are the model providers read at install time (JSON of modelcatalog.Capture); ProvidersError is the
	// reason the whole read failed.
	Providers      json.RawMessage
	ProvidersError *string
	// ProviderState is the effective providerState declaration (a JSON object).
	ProviderState json.RawMessage
}

const pluginColumns = `p.id,p.display_name,p.source,p.version,p.manifest,p.install_dir,p.status,p.last_error,p.installed_at,p.updated_at,
	(SELECT count(*) FROM project_plugins pp JOIN projects pr ON pr.id=pp.project_id WHERE pp.plugin_id=p.id AND pp.enabled AND pr.deleted_at IS NULL),
	(SELECT count(*) FROM agent_plugin_grants g JOIN agents a ON a.id=g.agent_id WHERE g.plugin_id=p.id AND a.deleted_at IS NULL)`

func scanPlugin(r db.Scanner) (Plugin, error) {
	var p Plugin
	err := r.Scan(&p.ID, &p.DisplayName, &p.Source, &p.Version, &p.Manifest, &p.InstallDir, &p.Status, &p.LastError, &p.InstalledAt, &p.UpdatedAt, &p.EnabledProjects, &p.GrantedAgents)
	return p, err
}

// ListPlugins returns all installed plugins by id.
func (s *Store) ListPlugins(ctx context.Context) ([]Plugin, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+pluginColumns+` FROM plugins p ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(r db.CollectableRow) (Plugin, error) { return scanPlugin(r) })
}

// GetPlugin returns a plugin; returns not_found when it does not exist.
func (s *Store) GetPlugin(ctx context.Context, id string) (Plugin, error) {
	p, err := scanPlugin(s.Pool.QueryRow(ctx, `SELECT `+pluginColumns+` FROM plugins p WHERE p.id=$1`, id))
	return p, notFoundIfNoRows(err, "插件")
}

// PluginVersions returns the versions a plugin has installed, newest first.
func (s *Store) PluginVersions(ctx context.Context, id string) ([]PluginVersion, error) {
	rows, err := s.Pool.Query(ctx, `SELECT version,manifest,install_dir,digest,installed_at,providers,providers_error,provider_state FROM plugin_versions WHERE plugin_id=$1 ORDER BY installed_at DESC, version DESC`, id)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(r db.CollectableRow) (PluginVersion, error) {
		var v PluginVersion
		return v, r.Scan(&v.Version, &v.Manifest, &v.InstallDir, &v.Digest, &v.InstalledAt, &v.Providers, &v.ProvidersError, &v.ProviderState)
	})
}

// PluginInstallDirs returns the install directories of all versions of each installed plugin (plugin id ->
// directories), used to tell which plugin a file belongs to.
func (s *Store) PluginInstallDirs(ctx context.Context) (map[string][]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT plugin_id,install_dir FROM plugin_versions ORDER BY plugin_id`)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for rows.Next() {
		var id, dir string
		if err := rows.Scan(&id, &dir); err != nil {
			rows.Close()
			return nil, err
		}
		out[id] = append(out[id], dir)
	}
	rows.Close()
	return out, rows.Err()
}

// PluginProviders are the model providers read from the current version of an installed plugin.
type PluginProviders struct {
	PluginID       string
	Providers      json.RawMessage
	ProvidersError *string
	ProviderState  json.RawMessage
}

// CurrentPluginProviders returns the model providers read from the current version of each installed plugin, sorted
// by plugin id.
func (s *Store) CurrentPluginProviders(ctx context.Context) ([]PluginProviders, error) {
	rows, err := s.Pool.Query(ctx, `SELECT p.id,v.providers,v.providers_error,v.provider_state FROM plugins p JOIN plugin_versions v ON v.plugin_id=p.id AND v.version=p.version ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(r db.CollectableRow) (PluginProviders, error) {
		var v PluginProviders
		return v, r.Scan(&v.PluginID, &v.Providers, &v.ProvidersError, &v.ProviderState)
	})
}

// PluginVersionExtras are the things stored with a version that are only obtained at install time
// (plugins/SPEC.md §13): the model providers read (JSON of modelcatalog.Capture), the reason the whole read failed,
// and the effective providerState declaration (a JSON object).
type PluginVersionExtras struct {
	Providers      json.RawMessage
	ProvidersError *string
	ProviderState  json.RawMessage
}

func (x PluginVersionExtras) values() (json.RawMessage, *string, json.RawMessage) {
	providers, state := x.Providers, x.ProviderState
	if len(providers) == 0 {
		providers = json.RawMessage(`{"providers":[],"errors":[]}`)
	}
	if len(state) == 0 {
		state = json.RawMessage(`{}`)
	}
	return providers, x.ProvidersError, state
}

// InstallPlugin registers a newly installed plugin and its first version; returns name_conflict when the id already
// exists.
func (s *Store) InstallPlugin(ctx context.Context, p Plugin, digest string, extra PluginVersionExtras) error {
	providers, providersError, state := extra.values()
	err := s.inTx(ctx, func(tx *db.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO plugins(id,display_name,source,version,manifest,install_dir,status) VALUES($1,$2,$3,$4,$5,$6,'installed')`,
			p.ID, p.DisplayName, p.Source, p.Version, p.Manifest, p.InstallDir); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO plugin_versions(plugin_id,version,manifest,install_dir,digest,providers,providers_error,provider_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, p.ID, p.Version, p.Manifest, p.InstallDir, digest, providers, providersError, state)
		return err
	})
	if isUniqueViolation(err, "plugins_pkey") {
		return apperr.Conflict(apperr.CodeNameConflict, "插件 "+p.ID+" 已安装，请用更新")
	}
	return err
}

// UpdatePlugin switches to a new version: returns plugin_version_conflict when the same version number is already
// installed with different content, and switches straight back when the content is the same.
func (s *Store) UpdatePlugin(ctx context.Context, p Plugin, digest string, extra PluginVersionExtras) error {
	providers, providersError, state := extra.values()
	return s.inTx(ctx, func(tx *db.Tx) error {
		var existing string
		err := tx.QueryRow(ctx, `SELECT digest FROM plugin_versions WHERE plugin_id=$1 AND version=$2`, p.ID, p.Version).Scan(&existing)
		switch {
		case errors.Is(err, db.ErrNoRows):
			if _, err = tx.Exec(ctx, `INSERT INTO plugin_versions(plugin_id,version,manifest,install_dir,digest,providers,providers_error,provider_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, p.ID, p.Version, p.Manifest, p.InstallDir, digest, providers, providersError, state); err != nil {
				return err
			}
		case err != nil:
			return err
		case existing != digest:
			return apperr.Conflict(apperr.CodePluginVersionConflict, "插件 "+p.ID+" 已装过版本 "+p.Version+"，内容不同；请改版本号")
		default:
			if _, err = tx.Exec(ctx, `UPDATE plugin_versions SET manifest=$3, install_dir=$4, providers=$5, providers_error=$6, provider_state=$7 WHERE plugin_id=$1 AND version=$2`, p.ID, p.Version, p.Manifest, p.InstallDir, providers, providersError, state); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, `UPDATE plugins SET display_name=$2, source=$3, version=$4, manifest=$5, install_dir=$6, updated_at=now() WHERE id=$1`,
			p.ID, p.DisplayName, p.Source, p.Version, p.Manifest, p.InstallDir)
		if err == nil && tag.RowsAffected() == 0 {
			return apperr.NotFound("插件")
		}
		return err
	})
}

// DeletePlugin deletes a plugin with its configuration, enablement, grants, accounts, state, deliveries and tokens,
// and returns the install directories of its versions.
func (s *Store) DeletePlugin(ctx context.Context, id string) ([]string, error) {
	var dirs []string
	err := s.inTx(ctx, func(tx *db.Tx) error {
		rows, err := tx.Query(ctx, `SELECT install_dir FROM plugin_versions WHERE plugin_id=$1`, id)
		if err != nil {
			return err
		}
		if dirs, err = db.CollectRows(rows, db.RowTo[string]); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM plugins WHERE id=$1`, id)
		if err == nil && tag.RowsAffected() == 0 {
			return apperr.NotFound("插件")
		}
		return err
	})
	return dirs, err
}

// PluginConfigRow is the global or a project's configuration; a Revision of 0 means it has never been saved.
type PluginConfigRow struct {
	Values   map[string]any
	Secrets  map[string]string
	Revision int64
}

// PluginConfig reads the global (nil project) or project-level configuration.
func (s *Store) PluginConfig(ctx context.Context, plugin string, project *uuid.UUID) (PluginConfigRow, error) {
	return pluginConfigTx(ctx, s.Pool, plugin, project)
}

type queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) *db.Row
}

// pluginConfigTx reads the plugin's global (nil project) or project configuration; returns an empty configuration
// when there is none.
func pluginConfigTx(ctx context.Context, q queryer, plugin string, project *uuid.UUID) (PluginConfigRow, error) {
	c := PluginConfigRow{Values: map[string]any{}, Secrets: map[string]string{}}
	err := q.QueryRow(ctx, `SELECT "values",secrets,revision FROM plugin_config WHERE plugin_id=$1 AND project_id IS $2`, plugin, project).
		Scan(&c.Values, &c.Secrets, &c.Revision)
	if errors.Is(err, db.ErrNoRows) {
		return c, nil
	}
	return c, err
}

// PluginConfigUpdate is one configuration save: Apply computes, inside the write transaction, the value to save from
// the current configuration; nothing is saved when it returns an error.
type PluginConfigUpdate struct {
	ExpectedRevision int64
	Apply            func(current PluginConfigRow) (PluginConfigRow, error)
}

// SavePluginConfig saves the configuration by revision: returns 412 when the revision does not match. Returns the
// configuration after saving.
func (s *Store) SavePluginConfig(ctx context.Context, plugin string, project *uuid.UUID, u PluginConfigUpdate) (PluginConfigRow, error) {
	var out PluginConfigRow
	err := s.inTx(ctx, func(tx *db.Tx) error {
		var err error
		out, err = s.savePluginConfigTx(ctx, tx, plugin, project, u)
		return err
	})
	return out, err
}

func (s *Store) savePluginConfigTx(ctx context.Context, tx *db.Tx, plugin string, project *uuid.UUID, u PluginConfigUpdate) (PluginConfigRow, error) {
	if err := requirePlugin(ctx, tx, plugin); err != nil {
		return PluginConfigRow{}, err
	}
	cur, err := pluginConfigTx(ctx, tx, plugin, project)
	if err != nil {
		return PluginConfigRow{}, err
	}
	if cur.Revision != u.ExpectedRevision {
		return PluginConfigRow{}, apperr.PreconditionFailed("配置已被修改，请重新读取后再保存")
	}
	next, err := u.Apply(cur)
	if err != nil {
		return PluginConfigRow{}, err
	}
	if reflect.DeepEqual(nonEmpty(cur.Values), nonEmpty(next.Values)) && maps.Equal(cur.Secrets, next.Secrets) {
		// Nothing changed (e.g. only the project's enablement changed): do not bump the revision or emit an event.
		return cur, nil
	}
	next.Revision = cur.Revision + 1
	if _, err = tx.Exec(ctx, `INSERT INTO plugin_config(plugin_id,project_id,"values",secrets,revision) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT (plugin_id, coalesce(project_id, '')) DO UPDATE SET "values"=excluded."values", secrets=excluded.secrets, revision=excluded.revision`,
		plugin, project, next.Values, next.Secrets, next.Revision); err != nil {
		return PluginConfigRow{}, err
	}
	return next, emitPluginEventTx(ctx, tx, "plugin.config_changed", project, nil, nil, nil, map[string]any{"pluginId": plugin})
}

func nonEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// requirePlugin confirms the plugin exists (write transactions are serialized and exclude uninstall and update, so the
// row does not need to be locked); returns not_found when it does not exist.
func requirePlugin(ctx context.Context, tx *db.Tx, plugin string) error {
	var id string
	return notFoundIfNoRows(tx.QueryRow(ctx, `SELECT id FROM plugins WHERE id=$1`, plugin).Scan(&id), "插件")
}

// PluginConfigs returns the plugin's global configuration and the configurations of the projects that enabled it
// (handed to the platform side together when the plugin host loads): only non-deleted projects that enabled the
// plugin are included, a project without a project-level configuration gets an empty one, and projects that disabled
// the plugin or are deleted are excluded even if they still have a configuration.
func (s *Store) PluginConfigs(ctx context.Context, plugin string) (PluginConfigRow, map[uuid.UUID]PluginConfigRow, error) {
	global := PluginConfigRow{Values: map[string]any{}, Secrets: map[string]string{}}
	projects := map[uuid.UUID]PluginConfigRow{}
	rows, err := s.Pool.Query(ctx, `SELECT NULL,"values",secrets,revision FROM plugin_config WHERE plugin_id=$1 AND project_id IS NULL
		UNION ALL
		SELECT pp.project_id,coalesce(c."values",'{}'),coalesce(c.secrets,'{}'),coalesce(c.revision,0)
		FROM project_plugins pp JOIN projects pr ON pr.id=pp.project_id AND pr.deleted_at IS NULL
		LEFT JOIN plugin_config c ON c.plugin_id=pp.plugin_id AND c.project_id=pp.project_id
		WHERE pp.plugin_id=$1 AND pp.enabled`, plugin)
	if err != nil {
		return global, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var project *uuid.UUID
		var c PluginConfigRow
		if err := rows.Scan(&project, &c.Values, &c.Secrets, &c.Revision); err != nil {
			return global, nil, err
		}
		if project == nil {
			global = c
		} else {
			projects[*project] = c
		}
	}
	return global, projects, rows.Err()
}

// ProjectPluginRow is a plugin's enablement and project-level configuration in one project.
type ProjectPluginRow struct {
	Plugin  Plugin
	Enabled bool
	Config  PluginConfigRow
}

// ProjectPlugins returns, for each installed plugin, its enablement and project-level configuration in the project;
// returns not_found when the project does not exist.
func (s *Store) ProjectPlugins(ctx context.Context, project uuid.UUID) ([]ProjectPluginRow, error) {
	if _, err := s.GetProject(ctx, project); err != nil {
		return nil, err
	}
	plugins, err := s.ListPlugins(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ProjectPluginRow, 0, len(plugins))
	for _, p := range plugins {
		row := ProjectPluginRow{Plugin: p}
		if err := s.Pool.QueryRow(ctx, `SELECT coalesce((SELECT enabled FROM project_plugins WHERE project_id=$1 AND plugin_id=$2),false)`, project, p.ID).Scan(&row.Enabled); err != nil {
			return nil, err
		}
		if row.Config, err = s.PluginConfig(ctx, p.ID, &project); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// SaveProjectPlugin saves the project's enablement and project-level configuration in one transaction, and returns
// the state after saving and whether the enablement changed.
func (s *Store) SaveProjectPlugin(ctx context.Context, project uuid.UUID, plugin string, enabled bool, u PluginConfigUpdate) (ProjectPluginRow, bool, error) {
	var row ProjectPluginRow
	changed := false
	err := s.inTx(ctx, func(tx *db.Tx) error {
		var deleted *time.Time
		if err := tx.QueryRow(ctx, `SELECT deleted_at FROM projects WHERE id=$1`, project).Scan(&deleted); err != nil || deleted != nil {
			return notFoundIfNoRows(orNoRows(err, deleted != nil), "项目")
		}
		var before *bool
		if err := tx.QueryRow(ctx, `SELECT enabled FROM project_plugins WHERE project_id=$1 AND plugin_id=$2`, project, plugin).Scan(&before); err != nil && !errors.Is(err, db.ErrNoRows) {
			return err
		}
		changed = (before == nil && enabled) || (before != nil && *before != enabled)
		if _, err := tx.Exec(ctx, `INSERT INTO project_plugins(project_id,plugin_id,enabled) VALUES($1,$2,$3)
			ON CONFLICT (project_id,plugin_id) DO UPDATE SET enabled=EXCLUDED.enabled`, project, plugin, enabled); err != nil {
			return err
		}
		if changed {
			typ := "plugin.disabled"
			if enabled {
				typ = "plugin.enabled"
			}
			if err := emitPluginEventTx(ctx, tx, typ, &project, nil, nil, nil, map[string]any{"pluginId": plugin}); err != nil {
				return err
			}
		}
		// Change the enablement first, then save the configuration: when the plugin was just enabled, it also receives
		// this plugin.config_changed.
		cfg, err := s.savePluginConfigTx(ctx, tx, plugin, &project, u)
		if err != nil {
			return err
		}
		row.Enabled, row.Config = enabled, cfg
		return nil
	})
	if err != nil {
		return row, false, err
	}
	row.Plugin, err = s.GetPlugin(ctx, plugin)
	return row, changed, err
}

func orNoRows(err error, missing bool) error {
	if err == nil && missing {
		return db.ErrNoRows
	}
	return err
}

// PluginEnabledIn reports whether the plugin is enabled in the project.
func (s *Store) PluginEnabledIn(ctx context.Context, plugin string, project uuid.UUID) (bool, error) {
	var on bool
	err := s.Pool.QueryRow(ctx, `SELECT coalesce((SELECT enabled FROM project_plugins WHERE project_id=$1 AND plugin_id=$2),false)`, project, plugin).Scan(&on)
	return on, err
}

// Reasons an Agent cannot use a plugin's Agent side (results of AgentPluginUsability).
const (
	PluginUnusableNotEnabled = "plugin_not_enabled" // the Agent's project has not enabled the plugin (project Agents only)
	PluginUnusableNotGranted = "agent_not_granted"  // the plugin is not granted to this Agent
)

// AgentPluginUsability reports whether an Agent can use a plugin's Agent side: empty means it can, otherwise one of
// PluginUnusable*. A project Agent needs the plugin enabled in its project and granted to it; a platform Agent has no
// project layer and only needs the grant (matching plugins.AgentAssets). Whether the plugin is installed or plugins
// are disabled globally is not checked here.
func (s *Store) AgentPluginUsability(ctx context.Context, agent uuid.UUID, plugin string) (string, error) {
	var project *uuid.UUID
	var enabled, granted bool
	err := s.Pool.QueryRow(ctx, `SELECT a.project_id,
		coalesce((SELECT enabled FROM project_plugins WHERE project_id=a.project_id AND plugin_id=$2),false),
		EXISTS(SELECT 1 FROM agent_plugin_grants WHERE agent_id=a.id AND plugin_id=$2)
		FROM agents a WHERE a.id=$1 AND a.deleted_at IS NULL`, agent, plugin).Scan(&project, &enabled, &granted)
	switch {
	case err != nil:
		return "", notFoundIfNoRows(err, "Agent")
	case project != nil && !enabled:
		return PluginUnusableNotEnabled, nil
	case !granted:
		return PluginUnusableNotGranted, nil
	}
	return "", nil
}

// AgentPluginGrant is an Agent's grant for one plugin.
type AgentPluginGrant struct {
	PluginID   string
	AccountIDs []uuid.UUID
}

// AgentPluginGrants returns all of the Agent's grants, sorted by plugin id.
func (s *Store) AgentPluginGrants(ctx context.Context, agent uuid.UUID) ([]AgentPluginGrant, error) {
	rows, err := s.Pool.Query(ctx, `SELECT plugin_id,account_ids FROM agent_plugin_grants WHERE agent_id=$1 ORDER BY plugin_id`, agent)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(r db.CollectableRow) (AgentPluginGrant, error) {
		var g AgentPluginGrant
		return g, r.Scan(&g.PluginID, &g.AccountIDs)
	})
}

// SaveAgentPluginGrant grants (granted) or revokes a grant. A project Agent can only be granted plugins enabled in the
// project; the account must be a non-deleted account of this plugin in that project.
func (s *Store) SaveAgentPluginGrant(ctx context.Context, agent uuid.UUID, plugin string, granted bool, accounts []uuid.UUID) error {
	return s.inTx(ctx, func(tx *db.Tx) error {
		var project *uuid.UUID
		var deleted *time.Time
		if err := tx.QueryRow(ctx, `SELECT project_id,deleted_at FROM agents WHERE id=$1`, agent).Scan(&project, &deleted); err != nil || deleted != nil {
			return notFoundIfNoRows(orNoRows(err, deleted != nil), "Agent")
		}
		if err := requirePlugin(ctx, tx, plugin); err != nil {
			return err
		}
		if !granted {
			_, err := tx.Exec(ctx, `DELETE FROM agent_plugin_grants WHERE agent_id=$1 AND plugin_id=$2`, agent, plugin)
			return err
		}
		if project != nil {
			var on bool
			if err := tx.QueryRow(ctx, `SELECT coalesce((SELECT enabled FROM project_plugins WHERE project_id=$1 AND plugin_id=$2),false)`, *project, plugin).Scan(&on); err != nil {
				return err
			}
			if !on {
				return apperr.Conflict(apperr.CodePluginNotEnabled, "项目没有启用插件 "+plugin+"，先在项目插件页启用")
			}
		}
		if len(accounts) > 0 {
			if project == nil {
				return apperr.Invalid("accountIds", "平台 Agent 没有项目账号")
			}
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM plugin_accounts WHERE id IN (SELECT value FROM json_each($1)) AND plugin_id=$2 AND project_id=$3 AND NOT deleted`, accounts, plugin, *project).Scan(&n); err != nil {
				return err
			}
			if n != len(accounts) {
				return apperr.Invalid("accountIds", "账号不存在、已删除或不属于这个项目的这个插件")
			}
		}
		if accounts == nil {
			accounts = []uuid.UUID{}
		}
		_, err := tx.Exec(ctx, `INSERT INTO agent_plugin_grants(agent_id,plugin_id,account_ids) VALUES($1,$2,$3)
			ON CONFLICT (agent_id,plugin_id) DO UPDATE SET account_ids=EXCLUDED.account_ids`, agent, plugin, accounts)
		return err
	})
}

// PluginAccount is an account of a plugin in a project. Values holds all fields (including secret fields).
type PluginAccount struct {
	ID        uuid.UUID
	PluginID  string
	ProjectID uuid.UUID
	Kind      string
	Name      string
	Values    map[string]any
	Revision  int64
}

const pluginAccountColumns = `id,plugin_id,project_id,kind,name,"values",revision`

func scanPluginAccount(r db.Scanner) (PluginAccount, error) {
	var a PluginAccount
	err := r.Scan(&a.ID, &a.PluginID, &a.ProjectID, &a.Kind, &a.Name, &a.Values, &a.Revision)
	return a, err
}

// PluginAccounts returns the non-deleted accounts of this plugin in the project, sorted by kind and name.
func (s *Store) PluginAccounts(ctx context.Context, project uuid.UUID, plugin string) ([]PluginAccount, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+pluginAccountColumns+` FROM plugin_accounts WHERE project_id=$1 AND plugin_id=$2 AND NOT deleted ORDER BY kind,name,id`, project, plugin)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(r db.CollectableRow) (PluginAccount, error) { return scanPluginAccount(r) })
}

// PluginAccountsOfKind returns the non-deleted accounts of one kind of this plugin in the project, in the order they
// were added (the plugin host's accounts_list).
func (s *Store) PluginAccountsOfKind(ctx context.Context, project uuid.UUID, plugin, kind string) ([]PluginAccount, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+pluginAccountColumns+` FROM plugin_accounts WHERE project_id=$1 AND plugin_id=$2 AND kind=$3 AND NOT deleted ORDER BY id`, project, plugin, kind)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(r db.CollectableRow) (PluginAccount, error) { return scanPluginAccount(r) })
}

// GetPluginAccount returns a non-deleted account.
func (s *Store) GetPluginAccount(ctx context.Context, project uuid.UUID, plugin string, id uuid.UUID) (PluginAccount, error) {
	a, err := scanPluginAccount(s.Pool.QueryRow(ctx, `SELECT `+pluginAccountColumns+` FROM plugin_accounts WHERE id=$1 AND project_id=$2 AND plugin_id=$3 AND NOT deleted`, id, project, plugin))
	return a, notFoundIfNoRows(err, "账号")
}

// CreatePluginAccount adds an account; returns name_conflict when single is true (the kind does not support multiple
// accounts) and an account already exists.
func (s *Store) CreatePluginAccount(ctx context.Context, a PluginAccount, single bool) (PluginAccount, error) {
	err := s.inTx(ctx, func(tx *db.Tx) error {
		var deleted *time.Time
		if err := tx.QueryRow(ctx, `SELECT deleted_at FROM projects WHERE id=$1`, a.ProjectID).Scan(&deleted); err != nil || deleted != nil {
			return notFoundIfNoRows(orNoRows(err, deleted != nil), "项目")
		}
		if err := requirePlugin(ctx, tx, a.PluginID); err != nil {
			return err
		}
		// Account creation for the same plugin in the same project is serialized, which is what makes the
		// single-account check reliable.
		var dup bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plugin_accounts WHERE project_id=$1 AND plugin_id=$2 AND kind=$3 AND NOT deleted AND ($4 OR name=$5))`,
			a.ProjectID, a.PluginID, a.Kind, single, a.Name).Scan(&dup); err != nil {
			return err
		}
		if dup {
			if single {
				return apperr.Conflict(apperr.CodeNameConflict, "这个种类只能有一个账号")
			}
			return apperr.Conflict(apperr.CodeNameConflict, "账号名称重复")
		}
		var err error
		a, err = scanPluginAccount(tx.QueryRow(ctx, `INSERT INTO plugin_accounts(id,plugin_id,project_id,kind,name,"values") VALUES($1,$2,$3,$4,$5,$6) RETURNING `+pluginAccountColumns,
			uuid.Must(uuid.NewV7()), a.PluginID, a.ProjectID, a.Kind, a.Name, a.Values))
		return err
	})
	return a, err
}

// UpdatePluginAccount changes an account by revision; apply computes the new name and fields after the current row is
// locked.
func (s *Store) UpdatePluginAccount(ctx context.Context, project uuid.UUID, plugin string, id uuid.UUID, expected int64, apply func(PluginAccount) (PluginAccount, error)) (PluginAccount, error) {
	var out PluginAccount
	err := s.inTx(ctx, func(tx *db.Tx) error {
		cur, err := scanPluginAccount(tx.QueryRow(ctx, `SELECT `+pluginAccountColumns+` FROM plugin_accounts WHERE id=$1 AND project_id=$2 AND plugin_id=$3 AND NOT deleted`, id, project, plugin))
		if err != nil {
			return notFoundIfNoRows(err, "账号")
		}
		if cur.Revision != expected {
			return apperr.PreconditionFailed("账号已被修改，请重新读取后再保存")
		}
		next, err := apply(cur)
		if err != nil {
			return err
		}
		var dup bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plugin_accounts WHERE project_id=$1 AND plugin_id=$2 AND kind=$3 AND name=$4 AND id<>$5 AND NOT deleted)`,
			project, plugin, cur.Kind, next.Name, id).Scan(&dup); err != nil {
			return err
		}
		if dup {
			return apperr.Conflict(apperr.CodeNameConflict, "账号名称重复")
		}
		out, err = scanPluginAccount(tx.QueryRow(ctx, `UPDATE plugin_accounts SET name=$2, "values"=$3, revision=revision+1 WHERE id=$1 RETURNING `+pluginAccountColumns, id, next.Name, next.Values))
		return err
	})
	return out, err
}

// DeletePluginAccount deletes an account (clearing its fields) and removes references to it from all Agents' grants.
func (s *Store) DeletePluginAccount(ctx context.Context, project uuid.UUID, plugin string, id uuid.UUID) error {
	return s.inTx(ctx, func(tx *db.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE plugin_accounts SET deleted=true, "values"='{}', revision=revision+1 WHERE id=$1 AND project_id=$2 AND plugin_id=$3 AND NOT deleted`, id, project, plugin)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.NotFound("账号")
		}
		_, err = tx.Exec(ctx, `UPDATE agent_plugin_grants SET account_ids=(SELECT json_group_array(v.value) FROM json_each(account_ids) v WHERE v.value<>$2)
			WHERE plugin_id=$1 AND EXISTS (SELECT 1 FROM json_each(account_ids) v WHERE v.value=$2)`, plugin, id.String())
		return err
	})
}

// PluginTokenRecord is the server-side record of a plugin token.
type PluginTokenRecord struct {
	PluginID string
	Hash     []byte
	Scopes   []string
	Revoked  bool
}

// IssuePluginToken revokes the plugin's existing tokens and registers a new one.
func (s *Store) IssuePluginToken(ctx context.Context, plugin string, id uuid.UUID, hash []byte, scopes []string) error {
	return s.inTx(ctx, func(tx *db.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE plugin_tokens SET revoked_at=now() WHERE plugin_id=$1 AND revoked_at IS NULL`, plugin); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO plugin_tokens(id,plugin_id,token_hash,scopes) VALUES($1,$2,$3,$4)`, id, plugin, hash, scopes)
		return err
	})
}

// LookupPluginToken looks up a plugin token by ID; returns not_found when it does not exist.
func (s *Store) LookupPluginToken(ctx context.Context, id uuid.UUID) (PluginTokenRecord, error) {
	var t PluginTokenRecord
	err := s.Pool.QueryRow(ctx, `SELECT plugin_id,token_hash,scopes,revoked_at IS NOT NULL FROM plugin_tokens WHERE id=$1`, id).Scan(&t.PluginID, &t.Hash, &t.Scopes, &t.Revoked)
	return t, notFoundIfNoRows(err, "令牌")
}

// PluginSettings are the global plugin settings.
type PluginSettings struct {
	Disabled bool     `json:"disabled"`
	Markets  []string `json:"markets"`
}

// GetPluginSettings reads the global plugin settings.
func (s *Store) GetPluginSettings(ctx context.Context) (PluginSettings, error) {
	var out PluginSettings
	err := s.Pool.QueryRow(ctx, `SELECT disabled,markets FROM plugin_settings`).Scan(&out.Disabled, &out.Markets)
	return out, err
}

// PutPluginSettings saves the global plugin settings.
func (s *Store) PutPluginSettings(ctx context.Context, v PluginSettings) error {
	if v.Markets == nil {
		v.Markets = []string{}
	}
	_, err := s.Pool.Exec(ctx, `UPDATE plugin_settings SET disabled=$1, markets=$2, updated_at=now()`, v.Disabled, v.Markets)
	return err
}

// PluginLogEntry is one plugin host log line.
type PluginLogEntry struct {
	ID    int64     `json:"id"`
	At    time.Time `json:"at"`
	Level string    `json:"level"`
	Line  string    `json:"line"`
}

// PluginLogs returns the plugin's plugin host logs, newest first; starts from the latest when before is 0.
func (s *Store) PluginLogs(ctx context.Context, plugin string, before int64, limit int) ([]PluginLogEntry, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,at,level,line FROM plugin_host_log WHERE plugin_id=$1 AND ($2=0 OR id<$2) ORDER BY id DESC LIMIT $3`, plugin, before, limit)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(r db.CollectableRow) (PluginLogEntry, error) {
		var e PluginLogEntry
		return e, r.Scan(&e.ID, &e.At, &e.Level, &e.Line)
	})
}

// PluginDeliveryRow is one delivery record.
type PluginDeliveryRow struct {
	PluginEvent
	Status     string
	Attempts   int
	LastError  *string
	AckedAt    *time.Time
	DurationMs *int64
}

// PluginDeliveryQuery holds the filters and paging for delivery records; empty values mean no filter.
type PluginDeliveryQuery struct {
	Status  string
	Project *uuid.UUID
	Before  string
	Limit   int
}

// PluginDeliveries returns the plugin's delivery records, newest first by event id (that is, by time).
func (s *Store) PluginDeliveries(ctx context.Context, plugin string, q PluginDeliveryQuery) ([]PluginDeliveryRow, error) {
	rows, err := s.Pool.Query(ctx, `SELECT e.id,e.type,e.at,e.project_id,e.agent_id,e.task_id,e.run_id,d.status,d.attempts,d.last_error,d.acked_at,d.duration_ms
		FROM plugin_event_deliveries d JOIN plugin_events e ON e.id=d.event_id
		WHERE d.plugin_id=$1 AND ($2='' OR d.status=$2) AND ($3 IS NULL OR e.project_id=$3) AND ($4='' OR e.id<$4)
		ORDER BY e.id DESC LIMIT $5`, plugin, q.Status, q.Project, q.Before, q.Limit)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(r db.CollectableRow) (PluginDeliveryRow, error) {
		var d PluginDeliveryRow
		return d, r.Scan(&d.ID, &d.Type, &d.At, &d.ProjectID, &d.AgentID, &d.TaskID, &d.RunID, &d.Status, &d.Attempts, &d.LastError, &d.AckedAt, &d.DurationMs)
	})
}
