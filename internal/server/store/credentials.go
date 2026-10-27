package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/clicredential"
	cm "loop-now/internal/server/credentialmodel"
)

// 凭证用途。
const (
	PurposeModel = "model"
	PurposeCLI   = "cli"
)

// CredentialRow 是一条凭证（明文）及其元数据。
type CredentialRow struct {
	LarkBinding     *clicredential.LarkBinding
	GoogleBinding   *clicredential.Binding
	DBConnection    *cm.DBConnection // internal capture metadata, never credential values
	ID              uuid.UUID
	AgentID         uuid.UUID
	Purpose         string
	CLIName         string
	TemplateVersion string
	TemplateSource  string
	Secret          []byte
	FieldNames      []string
	EnvNames        []string
	FilePaths       []string
	UpdatedAt       time.Time
}

// CredentialMeta 是写入时附带的元数据。
type CredentialMeta struct {
	TemplateVersion string
	FieldNames      []string
	EnvNames        []string
	FilePaths       []string
}

// ListAgentCLIs 返回 Agent 的可用 CLI 名称。
func (s *Store) ListAgentCLIs(ctx context.Context, agentID uuid.UUID) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT cli_name FROM agent_clis WHERE agent_id = $1 ORDER BY cli_name`, agentID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// AddAgentCLI 把 CLI 加入可用列表；已存在时不变。
func (s *Store) AddAgentCLI(ctx context.Context, agentID uuid.UUID, name string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockAgentProjectTx(ctx, tx, agentID); err != nil {
			return err
		}
		if _, err := lockAgent(ctx, tx, agentID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO agent_clis (agent_id, cli_name) VALUES ($1, $2) ON CONFLICT DO NOTHING`, agentID, name)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		return bumpConfig(ctx, tx, agentID)
	})
}

// RemoveAgentCLI 从可用列表移除 CLI，其凭证经外键级联删除。
func (s *Store) RemoveAgentCLI(ctx context.Context, agentID uuid.UUID, name string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockAgentProjectTx(ctx, tx, agentID); err != nil {
			return err
		}
		if _, err := lockAgent(ctx, tx, agentID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM agent_credential_selections WHERE agent_id=$1 AND purpose='cli' AND slot_key=$2`, agentID, name); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM agent_clis WHERE agent_id = $1 AND cli_name = $2`, agentID, name)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.NotFound("可用 CLI " + name)
		}
		return bumpConfig(ctx, tx, agentID)
	})
}

const credentialColumns = `id, agent_id, purpose, coalesce(cli_name, ''), coalesce(template_version, ''), coalesce(template_source, ''), secret,
	field_names, env_names, file_paths, updated_at`

func scanCredential(row pgx.Row) (CredentialRow, error) {
	var c CredentialRow
	err := row.Scan(&c.ID, &c.AgentID, &c.Purpose, &c.CLIName, &c.TemplateVersion, &c.TemplateSource, &c.Secret,
		&c.FieldNames, &c.EnvNames, &c.FilePaths, &c.UpdatedAt)
	return c, err
}

// ListCredentials 返回 Agent 的全部凭证（内容与元数据）。
func (s *Store) ListCredentials(ctx context.Context, agentID uuid.UUID) ([]CredentialRow, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+credentialColumns+` FROM agent_credentials WHERE agent_id = $1 ORDER BY purpose, cli_name`, agentID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (CredentialRow, error) { return scanCredential(r) })
}

// PutCredential 整体覆盖 Agent 的一条凭证。
// CLI 凭证要求该 CLI 已在可用列表中。
func (s *Store) PutCredential(ctx context.Context, agentID uuid.UUID, purpose, cliName string, meta CredentialMeta,
	secret []byte) (CredentialRow, error) {
	var row CredentialRow
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if a, err := lockAgent(ctx, tx, agentID); err != nil {
			return err
		} else if a.ProjectID != nil {
			return projectCredentialRequired(a)
		}
		var cli *string
		var templateSource *string
		if purpose == PurposeCLI {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM agent_clis WHERE agent_id = $1 AND cli_name = $2)`, agentID, cliName).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return apperr.Invalid("cli", cliName+" 不在该 Agent 的可用 CLI 列表中")
			}
			cli = &cliName
			// Main catalog currently contains only builtin/override templates.
			source := "builtin"
			templateSource = &source
		}
		if _, err := tx.Exec(ctx, `DELETE FROM agent_credentials WHERE agent_id = $1 AND purpose = $2 AND cli_name IS NOT DISTINCT FROM $3`, agentID, purpose, cli); err != nil {
			return err
		}
		id := uuid.Must(uuid.NewV7())
		var err error
		row, err = scanCredential(tx.QueryRow(ctx, `INSERT INTO agent_credentials
				(id, agent_id, purpose, cli_name, template_version, secret, field_names, env_names, file_paths, template_source)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING `+credentialColumns,
			id, agentID, purpose, cli, nullIfEmpty(meta.TemplateVersion), secret,
			nonNil(meta.FieldNames), nonNil(meta.EnvNames), nonNil(meta.FilePaths), templateSource))
		if err != nil {
			return err
		}
		return bumpConfig(ctx, tx, agentID)
	})
	return row, err
}

// DeleteCredential 删除一条凭证。
func (s *Store) DeleteCredential(ctx context.Context, agentID uuid.UUID, purpose, cliName string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if a, err := lockAgent(ctx, tx, agentID); err != nil {
			return err
		} else if a.ProjectID != nil {
			return projectCredentialRequired(a)
		}
		var cli *string
		if purpose == PurposeCLI {
			cli = &cliName
		}
		tag, err := tx.Exec(ctx, `DELETE FROM agent_credentials WHERE agent_id = $1 AND purpose = $2 AND cli_name IS NOT DISTINCT FROM $3`, agentID, purpose, cli)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.NotFound("凭证")
		}
		return bumpConfig(ctx, tx, agentID)
	})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
