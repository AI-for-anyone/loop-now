-- +goose Up
-- 项目公共Prompt与Agent Prompt独立编辑版本；保留既有Agent文本。
ALTER TABLE projects ADD COLUMN prompt text NOT NULL DEFAULT '',
    ADD COLUMN prompt_version bigint NOT NULL DEFAULT 1 CHECK (prompt_version > 0);
ALTER TABLE agents ADD COLUMN prompt_version bigint NOT NULL DEFAULT 1 CHECK (prompt_version > 0);

-- +goose Down
-- 有新Prompt或schema3任务数据时不得据此声称无损回退；运行回退另行review。
ALTER TABLE agents DROP COLUMN prompt_version;
ALTER TABLE projects DROP COLUMN prompt_version,DROP COLUMN prompt;
