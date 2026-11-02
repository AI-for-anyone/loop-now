-- +goose Up
ALTER TABLE inbox_questions DROP CONSTRAINT inbox_questions_state_check;
ALTER TABLE inbox_questions ADD CONSTRAINT inbox_questions_state_check CHECK(state IN ('open','answered','expired','closed'));
ALTER TABLE inbox_questions ADD closed_at timestamptz, ADD closed_reason text CHECK(closed_reason IN ('task_retry','task_cancel','node_completed'));
ALTER TABLE inbox_questions ADD CONSTRAINT inbox_questions_closed_record CHECK ((state='closed')=(closed_at IS NOT NULL AND closed_reason IS NOT NULL));
CREATE TABLE task_questions (
 question_message_id uuid PRIMARY KEY REFERENCES inbox_questions(message_id),
 task_id uuid NOT NULL REFERENCES task_records(id),
 runtime_id uuid NOT NULL,
 node_run_id uuid NOT NULL,
 FOREIGN KEY(runtime_id,task_id) REFERENCES task_instances(id,task_id)
);
CREATE INDEX task_questions_node ON task_questions(task_id,node_run_id);
CREATE TABLE inbox_question_outbox (
 id uuid PRIMARY KEY,
 question_message_id uuid NOT NULL REFERENCES inbox_questions(message_id),
 state text NOT NULL CHECK(state IN ('answered','expired','closed')),
 message_id uuid NOT NULL UNIQUE,
 delivered_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(question_message_id,state)
);
CREATE INDEX inbox_question_outbox_pending ON inbox_question_outbox(created_at,id) WHERE delivered_at IS NULL;
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM inbox_questions WHERE state='closed') OR EXISTS(SELECT 1 FROM task_questions) OR EXISTS(SELECT 1 FROM inbox_question_outbox) THEN
  RAISE EXCEPTION '00010 Down refuses to discard question history or pending delivery';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE inbox_question_outbox;
DROP TABLE task_questions;
ALTER TABLE inbox_questions DROP CONSTRAINT inbox_questions_closed_record, DROP closed_at, DROP closed_reason;
ALTER TABLE inbox_questions DROP CONSTRAINT inbox_questions_state_check;
ALTER TABLE inbox_questions ADD CONSTRAINT inbox_questions_state_check CHECK(state IN ('open','answered','expired'));
