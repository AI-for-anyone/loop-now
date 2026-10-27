-- +goose Up
-- Inbox 与对外访问（变更 add-inbox-and-external-access）：外部身份与令牌、Agent 令牌、Inbox 消息、审计，
-- 以及执行的来源与触发提醒的消息。

-- 外部身份：软删除后名称可以再用；名称规则与契约 ExternalIdentityName 相同。
CREATE TABLE external_identities (
    id          uuid PRIMARY KEY,
    name        text NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9._-]{0,63}$' AND name <> 'console'),
    created_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz
);
CREATE UNIQUE INDEX external_identities_name_live ON external_identities (name) WHERE deleted_at IS NULL;

-- 外部令牌：只存密钥的 sha256；显示前缀为 lne_ 加 ID 去掉连字符后的前 8 位。
CREATE TABLE external_tokens (
    id              uuid PRIMARY KEY,
    identity_id     uuid NOT NULL REFERENCES external_identities (id),
    secret_hash     bytea NOT NULL CHECK (octet_length(secret_hash) = 32),
    display_prefix  text NOT NULL CHECK (display_prefix ~ '^lne_[0-9a-f]{8}$'),
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz,
    revoked_at      timestamptz,
    last_used_at    timestamptz
);
CREATE INDEX external_tokens_by_identity ON external_tokens (identity_id, created_at DESC);

-- Agent 令牌：每次启动 Pi 签发一个，进程停止或服务端重启后作废。
CREATE TABLE agent_tokens (
    id           uuid PRIMARY KEY,
    agent_id     uuid NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    instance_id  uuid NOT NULL,
    secret_hash  bytea NOT NULL CHECK (octet_length(secret_hash) = 32),
    issued_at    timestamptz NOT NULL DEFAULT now(),
    revoked_at   timestamptz
);
CREATE INDEX agent_tokens_live_by_agent ON agent_tokens (agent_id) WHERE revoked_at IS NULL;
CREATE INDEX agent_tokens_live_by_instance ON agent_tokens (instance_id) WHERE revoked_at IS NULL;

-- Inbox 消息：一条消息一个发件人、一个收件人，各是 Agent、控制台操作者或外部身份之一。
-- 新线索的第一条消息：thread_id = id、in_reply_to 为空；回复沿用线索 ID 并记下回复的消息。
-- whenBusy 只对发给 Agent 的消息有意义，发给人时为空。消息一直保留。
CREATE TABLE inbox_messages (
    id                uuid PRIMARY KEY,
    thread_id         uuid NOT NULL REFERENCES inbox_messages (id),
    in_reply_to       uuid REFERENCES inbox_messages (id),
    from_kind         text NOT NULL CHECK (from_kind IN ('agent', 'console', 'external')),
    from_agent_id     uuid REFERENCES agents (id),
    from_identity_id  uuid REFERENCES external_identities (id),
    to_kind           text NOT NULL CHECK (to_kind IN ('agent', 'console', 'external')),
    to_agent_id       uuid REFERENCES agents (id),
    to_identity_id    uuid REFERENCES external_identities (id),
    text              text NOT NULL CHECK (text <> ''),
    when_busy         text CHECK (when_busy IN ('interrupt', 'queue')),
    created_at        timestamptz NOT NULL DEFAULT now(),
    read_at           timestamptz,
    CONSTRAINT inbox_messages_from_ref CHECK (
        (from_kind = 'agent' AND from_agent_id IS NOT NULL AND from_identity_id IS NULL)
        OR (from_kind = 'external' AND from_identity_id IS NOT NULL AND from_agent_id IS NULL)
        OR (from_kind = 'console' AND from_agent_id IS NULL AND from_identity_id IS NULL)),
    CONSTRAINT inbox_messages_to_ref CHECK (
        (to_kind = 'agent' AND to_agent_id IS NOT NULL AND to_identity_id IS NULL)
        OR (to_kind = 'external' AND to_identity_id IS NOT NULL AND to_agent_id IS NULL)
        OR (to_kind = 'console' AND to_agent_id IS NULL AND to_identity_id IS NULL)),
    CONSTRAINT inbox_messages_when_busy CHECK ((to_kind = 'agent') = (when_busy IS NOT NULL)),
    CONSTRAINT inbox_messages_thread CHECK ((in_reply_to IS NULL) = (thread_id = id))
);
-- 按收件人分页（新的在前）与统计未读。
CREATE INDEX inbox_to_agent ON inbox_messages (to_agent_id, created_at DESC, id DESC) WHERE to_kind = 'agent';
CREATE INDEX inbox_to_identity ON inbox_messages (to_identity_id, created_at DESC, id DESC) WHERE to_kind = 'external';
CREATE INDEX inbox_to_console ON inbox_messages (created_at DESC, id DESC) WHERE to_kind = 'console';
CREATE INDEX inbox_unread_agent ON inbox_messages (to_agent_id) WHERE to_kind = 'agent' AND read_at IS NULL;
-- 按发件人查找（外部身份的收件箱、线索列表）。
CREATE INDEX inbox_from_agent ON inbox_messages (from_agent_id, created_at DESC, id DESC) WHERE from_kind = 'agent';
CREATE INDEX inbox_from_identity ON inbox_messages (from_identity_id, created_at DESC, id DESC) WHERE from_kind = 'external';
CREATE INDEX inbox_from_console ON inbox_messages (created_at DESC, id DESC) WHERE from_kind = 'console';
CREATE INDEX inbox_by_thread ON inbox_messages (thread_id, created_at, id);

-- 审计：所有改变状态的请求，不含请求体、凭证与令牌。服务端命令行的操作者为 cli，没有请求 ID。
CREATE TABLE audit_log (
    id             uuid PRIMARY KEY,
    at             timestamptz NOT NULL DEFAULT now(),
    request_id     uuid,
    actor_kind     text NOT NULL CHECK (actor_kind IN ('console', 'external', 'agent', 'cli')),
    actor_id       uuid,
    actor_address  text,
    method         text NOT NULL,
    operation      text NOT NULL,
    target         jsonb NOT NULL DEFAULT '{}',
    status         integer NOT NULL
);
CREATE INDEX audit_log_by_time ON audit_log (at DESC, id DESC);

-- 执行的来源；平台提醒那一轮记下创建它的 Inbox 消息。
ALTER TABLE runs
    ADD COLUMN origin text NOT NULL DEFAULT 'console' CHECK (origin IN ('console', 'inbox_reminder')),
    ADD COLUMN trigger_inbox_message_id uuid REFERENCES inbox_messages (id),
    ADD CONSTRAINT runs_trigger_matches_origin CHECK ((origin = 'inbox_reminder') = (trigger_inbox_message_id IS NOT NULL));
-- 提醒合并：同一 Agent 在每个优先级上最多一条排队中的提醒。
CREATE UNIQUE INDEX runs_one_queued_reminder ON runs (agent_id, priority) WHERE status = 'queued' AND origin = 'inbox_reminder';

-- +goose Down
DROP INDEX runs_one_queued_reminder;
ALTER TABLE runs
    DROP CONSTRAINT runs_trigger_matches_origin,
    DROP COLUMN trigger_inbox_message_id,
    DROP COLUMN origin;
DROP TABLE audit_log;
DROP TABLE inbox_messages;
DROP TABLE agent_tokens;
DROP TABLE external_tokens;
DROP TABLE external_identities;
