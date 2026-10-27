-- +goose Up
-- 插件作为发件方的线索里可以提问（plugins/SPEC.md §11）：channel.outbound 带 isQuestion 与 question，形状与 identity.inbound 相同。
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION plugin_on_inbox_message() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
 t text;
 at timestamptz;
 agent uuid;
 runtime uuid;
 target plugin_parties;
 ev text;
 mgr identity_managers;
 qrow inbox_questions;
 exclude text;
BEGIN
 -- 发给代管身份的消息以 identity.inbound 只送给代管的插件（plugins/SPEC.md §12），不看订阅与项目启用；
 -- 这个插件不再收到这条消息的 inbox.received。触发器在提交时运行，提问的选项与期限已经写入。
 IF TG_OP = 'INSERT' AND NEW.to_kind = 'external' AND NOT (SELECT disabled FROM plugin_settings) THEN
  SELECT * INTO mgr FROM identity_managers WHERE identity_id = NEW.to_identity_id;
  IF mgr.plugin_id IS NOT NULL THEN
   exclude := mgr.plugin_id;
   SELECT * INTO qrow FROM inbox_questions WHERE message_id = NEW.id;
   ev := plugin_ulid();
   INSERT INTO plugin_events(id, type, at, project_id, agent_id, task_id, run_id, data)
    VALUES (ev, 'identity.inbound', NEW.created_at,
     coalesce((SELECT project_id FROM agents WHERE id = NEW.from_agent_id),
      (SELECT r.project_id FROM task_instances ti JOIN task_records r ON r.id = ti.task_id WHERE ti.id = NEW.from_runtime_id)),
     NEW.from_agent_id, (SELECT task_id FROM task_instances WHERE id = NEW.from_runtime_id), NULL,
     jsonb_build_object('identityId', NEW.to_identity_id, 'channelId', mgr.channel_id, 'messageId', NEW.id, 'threadId', NEW.thread_id,
      'from', jsonb_build_object('kind', NEW.from_kind, 'agentId', NEW.from_agent_id, 'name', CASE NEW.from_kind
        WHEN 'agent' THEN (SELECT name FROM agents WHERE id = NEW.from_agent_id)
        WHEN 'external' THEN (SELECT name FROM external_identities WHERE id = NEW.from_identity_id)
        WHEN 'plugin' THEN coalesce(NEW.plugin_sender_name, (SELECT display_name FROM plugin_parties WHERE id = NEW.from_plugin_party_id))
        WHEN 'console' THEN '控制台' ELSE '平台' END),
      'projectId', coalesce((SELECT project_id FROM agents WHERE id = NEW.from_agent_id),
       (SELECT r.project_id FROM task_instances ti JOIN task_records r ON r.id = ti.task_id WHERE ti.id = NEW.from_runtime_id)),
      'taskId', (SELECT task_id FROM task_instances WHERE id = NEW.from_runtime_id),
      'nodeRunId', coalesce((SELECT node_run_id FROM task_questions WHERE question_message_id = NEW.id),
       (SELECT node_run_id FROM task_node_inbox_messages WHERE message_id = NEW.id)),
      'isQuestion', qrow.message_id IS NOT NULL,
      'question', CASE WHEN qrow.message_id IS NULL THEN NULL ELSE
       jsonb_build_object('id', NEW.id, 'options', qrow.options, 'allowText', true, 'deadline', qrow.expires_at) END,
      'excerpt', left(NEW.text, 80)));
   INSERT INTO plugin_event_deliveries(plugin_id, event_id) VALUES (mgr.plugin_id, ev);
   PERFORM pg_notify('loop_now_plugin_events', '');
  END IF;
 END IF;
 -- 发给插件的消息（Agent 回复插件发来的线索）只给那个插件，以 channel.outbound 送达，不看订阅；
 -- Agent 在线索里提问时带上提问的选项与期限（形状与 identity.inbound 相同），插件回复这条消息即作答。
 IF TG_OP = 'INSERT' AND NEW.to_kind = 'plugin' AND NOT (SELECT disabled FROM plugin_settings) THEN
  SELECT * INTO target FROM plugin_parties WHERE id = NEW.to_plugin_party_id;
  IF EXISTS(SELECT 1 FROM plugins WHERE id = target.plugin_id) THEN
   SELECT * INTO qrow FROM inbox_questions WHERE message_id = NEW.id;
   ev := plugin_ulid();
   INSERT INTO plugin_events(id, type, at, project_id, agent_id, task_id, run_id, data)
    VALUES (ev, 'channel.outbound', NEW.created_at, (SELECT project_id FROM agents WHERE id = NEW.from_agent_id), NEW.from_agent_id,
     (SELECT task_id FROM task_instances WHERE id = NEW.from_runtime_id), NULL,
     jsonb_build_object('messageId', NEW.id, 'threadId', NEW.thread_id, 'channelId', NEW.plugin_channel,
      'from', plugin_party(NEW.from_kind, NEW.from_agent_id, NEW.from_identity_id, NEW.from_runtime_id, NULL, NULL),
      'isQuestion', qrow.message_id IS NOT NULL,
      'question', CASE WHEN qrow.message_id IS NULL THEN NULL ELSE
       jsonb_build_object('id', NEW.id, 'options', qrow.options, 'allowText', true, 'deadline', qrow.expires_at) END,
      'excerpt', left(NEW.text, 80)));
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
 PERFORM plugin_emit_excluding(t, at, (SELECT project_id FROM agents WHERE id = agent), agent, (SELECT task_id FROM task_instances WHERE id = runtime), NULL,
  jsonb_build_object('messageId', NEW.id, 'threadId', NEW.thread_id,
   'from', plugin_party(NEW.from_kind, NEW.from_agent_id, NEW.from_identity_id, NEW.from_runtime_id, NEW.from_plugin_party_id, NEW.plugin_channel),
   'to', plugin_party(NEW.to_kind, NEW.to_agent_id, NEW.to_identity_id, NEW.to_runtime_id, NEW.to_plugin_party_id, NEW.plugin_channel),
   'isQuestion', EXISTS(SELECT 1 FROM inbox_questions q WHERE q.message_id = NEW.id), 'excerpt', left(NEW.text, 80)), exclude);
 RETURN NULL;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION plugin_on_inbox_message() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
 t text;
 at timestamptz;
 agent uuid;
 runtime uuid;
 target plugin_parties;
 ev text;
 mgr identity_managers;
 qrow inbox_questions;
 exclude text;
BEGIN
 -- 发给代管身份的消息以 identity.inbound 只送给代管的插件（plugins/SPEC.md §12），不看订阅与项目启用；
 -- 这个插件不再收到这条消息的 inbox.received。触发器在提交时运行，提问的选项与期限已经写入。
 IF TG_OP = 'INSERT' AND NEW.to_kind = 'external' AND NOT (SELECT disabled FROM plugin_settings) THEN
  SELECT * INTO mgr FROM identity_managers WHERE identity_id = NEW.to_identity_id;
  IF mgr.plugin_id IS NOT NULL THEN
   exclude := mgr.plugin_id;
   SELECT * INTO qrow FROM inbox_questions WHERE message_id = NEW.id;
   ev := plugin_ulid();
   INSERT INTO plugin_events(id, type, at, project_id, agent_id, task_id, run_id, data)
    VALUES (ev, 'identity.inbound', NEW.created_at,
     coalesce((SELECT project_id FROM agents WHERE id = NEW.from_agent_id),
      (SELECT r.project_id FROM task_instances ti JOIN task_records r ON r.id = ti.task_id WHERE ti.id = NEW.from_runtime_id)),
     NEW.from_agent_id, (SELECT task_id FROM task_instances WHERE id = NEW.from_runtime_id), NULL,
     jsonb_build_object('identityId', NEW.to_identity_id, 'channelId', mgr.channel_id, 'messageId', NEW.id, 'threadId', NEW.thread_id,
      'from', jsonb_build_object('kind', NEW.from_kind, 'agentId', NEW.from_agent_id, 'name', CASE NEW.from_kind
        WHEN 'agent' THEN (SELECT name FROM agents WHERE id = NEW.from_agent_id)
        WHEN 'external' THEN (SELECT name FROM external_identities WHERE id = NEW.from_identity_id)
        WHEN 'plugin' THEN coalesce(NEW.plugin_sender_name, (SELECT display_name FROM plugin_parties WHERE id = NEW.from_plugin_party_id))
        WHEN 'console' THEN '控制台' ELSE '平台' END),
      'projectId', coalesce((SELECT project_id FROM agents WHERE id = NEW.from_agent_id),
       (SELECT r.project_id FROM task_instances ti JOIN task_records r ON r.id = ti.task_id WHERE ti.id = NEW.from_runtime_id)),
      'taskId', (SELECT task_id FROM task_instances WHERE id = NEW.from_runtime_id),
      'nodeRunId', coalesce((SELECT node_run_id FROM task_questions WHERE question_message_id = NEW.id),
       (SELECT node_run_id FROM task_node_inbox_messages WHERE message_id = NEW.id)),
      'isQuestion', qrow.message_id IS NOT NULL,
      'question', CASE WHEN qrow.message_id IS NULL THEN NULL ELSE
       jsonb_build_object('id', NEW.id, 'options', qrow.options, 'allowText', true, 'deadline', qrow.expires_at) END,
      'excerpt', left(NEW.text, 80)));
   INSERT INTO plugin_event_deliveries(plugin_id, event_id) VALUES (mgr.plugin_id, ev);
   PERFORM pg_notify('loop_now_plugin_events', '');
  END IF;
 END IF;
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
 PERFORM plugin_emit_excluding(t, at, (SELECT project_id FROM agents WHERE id = agent), agent, (SELECT task_id FROM task_instances WHERE id = runtime), NULL,
  jsonb_build_object('messageId', NEW.id, 'threadId', NEW.thread_id,
   'from', plugin_party(NEW.from_kind, NEW.from_agent_id, NEW.from_identity_id, NEW.from_runtime_id, NEW.from_plugin_party_id, NEW.plugin_channel),
   'to', plugin_party(NEW.to_kind, NEW.to_agent_id, NEW.to_identity_id, NEW.to_runtime_id, NEW.to_plugin_party_id, NEW.plugin_channel),
   'isQuestion', EXISTS(SELECT 1 FROM inbox_questions q WHERE q.message_id = NEW.id), 'excerpt', left(NEW.text, 80)), exclude);
 RETURN NULL;
END $$;
-- +goose StatementEnd
