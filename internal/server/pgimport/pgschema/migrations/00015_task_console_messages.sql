-- +goose Up
CREATE TABLE task_console_chat_eligibility (
 task_id uuid NOT NULL REFERENCES task_records(id),
 node_run_id uuid NOT NULL,
 source_run_id uuid NOT NULL REFERENCES task_turns(id),
 recorded_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(task_id,node_run_id)
);

CREATE TABLE task_console_message_lineage (
 run_id uuid PRIMARY KEY REFERENCES task_turns(id),
 task_id uuid NOT NULL REFERENCES task_records(id),
 node_run_id uuid NOT NULL,
 agent_id uuid NOT NULL REFERENCES agents(id),
 runtime_id uuid NOT NULL REFERENCES task_instances(id),
 execution_mode text NOT NULL CHECK(execution_mode IN ('task_node','task_conversation'))
);
CREATE INDEX task_console_lineage_target ON task_console_message_lineage(task_id,node_run_id);

ALTER TABLE task_turns DROP CONSTRAINT task_turns_origin_check;
ALTER TABLE task_turns ADD CONSTRAINT task_turns_origin_check CHECK(origin IN ('task_node','inbox_reminder','sleep_wake','console_node'));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM task_console_chat_eligibility)
 OR EXISTS(SELECT 1 FROM task_console_message_lineage)
 OR EXISTS(SELECT 1 FROM task_turns WHERE origin='console_node') THEN
  RAISE EXCEPTION 'task_console_messages_down_requires_no_new_data';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE task_turns DROP CONSTRAINT task_turns_origin_check;
ALTER TABLE task_turns ADD CONSTRAINT task_turns_origin_check CHECK(origin IN ('task_node','inbox_reminder','sleep_wake'));
DROP TABLE task_console_message_lineage;
DROP TABLE task_console_chat_eligibility;
