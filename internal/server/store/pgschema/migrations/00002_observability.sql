-- +goose Up
-- 可观测（变更 add-observability）：运行记录、用量，以及执行的两个时间点。只追加，不更新也不删除。

-- started_at 即派发时间；这里补上进入回复与发起打断的时间。
ALTER TABLE runs
    ADD COLUMN running_at          timestamptz,
    ADD COLUMN abort_requested_at  timestamptz;
-- 异常与失败列表按结束时间倒序翻页。
CREATE INDEX runs_anomalies ON runs (ended_at DESC, id DESC) WHERE status IN ('failed', 'interrupted');

-- 平台自动记录的生命周期事件。不含消息正文、工具参数与输出、凭证。
CREATE TABLE lifecycle_events (
    id                uuid PRIMARY KEY,
    at                timestamptz NOT NULL DEFAULT now(),
    agent_id          uuid NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    -- 与回复无关的 Agent 事件（例如空闲超时退出）为空。
    run_id            uuid REFERENCES runs (id) ON DELETE CASCADE,
    type              text NOT NULL CHECK (type ~ '^[a-z_]+(\.[a-z_]+)+$'),
    -- 预留 agent，表示 Agent 自述；第一版只有平台记录。
    source            text NOT NULL DEFAULT 'platform' CHECK (source IN ('platform')),
    data              jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(data) = 'object'),
    instance_id       uuid,
    -- 预留给任务、workflow 与 Inbox，第一版恒为空；相关表尚不存在，不加外键。
    task_id           uuid,
    workflow_node     text,
    inbox_message_id  uuid
);
CREATE INDEX lifecycle_events_by_run ON lifecycle_events (run_id, at) WHERE run_id IS NOT NULL;
CREATE INDEX lifecycle_events_by_agent ON lifecycle_events (agent_id, at);
-- 概览读取每个 Agent 上次进程退出。
CREATE INDEX lifecycle_events_process_exits ON lifecycle_events (agent_id, at DESC) WHERE type IN ('pi.stopped', 'pi.exited');
-- 启动时恢复每个 CLI 版本最近一次的安装结果。
CREATE INDEX lifecycle_events_cli_installs ON lifecycle_events ((data ->> 'cli'), (data ->> 'version'), at DESC) WHERE type = 'cli.install';

-- 每次模型调用（以及压缩、缓存预热、工具自身）的 token 用量。只统计 token，不保存费用。
CREATE TABLE usage_records (
    id              uuid PRIMARY KEY,
    -- 服务端收到这条用量的时间，作为统计的时间依据。
    at              timestamptz NOT NULL DEFAULT now(),
    agent_id        uuid NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    -- 回复之外的用量（例如两轮之间的缓存预热）为空。
    run_id          uuid REFERENCES runs (id) ON DELETE CASCADE,
    kind            text NOT NULL CHECK (kind IN ('assistant', 'tool', 'compaction', 'cache_warm', 'other')),
    -- Pi 模型目录中的提供方与模型 ID；都为空表示未知模型。
    provider        text,
    model           text,
    response_model  text,
    stop_reason     text,
    input           bigint NOT NULL DEFAULT 0 CHECK (input >= 0),
    output          bigint NOT NULL DEFAULT 0 CHECK (output >= 0),
    cache_read      bigint NOT NULL DEFAULT 0 CHECK (cache_read >= 0),
    cache_write     bigint NOT NULL DEFAULT 0 CHECK (cache_write >= 0),
    -- 推理 token 是输出的一部分，不计入总数。
    reasoning       bigint CHECK (reasoning >= 0),
    total           bigint GENERATED ALWAYS AS (input + output + cache_read + cache_write) STORED,
    duration_ms     bigint CHECK (duration_ms >= 0),
    CHECK ((provider IS NULL) = (model IS NULL))
);
CREATE INDEX usage_records_by_time ON usage_records (at);
CREATE INDEX usage_records_by_agent ON usage_records (agent_id, at);
CREATE INDEX usage_records_by_run ON usage_records (run_id) WHERE run_id IS NOT NULL;

-- +goose Down
DROP TABLE usage_records;
DROP TABLE lifecycle_events;
DROP INDEX runs_anomalies;
ALTER TABLE runs DROP COLUMN abort_requested_at, DROP COLUMN running_at;
