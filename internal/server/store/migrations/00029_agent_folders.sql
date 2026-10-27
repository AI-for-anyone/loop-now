-- +goose Up
-- One-time "pull from Git" into an Agent folder. Afterwards the files are plain
-- files; nothing is synced again.
CREATE TABLE agent_folder_git_pulls (
 id uuid PRIMARY KEY,
 agent_id uuid NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
 action_id uuid NOT NULL,
 url text NOT NULL,
 branch text NOT NULL,
 path text NOT NULL,
 status text NOT NULL CHECK(status IN ('queued','running','succeeded','failed')),
 error_code text CHECK(error_code IN ('target_exists','branch_not_found','timeout','clone_failed','server_restart')),
 error_message text,
 created_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz,
 UNIQUE(agent_id, action_id),
 CHECK((status IN ('succeeded','failed')) = (finished_at IS NOT NULL)),
 CHECK((status = 'failed') = (error_code IS NOT NULL))
);
CREATE UNIQUE INDEX agent_folder_git_pulls_active ON agent_folder_git_pulls(agent_id, path) WHERE status IN ('queued','running');

-- Folder sources are retired; their tables and data stay. Human writes and
-- deletes of upload sources that never reached disk are applied once at
-- startup; this table makes that replay happen exactly once per source.
CREATE TABLE agent_folder_legacy_applied (
 source_id uuid PRIMARY KEY REFERENCES agent_folder_sources(id) ON DELETE CASCADE,
 agent_id uuid NOT NULL,
 files_written integer NOT NULL CHECK(files_written >= 0),
 files_deleted integer NOT NULL CHECK(files_deleted >= 0),
 applied_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM agent_folder_git_pulls) OR EXISTS(SELECT 1 FROM agent_folder_legacy_applied) THEN
  RAISE EXCEPTION 'agent_folders_down_refused: git pulls or applied legacy folder files exist';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE agent_folder_legacy_applied;
DROP TABLE agent_folder_git_pulls;
