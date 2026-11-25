-- +goose Up
-- Permission modes: the mode new Agents get (one row; full at first, so nothing changes until someone chooses), and
-- what a runtime asked to do in another mode, waiting for a person. A request belongs to a run of a regular Agent or to
-- a task turn of a task instance (agent_id is then the Agent the instance runs, task_id and node_run_id its task and
-- node). question_message_id is the Inbox question that asks the console about it; answering either answers both.
CREATE TABLE permission_settings (
    singleton     INTEGER NOT NULL DEFAULT 1 CHECK (singleton IN (0, 1)),
    default_mode  TEXT NOT NULL DEFAULT 'full' CHECK (default_mode IN ('supervised', 'accept_edits', 'auto', 'full')),
    updated_at    INTEGER NOT NULL DEFAULT (now()),
    CONSTRAINT permission_settings_pkey PRIMARY KEY (singleton),
    CONSTRAINT permission_settings_singleton_check CHECK (singleton)
) STRICT;

INSERT INTO permission_settings DEFAULT VALUES;

CREATE TABLE permission_requests (
    id                   TEXT NOT NULL,
    agent_id             TEXT NOT NULL,
    run_id               TEXT,
    task_turn_id         TEXT,
    task_id              TEXT,
    node_run_id          TEXT,
    runtime              TEXT NOT NULL,
    kind                 TEXT NOT NULL CHECK (kind IN ('command', 'file_change', 'tool', 'network', 'other')),
    title                TEXT NOT NULL,
    detail               TEXT,
    created_at           INTEGER NOT NULL DEFAULT (now()),
    state                TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'approved', 'rejected', 'cancelled')),
    answered_at          INTEGER,
    reason               TEXT,
    question_message_id  TEXT,
    CONSTRAINT permission_requests_pkey PRIMARY KEY (id),
    CONSTRAINT permission_requests_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE,
    CONSTRAINT permission_requests_run_id_fkey FOREIGN KEY (run_id) REFERENCES runs(id) ON DELETE CASCADE,
    CONSTRAINT permission_requests_task_turn_id_fkey FOREIGN KEY (task_turn_id) REFERENCES task_turns(id) ON DELETE CASCADE,
    CONSTRAINT permission_requests_question_fkey FOREIGN KEY (question_message_id) REFERENCES inbox_messages(id),
    CONSTRAINT permission_requests_one_owner CHECK ((run_id IS NULL) <> (task_turn_id IS NULL)),
    CONSTRAINT permission_requests_task_check CHECK ((task_turn_id IS NULL) = (task_id IS NULL)),
    CONSTRAINT permission_requests_answered_check CHECK ((state = 'pending') = (answered_at IS NULL))
) STRICT;
CREATE INDEX permission_requests_pending ON permission_requests (agent_id, created_at) WHERE (state = 'pending');
CREATE INDEX permission_requests_by_run ON permission_requests (run_id, created_at) WHERE (run_id IS NOT NULL);
CREATE INDEX permission_requests_by_turn ON permission_requests (task_turn_id, created_at) WHERE (task_turn_id IS NOT NULL);
CREATE INDEX permission_requests_by_node ON permission_requests (node_run_id, created_at) WHERE (node_run_id IS NOT NULL AND state = 'pending');
CREATE UNIQUE INDEX permission_requests_question ON permission_requests (question_message_id) WHERE (question_message_id IS NOT NULL);

-- +goose Down
DROP TABLE permission_requests;
DROP TABLE permission_settings;
