-- +goose Up
-- Loop Now 第一个闭环的初始结构。所有 ID 由应用生成（UUIDv7），时间均为 timestamptz。

CREATE TABLE organizations (
    id          uuid PRIMARY KEY,
    name        text NOT NULL,
    is_default  boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
-- 只能有一个默认组织。
CREATE UNIQUE INDEX organizations_one_default ON organizations ((true)) WHERE is_default;

CREATE TABLE projects (
    id               uuid PRIMARY KEY,
    organization_id  uuid NOT NULL REFERENCES organizations (id),
    name             text NOT NULL,
    is_default       boolean NOT NULL DEFAULT false,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
-- 每个组织下只能有一个默认项目。
CREATE UNIQUE INDEX projects_one_default_per_org ON projects (organization_id) WHERE is_default;
-- 项目名在组织内唯一（Inbox 地址用名称）。
CREATE UNIQUE INDEX projects_name_per_org ON projects (organization_id, name);

CREATE TABLE agents (
    id              uuid PRIMARY KEY,
    project_id      uuid NOT NULL REFERENCES projects (id),
    name            text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    model_provider  text NOT NULL,
    model_id        text NOT NULL,
    prompt          text NOT NULL DEFAULT '',
    config_version  bigint NOT NULL DEFAULT 1,
    deleted_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX agents_live_by_project ON agents (project_id, created_at) WHERE deleted_at IS NULL;
-- Agent 名在项目内唯一；已软删除的 Agent 不占用名字。
CREATE UNIQUE INDEX agents_name_per_project ON agents (project_id, name) WHERE deleted_at IS NULL;

-- 内容寻址存储：上传资料与 Skill 文件。只追加，不修改。
CREATE TABLE blobs (
    sha256      text PRIMARY KEY CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    size        bigint NOT NULL CHECK (size >= 0),
    data        bytea NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE agent_folder_sources (
    id          uuid PRIMARY KEY,
    agent_id    uuid NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    path        text NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('git', 'files')),
    git_url     text,
    git_branch  text,
    -- 首次同步到磁盘的时间；为空表示尚未同步，此时不记录人工修改。
    synced_at   timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (agent_id, path),
    CHECK ((kind = 'git' AND git_url IS NOT NULL AND git_branch IS NOT NULL)
        OR (kind = 'files' AND git_url IS NULL AND git_branch IS NULL))
);

CREATE TABLE agent_folder_files (
    source_id    uuid NOT NULL REFERENCES agent_folder_sources (id) ON DELETE CASCADE,
    path         text NOT NULL,
    blob_sha256  text NOT NULL REFERENCES blobs (sha256),
    size         bigint NOT NULL CHECK (size >= 0),
    -- 内容是否为合法 UTF-8，用于判断能否在线编辑。
    utf8         boolean NOT NULL DEFAULT false,
    -- 待同步的人工修改：write 覆盖磁盘文件，delete 从磁盘删除（该行在列表中不显示）。
    pending      text NOT NULL DEFAULT 'none' CHECK (pending IN ('none', 'write', 'delete')),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, path)
);

CREATE TABLE skill_templates (
    id           uuid PRIMARY KEY,
    project_id   uuid NOT NULL REFERENCES projects (id),
    name         text NOT NULL,
    description  text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, name)
);

CREATE TABLE skill_template_files (
    template_id  uuid NOT NULL REFERENCES skill_templates (id) ON DELETE CASCADE,
    path         text NOT NULL,
    blob_sha256  text NOT NULL REFERENCES blobs (sha256),
    size         bigint NOT NULL CHECK (size >= 0),
    PRIMARY KEY (template_id, path)
);

CREATE TABLE agent_skills (
    id                  uuid PRIMARY KEY,
    agent_id            uuid NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    name                text NOT NULL,
    description         text NOT NULL,
    source_template_id  uuid REFERENCES skill_templates (id) ON DELETE SET NULL,
    copied_at           timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (agent_id, name)
);

CREATE TABLE agent_skill_files (
    agent_skill_id  uuid NOT NULL REFERENCES agent_skills (id) ON DELETE CASCADE,
    path            text NOT NULL,
    blob_sha256     text NOT NULL REFERENCES blobs (sha256),
    size            bigint NOT NULL CHECK (size >= 0),
    PRIMARY KEY (agent_skill_id, path)
);

CREATE TABLE agent_clis (
    agent_id    uuid NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    cli_name    text NOT NULL CHECK (cli_name ~ '^[a-z0-9][a-z0-9._-]{0,63}$'),
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, cli_name)
);

CREATE TABLE agent_credentials (
    id                uuid PRIMARY KEY,
    agent_id          uuid NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    purpose           text NOT NULL CHECK (purpose IN ('model', 'cli')),
    cli_name          text,
    template_version  text,
    -- 信封加密的结果；明文不入库。
    alg               text NOT NULL,
    key_id            text NOT NULL,
    wrapped_dek       bytea NOT NULL,
    nonce             bytea NOT NULL,
    ciphertext        bytea NOT NULL,
    -- 只用于展示的元数据。
    field_names       text[] NOT NULL DEFAULT '{}',
    env_names         text[] NOT NULL DEFAULT '{}',
    file_paths        text[] NOT NULL DEFAULT '{}',
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CHECK ((purpose = 'model' AND cli_name IS NULL) OR (purpose = 'cli' AND cli_name IS NOT NULL)),
    -- 从可用列表移除 CLI 时，一并删除其凭证。
    FOREIGN KEY (agent_id, cli_name) REFERENCES agent_clis (agent_id, cli_name) ON DELETE CASCADE
);
CREATE UNIQUE INDEX agent_credentials_one_model ON agent_credentials (agent_id) WHERE purpose = 'model';
CREATE UNIQUE INDEX agent_credentials_one_per_cli ON agent_credentials (agent_id, cli_name) WHERE purpose = 'cli';

CREATE TABLE agent_sessions (
    agent_id      uuid PRIMARY KEY REFERENCES agents (id) ON DELETE CASCADE,
    -- 只记录已确认写入磁盘的会话文件。
    session_file  text,
    state         text NOT NULL DEFAULT 'none' CHECK (state IN ('none', 'active', 'missing')),
    epoch         integer NOT NULL DEFAULT 1 CHECK (epoch >= 1),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CHECK ((state = 'none') = (session_file IS NULL))
);

CREATE TABLE runs (
    id               uuid PRIMARY KEY,
    agent_id         uuid NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    user_message_id  uuid NOT NULL UNIQUE,
    config_version   bigint NOT NULL,
    -- 开始执行的服务端实例（多实例预留）。
    instance_id      uuid,
    priority         smallint NOT NULL CHECK (priority IN (0, 1)),
    when_busy        text CHECK (when_busy IN ('interrupt', 'queue')),
    queue_seq        bigint GENERATED ALWAYS AS IDENTITY,
    status           text NOT NULL CHECK (status IN ('queued', 'starting', 'running', 'aborting', 'completed', 'failed', 'interrupted')),
    phase            text CHECK (phase IN ('waiting_slot', 'preparing', 'launching')),
    end_reason       text CHECK (end_reason IN ('user_interrupt', 'server_restart')),
    error_code       text,
    error_message    text,
    queued_at        timestamptz NOT NULL DEFAULT now(),
    started_at       timestamptz,
    ended_at         timestamptz
);
-- 同一 Agent 任一时刻最多一次进行中的执行；Redis 协调数据丢失时也成立。
CREATE UNIQUE INDEX runs_one_active_per_agent ON runs (agent_id) WHERE status IN ('starting', 'running', 'aborting');
CREATE INDEX runs_queued_by_agent ON runs (agent_id, priority, queue_seq) WHERE status = 'queued';

CREATE TABLE conversation_messages (
    id             uuid PRIMARY KEY,
    agent_id       uuid NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    seq            bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    role           text NOT NULL CHECK (role IN ('user', 'assistant')),
    text           text NOT NULL,
    run_id         uuid NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    session_epoch  integer NOT NULL CHECK (session_epoch >= 1),
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX conversation_messages_by_agent ON conversation_messages (agent_id, seq);
-- 每次执行最多一条回复。
CREATE UNIQUE INDEX conversation_messages_one_reply ON conversation_messages (run_id) WHERE role = 'assistant';

CREATE TABLE run_tool_events (
    run_id        uuid NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    seq           integer NOT NULL,
    tool_call_id  text NOT NULL,
    tool_name     text NOT NULL,
    state         text NOT NULL CHECK (state IN ('started', 'succeeded', 'failed')),
    at            timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, seq)
);

-- +goose Down
DROP TABLE run_tool_events;
DROP TABLE conversation_messages;
DROP TABLE runs;
DROP TABLE agent_sessions;
DROP TABLE agent_credentials;
DROP TABLE agent_clis;
DROP TABLE agent_skill_files;
DROP TABLE agent_skills;
DROP TABLE skill_template_files;
DROP TABLE skill_templates;
DROP TABLE agent_folder_files;
DROP TABLE agent_folder_sources;
DROP TABLE blobs;
DROP TABLE agents;
DROP TABLE projects;
DROP TABLE organizations;
