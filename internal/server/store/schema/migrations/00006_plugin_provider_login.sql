-- +goose Up
-- 插件提供方的订阅登录（plugins/SPEC.md §13）：项目登录操作不再只限 openai-codex，provider 是模型目录里任何带 OAuth 的提供方；
-- 设备码登录时记下给人看的验证码与验证地址（终态清空，与授权地址一样）。SQLite 不能修改 CHECK，整表重建。
CREATE TABLE project_model_auth_operations_new (
    id                 TEXT NOT NULL,
    project_id         TEXT NOT NULL,
    provider           TEXT NOT NULL,
    target_account_id  TEXT,
    target_revision    INTEGER,
    name               TEXT,
    account_id         TEXT,
    status             TEXT NOT NULL DEFAULT 'pending',
    authorization_url  TEXT,
    error_code         TEXT,
    started_at         INTEGER NOT NULL DEFAULT (now()),
    expires_at         INTEGER NOT NULL,
    completed_at       INTEGER,
    user_code          TEXT,
    verification_uri   TEXT,
    device_expires_at  INTEGER,
    CONSTRAINT project_model_auth_operations_pkey PRIMARY KEY (id),
    CONSTRAINT project_model_auth_operations_account_id_project_id_fkey FOREIGN KEY (account_id, project_id) REFERENCES project_credential_accounts(id, project_id),
    CONSTRAINT project_model_auth_operations_project_id_fkey FOREIGN KEY (project_id) REFERENCES projects(id),
    CONSTRAINT project_model_auth_operations_target_account_id_project_id_fkey FOREIGN KEY (target_account_id, project_id) REFERENCES project_credential_accounts(id, project_id),
    CONSTRAINT project_model_auth_operations_check CHECK ((((target_account_id IS NULL) AND (target_revision IS NULL) AND (name IS NOT NULL)) OR ((target_account_id IS NOT NULL) AND (target_revision >= 1) AND (name IS NULL)))),
    CONSTRAINT project_model_auth_operations_check1 CHECK (((status IN ('succeeded', 'failed', 'cancelled', 'expired')) = (completed_at IS NOT NULL))),
    CONSTRAINT project_model_auth_operations_check2 CHECK (((status IN ('pending', 'awaiting_authorization')) OR (authorization_url IS NULL))),
    CONSTRAINT project_model_auth_operations_device_check CHECK (((status IN ('pending', 'awaiting_authorization')) OR (user_code IS NULL AND verification_uri IS NULL AND device_expires_at IS NULL))),
    CONSTRAINT project_model_auth_operations_device_pair CHECK (((user_code IS NULL) = (verification_uri IS NULL))),
    CONSTRAINT project_model_auth_operations_provider_check CHECK ((length(provider) BETWEEN 1 AND 64)),
    CONSTRAINT project_model_auth_operations_status_check CHECK ((status IN ('pending', 'awaiting_authorization', 'succeeded', 'failed', 'cancelled', 'expired')))
) STRICT;

INSERT INTO project_model_auth_operations_new (id, project_id, provider, target_account_id, target_revision, name, account_id, status, authorization_url, error_code, started_at, expires_at, completed_at)
    SELECT id, project_id, provider, target_account_id, target_revision, name, account_id, status, authorization_url, error_code, started_at, expires_at, completed_at FROM project_model_auth_operations;
DROP TABLE project_model_auth_operations;
ALTER TABLE project_model_auth_operations_new RENAME TO project_model_auth_operations;
CREATE UNIQUE INDEX project_model_auth_active_new ON project_model_auth_operations (project_id, provider) WHERE ((target_account_id IS NULL) AND (status IN ('pending', 'awaiting_authorization')));
CREATE UNIQUE INDEX project_model_auth_active_target ON project_model_auth_operations (target_account_id) WHERE ((target_account_id IS NOT NULL) AND (status IN ('pending', 'awaiting_authorization')));
