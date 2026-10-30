-- +goose Up
CREATE TABLE model_settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    models_json text NOT NULL DEFAULT '{"providers":{}}',
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO model_settings(singleton) VALUES (true);

-- +goose Down
DROP TABLE model_settings;
