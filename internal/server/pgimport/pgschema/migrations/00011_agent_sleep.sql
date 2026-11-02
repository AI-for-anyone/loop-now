-- +goose Up
ALTER TABLE runs DROP CONSTRAINT runs_end_reason_check;
ALTER TABLE runs ADD CONSTRAINT runs_end_reason_check CHECK(end_reason IN ('user_interrupt','server_restart','sleep_superseded','sleep_invalidated'));
ALTER TABLE agent_sessions ADD COLUMN sleep_revision bigint NOT NULL DEFAULT 0 CHECK(sleep_revision>=0);
ALTER TABLE task_instances ADD COLUMN sleep_revision bigint NOT NULL DEFAULT 0 CHECK(sleep_revision>=0);
CREATE TABLE agent_sleeps (
 id uuid PRIMARY KEY,
 scope_key text NOT NULL,
 scope_kind text NOT NULL CHECK(scope_kind IN ('normal','task')),
 mode text NOT NULL CHECK(mode IN ('normal','task_node','task_conversation')),
 agent_id uuid NOT NULL REFERENCES agents(id),
 task_id uuid REFERENCES task_records(id),
 runtime_id uuid,
 node_run_id uuid,
 session_epoch integer,
 source_run_id uuid NOT NULL,
 action_id uuid NOT NULL,
 body_hash bytea NOT NULL CHECK(octet_length(body_hash)=32),
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object'),
 state text NOT NULL CHECK(state IN ('requested','sleeping','woken','cancelled')),
 seconds bigint NOT NULL CHECK(seconds BETWEEN 1 AND 2147483647),
 reason text,
 requested_at timestamptz NOT NULL DEFAULT now(),
 entered_at timestamptz,
 wake_at timestamptz,
 ended_at timestamptz,
 end_reason text CHECK(end_reason IN ('deadline','queued_work','execution_failed','interrupted','task_ended','node_changed','session_reopened','owner_deleted')),
 timer_run_id uuid NOT NULL UNIQUE,
 wake_run_id uuid,
 revision bigint NOT NULL CHECK(revision>0),
 FOREIGN KEY(runtime_id,task_id,agent_id) REFERENCES task_instances(id,task_id,agent_id),
 UNIQUE(scope_key,action_id),
 CHECK((scope_kind='normal' AND mode='normal' AND task_id IS NULL AND runtime_id IS NULL AND node_run_id IS NULL AND session_epoch IS NOT NULL AND session_epoch>0 AND scope_key='normal/'||agent_id::text)
 OR (scope_kind='task' AND mode IN ('task_node','task_conversation') AND task_id IS NOT NULL AND runtime_id IS NOT NULL AND session_epoch IS NULL AND scope_key='task/'||task_id::text||'/agent/'||agent_id::text AND (mode<>'task_node' OR node_run_id IS NOT NULL))),
 CHECK((entered_at IS NULL)=(wake_at IS NULL)),
 CHECK(state<>'sleeping' OR entered_at IS NOT NULL),
 CHECK(state<>'requested' OR entered_at IS NULL),
 CHECK((state IN ('woken','cancelled'))=(ended_at IS NOT NULL)),
 CHECK((ended_at IS NULL)=(end_reason IS NULL)),
 CHECK((state='woken')=(wake_run_id IS NOT NULL))
);
CREATE UNIQUE INDEX agent_sleeps_one_active ON agent_sleeps(scope_key) WHERE state IN ('requested','sleeping');
CREATE INDEX agent_sleeps_due ON agent_sleeps(wake_at,id) WHERE state='sleeping';
ALTER TABLE runs DROP CONSTRAINT runs_origin_check;
ALTER TABLE runs ADD CONSTRAINT runs_origin_check CHECK(origin IN ('console','inbox_reminder','sleep_wake'));
ALTER TABLE runs ADD COLUMN wake_sleep_id uuid REFERENCES agent_sleeps(id);
ALTER TABLE runs ADD CONSTRAINT runs_sleep_wake_reference CHECK(origin<>'sleep_wake' OR wake_sleep_id IS NOT NULL);
ALTER TABLE task_turns DROP CONSTRAINT task_turns_origin_check;
ALTER TABLE task_turns ADD CONSTRAINT task_turns_origin_check CHECK(origin IN ('task_node','inbox_reminder','sleep_wake'));
ALTER TABLE task_turns ADD COLUMN wake_sleep_id uuid REFERENCES agent_sleeps(id);
ALTER TABLE task_turns ADD COLUMN execution_mode text NOT NULL DEFAULT '' CHECK(execution_mode IN ('','task_node','task_conversation'));
ALTER TABLE task_turns ADD CONSTRAINT task_turns_sleep_wake_reference CHECK(origin<>'sleep_wake' OR (wake_sleep_id IS NOT NULL AND trigger_inbox_message_id IS NULL));
CREATE UNIQUE INDEX runs_one_sleep_carrier ON runs(wake_sleep_id) WHERE wake_sleep_id IS NOT NULL AND status IN ('queued','starting','running','aborting');
CREATE UNIQUE INDEX task_turns_one_sleep_carrier ON task_turns(wake_sleep_id) WHERE wake_sleep_id IS NOT NULL AND status IN ('queued','starting','running','aborting');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM agent_sleeps) OR EXISTS(SELECT 1 FROM runs WHERE origin='sleep_wake') OR EXISTS(SELECT 1 FROM task_turns WHERE origin='sleep_wake') THEN
  RAISE EXCEPTION '011 contains sleep history; restore a reviewed consistent backup instead of destructive Down';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE task_turns DROP COLUMN execution_mode;
ALTER TABLE task_turns DROP COLUMN wake_sleep_id;
ALTER TABLE task_turns DROP CONSTRAINT task_turns_origin_check;
ALTER TABLE task_turns ADD CONSTRAINT task_turns_origin_check CHECK(origin IN ('task_node','inbox_reminder'));
ALTER TABLE runs DROP COLUMN wake_sleep_id;
ALTER TABLE runs DROP CONSTRAINT runs_origin_check;
ALTER TABLE runs ADD CONSTRAINT runs_origin_check CHECK(origin IN ('console','inbox_reminder'));
DROP TABLE agent_sleeps;
ALTER TABLE runs DROP CONSTRAINT runs_end_reason_check;
ALTER TABLE runs ADD CONSTRAINT runs_end_reason_check CHECK(end_reason IN ('user_interrupt','server_restart'));
ALTER TABLE task_instances DROP COLUMN sleep_revision;
ALTER TABLE agent_sessions DROP COLUMN sleep_revision;
