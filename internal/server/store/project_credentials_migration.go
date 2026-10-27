package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/pressly/goose/v3"
)

const projectCredentialDDL = `
ALTER TABLE projects ADD COLUMN credentials_revision bigint NOT NULL DEFAULT 1 CHECK(credentials_revision>=1);
ALTER TABLE agents ADD CONSTRAINT agents_project_credential_owner UNIQUE(id,project_id);
CREATE TABLE project_credential_accounts (
 id uuid PRIMARY KEY, project_id uuid NOT NULL REFERENCES projects(id),
 purpose text NOT NULL CHECK(purpose IN ('model','cli')), provider text, cli_name text,
 slot_key text GENERATED ALWAYS AS(coalesce(provider,cli_name)) STORED,
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 200),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>=1),
 current_model_version_id uuid, current_cli_version_id uuid,
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),deleted_at timestamptz,
 CHECK((purpose='model' AND provider IS NOT NULL AND cli_name IS NULL AND current_cli_version_id IS NULL) OR
       (purpose='cli' AND cli_name IS NOT NULL AND provider IS NULL AND current_model_version_id IS NULL)),
 UNIQUE(id,project_id), UNIQUE(id,purpose,provider), UNIQUE(id,purpose,cli_name), UNIQUE(id,project_id,purpose,slot_key)
);
CREATE INDEX project_credential_accounts_slot ON project_credential_accounts(project_id,purpose,slot_key,created_at,id) WHERE deleted_at IS NULL;
ALTER TABLE agent_model_auth DROP CONSTRAINT agent_model_auth_version_id_agent_id_fkey;
ALTER TABLE model_auth_versions ALTER COLUMN agent_id DROP NOT NULL;
ALTER TABLE model_auth_versions ADD COLUMN project_account_id uuid;
ALTER TABLE model_auth_versions ADD COLUMN project_purpose text NOT NULL DEFAULT 'model' CHECK(project_purpose='model');
ALTER TABLE model_auth_versions ADD CONSTRAINT model_auth_owner CHECK((agent_id IS NULL)<>(project_account_id IS NULL));
ALTER TABLE model_auth_versions ADD CONSTRAINT model_auth_project_owner FOREIGN KEY(project_account_id,project_purpose,provider) REFERENCES project_credential_accounts(id,purpose,provider);
ALTER TABLE model_auth_versions ADD CONSTRAINT model_auth_project_identity UNIQUE(id,project_account_id);
ALTER TABLE agent_model_auth ADD CONSTRAINT agent_model_auth_version_id_agent_id_fkey FOREIGN KEY(version_id,agent_id) REFERENCES model_auth_versions(id,agent_id);
CREATE TABLE project_cli_versions (
 id uuid PRIMARY KEY,account_id uuid NOT NULL,purpose text NOT NULL DEFAULT 'cli' CHECK(purpose='cli'),cli_name text NOT NULL,
 template_source text NOT NULL,template_version text NOT NULL,
 alg text NOT NULL,key_id text NOT NULL,wrapped_dek bytea NOT NULL,nonce bytea NOT NULL,ciphertext bytea NOT NULL,
 field_names text[] NOT NULL,env_names text[] NOT NULL,file_paths text[] NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(id,account_id), FOREIGN KEY(account_id,purpose,cli_name) REFERENCES project_credential_accounts(id,purpose,cli_name)
);
ALTER TABLE project_credential_accounts ADD CONSTRAINT account_current_model_owner FOREIGN KEY(current_model_version_id,id) REFERENCES model_auth_versions(id,project_account_id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE project_credential_accounts ADD CONSTRAINT account_current_cli_owner FOREIGN KEY(current_cli_version_id,id) REFERENCES project_cli_versions(id,account_id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE agent_credential_selections (
 agent_id uuid NOT NULL,project_id uuid NOT NULL,purpose text NOT NULL CHECK(purpose IN('model','cli')),slot_key text NOT NULL,
 account_id uuid, mode text NOT NULL CHECK(mode IN('explicit','automatic_override')),
 PRIMARY KEY(agent_id,purpose,slot_key),
 CHECK((mode='explicit' AND account_id IS NOT NULL) OR(mode='automatic_override' AND account_id IS NULL AND purpose='model')),
 FOREIGN KEY(agent_id,project_id) REFERENCES agents(id,project_id),
 FOREIGN KEY(account_id,project_id,purpose,slot_key) REFERENCES project_credential_accounts(id,project_id,purpose,slot_key)
);
CREATE TABLE agent_model_legacy_cleared (
 agent_id uuid PRIMARY KEY,project_id uuid NOT NULL,
 FOREIGN KEY(agent_id,project_id) REFERENCES agents(id,project_id)
);
CREATE TABLE project_model_legacy_bindings (
 version_id uuid PRIMARY KEY REFERENCES model_auth_versions(id) ON DELETE CASCADE,
 source_agent_id uuid NOT NULL REFERENCES agents(id),provider text NOT NULL,
 account_id uuid NOT NULL,project_id uuid NOT NULL,
 FOREIGN KEY(account_id,project_id) REFERENCES project_credential_accounts(id,project_id)
);
-- Shadowed legacy rows remain project owned; never candidates.
CREATE TABLE project_credential_legacy_rows (
 id uuid PRIMARY KEY,project_id uuid NOT NULL REFERENCES projects(id),source_agent_id uuid NOT NULL REFERENCES agents(id),
 alg text NOT NULL,key_id text NOT NULL,wrapped_dek bytea NOT NULL,nonce bytea NOT NULL,ciphertext bytea NOT NULL,
 field_names text[] NOT NULL,env_names text[] NOT NULL,file_paths text[] NOT NULL,updated_at timestamptz NOT NULL
);
CREATE TABLE project_model_auth_operations (
 id uuid PRIMARY KEY,project_id uuid NOT NULL REFERENCES projects(id),provider text NOT NULL CHECK(provider='openai-codex'),
 target_account_id uuid,target_revision bigint,name text,account_id uuid,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN('pending','awaiting_authorization','succeeded','failed','cancelled','expired')),
 authorization_url text,error_code text,started_at timestamptz NOT NULL DEFAULT now(),expires_at timestamptz NOT NULL,completed_at timestamptz,
 CHECK((target_account_id IS NULL AND target_revision IS NULL AND name IS NOT NULL) OR(target_account_id IS NOT NULL AND target_revision>=1 AND name IS NULL)),
 CHECK((status IN('succeeded','failed','cancelled','expired'))=(completed_at IS NOT NULL)),
 CHECK(status IN('pending','awaiting_authorization') OR authorization_url IS NULL),
 FOREIGN KEY(target_account_id,project_id) REFERENCES project_credential_accounts(id,project_id),
 FOREIGN KEY(account_id,project_id) REFERENCES project_credential_accounts(id,project_id)
);
CREATE UNIQUE INDEX project_model_auth_active_new ON project_model_auth_operations(project_id,provider) WHERE target_account_id IS NULL AND status IN('pending','awaiting_authorization');
CREATE UNIQUE INDEX project_model_auth_active_target ON project_model_auth_operations(target_account_id) WHERE target_account_id IS NOT NULL AND status IN('pending','awaiting_authorization');
`

func (s *Store) projectCredentialMigration() *goose.Migration {
	return goose.NewGoMigration(12, &goose.GoFunc{RunTx: func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, projectCredentialDDL); err != nil {
			return err
		}
		return migrateProjectCredentials(ctx, tx)
	}}, &goose.GoFunc{RunTx: func(ctx context.Context, tx *sql.Tx) error {
		var populated bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM project_credential_accounts UNION ALL SELECT 1 FROM agent_model_legacy_cleared UNION ALL SELECT 1 FROM project_credential_legacy_rows UNION ALL SELECT 1 FROM project_model_auth_operations)`).Scan(&populated); err != nil {
			return err
		}
		if populated {
			return errors.New("项目凭证数据存在，拒绝有损回退；请审阅备份与refresh轮换边界")
		}
		_, err := tx.ExecContext(ctx, `DROP TABLE project_model_auth_operations,project_credential_legacy_rows,project_model_legacy_bindings,agent_model_legacy_cleared,agent_credential_selections;
ALTER TABLE project_credential_accounts DROP CONSTRAINT account_current_model_owner,DROP CONSTRAINT account_current_cli_owner;
DROP TABLE project_cli_versions;
ALTER TABLE model_auth_versions DROP CONSTRAINT model_auth_project_owner,DROP CONSTRAINT model_auth_project_identity,DROP CONSTRAINT model_auth_owner,DROP COLUMN project_account_id,DROP COLUMN project_purpose;
ALTER TABLE model_auth_versions ALTER COLUMN agent_id SET NOT NULL;
DROP TABLE project_credential_accounts;
ALTER TABLE agents DROP CONSTRAINT agents_project_credential_owner;
ALTER TABLE projects DROP COLUMN credentials_revision;`)
		return err
	}})
}

// errLegacyCredentialUpgrade is the only supported path for a database that
// still holds credentials or task snapshots from before this migration: they
// were encrypted with the removed master key and cannot be read here.
var errLegacyCredentialUpgrade = errors.New("数据库低于 012 且含旧版凭证或任务快照，本版本无法直接升级：请先用移除主密钥之前的版本升级到 025，运行一次性明文凭证转换程序，再启动本版本")

// migrateProjectCredentials moves only non-secret legacy state. Any Agent
// credential, model account version or task snapshot in a live project refuses
// the migration instead of being converted.
func migrateProjectCredentials(ctx context.Context, tx *sql.Tx) error {
	var pending bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_credentials c JOIN agents a ON a.id=c.agent_id JOIN projects p ON p.id=a.project_id WHERE p.deleted_at IS NULL)
 OR EXISTS(SELECT 1 FROM model_auth_versions v JOIN agents a ON a.id=v.agent_id JOIN projects p ON p.id=a.project_id WHERE p.deleted_at IS NULL)
 OR EXISTS(SELECT 1 FROM task_instances)`).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return errLegacyCredentialUpgrade
	}
	// Without versions every remaining live pointer is an explicit clear.
	_, err := tx.ExecContext(ctx, `INSERT INTO agent_model_legacy_cleared(agent_id,project_id) SELECT a.id,a.project_id FROM agent_model_auth m JOIN agents a ON a.id=m.agent_id JOIN projects p ON p.id=a.project_id WHERE p.deleted_at IS NULL;
DELETE FROM agent_model_auth m USING agents a,projects p WHERE m.agent_id=a.id AND a.project_id=p.id AND p.deleted_at IS NULL;
UPDATE model_auth_operations o SET status='cancelled',authorization_url=NULL,error_code='server_restarted',completed_at=now() FROM agents a,projects p WHERE o.agent_id=a.id AND a.project_id=p.id AND p.deleted_at IS NULL AND o.status IN('pending','awaiting_authorization');`)
	return err
}
