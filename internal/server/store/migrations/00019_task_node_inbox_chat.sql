-- +goose Up
ALTER TABLE task_turns ADD COLUMN inbox_scope_node_id uuid;
DROP INDEX task_turns_one_reminder;
CREATE UNIQUE INDEX task_turns_one_reminder ON task_turns(runtime_id,priority,coalesce(inbox_scope_node_id,'00000000-0000-0000-0000-000000000000'::uuid),(CASE WHEN inbox_scope_node_id IS NULL THEN '' ELSE execution_mode END)) WHERE status='queued' AND origin='inbox_reminder';
CREATE TABLE task_node_inbox_messages (
 message_id uuid PRIMARY KEY REFERENCES inbox_messages(id),
 root_message_id uuid NOT NULL REFERENCES inbox_messages(id),
 task_id uuid NOT NULL REFERENCES task_records(id),
 node_run_id uuid NOT NULL,
 agent_id uuid NOT NULL REFERENCES agents(id),
 runtime_id uuid NOT NULL,
 FOREIGN KEY(runtime_id,task_id,agent_id) REFERENCES task_instances(id,task_id,agent_id),
 execution_mode text NOT NULL CHECK(execution_mode IN ('task_node','task_conversation')),
 reminder_run_id uuid REFERENCES task_turns(id),
 not_executed_reason text,
 not_executed_at timestamptz,
 CHECK((not_executed_reason IS NULL)=(not_executed_at IS NULL))
);
CREATE INDEX task_node_inbox_scope ON task_node_inbox_messages(task_id,node_run_id);
CREATE INDEX task_node_inbox_batch ON task_node_inbox_messages(reminder_run_id);
CREATE TABLE task_node_inbox_run_links (
 message_id uuid NOT NULL REFERENCES task_node_inbox_messages(message_id),
 run_id uuid NOT NULL REFERENCES task_turns(id),
 role text NOT NULL CHECK(role IN ('notification','read','reply')),
 PRIMARY KEY(message_id,run_id,role)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM task_node_inbox_messages) OR EXISTS(SELECT 1 FROM task_node_inbox_run_links) OR EXISTS(SELECT 1 FROM task_turns WHERE inbox_scope_node_id IS NOT NULL) THEN
  RAISE EXCEPTION 'task_node_inbox_chat_down_requires_no_new_data';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE task_node_inbox_run_links;
DROP TABLE task_node_inbox_messages;
DROP INDEX task_turns_one_reminder;
CREATE UNIQUE INDEX task_turns_one_reminder ON task_turns(runtime_id,priority) WHERE status='queued' AND origin='inbox_reminder';
ALTER TABLE task_turns DROP COLUMN inbox_scope_node_id;
