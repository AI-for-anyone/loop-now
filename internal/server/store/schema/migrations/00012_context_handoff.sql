-- +goose Up
-- A change of runtime no longer starts the conversation over: the new runtime starts its own session and gets a
-- selection of the saved conversation with its first message (Run.contextHandoff in the API).
-- agent_sessions.handoff is the change whose conversation has not reached the new runtime yet:
-- {"id", "from": runtime that last ran the conversation, "sessionFile": that runtime's session, restored when the
-- change is undone before the new runtime ran anything, "dispatched": a message was dispatched to the new runtime}.
-- NULL when nothing is pending.
ALTER TABLE agent_sessions ADD COLUMN handoff TEXT CHECK (handoff IS NULL OR json_type(handoff) = 'object');
-- runs.selection is who ran the turn ({"runtime", "provider", "modelId", "thinkingLevel"}), recorded when the reply
-- began; runs.context_handoff is the selection of the conversation the turn carried to a new runtime. Both are NULL
-- for older runs.
ALTER TABLE runs ADD COLUMN selection TEXT CHECK (selection IS NULL OR json_type(selection) = 'object');
ALTER TABLE runs ADD COLUMN context_handoff TEXT CHECK (context_handoff IS NULL OR json_type(context_handoff) = 'object');

-- +goose Down
ALTER TABLE runs DROP COLUMN context_handoff;
ALTER TABLE runs DROP COLUMN selection;
ALTER TABLE agent_sessions DROP COLUMN handoff;
