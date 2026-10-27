-- +goose Up
-- Platform executable plugins; legacy 005 remains untouched.
ALTER TABLE task_records ADD COLUMN platform_plugins_captured boolean NOT NULL DEFAULT false;
CREATE TABLE platform_plugins (
 id uuid PRIMARY KEY, name text NOT NULL UNIQUE, display_name text NOT NULL,
 description text NOT NULL DEFAULT '', builtin boolean NOT NULL DEFAULT false,
 source jsonb NOT NULL CHECK(jsonb_typeof(source)='object'), version text NOT NULL,
 manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object'),
 package_dir text NOT NULL DEFAULT '', entry text NOT NULL DEFAULT '',
 deleted boolean NOT NULL DEFAULT false, updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE platform_plugin_versions (
 plugin_id uuid NOT NULL REFERENCES platform_plugins(id), version text NOT NULL,
 manifest jsonb NOT NULL, entry text NOT NULL DEFAULT '', package_dir text NOT NULL DEFAULT '',
 host_entry text NOT NULL, package_digest text NOT NULL,
 PRIMARY KEY(plugin_id,version)
);
CREATE TABLE project_platform_plugins (
 project_id uuid NOT NULL REFERENCES projects(id), plugin_id uuid NOT NULL REFERENCES platform_plugins(id),
 enabled boolean NOT NULL DEFAULT false, values jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(values)='object'),
 config_version bigint NOT NULL DEFAULT 1 CHECK(config_version>0), secrets jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(secrets)='object'),
 PRIMARY KEY(project_id,plugin_id)
);
CREATE TABLE platform_plugin_config_versions (
 project_id uuid NOT NULL REFERENCES projects(id),plugin_id uuid NOT NULL REFERENCES platform_plugins(id),version bigint NOT NULL CHECK(version>0),
 values jsonb NOT NULL DEFAULT '{}',secrets jsonb NOT NULL DEFAULT '{}',PRIMARY KEY(project_id,plugin_id,version)
);
CREATE TABLE agent_plugin_grants (
 agent_id uuid NOT NULL REFERENCES agents(id), plugin_id uuid NOT NULL REFERENCES platform_plugins(id),
 account_ids jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(account_ids)='array'), permissions jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(permissions)='array'), resources jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(resources)='array'), PRIMARY KEY(agent_id,plugin_id)
);
CREATE TABLE platform_plugin_accounts (
 id uuid PRIMARY KEY,project_id uuid NOT NULL REFERENCES projects(id),plugin_id uuid NOT NULL REFERENCES platform_plugins(id),
 kind text NOT NULL,name text NOT NULL,revision bigint NOT NULL CHECK(revision>0),version_id uuid NOT NULL,deleted boolean NOT NULL DEFAULT false
);
CREATE TABLE platform_plugin_account_versions (
 id uuid PRIMARY KEY,account_id uuid NOT NULL REFERENCES platform_plugin_accounts(id),project_id uuid NOT NULL REFERENCES projects(id),plugin_id uuid NOT NULL REFERENCES platform_plugins(id),
 kind text NOT NULL,name text NOT NULL,values jsonb NOT NULL,secret_fields jsonb NOT NULL,cipher jsonb NOT NULL
);
CREATE TABLE task_plugin_versions (
 task_id uuid NOT NULL REFERENCES task_records(id), plugin_id uuid NOT NULL REFERENCES platform_plugins(id),
 version text NOT NULL,bindings jsonb NOT NULL CHECK(jsonb_typeof(bindings)='array'),PRIMARY KEY(task_id,plugin_id),
 FOREIGN KEY(plugin_id,version) REFERENCES platform_plugin_versions(plugin_id,version)
);
CREATE TABLE platform_plugin_state (
 plugin_id uuid NOT NULL REFERENCES platform_plugins(id), project_id uuid NOT NULL REFERENCES projects(id),
 key text NOT NULL, value jsonb NOT NULL, PRIMARY KEY(plugin_id,project_id,key)
);
-- +goose Down
-- Refuse destructive rollback after plugins, config, state, or task pins exist.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM platform_plugins) OR EXISTS(SELECT 1 FROM task_plugin_versions) THEN
  RAISE EXCEPTION 'platform plugin data exists; destructive rollback refused';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE platform_plugin_state;
DROP TABLE task_plugin_versions;
DROP TABLE platform_plugin_account_versions;
DROP TABLE platform_plugin_accounts;
DROP TABLE agent_plugin_grants;
DROP TABLE platform_plugin_config_versions;
DROP TABLE project_platform_plugins;
DROP TABLE platform_plugin_versions;
DROP TABLE platform_plugins;
ALTER TABLE task_records DROP COLUMN platform_plugins_captured;
