-- +goose Up
-- 任务编号（API 的 TaskSummary.number，控制台显示为 #12）：每个项目从 1 开始，创建任务时分配，之后不变、不重用
-- （任务被删除也不重用）。编号保存在任务文档里（view.task.number），任务对象原样从文档读出；number 列由文档生成，
-- 用来保证它必有、为正、在项目内唯一。projects.last_task_number 是项目最后分配的编号，新建任务时在同一个写事务里加一。

-- 已有的任务按项目、创建时间（相同时按 id）补编号。
UPDATE task_records SET document = json_set(document, '$.view.task.number', n.number)
FROM (SELECT id, row_number() OVER (PARTITION BY project_id ORDER BY created_at, id) AS number FROM task_records) AS n
WHERE n.id = task_records.id;

ALTER TABLE task_records ADD COLUMN number INTEGER GENERATED ALWAYS AS (CAST(document->'$.view.task'->>'number' AS INTEGER)) VIRTUAL NOT NULL CHECK (number > 0);
CREATE UNIQUE INDEX task_records_project_number ON task_records (project_id, number);

ALTER TABLE projects ADD COLUMN last_task_number INTEGER NOT NULL DEFAULT 0 CHECK (last_task_number >= 0);
UPDATE projects SET last_task_number = coalesce((SELECT max(t.number) FROM task_records t WHERE t.project_id = projects.id), 0);

-- 编号分配后不变：改写文档时不能改掉它。
-- +goose StatementBegin
CREATE TRIGGER task_records_number_immutable BEFORE UPDATE OF document ON task_records
WHEN CAST(NEW.document->'$.view.task'->>'number' AS INTEGER) IS NOT CAST(OLD.document->'$.view.task'->>'number' AS INTEGER)
BEGIN
    SELECT RAISE(ABORT, 'task_number_is_immutable');
END;
-- +goose StatementEnd

-- 保存下来的任务对象也补上编号：task.updated 事件（事件流续传时重放）与幂等动作记下的响应（同一动作重放时原样返回，
-- 多数是任务详情）。
UPDATE task_events SET data = json_set(data, '$.task.number', (SELECT t.number FROM task_records t WHERE t.id = task_events.task_id))
WHERE type = 'task.updated' AND data->'$.task'->>'id' = task_id;
UPDATE task_actions SET response = json_set(response, '$.task.number', (SELECT t.number FROM task_records t WHERE t.id = task_actions.task_id))
WHERE json_type(response, '$.task') = 'object' AND response->'$.task'->>'id' = task_id;

-- +goose Down
UPDATE task_actions SET response = json_remove(response, '$.task.number')
WHERE json_type(response, '$.task') = 'object' AND response->'$.task'->>'id' = task_id;
UPDATE task_events SET data = json_remove(data, '$.task.number')
WHERE type = 'task.updated' AND data->'$.task'->>'id' = task_id;
DROP TRIGGER task_records_number_immutable;
ALTER TABLE projects DROP COLUMN last_task_number;
DROP INDEX task_records_project_number;
ALTER TABLE task_records DROP COLUMN number;
UPDATE task_records SET document = json_remove(document, '$.view.task.number');
