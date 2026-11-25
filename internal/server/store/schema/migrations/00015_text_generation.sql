-- +goose Up
-- Who writes with AI ("Write with AI", such as a Prompt draft): the runtime model a person chose, or Automatic when
-- provider and model_id are NULL. One row.
CREATE TABLE text_generation_settings (
    singleton       INTEGER NOT NULL DEFAULT 1 CHECK (singleton IN (0, 1)),
    provider        TEXT,
    model_id        TEXT,
    thinking_level  TEXT CHECK (thinking_level IN ('off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max')),
    updated_at      INTEGER NOT NULL DEFAULT (now()),
    CONSTRAINT text_generation_settings_pkey PRIMARY KEY (singleton),
    CONSTRAINT text_generation_settings_singleton_check CHECK (singleton),
    CONSTRAINT text_generation_settings_selection_check CHECK ((provider IS NULL) = (model_id IS NULL)
        AND (provider IS NOT NULL OR thinking_level IS NULL))
) STRICT;

INSERT INTO text_generation_settings DEFAULT VALUES;

-- +goose Down
DROP TABLE text_generation_settings;
