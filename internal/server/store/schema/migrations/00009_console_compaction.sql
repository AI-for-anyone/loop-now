-- 手动压缩（/compact）的运行没有用户消息：user_message_id 可为空，origin 增加 console_compact，二者绑定。
-- SQLite 不能改列约束，按 https://sqlite.org/lang_altertable.html#otheralter 重建 runs；
-- 重建期间关闭外键，避免 DROP TABLE 级联删除对话消息、用量与 Inbox 引用。

-- +goose NO TRANSACTION

-- +goose Up
PRAGMA foreign_keys = OFF;
BEGIN IMMEDIATE;
CREATE TABLE runs_new (
    id                        TEXT NOT NULL,
    agent_id                  TEXT NOT NULL,
    user_message_id           TEXT,
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
    queued_at                 INTEGER NOT NULL DEFAULT (now()),
    started_at                INTEGER,
    ended_at                  INTEGER,
    running_at                INTEGER,
    abort_requested_at        INTEGER,
    origin                    TEXT NOT NULL DEFAULT 'console',
    trigger_inbox_message_id  TEXT,
    wake_sleep_id             TEXT,
    stop_requested            INTEGER NOT NULL DEFAULT 0 CONSTRAINT runs_stop_requested_check CHECK (stop_requested IN (0, 1)),
    pi_command                TEXT,
    pi_command_handled        INTEGER NOT NULL DEFAULT 0 CHECK (pi_command_handled IN (0, 1)),
    pi_command_notices        TEXT CHECK (pi_command_notices IS NULL OR json_type(pi_command_notices) = 'object'),
    CONSTRAINT runs_pkey PRIMARY KEY (id),
    CONSTRAINT runs_user_message_id_key UNIQUE (user_message_id),
    CONSTRAINT runs_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    CONSTRAINT runs_trigger_inbox_message_id_fkey FOREIGN KEY (trigger_inbox_message_id) REFERENCES inbox_messages(id),
    CONSTRAINT runs_wake_sleep_id_fkey FOREIGN KEY (wake_sleep_id) REFERENCES agent_sleeps(id),
    CONSTRAINT runs_end_reason_check CHECK ((end_reason IN ('user_interrupt', 'server_restart', 'sleep_superseded', 'sleep_invalidated', 'user_stopped', 'user_cancelled', 'merged_into_interrupt', 'no_pending_messages'))),
    CONSTRAINT runs_origin_check CHECK ((origin IN ('console', 'inbox_reminder', 'sleep_wake', 'console_compact'))),
    CONSTRAINT runs_console_compact_message CHECK (((origin = 'console_compact') = (user_message_id IS NULL))),
    CONSTRAINT runs_phase_check CHECK ((phase IN ('waiting_slot', 'preparing', 'launching'))),
    CONSTRAINT runs_priority_check CHECK ((priority IN (0, 1))),
    CONSTRAINT runs_sleep_wake_reference CHECK (((origin <> 'sleep_wake') OR (wake_sleep_id IS NOT NULL))),
    CONSTRAINT runs_status_check CHECK ((status IN ('queued', 'starting', 'running', 'aborting', 'completed', 'failed', 'interrupted'))),
    CONSTRAINT runs_trigger_matches_origin CHECK (((origin = 'inbox_reminder') = (trigger_inbox_message_id IS NOT NULL))),
    CONSTRAINT runs_when_busy_check CHECK ((when_busy IN ('interrupt', 'queue')))
) STRICT;
INSERT INTO runs_new SELECT * FROM runs;
DROP TABLE runs;
ALTER TABLE runs_new RENAME TO runs;
CREATE INDEX runs_anomalies ON runs (ended_at DESC, id DESC) WHERE (status IN ('failed', 'interrupted'));
CREATE UNIQUE INDEX runs_one_active_per_agent ON runs (agent_id) WHERE (status IN ('starting', 'running', 'aborting'));
CREATE UNIQUE INDEX runs_one_queued_reminder ON runs (agent_id, priority) WHERE ((status = 'queued') AND (origin = 'inbox_reminder'));
CREATE UNIQUE INDEX runs_one_sleep_carrier ON runs (wake_sleep_id) WHERE ((wake_sleep_id IS NOT NULL) AND (status IN ('queued', 'starting', 'running', 'aborting')));
CREATE INDEX runs_queued_by_agent ON runs (agent_id, priority, queue_seq) WHERE (status = 'queued');
COMMIT;
PRAGMA foreign_keys = ON;

-- +goose Down
DELETE FROM runs WHERE origin = 'console_compact';
PRAGMA foreign_keys = OFF;
BEGIN IMMEDIATE;
CREATE TABLE runs_old (
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
    queued_at                 INTEGER NOT NULL DEFAULT (now()),
    started_at                INTEGER,
    ended_at                  INTEGER,
    running_at                INTEGER,
    abort_requested_at        INTEGER,
    origin                    TEXT NOT NULL DEFAULT 'console',
    trigger_inbox_message_id  TEXT,
    wake_sleep_id             TEXT,
    stop_requested            INTEGER NOT NULL DEFAULT 0 CONSTRAINT runs_stop_requested_check CHECK (stop_requested IN (0, 1)),
    pi_command                TEXT,
    pi_command_handled        INTEGER NOT NULL DEFAULT 0 CHECK (pi_command_handled IN (0, 1)),
    pi_command_notices        TEXT CHECK (pi_command_notices IS NULL OR json_type(pi_command_notices) = 'object'),
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
INSERT INTO runs_old SELECT * FROM runs;
DROP TABLE runs;
ALTER TABLE runs_old RENAME TO runs;
CREATE INDEX runs_anomalies ON runs (ended_at DESC, id DESC) WHERE (status IN ('failed', 'interrupted'));
CREATE UNIQUE INDEX runs_one_active_per_agent ON runs (agent_id) WHERE (status IN ('starting', 'running', 'aborting'));
CREATE UNIQUE INDEX runs_one_queued_reminder ON runs (agent_id, priority) WHERE ((status = 'queued') AND (origin = 'inbox_reminder'));
CREATE UNIQUE INDEX runs_one_sleep_carrier ON runs (wake_sleep_id) WHERE ((wake_sleep_id IS NOT NULL) AND (status IN ('queued', 'starting', 'running', 'aborting')));
CREATE INDEX runs_queued_by_agent ON runs (agent_id, priority, queue_seq) WHERE (status = 'queued');
COMMIT;
PRAGMA foreign_keys = ON;
