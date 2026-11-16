package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/clicredential"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/db"
)

// Credential purposes.
const (
	PurposeModel = "model"
	PurposeCLI   = "cli"
)

// CredentialRow is one credential (plaintext) with its metadata.
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

// CredentialMeta is the metadata attached when writing.
type CredentialMeta struct {
	TemplateVersion string
	FieldNames      []string
	EnvNames        []string
	FilePaths       []string
}

// ListAgentCLIs returns the names of the Agent's available CLIs.
func (s *Store) ListAgentCLIs(ctx context.Context, agentID uuid.UUID) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT cli_name FROM agent_clis WHERE agent_id = $1 ORDER BY cli_name`, agentID)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, db.RowTo[string])
}

// AddAgentCLI adds a CLI to the available list; does nothing when it is already there.
func (s *Store) AddAgentCLI(ctx context.Context, agentID uuid.UUID, name string) error {
	return s.inTx(ctx, func(tx *db.Tx) error {
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

// RemoveAgentCLI removes a CLI from the available list; its credentials are deleted by the foreign key cascade.
func (s *Store) RemoveAgentCLI(ctx context.Context, agentID uuid.UUID, name string) error {
	return s.inTx(ctx, func(tx *db.Tx) error {
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

func scanCredential(row db.Scanner) (CredentialRow, error) {
	var c CredentialRow
	err := row.Scan(&c.ID, &c.AgentID, &c.Purpose, &c.CLIName, &c.TemplateVersion, &c.TemplateSource, &c.Secret,
		&c.FieldNames, &c.EnvNames, &c.FilePaths, &c.UpdatedAt)
	return c, err
}

// ListCredentials returns all of the Agent's credentials (content and metadata).
func (s *Store) ListCredentials(ctx context.Context, agentID uuid.UUID) ([]CredentialRow, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+credentialColumns+` FROM agent_credentials WHERE agent_id = $1 ORDER BY purpose, cli_name`, agentID)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(r db.CollectableRow) (CredentialRow, error) { return scanCredential(r) })
}

// PutCredential overwrites one of the Agent's credentials as a whole.
// A CLI credential requires the CLI to be in the available list already.
func (s *Store) PutCredential(ctx context.Context, agentID uuid.UUID, purpose, cliName string, meta CredentialMeta,
	secret []byte) (CredentialRow, error) {
	var row CredentialRow
	err := s.inTx(ctx, func(tx *db.Tx) error {
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
		if _, err := tx.Exec(ctx, `DELETE FROM agent_credentials WHERE agent_id = $1 AND purpose = $2 AND cli_name IS $3`, agentID, purpose, cli); err != nil {
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

// DeleteCredential deletes one credential.
func (s *Store) DeleteCredential(ctx context.Context, agentID uuid.UUID, purpose, cliName string) error {
	return s.inTx(ctx, func(tx *db.Tx) error {
		if a, err := lockAgent(ctx, tx, agentID); err != nil {
			return err
		} else if a.ProjectID != nil {
			return projectCredentialRequired(a)
		}
		var cli *string
		if purpose == PurposeCLI {
			cli = &cliName
		}
		tag, err := tx.Exec(ctx, `DELETE FROM agent_credentials WHERE agent_id = $1 AND purpose = $2 AND cli_name IS $3`, agentID, purpose, cli)
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
