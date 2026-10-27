-- +goose Up
CREATE TABLE agent_db_connection_sets (
 agent_id uuid NOT NULL, project_id uuid NOT NULL,
 cli_name text NOT NULL CHECK(cli_name IN ('psql','mysql')),
 PRIMARY KEY(agent_id,cli_name),
 UNIQUE(agent_id,project_id,cli_name),
 FOREIGN KEY(agent_id,project_id) REFERENCES agents(id,project_id),
 FOREIGN KEY(agent_id,cli_name) REFERENCES agent_clis(agent_id,cli_name) ON DELETE CASCADE
);
CREATE TABLE agent_db_connection_members (
 agent_id uuid NOT NULL,project_id uuid NOT NULL,cli_name text NOT NULL,
 account_id uuid NOT NULL,purpose text NOT NULL DEFAULT 'cli' CHECK(purpose='cli'),
 PRIMARY KEY(agent_id,cli_name,account_id),
 FOREIGN KEY(agent_id,project_id,cli_name) REFERENCES agent_db_connection_sets(agent_id,project_id,cli_name) ON DELETE CASCADE,
 FOREIGN KEY(account_id,project_id,purpose,cli_name) REFERENCES project_credential_accounts(id,project_id,purpose,slot_key)
);
CREATE INDEX agent_db_connection_members_account ON agent_db_connection_members(account_id);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM agent_db_connection_sets) OR EXISTS(SELECT 1 FROM task_instances WHERE ((convert_from(snapshot,'UTF8')::jsonb)->>'schemaVersion')::integer>=5) THEN
  RAISE EXCEPTION 'db_connection_sets_down_requires_no_new_data';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE agent_db_connection_members;
DROP TABLE agent_db_connection_sets;
