package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/db"
)

// ---------- External identities ----------

// ExternalIdentity is an external identity; a non-nil DeletedAt means it has been soft-deleted. ManagedBy is the
// plugin channel that manages it (plugins/SPEC.md §12), or empty when there is none.
type ExternalIdentity struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
	DeletedAt *time.Time
	ManagedBy *IdentityManager
}

// IdentityManager is the plugin channel that manages an external identity.
type IdentityManager struct {
	PluginID  string `json:"pluginId"`
	ChannelID string `json:"channelId"`
}

const identityColumns = `id, name, created_at, deleted_at,
	(SELECT json_object('pluginId', m.plugin_id, 'channelId', m.channel_id) FROM identity_managers m WHERE m.identity_id = external_identities.id)`

func scanIdentity(row db.Scanner) (ExternalIdentity, error) {
	var x ExternalIdentity
	err := row.Scan(&x.ID, &x.Name, &x.CreatedAt, &x.DeletedAt, &x.ManagedBy)
	return x, err
}

// SetIdentityManager sets the plugin channel that manages an external identity (clears it when m is nil); returns
// not_found when the identity does not exist or has been deleted. The caller validates the plugin and channel.
func (s *Store) SetIdentityManager(ctx context.Context, identity uuid.UUID, m *IdentityManager) (ExternalIdentity, error) {
	err := s.inTx(ctx, func(tx *db.Tx) error {
		if err := requireLiveIdentity(ctx, tx, identity); err != nil {
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

// IdentityManagedBy reports whether the external identity is managed by this plugin.
func (s *Store) IdentityManagedBy(ctx context.Context, identity uuid.UUID, plugin string) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM identity_managers WHERE identity_id=$1 AND plugin_id=$2)`, identity, plugin).Scan(&ok)
	return ok, err
}

// PruneIdentityManagers removes the bindings on channels this plugin no longer declares it manages (after a plugin
// update).
func (s *Store) PruneIdentityManagers(ctx context.Context, plugin string, channels []string) error {
	if channels == nil {
		channels = []string{}
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM identity_managers WHERE plugin_id=$1 AND NOT channel_id  IN (SELECT value FROM json_each($2))`, plugin, channels)
	return err
}

// CreateExternalIdentity creates an external identity; returns name_conflict when the name clashes with an identity
// that has not been deleted. The caller validates the name format.
func (s *Store) CreateExternalIdentity(ctx context.Context, name string) (ExternalIdentity, error) {
	x, err := scanIdentity(s.Pool.QueryRow(ctx, `INSERT INTO external_identities (id, name) VALUES ($1, $2) RETURNING `+identityColumns,
		uuid.Must(uuid.NewV7()), name))
	if isUniqueViolation(err, "external_identities_name_live") {
		return x, apperr.Conflict(apperr.CodeNameConflict, "已有名为 "+name+" 的外部身份")
	}
	return x, err
}

// ListExternalIdentities lists the external identities that have not been deleted, oldest first.
func (s *Store) ListExternalIdentities(ctx context.Context) ([]ExternalIdentity, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+identityColumns+` FROM external_identities WHERE deleted_at IS NULL ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(r db.CollectableRow) (ExternalIdentity, error) { return scanIdentity(r) })
}

// GetExternalIdentity returns an external identity, including a deleted one.
func (s *Store) GetExternalIdentity(ctx context.Context, id uuid.UUID) (ExternalIdentity, error) {
	x, err := scanIdentity(s.Pool.QueryRow(ctx, `SELECT `+identityColumns+` FROM external_identities WHERE id = $1`, id))
	return x, notFoundIfNoRows(err, "外部身份")
}

// GetLiveExternalIdentityByName returns the non-deleted external identity with this name.
func (s *Store) GetLiveExternalIdentityByName(ctx context.Context, name string) (ExternalIdentity, error) {
	x, err := scanIdentity(s.Pool.QueryRow(ctx, `SELECT `+identityColumns+` FROM external_identities WHERE name = $1 AND deleted_at IS NULL`, name))
	return x, notFoundIfNoRows(err, "外部身份")
}

// DeleteExternalIdentity soft-deletes an external identity and revokes all of its tokens; returns not_found when it is
// already deleted or does not exist.
func (s *Store) DeleteExternalIdentity(ctx context.Context, id uuid.UUID) (ExternalIdentity, error) {
	var x ExternalIdentity
	err := s.inTx(ctx, func(tx *db.Tx) error {
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

// ---------- External tokens ----------

// ExternalToken is the metadata of an external token (without the secret).
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

func scanToken(row db.Scanner) (ExternalToken, error) {
	var t ExternalToken
	err := row.Scan(&t.ID, &t.IdentityID, &t.Prefix, &t.CreatedAt, &t.ExpiresAt, &t.RevokedAt, &t.LastUsedAt)
	return t, err
}

// requireLiveIdentity confirms that the external identity exists and is not deleted (write transactions are
// serialized, so the row does not need to be locked).
func requireLiveIdentity(ctx context.Context, tx *db.Tx, id uuid.UUID) error {
	var one int
	err := tx.QueryRow(ctx, `SELECT 1 FROM external_identities WHERE id = $1 AND deleted_at IS NULL`, id).Scan(&one)
	return notFoundIfNoRows(err, "外部身份")
}

// InsertExternalToken stores a token for a non-deleted external identity; returns not_found when the identity is
// deleted or does not exist.
func (s *Store) InsertExternalToken(ctx context.Context, identityID, tokenID uuid.UUID, hash []byte, prefix string, expiresAt *time.Time) (ExternalToken, error) {
	var t ExternalToken
	err := s.inTx(ctx, func(tx *db.Tx) error {
		if err := requireLiveIdentity(ctx, tx, identityID); err != nil {
			return err
		}
		var err error
		t, err = scanToken(tx.QueryRow(ctx, `INSERT INTO external_tokens (id, identity_id, secret_hash, display_prefix, expires_at)
			VALUES ($1, $2, $3, $4, $5) RETURNING `+tokenColumns, tokenID, identityID, hash, prefix, expiresAt))
		return err
	})
	return t, err
}

// ListExternalTokens lists all tokens of a non-deleted external identity (including revoked and expired ones),
// newest first.
func (s *Store) ListExternalTokens(ctx context.Context, identityID uuid.UUID) ([]ExternalToken, error) {
	var out []ExternalToken
	err := s.inTx(ctx, func(tx *db.Tx) error {
		if err := requireLiveIdentity(ctx, tx, identityID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+tokenColumns+` FROM external_tokens WHERE identity_id = $1 ORDER BY created_at DESC, id DESC`, identityID)
		if err != nil {
			return err
		}
		out, err = db.CollectRows(rows, func(r db.CollectableRow) (ExternalToken, error) { return scanToken(r) })
		return err
	})
	return out, err
}

// RevokeExternalToken revokes a token; an already revoked token keeps its original revocation time. Returns not_found
// when the identity is deleted, or the token does not exist or does not belong to that identity.
func (s *Store) RevokeExternalToken(ctx context.Context, identityID, tokenID uuid.UUID) (ExternalToken, error) {
	var t ExternalToken
	err := s.inTx(ctx, func(tx *db.Tx) error {
		if err := requireLiveIdentity(ctx, tx, identityID); err != nil {
			return err
		}
		var err error
		t, err = scanToken(tx.QueryRow(ctx, `UPDATE external_tokens SET revoked_at = coalesce(revoked_at, now())
			WHERE id = $1 AND identity_id = $2 RETURNING `+tokenColumns, tokenID, identityID))
		return notFoundIfNoRows(err, "令牌")
	})
	return t, err
}

// ExternalTokenAuth is the data needed to check an external token.
type ExternalTokenAuth struct {
	Token    ExternalToken
	Hash     []byte
	Identity ExternalIdentity
}

// LookupExternalToken reads a token and the identity it belongs to by ID; returns not_found when it does not exist.
// The caller decides whether it is valid.
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

// TouchExternalToken records when the token was last used, updating it at most once a minute.
func (s *Store) TouchExternalToken(ctx context.Context, tokenID uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `UPDATE external_tokens SET last_used_at = now()
		WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - 60000000)`, tokenID)
	return err
}

// ---------- Agent tokens ----------

// InsertAgentToken stores an Agent token issued for one Pi process.
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

// RevokeAgentToken revokes an Agent token; does nothing when it is already revoked.
func (s *Store) RevokeAgentToken(ctx context.Context, tokenID uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `UPDATE agent_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, tokenID)
	return err
}

// RevokeAgentTokensNotFrom revokes every Agent token not issued by the given instance (called at server startup) and
// returns how many were revoked.
func (s *Store) RevokeAgentTokensNotFrom(ctx context.Context, instanceID uuid.UUID) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE agent_tokens SET revoked_at = now() WHERE revoked_at IS NULL AND instance_id <> $1`, instanceID)
	return tag.RowsAffected(), err
}

// AgentTokenAuth is the data needed to check an Agent token.
type AgentTokenAuth struct {
	RuntimeID, TaskID *uuid.UUID
	AgentID           uuid.UUID
	InstanceID        uuid.UUID
	Hash              []byte
	Revoked           bool
}

// LookupAgentToken reads an Agent token by ID; returns not_found when it does not exist.
func (s *Store) LookupAgentToken(ctx context.Context, tokenID uuid.UUID) (AgentTokenAuth, error) {
	var a AgentTokenAuth
	err := s.Pool.QueryRow(ctx, `SELECT t.agent_id,t.instance_id,t.secret_hash,(t.revoked_at IS NOT NULL OR coalesce(i.retired,false)),t.runtime_id,t.task_id FROM agent_tokens t LEFT JOIN task_instances i ON i.id=t.runtime_id WHERE t.id=$1`, tokenID).
		Scan(&a.AgentID, &a.InstanceID, &a.Hash, &a.Revoked, &a.RuntimeID, &a.TaskID)
	return a, notFoundIfNoRows(err, "令牌")
}

// ---------- Audit ----------

// AuditEntry is one audit record; it contains no request body, credentials or tokens.
type AuditEntry struct {
	ID           uuid.UUID
	At           time.Time
	RequestID    *uuid.UUID // Operations from the server command line have no request ID
	ActorKind    string     // console / external / agent / cli / plugin
	ActorID      *uuid.UUID
	OnBehalfOf   *uuid.UUID // When a plugin calls on behalf of an external identity it manages, that identity
	ActorAddress string
	Method       string // HTTP method; CLI for the command line
	Operation    string // operationId in the contract; for the command line, the subcommand, e.g. identity.create
	Target       map[string]string
	Status       int // HTTP status code; 0 for a successful command-line operation
}

// InsertAudit writes one audit record.
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
		uuid.Must(uuid.NewV7()), e.RequestID, e.ActorKind, e.ActorID, e.ActorAddress, e.Method, e.Operation, json.RawMessage(target), e.Status, e.OnBehalfOf)
	return err
}

// ListAudit returns the most recent audit records, newest first (for tests and troubleshooting).
func (s *Store) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, at, request_id, actor_kind, actor_id, coalesce(actor_address, ''), method, operation, target, status
		FROM audit_log ORDER BY at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(r db.CollectableRow) (AuditEntry, error) {
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
