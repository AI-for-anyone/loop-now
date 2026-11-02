-- +goose Up
ALTER TABLE conversation_messages ADD COLUMN images jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(images)='array');
ALTER TABLE inbox_messages ADD COLUMN images jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(images)='array');
ALTER TABLE inbox_messages DROP CONSTRAINT inbox_messages_text_check;
ALTER TABLE inbox_messages ADD CONSTRAINT inbox_messages_text_check CHECK(text<>'' OR jsonb_array_length(images)>0);
-- Strict image reminders can require several bounded batches. Generic Inbox
-- keeps its existing one-reminder invariant; strict queues are task-lock serialized.
DROP INDEX task_turns_one_reminder;
CREATE UNIQUE INDEX task_turns_one_reminder ON task_turns(runtime_id,priority) WHERE status='queued' AND origin='inbox_reminder' AND inbox_scope_node_id IS NULL;
CREATE TABLE message_images (
 id uuid PRIMARY KEY,agent_id uuid NOT NULL REFERENCES agents(id),
 task_id uuid REFERENCES task_records(id),node_run_id uuid,runtime_id uuid,
 blob_sha text NOT NULL REFERENCES blobs(sha256),mime_type text NOT NULL CHECK(mime_type IN('image/png','image/jpeg','image/webp')),
 size bigint NOT NULL CHECK(size>0),width integer NOT NULL CHECK(width>0),height integer NOT NULL CHECK(height>0),
 conversation_message_id uuid REFERENCES conversation_messages(id),inbox_message_id uuid REFERENCES inbox_messages(id),created_at timestamptz NOT NULL DEFAULT now(),
 CHECK((task_id IS NULL AND node_run_id IS NULL AND runtime_id IS NULL) OR (task_id IS NOT NULL AND node_run_id IS NOT NULL AND runtime_id IS NOT NULL)),
 CHECK(NOT(conversation_message_id IS NOT NULL AND inbox_message_id IS NOT NULL)),
 CHECK(conversation_message_id IS NULL OR task_id IS NULL),CHECK(inbox_message_id IS NULL OR task_id IS NOT NULL)
);
CREATE INDEX message_images_inbox ON message_images(inbox_message_id) WHERE inbox_message_id IS NOT NULL;
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM message_images) THEN RAISE EXCEPTION 'message images exist; destructive rollback refused'; END IF; END $$;
-- +goose StatementEnd
DROP TABLE message_images;
DROP INDEX task_turns_one_reminder;
CREATE UNIQUE INDEX task_turns_one_reminder ON task_turns(runtime_id,priority,coalesce(inbox_scope_node_id,'00000000-0000-0000-0000-000000000000'::uuid),(CASE WHEN inbox_scope_node_id IS NULL THEN '' ELSE execution_mode END)) WHERE status='queued' AND origin='inbox_reminder';
ALTER TABLE inbox_messages DROP CONSTRAINT inbox_messages_text_check;
ALTER TABLE inbox_messages ADD CONSTRAINT inbox_messages_text_check CHECK(text<>'');
ALTER TABLE inbox_messages DROP COLUMN images;
ALTER TABLE conversation_messages DROP COLUMN images;
