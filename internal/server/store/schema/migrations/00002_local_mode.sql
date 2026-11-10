-- +goose Up
-- 个人版“本机模式”：三个全局开关（新装与升级都默认开启）、项目与 Agent 各自的本机目录清单，以及“从 Git 拉取”的两个新失败码。
-- 清单是 JSON 数组 [{"path": "...", "note": "..."}]，整体替换，revision 防止两个编辑器相互覆盖；没有行等同于 revision 0 的空清单。

CREATE TABLE local_mode_settings (
    singleton       INTEGER NOT NULL DEFAULT 1 CHECK (singleton IN (0, 1)),
    directories     INTEGER NOT NULL DEFAULT 1 CHECK (directories IN (0, 1)),
    git             INTEGER NOT NULL DEFAULT 1 CHECK (git IN (0, 1)),
    cli             INTEGER NOT NULL DEFAULT 1 CHECK (cli IN (0, 1)),
    updated_at      INTEGER NOT NULL DEFAULT (now()),
    CONSTRAINT local_mode_settings_pkey PRIMARY KEY (singleton),
    CONSTRAINT local_mode_settings_singleton_check CHECK (singleton)
) STRICT;

INSERT INTO local_mode_settings DEFAULT VALUES;

CREATE TABLE project_local_directories (
    project_id      TEXT NOT NULL,
    directories     TEXT NOT NULL DEFAULT '[]',
    revision        INTEGER NOT NULL DEFAULT 1,
    updated_at      INTEGER NOT NULL DEFAULT (now()),
    CONSTRAINT project_local_directories_pkey PRIMARY KEY (project_id),
    CONSTRAINT project_local_directories_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    CONSTRAINT project_local_directories_directories_check CHECK ((json_type(directories) = 'array')),
    CONSTRAINT project_local_directories_revision_check CHECK ((revision > 0))
) STRICT;

CREATE TABLE agent_local_directories (
    agent_id        TEXT NOT NULL,
    directories     TEXT NOT NULL DEFAULT '[]',
    revision        INTEGER NOT NULL DEFAULT 1,
    updated_at      INTEGER NOT NULL DEFAULT (now()),
    CONSTRAINT agent_local_directories_pkey PRIMARY KEY (agent_id),
    CONSTRAINT agent_local_directories_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    CONSTRAINT agent_local_directories_directories_check CHECK ((json_type(directories) = 'array')),
    CONSTRAINT agent_local_directories_revision_check CHECK ((revision > 0))
) STRICT;

-- “从 Git 拉取”的失败码新增 auth_failed 与 host_key_unverified。SQLite 改不了 CHECK，按“新建、复制、删旧表、改名”重建；
-- 没有别的表或触发器引用它，外键开着也可以在事务里做（删的是子表）。约束与索引除失败码列表外与基线相同。
CREATE TABLE agent_folder_git_pulls_new (
    id              TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    action_id       TEXT NOT NULL,
    url             TEXT NOT NULL,
    branch          TEXT NOT NULL,
    path            TEXT NOT NULL,
    status          TEXT NOT NULL,
    error_code      TEXT,
    error_message   TEXT,
    created_at      INTEGER NOT NULL DEFAULT (now()),
    finished_at     INTEGER,
    CONSTRAINT agent_folder_git_pulls_pkey PRIMARY KEY (id),
    CONSTRAINT agent_folder_git_pulls_agent_id_action_id_key UNIQUE (agent_id, action_id),
    CONSTRAINT agent_folder_git_pulls_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    CONSTRAINT agent_folder_git_pulls_check CHECK (((status IN ('succeeded', 'failed')) = (finished_at IS NOT NULL))),
    CONSTRAINT agent_folder_git_pulls_check1 CHECK (((status = 'failed') = (error_code IS NOT NULL))),
    CONSTRAINT agent_folder_git_pulls_error_code_check CHECK ((error_code IN ('target_exists', 'branch_not_found', 'timeout', 'auth_failed', 'host_key_unverified', 'clone_failed', 'server_restart'))),
    CONSTRAINT agent_folder_git_pulls_status_check CHECK ((status IN ('queued', 'running', 'succeeded', 'failed')))
) STRICT;

INSERT INTO agent_folder_git_pulls_new (id, agent_id, action_id, url, branch, path, status, error_code, error_message, created_at, finished_at)
    SELECT id, agent_id, action_id, url, branch, path, status, error_code, error_message, created_at, finished_at FROM agent_folder_git_pulls;
DROP TABLE agent_folder_git_pulls;
ALTER TABLE agent_folder_git_pulls_new RENAME TO agent_folder_git_pulls;
CREATE UNIQUE INDEX agent_folder_git_pulls_active ON agent_folder_git_pulls (agent_id, path) WHERE (status IN ('queued', 'running'));
