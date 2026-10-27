-- +goose Up
ALTER TABLE project_cli_versions DROP CONSTRAINT cli_auth_kind;
ALTER TABLE project_cli_versions ADD CONSTRAINT cli_auth_kind CHECK(
 (auth_method='static' OR (auth_method='service_account' AND cli_name='gcloud') OR
  (auth_method='oauth' AND cli_name IN('gcloud','gh','lark-cli'))) AND
 (auth_method<>'oauth' OR (account_label IS NOT NULL AND length(account_label)>0))
);
CREATE TABLE lark_cli_identities (
 version_id uuid PRIMARY KEY, account_id uuid NOT NULL,
 cli_name text NOT NULL DEFAULT 'lark-cli' CHECK(cli_name='lark-cli'),
 auth_method text NOT NULL DEFAULT 'oauth' CHECK(auth_method='oauth'),
 brand text NOT NULL CHECK(brand IN('feishu','lark')),
 app_id text NOT NULL CHECK(length(app_id) BETWEEN 1 AND 200),
 open_id text NOT NULL CHECK(length(open_id) BETWEEN 1 AND 200),
 display_name text NOT NULL CHECK(length(display_name) BETWEEN 1 AND 200),
 granted_scopes text[], verified_at timestamptz NOT NULL,
 runtime_variant_version text NOT NULL CHECK(runtime_variant_version='lark-cli-user-1.0.94-loop-authsidecar.1'),
 FOREIGN KEY(version_id,account_id,cli_name,auth_method)
  REFERENCES project_cli_versions(id,account_id,cli_name,auth_method) ON DELETE CASCADE
);
CREATE TABLE lark_cli_grants (
 version_id uuid PRIMARY KEY REFERENCES lark_cli_identities(version_id) ON DELETE CASCADE,
 generation bigint NOT NULL DEFAULT 1 CHECK(generation>0),
 refresh_state text NOT NULL DEFAULT 'ready' CHECK(refresh_state IN('ready','refreshing','unavailable')),
 lease_id uuid,lease_agent_id uuid REFERENCES agents(id),lease_until timestamptz,error_code text,
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK((refresh_state='refreshing' AND lease_id IS NOT NULL AND lease_agent_id IS NOT NULL AND lease_until IS NOT NULL)
 OR(refresh_state<>'refreshing' AND lease_id IS NULL AND lease_agent_id IS NULL AND lease_until IS NULL))
);
ALTER TABLE project_cli_auth_operations
 DROP CONSTRAINT project_cli_auth_pair,
 DROP CONSTRAINT project_cli_auth_target,
 DROP CONSTRAINT project_cli_auth_user_code,
 ADD COLUMN source_account_id uuid,
 ADD COLUMN source_version_id uuid,
 ADD COLUMN source_revision bigint,
 ADD CONSTRAINT project_cli_auth_source_account FOREIGN KEY(source_account_id,project_id,purpose,cli_name)
  REFERENCES project_credential_accounts(id,project_id,purpose,slot_key),
 ADD CONSTRAINT project_cli_auth_pair CHECK(
  (cli_name='gcloud' AND auth_type='google_user') OR (cli_name='gh' AND auth_type='github_user') OR
  (cli_name='lark-cli' AND auth_type='lark_user')),
 ADD CONSTRAINT project_cli_auth_target CHECK(
  (target_account_id IS NULL AND target_revision IS NULL AND
   ((cli_name='gcloud' AND name IS NOT NULL) OR (cli_name IN('gh','lark-cli') AND name IS NULL))) OR
  (target_account_id IS NOT NULL AND target_revision IS NOT NULL AND target_revision>0 AND name IS NULL)),
 ADD CONSTRAINT project_cli_auth_user_code CHECK(cli_name IN('gh','lark-cli') OR user_code IS NULL),
 ADD CONSTRAINT project_cli_auth_lark_source CHECK(
  (cli_name='lark-cli' AND target_account_id IS NULL AND source_account_id IS NOT NULL AND source_version_id IS NOT NULL AND source_revision IS NOT NULL AND source_revision>0)
  OR ((cli_name<>'lark-cli' OR target_account_id IS NOT NULL) AND source_account_id IS NULL AND source_version_id IS NULL AND source_revision IS NULL)),
 ADD CONSTRAINT project_cli_auth_lark_view CHECK(cli_name<>'lark-cli' OR
  (status='pending' AND authorization_url IS NULL AND user_code IS NULL) OR
  (status='awaiting_authorization' AND authorization_url IS NOT NULL AND (user_code IS NULL OR length(user_code) BETWEEN 1 AND 200)) OR
  (status NOT IN('pending','awaiting_authorization') AND authorization_url IS NULL AND user_code IS NULL));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_cli_auth_operations WHERE cli_name='lark-cli') OR
 EXISTS(SELECT 1 FROM lark_cli_grants) OR EXISTS(SELECT 1 FROM lark_cli_identities) OR
 EXISTS(SELECT 1 FROM project_cli_versions WHERE cli_name='lark-cli' AND auth_method='oauth') OR
 EXISTS(SELECT 1 FROM task_instances WHERE (convert_from(snapshot,'UTF8')::jsonb->>'schemaVersion')::int>=7) THEN
  RAISE EXCEPTION 'lark_user_login_down_requires_no_new_data';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE project_cli_auth_operations
 DROP CONSTRAINT project_cli_auth_lark_view,DROP CONSTRAINT project_cli_auth_lark_source,
 DROP CONSTRAINT project_cli_auth_source_account,
 DROP CONSTRAINT project_cli_auth_pair,DROP CONSTRAINT project_cli_auth_target,DROP CONSTRAINT project_cli_auth_user_code,
 DROP COLUMN source_account_id,DROP COLUMN source_version_id,DROP COLUMN source_revision,
 ADD CONSTRAINT project_cli_auth_pair CHECK((cli_name='gcloud' AND auth_type='google_user') OR(cli_name='gh' AND auth_type='github_user')),
 ADD CONSTRAINT project_cli_auth_target CHECK(
  (target_account_id IS NULL AND target_revision IS NULL AND ((cli_name='gcloud' AND name IS NOT NULL) OR(cli_name='gh' AND name IS NULL))) OR
  (target_account_id IS NOT NULL AND target_revision IS NOT NULL AND target_revision>0 AND name IS NULL)),
 ADD CONSTRAINT project_cli_auth_user_code CHECK(cli_name='gh' OR user_code IS NULL);
DROP TABLE lark_cli_grants;
DROP TABLE lark_cli_identities;
ALTER TABLE project_cli_versions DROP CONSTRAINT cli_auth_kind;
ALTER TABLE project_cli_versions ADD CONSTRAINT cli_auth_kind CHECK(
 (auth_method='static' OR (auth_method='service_account' AND cli_name='gcloud') OR
  (auth_method='oauth' AND cli_name IN('gcloud','gh'))) AND
 (auth_method<>'oauth' OR(account_label IS NOT NULL AND length(account_label)>0)));
