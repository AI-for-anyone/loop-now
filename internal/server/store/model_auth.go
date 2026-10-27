package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"loop-now/internal/server/apperr"
)

// ErrModelAuthBusy indicates an Agent already has a nonterminal login operation.
var ErrModelAuthBusy = errors.New("model authentication operation already active")

// ModelAuthVersion is an immutable account identity with coordinated mutable tokens.
type ModelAuthVersion struct {
	ID, AgentID          uuid.UUID
	ProjectAccountID     *uuid.UUID
	Provider, AuthMethod string
	AccountLabel         *string
	Secret               []byte
	Generation           int64
	RefreshState         string
	RefreshAttemptID     *uuid.UUID
	RefreshStartedAt     *time.Time
	ErrorCode            *string
	CreatedAt, UpdatedAt time.Time
}

// ModelAuthOperation contains login lifecycle state, never PKCE, codes or tokens.
type ModelAuthOperation struct {
	ID, AgentID                 uuid.UUID
	Provider                    string
	ConfigVersion               int64
	Status                      string
	AuthorizationURL, ErrorCode *string
	StartedAt, ExpiresAt        time.Time
	CompletedAt                 *time.Time
}

const modelAuthVersionColumns = `id,coalesce(agent_id,'00000000-0000-0000-0000-000000000000'::uuid),provider,auth_method,account_label,secret,generation,refresh_state,refresh_attempt_id,refresh_started_at,error_code,created_at,updated_at,project_account_id`
const modelAuthOperationColumns = `id,agent_id,provider,config_version,status,authorization_url,error_code,started_at,expires_at,completed_at`
const modelAuthActive = `('pending','awaiting_authorization')`

func scanModelAuthVersion(row pgx.Row) (ModelAuthVersion, error) {
	var v ModelAuthVersion
	err := row.Scan(&v.ID, &v.AgentID, &v.Provider, &v.AuthMethod, &v.AccountLabel, &v.Secret, &v.Generation, &v.RefreshState, &v.RefreshAttemptID, &v.RefreshStartedAt, &v.ErrorCode, &v.CreatedAt, &v.UpdatedAt, &v.ProjectAccountID)
	return v, err
}
func scanModelAuthOperation(row pgx.Row) (ModelAuthOperation, error) {
	var o ModelAuthOperation
	err := row.Scan(&o.ID, &o.AgentID, &o.Provider, &o.ConfigVersion, &o.Status, &o.AuthorizationURL, &o.ErrorCode, &o.StartedAt, &o.ExpiresAt, &o.CompletedAt)
	return o, err
}
func modelAuthTerminal(status string) bool {
	return status != "pending" && status != "awaiting_authorization"
}
func validateModelAuthAgent(a Agent, provider string, expected *int64) error {
	if expected != nil && a.ConfigVersion != *expected {
		return apperr.PreconditionFailed("Agent 配置已更改，请重新读取")
	}
	if a.ModelProvider != provider {
		return apperr.Invalid("provider", "提供方与 Agent 当前模型不一致")
	}
	return nil
}

func (s *Store) NewModelAuthOperation(ctx context.Context, agentID uuid.UUID, provider string, expectedConfigVersion int64, expiresAt time.Time) (ModelAuthOperation, error) {
	var o ModelAuthOperation
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		a, err := lockAgent(ctx, tx, agentID)
		if err != nil {
			return err
		}
		if a.ProjectID != nil {
			return projectCredentialRequired(a)
		}
		if err = validateModelAuthAgent(a, provider, &expectedConfigVersion); err != nil {
			return err
		}
		o, err = scanModelAuthOperation(tx.QueryRow(ctx, `INSERT INTO model_auth_operations(id,agent_id,provider,config_version,expires_at) VALUES($1,$2,$3,$4,$5) RETURNING `+modelAuthOperationColumns, uuid.Must(uuid.NewV7()), agentID, provider, expectedConfigVersion, expiresAt))
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" && pe.ConstraintName == "model_auth_operations_one_active" {
			return ErrModelAuthBusy
		}
		return err
	})
	return o, err
}
func (s *Store) GetModelAuthOperation(ctx context.Context, agentID, operationID uuid.UUID) (ModelAuthOperation, error) {
	o, err := scanModelAuthOperation(s.Pool.QueryRow(ctx, `SELECT `+modelAuthOperationColumns+` FROM model_auth_operations WHERE id=$1 AND agent_id=$2`, operationID, agentID))
	return o, notFoundIfNoRows(err, "模型登录操作")
}
func (s *Store) LatestModelAuthOperation(ctx context.Context, agentID uuid.UUID) (*ModelAuthOperation, error) {
	o, err := scanModelAuthOperation(s.Pool.QueryRow(ctx, `SELECT `+modelAuthOperationColumns+` FROM model_auth_operations WHERE agent_id=$1 ORDER BY started_at DESC,id DESC LIMIT 1`, agentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}
func (s *Store) SetModelAuthOperationURL(ctx context.Context, operationID uuid.UUID, url string) (ModelAuthOperation, error) {
	var o ModelAuthOperation
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		o, err = scanModelAuthOperation(tx.QueryRow(ctx, `SELECT `+modelAuthOperationColumns+` FROM model_auth_operations WHERE id=$1 FOR UPDATE`, operationID))
		if err != nil {
			return notFoundIfNoRows(err, "模型登录操作")
		}
		if modelAuthTerminal(o.Status) {
			return nil
		}
		o, err = scanModelAuthOperation(tx.QueryRow(ctx, `UPDATE model_auth_operations SET status='awaiting_authorization',authorization_url=$2 WHERE id=$1 RETURNING `+modelAuthOperationColumns, operationID, url))
		return err
	})
	return o, err
}
func endModelAuthOperation(ctx context.Context, tx pgx.Tx, id uuid.UUID, status, code string) (ModelAuthOperation, error) {
	return scanModelAuthOperation(tx.QueryRow(ctx, `UPDATE model_auth_operations SET status=$2,error_code=$3,authorization_url=NULL,completed_at=clock_timestamp() WHERE id=$1 RETURNING `+modelAuthOperationColumns, id, status, nullIfEmpty(code)))
}
func (s *Store) EndModelAuthOperation(ctx context.Context, operationID uuid.UUID, status, code string) (ModelAuthOperation, error) {
	if status != "failed" && status != "cancelled" && status != "expired" {
		return ModelAuthOperation{}, apperr.Invalid("status", "非法登录终态")
	}
	var o ModelAuthOperation
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		o, err = scanModelAuthOperation(tx.QueryRow(ctx, `SELECT `+modelAuthOperationColumns+` FROM model_auth_operations WHERE id=$1 FOR UPDATE`, operationID))
		if err != nil {
			return notFoundIfNoRows(err, "模型登录操作")
		}
		if modelAuthTerminal(o.Status) {
			return nil
		}
		o, err = endModelAuthOperation(ctx, tx, operationID, status, code)
		return err
	})
	return o, err
}

// insertModelAuthVersion stores a new account version before switching the pointer.
func insertModelAuthVersion(ctx context.Context, tx pgx.Tx, agentID uuid.UUID, provider, method string, label *string, secret []byte) (ModelAuthVersion, error) {
	id := uuid.Must(uuid.NewV7())
	return scanModelAuthVersion(tx.QueryRow(ctx, `INSERT INTO model_auth_versions(id,agent_id,provider,auth_method,account_label,secret) VALUES($1,$2,$3,$4,$5,$6) RETURNING `+modelAuthVersionColumns, id, agentID, provider, method, label, secret))
}
func setModelAuthPointer(ctx context.Context, tx pgx.Tx, agentID uuid.UUID, versionID *uuid.UUID) error {
	_, err := tx.Exec(ctx, `INSERT INTO agent_model_auth(agent_id,version_id) VALUES($1,$2) ON CONFLICT(agent_id) DO UPDATE SET version_id=excluded.version_id,updated_at=clock_timestamp()`, agentID, versionID)
	if err != nil {
		return err
	}
	return bumpConfig(ctx, tx, agentID)
}
func (s *Store) CompleteModelAuthOperation(ctx context.Context, operationID uuid.UUID, accountLabel *string, secret []byte) (ModelAuthOperation, *ModelAuthVersion, error) {
	var o ModelAuthOperation
	var v *ModelAuthVersion
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var agentID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT agent_id FROM model_auth_operations WHERE id=$1`, operationID).Scan(&agentID); err != nil {
			return notFoundIfNoRows(err, "模型登录操作")
		}
		// Include soft-deleted Agents so a late completion can safely terminate without credentials.
		var provider string
		var config int64
		var deleted *time.Time
		var project *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT model_provider,config_version,deleted_at,project_id FROM agents WHERE id=$1 FOR UPDATE`, agentID).Scan(&provider, &config, &deleted, &project); err != nil {
			return notFoundIfNoRows(err, "Agent")
		}
		var err error
		o, err = scanModelAuthOperation(tx.QueryRow(ctx, `SELECT `+modelAuthOperationColumns+` FROM model_auth_operations WHERE id=$1 FOR UPDATE`, operationID))
		if err != nil {
			return err
		}
		if modelAuthTerminal(o.Status) {
			return nil
		}
		var expired bool
		if err = tx.QueryRow(ctx, `SELECT $1::timestamptz <= clock_timestamp()`, o.ExpiresAt).Scan(&expired); err != nil {
			return err
		}
		if expired {
			o, err = endModelAuthOperation(ctx, tx, operationID, "expired", "auth_timeout")
			return err
		}
		if project != nil || deleted != nil || provider != o.Provider || config != o.ConfigVersion {
			o, err = endModelAuthOperation(ctx, tx, operationID, "failed", "configuration_changed")
			return err
		}
		saved, err := insertModelAuthVersion(ctx, tx, agentID, o.Provider, "oauth", accountLabel, secret)
		if err != nil {
			return err
		}
		if err = setModelAuthPointer(ctx, tx, agentID, &saved.ID); err != nil {
			return err
		}
		o, err = endModelAuthOperation(ctx, tx, operationID, "succeeded", "")
		if err == nil {
			v = &saved
		}
		return err
	})
	if err != nil {
		v = nil
	}
	return o, v, err
}
func (s *Store) PutModelAuthKey(ctx context.Context, agentID uuid.UUID, provider string, expectedConfigVersion *int64, secret []byte) (ModelAuthVersion, error) {
	var v ModelAuthVersion
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		a, err := lockAgent(ctx, tx, agentID)
		if err != nil {
			return err
		}
		if a.ProjectID != nil {
			return projectCredentialRequired(a)
		}
		if err = validateModelAuthAgent(a, provider, expectedConfigVersion); err != nil {
			return err
		}
		v, err = insertModelAuthVersion(ctx, tx, agentID, provider, "api_key", nil, secret)
		if err != nil {
			return err
		}
		return setModelAuthPointer(ctx, tx, agentID, &v.ID)
	})
	return v, err
}
func (s *Store) CurrentModelAuthVersion(ctx context.Context, agentID uuid.UUID) (*ModelAuthVersion, bool, error) {
	var id *uuid.UUID
	err := s.Pool.QueryRow(ctx, `SELECT version_id FROM agent_model_auth WHERE agent_id=$1`, agentID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if id == nil {
		return nil, true, nil
	}
	// Versions are retained: replacing/clearing the pointer cannot change this account identity.
	v, err := s.GetModelAuthVersion(ctx, *id)
	if err != nil {
		return nil, true, err
	}
	return &v, true, nil
}
func (s *Store) GetModelAuthVersion(ctx context.Context, versionID uuid.UUID) (ModelAuthVersion, error) {
	v, err := scanModelAuthVersion(s.Pool.QueryRow(ctx, `SELECT `+modelAuthVersionColumns+` FROM model_auth_versions WHERE id=$1`, versionID))
	return v, notFoundIfNoRows(err, "模型账号版本")
}
func (s *Store) ClearModelAuthVersion(ctx context.Context, agentID uuid.UUID) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if a, err := lockAgent(ctx, tx, agentID); err != nil {
			return err
		} else if a.ProjectID != nil {
			return projectCredentialRequired(a)
		}
		return setModelAuthPointer(ctx, tx, agentID, nil)
	})
}
func (s *Store) ClaimModelAuthRefresh(ctx context.Context, versionID uuid.UUID, generation int64, attemptID uuid.UUID) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE model_auth_versions SET refresh_state='refreshing',refresh_attempt_id=$3,refresh_started_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1 AND generation=$2 AND refresh_state='ready'`, versionID, generation, attemptID)
	return tag.RowsAffected() == 1, err
}
func (s *Store) CommitModelAuthRefresh(ctx context.Context, versionID, attemptID uuid.UUID, secret []byte) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE model_auth_versions SET secret=$3,generation=generation+1,refresh_state='ready',refresh_attempt_id=NULL,refresh_started_at=NULL,error_code=NULL,updated_at=clock_timestamp() WHERE id=$1 AND refresh_state='refreshing' AND refresh_attempt_id=$2`, versionID, attemptID, secret)
	return tag.RowsAffected() == 1, err
}
func (s *Store) FailModelAuthRefresh(ctx context.Context, versionID, attemptID uuid.UUID, code string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE model_auth_versions SET refresh_state='unavailable',refresh_attempt_id=NULL,refresh_started_at=NULL,error_code=$3,updated_at=clock_timestamp() WHERE id=$1 AND refresh_state='refreshing' AND refresh_attempt_id=$2`, versionID, attemptID, nullIfEmpty(code))
	return err
}
func (s *Store) RecoverModelAuth(ctx context.Context) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE model_auth_operations SET status='failed',error_code='server_restarted',authorization_url=NULL,completed_at=clock_timestamp() WHERE status IN `+modelAuthActive); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE project_model_auth_operations SET status='failed',error_code='server_restarted',authorization_url=NULL,completed_at=clock_timestamp() WHERE status IN ('pending','awaiting_authorization')`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE model_auth_versions SET refresh_state='unavailable',refresh_attempt_id=NULL,refresh_started_at=NULL,error_code='refresh_uncertain',updated_at=clock_timestamp() WHERE refresh_state='refreshing'`)
		return err
	})
}
