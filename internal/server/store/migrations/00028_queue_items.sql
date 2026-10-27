-- +goose Up
-- Queued Inbox messages are tracked one by one: each pending message points at
-- the queued reminder run (normal agents) or task turn (task instances, non-strict
-- messages) that will deliver it. Strict node messages keep using
-- task_node_inbox_messages.reminder_run_id. A human can cancel a queued message;
-- a cancelled message is never read and no longer counts as unread.
ALTER TABLE inbox_messages
 ADD COLUMN cancelled_at timestamptz,
 ADD COLUMN reminder_run_id uuid REFERENCES runs(id) ON DELETE SET NULL,
 ADD COLUMN reminder_turn_id uuid REFERENCES task_turns(id) ON DELETE SET NULL,
 ADD CONSTRAINT inbox_messages_cancelled_unread CHECK(cancelled_at IS NULL OR read_at IS NULL),
 ADD CONSTRAINT inbox_messages_one_reminder CHECK(reminder_run_id IS NULL OR reminder_turn_id IS NULL);
CREATE INDEX inbox_messages_reminder_run ON inbox_messages(reminder_run_id) WHERE reminder_run_id IS NOT NULL;
CREATE INDEX inbox_messages_reminder_turn ON inbox_messages(reminder_turn_id) WHERE reminder_turn_id IS NOT NULL;

ALTER TABLE runs DROP CONSTRAINT runs_end_reason_check;
ALTER TABLE runs ADD CONSTRAINT runs_end_reason_check CHECK(end_reason IN ('user_interrupt','server_restart','sleep_superseded','sleep_invalidated',
 'user_stopped','user_cancelled','merged_into_interrupt','no_pending_messages'));
ALTER TABLE task_turns DROP CONSTRAINT task_turns_void_reason_check;
ALTER TABLE task_turns ADD CONSTRAINT task_turns_void_reason_check CHECK(void_reason IN ('merged_into_interrupt','user_cancelled','no_pending_messages'));
ALTER TABLE inbox_questions DROP CONSTRAINT inbox_questions_state_check;
ALTER TABLE inbox_questions ADD CONSTRAINT inbox_questions_state_check CHECK(state IN ('open','answered','expired','closed','cancelled'));
-- A task reminder turn that also wakes the node after its own question was
-- resolved must run even when it carries no pending message.
ALTER TABLE task_turns ADD COLUMN question_wakeup boolean NOT NULL DEFAULT false;
UPDATE task_turns t SET question_wakeup=true
 WHERE t.status='queued' AND t.origin='inbox_reminder' AND t.inbox_scope_node_id IS NULL
 AND EXISTS(SELECT 1 FROM inbox_messages q WHERE q.id=t.trigger_inbox_message_id AND q.from_runtime_id=t.runtime_id);

-- Idempotency of console queue actions on normal agents (same contract as task_actions).
CREATE TABLE agent_actions (
 actor_key text NOT NULL,
 action_id uuid NOT NULL,
 operation text NOT NULL,
 agent_id uuid NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
 body_hash bytea NOT NULL CHECK(octet_length(body_hash)=32),
 response jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(actor_key, action_id)
);

-- Backfill: unread messages join the queued reminder of their priority, or the
-- first queued reminder when that priority has none.
UPDATE inbox_messages m SET reminder_run_id=r.id FROM runs r
 WHERE m.to_kind='agent' AND m.to_runtime_id IS NULL AND m.read_at IS NULL
 AND r.agent_id=m.to_agent_id AND r.status='queued' AND r.origin='inbox_reminder'
 AND r.priority=CASE WHEN m.when_busy='interrupt' THEN 0 ELSE 1 END;
UPDATE inbox_messages m SET reminder_run_id=(SELECT r.id FROM runs r WHERE r.agent_id=m.to_agent_id AND r.status='queued' AND r.origin='inbox_reminder' ORDER BY r.priority,r.queue_seq LIMIT 1)
 WHERE m.to_kind='agent' AND m.to_runtime_id IS NULL AND m.read_at IS NULL AND m.reminder_run_id IS NULL;
UPDATE inbox_messages m SET reminder_turn_id=t.id FROM task_turns t
 WHERE m.to_kind='agent' AND m.to_runtime_id IS NOT NULL AND m.read_at IS NULL
 AND NOT EXISTS(SELECT 1 FROM task_node_inbox_messages b WHERE b.message_id=m.id)
 AND t.runtime_id=m.to_runtime_id AND t.status='queued' AND t.origin='inbox_reminder' AND t.inbox_scope_node_id IS NULL
 AND t.priority=CASE WHEN m.when_busy='interrupt' THEN 0 ELSE 1 END;
UPDATE inbox_messages m SET reminder_turn_id=(SELECT t.id FROM task_turns t WHERE t.runtime_id=m.to_runtime_id AND t.status='queued' AND t.origin='inbox_reminder' AND t.inbox_scope_node_id IS NULL ORDER BY t.priority,t.queue_seq LIMIT 1)
 WHERE m.to_kind='agent' AND m.to_runtime_id IS NOT NULL AND m.read_at IS NULL AND m.reminder_turn_id IS NULL
 AND NOT EXISTS(SELECT 1 FROM task_node_inbox_messages b WHERE b.message_id=m.id);

-- Queued reminders that no longer carry any pending message are voided now
-- instead of being skipped when they start. A reminder that carries a sleep
-- wake still runs.
WITH voided AS (
 UPDATE runs r SET status='interrupted', end_reason='no_pending_messages', ended_at=now()
 WHERE r.status='queued' AND r.origin='inbox_reminder' AND r.wake_sleep_id IS NULL
 AND NOT EXISTS(SELECT 1 FROM inbox_messages m WHERE m.reminder_run_id=r.id AND m.read_at IS NULL AND m.cancelled_at IS NULL)
 RETURNING r.id, r.agent_id, r.trigger_inbox_message_id)
INSERT INTO lifecycle_events(id, agent_id, run_id, type, data, inbox_message_id)
 SELECT gen_random_uuid(), agent_id, id, 'run.ended', '{"status":"interrupted","endReason":"no_pending_messages"}'::jsonb, trigger_inbox_message_id FROM voided;
WITH voided AS (
 UPDATE task_turns t SET status='interrupted', void_reason='no_pending_messages', ended_at=now()
 WHERE t.status='queued' AND t.origin='inbox_reminder' AND t.wake_sleep_id IS NULL AND NOT t.question_wakeup
 AND NOT EXISTS(SELECT 1 FROM inbox_messages m WHERE m.reminder_turn_id=t.id AND m.read_at IS NULL AND m.cancelled_at IS NULL)
 AND NOT EXISTS(SELECT 1 FROM task_node_inbox_messages b JOIN inbox_messages m ON m.id=b.message_id
  WHERE b.reminder_run_id=t.id AND b.not_executed_at IS NULL AND m.read_at IS NULL AND m.cancelled_at IS NULL)
 RETURNING t.id, t.task_id, t.runtime_id, t.node_run_id, t.trigger_inbox_message_id)
INSERT INTO lifecycle_events(id, agent_id, type, data, task_id, workflow_node, task_turn_id, inbox_message_id)
 SELECT gen_random_uuid(), i.agent_id, 'run.ended', '{"outcome":"interrupted","voidReason":"no_pending_messages"}'::jsonb, v.task_id, v.node_run_id::text, v.id, v.trigger_inbox_message_id
 FROM voided v JOIN task_instances i ON i.id=v.runtime_id;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM inbox_messages WHERE cancelled_at IS NOT NULL)
 OR EXISTS(SELECT 1 FROM runs WHERE end_reason IN ('user_stopped','user_cancelled','merged_into_interrupt','no_pending_messages'))
 OR EXISTS(SELECT 1 FROM task_turns WHERE void_reason IN ('user_cancelled','no_pending_messages'))
 OR EXISTS(SELECT 1 FROM inbox_questions WHERE state='cancelled')
 OR EXISTS(SELECT 1 FROM agent_actions) THEN
  RAISE EXCEPTION 'queue_items_down_refused: cancelled or voided queue items exist';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE agent_actions;
ALTER TABLE task_turns DROP COLUMN question_wakeup;
ALTER TABLE inbox_questions DROP CONSTRAINT inbox_questions_state_check;
ALTER TABLE inbox_questions ADD CONSTRAINT inbox_questions_state_check CHECK(state IN ('open','answered','expired','closed'));
ALTER TABLE task_turns DROP CONSTRAINT task_turns_void_reason_check;
ALTER TABLE task_turns ADD CONSTRAINT task_turns_void_reason_check CHECK(void_reason IN ('merged_into_interrupt'));
ALTER TABLE runs DROP CONSTRAINT runs_end_reason_check;
ALTER TABLE runs ADD CONSTRAINT runs_end_reason_check CHECK(end_reason IN ('user_interrupt','server_restart','sleep_superseded','sleep_invalidated'));
DROP INDEX inbox_messages_reminder_turn;
DROP INDEX inbox_messages_reminder_run;
ALTER TABLE inbox_messages DROP CONSTRAINT inbox_messages_one_reminder, DROP CONSTRAINT inbox_messages_cancelled_unread,
 DROP COLUMN reminder_turn_id, DROP COLUMN reminder_run_id, DROP COLUMN cancelled_at;
