-- +goose Up
ALTER TABLE agents ADD COLUMN service_tier text
    CHECK (service_tier IN ('default', 'priority', 'ultrafast'))
    CHECK (service_tier IS NULL OR model_provider = 'openai-codex');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS (SELECT 1 FROM agents WHERE service_tier IS NOT NULL) THEN RAISE EXCEPTION 'explicit service tiers exist; destructive rollback refused'; END IF; END $$;
-- +goose StatementEnd
ALTER TABLE agents DROP COLUMN service_tier;
