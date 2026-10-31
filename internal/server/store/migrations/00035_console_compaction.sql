-- +goose Up
ALTER TABLE runs ALTER COLUMN user_message_id DROP NOT NULL;
ALTER TABLE runs DROP CONSTRAINT runs_origin_check;
ALTER TABLE runs ADD CONSTRAINT runs_origin_check CHECK(origin IN ('console','inbox_reminder','sleep_wake','console_compact'));
ALTER TABLE runs ADD CONSTRAINT runs_console_compact_message CHECK ((origin='console_compact')=(user_message_id IS NULL));

-- +goose Down
DELETE FROM runs WHERE origin='console_compact';
ALTER TABLE runs DROP CONSTRAINT runs_console_compact_message;
ALTER TABLE runs ALTER COLUMN user_message_id SET NOT NULL;
ALTER TABLE runs DROP CONSTRAINT runs_origin_check;
ALTER TABLE runs ADD CONSTRAINT runs_origin_check CHECK(origin IN ('console','inbox_reminder','sleep_wake'));
