-- Background tasks: work a runtime's CLI started during a reply that outlives the reply (a command, a sub-agent). Each
-- belongs to the run (or task turn) that started it and goes with it. agent_id is the executor's Agent: the regular Agent,
-- or the task instance the task ran on. native_id is the runtime's own id of the task; follow_up_run_id is the reply the
-- CLI gave by itself after the task ended.
-- A run can now come from such a follow-up (origin background) and end as merged_into_reply. SQLite cannot change a
-- column's constraints, so runs is rebuilt (https://sqlite.org/lang_altertable.html#otheralter) with foreign keys off,
-- so that DROP TABLE does not cascade to messages, usage and Inbox references.

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
    compaction                TEXT CHECK (compaction IS NULL OR (origin = 'console_compact' AND json_type(compaction) = 'object')),
    selection                 TEXT CHECK (selection IS NULL OR json_type(selection) = 'object'),
    context_handoff           TEXT CHECK (context_handoff IS NULL OR json_type(context_handoff) = 'object'),
    CONSTRAINT runs_pkey PRIMARY KEY (id),
    CONSTRAINT runs_user_message_id_key UNIQUE (user_message_id),
    CONSTRAINT runs_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    CONSTRAINT runs_trigger_inbox_message_id_fkey FOREIGN KEY (trigger_inbox_message_id) REFERENCES inbox_messages(id),
    CONSTRAINT runs_wake_sleep_id_fkey FOREIGN KEY (wake_sleep_id) REFERENCES agent_sleeps(id),
    CONSTRAINT runs_end_reason_check CHECK ((end_reason IN ('user_interrupt', 'server_restart', 'sleep_superseded', 'sleep_invalidated', 'user_stopped', 'user_cancelled', 'merged_into_interrupt', 'no_pending_messages', 'merged_into_reply'))),
    CONSTRAINT runs_origin_check CHECK ((origin IN ('console', 'inbox_reminder', 'sleep_wake', 'console_compact', 'background'))),
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

CREATE TABLE background_tasks (
    id                 TEXT NOT NULL,
    agent_id           TEXT NOT NULL,
    run_id             TEXT,
    task_turn_id       TEXT,
    native_id          TEXT NOT NULL,
    runtime            TEXT NOT NULL,
    kind               TEXT NOT NULL CHECK (kind IN ('command', 'subagent', 'other')),
    label              TEXT NOT NULL,
    tool_call_id       TEXT,
    started_at         INTEGER NOT NULL,
    status             TEXT NOT NULL CHECK (status IN ('running', 'completed', 'failed', 'stopped', 'unknown')),
    ended_at           INTEGER,
    exit_code          INTEGER,
    stop_reason        TEXT CHECK (stop_reason IN ('person', 'reply_stopped', 'settings_changed', 'runtime_switched', 'session_reset', 'agent_deleted', 'task_started', 'shutdown', 'task_limit')),
    stop_requested_at  INTEGER,
    output             TEXT,
    follow_up_run_id   TEXT,
    CONSTRAINT background_tasks_pkey PRIMARY KEY (id),
    CONSTRAINT background_tasks_run_id_fkey FOREIGN KEY (run_id) REFERENCES runs(id) ON DELETE CASCADE,
    CONSTRAINT background_tasks_task_turn_id_fkey FOREIGN KEY (task_turn_id) REFERENCES task_turns(id) ON DELETE CASCADE,
    CONSTRAINT background_tasks_follow_up_run_id_fkey FOREIGN KEY (follow_up_run_id) REFERENCES runs(id) ON DELETE SET NULL,
    CONSTRAINT background_tasks_one_owner CHECK ((run_id IS NULL) <> (task_turn_id IS NULL)),
    CONSTRAINT background_tasks_end_check CHECK ((status = 'running') = (ended_at IS NULL))
) STRICT;
CREATE INDEX background_tasks_running ON background_tasks (agent_id, started_at) WHERE (status = 'running');
CREATE INDEX background_tasks_by_run ON background_tasks (run_id, started_at) WHERE (run_id IS NOT NULL);
CREATE INDEX background_tasks_by_task_turn ON background_tasks (task_turn_id, started_at) WHERE (task_turn_id IS NOT NULL);
CREATE INDEX background_tasks_by_follow_up ON background_tasks (follow_up_run_id) WHERE (follow_up_run_id IS NOT NULL);
COMMIT;
PRAGMA foreign_keys = ON;

-- +goose Down
DELETE FROM runs WHERE origin = 'background';
PRAGMA foreign_keys = OFF;
BEGIN IMMEDIATE;
DROP TABLE background_tasks;
UPDATE runs SET end_reason = NULL WHERE end_reason = 'merged_into_reply';
CREATE TABLE runs_old (
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
    compaction                TEXT CHECK (compaction IS NULL OR (origin = 'console_compact' AND json_type(compaction) = 'object')),
    selection                 TEXT CHECK (selection IS NULL OR json_type(selection) = 'object'),
    context_handoff           TEXT CHECK (context_handoff IS NULL OR json_type(context_handoff) = 'object'),
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
