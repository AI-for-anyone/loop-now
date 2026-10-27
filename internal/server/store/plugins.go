package store

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"loop-now/internal/server/apperr"
)

// 插件第二版的安装、配置、启用、授权、账号、令牌与全局设置（plugins/SPEC.md §6、§8）。

// Plugin 是一个已安装的插件。Manifest 是插件服务保存的清单（JSON 对象）。
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

// PluginVersion 是装过的一个版本。
type PluginVersion struct {
	Version     string
	Manifest    json.RawMessage
	InstallDir  string
	Digest      string
	InstalledAt time.Time
}

const pluginColumns = `p.id,p.display_name,p.source,p.version,p.manifest,p.install_dir,p.status,p.last_error,p.installed_at,p.updated_at,
	(SELECT count(*) FROM project_plugins pp JOIN projects pr ON pr.id=pp.project_id WHERE pp.plugin_id=p.id AND pp.enabled AND pr.deleted_at IS NULL),
	(SELECT count(*) FROM agent_plugin_grants g JOIN agents a ON a.id=g.agent_id WHERE g.plugin_id=p.id AND a.deleted_at IS NULL)`

func scanPlugin(r pgx.Row) (Plugin, error) {
	var p Plugin
	err := r.Scan(&p.ID, &p.DisplayName, &p.Source, &p.Version, &p.Manifest, &p.InstallDir, &p.Status, &p.LastError, &p.InstalledAt, &p.UpdatedAt, &p.EnabledProjects, &p.GrantedAgents)
	return p, err
}

// ListPlugins 按 id 返回全部已安装的插件。
func (s *Store) ListPlugins(ctx context.Context) ([]Plugin, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+pluginColumns+` FROM plugins p ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Plugin, error) { return scanPlugin(r) })
}

// GetPlugin 返回一个插件；不存在时返回 not_found。
func (s *Store) GetPlugin(ctx context.Context, id string) (Plugin, error) {
	p, err := scanPlugin(s.Pool.QueryRow(ctx, `SELECT `+pluginColumns+` FROM plugins p WHERE p.id=$1`, id))
	return p, notFoundIfNoRows(err, "插件")
}

// PluginVersions 返回插件装过的版本，新的在前。
func (s *Store) PluginVersions(ctx context.Context, id string) ([]PluginVersion, error) {
	rows, err := s.Pool.Query(ctx, `SELECT version,manifest,install_dir,digest,installed_at FROM plugin_versions WHERE plugin_id=$1 ORDER BY installed_at DESC, version DESC`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PluginVersion, error) {
		var v PluginVersion
		return v, r.Scan(&v.Version, &v.Manifest, &v.InstallDir, &v.Digest, &v.InstalledAt)
	})
}

// InstallPlugin 登记新安装的插件与它的第一个版本；id 已存在时返回 name_conflict。
func (s *Store) InstallPlugin(ctx context.Context, p Plugin, digest string) error {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO plugins(id,display_name,source,version,manifest,install_dir,status) VALUES($1,$2,$3,$4,$5,$6,'installed')`,
			p.ID, p.DisplayName, p.Source, p.Version, p.Manifest, p.InstallDir); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO plugin_versions(plugin_id,version,manifest,install_dir,digest) VALUES($1,$2,$3,$4,$5)`, p.ID, p.Version, p.Manifest, p.InstallDir, digest)
		return err
	})
	if isUniqueViolation(err, "plugins_pkey") {
		return apperr.Conflict(apperr.CodeNameConflict, "插件 "+p.ID+" 已安装，请用更新")
	}
	return err
}

// UpdatePlugin 切换到新版本：同一版本号已装且内容不同时返回 plugin_version_conflict，内容相同时直接切回。
func (s *Store) UpdatePlugin(ctx context.Context, p Plugin, digest string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var existing string
		err := tx.QueryRow(ctx, `SELECT digest FROM plugin_versions WHERE plugin_id=$1 AND version=$2`, p.ID, p.Version).Scan(&existing)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if _, err = tx.Exec(ctx, `INSERT INTO plugin_versions(plugin_id,version,manifest,install_dir,digest) VALUES($1,$2,$3,$4,$5)`, p.ID, p.Version, p.Manifest, p.InstallDir, digest); err != nil {
				return err
			}
		case err != nil:
			return err
		case existing != digest:
			return apperr.Conflict(apperr.CodePluginVersionConflict, "插件 "+p.ID+" 已装过版本 "+p.Version+"，内容不同；请改版本号")
		default:
			if _, err = tx.Exec(ctx, `UPDATE plugin_versions SET manifest=$3, install_dir=$4 WHERE plugin_id=$1 AND version=$2`, p.ID, p.Version, p.Manifest, p.InstallDir); err != nil {
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

// DeletePlugin 删除插件及其配置、启用、授权、账号、状态、投递与令牌，返回各版本的安装目录。
func (s *Store) DeletePlugin(ctx context.Context, id string) ([]string, error) {
	var dirs []string
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT install_dir FROM plugin_versions WHERE plugin_id=$1`, id)
		if err != nil {
			return err
		}
		if dirs, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
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

// PluginConfigRow 是全局或某个项目的配置；Revision 为 0 表示还没有保存过。
type PluginConfigRow struct {
	Values   map[string]any
	Secrets  map[string]string
	Revision int64
}

// PluginConfig 读取全局（project 为空）或项目级配置。
func (s *Store) PluginConfig(ctx context.Context, plugin string, project *uuid.UUID) (PluginConfigRow, error) {
	return pluginConfigTx(ctx, s.Pool, plugin, project, false)
}

type queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func pluginConfigTx(ctx context.Context, q queryer, plugin string, project *uuid.UUID, lock bool) (PluginConfigRow, error) {
	c := PluginConfigRow{Values: map[string]any{}, Secrets: map[string]string{}}
	sql := `SELECT values,secrets,revision FROM plugin_config WHERE plugin_id=$1 AND project_id IS NOT DISTINCT FROM $2`
	if lock {
		sql += ` FOR UPDATE`
	}
	err := q.QueryRow(ctx, sql, plugin, project).Scan(&c.Values, &c.Secrets, &c.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, nil
	}
	return c, err
}

// PluginConfigUpdate 是一次配置保存：Apply 在锁住当前行后算出要保存的值，返回错误时不保存。
type PluginConfigUpdate struct {
	ExpectedRevision int64
	Apply            func(current PluginConfigRow) (PluginConfigRow, error)
}

// SavePluginConfig 按版本保存配置：版本不一致返回 412。返回保存后的配置。
func (s *Store) SavePluginConfig(ctx context.Context, plugin string, project *uuid.UUID, u PluginConfigUpdate) (PluginConfigRow, error) {
	var out PluginConfigRow
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = s.savePluginConfigTx(ctx, tx, plugin, project, u)
		return err
	})
	return out, err
}

func (s *Store) savePluginConfigTx(ctx context.Context, tx pgx.Tx, plugin string, project *uuid.UUID, u PluginConfigUpdate) (PluginConfigRow, error) {
	if err := lockPlugin(ctx, tx, plugin); err != nil {
		return PluginConfigRow{}, err
	}
	cur, err := pluginConfigTx(ctx, tx, plugin, project, true)
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
		// 没有变化（例如只改了项目的启用状态）：不加版本、不发事件。
		return cur, nil
	}
	next.Revision = cur.Revision + 1
	if _, err = tx.Exec(ctx, `INSERT INTO plugin_config(plugin_id,project_id,values,secrets,revision) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT (plugin_id,project_id) DO UPDATE SET values=EXCLUDED.values, secrets=EXCLUDED.secrets, revision=EXCLUDED.revision`,
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

// lockPlugin 锁住插件行（与卸载、更新互斥）；插件不存在时返回 not_found。
func lockPlugin(ctx context.Context, tx pgx.Tx, plugin string) error {
	var id string
	return notFoundIfNoRows(tx.QueryRow(ctx, `SELECT id FROM plugins WHERE id=$1 FOR SHARE`, plugin).Scan(&id), "插件")
}

// PluginConfigs 返回插件的全局配置与启用了它的各项目的配置（宿主加载时一起交给平台半边）：
// 只含启用了插件、未删除的项目，没有项目级配置的项目为空配置；停用或删除的项目即使留有配置也不含。
func (s *Store) PluginConfigs(ctx context.Context, plugin string) (PluginConfigRow, map[uuid.UUID]PluginConfigRow, error) {
	global := PluginConfigRow{Values: map[string]any{}, Secrets: map[string]string{}}
	projects := map[uuid.UUID]PluginConfigRow{}
	rows, err := s.Pool.Query(ctx, `SELECT NULL::uuid,values,secrets,revision FROM plugin_config WHERE plugin_id=$1 AND project_id IS NULL
		UNION ALL
		SELECT pp.project_id,coalesce(c.values,'{}'),coalesce(c.secrets,'{}'),coalesce(c.revision,0)
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

// ProjectPluginRow 是插件在一个项目里的启用状态与项目级配置。
type ProjectPluginRow struct {
	Plugin  Plugin
	Enabled bool
	Config  PluginConfigRow
}

// ProjectPlugins 返回每个已安装插件在项目里的启用状态与项目级配置；项目不存在时返回 not_found。
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

// SaveProjectPlugin 在一个事务里保存项目的启用状态与项目级配置，返回保存后的状态与启用是否变化。
func (s *Store) SaveProjectPlugin(ctx context.Context, project uuid.UUID, plugin string, enabled bool, u PluginConfigUpdate) (ProjectPluginRow, bool, error) {
	var row ProjectPluginRow
	changed := false
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var deleted *time.Time
		if err := tx.QueryRow(ctx, `SELECT deleted_at FROM projects WHERE id=$1 FOR SHARE`, project).Scan(&deleted); err != nil || deleted != nil {
			return notFoundIfNoRows(orNoRows(err, deleted != nil), "项目")
		}
		var before *bool
		if err := tx.QueryRow(ctx, `SELECT enabled FROM project_plugins WHERE project_id=$1 AND plugin_id=$2 FOR UPDATE`, project, plugin).Scan(&before); err != nil && !errors.Is(err, pgx.ErrNoRows) {
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
		// 先改启用状态再存配置：刚启用时这次的 plugin.config_changed 也发给它。
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
		return pgx.ErrNoRows
	}
	return err
}

// PluginEnabledIn 报告插件是否在项目里启用。
func (s *Store) PluginEnabledIn(ctx context.Context, plugin string, project uuid.UUID) (bool, error) {
	var on bool
	err := s.Pool.QueryRow(ctx, `SELECT coalesce((SELECT enabled FROM project_plugins WHERE project_id=$1 AND plugin_id=$2),false)`, project, plugin).Scan(&on)
	return on, err
}

// AgentPluginGrant 是 Agent 对一个插件的授权。
type AgentPluginGrant struct {
	PluginID   string
	AccountIDs []uuid.UUID
}

// AgentPluginGrants 返回 Agent 的全部授权，按插件 id 排序。
func (s *Store) AgentPluginGrants(ctx context.Context, agent uuid.UUID) ([]AgentPluginGrant, error) {
	rows, err := s.Pool.Query(ctx, `SELECT plugin_id,account_ids FROM agent_plugin_grants WHERE agent_id=$1 ORDER BY plugin_id`, agent)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AgentPluginGrant, error) {
		var g AgentPluginGrant
		return g, r.Scan(&g.PluginID, &g.AccountIDs)
	})
}

// SaveAgentPluginGrant 授权（granted）或取消授权。项目 Agent 只能授权项目已启用的插件；
// 账号须是该项目这个插件未删除的账号。
func (s *Store) SaveAgentPluginGrant(ctx context.Context, agent uuid.UUID, plugin string, granted bool, accounts []uuid.UUID) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var project *uuid.UUID
		var deleted *time.Time
		if err := tx.QueryRow(ctx, `SELECT project_id,deleted_at FROM agents WHERE id=$1 FOR SHARE`, agent).Scan(&project, &deleted); err != nil || deleted != nil {
			return notFoundIfNoRows(orNoRows(err, deleted != nil), "Agent")
		}
		if err := lockPlugin(ctx, tx, plugin); err != nil {
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
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM plugin_accounts WHERE id=ANY($1) AND plugin_id=$2 AND project_id=$3 AND NOT deleted`, accounts, plugin, *project).Scan(&n); err != nil {
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

// PluginAccount 是项目里一个插件的账号。Values 含全部字段（包括 secret 字段）。
type PluginAccount struct {
	ID        uuid.UUID
	PluginID  string
	ProjectID uuid.UUID
	Kind      string
	Name      string
	Values    map[string]any
	Revision  int64
}

const pluginAccountColumns = `id,plugin_id,project_id,kind,name,values,revision`

func scanPluginAccount(r pgx.Row) (PluginAccount, error) {
	var a PluginAccount
	err := r.Scan(&a.ID, &a.PluginID, &a.ProjectID, &a.Kind, &a.Name, &a.Values, &a.Revision)
	return a, err
}

// PluginAccounts 返回项目里这个插件未删除的账号，按种类、名称排序。
func (s *Store) PluginAccounts(ctx context.Context, project uuid.UUID, plugin string) ([]PluginAccount, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+pluginAccountColumns+` FROM plugin_accounts WHERE project_id=$1 AND plugin_id=$2 AND NOT deleted ORDER BY kind,name,id`, project, plugin)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PluginAccount, error) { return scanPluginAccount(r) })
}

// PluginAccountsOfKind 返回项目里这个插件某个种类未删除的账号，按添加的先后（宿主的 accounts_list）。
func (s *Store) PluginAccountsOfKind(ctx context.Context, project uuid.UUID, plugin, kind string) ([]PluginAccount, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+pluginAccountColumns+` FROM plugin_accounts WHERE project_id=$1 AND plugin_id=$2 AND kind=$3 AND NOT deleted ORDER BY id`, project, plugin, kind)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PluginAccount, error) { return scanPluginAccount(r) })
}

// GetPluginAccount 返回一个未删除的账号。
func (s *Store) GetPluginAccount(ctx context.Context, project uuid.UUID, plugin string, id uuid.UUID) (PluginAccount, error) {
	a, err := scanPluginAccount(s.Pool.QueryRow(ctx, `SELECT `+pluginAccountColumns+` FROM plugin_accounts WHERE id=$1 AND project_id=$2 AND plugin_id=$3 AND NOT deleted`, id, project, plugin))
	return a, notFoundIfNoRows(err, "账号")
}

// CreatePluginAccount 添加账号；single 为 true（该种类不支持多账号）且已有账号时返回 name_conflict。
func (s *Store) CreatePluginAccount(ctx context.Context, a PluginAccount, single bool) (PluginAccount, error) {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var deleted *time.Time
		if err := tx.QueryRow(ctx, `SELECT deleted_at FROM projects WHERE id=$1 FOR SHARE`, a.ProjectID).Scan(&deleted); err != nil || deleted != nil {
			return notFoundIfNoRows(orNoRows(err, deleted != nil), "项目")
		}
		if err := lockPlugin(ctx, tx, a.PluginID); err != nil {
			return err
		}
		// 同一项目同一插件的账号创建串行，单账号检查才可靠。
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('plugin_accounts:'||$1::text||':'||$2))`, a.ProjectID, a.PluginID); err != nil {
			return err
		}
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
		a, err = scanPluginAccount(tx.QueryRow(ctx, `INSERT INTO plugin_accounts(id,plugin_id,project_id,kind,name,values) VALUES($1,$2,$3,$4,$5,$6) RETURNING `+pluginAccountColumns,
			uuid.Must(uuid.NewV7()), a.PluginID, a.ProjectID, a.Kind, a.Name, a.Values))
		return err
	})
	return a, err
}

// UpdatePluginAccount 按版本修改账号；apply 在锁住当前行后算出新的名称与字段。
func (s *Store) UpdatePluginAccount(ctx context.Context, project uuid.UUID, plugin string, id uuid.UUID, expected int64, apply func(PluginAccount) (PluginAccount, error)) (PluginAccount, error) {
	var out PluginAccount
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		cur, err := scanPluginAccount(tx.QueryRow(ctx, `SELECT `+pluginAccountColumns+` FROM plugin_accounts WHERE id=$1 AND project_id=$2 AND plugin_id=$3 AND NOT deleted FOR UPDATE`, id, project, plugin))
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
		out, err = scanPluginAccount(tx.QueryRow(ctx, `UPDATE plugin_accounts SET name=$2, values=$3, revision=revision+1 WHERE id=$1 RETURNING `+pluginAccountColumns, id, next.Name, next.Values))
		return err
	})
	return out, err
}

// DeletePluginAccount 删除账号（字段清空），并从各 Agent 的授权中移除对它的引用。
func (s *Store) DeletePluginAccount(ctx context.Context, project uuid.UUID, plugin string, id uuid.UUID) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE plugin_accounts SET deleted=true, values='{}', revision=revision+1 WHERE id=$1 AND project_id=$2 AND plugin_id=$3 AND NOT deleted`, id, project, plugin)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.NotFound("账号")
		}
		_, err = tx.Exec(ctx, `UPDATE agent_plugin_grants SET account_ids=(SELECT coalesce(jsonb_agg(v),'[]') FROM jsonb_array_elements(account_ids) v WHERE v<>to_jsonb($2::text))
			WHERE plugin_id=$1 AND account_ids @> jsonb_build_array($2::text)`, plugin, id.String())
		return err
	})
}

// PluginTokenRecord 是插件令牌的服务端记录。
type PluginTokenRecord struct {
	PluginID string
	Hash     []byte
	Scopes   []string
	Revoked  bool
}

// IssuePluginToken 吊销插件现有的令牌并登记新的一个。
func (s *Store) IssuePluginToken(ctx context.Context, plugin string, id uuid.UUID, hash []byte, scopes []string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE plugin_tokens SET revoked_at=now() WHERE plugin_id=$1 AND revoked_at IS NULL`, plugin); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO plugin_tokens(id,plugin_id,token_hash,scopes) VALUES($1,$2,$3,$4)`, id, plugin, hash, scopes)
		return err
	})
}

// LookupPluginToken 按 ID 查找插件令牌；不存在时返回 not_found。
func (s *Store) LookupPluginToken(ctx context.Context, id uuid.UUID) (PluginTokenRecord, error) {
	var t PluginTokenRecord
	err := s.Pool.QueryRow(ctx, `SELECT plugin_id,token_hash,scopes,revoked_at IS NOT NULL FROM plugin_tokens WHERE id=$1`, id).Scan(&t.PluginID, &t.Hash, &t.Scopes, &t.Revoked)
	return t, notFoundIfNoRows(err, "令牌")
}

// PluginSettings 是全局插件设置。
type PluginSettings struct {
	Disabled bool     `json:"disabled"`
	Markets  []string `json:"markets"`
}

// GetPluginSettings 读取全局插件设置。
func (s *Store) GetPluginSettings(ctx context.Context) (PluginSettings, error) {
	var out PluginSettings
	err := s.Pool.QueryRow(ctx, `SELECT disabled,markets FROM plugin_settings`).Scan(&out.Disabled, &out.Markets)
	return out, err
}

// PutPluginSettings 保存全局插件设置。
func (s *Store) PutPluginSettings(ctx context.Context, v PluginSettings) error {
	if v.Markets == nil {
		v.Markets = []string{}
	}
	_, err := s.Pool.Exec(ctx, `UPDATE plugin_settings SET disabled=$1, markets=$2, updated_at=now()`, v.Disabled, v.Markets)
	return err
}

// PluginLogEntry 是一行宿主日志。
type PluginLogEntry struct {
	ID    int64     `json:"id"`
	At    time.Time `json:"at"`
	Level string    `json:"level"`
	Line  string    `json:"line"`
}

// PluginLogs 返回插件的宿主日志，新的在前；before 为 0 时从最新开始。
func (s *Store) PluginLogs(ctx context.Context, plugin string, before int64, limit int) ([]PluginLogEntry, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,at,level,line FROM plugin_host_log WHERE plugin_id=$1 AND ($2=0 OR id<$2) ORDER BY id DESC LIMIT $3`, plugin, before, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PluginLogEntry, error) {
		var e PluginLogEntry
		return e, r.Scan(&e.ID, &e.At, &e.Level, &e.Line)
	})
}

// PluginDeliveryRow 是一条投递记录。
type PluginDeliveryRow struct {
	PluginEvent
	Status     string
	Attempts   int
	LastError  *string
	AckedAt    *time.Time
	DurationMs *int64
}

// PluginDeliveryQuery 是投递记录的筛选与分页；空值表示不筛选。
type PluginDeliveryQuery struct {
	Status  string
	Project *uuid.UUID
	Before  string
	Limit   int
}

// PluginDeliveries 返回插件的投递记录，按事件 id（即时间）新的在前。
func (s *Store) PluginDeliveries(ctx context.Context, plugin string, q PluginDeliveryQuery) ([]PluginDeliveryRow, error) {
	rows, err := s.Pool.Query(ctx, `SELECT e.id,e.type,e.at,e.project_id,e.agent_id,e.task_id,e.run_id,d.status,d.attempts,d.last_error,d.acked_at,d.duration_ms
		FROM plugin_event_deliveries d JOIN plugin_events e ON e.id=d.event_id
		WHERE d.plugin_id=$1 AND ($2='' OR d.status=$2) AND ($3::uuid IS NULL OR e.project_id=$3) AND ($4='' OR e.id<$4)
		ORDER BY e.id DESC LIMIT $5`, plugin, q.Status, q.Project, q.Before, q.Limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PluginDeliveryRow, error) {
		var d PluginDeliveryRow
		return d, r.Scan(&d.ID, &d.Type, &d.At, &d.ProjectID, &d.AgentID, &d.TaskID, &d.RunID, &d.Status, &d.Attempts, &d.LastError, &d.AckedAt, &d.DurationMs)
	})
}

// EmitPluginEvent 写一条观察类事件（事务外的改动，例如文件夹变化）。
func (s *Store) EmitPluginEvent(ctx context.Context, typ string, project, agent, task, run *uuid.UUID, data map[string]any) error {
	_, err := s.Pool.Exec(ctx, `SELECT plugin_emit($1,now(),$2,$3,$4,$5,$6)`, typ, project, agent, task, run, data)
	return err
}

// emitPluginEventTx 在当前事务里写一条观察类事件（经 plugin_emit，按订阅与启用生成投递）。
func emitPluginEventTx(ctx context.Context, tx pgx.Tx, typ string, project, agent, task, run *uuid.UUID, data map[string]any) error {
	_, err := tx.Exec(ctx, `SELECT plugin_emit($1,now(),$2,$3,$4,$5,$6)`, typ, project, agent, task, run, data)
	return err
}
