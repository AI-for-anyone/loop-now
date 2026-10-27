-- +goose Up
ALTER TABLE project_cli_versions ADD COLUMN auth_method text NOT NULL DEFAULT 'static' CHECK(auth_method IN('static','service_account','oauth')),
 ADD COLUMN account_label text;
ALTER TABLE project_cli_versions ADD CONSTRAINT cli_auth_kind CHECK((auth_method='static' OR cli_name='gcloud') AND (auth_method<>'oauth' OR (account_label IS NOT NULL AND length(account_label)>0)));
ALTER TABLE project_cli_versions ADD CONSTRAINT cli_grant_identity UNIQUE(id,account_id,cli_name,auth_method);
CREATE TABLE google_cli_grants (
 version_id uuid PRIMARY KEY,account_id uuid NOT NULL,
 cli_name text NOT NULL DEFAULT 'gcloud' CHECK(cli_name='gcloud'),
 auth_method text NOT NULL DEFAULT 'oauth' CHECK(auth_method='oauth'),
 generation bigint NOT NULL DEFAULT 1 CHECK(generation>0),
 refresh_state text NOT NULL DEFAULT 'ready' CHECK(refresh_state IN('ready','refreshing','unavailable')),
 lease_id uuid,lease_agent_id uuid REFERENCES agents(id),lease_until timestamptz,error_code text,
 updated_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(version_id,account_id,cli_name,auth_method) REFERENCES project_cli_versions(id,account_id,cli_name,auth_method) ON DELETE CASCADE,
 CHECK((refresh_state='refreshing' AND lease_id IS NOT NULL AND lease_agent_id IS NOT NULL AND lease_until IS NOT NULL) OR (refresh_state<>'refreshing' AND lease_id IS NULL AND lease_agent_id IS NULL AND lease_until IS NULL))
);
CREATE TABLE project_cli_auth_operations (
 id uuid PRIMARY KEY,project_id uuid NOT NULL REFERENCES projects(id),
 cli_name text NOT NULL CHECK(cli_name='gcloud'),auth_type text NOT NULL CHECK(auth_type='google_user'),
 purpose text NOT NULL DEFAULT 'cli' CHECK(purpose='cli'),
 request_hash text NOT NULL CHECK(length(request_hash)=64),
 target_account_id uuid,target_revision bigint,name text,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN('pending','awaiting_authorization','succeeded','failed','cancelled','expired')),
 account_id uuid,authorization_url text,error_code text,
 started_at timestamptz NOT NULL DEFAULT now(),expires_at timestamptz NOT NULL,completed_at timestamptz,
 FOREIGN KEY(target_account_id,project_id,purpose,cli_name) REFERENCES project_credential_accounts(id,project_id,purpose,slot_key),
 FOREIGN KEY(account_id,project_id,purpose,cli_name) REFERENCES project_credential_accounts(id,project_id,purpose,slot_key),
 CHECK((target_account_id IS NULL AND target_revision IS NULL AND name IS NOT NULL) OR(target_account_id IS NOT NULL AND target_revision>0 AND name IS NULL)),
 CHECK(status IN('pending','awaiting_authorization') OR authorization_url IS NULL),
 CHECK((status IN('pending','awaiting_authorization') AND completed_at IS NULL) OR(status NOT IN('pending','awaiting_authorization') AND completed_at IS NOT NULL))
);
CREATE UNIQUE INDEX project_cli_auth_active_target ON project_cli_auth_operations(project_id,cli_name,coalesce(target_account_id,'00000000-0000-0000-0000-000000000000'::uuid)) WHERE status IN('pending','awaiting_authorization');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM task_instances WHERE (convert_from(snapshot,'UTF8')::jsonb->>'schemaVersion')::int>=6) OR EXISTS(SELECT 1 FROM google_cli_grants) OR EXISTS(SELECT 1 FROM project_cli_auth_operations) OR EXISTS(SELECT 1 FROM project_cli_versions WHERE auth_method<>'static') THEN
  RAISE EXCEPTION 'google_cli_auth_down_requires_no_new_data';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_cli_auth_operations;
DROP TABLE google_cli_grants;
ALTER TABLE project_cli_versions DROP CONSTRAINT cli_grant_identity,DROP CONSTRAINT cli_auth_kind;
ALTER TABLE project_cli_versions DROP COLUMN auth_method,DROP COLUMN account_label;
