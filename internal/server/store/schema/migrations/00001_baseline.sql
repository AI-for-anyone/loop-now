-- +goose Up
-- Loop Now 的 SQLite 基线（版本 1）：等同于 PostgreSQL 迁移 001–034（含版本 12 的 Go 迁移）执行完后的全部表，
-- 按表最早出现的那条 PG 迁移分组。换写规则：
--   * 所有表 STRICT；data.db 放业务表，events.db（挂载名 ev）只放四张可清理的日志表，两库之间没有外键。
--   * uuid 存小写文本；timestamptz 存 UTC 微秒整数（默认值只是兜底，代码传入时刻）；boolean 存 0/1；
--     jsonb/json 存 JSON 文本；text[] 存 JSON 数组；bytea 存 BLOB。
--   * 正则 CHECK 换成等价的 GLOB 与 length；UNIQUE NULLS NOT DISTINCT 换成 coalesce 表达式唯一索引；
--     列级 ON DELETE SET NULL (列) 换成父表 BEFORE DELETE 触发器；非主键 identity 列改由 sequences 表取号。
--   * 约束保留 PG 的名字。PG 之外新增的只有 sequences 表和 runs、task_turns 的 stop_requested 列。

-- ===== 00001_init.sql =====

CREATE TABLE agent_clis (
    agent_id        TEXT NOT NULL,
    cli_name        TEXT NOT NULL,
    created_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT agent_clis_pkey PRIMARY KEY (agent_id, cli_name),
    CONSTRAINT agent_clis_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    CONSTRAINT agent_clis_cli_name_check CHECK ((length(cli_name) BETWEEN 1 AND 64 AND cli_name GLOB '[a-z0-9]*' AND cli_name NOT GLOB '*[^a-z0-9._-]*'))
) STRICT;

CREATE TABLE agent_credentials (
    id                TEXT NOT NULL,
    agent_id          TEXT NOT NULL,
    purpose           TEXT NOT NULL,
    cli_name          TEXT,
    template_version  TEXT,
    secret            BLOB NOT NULL,
    field_names       TEXT NOT NULL DEFAULT '[]' CHECK (json_type(field_names) = 'array'),
    env_names         TEXT NOT NULL DEFAULT '[]' CHECK (json_type(env_names) = 'array'),
    file_paths        TEXT NOT NULL DEFAULT '[]' CHECK (json_type(file_paths) = 'array'),
    created_at        INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    updated_at        INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    template_source   TEXT,
    CONSTRAINT agent_credentials_pkey PRIMARY KEY (id),
    CONSTRAINT agent_credentials_agent_id_cli_name_fkey FOREIGN KEY (agent_id, cli_name) REFERENCES agent_clis(agent_id, cli_name) ON DELETE CASCADE,
    CONSTRAINT agent_credentials_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    CONSTRAINT agent_credentials_check CHECK ((((purpose = 'model') AND (cli_name IS NULL)) OR ((purpose = 'cli') AND (cli_name IS NOT NULL)))),
    CONSTRAINT agent_credentials_purpose_check CHECK ((purpose IN ('model', 'cli'))),
    CONSTRAINT agent_credentials_template_source CHECK (((purpose = 'cli') = (template_source IS NOT NULL)))
) STRICT;
CREATE UNIQUE INDEX agent_credentials_one_model ON agent_credentials (agent_id) WHERE (purpose = 'model');
CREATE UNIQUE INDEX agent_credentials_one_per_cli ON agent_credentials (agent_id, cli_name) WHERE (purpose = 'cli');

CREATE TABLE agent_folder_files (
    source_id       TEXT NOT NULL,
    path            TEXT NOT NULL,
    blob_sha256     TEXT NOT NULL,
    size            INTEGER NOT NULL,
    utf8            INTEGER NOT NULL DEFAULT 0 CHECK (utf8 IN (0, 1)),
    pending         TEXT NOT NULL DEFAULT 'none',
    updated_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT agent_folder_files_pkey PRIMARY KEY (source_id, path),
    CONSTRAINT agent_folder_files_blob_sha256_fkey FOREIGN KEY (blob_sha256) REFERENCES blobs(sha256),
    CONSTRAINT agent_folder_files_source_id_fkey FOREIGN KEY (source_id) REFERENCES agent_folder_sources(id) ON DELETE CASCADE,
    CONSTRAINT agent_folder_files_pending_check CHECK ((pending IN ('none', 'write', 'delete'))),
    CONSTRAINT agent_folder_files_size_check CHECK ((size >= 0))
) STRICT;

CREATE TABLE agent_folder_sources (
    id              TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    path            TEXT NOT NULL,
    kind            TEXT NOT NULL,
    git_url         TEXT,
    git_branch      TEXT,
    synced_at       INTEGER,
    created_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    updated_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT agent_folder_sources_pkey PRIMARY KEY (id),
    CONSTRAINT agent_folder_sources_agent_id_path_key UNIQUE (agent_id, path),
    CONSTRAINT agent_folder_sources_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    CONSTRAINT agent_folder_sources_check CHECK ((((kind = 'git') AND (git_url IS NOT NULL) AND (git_branch IS NOT NULL)) OR ((kind = 'files') AND (git_url IS NULL) AND (git_branch IS NULL)))),
    CONSTRAINT agent_folder_sources_kind_check CHECK ((kind IN ('git', 'files')))
) STRICT;

CREATE TABLE agent_sessions (
    agent_id        TEXT NOT NULL,
    session_file    TEXT,
    state           TEXT NOT NULL DEFAULT 'none',
    epoch           INTEGER NOT NULL DEFAULT 1,
    updated_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    sleep_revision  INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT agent_sessions_pkey PRIMARY KEY (agent_id),
    CONSTRAINT agent_sessions_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    CONSTRAINT agent_sessions_check CHECK (((state = 'none') = (session_file IS NULL))),
    CONSTRAINT agent_sessions_epoch_check CHECK ((epoch >= 1)),
    CONSTRAINT agent_sessions_sleep_revision_check CHECK ((sleep_revision >= 0)),
    CONSTRAINT agent_sessions_state_check CHECK ((state IN ('none', 'active', 'missing')))
) STRICT;

CREATE TABLE agent_skill_files (
    agent_skill_id  TEXT NOT NULL,
    path            TEXT NOT NULL,
    blob_sha256     TEXT NOT NULL,
    size            INTEGER NOT NULL,
    CONSTRAINT agent_skill_files_pkey PRIMARY KEY (agent_skill_id, path),
    CONSTRAINT agent_skill_files_agent_skill_id_fkey FOREIGN KEY (agent_skill_id) REFERENCES agent_skills(id) ON DELETE CASCADE,
    CONSTRAINT agent_skill_files_blob_sha256_fkey FOREIGN KEY (blob_sha256) REFERENCES blobs(sha256),
    CONSTRAINT agent_skill_files_size_check CHECK ((size >= 0))
) STRICT;

CREATE TABLE agent_skills (
    id                  TEXT NOT NULL,
    agent_id            TEXT NOT NULL,
    name                TEXT NOT NULL,
    description         TEXT NOT NULL,
    source_template_id  TEXT,
    copied_at           INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    updated_at          INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT agent_skills_pkey PRIMARY KEY (id),
    CONSTRAINT agent_skills_agent_id_name_key UNIQUE (agent_id, name),
    CONSTRAINT agent_skills_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    CONSTRAINT agent_skills_source_template_id_fkey FOREIGN KEY (source_template_id) REFERENCES skill_templates(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE agents (
    id              TEXT NOT NULL,
    project_id      TEXT,
    name            TEXT NOT NULL,
    model_provider  TEXT NOT NULL,
    model_id        TEXT NOT NULL,
    prompt          TEXT NOT NULL DEFAULT '',
    config_version  INTEGER NOT NULL DEFAULT 1,
    deleted_at      INTEGER,
    created_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    updated_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    prompt_version  INTEGER NOT NULL DEFAULT 1,
    thinking_level  TEXT,
    service_tier    TEXT,
    CONSTRAINT agents_pkey PRIMARY KEY (id),
    CONSTRAINT agents_id_project UNIQUE (id, project_id),
    CONSTRAINT agents_project_credential_owner UNIQUE (id, project_id),
    CONSTRAINT agents_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id),
    CONSTRAINT agents_check CHECK (((service_tier IS NULL) OR (model_provider = 'openai-codex'))),
    CONSTRAINT agents_name_check CHECK (((length(name) >= 1) AND (length(name) <= 100))),
    CONSTRAINT agents_prompt_version_check CHECK ((prompt_version > 0)),
    CONSTRAINT agents_service_tier_check CHECK ((service_tier IN ('default', 'priority', 'ultrafast'))),
    CONSTRAINT agents_thinking_level_check CHECK ((thinking_level IN ('off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max')))
) STRICT;
CREATE INDEX agents_live_by_project ON agents (project_id, created_at) WHERE (deleted_at IS NULL);
CREATE INDEX agents_live_platform ON agents (created_at) WHERE ((project_id IS NULL) AND (deleted_at IS NULL));
CREATE UNIQUE INDEX agents_name_per_project ON agents (project_id, name) WHERE (deleted_at IS NULL);
CREATE UNIQUE INDEX agents_name_platform ON agents (name) WHERE ((project_id IS NULL) AND (deleted_at IS NULL));

CREATE TABLE blobs (
    sha256          TEXT NOT NULL,
    size            INTEGER NOT NULL,
    data            BLOB NOT NULL,
    created_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT blobs_pkey PRIMARY KEY (sha256),
    CONSTRAINT blobs_sha256_check CHECK ((length(sha256)=64 AND sha256 NOT GLOB '*[^0-9a-f]*')),
    CONSTRAINT blobs_size_check CHECK ((size >= 0))
) STRICT;

CREATE TABLE conversation_messages (
    id              TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    seq             INTEGER NOT NULL,
    role            TEXT NOT NULL,
    text            TEXT NOT NULL,
    run_id          TEXT NOT NULL,
    session_epoch   INTEGER NOT NULL,
    created_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    images          TEXT NOT NULL DEFAULT '[]',
    CONSTRAINT conversation_messages_pkey PRIMARY KEY (id),
    CONSTRAINT conversation_messages_seq_key UNIQUE (seq),
    CONSTRAINT conversation_messages_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    CONSTRAINT conversation_messages_run_id_fkey FOREIGN KEY (run_id) REFERENCES runs(id) ON DELETE CASCADE,
    CONSTRAINT conversation_messages_images_check CHECK ((json_type(images) = 'array')),
    CONSTRAINT conversation_messages_role_check CHECK ((role IN ('user', 'assistant'))),
    CONSTRAINT conversation_messages_session_epoch_check CHECK ((session_epoch >= 1))
) STRICT;
CREATE INDEX conversation_messages_by_agent ON conversation_messages (agent_id, seq);
CREATE UNIQUE INDEX conversation_messages_one_reply ON conversation_messages (run_id) WHERE (role = 'assistant');

CREATE TABLE organizations (
    id              TEXT NOT NULL,
    name            TEXT NOT NULL,
    is_default      INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1)),
    created_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    updated_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT organizations_pkey PRIMARY KEY (id)
) STRICT;
CREATE UNIQUE INDEX organizations_name_unique ON organizations (name);
-- 只能有一个默认组织。PG 写成 ((true))；SQLite 的 ON CONFLICT 认不出常量表达式索引，改按 is_default 建，约束相同。
CREATE UNIQUE INDEX organizations_one_default ON organizations (is_default) WHERE is_default;

CREATE TABLE projects (
    id                    TEXT NOT NULL,
    organization_id       TEXT NOT NULL,
    name                  TEXT NOT NULL,
    is_default            INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1)),
    created_at            INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    updated_at            INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    deleted_at            INTEGER,
    manager_agent_id      TEXT,
    prompt                TEXT NOT NULL DEFAULT '',
    prompt_version        INTEGER NOT NULL DEFAULT 1,
    credentials_revision  INTEGER NOT NULL DEFAULT 1,
    CONSTRAINT projects_pkey PRIMARY KEY (id),
    CONSTRAINT projects_manager_in_project FOREIGN KEY (manager_agent_id, id) REFERENCES agents(id, project_id),
    CONSTRAINT projects_organization_id_fkey FOREIGN KEY (organization_id) REFERENCES organizations(id),
    CONSTRAINT projects_credentials_revision_check CHECK ((credentials_revision >= 1)),
    CONSTRAINT projects_default_not_deleted CHECK ((NOT (is_default AND (deleted_at IS NOT NULL)))),
    CONSTRAINT projects_prompt_version_check CHECK ((prompt_version > 0))
) STRICT;
CREATE UNIQUE INDEX projects_name_live ON projects (organization_id, name) WHERE (deleted_at IS NULL);
CREATE UNIQUE INDEX projects_one_default_per_org ON projects (organization_id) WHERE is_default;

-- 跨库外键已去掉：FOREIGN KEY (run_id) REFERENCES runs(id) ON DELETE CASCADE
CREATE TABLE ev.run_tool_events (
    run_id          TEXT NOT NULL,
    seq             INTEGER NOT NULL,
    tool_call_id    TEXT NOT NULL,
    tool_name       TEXT NOT NULL,
    state           TEXT NOT NULL,
    at              INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT run_tool_events_pkey PRIMARY KEY (run_id, seq),
    CONSTRAINT run_tool_events_state_check CHECK ((state IN ('started', 'succeeded', 'failed')))
) STRICT;

CREATE TABLE runs (
    id                        TEXT NOT NULL,
    agent_id                  TEXT NOT NULL,
    user_message_id           TEXT NOT NULL,
    config_version            INTEGER NOT NULL,
    instance_id               TEXT,
    priority                  INTEGER NOT NULL,
    when_busy                 TEXT,
    queue_seq                 INTEGER NOT NULL,
    status                    TEXT NOT NULL,
    phase                     TEXT,
    end_reason                TEXT,
    error_code                TEXT,
    error_message             TEXT,
    queued_at                 INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    started_at                INTEGER,
    ended_at                  INTEGER,
    running_at                INTEGER,
    abort_requested_at        INTEGER,
    origin                    TEXT NOT NULL DEFAULT 'console',
    trigger_inbox_message_id  TEXT,
    wake_sleep_id             TEXT,
    stop_requested            INTEGER NOT NULL DEFAULT 0 CONSTRAINT runs_stop_requested_check CHECK (stop_requested IN (0, 1)),
    CONSTRAINT runs_pkey PRIMARY KEY (id),
    CONSTRAINT runs_user_message_id_key UNIQUE (user_message_id),
    CONSTRAINT runs_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    CONSTRAINT runs_trigger_inbox_message_id_fkey FOREIGN KEY (trigger_inbox_message_id) REFERENCES inbox_messages(id),
    CONSTRAINT runs_wake_sleep_id_fkey FOREIGN KEY (wake_sleep_id) REFERENCES agent_sleeps(id),
    CONSTRAINT runs_end_reason_check CHECK ((end_reason IN ('user_interrupt', 'server_restart', 'sleep_superseded', 'sleep_invalidated', 'user_stopped', 'user_cancelled', 'merged_into_interrupt', 'no_pending_messages'))),
    CONSTRAINT runs_origin_check CHECK ((origin IN ('console', 'inbox_reminder', 'sleep_wake'))),
    CONSTRAINT runs_phase_check CHECK ((phase IN ('waiting_slot', 'preparing', 'launching'))),
    CONSTRAINT runs_priority_check CHECK ((priority IN (0, 1))),
    CONSTRAINT runs_sleep_wake_reference CHECK (((origin <> 'sleep_wake') OR (wake_sleep_id IS NOT NULL))),
    CONSTRAINT runs_status_check CHECK ((status IN ('queued', 'starting', 'running', 'aborting', 'completed', 'failed', 'interrupted'))),
    CONSTRAINT runs_trigger_matches_origin CHECK (((origin = 'inbox_reminder') = (trigger_inbox_message_id IS NOT NULL))),
    CONSTRAINT runs_when_busy_check CHECK ((when_busy IN ('interrupt', 'queue')))
) STRICT;
CREATE INDEX runs_anomalies ON runs (ended_at DESC, id DESC) WHERE (status IN ('failed', 'interrupted'));
CREATE UNIQUE INDEX runs_one_active_per_agent ON runs (agent_id) WHERE (status IN ('starting', 'running', 'aborting'));
CREATE UNIQUE INDEX runs_one_queued_reminder ON runs (agent_id, priority) WHERE ((status = 'queued') AND (origin = 'inbox_reminder'));
CREATE UNIQUE INDEX runs_one_sleep_carrier ON runs (wake_sleep_id) WHERE ((wake_sleep_id IS NOT NULL) AND (status IN ('queued', 'starting', 'running', 'aborting')));
CREATE INDEX runs_queued_by_agent ON runs (agent_id, priority, queue_seq) WHERE (status = 'queued');

CREATE TABLE skill_template_files (
    template_id     TEXT NOT NULL,
    path            TEXT NOT NULL,
    blob_sha256     TEXT NOT NULL,
    size            INTEGER NOT NULL,
    CONSTRAINT skill_template_files_pkey PRIMARY KEY (template_id, path),
    CONSTRAINT skill_template_files_blob_sha256_fkey FOREIGN KEY (blob_sha256) REFERENCES blobs(sha256),
    CONSTRAINT skill_template_files_template_id_fkey FOREIGN KEY (template_id) REFERENCES skill_templates(id) ON DELETE CASCADE,
    CONSTRAINT skill_template_files_size_check CHECK ((size >= 0))
) STRICT;

CREATE TABLE skill_templates (
    id              TEXT NOT NULL,
    name            TEXT NOT NULL,
    description     TEXT NOT NULL,
    created_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    updated_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT skill_templates_pkey PRIMARY KEY (id),
    CONSTRAINT skill_templates_name_key UNIQUE (name)
) STRICT;

-- ===== 00002_observability.sql =====

-- 跨库外键已去掉：FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE
-- 跨库外键已去掉：FOREIGN KEY (run_id) REFERENCES runs(id) ON DELETE CASCADE
-- 跨库外键已去掉：FOREIGN KEY (task_turn_id) REFERENCES task_turns(id)
CREATE TABLE ev.lifecycle_events (
    id                TEXT NOT NULL,
    at                INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    agent_id          TEXT NOT NULL,
    run_id            TEXT,
    type              TEXT NOT NULL,
    source            TEXT NOT NULL DEFAULT 'platform',
    data              TEXT NOT NULL DEFAULT '{}',
    instance_id       TEXT,
    task_id           TEXT,
    workflow_node     TEXT,
    inbox_message_id  TEXT,
    task_turn_id      TEXT,
    CONSTRAINT lifecycle_events_pkey PRIMARY KEY (id),
    CONSTRAINT lifecycle_events_data_check CHECK ((json_type(data) = 'object')),
    CONSTRAINT lifecycle_events_source_check CHECK ((source = 'platform')),
    CONSTRAINT lifecycle_events_type_check CHECK ((type GLOB '*.*' AND type NOT GLOB '*[^a-z_.]*' AND type NOT GLOB '.*' AND type NOT GLOB '*.' AND type NOT GLOB '*..*'))
) STRICT;
CREATE INDEX ev.lifecycle_events_by_agent ON lifecycle_events (agent_id, at);
CREATE INDEX ev.lifecycle_events_by_run ON lifecycle_events (run_id, at) WHERE (run_id IS NOT NULL);
CREATE INDEX ev.lifecycle_events_cli_installs ON lifecycle_events (((data ->> 'cli')), ((data ->> 'version')), at DESC) WHERE (type = 'cli.install');
CREATE INDEX ev.lifecycle_events_process_exits ON lifecycle_events (agent_id, at DESC) WHERE (type IN ('pi.stopped', 'pi.exited'));

CREATE TABLE usage_records (
    id              TEXT NOT NULL,
    at              INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    agent_id        TEXT NOT NULL,
    run_id          TEXT,
    kind            TEXT NOT NULL,
    provider        TEXT,
    model           TEXT,
    response_model  TEXT,
    stop_reason     TEXT,
    input           INTEGER NOT NULL DEFAULT 0,
    output          INTEGER NOT NULL DEFAULT 0,
    cache_read      INTEGER NOT NULL DEFAULT 0,
    cache_write     INTEGER NOT NULL DEFAULT 0,
    reasoning       INTEGER,
    total           INTEGER GENERATED ALWAYS AS ((((input + output) + cache_read) + cache_write)) STORED,
    duration_ms     INTEGER,
    task_id         TEXT,
    task_turn_id    TEXT,
    CONSTRAINT usage_records_pkey PRIMARY KEY (id),
    CONSTRAINT usage_records_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    CONSTRAINT usage_records_run_id_fkey FOREIGN KEY (run_id) REFERENCES runs(id) ON DELETE CASCADE,
    CONSTRAINT usage_records_task_id_fkey FOREIGN KEY (task_id) REFERENCES task_records(id),
    CONSTRAINT usage_records_task_turn_id_fkey FOREIGN KEY (task_turn_id) REFERENCES task_turns(id),
    CONSTRAINT usage_records_cache_read_check CHECK ((cache_read >= 0)),
    CONSTRAINT usage_records_cache_write_check CHECK ((cache_write >= 0)),
    CONSTRAINT usage_records_check CHECK (((provider IS NULL) = (model IS NULL))),
    CONSTRAINT usage_records_duration_ms_check CHECK ((duration_ms >= 0)),
    CONSTRAINT usage_records_input_check CHECK ((input >= 0)),
    CONSTRAINT usage_records_kind_check CHECK ((kind IN ('assistant', 'tool', 'compaction', 'cache_warm', 'other'))),
    CONSTRAINT usage_records_output_check CHECK ((output >= 0)),
    CONSTRAINT usage_records_reasoning_check CHECK ((reasoning >= 0))
) STRICT;
CREATE INDEX usage_records_by_agent ON usage_records (agent_id, at);
CREATE INDEX usage_records_by_run ON usage_records (run_id) WHERE (run_id IS NOT NULL);
CREATE INDEX usage_records_by_time ON usage_records (at);

-- ===== 00004_inbox_and_access.sql =====

CREATE TABLE agent_tokens (
    id              TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    instance_id     TEXT NOT NULL,
    secret_hash     BLOB NOT NULL,
    issued_at       INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    revoked_at      INTEGER,
    runtime_id      TEXT,
    task_id         TEXT,
    CONSTRAINT agent_tokens_pkey PRIMARY KEY (id),
    CONSTRAINT agent_tokens_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    CONSTRAINT agent_tokens_task_runtime FOREIGN KEY (runtime_id, task_id, agent_id) REFERENCES task_instances(id, task_id, agent_id),
    CONSTRAINT agent_tokens_runtime_pair CHECK (((runtime_id IS NULL) = (task_id IS NULL))),
    CONSTRAINT agent_tokens_secret_hash_check CHECK ((length(secret_hash) = 32))
) STRICT;
CREATE INDEX agent_tokens_live_by_agent ON agent_tokens (agent_id) WHERE (revoked_at IS NULL);
CREATE INDEX agent_tokens_live_by_instance ON agent_tokens (instance_id) WHERE (revoked_at IS NULL);

CREATE TABLE ev.audit_log (
    id              TEXT NOT NULL,
    at              INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    request_id      TEXT,
    actor_kind      TEXT NOT NULL,
    actor_id        TEXT,
    actor_address   TEXT,
    method          TEXT NOT NULL,
    operation       TEXT NOT NULL,
    target          TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(target)),
    status          INTEGER NOT NULL,
    on_behalf_of    TEXT,
    CONSTRAINT audit_log_pkey PRIMARY KEY (id),
    CONSTRAINT audit_log_actor_kind_check CHECK ((actor_kind IN ('console', 'external', 'agent', 'cli', 'plugin')))
) STRICT;
CREATE INDEX ev.audit_log_by_time ON audit_log (at DESC, id DESC);

CREATE TABLE external_identities (
    id              TEXT NOT NULL,
    name            TEXT NOT NULL,
    created_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    deleted_at      INTEGER,
    CONSTRAINT external_identities_pkey PRIMARY KEY (id),
    CONSTRAINT external_identities_name_check CHECK (((length(name) BETWEEN 1 AND 64 AND name GLOB '[a-z0-9]*' AND name NOT GLOB '*[^a-z0-9._-]*') AND (name NOT IN ('console', 'platform'))))
) STRICT;
CREATE UNIQUE INDEX external_identities_name_live ON external_identities (name) WHERE (deleted_at IS NULL);

CREATE TABLE external_tokens (
    id              TEXT NOT NULL,
    identity_id     TEXT NOT NULL,
    secret_hash     BLOB NOT NULL,
    display_prefix  TEXT NOT NULL,
    created_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    expires_at      INTEGER,
    revoked_at      INTEGER,
    last_used_at    INTEGER,
    CONSTRAINT external_tokens_pkey PRIMARY KEY (id),
    CONSTRAINT external_tokens_identity_id_fkey FOREIGN KEY (identity_id) REFERENCES external_identities(id),
    CONSTRAINT external_tokens_display_prefix_check CHECK ((length(display_prefix)=12 AND display_prefix GLOB 'lne_*' AND substr(display_prefix,5) NOT GLOB '*[^0-9a-f]*')),
    CONSTRAINT external_tokens_secret_hash_check CHECK ((length(secret_hash) = 32))
) STRICT;
CREATE INDEX external_tokens_by_identity ON external_tokens (identity_id, created_at DESC);

CREATE TABLE inbox_messages (
    id                    TEXT NOT NULL,
    thread_id             TEXT NOT NULL,
    in_reply_to           TEXT,
    from_kind             TEXT NOT NULL,
    from_agent_id         TEXT,
    from_identity_id      TEXT,
    to_kind               TEXT NOT NULL,
    to_agent_id           TEXT,
    to_identity_id        TEXT,
    text                  TEXT NOT NULL,
    when_busy             TEXT,
    created_at            INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    read_at               INTEGER,
    via                   TEXT CHECK (json_valid(via)),
    answers_message_id    TEXT,
    answer_option_id      TEXT,
    notice                TEXT CHECK (json_valid(notice)),
    from_runtime_id       TEXT,
    to_runtime_id         TEXT,
    images                TEXT NOT NULL DEFAULT '[]',
    cancelled_at          INTEGER,
    reminder_run_id       TEXT,
    reminder_turn_id      TEXT,
    from_plugin_party_id  TEXT,
    to_plugin_party_id    TEXT,
    plugin_channel        TEXT,
    plugin_sender_name    TEXT,
    CONSTRAINT inbox_messages_pkey PRIMARY KEY (id),
    CONSTRAINT inbox_from_runtime_agent FOREIGN KEY (from_runtime_id, from_agent_id) REFERENCES task_instances(id, agent_id),
    CONSTRAINT inbox_messages_answers_message_id_fkey FOREIGN KEY (answers_message_id) REFERENCES inbox_messages(id),
    CONSTRAINT inbox_messages_from_agent_id_fkey FOREIGN KEY (from_agent_id) REFERENCES agents(id),
    CONSTRAINT inbox_messages_from_identity_id_fkey FOREIGN KEY (from_identity_id) REFERENCES external_identities(id),
    CONSTRAINT inbox_messages_from_plugin_party_id_fkey FOREIGN KEY (from_plugin_party_id) REFERENCES plugin_parties(id),
    CONSTRAINT inbox_messages_from_runtime_id_fkey FOREIGN KEY (from_runtime_id) REFERENCES task_instances(id),
    CONSTRAINT inbox_messages_in_reply_to_fkey FOREIGN KEY (in_reply_to) REFERENCES inbox_messages(id),
    CONSTRAINT inbox_messages_reminder_run_id_fkey FOREIGN KEY (reminder_run_id) REFERENCES runs(id) ON DELETE SET NULL,
    CONSTRAINT inbox_messages_reminder_turn_id_fkey FOREIGN KEY (reminder_turn_id) REFERENCES task_turns(id) ON DELETE SET NULL,
    CONSTRAINT inbox_messages_thread_id_fkey FOREIGN KEY (thread_id) REFERENCES inbox_messages(id),
    CONSTRAINT inbox_messages_to_agent_id_fkey FOREIGN KEY (to_agent_id) REFERENCES agents(id),
    CONSTRAINT inbox_messages_to_identity_id_fkey FOREIGN KEY (to_identity_id) REFERENCES external_identities(id),
    CONSTRAINT inbox_messages_to_plugin_party_id_fkey FOREIGN KEY (to_plugin_party_id) REFERENCES plugin_parties(id),
    CONSTRAINT inbox_messages_to_runtime_id_fkey FOREIGN KEY (to_runtime_id) REFERENCES task_instances(id),
    CONSTRAINT inbox_to_runtime_agent FOREIGN KEY (to_runtime_id, to_agent_id) REFERENCES task_instances(id, agent_id),
    CONSTRAINT inbox_messages_cancelled_unread CHECK (((cancelled_at IS NULL) OR (read_at IS NULL))),
    CONSTRAINT inbox_messages_from_kind_check CHECK ((from_kind IN ('agent', 'console', 'external', 'platform', 'plugin'))),
    CONSTRAINT inbox_messages_from_ref CHECK ((((from_kind = 'agent') AND (from_agent_id IS NOT NULL) AND (from_identity_id IS NULL) AND (from_plugin_party_id IS NULL)) OR ((from_kind = 'external') AND (from_identity_id IS NOT NULL) AND (from_agent_id IS NULL) AND (from_plugin_party_id IS NULL)) OR ((from_kind = 'plugin') AND (from_plugin_party_id IS NOT NULL) AND (from_agent_id IS NULL) AND (from_identity_id IS NULL)) OR ((from_kind IN ('console', 'platform')) AND (from_agent_id IS NULL) AND (from_identity_id IS NULL) AND (from_plugin_party_id IS NULL)))),
    CONSTRAINT inbox_messages_images_check CHECK ((json_type(images) = 'array')),
    CONSTRAINT inbox_messages_one_reminder CHECK (((reminder_run_id IS NULL) OR (reminder_turn_id IS NULL))),
    CONSTRAINT inbox_messages_plugin_side CHECK ((((plugin_channel IS NULL) AND (plugin_sender_name IS NULL)) OR (from_kind = 'plugin') OR (to_kind = 'plugin'))),
    CONSTRAINT inbox_messages_text_check CHECK (((text <> '') OR (json_array_length(images) > 0))),
    CONSTRAINT inbox_messages_thread CHECK (((in_reply_to IS NULL) = (thread_id = id))),
    CONSTRAINT inbox_messages_to_kind_check CHECK ((to_kind IN ('agent', 'console', 'external', 'plugin'))),
    CONSTRAINT inbox_messages_to_ref CHECK ((((to_kind = 'agent') AND (to_agent_id IS NOT NULL) AND (to_identity_id IS NULL) AND (to_plugin_party_id IS NULL)) OR ((to_kind = 'external') AND (to_identity_id IS NOT NULL) AND (to_agent_id IS NULL) AND (to_plugin_party_id IS NULL)) OR ((to_kind = 'plugin') AND (to_plugin_party_id IS NOT NULL) AND (to_agent_id IS NULL) AND (to_identity_id IS NULL)) OR ((to_kind = 'console') AND (to_agent_id IS NULL) AND (to_identity_id IS NULL) AND (to_plugin_party_id IS NULL)))),
    CONSTRAINT inbox_messages_when_busy CHECK (((to_kind = 'agent') = (when_busy IS NOT NULL))),
    CONSTRAINT inbox_messages_when_busy_check CHECK ((when_busy IN ('interrupt', 'queue')))
) STRICT;
CREATE INDEX inbox_by_thread ON inbox_messages (thread_id, created_at, id);
CREATE INDEX inbox_from_agent ON inbox_messages (from_agent_id, created_at DESC, id DESC) WHERE (from_kind = 'agent');
CREATE INDEX inbox_from_console ON inbox_messages (created_at DESC, id DESC) WHERE (from_kind = 'console');
CREATE INDEX inbox_from_identity ON inbox_messages (from_identity_id, created_at DESC, id DESC) WHERE (from_kind = 'external');
CREATE INDEX inbox_from_plugin ON inbox_messages (from_plugin_party_id, created_at DESC, id DESC) WHERE (from_kind = 'plugin');
CREATE INDEX inbox_messages_reminder_run ON inbox_messages (reminder_run_id) WHERE (reminder_run_id IS NOT NULL);
CREATE INDEX inbox_messages_reminder_turn ON inbox_messages (reminder_turn_id) WHERE (reminder_turn_id IS NOT NULL);
CREATE INDEX inbox_to_agent ON inbox_messages (to_agent_id, created_at DESC, id DESC) WHERE (to_kind = 'agent');
CREATE INDEX inbox_to_console ON inbox_messages (created_at DESC, id DESC) WHERE (to_kind = 'console');
CREATE INDEX inbox_to_identity ON inbox_messages (to_identity_id, created_at DESC, id DESC) WHERE (to_kind = 'external');
CREATE INDEX inbox_to_plugin ON inbox_messages (to_plugin_party_id, created_at DESC, id DESC) WHERE (to_kind = 'plugin');
CREATE INDEX inbox_to_runtime ON inbox_messages (to_runtime_id, created_at DESC, id DESC) WHERE (to_runtime_id IS NOT NULL);
CREATE INDEX inbox_unread_agent ON inbox_messages (to_agent_id) WHERE ((to_kind = 'agent') AND (read_at IS NULL));

-- ===== 00005_plugins.sql =====

CREATE TABLE plugins (
    id              TEXT NOT NULL,
    display_name    TEXT NOT NULL,
    source          TEXT NOT NULL,
    version         TEXT NOT NULL,
    manifest        TEXT NOT NULL,
    install_dir     TEXT NOT NULL,
    status          TEXT NOT NULL,
    last_error      TEXT,
    installed_at    INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    updated_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT plugins_pkey PRIMARY KEY (id),
    CONSTRAINT plugins_id_check CHECK ((length(id) BETWEEN 1 AND 64 AND id NOT GLOB '*[^a-z0-9-]*' AND id NOT GLOB '-*' AND id NOT GLOB '*-')),
    CONSTRAINT plugins_manifest_check CHECK ((json_type(manifest) = 'object')),
    CONSTRAINT plugins_source_check CHECK ((json_type(source) = 'object')),
    CONSTRAINT plugins_status_check CHECK ((status IN ('installed', 'active', 'error', 'host_stopped')))
) STRICT;

-- ===== 00006_inbox_channels.sql =====

CREATE TABLE inbox_attachments (
    id              TEXT NOT NULL,
    message_id      TEXT NOT NULL,
    ordinal         INTEGER NOT NULL,
    name            TEXT NOT NULL,
    mime_type       TEXT NOT NULL,
    size            INTEGER NOT NULL,
    sha256          TEXT NOT NULL,
    created_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT inbox_attachments_pkey PRIMARY KEY (id),
    CONSTRAINT inbox_attachments_message_id_ordinal_key UNIQUE (message_id, ordinal),
    CONSTRAINT inbox_attachments_message_id_fkey FOREIGN KEY (message_id) REFERENCES inbox_messages(id),
    CONSTRAINT inbox_attachments_sha256_fkey FOREIGN KEY (sha256) REFERENCES blobs(sha256),
    CONSTRAINT inbox_attachments_ordinal_check CHECK ((ordinal >= 0)),
    CONSTRAINT inbox_attachments_size_check CHECK ((size >= 0))
) STRICT;

CREATE TABLE inbox_questions (
    message_id         TEXT NOT NULL,
    options            TEXT NOT NULL,
    expires_at         INTEGER NOT NULL,
    state              TEXT NOT NULL DEFAULT 'open',
    expired_reason     TEXT,
    answer_message_id  TEXT,
    answer_option_id   TEXT,
    answered_by        TEXT CHECK (json_valid(answered_by)),
    answered_at        INTEGER,
    notice_state       TEXT NOT NULL DEFAULT 'none',
    closed_at          INTEGER,
    closed_reason      TEXT,
    CONSTRAINT inbox_questions_pkey PRIMARY KEY (message_id),
    CONSTRAINT inbox_questions_answer_message_id_fkey FOREIGN KEY (answer_message_id) REFERENCES inbox_messages(id),
    CONSTRAINT inbox_questions_message_id_fkey FOREIGN KEY (message_id) REFERENCES inbox_messages(id),
    CONSTRAINT inbox_questions_closed_reason_check CHECK ((closed_reason IN ('task_retry', 'task_cancel', 'node_completed'))),
    CONSTRAINT inbox_questions_closed_record CHECK (((state = 'closed') = ((closed_at IS NOT NULL) AND (closed_reason IS NOT NULL)))),
    CONSTRAINT inbox_questions_notice_state_check CHECK ((notice_state IN ('none', 'pending', 'sent', 'skipped'))),
    CONSTRAINT inbox_questions_options_check CHECK ((json_type(options) = 'array')),
    CONSTRAINT inbox_questions_state_check CHECK ((state IN ('open', 'answered', 'expired', 'closed', 'cancelled')))
) STRICT;
CREATE INDEX inbox_questions_expiry ON inbox_questions (expires_at, message_id) WHERE (state = 'open');
CREATE INDEX inbox_questions_pending_notice ON inbox_questions (message_id) WHERE (notice_state = 'pending');

-- ===== 00007_tasks.sql =====

CREATE TABLE task_actions (
    actor_key       TEXT NOT NULL,
    action_id       TEXT NOT NULL,
    operation       TEXT NOT NULL,
    task_id         TEXT NOT NULL,
    body_hash       BLOB NOT NULL,
    response        TEXT NOT NULL CHECK (json_valid(response)),
    CONSTRAINT task_actions_pkey PRIMARY KEY (actor_key, action_id),
    CONSTRAINT task_actions_task_id_fkey FOREIGN KEY (task_id) REFERENCES task_records(id),
    CONSTRAINT task_actions_body_hash_check CHECK ((length(body_hash) = 32))
) STRICT;

CREATE TABLE task_archives (
    runtime_id      TEXT NOT NULL,
    blob_sha256     TEXT NOT NULL,
    archived_at     INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT task_archives_pkey PRIMARY KEY (runtime_id),
    CONSTRAINT task_archives_blob_sha256_fkey FOREIGN KEY (blob_sha256) REFERENCES blobs(sha256),
    CONSTRAINT task_archives_runtime_id_fkey FOREIGN KEY (runtime_id) REFERENCES task_instances(id)
) STRICT;

CREATE TABLE task_artifacts (
    id              TEXT NOT NULL,
    task_id         TEXT NOT NULL,
    node_run_id     TEXT NOT NULL,
    name            TEXT NOT NULL,
    content_type    TEXT NOT NULL,
    blob_sha256     TEXT NOT NULL,
    size            INTEGER NOT NULL,
    created_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT task_artifacts_pkey PRIMARY KEY (id),
    CONSTRAINT task_artifacts_blob_sha256_fkey FOREIGN KEY (blob_sha256) REFERENCES blobs(sha256),
    CONSTRAINT task_artifacts_task_id_fkey FOREIGN KEY (task_id) REFERENCES task_records(id),
    CONSTRAINT task_artifacts_size_check CHECK ((size >= 0))
) STRICT;
CREATE INDEX task_artifacts_task ON task_artifacts (task_id, created_at, id);

CREATE TABLE task_events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id         TEXT NOT NULL,
    project_id      TEXT NOT NULL,
    type            TEXT NOT NULL,
    data            TEXT NOT NULL CHECK (json_valid(data)),
    created_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT task_events_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id),
    CONSTRAINT task_events_task_id_fkey FOREIGN KEY (task_id) REFERENCES task_records(id)
) STRICT;
CREATE INDEX task_events_project ON task_events (project_id, id);
CREATE INDEX task_events_task ON task_events (task_id, id);

CREATE TABLE task_instances (
    id              TEXT NOT NULL,
    task_id         TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    agent_name      TEXT NOT NULL,
    snapshot        BLOB NOT NULL,
    sources         TEXT NOT NULL DEFAULT '[]',
    session_file    TEXT,
    session_state   TEXT NOT NULL DEFAULT 'none',
    fork_source     TEXT,
    prepared        INTEGER NOT NULL DEFAULT 0 CHECK (prepared IN (0, 1)),
    retired         INTEGER NOT NULL DEFAULT 0 CHECK (retired IN (0, 1)),
    sleep_revision  INTEGER NOT NULL DEFAULT 0,
    snapshot_sha    TEXT NOT NULL,
    CONSTRAINT task_instances_pkey PRIMARY KEY (id),
    CONSTRAINT task_instances_id_agent_id_key UNIQUE (id, agent_id),
    CONSTRAINT task_instances_id_task_id_agent_id_key UNIQUE (id, task_id, agent_id),
    CONSTRAINT task_instances_id_task_id_key UNIQUE (id, task_id),
    CONSTRAINT task_instances_task_id_agent_id_key UNIQUE (task_id, agent_id),
    CONSTRAINT task_instances_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id),
    CONSTRAINT task_instances_task_id_fkey FOREIGN KEY (task_id) REFERENCES task_records(id),
    CONSTRAINT task_instances_session_state_check CHECK ((session_state IN ('none', 'active', 'missing'))),
    CONSTRAINT task_instances_sleep_revision_check CHECK ((sleep_revision >= 0)),
    CONSTRAINT task_instances_snapshot_sha CHECK ((length(snapshot_sha)=64 AND snapshot_sha NOT GLOB '*[^0-9a-f]*')),
    CONSTRAINT task_instances_sources_check CHECK ((json_type(sources) = 'array'))
) STRICT;
CREATE INDEX task_instances_by_agent ON task_instances (agent_id, task_id);

CREATE TABLE task_notifications (
    id              TEXT NOT NULL,
    task_id         TEXT NOT NULL,
    node_run_id     TEXT,
    kind            TEXT NOT NULL,
    message_id      TEXT NOT NULL,
    text            TEXT NOT NULL,
    sent_at         INTEGER,
    CONSTRAINT task_notifications_pkey PRIMARY KEY (id),
    CONSTRAINT task_notifications_message_id_key UNIQUE (message_id),
    CONSTRAINT task_notifications_task_id_fkey FOREIGN KEY (task_id) REFERENCES task_records(id)
) STRICT;
-- PG 的 UNIQUE NULLS NOT DISTINCT：NULL 视为相同；ON CONFLICT 目标须写同样的表达式。
CREATE UNIQUE INDEX task_notifications_task_id_node_run_id_kind_key ON task_notifications (task_id, coalesce(node_run_id, ''), kind);

CREATE TABLE task_records (
    id              TEXT NOT NULL,
    project_id      TEXT NOT NULL,
    status          TEXT NOT NULL,
    document        TEXT NOT NULL,
    version         INTEGER NOT NULL,
    created_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    updated_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    ended_at        INTEGER,
    CONSTRAINT task_records_pkey PRIMARY KEY (id),
    CONSTRAINT task_records_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id),
    CONSTRAINT task_records_check CHECK (((status IN ('completed', 'cancelled')) = (ended_at IS NOT NULL))),
    CONSTRAINT task_records_check1 CHECK (((((document -> 'view') -> 'task') ->> 'status') = status)),
    CONSTRAINT task_records_check2 CHECK ((CAST(document->'$.view.task'->>'version' AS INTEGER) = version)),
    CONSTRAINT task_records_document_check CHECK ((json_type(document) = 'object')),
    CONSTRAINT task_records_status_check CHECK ((status IN ('preparing', 'running', 'waiting', 'blocked', 'completed', 'cancelled'))),
    CONSTRAINT task_records_version_check CHECK ((version > 0))
) STRICT;
CREATE INDEX task_records_project_page ON task_records (project_id, created_at DESC, id DESC);
CREATE INDEX task_records_unfinished ON task_records (project_id) WHERE (ended_at IS NULL);

CREATE TABLE task_turns (
    id                        TEXT NOT NULL,
    task_id                   TEXT NOT NULL,
    runtime_id                TEXT NOT NULL,
    node_run_id               TEXT,
    user_message_id           TEXT NOT NULL,
    assistant_message_id      TEXT NOT NULL,
    status                    TEXT NOT NULL,
    origin                    TEXT NOT NULL,
    priority                  INTEGER NOT NULL DEFAULT 1,
    queue_seq                 INTEGER NOT NULL,
    text                      TEXT NOT NULL,
    reply                     TEXT NOT NULL DEFAULT '',
    error_code                TEXT,
    error_message             TEXT,
    queued_at                 INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    started_at                INTEGER,
    ended_at                  INTEGER,
    request_id                TEXT,
    trigger_inbox_message_id  TEXT,
    wake_sleep_id             TEXT,
    execution_mode            TEXT NOT NULL DEFAULT '',
    inbox_scope_node_id       TEXT,
    lark_binding_revision     INTEGER NOT NULL DEFAULT 0,
    lark_switch_action_id     TEXT,
    lark_submitted            INTEGER NOT NULL DEFAULT 0 CHECK (lark_submitted IN (0, 1)),
    void_reason               TEXT,
    question_wakeup           INTEGER NOT NULL DEFAULT 0 CHECK (question_wakeup IN (0, 1)),
    stop_requested            INTEGER NOT NULL DEFAULT 0 CONSTRAINT task_turns_stop_requested_check CHECK (stop_requested IN (0, 1)),
    CONSTRAINT task_turns_pkey PRIMARY KEY (id),
    CONSTRAINT task_turns_assistant_message_id_key UNIQUE (assistant_message_id),
    CONSTRAINT task_turns_queue_seq_key UNIQUE (queue_seq),
    CONSTRAINT task_turns_user_message_id_key UNIQUE (user_message_id),
    CONSTRAINT task_turns_runtime_id_task_id_fkey FOREIGN KEY (runtime_id, task_id) REFERENCES task_instances(id, task_id),
    CONSTRAINT task_turns_task_id_fkey FOREIGN KEY (task_id) REFERENCES task_records(id),
    CONSTRAINT task_turns_trigger_inbox_message_id_fkey FOREIGN KEY (trigger_inbox_message_id) REFERENCES inbox_messages(id),
    CONSTRAINT task_turns_wake_sleep_id_fkey FOREIGN KEY (wake_sleep_id) REFERENCES agent_sleeps(id),
    CONSTRAINT task_turns_execution_mode_check CHECK ((execution_mode IN ('', 'task_node', 'task_conversation'))),
    CONSTRAINT task_turns_lark_binding_revision_check CHECK ((lark_binding_revision >= 0)),
    CONSTRAINT task_turns_origin_check CHECK ((origin IN ('task_node', 'inbox_reminder', 'sleep_wake', 'console_node'))),
    CONSTRAINT task_turns_priority_check CHECK ((priority IN (0, 1))),
    CONSTRAINT task_turns_sleep_wake_reference CHECK (((origin <> 'sleep_wake') OR ((wake_sleep_id IS NOT NULL) AND (trigger_inbox_message_id IS NULL)))),
    CONSTRAINT task_turns_status_check CHECK ((status IN ('queued', 'starting', 'running', 'aborting', 'completed', 'failed', 'interrupted'))),
    CONSTRAINT task_turns_void_reason_check CHECK ((void_reason IN ('merged_into_interrupt', 'user_cancelled', 'no_pending_messages')))
) STRICT;
CREATE INDEX task_turns_node ON task_turns (node_run_id, queued_at, id);
CREATE UNIQUE INDEX task_turns_one_active ON task_turns (runtime_id) WHERE (status IN ('starting', 'running', 'aborting'));
CREATE UNIQUE INDEX task_turns_one_reminder ON task_turns (runtime_id, priority) WHERE ((status = 'queued') AND (origin = 'inbox_reminder') AND (inbox_scope_node_id IS NULL));
CREATE UNIQUE INDEX task_turns_one_sleep_carrier ON task_turns (wake_sleep_id) WHERE ((wake_sleep_id IS NOT NULL) AND (status IN ('queued', 'starting', 'running', 'aborting')));
CREATE INDEX task_turns_queue ON task_turns (runtime_id, priority, queue_seq) WHERE (status = 'queued');

-- ===== 00008_model_auth.sql =====

CREATE TABLE agent_model_auth (
    agent_id        TEXT NOT NULL,
    version_id      TEXT,
    updated_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT agent_model_auth_pkey PRIMARY KEY (agent_id),
    CONSTRAINT agent_model_auth_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id),
    CONSTRAINT agent_model_auth_version_id_agent_id_fkey FOREIGN KEY (version_id, agent_id) REFERENCES model_auth_versions(id, agent_id)
) STRICT;

CREATE TABLE model_auth_operations (
    id                 TEXT NOT NULL,
    agent_id           TEXT NOT NULL,
    provider           TEXT NOT NULL,
    config_version     INTEGER NOT NULL,
    status             TEXT NOT NULL DEFAULT 'pending',
    authorization_url  TEXT,
    error_code         TEXT,
    started_at         INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    expires_at         INTEGER NOT NULL,
    completed_at       INTEGER,
    CONSTRAINT model_auth_operations_pkey PRIMARY KEY (id),
    CONSTRAINT model_auth_operations_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id),
    CONSTRAINT model_auth_operations_check CHECK (((status IN ('succeeded', 'failed', 'cancelled', 'expired')) = (completed_at IS NOT NULL))),
    CONSTRAINT model_auth_operations_check1 CHECK (((status IN ('pending', 'awaiting_authorization')) OR (authorization_url IS NULL))),
    CONSTRAINT model_auth_operations_config_version_check CHECK ((config_version >= 1)),
    CONSTRAINT model_auth_operations_status_check CHECK ((status IN ('pending', 'awaiting_authorization', 'succeeded', 'failed', 'cancelled', 'expired')))
) STRICT;
CREATE INDEX model_auth_operations_latest ON model_auth_operations (agent_id, started_at DESC, id DESC);
CREATE UNIQUE INDEX model_auth_operations_one_active ON model_auth_operations (agent_id) WHERE (status IN ('pending', 'awaiting_authorization'));

CREATE TABLE model_auth_versions (
    id                  TEXT NOT NULL,
    agent_id            TEXT,
    provider            TEXT NOT NULL,
    auth_method         TEXT NOT NULL,
    account_label       TEXT,
    secret              BLOB NOT NULL,
    generation          INTEGER NOT NULL DEFAULT 1,
    refresh_state       TEXT NOT NULL DEFAULT 'ready',
    refresh_attempt_id  TEXT,
    refresh_started_at  INTEGER,
    error_code          TEXT,
    created_at          INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    updated_at          INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    project_account_id  TEXT,
    project_purpose     TEXT NOT NULL DEFAULT 'model',
    CONSTRAINT model_auth_versions_pkey PRIMARY KEY (id),
    CONSTRAINT model_auth_project_identity UNIQUE (id, project_account_id),
    CONSTRAINT model_auth_versions_id_agent_id_key UNIQUE (id, agent_id),
    CONSTRAINT model_auth_project_owner FOREIGN KEY (project_account_id, project_purpose, provider) REFERENCES project_credential_accounts(id, purpose, provider),
    CONSTRAINT model_auth_versions_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id),
    CONSTRAINT model_auth_owner CHECK (((agent_id IS NULL) <> (project_account_id IS NULL))),
    CONSTRAINT model_auth_versions_auth_method_check CHECK ((auth_method IN ('api_key', 'oauth'))),
    CONSTRAINT model_auth_versions_check CHECK (((refresh_state = 'refreshing') = (refresh_attempt_id IS NOT NULL))),
    CONSTRAINT model_auth_versions_check1 CHECK (((refresh_state = 'refreshing') = (refresh_started_at IS NOT NULL))),
    CONSTRAINT model_auth_versions_generation_check CHECK ((generation >= 1)),
    CONSTRAINT model_auth_versions_project_purpose_check CHECK ((project_purpose = 'model')),
    CONSTRAINT model_auth_versions_refresh_state_check CHECK ((refresh_state IN ('ready', 'refreshing', 'unavailable')))
) STRICT;
CREATE INDEX model_auth_versions_agent ON model_auth_versions (agent_id, created_at DESC, id DESC);

-- ===== 00010_inbox_question_task_completion.sql =====

CREATE TABLE inbox_question_outbox (
    id                   TEXT NOT NULL,
    question_message_id  TEXT NOT NULL,
    state                TEXT NOT NULL,
    message_id           TEXT NOT NULL,
    delivered_at         INTEGER,
    created_at           INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT inbox_question_outbox_pkey PRIMARY KEY (id),
    CONSTRAINT inbox_question_outbox_message_id_key UNIQUE (message_id),
    CONSTRAINT inbox_question_outbox_question_message_id_state_key UNIQUE (question_message_id, state),
    CONSTRAINT inbox_question_outbox_question_message_id_fkey FOREIGN KEY (question_message_id) REFERENCES inbox_questions(message_id),
    CONSTRAINT inbox_question_outbox_state_check CHECK ((state IN ('answered', 'expired', 'closed')))
) STRICT;
CREATE INDEX inbox_question_outbox_pending ON inbox_question_outbox (created_at, id) WHERE (delivered_at IS NULL);

CREATE TABLE task_questions (
    question_message_id  TEXT NOT NULL,
    task_id              TEXT NOT NULL,
    runtime_id           TEXT NOT NULL,
    node_run_id          TEXT NOT NULL,
    CONSTRAINT task_questions_pkey PRIMARY KEY (question_message_id),
    CONSTRAINT task_questions_question_message_id_fkey FOREIGN KEY (question_message_id) REFERENCES inbox_questions(message_id),
    CONSTRAINT task_questions_runtime_id_task_id_fkey FOREIGN KEY (runtime_id, task_id) REFERENCES task_instances(id, task_id),
    CONSTRAINT task_questions_task_id_fkey FOREIGN KEY (task_id) REFERENCES task_records(id)
) STRICT;
CREATE INDEX task_questions_node ON task_questions (task_id, node_run_id);

-- ===== 00011_agent_sleep.sql =====

CREATE TABLE agent_sleeps (
    id              TEXT NOT NULL,
    scope_key       TEXT NOT NULL,
    scope_kind      TEXT NOT NULL,
    mode            TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    task_id         TEXT,
    runtime_id      TEXT,
    node_run_id     TEXT,
    session_epoch   INTEGER,
    source_run_id   TEXT NOT NULL,
    action_id       TEXT NOT NULL,
    body_hash       BLOB NOT NULL,
    receipt         TEXT NOT NULL,
    state           TEXT NOT NULL,
    seconds         INTEGER NOT NULL,
    reason          TEXT,
    requested_at    INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    entered_at      INTEGER,
    wake_at         INTEGER,
    ended_at        INTEGER,
    end_reason      TEXT,
    timer_run_id    TEXT NOT NULL,
    wake_run_id     TEXT,
    revision        INTEGER NOT NULL,
    CONSTRAINT agent_sleeps_pkey PRIMARY KEY (id),
    CONSTRAINT agent_sleeps_scope_key_action_id_key UNIQUE (scope_key, action_id),
    CONSTRAINT agent_sleeps_timer_run_id_key UNIQUE (timer_run_id),
    CONSTRAINT agent_sleeps_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id),
    CONSTRAINT agent_sleeps_runtime_id_task_id_agent_id_fkey FOREIGN KEY (runtime_id, task_id, agent_id) REFERENCES task_instances(id, task_id, agent_id),
    CONSTRAINT agent_sleeps_task_id_fkey FOREIGN KEY (task_id) REFERENCES task_records(id),
    CONSTRAINT agent_sleeps_body_hash_check CHECK ((length(body_hash) = 32)),
    CONSTRAINT agent_sleeps_check CHECK ((((scope_kind = 'normal') AND (mode = 'normal') AND (task_id IS NULL) AND (runtime_id IS NULL) AND (node_run_id IS NULL) AND (session_epoch IS NOT NULL) AND (session_epoch > 0) AND (scope_key = ('normal/' || (agent_id)))) OR ((scope_kind = 'task') AND (mode IN ('task_node', 'task_conversation')) AND (task_id IS NOT NULL) AND (runtime_id IS NOT NULL) AND (session_epoch IS NULL) AND (scope_key = ((('task/' || (task_id)) || '/agent/') || (agent_id))) AND ((mode <> 'task_node') OR (node_run_id IS NOT NULL))))),
    CONSTRAINT agent_sleeps_check1 CHECK (((entered_at IS NULL) = (wake_at IS NULL))),
    CONSTRAINT agent_sleeps_check2 CHECK (((state <> 'sleeping') OR (entered_at IS NOT NULL))),
    CONSTRAINT agent_sleeps_check3 CHECK (((state <> 'requested') OR (entered_at IS NULL))),
    CONSTRAINT agent_sleeps_check4 CHECK (((state IN ('woken', 'cancelled')) = (ended_at IS NOT NULL))),
    CONSTRAINT agent_sleeps_check5 CHECK (((ended_at IS NULL) = (end_reason IS NULL))),
    CONSTRAINT agent_sleeps_check6 CHECK (((state = 'woken') = (wake_run_id IS NOT NULL))),
    CONSTRAINT agent_sleeps_end_reason_check CHECK ((end_reason IN ('deadline', 'queued_work', 'execution_failed', 'interrupted', 'task_ended', 'node_changed', 'session_reopened', 'owner_deleted'))),
    CONSTRAINT agent_sleeps_mode_check CHECK ((mode IN ('normal', 'task_node', 'task_conversation'))),
    CONSTRAINT agent_sleeps_receipt_check CHECK ((json_type(receipt) = 'object')),
    CONSTRAINT agent_sleeps_revision_check CHECK ((revision > 0)),
    CONSTRAINT agent_sleeps_scope_kind_check CHECK ((scope_kind IN ('normal', 'task'))),
    CONSTRAINT agent_sleeps_seconds_check CHECK (((seconds >= 1) AND (seconds <= 2147483647))),
    CONSTRAINT agent_sleeps_state_check CHECK ((state IN ('requested', 'sleeping', 'woken', 'cancelled')))
) STRICT;
CREATE INDEX agent_sleeps_due ON agent_sleeps (wake_at, id) WHERE (state = 'sleeping');
CREATE UNIQUE INDEX agent_sleeps_one_active ON agent_sleeps (scope_key) WHERE (state IN ('requested', 'sleeping'));

-- ===== 00012（Go 迁移 project credentials） =====

CREATE TABLE agent_credential_selections (
    agent_id        TEXT NOT NULL,
    project_id      TEXT NOT NULL,
    purpose         TEXT NOT NULL,
    slot_key        TEXT NOT NULL,
    account_id      TEXT,
    mode            TEXT NOT NULL,
    CONSTRAINT agent_credential_selections_pkey PRIMARY KEY (agent_id, purpose, slot_key),
    CONSTRAINT agent_credential_selections_account_id_project_id_purpose__fkey FOREIGN KEY (account_id, project_id, purpose, slot_key) REFERENCES project_credential_accounts(id, project_id, purpose, slot_key),
    CONSTRAINT agent_credential_selections_agent_id_project_id_fkey FOREIGN KEY (agent_id, project_id) REFERENCES agents(id, project_id),
    CONSTRAINT agent_credential_selections_check CHECK ((((mode = 'explicit') AND (account_id IS NOT NULL)) OR ((mode = 'automatic_override') AND (account_id IS NULL) AND (purpose = 'model')))),
    CONSTRAINT agent_credential_selections_mode_check CHECK ((mode IN ('explicit', 'automatic_override'))),
    CONSTRAINT agent_credential_selections_purpose_check CHECK ((purpose IN ('model', 'cli')))
) STRICT;

CREATE TABLE agent_model_legacy_cleared (
    agent_id        TEXT NOT NULL,
    project_id      TEXT NOT NULL,
    CONSTRAINT agent_model_legacy_cleared_pkey PRIMARY KEY (agent_id),
    CONSTRAINT agent_model_legacy_cleared_agent_id_project_id_fkey FOREIGN KEY (agent_id, project_id) REFERENCES agents(id, project_id)
) STRICT;

CREATE TABLE project_cli_versions (
    id                TEXT NOT NULL,
    account_id        TEXT NOT NULL,
    purpose           TEXT NOT NULL DEFAULT 'cli',
    cli_name          TEXT NOT NULL,
    template_source   TEXT NOT NULL,
    template_version  TEXT NOT NULL,
    secret            BLOB NOT NULL,
    field_names       TEXT NOT NULL CHECK (json_type(field_names) = 'array'),
    env_names         TEXT NOT NULL CHECK (json_type(env_names) = 'array'),
    file_paths        TEXT NOT NULL CHECK (json_type(file_paths) = 'array'),
    created_at        INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    updated_at        INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    auth_method       TEXT NOT NULL DEFAULT 'static',
    account_label     TEXT,
    CONSTRAINT project_cli_versions_pkey PRIMARY KEY (id),
    CONSTRAINT cli_grant_identity UNIQUE (id, account_id, cli_name, auth_method),
    CONSTRAINT project_cli_versions_id_account_id_key UNIQUE (id, account_id),
    CONSTRAINT project_cli_versions_account_id_purpose_cli_name_fkey FOREIGN KEY (account_id, purpose, cli_name) REFERENCES project_credential_accounts(id, purpose, cli_name),
    CONSTRAINT cli_auth_kind CHECK ((((auth_method = 'static') OR ((auth_method = 'service_account') AND (cli_name = 'gcloud')) OR ((auth_method = 'oauth') AND (cli_name IN ('gcloud', 'gh', 'lark-cli')))) AND ((auth_method <> 'oauth') OR ((account_label IS NOT NULL) AND (length(account_label) > 0))))),
    CONSTRAINT project_cli_versions_auth_method_check CHECK ((auth_method IN ('static', 'service_account', 'oauth'))),
    CONSTRAINT project_cli_versions_purpose_check CHECK ((purpose = 'cli'))
) STRICT;

CREATE TABLE project_credential_accounts (
    id                        TEXT NOT NULL,
    project_id                TEXT NOT NULL,
    purpose                   TEXT NOT NULL,
    provider                  TEXT,
    cli_name                  TEXT,
    slot_key                  TEXT GENERATED ALWAYS AS (COALESCE(provider, cli_name)) STORED,
    name                      TEXT NOT NULL,
    revision                  INTEGER NOT NULL DEFAULT 1,
    current_model_version_id  TEXT,
    current_cli_version_id    TEXT,
    created_at                INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    updated_at                INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    deleted_at                INTEGER,
    CONSTRAINT project_credential_accounts_pkey PRIMARY KEY (id),
    CONSTRAINT project_credential_accounts_id_project_id_key UNIQUE (id, project_id),
    CONSTRAINT project_credential_accounts_id_project_id_purpose_slot_key_key UNIQUE (id, project_id, purpose, slot_key),
    CONSTRAINT project_credential_accounts_id_purpose_cli_name_key UNIQUE (id, purpose, cli_name),
    CONSTRAINT project_credential_accounts_id_purpose_provider_key UNIQUE (id, purpose, provider),
    CONSTRAINT account_current_cli_owner FOREIGN KEY (current_cli_version_id, id) REFERENCES project_cli_versions(id, account_id) DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT account_current_model_owner FOREIGN KEY (current_model_version_id, id) REFERENCES model_auth_versions(id, project_account_id) DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT project_credential_accounts_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id),
    CONSTRAINT project_credential_accounts_check CHECK ((((purpose = 'model') AND (provider IS NOT NULL) AND (cli_name IS NULL) AND (current_cli_version_id IS NULL)) OR ((purpose = 'cli') AND (cli_name IS NOT NULL) AND (provider IS NULL) AND (current_model_version_id IS NULL)))),
    CONSTRAINT project_credential_accounts_name_check CHECK (((length(name) >= 1) AND (length(name) <= 200))),
    CONSTRAINT project_credential_accounts_purpose_check CHECK ((purpose IN ('model', 'cli'))),
    CONSTRAINT project_credential_accounts_revision_check CHECK ((revision >= 1))
) STRICT;
CREATE INDEX project_credential_accounts_slot ON project_credential_accounts (project_id, purpose, slot_key, created_at, id) WHERE (deleted_at IS NULL);

CREATE TABLE project_credential_legacy_rows (
    id               TEXT NOT NULL,
    project_id       TEXT NOT NULL,
    source_agent_id  TEXT NOT NULL,
    secret           BLOB NOT NULL,
    field_names      TEXT NOT NULL CHECK (json_type(field_names) = 'array'),
    env_names        TEXT NOT NULL CHECK (json_type(env_names) = 'array'),
    file_paths       TEXT NOT NULL CHECK (json_type(file_paths) = 'array'),
    updated_at       INTEGER NOT NULL,
    CONSTRAINT project_credential_legacy_rows_pkey PRIMARY KEY (id),
    CONSTRAINT project_credential_legacy_rows_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id),
    CONSTRAINT project_credential_legacy_rows_source_agent_id_fkey FOREIGN KEY (source_agent_id) REFERENCES agents(id)
) STRICT;

CREATE TABLE project_model_auth_operations (
    id                 TEXT NOT NULL,
    project_id         TEXT NOT NULL,
    provider           TEXT NOT NULL,
    target_account_id  TEXT,
    target_revision    INTEGER,
    name               TEXT,
    account_id         TEXT,
    status             TEXT NOT NULL DEFAULT 'pending',
    authorization_url  TEXT,
    error_code         TEXT,
    started_at         INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    expires_at         INTEGER NOT NULL,
    completed_at       INTEGER,
    CONSTRAINT project_model_auth_operations_pkey PRIMARY KEY (id),
    CONSTRAINT project_model_auth_operations_account_id_project_id_fkey FOREIGN KEY (account_id, project_id) REFERENCES project_credential_accounts(id, project_id),
    CONSTRAINT project_model_auth_operations_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id),
    CONSTRAINT project_model_auth_operations_target_account_id_project_id_fkey FOREIGN KEY (target_account_id, project_id) REFERENCES project_credential_accounts(id, project_id),
    CONSTRAINT project_model_auth_operations_check CHECK ((((target_account_id IS NULL) AND (target_revision IS NULL) AND (name IS NOT NULL)) OR ((target_account_id IS NOT NULL) AND (target_revision >= 1) AND (name IS NULL)))),
    CONSTRAINT project_model_auth_operations_check1 CHECK (((status IN ('succeeded', 'failed', 'cancelled', 'expired')) = (completed_at IS NOT NULL))),
    CONSTRAINT project_model_auth_operations_check2 CHECK (((status IN ('pending', 'awaiting_authorization')) OR (authorization_url IS NULL))),
    CONSTRAINT project_model_auth_operations_provider_check CHECK ((provider = 'openai-codex')),
    CONSTRAINT project_model_auth_operations_status_check CHECK ((status IN ('pending', 'awaiting_authorization', 'succeeded', 'failed', 'cancelled', 'expired')))
) STRICT;
CREATE UNIQUE INDEX project_model_auth_active_new ON project_model_auth_operations (project_id, provider) WHERE ((target_account_id IS NULL) AND (status IN ('pending', 'awaiting_authorization')));
CREATE UNIQUE INDEX project_model_auth_active_target ON project_model_auth_operations (target_account_id) WHERE ((target_account_id IS NOT NULL) AND (status IN ('pending', 'awaiting_authorization')));

CREATE TABLE project_model_legacy_bindings (
    version_id       TEXT NOT NULL,
    source_agent_id  TEXT NOT NULL,
    provider         TEXT NOT NULL,
    account_id       TEXT NOT NULL,
    project_id       TEXT NOT NULL,
    CONSTRAINT project_model_legacy_bindings_pkey PRIMARY KEY (version_id),
    CONSTRAINT project_model_legacy_bindings_account_id_project_id_fkey FOREIGN KEY (account_id, project_id) REFERENCES project_credential_accounts(id, project_id),
    CONSTRAINT project_model_legacy_bindings_source_agent_id_fkey FOREIGN KEY (source_agent_id) REFERENCES agents(id),
    CONSTRAINT project_model_legacy_bindings_version_id_fkey FOREIGN KEY (version_id) REFERENCES model_auth_versions(id) ON DELETE CASCADE
) STRICT;

-- ===== 00013_db_connection_sets.sql =====

CREATE TABLE agent_db_connection_members (
    agent_id        TEXT NOT NULL,
    project_id      TEXT NOT NULL,
    cli_name        TEXT NOT NULL,
    account_id      TEXT NOT NULL,
    purpose         TEXT NOT NULL DEFAULT 'cli',
    CONSTRAINT agent_db_connection_members_pkey PRIMARY KEY (agent_id, cli_name, account_id),
    CONSTRAINT agent_db_connection_members_account_id_project_id_purpose__fkey FOREIGN KEY (account_id, project_id, purpose, cli_name) REFERENCES project_credential_accounts(id, project_id, purpose, slot_key),
    CONSTRAINT agent_db_connection_members_agent_id_project_id_cli_name_fkey FOREIGN KEY (agent_id, project_id, cli_name) REFERENCES agent_db_connection_sets(agent_id, project_id, cli_name) ON DELETE CASCADE,
    CONSTRAINT agent_db_connection_members_purpose_check CHECK ((purpose = 'cli'))
) STRICT;
CREATE INDEX agent_db_connection_members_account ON agent_db_connection_members (account_id);

CREATE TABLE agent_db_connection_sets (
    agent_id        TEXT NOT NULL,
    project_id      TEXT NOT NULL,
    cli_name        TEXT NOT NULL,
    CONSTRAINT agent_db_connection_sets_pkey PRIMARY KEY (agent_id, cli_name),
    CONSTRAINT agent_db_connection_sets_agent_id_project_id_cli_name_key UNIQUE (agent_id, project_id, cli_name),
    CONSTRAINT agent_db_connection_sets_agent_id_cli_name_fkey FOREIGN KEY (agent_id, cli_name) REFERENCES agent_clis(agent_id, cli_name) ON DELETE CASCADE,
    CONSTRAINT agent_db_connection_sets_agent_id_project_id_fkey FOREIGN KEY (agent_id, project_id) REFERENCES agents(id, project_id),
    CONSTRAINT agent_db_connection_sets_cli_name_check CHECK ((cli_name IN ('psql', 'mysql')))
) STRICT;

-- ===== 00014_google_cli_auth.sql =====

CREATE TABLE google_cli_grants (
    version_id             TEXT NOT NULL,
    account_id             TEXT NOT NULL,
    cli_name               TEXT NOT NULL DEFAULT 'gcloud',
    auth_method            TEXT NOT NULL DEFAULT 'oauth',
    generation             INTEGER NOT NULL DEFAULT 1,
    refresh_state          TEXT NOT NULL DEFAULT 'ready',
    lease_id               TEXT,
    lease_agent_id         TEXT,
    lease_until            INTEGER,
    error_code             TEXT,
    updated_at             INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    lease_verification_id  TEXT,
    CONSTRAINT google_cli_grants_pkey PRIMARY KEY (version_id),
    CONSTRAINT google_cli_grants_lease_agent_id_fkey FOREIGN KEY (lease_agent_id) REFERENCES agents(id),
    CONSTRAINT google_cli_grants_version_id_account_id_cli_name_auth_meth_fkey FOREIGN KEY (version_id, account_id, cli_name, auth_method) REFERENCES project_cli_versions(id, account_id, cli_name, auth_method) ON DELETE CASCADE,
    CONSTRAINT google_cli_verification_lease_identity FOREIGN KEY (lease_verification_id, version_id, account_id, cli_name, auth_method) REFERENCES cli_credential_verifications(id, version_id, account_id, cli_name, auth_method),
    CONSTRAINT google_cli_grants_auth_method_check CHECK ((auth_method = 'oauth')),
    CONSTRAINT google_cli_grants_cli_name_check CHECK ((cli_name = 'gcloud')),
    CONSTRAINT google_cli_grants_generation_check CHECK ((generation > 0)),
    CONSTRAINT google_cli_grants_refresh_state_check CHECK ((refresh_state IN ('ready', 'refreshing', 'unavailable'))),
    CONSTRAINT google_cli_refresh_lease_owner CHECK ((((refresh_state = 'refreshing') AND (lease_id IS NOT NULL) AND (lease_until IS NOT NULL) AND (((lease_agent_id IS NOT NULL) + (lease_verification_id IS NOT NULL)) = 1)) OR ((refresh_state <> 'refreshing') AND (lease_id IS NULL) AND (lease_agent_id IS NULL) AND (lease_verification_id IS NULL) AND (lease_until IS NULL))))
) STRICT;

CREATE TABLE project_cli_auth_operations (
    id                 TEXT NOT NULL,
    project_id         TEXT NOT NULL,
    cli_name           TEXT NOT NULL,
    auth_type          TEXT NOT NULL,
    purpose            TEXT NOT NULL DEFAULT 'cli',
    request_hash       TEXT NOT NULL,
    target_account_id  TEXT,
    target_revision    INTEGER,
    name               TEXT,
    status             TEXT NOT NULL DEFAULT 'pending',
    account_id         TEXT,
    authorization_url  TEXT,
    error_code         TEXT,
    started_at         INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    expires_at         INTEGER NOT NULL,
    completed_at       INTEGER,
    user_code          TEXT,
    source_account_id  TEXT,
    source_version_id  TEXT,
    source_revision    INTEGER,
    CONSTRAINT project_cli_auth_operations_pkey PRIMARY KEY (id),
    CONSTRAINT project_cli_auth_operations_account_id_project_id_purpose__fkey FOREIGN KEY (account_id, project_id, purpose, cli_name) REFERENCES project_credential_accounts(id, project_id, purpose, slot_key),
    CONSTRAINT project_cli_auth_operations_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id),
    CONSTRAINT project_cli_auth_operations_target_account_id_project_id_p_fkey FOREIGN KEY (target_account_id, project_id, purpose, cli_name) REFERENCES project_credential_accounts(id, project_id, purpose, slot_key),
    CONSTRAINT project_cli_auth_source_account FOREIGN KEY (source_account_id, project_id, purpose, cli_name) REFERENCES project_credential_accounts(id, project_id, purpose, slot_key),
    CONSTRAINT project_cli_auth_github_view CHECK (((cli_name <> 'gh') OR ((status = 'pending') AND (user_code IS NULL) AND (authorization_url IS NULL)) OR ((status = 'awaiting_authorization') AND (user_code IS NOT NULL) AND (length(user_code)=9 AND user_code GLOB '[A-Z0-9][A-Z0-9][A-Z0-9][A-Z0-9]-[A-Z0-9][A-Z0-9][A-Z0-9][A-Z0-9]') AND (authorization_url IS NOT NULL) AND (authorization_url = 'https://github.com/login/device')) OR ((status NOT IN ('pending', 'awaiting_authorization')) AND (user_code IS NULL) AND (authorization_url IS NULL)))),
    CONSTRAINT project_cli_auth_lark_source CHECK ((((cli_name = 'lark-cli') AND (target_account_id IS NULL) AND (source_account_id IS NOT NULL) AND (source_version_id IS NOT NULL) AND (source_revision IS NOT NULL) AND (source_revision > 0)) OR (((cli_name <> 'lark-cli') OR (target_account_id IS NOT NULL)) AND (source_account_id IS NULL) AND (source_version_id IS NULL) AND (source_revision IS NULL)))),
    CONSTRAINT project_cli_auth_lark_view CHECK (((cli_name <> 'lark-cli') OR ((status = 'pending') AND (authorization_url IS NULL) AND (user_code IS NULL)) OR ((status = 'awaiting_authorization') AND (authorization_url IS NOT NULL) AND ((user_code IS NULL) OR ((length(user_code) >= 1) AND (length(user_code) <= 200)))) OR ((status NOT IN ('pending', 'awaiting_authorization')) AND (authorization_url IS NULL) AND (user_code IS NULL)))),
    CONSTRAINT project_cli_auth_operations_check1 CHECK (((status IN ('pending', 'awaiting_authorization')) OR (authorization_url IS NULL))),
    CONSTRAINT project_cli_auth_operations_check2 CHECK ((((status IN ('pending', 'awaiting_authorization')) AND (completed_at IS NULL)) OR ((status NOT IN ('pending', 'awaiting_authorization')) AND (completed_at IS NOT NULL)))),
    CONSTRAINT project_cli_auth_operations_purpose_check CHECK ((purpose = 'cli')),
    CONSTRAINT project_cli_auth_operations_request_hash_check CHECK ((length(request_hash) = 64)),
    CONSTRAINT project_cli_auth_operations_status_check CHECK ((status IN ('pending', 'awaiting_authorization', 'succeeded', 'failed', 'cancelled', 'expired'))),
    CONSTRAINT project_cli_auth_pair CHECK ((((cli_name = 'gcloud') AND (auth_type = 'google_user')) OR ((cli_name = 'gh') AND (auth_type = 'github_user')) OR ((cli_name = 'lark-cli') AND (auth_type = 'lark_user')))),
    CONSTRAINT project_cli_auth_target CHECK ((((target_account_id IS NULL) AND (target_revision IS NULL) AND (((cli_name = 'gcloud') AND (name IS NOT NULL)) OR ((cli_name IN ('gh', 'lark-cli')) AND (name IS NULL)))) OR ((target_account_id IS NOT NULL) AND (target_revision IS NOT NULL) AND (target_revision > 0) AND (name IS NULL)))),
    CONSTRAINT project_cli_auth_user_code CHECK (((cli_name IN ('gh', 'lark-cli')) OR (user_code IS NULL)))
) STRICT;
CREATE UNIQUE INDEX project_cli_auth_active_target ON project_cli_auth_operations (project_id, cli_name, COALESCE(target_account_id, '00000000-0000-0000-0000-000000000000')) WHERE (status IN ('pending', 'awaiting_authorization'));

-- ===== 00015_task_console_messages.sql =====

CREATE TABLE task_console_chat_eligibility (
    task_id         TEXT NOT NULL,
    node_run_id     TEXT NOT NULL,
    source_run_id   TEXT NOT NULL,
    recorded_at     INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT task_console_chat_eligibility_pkey PRIMARY KEY (task_id, node_run_id),
    CONSTRAINT task_console_chat_eligibility_source_run_id_fkey FOREIGN KEY (source_run_id) REFERENCES task_turns(id),
    CONSTRAINT task_console_chat_eligibility_task_id_fkey FOREIGN KEY (task_id) REFERENCES task_records(id)
) STRICT;

CREATE TABLE task_console_message_lineage (
    run_id          TEXT NOT NULL,
    task_id         TEXT NOT NULL,
    node_run_id     TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    runtime_id      TEXT NOT NULL,
    execution_mode  TEXT NOT NULL,
    CONSTRAINT task_console_message_lineage_pkey PRIMARY KEY (run_id),
    CONSTRAINT task_console_message_lineage_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id),
    CONSTRAINT task_console_message_lineage_run_id_fkey FOREIGN KEY (run_id) REFERENCES task_turns(id),
    CONSTRAINT task_console_message_lineage_runtime_id_fkey FOREIGN KEY (runtime_id) REFERENCES task_instances(id),
    CONSTRAINT task_console_message_lineage_task_id_fkey FOREIGN KEY (task_id) REFERENCES task_records(id),
    CONSTRAINT task_console_message_lineage_execution_mode_check CHECK ((execution_mode IN ('task_node', 'task_conversation')))
) STRICT;
CREATE INDEX task_console_lineage_target ON task_console_message_lineage (task_id, node_run_id);

-- ===== 00016_cli_credential_verification.sql =====

CREATE TABLE cli_credential_verifications (
    id                    TEXT NOT NULL,
    project_id            TEXT NOT NULL,
    account_id            TEXT NOT NULL,
    version_id            TEXT NOT NULL,
    live_version_id       TEXT,
    purpose               TEXT NOT NULL DEFAULT 'cli',
    cli_name              TEXT NOT NULL,
    auth_method           TEXT NOT NULL,
    request_hash          TEXT NOT NULL,
    initiator_kind        TEXT NOT NULL,
    initiator_id          TEXT,
    template_source       TEXT NOT NULL,
    credential_format     TEXT NOT NULL,
    template_fingerprint  TEXT NOT NULL,
    tool_version          TEXT,
    adapter_revision      TEXT NOT NULL,
    probe_kind            TEXT NOT NULL,
    status                TEXT NOT NULL DEFAULT 'pending',
    phase                 TEXT DEFAULT 'preparing_tool',
    started_at            INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    expires_at            INTEGER NOT NULL,
    completed_at          INTEGER,
    result_outcome        TEXT,
    verified_at           INTEGER,
    duration_ms           INTEGER,
    error_code            TEXT,
    http_status           INTEGER,
    CONSTRAINT cli_credential_verifications_pkey PRIMARY KEY (id),
    CONSTRAINT cli_credential_verifications_id_version_id_account_id_cli_n_key UNIQUE (id, version_id, account_id, cli_name, auth_method),
    CONSTRAINT cli_credential_verifications_account_id_project_id_purpose_fkey FOREIGN KEY (account_id, project_id, purpose, cli_name) REFERENCES project_credential_accounts(id, project_id, purpose, slot_key),
    CONSTRAINT cli_credential_verifications_live_version_id_account_id_cl_fkey FOREIGN KEY (live_version_id, account_id, cli_name, auth_method) REFERENCES project_cli_versions(id, account_id, cli_name, auth_method),
    CONSTRAINT cli_credential_verifications_adapter_revision_check CHECK ((length(adapter_revision) > 0)),
    CONSTRAINT cli_credential_verifications_auth_method_check CHECK ((auth_method IN ('static', 'service_account', 'oauth'))),
    CONSTRAINT cli_credential_verifications_check CHECK (((live_version_id IS NULL) OR (live_version_id = version_id))),
    CONSTRAINT cli_credential_verifications_check1 CHECK ((expires_at > started_at)),
    CONSTRAINT cli_credential_verifications_check2 CHECK ((((initiator_kind = 'console') AND (initiator_id IS NULL)) OR ((initiator_kind = 'external') AND (initiator_id IS NOT NULL) AND (initiator_id <> '00000000-0000-0000-0000-000000000000')))),
    CONSTRAINT cli_credential_verifications_check3 CHECK ((((status IN ('pending', 'running')) AND (live_version_id IS NOT NULL) AND (phase IS NOT NULL) AND (completed_at IS NULL) AND (result_outcome IS NULL) AND (verified_at IS NULL) AND (duration_ms IS NULL) AND (error_code IS NULL) AND (http_status IS NULL)) OR ((status IN ('succeeded', 'failed', 'cancelled', 'expired')) AND (phase IS NULL) AND (completed_at IS NOT NULL) AND (result_outcome IS NOT NULL) AND (duration_ms IS NOT NULL) AND (((status = 'succeeded') AND (result_outcome = 'passed') AND (verified_at IS NOT NULL) AND (tool_version IS NOT NULL) AND (error_code IS NULL)) OR ((status = 'failed') AND (result_outcome = 'failed') AND (verified_at IS NULL) AND (error_code IS NOT NULL)) OR ((status = 'cancelled') AND (result_outcome = 'cancelled') AND (verified_at IS NULL) AND (error_code IS NULL)) OR ((status = 'expired') AND (result_outcome = 'expired') AND (verified_at IS NULL) AND (error_code = 'timeout')))))),
    CONSTRAINT cli_credential_verifications_credential_format_check CHECK ((length(credential_format) > 0)),
    CONSTRAINT cli_credential_verifications_duration_ms_check CHECK ((duration_ms >= 0)),
    CONSTRAINT cli_credential_verifications_error_code_check CHECK ((error_code IN ('tool_unsupported', 'tool_prepare_failed', 'credential_unavailable', 'configuration_invalid', 'tls_failed', 'authentication_failed', 'database_unavailable', 'permission_denied', 'provider_policy', 'rate_limited', 'network_failed', 'timeout', 'requires_login', 'target_changed', 'server_restarted', 'probe_failed', 'internal_error'))),
    CONSTRAINT cli_credential_verifications_http_status_check CHECK (((http_status >= 100) AND (http_status <= 599))),
    CONSTRAINT cli_credential_verifications_id_check CHECK ((id <> '00000000-0000-0000-0000-000000000000')),
    CONSTRAINT cli_credential_verifications_initiator_kind_check CHECK ((initiator_kind IN ('console', 'external'))),
    CONSTRAINT cli_credential_verifications_phase_check CHECK ((phase IN ('preparing_tool', 'resolving_credentials', 'probing', 'recording'))),
    CONSTRAINT cli_credential_verifications_probe_kind_check CHECK ((probe_kind IN ('database_select1', 'github_user', 'google_crm_read', 'lark_bot'))),
    CONSTRAINT cli_credential_verifications_purpose_check CHECK ((purpose = 'cli')),
    CONSTRAINT cli_credential_verifications_request_hash_check CHECK ((length(request_hash)=64 AND request_hash NOT GLOB '*[^0-9a-f]*')),
    CONSTRAINT cli_credential_verifications_result_outcome_check CHECK ((result_outcome IN ('passed', 'failed', 'cancelled', 'expired'))),
    CONSTRAINT cli_credential_verifications_status_check CHECK ((status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled', 'expired'))),
    CONSTRAINT cli_credential_verifications_template_fingerprint_check CHECK ((length(template_fingerprint)=64 AND template_fingerprint NOT GLOB '*[^0-9a-f]*')),
    CONSTRAINT cli_credential_verifications_template_source_check CHECK ((length(template_source) > 0)),
    CONSTRAINT cli_credential_verifications_tool_version_check CHECK ((length(tool_version) > 0)),
    CONSTRAINT cli_credential_verifications_version_id_check CHECK ((version_id <> '00000000-0000-0000-0000-000000000000'))
) STRICT;
CREATE INDEX cli_verification_account_version ON cli_credential_verifications (account_id, version_id, started_at DESC, id DESC);
CREATE UNIQUE INDEX cli_verification_active_account ON cli_credential_verifications (account_id) WHERE (status IN ('pending', 'running'));

-- ===== 00017_local_cli_credential_import.sql =====

CREATE TABLE cli_credential_import_items (
    operation_id    TEXT NOT NULL,
    candidate_id    TEXT NOT NULL,
    ordinal         INTEGER NOT NULL,
    cli_name        TEXT NOT NULL,
    label           TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',
    account_id      TEXT,
    version_id      TEXT,
    error_code      TEXT,
    CONSTRAINT cli_credential_import_items_pkey PRIMARY KEY (operation_id, candidate_id),
    CONSTRAINT cli_credential_import_items_operation_id_ordinal_key UNIQUE (operation_id, ordinal),
    CONSTRAINT cli_credential_import_items_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES cli_credential_import_operations(id),
    CONSTRAINT cli_credential_import_items_account_id_check CHECK ((account_id <> '00000000-0000-0000-0000-000000000000')),
    CONSTRAINT cli_credential_import_items_candidate_id_check CHECK ((candidate_id <> '00000000-0000-0000-0000-000000000000')),
    CONSTRAINT cli_credential_import_items_check CHECK ((((status IN ('imported', 'already_imported')) AND (account_id IS NOT NULL) AND (version_id IS NOT NULL) AND (error_code IS NULL)) OR ((status NOT IN ('imported', 'already_imported')) AND (account_id IS NULL) AND (version_id IS NULL)))),
    CONSTRAINT cli_credential_import_items_check1 CHECK (((status = 'failed') = (error_code IS NOT NULL))),
    CONSTRAINT cli_credential_import_items_cli_name_check CHECK ((cli_name IN ('gh', 'lark-cli', 'gcloud', 'psql', 'mysql'))),
    CONSTRAINT cli_credential_import_items_error_code_check CHECK (((error_code IS NULL) OR (error_code IN ('source_changed', 'source_unreadable', 'missing_field', 'unsupported', 'template_mismatch', 'supplement_override', 'invalid_credential', 'consumer_failed', 'precondition_failed', 'source_conflict', 'provenance_key_changed', 'cancelled', 'timeout', 'worker_restarted', 'project_deleted', 'internal_error')))),
    CONSTRAINT cli_credential_import_items_label_check CHECK ((length(label) <= 200)),
    CONSTRAINT cli_credential_import_items_ordinal_check CHECK (((ordinal >= 0) AND (ordinal < 20))),
    CONSTRAINT cli_credential_import_items_status_check CHECK ((status IN ('pending', 'not_committed', 'imported', 'already_imported', 'failed'))),
    CONSTRAINT cli_credential_import_items_version_id_check CHECK ((version_id <> '00000000-0000-0000-0000-000000000000'))
) STRICT;

CREATE TABLE cli_credential_import_operations (
    id                              TEXT NOT NULL,
    project_id                      TEXT NOT NULL,
    scan_id                         TEXT NOT NULL,
    instance_id                     TEXT NOT NULL,
    initiator_kind                  TEXT NOT NULL DEFAULT 'console',
    request_fingerprint             BLOB NOT NULL,
    expected_credentials_revision   INTEGER NOT NULL,
    provenance_key_id               TEXT NOT NULL,
    status                          TEXT NOT NULL DEFAULT 'pending',
    phase                           TEXT DEFAULT 'preparing',
    created_at                      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    deadline_at                     INTEGER NOT NULL,
    completed_at                    INTEGER,
    error_code                      TEXT,
    committed_credentials_revision  INTEGER,
    CONSTRAINT cli_credential_import_operations_pkey PRIMARY KEY (id),
    CONSTRAINT cli_credential_import_operations_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id),
    CONSTRAINT cli_credential_import_operat_committed_credentials_revisi_check CHECK ((committed_credentials_revision >= 1)),
    CONSTRAINT cli_credential_import_operat_expected_credentials_revisio_check CHECK ((expected_credentials_revision >= 1)),
    CONSTRAINT cli_credential_import_operations_check CHECK ((deadline_at > created_at)),
    CONSTRAINT cli_credential_import_operations_check1 CHECK ((((status IN ('pending', 'running')) AND (phase IS NOT NULL) AND (completed_at IS NULL) AND (error_code IS NULL) AND (committed_credentials_revision IS NULL)) OR ((status IN ('succeeded', 'failed', 'cancelled', 'expired')) AND (phase IS NULL) AND (completed_at IS NOT NULL) AND (((status = 'succeeded') AND (error_code IS NULL) AND (committed_credentials_revision IS NOT NULL)) OR ((status <> 'succeeded') AND (error_code IS NOT NULL) AND (committed_credentials_revision IS NULL)))))),
    CONSTRAINT cli_credential_import_operations_error_code_check CHECK (((error_code IS NULL) OR (error_code IN ('source_changed', 'source_unreadable', 'missing_field', 'unsupported', 'template_mismatch', 'supplement_override', 'invalid_credential', 'consumer_failed', 'precondition_failed', 'source_conflict', 'provenance_key_changed', 'cancelled', 'timeout', 'worker_restarted', 'project_deleted', 'internal_error')))),
    CONSTRAINT cli_credential_import_operations_id_check CHECK ((id <> '00000000-0000-0000-0000-000000000000')),
    CONSTRAINT cli_credential_import_operations_initiator_kind_check CHECK ((initiator_kind = 'console')),
    CONSTRAINT cli_credential_import_operations_instance_id_check CHECK ((instance_id <> '00000000-0000-0000-0000-000000000000')),
    CONSTRAINT cli_credential_import_operations_phase_check CHECK ((phase IN ('preparing', 'extracting', 'validating', 'committing'))),
    CONSTRAINT cli_credential_import_operations_provenance_key_id_check CHECK (((length(provenance_key_id) >= 1) AND (length(provenance_key_id) <= 200))),
    CONSTRAINT cli_credential_import_operations_request_fingerprint_check CHECK ((length(request_fingerprint) = 32)),
    CONSTRAINT cli_credential_import_operations_scan_id_check CHECK ((scan_id <> '00000000-0000-0000-0000-000000000000')),
    CONSTRAINT cli_credential_import_operations_status_check CHECK ((status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled', 'expired')))
) STRICT;
CREATE UNIQUE INDEX cli_import_active_project ON cli_credential_import_operations (project_id) WHERE (status IN ('pending', 'running'));

CREATE TABLE cli_credential_import_receipts (
    project_id         TEXT NOT NULL,
    provenance_key_id  TEXT NOT NULL,
    origin_mac         BLOB NOT NULL,
    content_mac        BLOB NOT NULL,
    purpose            TEXT NOT NULL DEFAULT 'cli',
    cli_name           TEXT NOT NULL,
    credential_format  TEXT NOT NULL,
    account_id         TEXT NOT NULL,
    version_id         TEXT NOT NULL,
    live_account_id    TEXT,
    live_version_id    TEXT,
    created_at         INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT cli_credential_import_receipts_pkey PRIMARY KEY (project_id, provenance_key_id, origin_mac),
    CONSTRAINT cli_credential_import_receipt_live_account_id_project_id_p_fkey FOREIGN KEY (live_account_id, project_id, purpose, cli_name) REFERENCES project_credential_accounts(id, project_id, purpose, slot_key),
    CONSTRAINT cli_credential_import_receipts_live_version_id_account_id_fkey FOREIGN KEY (live_version_id, account_id) REFERENCES project_cli_versions(id, account_id),
    CONSTRAINT cli_credential_import_receipts_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id),
    CONSTRAINT cli_credential_import_receipts_account_id_check CHECK ((account_id <> '00000000-0000-0000-0000-000000000000')),
    CONSTRAINT cli_credential_import_receipts_check CHECK (((live_account_id IS NULL) OR (live_account_id = account_id))),
    CONSTRAINT cli_credential_import_receipts_check1 CHECK (((live_version_id IS NULL) OR (live_version_id = version_id))),
    CONSTRAINT cli_credential_import_receipts_cli_name_check CHECK ((cli_name IN ('gh', 'lark-cli', 'gcloud', 'psql', 'mysql'))),
    CONSTRAINT cli_credential_import_receipts_content_mac_check CHECK ((length(content_mac) = 32)),
    CONSTRAINT cli_credential_import_receipts_credential_format_check CHECK ((length(credential_format) > 0)),
    CONSTRAINT cli_credential_import_receipts_origin_mac_check CHECK ((length(origin_mac) = 32)),
    CONSTRAINT cli_credential_import_receipts_provenance_key_id_check CHECK (((length(provenance_key_id) >= 1) AND (length(provenance_key_id) <= 200))),
    CONSTRAINT cli_credential_import_receipts_purpose_check CHECK ((purpose = 'cli')),
    CONSTRAINT cli_credential_import_receipts_version_id_check CHECK ((version_id <> '00000000-0000-0000-0000-000000000000'))
) STRICT;

-- ===== 00018_github_cli_login.sql =====

CREATE TABLE github_cli_identities (
    version_id      TEXT NOT NULL,
    account_id      TEXT NOT NULL,
    cli_name        TEXT NOT NULL DEFAULT 'gh',
    auth_method     TEXT NOT NULL DEFAULT 'oauth',
    host            TEXT NOT NULL,
    user_id         TEXT NOT NULL,
    login           TEXT NOT NULL,
    granted_scopes  TEXT CHECK (json_type(granted_scopes) = 'array'),
    verified_at     INTEGER NOT NULL,
    CONSTRAINT github_cli_identities_pkey PRIMARY KEY (version_id),
    CONSTRAINT github_cli_identities_version_id_account_id_cli_name_auth__fkey FOREIGN KEY (version_id, account_id, cli_name, auth_method) REFERENCES project_cli_versions(id, account_id, cli_name, auth_method) ON DELETE CASCADE,
    CONSTRAINT github_cli_identities_auth_method_check CHECK ((auth_method = 'oauth')),
    CONSTRAINT github_cli_identities_cli_name_check CHECK ((cli_name = 'gh')),
    CONSTRAINT github_cli_identities_host_check CHECK ((host = 'github.com')),
    CONSTRAINT github_cli_identities_login_check CHECK (((length(login) >= 1) AND (length(login) <= 200))),
    CONSTRAINT github_cli_identities_user_id_check CHECK ((user_id GLOB '[1-9]*' AND user_id NOT GLOB '*[^0-9]*'))
) STRICT;

-- ===== 00019_task_node_inbox_chat.sql =====

CREATE TABLE task_node_inbox_messages (
    message_id           TEXT NOT NULL,
    root_message_id      TEXT NOT NULL,
    task_id              TEXT NOT NULL,
    node_run_id          TEXT NOT NULL,
    agent_id             TEXT NOT NULL,
    runtime_id           TEXT NOT NULL,
    execution_mode       TEXT NOT NULL,
    reminder_run_id      TEXT,
    not_executed_reason  TEXT,
    not_executed_at      INTEGER,
    CONSTRAINT task_node_inbox_messages_pkey PRIMARY KEY (message_id),
    CONSTRAINT task_node_inbox_messages_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id),
    CONSTRAINT task_node_inbox_messages_message_id_fkey FOREIGN KEY (message_id) REFERENCES inbox_messages(id),
    CONSTRAINT task_node_inbox_messages_reminder_run_id_fkey FOREIGN KEY (reminder_run_id) REFERENCES task_turns(id),
    CONSTRAINT task_node_inbox_messages_root_message_id_fkey FOREIGN KEY (root_message_id) REFERENCES inbox_messages(id),
    CONSTRAINT task_node_inbox_messages_runtime_id_task_id_agent_id_fkey FOREIGN KEY (runtime_id, task_id, agent_id) REFERENCES task_instances(id, task_id, agent_id),
    CONSTRAINT task_node_inbox_messages_task_id_fkey FOREIGN KEY (task_id) REFERENCES task_records(id),
    CONSTRAINT task_node_inbox_messages_check CHECK (((not_executed_reason IS NULL) = (not_executed_at IS NULL))),
    CONSTRAINT task_node_inbox_messages_execution_mode_check CHECK ((execution_mode IN ('task_node', 'task_conversation')))
) STRICT;
CREATE INDEX task_node_inbox_batch ON task_node_inbox_messages (reminder_run_id);
CREATE INDEX task_node_inbox_scope ON task_node_inbox_messages (task_id, node_run_id);

CREATE TABLE task_node_inbox_run_links (
    message_id      TEXT NOT NULL,
    run_id          TEXT NOT NULL,
    role            TEXT NOT NULL,
    CONSTRAINT task_node_inbox_run_links_pkey PRIMARY KEY (message_id, run_id, role),
    CONSTRAINT task_node_inbox_run_links_message_id_fkey FOREIGN KEY (message_id) REFERENCES task_node_inbox_messages(message_id),
    CONSTRAINT task_node_inbox_run_links_run_id_fkey FOREIGN KEY (run_id) REFERENCES task_turns(id),
    CONSTRAINT task_node_inbox_run_links_role_check CHECK ((role IN ('notification', 'read', 'reply')))
) STRICT;

-- ===== 00020_lark_user_login.sql =====

CREATE TABLE lark_cli_grants (
    version_id      TEXT NOT NULL,
    generation      INTEGER NOT NULL DEFAULT 1,
    refresh_state   TEXT NOT NULL DEFAULT 'ready',
    lease_id        TEXT,
    lease_agent_id  TEXT,
    lease_until     INTEGER,
    error_code      TEXT,
    updated_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT lark_cli_grants_pkey PRIMARY KEY (version_id),
    CONSTRAINT lark_cli_grants_lease_agent_id_fkey FOREIGN KEY (lease_agent_id) REFERENCES agents(id),
    CONSTRAINT lark_cli_grants_version_id_fkey FOREIGN KEY (version_id) REFERENCES lark_cli_identities(version_id) ON DELETE CASCADE,
    CONSTRAINT lark_cli_grants_check CHECK ((((refresh_state = 'refreshing') AND (lease_id IS NOT NULL) AND (lease_agent_id IS NOT NULL) AND (lease_until IS NOT NULL)) OR ((refresh_state <> 'refreshing') AND (lease_id IS NULL) AND (lease_agent_id IS NULL) AND (lease_until IS NULL)))),
    CONSTRAINT lark_cli_grants_generation_check CHECK ((generation > 0)),
    CONSTRAINT lark_cli_grants_refresh_state_check CHECK ((refresh_state IN ('ready', 'refreshing', 'unavailable')))
) STRICT;

CREATE TABLE lark_cli_identities (
    version_id               TEXT NOT NULL,
    account_id               TEXT NOT NULL,
    cli_name                 TEXT NOT NULL DEFAULT 'lark-cli',
    auth_method              TEXT NOT NULL DEFAULT 'oauth',
    brand                    TEXT NOT NULL,
    app_id                   TEXT NOT NULL,
    open_id                  TEXT NOT NULL,
    display_name             TEXT NOT NULL,
    granted_scopes           TEXT CHECK (json_type(granted_scopes) = 'array'),
    verified_at              INTEGER NOT NULL,
    runtime_variant_version  TEXT NOT NULL,
    CONSTRAINT lark_cli_identities_pkey PRIMARY KEY (version_id),
    CONSTRAINT lark_cli_identities_version_id_account_id_cli_name_auth_me_fkey FOREIGN KEY (version_id, account_id, cli_name, auth_method) REFERENCES project_cli_versions(id, account_id, cli_name, auth_method) ON DELETE CASCADE,
    CONSTRAINT lark_cli_identities_app_id_check CHECK (((length(app_id) >= 1) AND (length(app_id) <= 200))),
    CONSTRAINT lark_cli_identities_auth_method_check CHECK ((auth_method = 'oauth')),
    CONSTRAINT lark_cli_identities_brand_check CHECK ((brand IN ('feishu', 'lark'))),
    CONSTRAINT lark_cli_identities_cli_name_check CHECK ((cli_name = 'lark-cli')),
    CONSTRAINT lark_cli_identities_display_name_check CHECK (((length(display_name) >= 1) AND (length(display_name) <= 200))),
    CONSTRAINT lark_cli_identities_open_id_check CHECK (((length(open_id) >= 1) AND (length(open_id) <= 200))),
    CONSTRAINT lark_cli_identities_runtime_variant_version_check CHECK ((runtime_variant_version = 'lark-cli-user-1.0.94-loop-authsidecar.1'))
) STRICT;

-- ===== 00021_task_lark_account_switch.sql =====

CREATE TABLE task_lark_binding_members (
    task_id            TEXT NOT NULL,
    revision           INTEGER NOT NULL,
    runtime_id         TEXT NOT NULL,
    agent_id           TEXT NOT NULL,
    member_role        TEXT NOT NULL,
    previous_revision  INTEGER NOT NULL,
    base_snapshot_sha  TEXT NOT NULL,
    CONSTRAINT task_lark_binding_members_pkey PRIMARY KEY (task_id, revision, runtime_id),
    CONSTRAINT task_lark_binding_members_task_id_revision_member_role_key UNIQUE (task_id, revision, member_role),
    CONSTRAINT task_lark_binding_members_runtime_id_task_id_agent_id_fkey FOREIGN KEY (runtime_id, task_id, agent_id) REFERENCES task_instances(id, task_id, agent_id),
    CONSTRAINT task_lark_binding_members_task_id_revision_fkey FOREIGN KEY (task_id, revision) REFERENCES task_lark_credential_bindings(task_id, revision),
    CONSTRAINT task_lark_binding_members_base_snapshot_sha_check CHECK ((length(base_snapshot_sha)=64 AND base_snapshot_sha NOT GLOB '*[^0-9a-f]*')),
    CONSTRAINT task_lark_binding_members_check CHECK (((previous_revision >= 0) AND (previous_revision < revision))),
    CONSTRAINT task_lark_binding_members_member_role_check CHECK ((member_role IN ('publisher', 'followup')))
) STRICT;

CREATE TABLE task_lark_credential_bindings (
    task_id                   TEXT NOT NULL,
    revision                  INTEGER NOT NULL,
    project_id                TEXT NOT NULL,
    account_id                TEXT NOT NULL,
    version_id                TEXT NOT NULL,
    app_id                    TEXT NOT NULL,
    user_id                   TEXT NOT NULL,
    brand                     TEXT NOT NULL,
    runtime_variant_version   TEXT NOT NULL,
    actor_key                 TEXT NOT NULL,
    action_id                 TEXT NOT NULL,
    continuation_node_run_id  TEXT NOT NULL,
    continuation_turn_id      TEXT NOT NULL,
    created_at                INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT task_lark_credential_bindings_pkey PRIMARY KEY (task_id, revision),
    CONSTRAINT task_lark_credential_bindings_actor_key_action_id_key UNIQUE (actor_key, action_id),
    CONSTRAINT task_lark_credential_bindings_continuation_turn_id_key UNIQUE (continuation_turn_id),
    CONSTRAINT task_lark_credential_bindings_account_id_project_id_fkey FOREIGN KEY (account_id, project_id) REFERENCES project_credential_accounts(id, project_id),
    CONSTRAINT task_lark_credential_bindings_actor_key_action_id_task_id_fkey FOREIGN KEY (actor_key, action_id, task_id) REFERENCES task_lark_switch_actions(actor_key, action_id, task_id),
    CONSTRAINT task_lark_credential_bindings_continuation_turn_id_fkey FOREIGN KEY (continuation_turn_id) REFERENCES task_turns(id),
    CONSTRAINT task_lark_credential_bindings_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id),
    CONSTRAINT task_lark_credential_bindings_task_id_fkey FOREIGN KEY (task_id) REFERENCES task_records(id),
    CONSTRAINT task_lark_credential_bindings_version_id_account_id_fkey FOREIGN KEY (version_id, account_id) REFERENCES project_cli_versions(id, account_id),
    CONSTRAINT task_lark_credential_bindings_brand_check CHECK ((brand IN ('feishu', 'lark'))),
    CONSTRAINT task_lark_credential_bindings_revision_check CHECK ((revision > 0)),
    CONSTRAINT task_lark_credential_bindings_runtime_variant_version_check CHECK ((runtime_variant_version <> ''))
) STRICT;

CREATE TABLE task_lark_deferred_inbox (
    message_id      TEXT NOT NULL,
    runtime_id      TEXT NOT NULL,
    task_id         TEXT NOT NULL,
    actor_key       TEXT NOT NULL,
    action_id       TEXT NOT NULL,
    delivered_at    INTEGER,
    CONSTRAINT task_lark_deferred_inbox_pkey PRIMARY KEY (message_id),
    CONSTRAINT task_lark_deferred_inbox_actor_key_action_id_task_id_fkey FOREIGN KEY (actor_key, action_id, task_id) REFERENCES task_lark_switch_actions(actor_key, action_id, task_id),
    CONSTRAINT task_lark_deferred_inbox_message_id_fkey FOREIGN KEY (message_id) REFERENCES inbox_messages(id),
    CONSTRAINT task_lark_deferred_inbox_runtime_id_task_id_fkey FOREIGN KEY (runtime_id, task_id) REFERENCES task_instances(id, task_id)
) STRICT;

CREATE TABLE task_lark_switch_actions (
    actor_key                 TEXT NOT NULL,
    action_id                 TEXT NOT NULL,
    task_id                   TEXT NOT NULL,
    node_run_id               TEXT NOT NULL,
    request_hash              BLOB NOT NULL,
    status                    TEXT NOT NULL,
    reservation_task_version  INTEGER NOT NULL,
    captured_request          TEXT NOT NULL DEFAULT '{}',
    recovery_record           TEXT,
    receipt                   TEXT CHECK (json_valid(receipt)),
    failure_reason            TEXT,
    created_at                INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    finished_at               INTEGER,
    CONSTRAINT task_lark_switch_actions_pkey PRIMARY KEY (actor_key, action_id),
    CONSTRAINT task_lark_switch_actions_actor_key_action_id_task_id_key UNIQUE (actor_key, action_id, task_id),
    CONSTRAINT task_lark_switch_actions_task_id_fkey FOREIGN KEY (task_id) REFERENCES task_records(id),
    CONSTRAINT task_lark_switch_actions_captured_request_check CHECK ((json_type(captured_request) = 'object')),
    CONSTRAINT task_lark_switch_actions_check CHECK (((status IN ('succeeded', 'failed')) = (finished_at IS NOT NULL))),
    CONSTRAINT task_lark_switch_actions_check1 CHECK (((status <> 'succeeded') OR (receipt IS NOT NULL))),
    CONSTRAINT task_lark_switch_actions_recovery_record_check CHECK (((recovery_record IS NULL) OR (json_type(recovery_record) = 'object'))),
    CONSTRAINT task_lark_switch_actions_request_hash_check CHECK ((length(request_hash) = 32)),
    CONSTRAINT task_lark_switch_actions_reservation_task_version_check CHECK ((reservation_task_version > 0)),
    CONSTRAINT task_lark_switch_actions_status_check CHECK ((status IN ('pending', 'handoff', 'succeeded', 'failed')))
) STRICT;

CREATE TABLE task_lark_switch_fences (
    runtime_id      TEXT NOT NULL,
    task_id         TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    actor_key       TEXT NOT NULL,
    action_id       TEXT NOT NULL,
    member_role     TEXT NOT NULL,
    CONSTRAINT task_lark_switch_fences_pkey PRIMARY KEY (runtime_id),
    CONSTRAINT task_lark_switch_fences_actor_key_action_id_member_role_key UNIQUE (actor_key, action_id, member_role),
    CONSTRAINT task_lark_switch_fences_actor_key_action_id_task_id_fkey FOREIGN KEY (actor_key, action_id, task_id) REFERENCES task_lark_switch_actions(actor_key, action_id, task_id),
    CONSTRAINT task_lark_switch_fences_runtime_id_task_id_agent_id_fkey FOREIGN KEY (runtime_id, task_id, agent_id) REFERENCES task_instances(id, task_id, agent_id),
    CONSTRAINT task_lark_switch_fences_member_role_check CHECK ((member_role IN ('publisher', 'followup')))
) STRICT;

-- ===== 00022_platform_plugins.sql =====

CREATE TABLE agent_plugin_grants (
    agent_id        TEXT NOT NULL,
    plugin_id       TEXT NOT NULL,
    account_ids     TEXT NOT NULL DEFAULT '[]',
    CONSTRAINT agent_plugin_grants_pkey PRIMARY KEY (agent_id, plugin_id),
    CONSTRAINT agent_plugin_grants_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id),
    CONSTRAINT agent_plugin_grants_plugin_id_fkey FOREIGN KEY (plugin_id) REFERENCES plugins(id) ON DELETE CASCADE,
    CONSTRAINT agent_plugin_grants_account_ids_check CHECK ((json_type(account_ids) = 'array'))
) STRICT;

-- ===== 00023_message_images.sql =====

CREATE TABLE message_images (
    id                       TEXT NOT NULL,
    agent_id                 TEXT NOT NULL,
    task_id                  TEXT,
    node_run_id              TEXT,
    runtime_id               TEXT,
    blob_sha                 TEXT NOT NULL,
    mime_type                TEXT NOT NULL,
    size                     INTEGER NOT NULL,
    width                    INTEGER NOT NULL,
    height                   INTEGER NOT NULL,
    conversation_message_id  TEXT,
    inbox_message_id         TEXT,
    created_at               INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT message_images_pkey PRIMARY KEY (id),
    CONSTRAINT message_images_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id),
    CONSTRAINT message_images_blob_sha_fkey FOREIGN KEY (blob_sha) REFERENCES blobs(sha256),
    CONSTRAINT message_images_conversation_message_id_fkey FOREIGN KEY (conversation_message_id) REFERENCES conversation_messages(id),
    CONSTRAINT message_images_inbox_message_id_fkey FOREIGN KEY (inbox_message_id) REFERENCES inbox_messages(id),
    CONSTRAINT message_images_task_id_fkey FOREIGN KEY (task_id) REFERENCES task_records(id),
    CONSTRAINT message_images_check CHECK ((((task_id IS NULL) AND (node_run_id IS NULL) AND (runtime_id IS NULL)) OR ((task_id IS NOT NULL) AND (node_run_id IS NOT NULL) AND (runtime_id IS NOT NULL)))),
    CONSTRAINT message_images_check1 CHECK ((NOT ((conversation_message_id IS NOT NULL) AND (inbox_message_id IS NOT NULL)))),
    CONSTRAINT message_images_check2 CHECK (((conversation_message_id IS NULL) OR (task_id IS NULL))),
    CONSTRAINT message_images_check3 CHECK (((inbox_message_id IS NULL) OR (task_id IS NOT NULL))),
    CONSTRAINT message_images_height_check CHECK ((height > 0)),
    CONSTRAINT message_images_mime_type_check CHECK ((mime_type IN ('image/png', 'image/jpeg', 'image/webp'))),
    CONSTRAINT message_images_size_check CHECK ((size > 0)),
    CONSTRAINT message_images_width_check CHECK ((width > 0))
) STRICT;
CREATE INDEX message_images_inbox ON message_images (inbox_message_id) WHERE (inbox_message_id IS NOT NULL);

-- ===== 00028_queue_items.sql =====

CREATE TABLE agent_actions (
    actor_key       TEXT NOT NULL,
    action_id       TEXT NOT NULL,
    operation       TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    body_hash       BLOB NOT NULL,
    response        TEXT NOT NULL CHECK (json_valid(response)),
    created_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT agent_actions_pkey PRIMARY KEY (actor_key, action_id),
    CONSTRAINT agent_actions_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    CONSTRAINT agent_actions_body_hash_check CHECK ((length(body_hash) = 32))
) STRICT;

-- ===== 00029_agent_folders.sql =====

CREATE TABLE agent_folder_git_pulls (
    id              TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    action_id       TEXT NOT NULL,
    url             TEXT NOT NULL,
    branch          TEXT NOT NULL,
    path            TEXT NOT NULL,
    status          TEXT NOT NULL,
    error_code      TEXT,
    error_message   TEXT,
    created_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    finished_at     INTEGER,
    CONSTRAINT agent_folder_git_pulls_pkey PRIMARY KEY (id),
    CONSTRAINT agent_folder_git_pulls_agent_id_action_id_key UNIQUE (agent_id, action_id),
    CONSTRAINT agent_folder_git_pulls_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    CONSTRAINT agent_folder_git_pulls_check CHECK (((status IN ('succeeded', 'failed')) = (finished_at IS NOT NULL))),
    CONSTRAINT agent_folder_git_pulls_check1 CHECK (((status = 'failed') = (error_code IS NOT NULL))),
    CONSTRAINT agent_folder_git_pulls_error_code_check CHECK ((error_code IN ('target_exists', 'branch_not_found', 'timeout', 'clone_failed', 'server_restart'))),
    CONSTRAINT agent_folder_git_pulls_status_check CHECK ((status IN ('queued', 'running', 'succeeded', 'failed')))
) STRICT;
CREATE UNIQUE INDEX agent_folder_git_pulls_active ON agent_folder_git_pulls (agent_id, path) WHERE (status IN ('queued', 'running'));

CREATE TABLE agent_folder_legacy_applied (
    source_id       TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    files_written   INTEGER NOT NULL,
    files_deleted   INTEGER NOT NULL,
    applied_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT agent_folder_legacy_applied_pkey PRIMARY KEY (source_id),
    CONSTRAINT agent_folder_legacy_applied_source_id_fkey FOREIGN KEY (source_id) REFERENCES agent_folder_sources(id) ON DELETE CASCADE,
    CONSTRAINT agent_folder_legacy_applied_files_deleted_check CHECK ((files_deleted >= 0)),
    CONSTRAINT agent_folder_legacy_applied_files_written_check CHECK ((files_written >= 0))
) STRICT;

-- ===== 00030_credential_pending_wipes.sql =====

CREATE TABLE credential_pending_wipes (
    account_id      TEXT NOT NULL,
    project_id      TEXT NOT NULL,
    created_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT credential_pending_wipes_pkey PRIMARY KEY (account_id),
    CONSTRAINT credential_pending_wipes_account_id_project_id_fkey FOREIGN KEY (account_id, project_id) REFERENCES project_credential_accounts(id, project_id)
) STRICT;
CREATE INDEX credential_pending_wipes_project ON credential_pending_wipes (project_id);

-- ===== 00031_plugins_v2.sql =====

CREATE TABLE extension_dialogs (
    question_message_id  TEXT NOT NULL,
    run_id               TEXT NOT NULL,
    request_id           TEXT NOT NULL,
    method               TEXT NOT NULL,
    CONSTRAINT extension_dialogs_pkey PRIMARY KEY (question_message_id),
    CONSTRAINT extension_dialogs_question_message_id_fkey FOREIGN KEY (question_message_id) REFERENCES inbox_questions(message_id) ON DELETE CASCADE,
    CONSTRAINT extension_dialogs_method_check CHECK ((method IN ('select', 'confirm', 'input')))
) STRICT;

CREATE TABLE plugin_accounts (
    id              TEXT NOT NULL,
    plugin_id       TEXT NOT NULL,
    project_id      TEXT NOT NULL,
    kind            TEXT NOT NULL,
    name            TEXT NOT NULL,
    "values"        TEXT NOT NULL DEFAULT '{}',
    revision        INTEGER NOT NULL DEFAULT 1,
    deleted         INTEGER NOT NULL DEFAULT 0 CHECK (deleted IN (0, 1)),
    CONSTRAINT plugin_accounts_pkey PRIMARY KEY (id),
    CONSTRAINT plugin_accounts_plugin_id_fkey FOREIGN KEY (plugin_id) REFERENCES plugins(id) ON DELETE CASCADE,
    CONSTRAINT plugin_accounts_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id),
    CONSTRAINT plugin_accounts_revision_check CHECK ((revision > 0)),
    CONSTRAINT plugin_accounts_values_check CHECK ((json_type("values") = 'object'))
) STRICT;
CREATE INDEX plugin_accounts_project ON plugin_accounts (project_id, plugin_id, kind) WHERE (NOT deleted);

CREATE TABLE plugin_config (
    plugin_id       TEXT NOT NULL,
    project_id      TEXT,
    "values"        TEXT NOT NULL DEFAULT '{}',
    secrets         TEXT NOT NULL DEFAULT '{}',
    revision        INTEGER NOT NULL DEFAULT 1,
    CONSTRAINT plugin_config_plugin_id_fkey FOREIGN KEY (plugin_id) REFERENCES plugins(id) ON DELETE CASCADE,
    CONSTRAINT plugin_config_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id),
    CONSTRAINT plugin_config_revision_check CHECK ((revision > 0)),
    CONSTRAINT plugin_config_secrets_check CHECK ((json_type(secrets) = 'object')),
    CONSTRAINT plugin_config_values_check CHECK ((json_type("values") = 'object'))
) STRICT;
-- PG 的 UNIQUE NULLS NOT DISTINCT：NULL 视为相同；ON CONFLICT 目标须写同样的表达式。
CREATE UNIQUE INDEX plugin_config_plugin_id_project_id_key ON plugin_config (plugin_id, coalesce(project_id, ''));

CREATE TABLE plugin_event_deliveries (
    plugin_id        TEXT NOT NULL,
    event_id         TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending',
    attempts         INTEGER NOT NULL DEFAULT 0,
    next_attempt_at  INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    last_error       TEXT,
    acked_at         INTEGER,
    duration_ms      INTEGER,
    CONSTRAINT plugin_event_deliveries_pkey PRIMARY KEY (plugin_id, event_id),
    CONSTRAINT plugin_event_deliveries_event_id_fkey FOREIGN KEY (event_id) REFERENCES plugin_events(id) ON DELETE CASCADE,
    CONSTRAINT plugin_event_deliveries_plugin_id_fkey FOREIGN KEY (plugin_id) REFERENCES plugins(id) ON DELETE CASCADE,
    CONSTRAINT plugin_event_deliveries_attempts_check CHECK ((attempts >= 0)),
    CONSTRAINT plugin_event_deliveries_duration_ms_check CHECK ((duration_ms >= 0)),
    CONSTRAINT plugin_event_deliveries_status_check CHECK ((status IN ('pending', 'delivered', 'failed')))
) STRICT;
CREATE INDEX plugin_event_deliveries_acked ON plugin_event_deliveries (acked_at) WHERE (acked_at IS NOT NULL);
CREATE INDEX plugin_event_deliveries_pending ON plugin_event_deliveries (plugin_id, next_attempt_at) WHERE (status = 'pending');

CREATE TABLE plugin_events (
    id              TEXT NOT NULL,
    type            TEXT NOT NULL,
    at              INTEGER NOT NULL,
    project_id      TEXT,
    agent_id        TEXT,
    task_id         TEXT,
    run_id          TEXT,
    data            TEXT NOT NULL DEFAULT '{}',
    CONSTRAINT plugin_events_pkey PRIMARY KEY (id),
    CONSTRAINT plugin_events_data_check CHECK ((json_type(data) = 'object')),
    CONSTRAINT plugin_events_id_check CHECK ((length(id)=26 AND id NOT GLOB '*[^0-9A-HJKMNP-TV-Z]*'))
) STRICT;
CREATE INDEX plugin_events_at ON plugin_events (at, id);

CREATE TABLE ev.plugin_host_log (
    id              INTEGER PRIMARY KEY,
    at              INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    plugin_id       TEXT,
    level           TEXT NOT NULL,
    line            TEXT NOT NULL,
    CONSTRAINT plugin_host_log_level_check CHECK ((level IN ('debug', 'info', 'warn', 'error')))
) STRICT;
CREATE INDEX ev.plugin_host_log_at ON plugin_host_log (at);
CREATE INDEX ev.plugin_host_log_plugin ON plugin_host_log (plugin_id, at DESC);

CREATE TABLE plugin_parties (
    id              TEXT NOT NULL,
    plugin_id       TEXT NOT NULL,
    display_name    TEXT NOT NULL,
    CONSTRAINT plugin_parties_pkey PRIMARY KEY (id),
    CONSTRAINT plugin_parties_plugin_id_key UNIQUE (plugin_id)
) STRICT;

CREATE TABLE plugin_settings (
    singleton       INTEGER NOT NULL DEFAULT 1 CHECK (singleton IN (0, 1)),
    disabled        INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0, 1)),
    markets         TEXT NOT NULL DEFAULT '[]',
    updated_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT plugin_settings_pkey PRIMARY KEY (singleton),
    CONSTRAINT plugin_settings_markets_check CHECK ((json_type(markets) = 'array')),
    CONSTRAINT plugin_settings_singleton_check CHECK (singleton)
) STRICT;

CREATE TABLE plugin_state (
    plugin_id       TEXT NOT NULL,
    project_id      TEXT,
    key             TEXT NOT NULL,
    value           TEXT NOT NULL CHECK (json_valid(value)),
    updated_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT plugin_state_plugin_id_fkey FOREIGN KEY (plugin_id) REFERENCES plugins(id) ON DELETE CASCADE,
    CONSTRAINT plugin_state_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id),
    CONSTRAINT plugin_state_key_check CHECK (((length(key) >= 1) AND (length(key) <= 512)))
) STRICT;
-- PG 的 UNIQUE NULLS NOT DISTINCT：NULL 视为相同；ON CONFLICT 目标须写同样的表达式。
CREATE UNIQUE INDEX plugin_state_plugin_id_project_id_key_key ON plugin_state (plugin_id, coalesce(project_id, ''), key);

CREATE TABLE plugin_tokens (
    id              TEXT NOT NULL,
    plugin_id       TEXT NOT NULL,
    token_hash      BLOB NOT NULL,
    scopes          TEXT NOT NULL,
    created_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    revoked_at      INTEGER,
    CONSTRAINT plugin_tokens_pkey PRIMARY KEY (id),
    CONSTRAINT plugin_tokens_plugin_id_fkey FOREIGN KEY (plugin_id) REFERENCES plugins(id) ON DELETE CASCADE,
    CONSTRAINT plugin_tokens_scopes_check CHECK ((json_type(scopes) = 'array'))
) STRICT;
CREATE INDEX plugin_tokens_live ON plugin_tokens (plugin_id) WHERE (revoked_at IS NULL);

CREATE TABLE plugin_versions (
    plugin_id       TEXT NOT NULL,
    version         TEXT NOT NULL,
    manifest        TEXT NOT NULL,
    install_dir     TEXT NOT NULL,
    digest          TEXT NOT NULL,
    installed_at    INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT plugin_versions_pkey PRIMARY KEY (plugin_id, version),
    CONSTRAINT plugin_versions_plugin_id_fkey FOREIGN KEY (plugin_id) REFERENCES plugins(id) ON DELETE CASCADE,
    CONSTRAINT plugin_versions_manifest_check CHECK ((json_type(manifest) = 'object'))
) STRICT;

CREATE TABLE project_plugins (
    project_id      TEXT NOT NULL,
    plugin_id       TEXT NOT NULL,
    enabled         INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    CONSTRAINT project_plugins_pkey PRIMARY KEY (project_id, plugin_id),
    CONSTRAINT project_plugins_plugin_id_fkey FOREIGN KEY (plugin_id) REFERENCES plugins(id) ON DELETE CASCADE,
    CONSTRAINT project_plugins_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id)
) STRICT;

-- ===== 00032_identity_managers.sql =====

CREATE TABLE identity_managers (
    identity_id     TEXT NOT NULL,
    plugin_id       TEXT NOT NULL,
    channel_id      TEXT NOT NULL,
    updated_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT identity_managers_pkey PRIMARY KEY (identity_id),
    CONSTRAINT identity_managers_identity_id_fkey FOREIGN KEY (identity_id) REFERENCES external_identities(id),
    CONSTRAINT identity_managers_plugin_id_fkey FOREIGN KEY (plugin_id) REFERENCES plugins(id) ON DELETE CASCADE
) STRICT;
CREATE INDEX identity_managers_plugin ON identity_managers (plugin_id);

-- ===== 00034_model_settings.sql =====

CREATE TABLE model_settings (
    singleton       INTEGER NOT NULL DEFAULT 1 CHECK (singleton IN (0, 1)),
    models_json     TEXT NOT NULL DEFAULT '{"providers":{}}',
    revision        INTEGER NOT NULL DEFAULT 1,
    updated_at      INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000000 AS INTEGER)),
    CONSTRAINT model_settings_pkey PRIMARY KEY (singleton),
    CONSTRAINT model_settings_revision_check CHECK ((revision > 0)),
    CONSTRAINT model_settings_singleton_check CHECK (singleton)
) STRICT;

-- ===== SQLite 基线新增 =====

-- 原 identity 列（runs.queue_seq、task_turns.queue_seq、conversation_messages.seq）的取号表，在写事务里
-- UPDATE … RETURNING 取下一个号。
CREATE TABLE sequences (
    name   TEXT PRIMARY KEY,
    value  INTEGER NOT NULL CHECK (value >= 0)
) STRICT;
INSERT INTO sequences (name, value) VALUES ('conversation_messages.seq', 0), ('runs.queue_seq', 0), ('task_turns.queue_seq', 0);

-- 列级 ON DELETE SET NULL：删父行前只把指向它的那一列置空，其余外键列保持不变。
-- +goose StatementBegin
CREATE TRIGGER project_cli_versions_set_null_live BEFORE DELETE ON project_cli_versions
BEGIN
    UPDATE cli_credential_import_receipts SET live_version_id = NULL WHERE live_version_id = OLD.id AND account_id = OLD.account_id;
    UPDATE cli_credential_verifications SET live_version_id = NULL WHERE live_version_id = OLD.id AND account_id = OLD.account_id AND cli_name = OLD.cli_name AND auth_method = OLD.auth_method;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER project_credential_accounts_set_null_live BEFORE DELETE ON project_credential_accounts
BEGIN
    UPDATE cli_credential_import_receipts SET live_account_id = NULL WHERE live_account_id = OLD.id AND project_id = OLD.project_id AND purpose = OLD.purpose AND cli_name = OLD.slot_key;
END;
-- +goose StatementEnd

-- 飞书凭证绑定只追加：任何 UPDATE 与 DELETE（包括级联）都拒绝。
-- +goose StatementBegin
CREATE TRIGGER task_lark_binding_immutable BEFORE UPDATE ON task_lark_credential_bindings
BEGIN
    SELECT RAISE(ABORT, 'task_lark_binding_is_append_only');
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER task_lark_binding_immutable_delete BEFORE DELETE ON task_lark_credential_bindings
BEGIN
    SELECT RAISE(ABORT, 'task_lark_binding_is_append_only');
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER task_lark_member_immutable BEFORE UPDATE ON task_lark_binding_members
BEGIN
    SELECT RAISE(ABORT, 'task_lark_binding_is_append_only');
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER task_lark_member_immutable_delete BEFORE DELETE ON task_lark_binding_members
BEGIN
    SELECT RAISE(ABORT, 'task_lark_binding_is_append_only');
END;
-- +goose StatementEnd

-- ===== 种子数据 =====
-- PG 迁移里随建表写入的单例行（031 的插件设置、034 的模型配置）。
INSERT INTO plugin_settings DEFAULT VALUES;
INSERT INTO model_settings (singleton) VALUES (1);

-- ===== 插件事件捕获 =====
-- PostgreSQL 里由触发器直接写插件事件；这里的触发器只记下“发生了什么”，条件与原触发器相同。
-- 每个写事务提交前，store 的写钩子在同一事务里读出这些记录、生成插件事件（订阅与启用过滤、事件内容都在 Go 里），
-- 然后清空。lifecycle_events 在 events.db，触发器只能写同一个库，所以它的记录单独放在 ev.plugin_lifecycle_changes；
-- 两张表都记下 change_order()（db 包提供的进程内序号），读的时候按它还原原来触发器的先后。
CREATE TABLE plugin_changes (
    seq     INTEGER PRIMARY KEY,
    ord     INTEGER NOT NULL,
    kind    TEXT NOT NULL CHECK (kind IN ('agent.created', 'agent.updated', 'agent.deleted', 'sleep.requested', 'sleep.woken',
                                          'inbox.inserted', 'inbox.read', 'inbox.cancelled', 'question.asked', 'question.answered')),
    row_id  TEXT NOT NULL,
    data    TEXT NOT NULL DEFAULT '{}' CHECK (json_type(data) = 'object')
) STRICT;

CREATE TABLE ev.plugin_lifecycle_changes (
    seq     INTEGER PRIMARY KEY,
    ord     INTEGER NOT NULL,
    row_id  TEXT NOT NULL
) STRICT;

-- +goose StatementBegin
CREATE TRIGGER plugin_capture_agent_insert AFTER INSERT ON agents
BEGIN
    INSERT INTO plugin_changes (ord, kind, row_id, data) VALUES (change_order(), 'agent.created', NEW.id, json_object('name', NEW.name));
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER plugin_capture_agent_update AFTER UPDATE ON agents
WHEN (OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL)
    OR (NEW.deleted_at IS NULL AND (NEW.config_version <> OLD.config_version OR NEW.name <> OLD.name))
BEGIN
    INSERT INTO plugin_changes (ord, kind, row_id, data)
    VALUES (change_order(), CASE WHEN OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL THEN 'agent.deleted' ELSE 'agent.updated' END,
            NEW.id, json_object('name', NEW.name));
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER plugin_capture_sleep_insert AFTER INSERT ON agent_sleeps
BEGIN
    INSERT INTO plugin_changes (ord, kind, row_id, data) VALUES (change_order(), 'sleep.requested', NEW.id,
        json_object('at', NEW.requested_at, 'agentId', NEW.agent_id, 'taskId', NEW.task_id, 'runId', NEW.source_run_id, 'wakeAt', NEW.wake_at));
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER plugin_capture_sleep_woken AFTER UPDATE ON agent_sleeps
WHEN OLD.state <> 'woken' AND NEW.state = 'woken'
BEGIN
    INSERT INTO plugin_changes (ord, kind, row_id, data) VALUES (change_order(), 'sleep.woken', NEW.id,
        json_object('at', NEW.ended_at, 'agentId', NEW.agent_id, 'taskId', NEW.task_id, 'runId', NEW.wake_run_id, 'wakeAt', NEW.wake_at,
                    'endReason', NEW.end_reason));
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER plugin_capture_inbox_insert AFTER INSERT ON inbox_messages
BEGIN
    INSERT INTO plugin_changes (ord, kind, row_id) VALUES (change_order(), 'inbox.inserted', NEW.id);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER plugin_capture_inbox_update AFTER UPDATE ON inbox_messages
WHEN (OLD.read_at IS NULL AND NEW.read_at IS NOT NULL) OR (OLD.cancelled_at IS NULL AND NEW.cancelled_at IS NOT NULL)
BEGIN
    INSERT INTO plugin_changes (ord, kind, row_id, data)
    VALUES (change_order(), CASE WHEN OLD.read_at IS NULL AND NEW.read_at IS NOT NULL THEN 'inbox.read' ELSE 'inbox.cancelled' END, NEW.id,
            json_object('at', CASE WHEN OLD.read_at IS NULL AND NEW.read_at IS NOT NULL THEN NEW.read_at ELSE NEW.cancelled_at END));
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER plugin_capture_question_insert AFTER INSERT ON inbox_questions
BEGIN
    INSERT INTO plugin_changes (ord, kind, row_id) VALUES (change_order(), 'question.asked', NEW.message_id);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER plugin_capture_question_answered AFTER UPDATE ON inbox_questions
WHEN OLD.state <> 'answered' AND NEW.state = 'answered'
BEGIN
    INSERT INTO plugin_changes (ord, kind, row_id, data) VALUES (change_order(), 'question.answered', NEW.message_id,
        json_object('at', NEW.answered_at, 'optionId', NEW.answer_option_id));
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER ev.plugin_capture_lifecycle AFTER INSERT ON lifecycle_events
WHEN NEW.type IN ('run.queued', 'run.dispatched', 'run.running', 'run.ended', 'pi.started', 'pi.exited', 'pi.stopped',
                  'model.retry_started', 'context.compaction')
BEGIN
    INSERT INTO plugin_lifecycle_changes (ord, row_id) VALUES (change_order(), NEW.id);
END;
-- +goose StatementEnd
