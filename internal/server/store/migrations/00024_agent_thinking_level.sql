-- +goose Up
ALTER TABLE agents ADD COLUMN thinking_level text CHECK(thinking_level IN ('off','minimal','low','medium','high','xhigh','max'));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM agents WHERE thinking_level IS NOT NULL) THEN RAISE EXCEPTION 'explicit thinking levels exist; destructive rollback refused'; END IF; END $$;
-- +goose StatementEnd
ALTER TABLE agents DROP COLUMN thinking_level;
