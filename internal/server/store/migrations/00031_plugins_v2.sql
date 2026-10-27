-- +goose Up
-- 插件系统第二版（plugins/SPEC.md §8）。第一版（022）与更早的候选（005）的插件表整体删除，内容不迁移。
-- 005 加的 agent_credentials.template_source 仍被 CLI 凭证模板使用，保留。
-- 006 的通道列与通道表没有代码使用，一并删除；渠道在第三阶段另建自己的表。
DROP TABLE platform_plugin_state;
DROP TABLE task_plugin_versions;
DROP TABLE platform_plugin_account_versions;
DROP TABLE platform_plugin_accounts;
DROP TABLE agent_plugin_grants;
DROP TABLE platform_plugin_config_versions;
DROP TABLE project_platform_plugins;
DROP TABLE platform_plugin_versions;
DROP TABLE platform_plugins;
ALTER TABLE task_records DROP COLUMN platform_plugins_captured;

DROP TABLE inbox_channel_dedupe;
DROP TABLE inbox_channel_deliveries;
DROP TABLE inbox_channel_threads;
-- 插件作为 Inbox 的一方（SPEC §11）：每个插件一条记录，插件卸载后保留，消息历史仍能显示发件方。
-- 渠道与发件名称记在消息上（plugin_channel、plugin_sender_name，说的是消息中插件那一方）。
CREATE TABLE plugin_parties (
 id uuid PRIMARY KEY,
 plugin_id text NOT NULL UNIQUE,
 display_name text NOT NULL
);
-- 删列会连带删掉引用它的约束，所以先删再按去掉通道、加上插件后的写法重建。
ALTER TABLE inbox_messages
 DROP CONSTRAINT inbox_messages_from_ref, DROP CONSTRAINT inbox_messages_to_ref,
 DROP CONSTRAINT inbox_messages_from_kind_check, DROP CONSTRAINT inbox_messages_to_kind_check,
 DROP COLUMN from_channel_id, DROP COLUMN to_channel_id;
ALTER TABLE inbox_messages
 ADD COLUMN from_plugin_party_id uuid REFERENCES plugin_parties(id),
 ADD COLUMN to_plugin_party_id uuid REFERENCES plugin_parties(id),
 ADD COLUMN plugin_channel text,
 ADD COLUMN plugin_sender_name text,
 ADD CONSTRAINT inbox_messages_plugin_side CHECK ((plugin_channel IS NULL AND plugin_sender_name IS NULL) OR from_kind = 'plugin' OR to_kind = 'plugin'),
 ADD CONSTRAINT inbox_messages_from_kind_check CHECK (from_kind IN ('agent','console','external','platform','plugin')),
 ADD CONSTRAINT inbox_messages_to_kind_check CHECK (to_kind IN ('agent','console','external','plugin')),
 ADD CONSTRAINT inbox_messages_from_ref CHECK (
  (from_kind='agent' AND from_agent_id IS NOT NULL AND from_identity_id IS NULL AND from_plugin_party_id IS NULL)
  OR (from_kind='external' AND from_identity_id IS NOT NULL AND from_agent_id IS NULL AND from_plugin_party_id IS NULL)
  OR (from_kind='plugin' AND from_plugin_party_id IS NOT NULL AND from_agent_id IS NULL AND from_identity_id IS NULL)
  OR (from_kind IN ('console','platform') AND from_agent_id IS NULL AND from_identity_id IS NULL AND from_plugin_party_id IS NULL)),
 ADD CONSTRAINT inbox_messages_to_ref CHECK (
  (to_kind='agent' AND to_agent_id IS NOT NULL AND to_identity_id IS NULL AND to_plugin_party_id IS NULL)
  OR (to_kind='external' AND to_identity_id IS NOT NULL AND to_agent_id IS NULL AND to_plugin_party_id IS NULL)
  OR (to_kind='plugin' AND to_plugin_party_id IS NOT NULL AND to_agent_id IS NULL AND to_identity_id IS NULL)
  OR (to_kind='console' AND to_agent_id IS NULL AND to_identity_id IS NULL AND to_plugin_party_id IS NULL));
CREATE INDEX inbox_to_plugin ON inbox_messages(to_plugin_party_id, created_at DESC, id DESC) WHERE to_kind='plugin';
CREATE INDEX inbox_from_plugin ON inbox_messages(from_plugin_party_id, created_at DESC, id DESC) WHERE from_kind='plugin';
DROP TABLE plugin_template_names;
DROP TABLE plugin_instances;
DROP TABLE plugins;

-- 已安装的插件；version 与 manifest 是当前生效的版本。manifest 用 json 而不是 jsonb：原样保存清单的写法，
-- configSchema 等表单 schema 的属性先后就是表单字段的先后（jsonb 会重排键）。
CREATE TABLE plugins (
 id text PRIMARY KEY CHECK (id ~ '^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$'),
 display_name text NOT NULL,
 source jsonb NOT NULL CHECK (jsonb_typeof(source) = 'object'),
 version text NOT NULL,
 manifest json NOT NULL CHECK (json_typeof(manifest) = 'object'),
 install_dir text NOT NULL,
 status text NOT NULL CHECK (status IN ('installed', 'active', 'error', 'host_stopped')),
 last_error text,
 installed_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE plugin_versions (
 plugin_id text NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
 version text NOT NULL,
 manifest json NOT NULL CHECK (json_typeof(manifest) = 'object'),
 install_dir text NOT NULL,
 digest text NOT NULL,
 installed_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (plugin_id, version)
);
-- project_id 为空的一行是全局值；项目级覆盖一行一项目。secrets 只写不回显。
CREATE TABLE plugin_config (
 plugin_id text NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
 project_id uuid REFERENCES projects(id),
 values jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(values) = 'object'),
 secrets jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(secrets) = 'object'),
 revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
 UNIQUE NULLS NOT DISTINCT (plugin_id, project_id)
);
CREATE TABLE project_plugins (
 project_id uuid NOT NULL REFERENCES projects(id),
 plugin_id text NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
 enabled boolean NOT NULL DEFAULT false,
 PRIMARY KEY (project_id, plugin_id)
);
CREATE TABLE agent_plugin_grants (
 agent_id uuid NOT NULL REFERENCES agents(id),
 plugin_id text NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
 account_ids jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(account_ids) = 'array'),
 PRIMARY KEY (agent_id, plugin_id)
);
CREATE TABLE plugin_accounts (
 id uuid PRIMARY KEY,
 plugin_id text NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
 project_id uuid NOT NULL REFERENCES projects(id),
 kind text NOT NULL,
 name text NOT NULL,
 values jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(values) = 'object'),
 revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
 deleted boolean NOT NULL DEFAULT false
);
CREATE INDEX plugin_accounts_project ON plugin_accounts(project_id, plugin_id, kind) WHERE NOT deleted;
-- 平台半边经宿主读写的持久状态；project_id 为空表示插件全局。
CREATE TABLE plugin_state (
 plugin_id text NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
 project_id uuid REFERENCES projects(id),
 key text NOT NULL CHECK (length(key) BETWEEN 1 AND 512),
 value jsonb NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE NULLS NOT DISTINCT (plugin_id, project_id, key)
);
-- 观察类事件在业务事务提交后写入；id 为 ULID。上下文列不加外键：事件是历史记录。
CREATE TABLE plugin_events (
 id text PRIMARY KEY CHECK (id ~ '^[0-9A-HJKMNP-TV-Z]{26}$'),
 type text NOT NULL,
 at timestamptz NOT NULL,
 project_id uuid,
 agent_id uuid,
 task_id uuid,
 run_id uuid,
 data jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(data) = 'object')
);
CREATE INDEX plugin_events_at ON plugin_events(at, id);
-- 按订阅为每个插件生成一条；宿主 ack 后为 delivered，重试用尽为 failed。
CREATE TABLE plugin_event_deliveries (
 plugin_id text NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
 event_id text NOT NULL REFERENCES plugin_events(id) ON DELETE CASCADE,
 status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'delivered', 'failed')),
 attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 last_error text,
 acked_at timestamptz,
 -- 成功那一次从发出到收到 ack 的毫秒数
 duration_ms bigint CHECK (duration_ms >= 0),
 PRIMARY KEY (plugin_id, event_id)
);
CREATE INDEX plugin_event_deliveries_pending ON plugin_event_deliveries(plugin_id, next_attempt_at) WHERE status = 'pending';
CREATE INDEX plugin_event_deliveries_acked ON plugin_event_deliveries(acked_at) WHERE acked_at IS NOT NULL;
-- lnp_ 令牌：按插件签发，只含清单声明的范围；服务端只存密钥摘要。
CREATE TABLE plugin_tokens (
 id uuid PRIMARY KEY,
 plugin_id text NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
 token_hash bytea NOT NULL,
 scopes jsonb NOT NULL CHECK (jsonb_typeof(scopes) = 'array'),
 created_at timestamptz NOT NULL DEFAULT now(),
 revoked_at timestamptz
);
CREATE INDEX plugin_tokens_live ON plugin_tokens(plugin_id) WHERE revoked_at IS NULL;
-- 宿主 stderr 与插件日志，按插件过滤，保留 7 天。plugin_id 为空是宿主自身的行。
CREATE TABLE plugin_host_log (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 at timestamptz NOT NULL DEFAULT now(),
 plugin_id text,
 level text NOT NULL CHECK (level IN ('debug', 'info', 'warn', 'error')),
 line text NOT NULL
);
CREATE INDEX plugin_host_log_plugin ON plugin_host_log(plugin_id, at DESC);
CREATE INDEX plugin_host_log_at ON plugin_host_log(at);
-- 全局插件设置（单行）：一键停用与额外的市场地址。
CREATE TABLE plugin_settings (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
 disabled boolean NOT NULL DEFAULT false,
 markets jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(markets) = 'array'),
 updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO plugin_settings DEFAULT VALUES;
-- 插件令牌调用控制台接口时，审计的操作者为 plugin。
ALTER TABLE audit_log DROP CONSTRAINT audit_log_actor_kind_check,
 ADD CONSTRAINT audit_log_actor_kind_check CHECK (actor_kind IN ('console', 'external', 'agent', 'cli', 'plugin'));

-- 观察类事件（SPEC §4）：触发器与业务改动在同一事务里写 plugin_events 与投递，提交后 pg_notify 唤醒投递协程。
-- 任务与节点的状态变化由 Go 在任务事务里比较前后两份文档后调用 plugin_emit；folder.changed 与 plugin.* 也由 Go 调用。
-- +goose StatementBegin
CREATE FUNCTION plugin_ulid() RETURNS text LANGUAGE plpgsql VOLATILE AS $$
DECLARE
 b bytea := uuid_send(uuidv7());
 alphabet text := '0123456789ABCDEFGHJKMNPQRSTVWXYZ';
 acc bigint := 0;
 bits int := 2; -- 128 位前补 2 个 0 位，正好 26 组 5 位
 out text := '';
BEGIN
 FOR i IN 0..15 LOOP
  acc := ((acc << 8) | get_byte(b, i)) & 8191;
  bits := bits + 8;
  WHILE bits >= 5 LOOP
   bits := bits - 5;
   out := out || substr(alphabet, ((acc >> bits) & 31)::int + 1, 1);
  END LOOP;
 END LOOP;
 RETURN out;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION plugin_party(p_kind text, p_agent uuid, p_identity uuid, p_runtime uuid, p_plugin uuid, p_channel text) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT CASE p_kind
  WHEN 'agent' THEN jsonb_build_object('kind', 'agent', 'id', p_agent, 'address',
   (SELECT CASE WHEN a.project_id IS NULL THEN a.name ELSE o.name || '/' || pr.name || '/' || a.name END
     || coalesce('#' || (SELECT ti.task_id::text FROM task_instances ti WHERE ti.id = p_runtime), '')
    FROM agents a LEFT JOIN projects pr ON pr.id = a.project_id LEFT JOIN organizations o ON o.id = pr.organization_id WHERE a.id = p_agent))
  WHEN 'external' THEN jsonb_build_object('kind', 'external', 'id', p_identity, 'address', (SELECT '@' || name FROM external_identities WHERE id = p_identity))
  WHEN 'plugin' THEN jsonb_build_object('kind', 'plugin', 'id', NULL, 'address',
   (SELECT 'plugin:' || plugin_id || coalesce('/' || p_channel, '') FROM plugin_parties WHERE id = p_plugin))
  ELSE jsonb_build_object('kind', p_kind, 'id', NULL, 'address', NULL)
 END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
-- plugin_emit 为订阅了 p_type 的插件写一条事件与投递：项目内事件只给在该项目启用了插件的，项目为空的给全部订阅者；全局停用时不写。
CREATE FUNCTION plugin_emit(p_type text, p_at timestamptz, p_project uuid, p_agent uuid, p_task uuid, p_run uuid, p_data jsonb) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
 subscribers text[];
 ev text;
BEGIN
 IF (SELECT disabled FROM plugin_settings) THEN
  RETURN;
 END IF;
 SELECT array_agg(p.id ORDER BY p.id) INTO subscribers FROM plugins p
  WHERE p.manifest::jsonb -> 'events' ? p_type
   AND (p_project IS NULL OR EXISTS(SELECT 1 FROM project_plugins pp WHERE pp.plugin_id = p.id AND pp.project_id = p_project AND pp.enabled));
 IF subscribers IS NULL THEN
  RETURN;
 END IF;
 ev := plugin_ulid();
 INSERT INTO plugin_events(id, type, at, project_id, agent_id, task_id, run_id, data)
  VALUES (ev, p_type, p_at, p_project, p_agent, p_task, p_run, coalesce(p_data, '{}'::jsonb));
 INSERT INTO plugin_event_deliveries(plugin_id, event_id) SELECT unnest(subscribers), ev;
 PERFORM pg_notify('loop_now_plugin_events', '');
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
-- plugin_subscribed 是触发器的快速判断：没有插件订阅时不做别的查询。
CREATE FUNCTION plugin_subscribed(p_type text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM plugins WHERE manifest::jsonb -> 'events' ? p_type)
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION plugin_on_lifecycle() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
 t text;
 d jsonb;
 run uuid := coalesce(NEW.run_id, NEW.task_turn_id);
 origin text;
BEGIN
 t := CASE NEW.type
  WHEN 'run.queued' THEN 'run.queued' WHEN 'run.dispatched' THEN 'run.queued' WHEN 'run.running' THEN 'run.started' WHEN 'run.ended' THEN 'run.ended'
  WHEN 'pi.started' THEN 'pi.started' WHEN 'pi.exited' THEN 'pi.exited' WHEN 'pi.stopped' THEN 'pi.exited'
  WHEN 'model.retry_started' THEN 'model.retry' WHEN 'context.compaction' THEN 'context.compacted' END;
 IF t IS NULL OR NOT plugin_subscribed(t) THEN
  RETURN NULL;
 END IF;
 IF t LIKE 'run.%' THEN
  IF NEW.run_id IS NOT NULL THEN
   SELECT r.origin INTO origin FROM runs r WHERE r.id = NEW.run_id;
  ELSE
   SELECT tt.origin INTO origin FROM task_turns tt WHERE tt.id = NEW.task_turn_id;
  END IF;
  d := jsonb_build_object('origin', origin);
  IF t = 'run.ended' THEN
   d := d || jsonb_build_object('status', coalesce(NEW.data ->> 'status', NEW.data ->> 'outcome'),
    'endReason', coalesce(NEW.data -> 'endReason', 'null'), 'errorCode', coalesce(NEW.data -> 'errorCode', 'null'),
    'tokens', (SELECT jsonb_build_object('input', coalesce(sum(u.input), 0), 'output', coalesce(sum(u.output), 0), 'cacheRead', coalesce(sum(u.cache_read), 0),
       'cacheWrite', coalesce(sum(u.cache_write), 0), 'total', coalesce(sum(u.total), 0))
      FROM usage_records u WHERE (NEW.run_id IS NOT NULL AND u.run_id = NEW.run_id) OR (NEW.run_id IS NULL AND u.task_turn_id = NEW.task_turn_id)));
  END IF;
 ELSIF t = 'pi.exited' THEN
  d := jsonb_build_object('reason', CASE WHEN NEW.type = 'pi.exited' THEN 'unexpected_exit' ELSE NEW.data ->> 'reason' END);
 ELSIF t = 'model.retry' THEN
  d := jsonb_build_object('attempt', coalesce(NEW.data -> 'attempt', 'null'), 'delayMs', coalesce(NEW.data -> 'delayMs', 'null'));
 ELSE
  d := jsonb_build_object('reason', coalesce(NEW.data -> 'reason', 'null'));
 END IF;
 PERFORM plugin_emit(t, NEW.at, (SELECT project_id FROM agents WHERE id = NEW.agent_id), NEW.agent_id, NEW.task_id, run, d);
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER plugin_lifecycle_events AFTER INSERT ON lifecycle_events FOR EACH ROW EXECUTE FUNCTION plugin_on_lifecycle();
-- +goose StatementBegin
-- 收件事件在提交时触发（可延迟的约束触发器）：这时同一事务里的提问记录已经写入。
CREATE FUNCTION plugin_on_inbox_message() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
 t text;
 at timestamptz;
 agent uuid;
 runtime uuid;
 target plugin_parties;
 ev text;
BEGIN
 -- 发给插件的消息（Agent 回复插件发来的线索）只给那个插件，以 channel.outbound 送达，不看订阅。
 IF TG_OP = 'INSERT' AND NEW.to_kind = 'plugin' AND NOT (SELECT disabled FROM plugin_settings) THEN
  SELECT * INTO target FROM plugin_parties WHERE id = NEW.to_plugin_party_id;
  IF EXISTS(SELECT 1 FROM plugins WHERE id = target.plugin_id) THEN
   ev := plugin_ulid();
   INSERT INTO plugin_events(id, type, at, project_id, agent_id, task_id, run_id, data)
    VALUES (ev, 'channel.outbound', NEW.created_at, (SELECT project_id FROM agents WHERE id = NEW.from_agent_id), NEW.from_agent_id,
     (SELECT task_id FROM task_instances WHERE id = NEW.from_runtime_id), NULL,
     jsonb_build_object('messageId', NEW.id, 'threadId', NEW.thread_id, 'channelId', NEW.plugin_channel,
      'from', plugin_party(NEW.from_kind, NEW.from_agent_id, NEW.from_identity_id, NEW.from_runtime_id, NULL, NULL), 'excerpt', left(NEW.text, 80)));
   INSERT INTO plugin_event_deliveries(plugin_id, event_id) VALUES (target.plugin_id, ev);
   PERFORM pg_notify('loop_now_plugin_events', '');
  END IF;
 END IF;
 IF TG_OP = 'INSERT' THEN
  t := 'inbox.received'; at := NEW.created_at;
 ELSIF OLD.read_at IS NULL AND NEW.read_at IS NOT NULL THEN
  t := 'inbox.read'; at := NEW.read_at;
 ELSIF OLD.cancelled_at IS NULL AND NEW.cancelled_at IS NOT NULL THEN
  t := 'inbox.cancelled'; at := NEW.cancelled_at;
 ELSE
  RETURN NULL;
 END IF;
 IF NOT plugin_subscribed(t) THEN
  RETURN NULL;
 END IF;
 IF NEW.to_kind = 'agent' THEN
  agent := NEW.to_agent_id; runtime := NEW.to_runtime_id;
 ELSIF NEW.from_kind = 'agent' THEN
  agent := NEW.from_agent_id; runtime := NEW.from_runtime_id;
 END IF;
 PERFORM plugin_emit(t, at, (SELECT project_id FROM agents WHERE id = agent), agent, (SELECT task_id FROM task_instances WHERE id = runtime), NULL,
  jsonb_build_object('messageId', NEW.id, 'threadId', NEW.thread_id,
   'from', plugin_party(NEW.from_kind, NEW.from_agent_id, NEW.from_identity_id, NEW.from_runtime_id, NEW.from_plugin_party_id, NEW.plugin_channel),
   'to', plugin_party(NEW.to_kind, NEW.to_agent_id, NEW.to_identity_id, NEW.to_runtime_id, NEW.to_plugin_party_id, NEW.plugin_channel),
   'isQuestion', EXISTS(SELECT 1 FROM inbox_questions q WHERE q.message_id = NEW.id), 'excerpt', left(NEW.text, 80)));
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER plugin_inbox_messages AFTER INSERT OR UPDATE ON inbox_messages DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION plugin_on_inbox_message();
-- +goose StatementBegin
CREATE FUNCTION plugin_on_inbox_question() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
 t text;
 at timestamptz;
 m inbox_messages;
 agent uuid;
 runtime uuid;
 d jsonb;
BEGIN
 IF TG_OP = 'INSERT' THEN
  t := 'question.asked';
 ELSIF OLD.state <> 'answered' AND NEW.state = 'answered' THEN
  t := 'question.answered'; at := NEW.answered_at;
 ELSE
  RETURN NULL;
 END IF;
 IF NOT plugin_subscribed(t) THEN
  RETURN NULL;
 END IF;
 SELECT * INTO m FROM inbox_messages WHERE id = NEW.message_id;
 at := coalesce(at, m.created_at);
 IF m.from_kind = 'agent' THEN
  agent := m.from_agent_id; runtime := m.from_runtime_id;
 ELSIF m.to_kind = 'agent' THEN
  agent := m.to_agent_id; runtime := m.to_runtime_id;
 END IF;
 d := jsonb_build_object('messageId', m.id, 'threadId', m.thread_id,
  'from', plugin_party(m.from_kind, m.from_agent_id, m.from_identity_id, m.from_runtime_id, m.from_plugin_party_id, m.plugin_channel),
  'to', plugin_party(m.to_kind, m.to_agent_id, m.to_identity_id, m.to_runtime_id, m.to_plugin_party_id, m.plugin_channel));
 IF t = 'question.answered' THEN
  d := d || jsonb_build_object('optionId', NEW.answer_option_id);
 END IF;
 PERFORM plugin_emit(t, at, (SELECT project_id FROM agents WHERE id = agent), agent, (SELECT task_id FROM task_instances WHERE id = runtime), NULL, d);
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER plugin_inbox_questions AFTER INSERT OR UPDATE ON inbox_questions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION plugin_on_inbox_question();
-- +goose StatementBegin
CREATE FUNCTION plugin_on_sleep() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
 t text;
 d jsonb;
BEGIN
 IF TG_OP = 'INSERT' THEN
  t := 'sleep.requested';
  d := jsonb_build_object('sleepId', NEW.id, 'wakeAt', NEW.wake_at);
 ELSIF OLD.state <> 'woken' AND NEW.state = 'woken' THEN
  t := 'sleep.woken';
  d := jsonb_build_object('sleepId', NEW.id, 'wakeAt', NEW.wake_at, 'endReason', NEW.end_reason);
 ELSE
  RETURN NULL;
 END IF;
 IF NOT plugin_subscribed(t) THEN
  RETURN NULL;
 END IF;
 PERFORM plugin_emit(t, CASE WHEN TG_OP = 'INSERT' THEN NEW.requested_at ELSE NEW.ended_at END,
  (SELECT project_id FROM agents WHERE id = NEW.agent_id), NEW.agent_id, NEW.task_id,
  CASE WHEN TG_OP = 'INSERT' THEN NEW.source_run_id ELSE NEW.wake_run_id END, d);
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER plugin_agent_sleeps AFTER INSERT OR UPDATE ON agent_sleeps FOR EACH ROW EXECUTE FUNCTION plugin_on_sleep();
-- +goose StatementBegin
CREATE FUNCTION plugin_on_agent() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
 t text;
BEGIN
 IF TG_OP = 'INSERT' THEN
  t := 'agent.created';
 ELSIF OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL THEN
  t := 'agent.deleted';
 ELSIF NEW.deleted_at IS NULL AND (NEW.config_version <> OLD.config_version OR NEW.name <> OLD.name) THEN
  t := 'agent.updated';
 ELSE
  RETURN NULL;
 END IF;
 IF NOT plugin_subscribed(t) THEN
  RETURN NULL;
 END IF;
 PERFORM plugin_emit(t, now(), NEW.project_id, NEW.id, NULL, NULL,
  jsonb_build_object('name', NEW.name, 'address', plugin_party('agent', NEW.id, NULL, NULL, NULL, NULL) -> 'address'));
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER plugin_agents AFTER INSERT OR UPDATE ON agents FOR EACH ROW EXECUTE FUNCTION plugin_on_agent();

-- Pi 扩展的对话（extension_ui_request 的 confirm、select、input）作为 Agent 发给控制台的提问（plugins/SPEC.md §11）。
-- 这类提问的回答直接交还给 Pi，不再作为消息提醒 Agent；到期也不给 Agent 发到期通知。
CREATE TABLE extension_dialogs (
 question_message_id uuid PRIMARY KEY REFERENCES inbox_questions(message_id) ON DELETE CASCADE,
 run_id uuid NOT NULL,
 request_id text NOT NULL,
 method text NOT NULL CHECK (method IN ('select', 'confirm', 'input'))
);

-- +goose Down
-- 只在第二版没有任何插件数据时回退；旧表按 030 时的结构重建为空表，旧数据不会回来。
-- inbox_messages 的两个通道列重建在表尾（列序与 030 不同，没有代码按列序读写）。
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM plugins) OR EXISTS(SELECT 1 FROM plugin_events) OR EXISTS(SELECT 1 FROM plugin_host_log)
  OR EXISTS(SELECT 1 FROM audit_log WHERE actor_kind = 'plugin') OR EXISTS(SELECT 1 FROM plugin_parties)
  OR EXISTS(SELECT 1 FROM plugin_settings WHERE disabled OR markets <> '[]'::jsonb) THEN
  RAISE EXCEPTION 'plugins_v2_down_refused: plugin data exists';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE extension_dialogs;
DROP TRIGGER plugin_agents ON agents;
DROP TRIGGER plugin_agent_sleeps ON agent_sleeps;
DROP TRIGGER plugin_inbox_questions ON inbox_questions;
DROP TRIGGER plugin_inbox_messages ON inbox_messages;
DROP TRIGGER plugin_lifecycle_events ON lifecycle_events;
DROP FUNCTION plugin_on_agent();
DROP FUNCTION plugin_on_sleep();
DROP FUNCTION plugin_on_inbox_question();
DROP FUNCTION plugin_on_inbox_message();
DROP FUNCTION plugin_on_lifecycle();
DROP FUNCTION plugin_subscribed(text);
DROP FUNCTION plugin_emit(text, timestamptz, uuid, uuid, uuid, uuid, jsonb);
DROP FUNCTION plugin_party(text, uuid, uuid, uuid, uuid, text);
DROP FUNCTION plugin_ulid();
ALTER TABLE audit_log DROP CONSTRAINT audit_log_actor_kind_check,
 ADD CONSTRAINT audit_log_actor_kind_check CHECK (actor_kind IN ('console', 'external', 'agent', 'cli'));
DROP TABLE plugin_settings;
DROP TABLE plugin_host_log;
DROP TABLE plugin_tokens;
DROP TABLE plugin_event_deliveries;
DROP TABLE plugin_events;
DROP TABLE plugin_state;
DROP TABLE plugin_accounts;
DROP TABLE agent_plugin_grants;
DROP TABLE project_plugins;
DROP TABLE plugin_config;
DROP TABLE plugin_versions;
DROP TABLE plugins;

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
    secret_fields jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(secret_fields) = 'object'),
    config_version bigint NOT NULL DEFAULT 1 CHECK (config_version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);
CREATE UNIQUE INDEX plugin_instances_live_pair ON plugin_instances(plugin_id,project_id) WHERE deleted_at IS NULL;
CREATE INDEX plugin_instances_project ON plugin_instances(project_id) WHERE deleted_at IS NULL;
CREATE TABLE plugin_template_names (
    name text PRIMARY KEY,
    plugin_id uuid NOT NULL REFERENCES plugins(id),
    plugin_name text NOT NULL,
    removed_at timestamptz
);
ALTER TABLE inbox_messages
 DROP CONSTRAINT inbox_messages_from_ref, DROP CONSTRAINT inbox_messages_to_ref,
 DROP CONSTRAINT inbox_messages_from_kind_check, DROP CONSTRAINT inbox_messages_to_kind_check,
 DROP CONSTRAINT inbox_messages_plugin_side,
 DROP COLUMN from_plugin_party_id, DROP COLUMN to_plugin_party_id, DROP COLUMN plugin_channel, DROP COLUMN plugin_sender_name,
 ADD COLUMN from_channel_id uuid REFERENCES plugin_instances(id),
 ADD COLUMN to_channel_id uuid REFERENCES plugin_instances(id);
ALTER TABLE inbox_messages
    ADD CONSTRAINT inbox_messages_from_kind_check CHECK (from_kind IN ('agent','console','external','channel','platform')),
    ADD CONSTRAINT inbox_messages_to_kind_check CHECK (to_kind IN ('agent','console','external','channel')),
    ADD CONSTRAINT inbox_messages_from_ref CHECK (
        (from_kind='agent' AND from_agent_id IS NOT NULL AND from_identity_id IS NULL AND from_channel_id IS NULL)
        OR (from_kind='external' AND from_identity_id IS NOT NULL AND from_agent_id IS NULL AND from_channel_id IS NULL)
        OR (from_kind='channel' AND from_channel_id IS NOT NULL AND from_agent_id IS NULL AND from_identity_id IS NULL)
        OR (from_kind IN ('console','platform') AND from_agent_id IS NULL AND from_identity_id IS NULL AND from_channel_id IS NULL)),
    ADD CONSTRAINT inbox_messages_to_ref CHECK (
        (to_kind='agent' AND to_agent_id IS NOT NULL AND to_identity_id IS NULL AND to_channel_id IS NULL)
        OR (to_kind='external' AND to_identity_id IS NOT NULL AND to_agent_id IS NULL AND to_channel_id IS NULL)
        OR (to_kind='channel' AND to_channel_id IS NOT NULL AND to_agent_id IS NULL AND to_identity_id IS NULL)
        OR (to_kind='console' AND to_agent_id IS NULL AND to_identity_id IS NULL AND to_channel_id IS NULL));
DROP TABLE plugin_parties;
CREATE INDEX inbox_to_channel ON inbox_messages(to_channel_id,created_at DESC,id DESC) WHERE to_kind='channel';
CREATE INDEX inbox_from_channel ON inbox_messages(from_channel_id,created_at DESC,id DESC) WHERE from_kind='channel';
CREATE TABLE inbox_channel_threads (
    thread_id uuid PRIMARY KEY REFERENCES inbox_messages(id),
    channel_id uuid NOT NULL REFERENCES plugin_instances(id),
    conversation_key text NOT NULL,
    conversation_title text NOT NULL,
    conversation_data jsonb NOT NULL,
    current boolean NOT NULL DEFAULT true,
    task_id uuid,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX inbox_channel_threads_current ON inbox_channel_threads(channel_id,conversation_key) WHERE current;
CREATE TABLE inbox_channel_deliveries (
    id uuid PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('message','question_closed')),
    message_id uuid NOT NULL REFERENCES inbox_messages(id),
    channel_id uuid NOT NULL REFERENCES plugin_instances(id),
    thread_id uuid NOT NULL REFERENCES inbox_channel_threads(thread_id),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','delivered','failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts>=0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error text,
    external_id text,
    notice_state text NOT NULL DEFAULT 'none' CHECK (notice_state IN ('none','pending','sent','skipped')),
    created_at timestamptz NOT NULL DEFAULT now(),
    delivered_at timestamptz,
    expires_at timestamptz NOT NULL
);
CREATE INDEX inbox_channel_deliveries_pending ON inbox_channel_deliveries(thread_id,created_at,id) WHERE status='pending';
CREATE INDEX inbox_channel_deliveries_notice ON inbox_channel_deliveries(id) WHERE notice_state='pending';
CREATE TABLE inbox_channel_dedupe (
    channel_id uuid NOT NULL REFERENCES plugin_instances(id),
    dedupe_key text NOT NULL,
    result jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(channel_id,dedupe_key)
);

ALTER TABLE task_records ADD COLUMN platform_plugins_captured boolean NOT NULL DEFAULT false;
CREATE TABLE platform_plugins (
 id uuid PRIMARY KEY, name text NOT NULL UNIQUE, display_name text NOT NULL,
 description text NOT NULL DEFAULT '', builtin boolean NOT NULL DEFAULT false,
 source jsonb NOT NULL CHECK(jsonb_typeof(source)='object'), version text NOT NULL,
 manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object'),
 package_dir text NOT NULL DEFAULT '', entry text NOT NULL DEFAULT '',
 deleted boolean NOT NULL DEFAULT false, updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE platform_plugin_versions (
 plugin_id uuid NOT NULL REFERENCES platform_plugins(id), version text NOT NULL,
 manifest jsonb NOT NULL, entry text NOT NULL DEFAULT '', package_dir text NOT NULL DEFAULT '',
 host_entry text NOT NULL, package_digest text NOT NULL,
 PRIMARY KEY(plugin_id,version)
);
CREATE TABLE project_platform_plugins (
 project_id uuid NOT NULL REFERENCES projects(id), plugin_id uuid NOT NULL REFERENCES platform_plugins(id),
 enabled boolean NOT NULL DEFAULT false, values jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(values)='object'),
 config_version bigint NOT NULL DEFAULT 1 CHECK(config_version>0), secrets jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(secrets)='object'),
 PRIMARY KEY(project_id,plugin_id)
);
CREATE TABLE platform_plugin_config_versions (
 project_id uuid NOT NULL REFERENCES projects(id),plugin_id uuid NOT NULL REFERENCES platform_plugins(id),version bigint NOT NULL CHECK(version>0),
 values jsonb NOT NULL DEFAULT '{}',secrets jsonb NOT NULL DEFAULT '{}',PRIMARY KEY(project_id,plugin_id,version)
);
CREATE TABLE agent_plugin_grants (
 agent_id uuid NOT NULL REFERENCES agents(id), plugin_id uuid NOT NULL REFERENCES platform_plugins(id),
 account_ids jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(account_ids)='array'), permissions jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(permissions)='array'), resources jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(resources)='array'), PRIMARY KEY(agent_id,plugin_id)
);
CREATE TABLE platform_plugin_accounts (
 id uuid PRIMARY KEY,project_id uuid NOT NULL REFERENCES projects(id),plugin_id uuid NOT NULL REFERENCES platform_plugins(id),
 kind text NOT NULL,name text NOT NULL,revision bigint NOT NULL CHECK(revision>0),version_id uuid NOT NULL,deleted boolean NOT NULL DEFAULT false
);
CREATE TABLE platform_plugin_account_versions (
 id uuid PRIMARY KEY,account_id uuid NOT NULL REFERENCES platform_plugin_accounts(id),project_id uuid NOT NULL REFERENCES projects(id),plugin_id uuid NOT NULL REFERENCES platform_plugins(id),
 kind text NOT NULL,name text NOT NULL,values jsonb NOT NULL,secret_fields jsonb NOT NULL,secret jsonb
);
CREATE TABLE task_plugin_versions (
 task_id uuid NOT NULL REFERENCES task_records(id), plugin_id uuid NOT NULL REFERENCES platform_plugins(id),
 version text NOT NULL,bindings jsonb NOT NULL CHECK(jsonb_typeof(bindings)='array'),PRIMARY KEY(task_id,plugin_id),
 FOREIGN KEY(plugin_id,version) REFERENCES platform_plugin_versions(plugin_id,version)
);
CREATE TABLE platform_plugin_state (
 plugin_id uuid NOT NULL REFERENCES platform_plugins(id), project_id uuid NOT NULL REFERENCES projects(id),
 key text NOT NULL, value jsonb NOT NULL, PRIMARY KEY(plugin_id,project_id,key)
);
