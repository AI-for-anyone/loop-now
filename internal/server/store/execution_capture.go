package store

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/prompts"
)

// ExecutionIdentity captures configuration and credential identity under the
// same Agent lock used by login/key/configuration commits. Resolution after the
// transaction must use ModelAuth, never reread the current account pointer.
type ExecutionIdentity struct {
	Prompts             *prompts.Layers
	Agent               Agent
	ModelAuthManaged    bool
	ModelAuth           *modelcredential.Binding
	CredentialRows      []CredentialRow
	CLIs                []string
	CredentialsRevision int64
	CredentialIdentity  string
}

func (s *Store) CaptureExecutionIdentity(ctx context.Context, agentID uuid.UUID) (ExecutionIdentity, error) {
	var out ExecutionIdentity
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		var projectID *uuid.UUID
		if err = tx.QueryRow(ctx, `SELECT project_id FROM agents WHERE id=$1 AND deleted_at IS NULL`, agentID).Scan(&projectID); err != nil {
			return notFoundIfNoRows(err, "Agent")
		}
		layer := &prompts.Layers{Format: prompts.FormatV1, ProjectID: projectID}
		if projectID != nil {
			if err = tx.QueryRow(ctx, `SELECT prompt,prompt_version FROM projects WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, *projectID).Scan(&layer.ProjectPrompt, &layer.ProjectVersion); err != nil {
				return notFoundIfNoRows(err, "项目")
			}
		}
		out.Agent, err = scanAgent(tx.QueryRow(ctx, `SELECT `+agentColumns+` FROM agents WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, agentID))
		if err != nil {
			return notFoundIfNoRows(err, "Agent")
		}
		if (out.Agent.ProjectID == nil) != (projectID == nil) || (projectID != nil && *out.Agent.ProjectID != *projectID) {
			return apperr.NotFound("Agent归属已变化")
		}
		layer.AgentPrompt = out.Agent.Prompt
		layer.AgentVersion = out.Agent.PromptVersion
		out.Prompts = layer
		cliRows, e := tx.Query(ctx, `SELECT cli_name FROM agent_clis WHERE agent_id=$1 ORDER BY cli_name`, agentID)
		if e != nil {
			return e
		}
		out.CLIs, e = pgx.CollectRows(cliRows, pgx.RowTo[string])
		if e != nil {
			return e
		}
		if out.Agent.ProjectID != nil {
			if e = tx.QueryRow(ctx, `SELECT credentials_revision FROM projects WHERE id=$1`, *out.Agent.ProjectID).Scan(&out.CredentialsRevision); e != nil {
				return e
			}
			var resolved []cm.Selection
			out.ModelAuthManaged = true
			out.ModelAuth, out.CredentialRows, resolved, e = captureProjectCredentialsTx(ctx, tx, out.Agent, out.CLIs)
			if e != nil {
				return e
			}
			out.CredentialIdentity = projectCredentialIdentity(out.CLIs, resolved, out.CredentialRows)
			return nil
		}
		var version *uuid.UUID
		err = tx.QueryRow(ctx, `SELECT version_id FROM agent_model_auth WHERE agent_id=$1`, agentID).Scan(&version)
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
		out.ModelAuthManaged = err == nil
		if version != nil {
			var provider string
			if err := tx.QueryRow(ctx, `SELECT provider FROM model_auth_versions WHERE id=$1 AND agent_id=$2`, *version, agentID).Scan(&provider); err != nil {
				return err
			}
			if provider != out.Agent.ModelProvider {
				return apperr.Invalid("credentials", "模型授权与当前提供方不匹配")
			}
			out.ModelAuth = &modelcredential.Binding{VersionID: *version, AgentID: agentID, Provider: provider}
		}
		rows, err := tx.Query(ctx, `SELECT id,agent_id,purpose,coalesce(cli_name,''),coalesce(template_version,''),coalesce(template_source,''),secret,field_names,env_names,file_paths,updated_at FROM agent_credentials WHERE agent_id=$1 ORDER BY id`, agentID)
		if err != nil {
			return err
		}
		out.CredentialRows, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (CredentialRow, error) {
			var c CredentialRow
			err := r.Scan(&c.ID, &c.AgentID, &c.Purpose, &c.CLIName, &c.TemplateVersion, &c.TemplateSource, &c.Secret, &c.FieldNames, &c.EnvNames, &c.FilePaths, &c.UpdatedAt)
			return c, err
		})
		return err
	})
	return out, err
}
