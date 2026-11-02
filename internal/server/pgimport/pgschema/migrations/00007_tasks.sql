-- +goose Up
-- 通用任务（005/006保留给插件及通道）。本迁移当前只编写，不执行开发库DDL。
CREATE TABLE task_records (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects(id),
    status text NOT NULL CHECK(status IN ('preparing','running','waiting','blocked','completed','cancelled')),
    document jsonb NOT NULL CHECK(jsonb_typeof(document)='object'),
    version bigint NOT NULL CHECK(version>0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    ended_at timestamptz,
    CHECK((status IN ('completed','cancelled'))=(ended_at IS NOT NULL)),
    CHECK(document->'view'->'task'->>'status'=status),
    CHECK((document->'view'->'task'->>'version')::bigint=version)
);
CREATE INDEX task_records_project_page ON task_records(project_id,created_at DESC,id DESC);
CREATE INDEX task_records_unfinished ON task_records(project_id) WHERE ended_at IS NULL;

CREATE TABLE task_instances (
    id uuid PRIMARY KEY,
    task_id uuid NOT NULL REFERENCES task_records(id),
    agent_id uuid NOT NULL REFERENCES agents(id),
    agent_name text NOT NULL,
    snapshot bytea NOT NULL,
    sources jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(sources)='array'),
    session_file text,
    session_state text NOT NULL DEFAULT 'none' CHECK(session_state IN ('none','active','missing')),
    fork_source text,
    prepared boolean NOT NULL DEFAULT false,
    retired boolean NOT NULL DEFAULT false,
    UNIQUE(task_id,agent_id),
    UNIQUE(id,task_id),
    UNIQUE(id,agent_id),
    UNIQUE(id,task_id,agent_id)
);
CREATE INDEX task_instances_by_agent ON task_instances(agent_id,task_id);

CREATE TABLE task_turns (
    id uuid PRIMARY KEY,
    task_id uuid NOT NULL REFERENCES task_records(id),
    runtime_id uuid NOT NULL,
    FOREIGN KEY(runtime_id,task_id) REFERENCES task_instances(id,task_id),
    node_run_id uuid,
	user_message_id uuid NOT NULL UNIQUE,
	assistant_message_id uuid NOT NULL UNIQUE,
    status text NOT NULL CHECK(status IN ('queued','starting','running','aborting','completed','failed','interrupted')),
    origin text NOT NULL CHECK(origin IN ('task_node','inbox_reminder')),
    priority smallint NOT NULL DEFAULT 1 CHECK(priority IN (0,1)),
    queue_seq bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    text text NOT NULL,
    reply text NOT NULL DEFAULT '',
    error_code text,
    error_message text,
    queued_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    ended_at timestamptz,
    request_id uuid,
    trigger_inbox_message_id uuid REFERENCES inbox_messages(id)
);
CREATE UNIQUE INDEX task_turns_one_active ON task_turns(runtime_id) WHERE status IN ('starting','running','aborting');
CREATE UNIQUE INDEX task_turns_one_reminder ON task_turns(runtime_id,priority) WHERE status='queued' AND origin='inbox_reminder';
CREATE INDEX task_turns_queue ON task_turns(runtime_id,priority,queue_seq) WHERE status='queued';
CREATE INDEX task_turns_node ON task_turns(node_run_id,queued_at,id);

CREATE TABLE task_actions (
    actor_key text NOT NULL,
    action_id uuid NOT NULL,
    operation text NOT NULL,
    task_id uuid NOT NULL REFERENCES task_records(id),
    body_hash bytea NOT NULL CHECK(octet_length(body_hash)=32),
    response jsonb NOT NULL,
    PRIMARY KEY(actor_key,action_id)
);
CREATE TABLE task_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id uuid NOT NULL REFERENCES task_records(id),
    project_id uuid NOT NULL REFERENCES projects(id),
    type text NOT NULL,
    data jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX task_events_task ON task_events(task_id,id);
CREATE INDEX task_events_project ON task_events(project_id,id);

CREATE TABLE task_artifacts (
    id uuid PRIMARY KEY,
    task_id uuid NOT NULL REFERENCES task_records(id),
    node_run_id uuid NOT NULL,
    name text NOT NULL,
    content_type text NOT NULL,
    blob_sha256 text NOT NULL REFERENCES blobs(sha256),
    size bigint NOT NULL CHECK(size>=0),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX task_artifacts_task ON task_artifacts(task_id,created_at,id);
CREATE TABLE task_archives (
    runtime_id uuid PRIMARY KEY REFERENCES task_instances(id),
    blob_sha256 text NOT NULL REFERENCES blobs(sha256),
    archived_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE task_notifications (
    id uuid PRIMARY KEY,
    task_id uuid NOT NULL REFERENCES task_records(id),
    node_run_id uuid,
    kind text NOT NULL,
    message_id uuid NOT NULL UNIQUE,
    text text NOT NULL,
    sent_at timestamptz,
    UNIQUE NULLS NOT DISTINCT(task_id,node_run_id,kind)
);

ALTER TABLE agent_tokens ADD COLUMN runtime_id uuid,
    ADD COLUMN task_id uuid,
    ADD CONSTRAINT agent_tokens_task_runtime FOREIGN KEY(runtime_id,task_id,agent_id) REFERENCES task_instances(id,task_id,agent_id),
    ADD CONSTRAINT agent_tokens_runtime_pair CHECK((runtime_id IS NULL)=(task_id IS NULL));
ALTER TABLE inbox_messages ADD COLUMN from_runtime_id uuid REFERENCES task_instances(id),
    ADD COLUMN to_runtime_id uuid REFERENCES task_instances(id),
    ADD CONSTRAINT inbox_from_runtime_agent FOREIGN KEY(from_runtime_id,from_agent_id) REFERENCES task_instances(id,agent_id),
    ADD CONSTRAINT inbox_to_runtime_agent FOREIGN KEY(to_runtime_id,to_agent_id) REFERENCES task_instances(id,agent_id);
CREATE INDEX inbox_to_runtime ON inbox_messages(to_runtime_id,created_at DESC,id DESC) WHERE to_runtime_id IS NOT NULL;
ALTER TABLE lifecycle_events ADD COLUMN task_turn_id uuid REFERENCES task_turns(id);
ALTER TABLE usage_records ADD COLUMN task_id uuid REFERENCES task_records(id),
    ADD COLUMN task_turn_id uuid REFERENCES task_turns(id);

-- +goose Down
ALTER TABLE usage_records DROP COLUMN task_turn_id,DROP COLUMN task_id;
ALTER TABLE lifecycle_events DROP COLUMN task_turn_id;
ALTER TABLE inbox_messages DROP COLUMN to_runtime_id,DROP COLUMN from_runtime_id;
ALTER TABLE agent_tokens DROP CONSTRAINT agent_tokens_task_runtime,DROP CONSTRAINT agent_tokens_runtime_pair,DROP COLUMN runtime_id,DROP COLUMN task_id;
DROP TABLE task_notifications;
DROP TABLE task_archives;
DROP TABLE task_artifacts;
DROP TABLE task_events;
DROP TABLE task_actions;
DROP TABLE task_turns;
DROP TABLE task_instances;
DROP TABLE task_records;
