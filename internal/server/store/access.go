package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"loop-now/internal/server/apperr"
)

// ---------- 外部身份 ----------

// ExternalIdentity 是外部身份；DeletedAt 不为空表示已软删除。ManagedBy 是代管它的插件渠道（plugins/SPEC.md §12），没有时为空。
type ExternalIdentity struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
	DeletedAt *time.Time
	ManagedBy *IdentityManager
}

// IdentityManager 是代管外部身份的插件渠道。
type IdentityManager struct {
	PluginID  string `json:"pluginId"`
	ChannelID string `json:"channelId"`
}

const identityColumns = `id, name, created_at, deleted_at,
	(SELECT jsonb_build_object('pluginId', m.plugin_id, 'channelId', m.channel_id) FROM identity_managers m WHERE m.identity_id = external_identities.id)`

func scanIdentity(row pgx.Row) (ExternalIdentity, error) {
	var x ExternalIdentity
	err := row.Scan(&x.ID, &x.Name, &x.CreatedAt, &x.DeletedAt, &x.ManagedBy)
	return x, err
}

// SetIdentityManager 设置（m 为空时清空）代管外部身份的插件渠道；身份不存在或已删除时返回 not_found。插件与渠道由调用方校验。
func (s *Store) SetIdentityManager(ctx context.Context, identity uuid.UUID, m *IdentityManager) (ExternalIdentity, error) {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockLiveIdentity(ctx, tx, identity); err != nil {
			return notFoundAs(err, "外部身份")
		}
		if m == nil {
			_, err := tx.Exec(ctx, `DELETE FROM identity_managers WHERE identity_id=$1`, identity)
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO identity_managers(identity_id,plugin_id,channel_id) VALUES($1,$2,$3)
			ON CONFLICT (identity_id) DO UPDATE SET plugin_id=EXCLUDED.plugin_id, channel_id=EXCLUDED.channel_id, updated_at=now()`, identity, m.PluginID, m.ChannelID)
		return err
	})
	if err != nil {
		return ExternalIdentity{}, err
	}
	return s.GetExternalIdentity(ctx, identity)
}

// IdentityManagedBy 报告外部身份是不是由这个插件代管。
func (s *Store) IdentityManagedBy(ctx context.Context, identity uuid.UUID, plugin string) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM identity_managers WHERE identity_id=$1 AND plugin_id=$2)`, identity, plugin).Scan(&ok)
	return ok, err
}

// PruneIdentityManagers 清掉这个插件不再声明代管的渠道上的绑定（插件更新后）。
func (s *Store) PruneIdentityManagers(ctx context.Context, plugin string, channels []string) error {
	if channels == nil {
		channels = []string{}
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM identity_managers WHERE plugin_id=$1 AND NOT channel_id = ANY($2)`, plugin, channels)
	return err
}

// CreateExternalIdentity 新建外部身份；与未删除的身份重名时返回 name_conflict。名称格式由调用方校验。
func (s *Store) CreateExternalIdentity(ctx context.Context, name string) (ExternalIdentity, error) {
	x, err := scanIdentity(s.Pool.QueryRow(ctx, `INSERT INTO external_identities (id, name) VALUES ($1, $2) RETURNING `+identityColumns,
		uuid.Must(uuid.NewV7()), name))
	if isUniqueViolation(err, "external_identities_name_live") {
		return x, apperr.Conflict(apperr.CodeNameConflict, "已有名为 "+name+" 的外部身份")
	}
	return x, err
}

// ListExternalIdentities 列出未删除的外部身份，按创建时间正序。
func (s *Store) ListExternalIdentities(ctx context.Context) ([]ExternalIdentity, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+identityColumns+` FROM external_identities WHERE deleted_at IS NULL ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ExternalIdentity, error) { return scanIdentity(r) })
}

// GetExternalIdentity 返回外部身份，已删除的也返回。
func (s *Store) GetExternalIdentity(ctx context.Context, id uuid.UUID) (ExternalIdentity, error) {
	x, err := scanIdentity(s.Pool.QueryRow(ctx, `SELECT `+identityColumns+` FROM external_identities WHERE id = $1`, id))
	return x, notFoundIfNoRows(err, "外部身份")
}

// GetLiveExternalIdentityByName 按名称返回未删除的外部身份。
func (s *Store) GetLiveExternalIdentityByName(ctx context.Context, name string) (ExternalIdentity, error) {
	x, err := scanIdentity(s.Pool.QueryRow(ctx, `SELECT `+identityColumns+` FROM external_identities WHERE name = $1 AND deleted_at IS NULL`, name))
	return x, notFoundIfNoRows(err, "外部身份")
}

// DeleteExternalIdentity 软删除外部身份并吊销它的全部令牌；已删除或不存在时返回 not_found。
func (s *Store) DeleteExternalIdentity(ctx context.Context, id uuid.UUID) (ExternalIdentity, error) {
	var x ExternalIdentity
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		x, err = scanIdentity(tx.QueryRow(ctx, `UPDATE external_identities SET deleted_at = now()
			WHERE id = $1 AND deleted_at IS NULL RETURNING `+identityColumns, id))
		if err != nil {
			return notFoundIfNoRows(err, "外部身份")
		}
		_, err = tx.Exec(ctx, `UPDATE external_tokens SET revoked_at = now() WHERE identity_id = $1 AND revoked_at IS NULL`, id)
		return err
	})
	return x, err
}

// ---------- 外部令牌 ----------

// ExternalToken 是外部令牌的元数据（不含密钥）。
type ExternalToken struct {
	ID         uuid.UUID
	IdentityID uuid.UUID
	Prefix     string
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
	LastUsedAt *time.Time
}

const tokenColumns = `id, identity_id, display_prefix, created_at, expires_at, revoked_at, last_used_at`

func scanToken(row pgx.Row) (ExternalToken, error) {
	var t ExternalToken
	err := row.Scan(&t.ID, &t.IdentityID, &t.Prefix, &t.CreatedAt, &t.ExpiresAt, &t.RevokedAt, &t.LastUsedAt)
	return t, err
}

// lockLiveIdentity 以 FOR SHARE 锁住未删除的外部身份，与删除身份串行化。
func lockLiveIdentity(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	var one int
	err := tx.QueryRow(ctx, `SELECT 1 FROM external_identities WHERE id = $1 AND deleted_at IS NULL FOR SHARE`, id).Scan(&one)
	return notFoundIfNoRows(err, "外部身份")
}

// InsertExternalToken 为未删除的外部身份保存令牌；身份已删除或不存在时返回 not_found。
func (s *Store) InsertExternalToken(ctx context.Context, identityID, tokenID uuid.UUID, hash []byte, prefix string, expiresAt *time.Time) (ExternalToken, error) {
	var t ExternalToken
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockLiveIdentity(ctx, tx, identityID); err != nil {
			return err
		}
		var err error
		t, err = scanToken(tx.QueryRow(ctx, `INSERT INTO external_tokens (id, identity_id, secret_hash, display_prefix, expires_at)
			VALUES ($1, $2, $3, $4, $5) RETURNING `+tokenColumns, tokenID, identityID, hash, prefix, expiresAt))
		return err
	})
	return t, err
}

// ListExternalTokens 列出未删除外部身份的全部令牌（含已吊销与已过期），新的在前。
func (s *Store) ListExternalTokens(ctx context.Context, identityID uuid.UUID) ([]ExternalToken, error) {
	var out []ExternalToken
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockLiveIdentity(ctx, tx, identityID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+tokenColumns+` FROM external_tokens WHERE identity_id = $1 ORDER BY created_at DESC, id DESC`, identityID)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ExternalToken, error) { return scanToken(r) })
		return err
	})
	return out, err
}

// RevokeExternalToken 吊销令牌；已吊销时保持原吊销时间。身份已删除、令牌不存在或不属于该身份时返回 not_found。
func (s *Store) RevokeExternalToken(ctx context.Context, identityID, tokenID uuid.UUID) (ExternalToken, error) {
	var t ExternalToken
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockLiveIdentity(ctx, tx, identityID); err != nil {
			return err
		}
		var err error
		t, err = scanToken(tx.QueryRow(ctx, `UPDATE external_tokens SET revoked_at = coalesce(revoked_at, now())
			WHERE id = $1 AND identity_id = $2 RETURNING `+tokenColumns, tokenID, identityID))
		return notFoundIfNoRows(err, "令牌")
	})
	return t, err
}

// ExternalTokenAuth 是校验外部令牌所需的数据。
type ExternalTokenAuth struct {
	Token    ExternalToken
	Hash     []byte
	Identity ExternalIdentity
}

// LookupExternalToken 按 ID 读取令牌与所属身份；不存在时返回 not_found。是否有效由调用方判断。
func (s *Store) LookupExternalToken(ctx context.Context, tokenID uuid.UUID) (ExternalTokenAuth, error) {
	var a ExternalTokenAuth
	t, x := &a.Token, &a.Identity
	err := s.Pool.QueryRow(ctx, `SELECT t.id, t.identity_id, t.display_prefix, t.created_at, t.expires_at, t.revoked_at, t.last_used_at, t.secret_hash,
			i.id, i.name, i.created_at, i.deleted_at
		FROM external_tokens t JOIN external_identities i ON i.id = t.identity_id WHERE t.id = $1`, tokenID).
		Scan(&t.ID, &t.IdentityID, &t.Prefix, &t.CreatedAt, &t.ExpiresAt, &t.RevokedAt, &t.LastUsedAt, &a.Hash,
			&x.ID, &x.Name, &x.CreatedAt, &x.DeletedAt)
	return a, notFoundIfNoRows(err, "令牌")
}

// TouchExternalToken 记录令牌的最近使用时间，最多每分钟更新一次。
func (s *Store) TouchExternalToken(ctx context.Context, tokenID uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `UPDATE external_tokens SET last_used_at = now()
		WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute')`, tokenID)
	return err
}

// ---------- Agent 令牌 ----------

// InsertAgentToken 保存为某次 Pi 进程签发的 Agent 令牌。
func (s *Store) InsertAgentToken(ctx context.Context, tokenID, agentID, instanceID uuid.UUID, hash []byte) error {
	inst, err := s.TaskInstance(ctx, agentID)
	if err != nil && !IsNotFound(err) {
		return err
	}
	if err == nil {
		if inst.Retired {
			return apperr.Forbidden("任务实例已经回收")
		}
		_, err = s.Pool.Exec(ctx, `INSERT INTO agent_tokens(id,agent_id,instance_id,secret_hash,runtime_id,task_id) VALUES($1,$2,$3,$4,$5,$6)`, tokenID, inst.AgentID, instanceID, hash, inst.ID, inst.TaskID)
		return err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO agent_tokens (id, agent_id, instance_id, secret_hash) VALUES ($1, $2, $3, $4)`, tokenID, agentID, instanceID, hash)
	return err
}

// RevokeAgentToken 作废 Agent 令牌；已作废时不变。
func (s *Store) RevokeAgentToken(ctx context.Context, tokenID uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `UPDATE agent_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, tokenID)
	return err
}

// RevokeAgentTokensNotFrom 作废不是由指定实例签发的全部 Agent 令牌（服务端启动时调用），返回作废的数量。
func (s *Store) RevokeAgentTokensNotFrom(ctx context.Context, instanceID uuid.UUID) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE agent_tokens SET revoked_at = now() WHERE revoked_at IS NULL AND instance_id <> $1`, instanceID)
	return tag.RowsAffected(), err
}

// AgentTokenAuth 是校验 Agent 令牌所需的数据。
type AgentTokenAuth struct {
	RuntimeID, TaskID *uuid.UUID
	AgentID           uuid.UUID
	InstanceID        uuid.UUID
	Hash              []byte
	Revoked           bool
}

// LookupAgentToken 按 ID 读取 Agent 令牌；不存在时返回 not_found。
func (s *Store) LookupAgentToken(ctx context.Context, tokenID uuid.UUID) (AgentTokenAuth, error) {
	var a AgentTokenAuth
	err := s.Pool.QueryRow(ctx, `SELECT t.agent_id,t.instance_id,t.secret_hash,(t.revoked_at IS NOT NULL OR coalesce(i.retired,false)),t.runtime_id,t.task_id FROM agent_tokens t LEFT JOIN task_instances i ON i.id=t.runtime_id WHERE t.id=$1`, tokenID).
		Scan(&a.AgentID, &a.InstanceID, &a.Hash, &a.Revoked, &a.RuntimeID, &a.TaskID)
	return a, notFoundIfNoRows(err, "令牌")
}

// ---------- 审计 ----------

// AuditEntry 是一条审计记录；不含请求体、凭证与令牌。
type AuditEntry struct {
	ID           uuid.UUID
	At           time.Time
	RequestID    *uuid.UUID // 服务端命令行的操作没有请求 ID
	ActorKind    string     // console / external / agent / cli / plugin
	ActorID      *uuid.UUID
	OnBehalfOf   *uuid.UUID // 插件以代管的外部身份的名义调用时，那个身份
	ActorAddress string
	Method       string // HTTP 方法；命令行为 CLI
	Operation    string // 契约中的 operationId；命令行为子命令，例如 identity.create
	Target       map[string]string
	Status       int // HTTP 状态码；命令行成功为 0
}

// InsertAudit 写入一条审计记录。
func (s *Store) InsertAudit(ctx context.Context, e AuditEntry) error {
	if e.Target == nil {
		e.Target = map[string]string{}
	}
	target, err := json.Marshal(e.Target)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO audit_log (id, request_id, actor_kind, actor_id, actor_address, method, operation, target, status, on_behalf_of)
		VALUES ($1, $2, $3, $4, nullif($5, ''), $6, $7, $8, $9, $10)`,
		uuid.Must(uuid.NewV7()), e.RequestID, e.ActorKind, e.ActorID, e.ActorAddress, e.Method, e.Operation, target, e.Status, e.OnBehalfOf)
	return err
}

// ListAudit 返回最近的审计记录，新的在前（测试与排查用）。
func (s *Store) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, at, request_id, actor_kind, actor_id, coalesce(actor_address, ''), method, operation, target, status
		FROM audit_log ORDER BY at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AuditEntry, error) {
		var e AuditEntry
		var target []byte
		if err := r.Scan(&e.ID, &e.At, &e.RequestID, &e.ActorKind, &e.ActorID, &e.ActorAddress, &e.Method, &e.Operation, &target, &e.Status); err != nil {
			return e, err
		}
		if err := json.Unmarshal(target, &e.Target); err != nil {
			return e, errors.Join(errors.New("审计目标不是 JSON"), err)
		}
		return e, nil
	})
}
