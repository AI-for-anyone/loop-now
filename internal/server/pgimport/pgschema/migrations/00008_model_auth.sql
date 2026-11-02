-- +goose Up
-- 只新增版本化模型认证存储；不转换或删除旧凭证，不执行开发库迁移。
CREATE TABLE model_auth_versions (
    id uuid PRIMARY KEY,
    agent_id uuid NOT NULL REFERENCES agents(id),
    provider text NOT NULL,
    auth_method text NOT NULL CHECK (auth_method IN ('api_key','oauth')),
    account_label text,
    alg text NOT NULL,
    key_id text NOT NULL,
    wrapped_dek bytea NOT NULL,
    nonce bytea NOT NULL,
    ciphertext bytea NOT NULL,
    generation bigint NOT NULL DEFAULT 1 CHECK (generation >= 1),
    refresh_state text NOT NULL DEFAULT 'ready' CHECK (refresh_state IN ('ready','refreshing','unavailable')),
    refresh_attempt_id uuid,
    refresh_started_at timestamptz,
    error_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id,agent_id),
    CHECK ((refresh_state = 'refreshing') = (refresh_attempt_id IS NOT NULL)),
    CHECK ((refresh_state = 'refreshing') = (refresh_started_at IS NOT NULL))
);
CREATE INDEX model_auth_versions_agent ON model_auth_versions(agent_id,created_at DESC,id DESC);

CREATE TABLE agent_model_auth (
    agent_id uuid PRIMARY KEY REFERENCES agents(id),
    version_id uuid,
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (version_id,agent_id) REFERENCES model_auth_versions(id,agent_id)
);

CREATE TABLE model_auth_operations (
    id uuid PRIMARY KEY,
    agent_id uuid NOT NULL REFERENCES agents(id),
    provider text NOT NULL,
    config_version bigint NOT NULL CHECK (config_version >= 1),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','awaiting_authorization','succeeded','failed','cancelled','expired')),
    authorization_url text,
    error_code text,
    started_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    completed_at timestamptz,
    CHECK ((status IN ('succeeded','failed','cancelled','expired')) = (completed_at IS NOT NULL)),
    CHECK (status IN ('pending','awaiting_authorization') OR authorization_url IS NULL)
);
CREATE UNIQUE INDEX model_auth_operations_one_active ON model_auth_operations(agent_id)
    WHERE status IN ('pending','awaiting_authorization');
CREATE INDEX model_auth_operations_latest ON model_auth_operations(agent_id,started_at DESC,id DESC);

-- +goose Down
DROP TABLE model_auth_operations;
DROP TABLE agent_model_auth;
DROP TABLE model_auth_versions;
