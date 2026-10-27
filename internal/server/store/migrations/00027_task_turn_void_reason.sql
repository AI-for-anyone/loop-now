-- +goose Up
-- A queued node notification turn emptied by promoting its messages to an
-- interrupt turn is voided with this reason so the console can hide it.
ALTER TABLE task_turns ADD COLUMN void_reason text CHECK(void_reason IN ('merged_into_interrupt'));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM task_turns WHERE void_reason IS NOT NULL) THEN
  RAISE EXCEPTION 'task_turn_void_reason_down_requires_no_data';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE task_turns DROP COLUMN void_reason;
