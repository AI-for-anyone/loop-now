-- +goose Up
ALTER TABLE project_cli_versions DROP CONSTRAINT cli_auth_kind;
ALTER TABLE project_cli_versions ADD CONSTRAINT cli_auth_kind CHECK(
 (auth_method='static' OR (auth_method='service_account' AND cli_name='gcloud') OR
  (auth_method='oauth' AND cli_name IN('gcloud','gh'))) AND
 (auth_method<>'oauth' OR (account_label IS NOT NULL AND length(account_label)>0))
);

CREATE TABLE github_cli_identities (
 version_id uuid PRIMARY KEY,
 account_id uuid NOT NULL,
 cli_name text NOT NULL DEFAULT 'gh' CHECK(cli_name='gh'),
 auth_method text NOT NULL DEFAULT 'oauth' CHECK(auth_method='oauth'),
 host text NOT NULL CHECK(host='github.com'),
 user_id text NOT NULL CHECK(user_id ~ '^[1-9][0-9]*$'),
 login text NOT NULL CHECK(length(login) BETWEEN 1 AND 200),
 granted_scopes text[],
 verified_at timestamptz NOT NULL,
 FOREIGN KEY(version_id,account_id,cli_name,auth_method)
  REFERENCES project_cli_versions(id,account_id,cli_name,auth_method) ON DELETE CASCADE
);

ALTER TABLE project_cli_auth_operations
 DROP CONSTRAINT project_cli_auth_operations_cli_name_check,
 DROP CONSTRAINT project_cli_auth_operations_auth_type_check,
 DROP CONSTRAINT project_cli_auth_operations_check,
 ADD COLUMN user_code text;
ALTER TABLE project_cli_auth_operations
 ADD CONSTRAINT project_cli_auth_pair CHECK(
  (cli_name='gcloud' AND auth_type='google_user') OR (cli_name='gh' AND auth_type='github_user')),
 ADD CONSTRAINT project_cli_auth_target CHECK(
  (target_account_id IS NULL AND target_revision IS NULL AND
   ((cli_name='gcloud' AND name IS NOT NULL) OR (cli_name='gh' AND name IS NULL))) OR
  (target_account_id IS NOT NULL AND target_revision IS NOT NULL AND target_revision>0 AND name IS NULL)),
 ADD CONSTRAINT project_cli_auth_user_code CHECK(cli_name='gh' OR user_code IS NULL),
 ADD CONSTRAINT project_cli_auth_github_view CHECK(cli_name<>'gh' OR
  (status='pending' AND user_code IS NULL AND authorization_url IS NULL) OR
  (status='awaiting_authorization' AND user_code IS NOT NULL AND user_code ~ '^[A-Z0-9]{4}-[A-Z0-9]{4}$'
   AND authorization_url IS NOT NULL AND authorization_url='https://github.com/login/device') OR
  (status NOT IN('pending','awaiting_authorization') AND user_code IS NULL AND authorization_url IS NULL));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_cli_auth_operations WHERE cli_name='gh' OR user_code IS NOT NULL)
  OR EXISTS(SELECT 1 FROM project_cli_versions WHERE cli_name='gh' AND auth_method='oauth')
  OR EXISTS(SELECT 1 FROM github_cli_identities) THEN
  RAISE EXCEPTION 'github_cli_login_down_requires_no_new_data';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE project_cli_auth_operations
 DROP CONSTRAINT project_cli_auth_pair,
 DROP CONSTRAINT project_cli_auth_target,
 DROP CONSTRAINT project_cli_auth_user_code,
 DROP CONSTRAINT project_cli_auth_github_view,
 DROP COLUMN user_code,
 ADD CONSTRAINT project_cli_auth_operations_cli_name_check CHECK(cli_name='gcloud'),
 ADD CONSTRAINT project_cli_auth_operations_auth_type_check CHECK(auth_type='google_user'),
 ADD CONSTRAINT project_cli_auth_operations_check CHECK(
  (target_account_id IS NULL AND target_revision IS NULL AND name IS NOT NULL) OR
  (target_account_id IS NOT NULL AND target_revision>0 AND name IS NULL));
DROP TABLE github_cli_identities;
ALTER TABLE project_cli_versions DROP CONSTRAINT cli_auth_kind;
ALTER TABLE project_cli_versions ADD CONSTRAINT cli_auth_kind CHECK(
 (auth_method='static' OR cli_name='gcloud') AND
 (auth_method<>'oauth' OR (account_label IS NOT NULL AND length(account_label)>0)));
