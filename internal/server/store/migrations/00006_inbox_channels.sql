-- +goose Up
-- 独立工作树候选：006普通通道/统一问题。任务关闭语义尚待澄清，不增加closed状态。
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM external_identities WHERE name='platform' AND deleted_at IS NULL) THEN
        RAISE EXCEPTION '00006 cannot reserve platform: a live external identity uses that name';
    END IF;
END $$;
-- +goose StatementEnd
UPDATE external_identities SET name='platform-deleted-' || left(id::text,8)
    WHERE name='platform' AND deleted_at IS NOT NULL;
ALTER TABLE external_identities DROP CONSTRAINT external_identities_name_check;
ALTER TABLE external_identities ADD CONSTRAINT external_identities_name_check
    CHECK (name ~ '^[a-z0-9][a-z0-9._-]{0,63}$' AND name NOT IN ('console','platform'));

ALTER TABLE inbox_messages
    ADD COLUMN from_channel_id uuid REFERENCES plugin_instances(id),
    ADD COLUMN to_channel_id uuid REFERENCES plugin_instances(id),
    ADD COLUMN via jsonb,
    ADD COLUMN answers_message_id uuid REFERENCES inbox_messages(id),
    ADD COLUMN answer_option_id text,
    ADD COLUMN notice jsonb,
    DROP CONSTRAINT inbox_messages_from_kind_check,
    DROP CONSTRAINT inbox_messages_to_kind_check,
    DROP CONSTRAINT inbox_messages_from_ref,
    DROP CONSTRAINT inbox_messages_to_ref,
    DROP CONSTRAINT inbox_messages_when_busy;
ALTER TABLE inbox_messages
    ADD CONSTRAINT inbox_messages_from_kind_check CHECK (from_kind IN ('agent','console','external','channel','platform')),
    ADD CONSTRAINT inbox_messages_to_kind_check CHECK (to_kind IN ('agent','console','external','channel')),
    ADD CONSTRAINT inbox_messages_from_ref CHECK (
        (from_kind='agent' AND from_agent_id IS NOT NULL AND from_identity_id IS NULL AND from_channel_id IS NULL)
        OR (from_kind='external' AND from_identity_id IS NOT NULL AND from_agent_id IS NULL AND from_channel_id IS NULL)
        OR (from_kind='channel' AND from_channel_id IS NOT NULL AND from_agent_id IS NULL AND from_identity_id IS NULL)
        OR (from_kind IN ('console','platform') AND from_agent_id IS NULL AND from_identity_id IS NULL AND from_channel_id IS NULL)),
    ADD CONSTRAINT inbox_messages_to_ref CHECK (
        (to_kind='agent' AND to_agent_id IS NOT NULL AND to_identity_id IS NULL AND to_channel_id IS NULL)
        OR (to_kind='external' AND to_identity_id IS NOT NULL AND to_agent_id IS NULL AND to_channel_id IS NULL)
        OR (to_kind='channel' AND to_channel_id IS NOT NULL AND to_agent_id IS NULL AND to_identity_id IS NULL)
        OR (to_kind='console' AND to_agent_id IS NULL AND to_identity_id IS NULL AND to_channel_id IS NULL)),
    ADD CONSTRAINT inbox_messages_when_busy CHECK ((to_kind='agent')=(when_busy IS NOT NULL));
CREATE INDEX inbox_to_channel ON inbox_messages(to_channel_id,created_at DESC,id DESC) WHERE to_kind='channel';
CREATE INDEX inbox_from_channel ON inbox_messages(from_channel_id,created_at DESC,id DESC) WHERE from_kind='channel';

CREATE TABLE inbox_questions (
    message_id uuid PRIMARY KEY REFERENCES inbox_messages(id),
    options jsonb NOT NULL CHECK (jsonb_typeof(options)='array'),
    expires_at timestamptz NOT NULL,
    state text NOT NULL DEFAULT 'open' CHECK (state IN ('open','answered','expired')),
    expired_reason text,
    answer_message_id uuid REFERENCES inbox_messages(id),
    answer_option_id text,
    answered_by jsonb,
    answered_at timestamptz,
    notice_state text NOT NULL DEFAULT 'none' CHECK (notice_state IN ('none','pending','sent','skipped'))
);
CREATE INDEX inbox_questions_expiry ON inbox_questions(expires_at,message_id) WHERE state='open';
CREATE INDEX inbox_questions_pending_notice ON inbox_questions(message_id) WHERE notice_state='pending';

CREATE TABLE inbox_channel_threads (
    thread_id uuid PRIMARY KEY REFERENCES inbox_messages(id),
    channel_id uuid NOT NULL REFERENCES plugin_instances(id),
    conversation_key text NOT NULL,
    conversation_title text NOT NULL,
    conversation_data jsonb NOT NULL,
    current boolean NOT NULL DEFAULT true,
    task_id uuid,
    updated_at timestamptz NOT NULL DEFAULT now()
);
-- task_id按006规格仅预留；此候选不读取、不写任务路由，不提前引用007表。
CREATE UNIQUE INDEX inbox_channel_threads_current ON inbox_channel_threads(channel_id,conversation_key) WHERE current;

CREATE TABLE inbox_channel_deliveries (
    id uuid PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('message','question_closed')),
    message_id uuid NOT NULL REFERENCES inbox_messages(id),
    channel_id uuid NOT NULL REFERENCES plugin_instances(id),
    thread_id uuid NOT NULL REFERENCES inbox_channel_threads(thread_id),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','delivered','failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts>=0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error text,
    external_id text,
    notice_state text NOT NULL DEFAULT 'none' CHECK (notice_state IN ('none','pending','sent','skipped')),
    created_at timestamptz NOT NULL DEFAULT now(),
    delivered_at timestamptz,
    expires_at timestamptz NOT NULL
);
CREATE INDEX inbox_channel_deliveries_pending ON inbox_channel_deliveries(thread_id,created_at,id) WHERE status='pending';
CREATE INDEX inbox_channel_deliveries_notice ON inbox_channel_deliveries(id) WHERE notice_state='pending';
CREATE TABLE inbox_channel_dedupe (
    channel_id uuid NOT NULL REFERENCES plugin_instances(id),
    dedupe_key text NOT NULL,
    result jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(channel_id,dedupe_key)
);
CREATE TABLE inbox_attachments (
    id uuid PRIMARY KEY,
    message_id uuid NOT NULL REFERENCES inbox_messages(id),
    ordinal integer NOT NULL CHECK (ordinal>=0),
    name text NOT NULL,
    mime_type text NOT NULL,
    size bigint NOT NULL CHECK (size>=0),
    sha256 text NOT NULL REFERENCES blobs(sha256),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(message_id,ordinal)
);

-- +goose Down
-- 仅验证扩展数据为空时的结构Down；普通参与方的新问题/附件也会被DROP。
-- 有扩展数据时的回退策略未确认，不据此执行有数据的回退。
-- channel/platform消息不能满足004约束，该情形会事务失败。
DROP TABLE inbox_attachments;
DROP TABLE inbox_channel_dedupe;
DROP TABLE inbox_channel_deliveries;
DROP TABLE inbox_channel_threads;
DROP TABLE inbox_questions;
DROP INDEX inbox_from_channel;
DROP INDEX inbox_to_channel;
ALTER TABLE inbox_messages
    DROP CONSTRAINT inbox_messages_from_kind_check,
    DROP CONSTRAINT inbox_messages_to_kind_check,
    DROP CONSTRAINT inbox_messages_from_ref,
    DROP CONSTRAINT inbox_messages_to_ref,
    DROP CONSTRAINT inbox_messages_when_busy,
    DROP COLUMN notice,
    DROP COLUMN answer_option_id,
    DROP COLUMN answers_message_id,
    DROP COLUMN via,
    DROP COLUMN to_channel_id,
    DROP COLUMN from_channel_id;
ALTER TABLE inbox_messages
    ADD CONSTRAINT inbox_messages_from_kind_check CHECK (from_kind IN ('agent','console','external')),
    ADD CONSTRAINT inbox_messages_to_kind_check CHECK (to_kind IN ('agent','console','external')),
    ADD CONSTRAINT inbox_messages_from_ref CHECK (
        (from_kind='agent' AND from_agent_id IS NOT NULL AND from_identity_id IS NULL)
        OR (from_kind='external' AND from_identity_id IS NOT NULL AND from_agent_id IS NULL)
        OR (from_kind='console' AND from_agent_id IS NULL AND from_identity_id IS NULL)),
    ADD CONSTRAINT inbox_messages_to_ref CHECK (
        (to_kind='agent' AND to_agent_id IS NOT NULL AND to_identity_id IS NULL)
        OR (to_kind='external' AND to_identity_id IS NOT NULL AND to_agent_id IS NULL)
        OR (to_kind='console' AND to_agent_id IS NULL AND to_identity_id IS NULL)),
    ADD CONSTRAINT inbox_messages_when_busy CHECK ((to_kind='agent')=(when_busy IS NOT NULL));
ALTER TABLE external_identities DROP CONSTRAINT external_identities_name_check;
ALTER TABLE external_identities ADD CONSTRAINT external_identities_name_check
    CHECK (name ~ '^[a-z0-9][a-z0-9._-]{0,63}$' AND name<>'console');
-- 已删除platform身份的规范化名称保留；不恢复保留名，不改变历史ID。
