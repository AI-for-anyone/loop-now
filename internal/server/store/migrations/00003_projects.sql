-- +goose Up
-- 项目层（变更 add-project-layer）：项目软删除与 Manager、平台持有的 Agent、全局 Skills 库、组织名全平台唯一。

-- 组织名在全平台唯一（第一版只有默认组织）。
CREATE UNIQUE INDEX organizations_name_unique ON organizations (name);

-- 项目：软删除后名称可以再用；默认项目不能删除；Manager 由人指定。
ALTER TABLE projects
    ADD COLUMN deleted_at        timestamptz,
    ADD COLUMN manager_agent_id  uuid,
    ADD CONSTRAINT projects_default_not_deleted CHECK (NOT (is_default AND deleted_at IS NOT NULL));
DROP INDEX projects_name_per_org;
CREATE UNIQUE INDEX projects_name_live ON projects (organization_id, name) WHERE deleted_at IS NULL;

-- Agent：project_id 为空表示平台持有；平台内名称唯一。
ALTER TABLE agents ALTER COLUMN project_id DROP NOT NULL;
ALTER TABLE agents ADD CONSTRAINT agents_id_project UNIQUE (id, project_id);
CREATE UNIQUE INDEX agents_name_platform ON agents (name) WHERE project_id IS NULL AND deleted_at IS NULL;
CREATE INDEX agents_live_platform ON agents (created_at) WHERE project_id IS NULL AND deleted_at IS NULL;

-- Manager 必须是本项目的 Agent：平台 Agent 的 project_id 为空，匹配不上任何项目。
ALTER TABLE projects ADD CONSTRAINT projects_manager_in_project
    FOREIGN KEY (manager_agent_id, id) REFERENCES agents (id, project_id);

-- 全局 Skills 库：不再按项目划分。跨项目同名时迁移失败，由人处理后再迁移。
-- +goose StatementBegin
DO $$
DECLARE dup text;
BEGIN
    SELECT string_agg(DISTINCT name, ', ') INTO dup
    FROM (SELECT name FROM skill_templates GROUP BY name HAVING count(*) > 1) d;
    IF dup IS NOT NULL THEN
        RAISE EXCEPTION '全局 Skills 库要求模板名称唯一，以下名称在多个项目中重复：%', dup;
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE skill_templates DROP CONSTRAINT skill_templates_project_id_name_key;
ALTER TABLE skill_templates DROP COLUMN project_id;
ALTER TABLE skill_templates ADD CONSTRAINT skill_templates_name_key UNIQUE (name);

-- +goose Down
-- 平台 Agent 没有可以放回的项目：存在时回退失败，避免悄悄丢数据。
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM agents WHERE project_id IS NULL) THEN
        RAISE EXCEPTION '存在平台持有的 Agent，不能回退到 00002';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE skill_templates DROP CONSTRAINT skill_templates_name_key;
ALTER TABLE skill_templates ADD COLUMN project_id uuid REFERENCES projects (id);
UPDATE skill_templates SET project_id = (SELECT id FROM projects WHERE is_default);
ALTER TABLE skill_templates ALTER COLUMN project_id SET NOT NULL;
ALTER TABLE skill_templates ADD CONSTRAINT skill_templates_project_id_name_key UNIQUE (project_id, name);

ALTER TABLE projects DROP CONSTRAINT projects_manager_in_project;
DROP INDEX agents_live_platform;
DROP INDEX agents_name_platform;
ALTER TABLE agents DROP CONSTRAINT agents_id_project;
ALTER TABLE agents ALTER COLUMN project_id SET NOT NULL;

DROP INDEX projects_name_live;
CREATE UNIQUE INDEX projects_name_per_org ON projects (organization_id, name);
ALTER TABLE projects
    DROP CONSTRAINT projects_default_not_deleted,
    DROP COLUMN manager_agent_id,
    DROP COLUMN deleted_at;

DROP INDEX organizations_name_unique;
