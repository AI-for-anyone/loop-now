-- Importing conversations (Claude Code and Codex conversations on this computer, continued by Agents) and permission
-- modes.
-- agents: working_directory is the folder the Agent works in (NULL: its own workspace, as before); use_local_config loads
-- the person's own CLI configuration; permission_mode is how much the Agent may do without asking ('full', the default,
-- is how every Agent ran before). imported_* say which conversation the Agent was imported from; imported_capture is the
-- copy taken at import (Claude Code's session file, or Codex's thread and last turn) that its first turn continues
-- from. A conversation has at most one live Agent.
-- runs: an imported turn is a completed run of origin imported (its prompt and the joined reply as its messages). SQLite
-- cannot change a column's constraints, so runs is rebuilt (https://sqlite.org/lang_altertable.html#otheralter) with
-- foreign keys off, as in migration 16.

-- +goose NO TRANSACTION

-- +goose Up
PRAGMA foreign_keys = OFF;
BEGIN IMMEDIATE;
ALTER TABLE agents ADD COLUMN working_directory TEXT CHECK (working_directory IS NULL OR substr(working_directory, 1, 1) = '/');
ALTER TABLE agents ADD COLUMN use_local_config INTEGER NOT NULL DEFAULT 0 CHECK (use_local_config IN (0, 1));
ALTER TABLE agents ADD COLUMN permission_mode TEXT NOT NULL DEFAULT 'full' CHECK (permission_mode IN ('supervised', 'accept_edits', 'auto', 'full'));
ALTER TABLE agents ADD COLUMN imported_cli TEXT CHECK (imported_cli IN ('claude', 'codex'));
ALTER TABLE agents ADD COLUMN imported_session_id TEXT;
ALTER TABLE agents ADD COLUMN imported_path TEXT;
ALTER TABLE agents ADD COLUMN imported_capture TEXT;
ALTER TABLE agents ADD COLUMN imported_at INTEGER;
CREATE UNIQUE INDEX agents_imported_once ON agents (imported_cli, imported_session_id) WHERE ((imported_session_id IS NOT NULL) AND (deleted_at IS NULL));
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
    CONSTRAINT runs_origin_check CHECK ((origin IN ('console', 'inbox_reminder', 'sleep_wake', 'console_compact', 'background', 'imported'))),
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
DELETE FROM runs WHERE origin = 'imported';
PRAGMA foreign_keys = OFF;
BEGIN IMMEDIATE;
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
INSERT INTO runs_old SELECT * FROM runs;
DROP TABLE runs;
ALTER TABLE runs_old RENAME TO runs;
CREATE INDEX runs_anomalies ON runs (ended_at DESC, id DESC) WHERE (status IN ('failed', 'interrupted'));
CREATE UNIQUE INDEX runs_one_active_per_agent ON runs (agent_id) WHERE (status IN ('starting', 'running', 'aborting'));
CREATE UNIQUE INDEX runs_one_queued_reminder ON runs (agent_id, priority) WHERE ((status = 'queued') AND (origin = 'inbox_reminder'));
CREATE UNIQUE INDEX runs_one_sleep_carrier ON runs (wake_sleep_id) WHERE ((wake_sleep_id IS NOT NULL) AND (status IN ('queued', 'starting', 'running', 'aborting')));
CREATE INDEX runs_queued_by_agent ON runs (agent_id, priority, queue_seq) WHERE (status = 'queued');
DROP INDEX agents_imported_once;
ALTER TABLE agents DROP COLUMN imported_at;
ALTER TABLE agents DROP COLUMN imported_capture;
ALTER TABLE agents DROP COLUMN imported_path;
ALTER TABLE agents DROP COLUMN imported_session_id;
ALTER TABLE agents DROP COLUMN imported_cli;
ALTER TABLE agents DROP COLUMN permission_mode;
ALTER TABLE agents DROP COLUMN use_local_config;
ALTER TABLE agents DROP COLUMN working_directory;
COMMIT;
PRAGMA foreign_keys = ON;
