-- +goose Up
-- 独立工作树候选：005插件存储。尚未整合主运行包，不授权DEV执行。
CREATE TABLE plugins (
    id uuid PRIMARY KEY,
    name text,
    display_name text,
    description text,
    version text,
    source jsonb NOT NULL CHECK (jsonb_typeof(source) = 'object'),
    resolved jsonb,
    manifest jsonb,
    package_dir text,
    managed_dir boolean NOT NULL DEFAULT false,
    status text NOT NULL CHECK (status IN ('installing','installed','upgrading','failed')),
    publication_generation bigint NOT NULL DEFAULT 1 CHECK (publication_generation > 0),
    package_generation bigint NOT NULL DEFAULT 0 CHECK (package_generation >= 0),
    enabled boolean NOT NULL DEFAULT false,
    pending_source jsonb,
    last_error_operation text CHECK (last_error_operation IN ('install','upgrade','load')),
    last_error text,
    last_error_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);
CREATE UNIQUE INDEX plugins_name_live ON plugins(name) WHERE deleted_at IS NULL AND name IS NOT NULL;

CREATE TABLE plugin_instances (
    id uuid PRIMARY KEY,
    plugin_id uuid NOT NULL REFERENCES plugins(id),
    project_id uuid NOT NULL REFERENCES projects(id),
    enabled boolean NOT NULL DEFAULT false,
    values jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(values) = 'object'),
    alg text,
    key_id text,
    wrapped_dek bytea,
    nonce bytea,
    ciphertext bytea,
    secret_fields jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(secret_fields) = 'object'),
    config_version bigint NOT NULL DEFAULT 1 CHECK (config_version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CONSTRAINT plugin_instances_sealed_all_or_none CHECK (
        (alg IS NULL AND key_id IS NULL AND wrapped_dek IS NULL AND nonce IS NULL AND ciphertext IS NULL)
        OR (alg IS NOT NULL AND key_id IS NOT NULL AND wrapped_dek IS NOT NULL AND nonce IS NOT NULL AND ciphertext IS NOT NULL))
);
CREATE UNIQUE INDEX plugin_instances_live_pair ON plugin_instances(plugin_id,project_id) WHERE deleted_at IS NULL;
CREATE INDEX plugin_instances_project ON plugin_instances(project_id) WHERE deleted_at IS NULL;

CREATE TABLE plugin_template_names (
    name text PRIMARY KEY,
    plugin_id uuid NOT NULL REFERENCES plugins(id),
    plugin_name text NOT NULL,
    removed_at timestamptz
);

ALTER TABLE agent_credentials ADD COLUMN template_source text;
UPDATE agent_credentials SET template_source = 'builtin' WHERE purpose = 'cli';
ALTER TABLE agent_credentials ADD CONSTRAINT agent_credentials_template_source
    CHECK ((purpose = 'cli') = (template_source IS NOT NULL));

-- +goose Down
-- 仅验证候选表为空时的结构Down；有插件/实例数据的回退策略尚未确认。
-- 只能在006及依赖方已回退后使用；不使用CASCADE吞掉下游依赖。
ALTER TABLE agent_credentials DROP COLUMN template_source;
DROP TABLE plugin_template_names;
DROP TABLE plugin_instances;
DROP TABLE plugins;
