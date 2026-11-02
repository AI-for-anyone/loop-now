-- +goose Up
-- Append-only task overlays; no rewrite/reseal of original task snapshots.
CREATE TABLE task_lark_switch_actions (
 actor_key text NOT NULL,
 action_id uuid NOT NULL,
 task_id uuid NOT NULL REFERENCES task_records(id),
 node_run_id uuid NOT NULL,
 request_hash bytea NOT NULL CHECK(octet_length(request_hash)=32),
 status text NOT NULL CHECK(status IN ('pending','handoff','succeeded','failed')),
 reservation_task_version bigint NOT NULL CHECK(reservation_task_version>0),
 captured_request jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(captured_request)='object'),
 recovery_record jsonb CHECK(recovery_record IS NULL OR jsonb_typeof(recovery_record)='object'),
 receipt jsonb,
 failure_reason text,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 finished_at timestamptz,
 PRIMARY KEY(actor_key,action_id),
 UNIQUE(actor_key,action_id,task_id),
 CHECK((status IN ('succeeded','failed'))=(finished_at IS NOT NULL)),
 CHECK(status<>'succeeded' OR receipt IS NOT NULL)
);
CREATE TABLE task_lark_switch_fences (
 runtime_id uuid PRIMARY KEY,
 task_id uuid NOT NULL,
 agent_id uuid NOT NULL,
 actor_key text NOT NULL,
 action_id uuid NOT NULL,
 member_role text NOT NULL CHECK(member_role IN ('publisher','followup')),
 FOREIGN KEY(runtime_id,task_id,agent_id) REFERENCES task_instances(id,task_id,agent_id),
 FOREIGN KEY(actor_key,action_id,task_id) REFERENCES task_lark_switch_actions(actor_key,action_id,task_id),
 UNIQUE(actor_key,action_id,member_role)
);
CREATE TABLE task_lark_credential_bindings (
 task_id uuid NOT NULL REFERENCES task_records(id),
 revision bigint NOT NULL CHECK(revision>0),
 project_id uuid NOT NULL REFERENCES projects(id),
 account_id uuid NOT NULL,
 version_id uuid NOT NULL,
 app_id text NOT NULL,
 user_id text NOT NULL,
 brand text NOT NULL CHECK(brand IN ('feishu','lark')),
 runtime_variant_version text NOT NULL CHECK(runtime_variant_version<>''),
 actor_key text NOT NULL,
 action_id uuid NOT NULL,
 continuation_node_run_id uuid NOT NULL,
 continuation_turn_id uuid NOT NULL UNIQUE REFERENCES task_turns(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(task_id,revision),
 UNIQUE(actor_key,action_id),
 FOREIGN KEY(account_id,project_id) REFERENCES project_credential_accounts(id,project_id),
 FOREIGN KEY(version_id,account_id) REFERENCES project_cli_versions(id,account_id),
 FOREIGN KEY(actor_key,action_id,task_id) REFERENCES task_lark_switch_actions(actor_key,action_id,task_id)
);
CREATE TABLE task_lark_binding_members (
 task_id uuid NOT NULL,
 revision bigint NOT NULL,
 runtime_id uuid NOT NULL,
 agent_id uuid NOT NULL,
 member_role text NOT NULL CHECK(member_role IN ('publisher','followup')),
 previous_revision bigint NOT NULL CHECK(previous_revision>=0 AND previous_revision<revision),
 base_snapshot_sha text NOT NULL CHECK(base_snapshot_sha ~ '^[0-9a-f]{64}$'),
 PRIMARY KEY(task_id,revision,runtime_id),
 UNIQUE(task_id,revision,member_role),
 FOREIGN KEY(task_id,revision) REFERENCES task_lark_credential_bindings(task_id,revision),
 FOREIGN KEY(runtime_id,task_id,agent_id) REFERENCES task_instances(id,task_id,agent_id)
);
CREATE TABLE task_lark_deferred_inbox (
 message_id uuid PRIMARY KEY REFERENCES inbox_messages(id),
 runtime_id uuid NOT NULL,
 task_id uuid NOT NULL,
 actor_key text NOT NULL,
 action_id uuid NOT NULL,
 delivered_at timestamptz,
 FOREIGN KEY(runtime_id,task_id) REFERENCES task_instances(id,task_id),
 FOREIGN KEY(actor_key,action_id,task_id) REFERENCES task_lark_switch_actions(actor_key,action_id,task_id)
);
ALTER TABLE task_turns ADD COLUMN lark_binding_revision bigint NOT NULL DEFAULT 0 CHECK(lark_binding_revision>=0);
ALTER TABLE task_turns ADD COLUMN lark_switch_action_id uuid;
ALTER TABLE task_turns ADD COLUMN lark_submitted boolean NOT NULL DEFAULT false;

-- +goose StatementBegin
CREATE FUNCTION task_lark_binding_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'task_lark_binding_is_append_only';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER task_lark_binding_immutable BEFORE UPDATE OR DELETE ON task_lark_credential_bindings FOR EACH ROW EXECUTE FUNCTION task_lark_binding_immutable();
CREATE TRIGGER task_lark_member_immutable BEFORE UPDATE OR DELETE ON task_lark_binding_members FOR EACH ROW EXECUTE FUNCTION task_lark_binding_immutable();

-- Final publication cannot commit a partial member group or a continuation
-- attached to a different runtime/revision. All checks see final Tx C state.
-- +goose StatementBegin
CREATE FUNCTION task_lark_complete_group() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (SELECT count(*) FROM task_lark_binding_members WHERE task_id=NEW.task_id AND revision=NEW.revision)<>2
 OR NOT EXISTS(SELECT 1 FROM task_lark_binding_members p JOIN task_turns t ON t.id=NEW.continuation_turn_id
   WHERE p.task_id=NEW.task_id AND p.revision=NEW.revision AND p.member_role='publisher'
   AND t.task_id=p.task_id AND t.runtime_id=p.runtime_id AND t.node_run_id=NEW.continuation_node_run_id
   AND t.lark_binding_revision=NEW.revision AND t.lark_switch_action_id=NEW.action_id)
 OR NOT EXISTS(SELECT 1 FROM task_lark_switch_actions a WHERE a.actor_key=NEW.actor_key AND a.action_id=NEW.action_id AND a.task_id=NEW.task_id AND a.status='succeeded') THEN
  RAISE EXCEPTION 'task_lark_switch_requires_complete_published_group';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER task_lark_complete_group AFTER INSERT ON task_lark_credential_bindings DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION task_lark_complete_group();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM task_lark_switch_actions)
 OR EXISTS(SELECT 1 FROM task_lark_credential_bindings)
 OR EXISTS(SELECT 1 FROM task_lark_binding_members)
 OR EXISTS(SELECT 1 FROM task_lark_switch_fences)
 OR EXISTS(SELECT 1 FROM task_lark_deferred_inbox)
 OR EXISTS(SELECT 1 FROM task_turns WHERE lark_binding_revision<>0 OR lark_switch_action_id IS NOT NULL OR lark_submitted) THEN
  RAISE EXCEPTION 'task_lark_switch_down_requires_no_new_data';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE task_lark_deferred_inbox;
DROP TABLE task_lark_binding_members;
DROP TABLE task_lark_credential_bindings;
DROP FUNCTION task_lark_complete_group();
DROP FUNCTION task_lark_binding_immutable();
DROP TABLE task_lark_switch_fences;
DROP TABLE task_lark_switch_actions;
ALTER TABLE task_turns DROP COLUMN lark_switch_action_id;
ALTER TABLE task_turns DROP COLUMN lark_submitted;
ALTER TABLE task_turns DROP COLUMN lark_binding_revision;
