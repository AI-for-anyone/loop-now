-- +goose Up
-- A tool still open when its turn ends is closed as no_result: the runtime never said how it ended (Antigravity's
-- background commands whose end never came, or any tool an agent leaves open). SQLite cannot change a CHECK
-- constraint, so run_tool_events is rebuilt in events.db (https://sqlite.org/lang_altertable.html#otheralter). No
-- foreign key refers to it or leaves it and it has no index besides its primary key, so foreign keys stay on and goose's
-- transaction holds the whole rebuild: when any step fails, the old table is left exactly as it was.
CREATE TABLE ev.run_tool_events_new (
    run_id          TEXT NOT NULL,
    seq             INTEGER NOT NULL,
    tool_call_id    TEXT NOT NULL,
    tool_name       TEXT NOT NULL,
    state           TEXT NOT NULL,
    at              INTEGER NOT NULL DEFAULT (now()),
    CONSTRAINT run_tool_events_pkey PRIMARY KEY (run_id, seq),
    CONSTRAINT run_tool_events_state_check CHECK ((state IN ('started', 'succeeded', 'failed', 'no_result')))
) STRICT;
INSERT INTO ev.run_tool_events_new (run_id, seq, tool_call_id, tool_name, state, at)
    SELECT run_id, seq, tool_call_id, tool_name, state, at FROM ev.run_tool_events;
DROP TABLE ev.run_tool_events;
ALTER TABLE ev.run_tool_events_new RENAME TO run_tool_events;

-- +goose Down
-- The older schema knows three states: a tool closed without a result reads as failed.
CREATE TABLE ev.run_tool_events_old (
    run_id          TEXT NOT NULL,
    seq             INTEGER NOT NULL,
    tool_call_id    TEXT NOT NULL,
    tool_name       TEXT NOT NULL,
    state           TEXT NOT NULL,
    at              INTEGER NOT NULL DEFAULT (now()),
    CONSTRAINT run_tool_events_pkey PRIMARY KEY (run_id, seq),
    CONSTRAINT run_tool_events_state_check CHECK ((state IN ('started', 'succeeded', 'failed')))
) STRICT;
INSERT INTO ev.run_tool_events_old (run_id, seq, tool_call_id, tool_name, state, at)
    SELECT run_id, seq, tool_call_id, tool_name, CASE state WHEN 'no_result' THEN 'failed' ELSE state END, at FROM ev.run_tool_events;
DROP TABLE ev.run_tool_events;
ALTER TABLE ev.run_tool_events_old RENAME TO run_tool_events;
