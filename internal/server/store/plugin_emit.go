package store

// 插件事件的生成。PostgreSQL 时由触发器（plugin_on_agent、plugin_on_sleep、plugin_on_lifecycle、plugin_on_inbox_message、
// plugin_on_inbox_question）与 plugin_emit 在写入的同一事务里直接写 plugin_events；SQLite 下捕获触发器只在
// plugin_changes（events.db 的生命周期事件在 plugin_lifecycle_changes）记下“发生了什么”，每个写事务提交前由
// beforeCommit 读出、在同一事务里生成事件并清空。规则与原来的 PL/pgSQL 逐条对应。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
)

// SetPluginEventsCommitted 设置写了插件事件的事务提交后要调用的函数（插件服务把它设为唤醒宿主投递，
// 代替 PostgreSQL 的 LISTEN/NOTIFY）。
func (s *Store) SetPluginEventsCommitted(f func()) { s.pluginWake.Store(&f) }

func (s *Store) pluginEventsCommitted() {
	if f := s.pluginWake.Load(); f != nil && *f != nil {
		(*f)()
	}
}

// pluginEventsWritten 是事务上的标记：这个事务写了插件事件，提交后要唤醒宿主。
type pluginEventsWritten struct{}

type pluginChange struct {
	kind  string
	rowID uuid.UUID
	data  map[string]json.RawMessage
}

// deferred 报告这类变更原来是否由延迟到提交时的触发器处理（Inbox 消息与提问）。
func (c pluginChange) deferred() bool {
	return strings.HasPrefix(c.kind, "inbox.") || strings.HasPrefix(c.kind, "question.")
}

// beforeCommit 在每个写事务提交前执行（见 db.SetWriteHook）：把捕获触发器记下的变更变成插件事件。
// 没有变更时只有一次查询。
func (s *Store) beforeCommit(ctx context.Context, tx *db.Tx) error {
	rows, err := tx.Query(ctx, `SELECT 0, ord, kind, row_id, data FROM plugin_changes
		UNION ALL SELECT 1, ord, 'lifecycle', row_id, '{}' FROM plugin_lifecycle_changes ORDER BY 2`)
	if err != nil {
		return err
	}
	var immediate, deferred []pluginChange
	var main, lifecycle int
	for rows.Next() {
		var source, ord int64
		var c pluginChange
		var raw json.RawMessage
		if err := rows.Scan(&source, &ord, &c.kind, &c.rowID, &raw); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal(raw, &c.data); err != nil {
			rows.Close()
			return err
		}
		if source == 1 {
			lifecycle++
		} else {
			main++
		}
		if c.deferred() {
			deferred = append(deferred, c)
		} else {
			immediate = append(immediate, c)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	defer func() {
		if tx.Value(pluginEventsWritten{}) != nil {
			tx.AfterCommit(s.pluginEventsCommitted)
		}
	}()
	if main > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM plugin_changes`); err != nil {
			return err
		}
	}
	if lifecycle > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM plugin_lifecycle_changes`); err != nil {
			return err
		}
	}
	e := &pluginEmitter{tx: tx}
	// 原来的立即触发器在语句结束时执行，延迟触发器（Inbox 消息与提问）在提交时执行：
	// 先按写入顺序处理前者（两个库的变更按 ord 合在一起），再处理后者。
	for _, c := range append(immediate, deferred...) {
		if err := e.change(ctx, c); err != nil {
			return fmt.Errorf("生成插件事件（%s %s）失败：%w", c.kind, c.rowID, err)
		}
	}
	return nil
}

// pluginEmitter 在一个写事务里生成插件事件。
type pluginEmitter struct {
	tx       *db.Tx
	disabled *bool
}

func (e *pluginEmitter) change(ctx context.Context, c pluginChange) error {
	switch c.kind {
	case "agent.created", "agent.updated", "agent.deleted":
		return e.agent(ctx, c)
	case "sleep.requested", "sleep.woken":
		return e.sleep(ctx, c)
	case "inbox.inserted":
		return e.inboxInserted(ctx, c.rowID)
	case "inbox.read", "inbox.cancelled":
		return e.inboxGeneric(ctx, c.rowID, c.kind, e.timeField(c, "at"), "")
	case "question.asked", "question.answered":
		return e.question(ctx, c)
	case "lifecycle":
		return e.lifecycle(ctx, c.rowID)
	}
	return fmt.Errorf("未知的变更类型 %q", c.kind)
}

// ---------- 基础：订阅、启用、写入 ----------

func (e *pluginEmitter) pluginsDisabled(ctx context.Context) (bool, error) {
	if e.disabled == nil {
		var d bool
		if err := e.tx.QueryRow(ctx, `SELECT coalesce((SELECT disabled FROM plugin_settings), 0)`).Scan(&d); err != nil {
			return false, err
		}
		e.disabled = &d
	}
	return *e.disabled, nil
}

// subscribed 对应 plugin_subscribed：有插件在清单里订阅了这类事件。
func (e *pluginEmitter) subscribed(ctx context.Context, typ string) (bool, error) {
	var ok bool
	err := e.tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM plugins p, json_each(p.manifest, '$.events') ev WHERE ev.value = $1)`, typ).Scan(&ok)
	return ok, err
}

type pluginEvent struct {
	typ                       string
	at                        time.Time
	project, agent, task, run *uuid.UUID
	data                      any
}

// emit 对应 plugin_emit_excluding：插件总开关关闭时不写；按订阅与项目启用选出插件（排除 exclude），
// 没有订阅者时不写；否则写事件与每个订阅者的投递记录。
func (e *pluginEmitter) emit(ctx context.Context, ev pluginEvent, exclude string) error {
	if off, err := e.pluginsDisabled(ctx); err != nil || off {
		return err
	}
	var excl *string
	if exclude != "" {
		excl = &exclude
	}
	rows, err := e.tx.Query(ctx, `SELECT p.id FROM plugins p
		WHERE EXISTS (SELECT 1 FROM json_each(p.manifest, '$.events') ev WHERE ev.value = $1) AND p.id IS NOT $2
			AND ($3 IS NULL OR EXISTS (SELECT 1 FROM project_plugins pp WHERE pp.plugin_id = p.id AND pp.project_id = $3 AND pp.enabled))
		ORDER BY p.id`, ev.typ, excl, ev.project)
	if err != nil {
		return err
	}
	subscribers, err := db.CollectRows(rows, db.RowTo[string])
	if err != nil || len(subscribers) == 0 {
		return err
	}
	return e.write(ctx, ev, subscribers)
}

// write 写一条事件与给定插件的投递记录，并在提交后唤醒宿主。
func (e *pluginEmitter) write(ctx context.Context, ev pluginEvent, plugins []string) error {
	id := NewPluginEventID()
	data := ev.data
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := e.tx.Exec(ctx, `INSERT INTO plugin_events (id, type, at, project_id, agent_id, task_id, run_id, data) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, ev.typ, ev.at, ev.project, ev.agent, ev.task, ev.run, json.RawMessage(raw)); err != nil {
		return err
	}
	if _, err := e.tx.Exec(ctx, `INSERT INTO plugin_event_deliveries (plugin_id, event_id, next_attempt_at) SELECT value, $2, $3 FROM json_each($1)`,
		plugins, id, e.tx.Now()); err != nil {
		return err
	}
	e.tx.SetValue(pluginEventsWritten{}, true)
	return nil
}

// ---------- 取数 ----------

func (e *pluginEmitter) agentProject(ctx context.Context, agent *uuid.UUID) (*uuid.UUID, error) {
	if agent == nil {
		return nil, nil
	}
	var p *uuid.UUID
	err := e.tx.QueryRow(ctx, `SELECT project_id FROM agents WHERE id = $1`, *agent).Scan(&p)
	if db.IsNoRows(err) {
		return nil, nil
	}
	return p, err
}

func (e *pluginEmitter) runtimeTask(ctx context.Context, runtime *uuid.UUID) (*uuid.UUID, error) {
	if runtime == nil {
		return nil, nil
	}
	var t *uuid.UUID
	err := e.tx.QueryRow(ctx, `SELECT task_id FROM task_instances WHERE id = $1`, *runtime).Scan(&t)
	if db.IsNoRows(err) {
		return nil, nil
	}
	return t, err
}

// party 对应 plugin_party：消息一方的 kind、id 与地址（Agent 为 组织/项目/名称#任务，外部身份为 @名称，插件为 plugin:id/频道）。
func (e *pluginEmitter) party(ctx context.Context, kind string, agent, identity, runtime, pluginParty *uuid.UUID, channel *string) (map[string]any, error) {
	out := map[string]any{"kind": kind, "id": nil, "address": nil}
	var address *string
	var err error
	switch kind {
	case "agent":
		out["id"] = agent
		if agent == nil {
			break
		}
		err = e.tx.QueryRow(ctx, `SELECT CASE WHEN a.project_id IS NULL THEN a.name ELSE o.name || '/' || pr.name || '/' || a.name END
				|| coalesce('#' || (SELECT ti.task_id FROM task_instances ti WHERE ti.id = $2), '')
			FROM agents a LEFT JOIN projects pr ON pr.id = a.project_id LEFT JOIN organizations o ON o.id = pr.organization_id WHERE a.id = $1`,
			*agent, runtime).Scan(&address)
	case "external":
		out["id"] = identity
		if identity == nil {
			break
		}
		err = e.tx.QueryRow(ctx, `SELECT '@' || name FROM external_identities WHERE id = $1`, *identity).Scan(&address)
	case "plugin":
		if pluginParty == nil {
			break
		}
		err = e.tx.QueryRow(ctx, `SELECT 'plugin:' || plugin_id || coalesce('/' || $2, '') FROM plugin_parties WHERE id = $1`, *pluginParty, channel).Scan(&address)
	}
	if err != nil && !db.IsNoRows(err) {
		return nil, err
	}
	if address != nil {
		out["address"] = *address
	}
	return out, nil
}

func (e *pluginEmitter) timeField(c pluginChange, key string) time.Time {
	var us int64
	if raw, ok := c.data[key]; ok {
		_ = json.Unmarshal(raw, &us)
	}
	if us == 0 {
		return e.tx.Now()
	}
	return time.UnixMicro(us).UTC()
}

func (e *pluginEmitter) idField(c pluginChange, key string) *uuid.UUID {
	var s *string
	if raw, ok := c.data[key]; ok {
		_ = json.Unmarshal(raw, &s)
	}
	if s == nil {
		return nil
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return nil
	}
	return &id
}

// jsonTime 把捕获下来的微秒时间转成事件里的时间值；为空时为 null。
func jsonTime(c pluginChange, key string) any {
	var us *int64
	if raw, ok := c.data[key]; ok {
		_ = json.Unmarshal(raw, &us)
	}
	if us == nil {
		return nil
	}
	return time.UnixMicro(*us).UTC()
}

func excerpt(text string) string {
	if utf8.RuneCountInString(text) <= 80 {
		return text
	}
	r := []rune(text)
	return string(r[:80])
}

// ---------- Agent（plugin_on_agent） ----------

func (e *pluginEmitter) agent(ctx context.Context, c pluginChange) error {
	if ok, err := e.subscribed(ctx, c.kind); err != nil || !ok {
		return err
	}
	var project *uuid.UUID
	if err := e.tx.QueryRow(ctx, `SELECT project_id FROM agents WHERE id = $1`, c.rowID).Scan(&project); err != nil {
		return err
	}
	var name string
	_ = json.Unmarshal(c.data["name"], &name)
	party, err := e.party(ctx, "agent", &c.rowID, nil, nil, nil, nil)
	if err != nil {
		return err
	}
	return e.emit(ctx, pluginEvent{typ: c.kind, at: e.tx.Now(), project: project, agent: &c.rowID,
		data: map[string]any{"name": name, "address": party["address"]}}, "")
}

// ---------- sleep（plugin_on_sleep） ----------

func (e *pluginEmitter) sleep(ctx context.Context, c pluginChange) error {
	if ok, err := e.subscribed(ctx, c.kind); err != nil || !ok {
		return err
	}
	agent := e.idField(c, "agentId")
	project, err := e.agentProject(ctx, agent)
	if err != nil {
		return err
	}
	data := map[string]any{"sleepId": c.rowID, "wakeAt": jsonTime(c, "wakeAt")}
	if c.kind == "sleep.woken" {
		var reason *string
		_ = json.Unmarshal(c.data["endReason"], &reason)
		data["endReason"] = reason
	}
	return e.emit(ctx, pluginEvent{typ: c.kind, at: e.timeField(c, "at"), project: project, agent: agent,
		task: e.idField(c, "taskId"), run: e.idField(c, "runId"), data: data}, "")
}

// ---------- 生命周期（plugin_on_lifecycle） ----------

var lifecyclePluginTypes = map[string]string{
	"run.queued": "run.queued", "run.dispatched": "run.queued", "run.running": "run.started", "run.ended": "run.ended",
	"pi.started": "pi.started", "pi.exited": "pi.exited", "pi.stopped": "pi.exited",
	"model.retry_started": "model.retry", "context.compaction": "context.compacted",
}

func (e *pluginEmitter) lifecycle(ctx context.Context, id uuid.UUID) error {
	var typ string
	var at time.Time
	var agent uuid.UUID
	var runID, task, turn *uuid.UUID
	var raw json.RawMessage
	if err := e.tx.QueryRow(ctx, `SELECT type, at, agent_id, run_id, task_id, task_turn_id, data FROM lifecycle_events WHERE id = $1`, id).
		Scan(&typ, &at, &agent, &runID, &task, &turn, &raw); err != nil {
		return err
	}
	t := lifecyclePluginTypes[typ]
	if t == "" {
		return nil
	}
	if ok, err := e.subscribed(ctx, t); err != nil || !ok {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	value := func(key string) json.RawMessage {
		if v, ok := fields[key]; ok {
			return v
		}
		return json.RawMessage("null")
	}
	// text 对应 data ->> key：字符串取其值，其他 JSON 值取其文本，缺失或 null 为 nil。
	text := func(key string) *string {
		v, ok := fields[key]
		var x any
		if !ok || json.Unmarshal(v, &x) != nil || x == nil {
			return nil
		}
		if str, isStr := x.(string); isStr {
			return &str
		}
		str := strings.TrimSpace(string(v))
		return &str
	}
	var data map[string]any
	switch {
	case strings.HasPrefix(t, "run."):
		var origin *string
		var err error
		if runID != nil {
			err = e.tx.QueryRow(ctx, `SELECT origin FROM runs WHERE id = $1`, *runID).Scan(&origin)
		} else if turn != nil {
			err = e.tx.QueryRow(ctx, `SELECT origin FROM task_turns WHERE id = $1`, *turn).Scan(&origin)
		}
		if err != nil && !db.IsNoRows(err) {
			return err
		}
		data = map[string]any{"origin": origin}
		if t == "run.ended" {
			status := text("status")
			if status == nil {
				status = text("outcome")
			}
			var in, out, read, write, total int64
			if err := e.tx.QueryRow(ctx, `SELECT coalesce(sum(input), 0), coalesce(sum(output), 0), coalesce(sum(cache_read), 0),
					coalesce(sum(cache_write), 0), coalesce(sum(total), 0)
				FROM usage_records u WHERE ($1 IS NOT NULL AND u.run_id = $1) OR ($1 IS NULL AND u.task_turn_id = $2)`, runID, turn).
				Scan(&in, &out, &read, &write, &total); err != nil {
				return err
			}
			data["status"] = status
			data["endReason"] = value("endReason")
			data["errorCode"] = value("errorCode")
			data["tokens"] = map[string]int64{"input": in, "output": out, "cacheRead": read, "cacheWrite": write, "total": total}
		}
	case t == "pi.exited":
		if typ == "pi.exited" {
			data = map[string]any{"reason": "unexpected_exit"}
		} else {
			data = map[string]any{"reason": text("reason")}
		}
	case t == "model.retry":
		data = map[string]any{"attempt": value("attempt"), "delayMs": value("delayMs")}
	default:
		data = map[string]any{"reason": value("reason")}
	}
	project, err := e.agentProject(ctx, &agent)
	if err != nil {
		return err
	}
	run := runID
	if run == nil {
		run = turn
	}
	return e.emit(ctx, pluginEvent{typ: t, at: at, project: project, agent: &agent, task: task, run: run, data: data}, "")
}

// ---------- Inbox 消息（plugin_on_inbox_message） ----------

type inboxEventRow struct {
	id, thread                                       uuid.UUID
	fromKind, toKind                                 string
	fromAgent, fromIdentity, fromRuntime, fromPlugin *uuid.UUID
	toAgent, toIdentity, toRuntime, toPlugin         *uuid.UUID
	senderName, channel                              *string
	text                                             string
	createdAt                                        time.Time
}

func (e *pluginEmitter) inboxRow(ctx context.Context, id uuid.UUID) (inboxEventRow, error) {
	var m inboxEventRow
	err := e.tx.QueryRow(ctx, `SELECT id, thread_id, from_kind, from_agent_id, from_identity_id, from_runtime_id, from_plugin_party_id, plugin_sender_name,
			to_kind, to_agent_id, to_identity_id, to_runtime_id, to_plugin_party_id, plugin_channel, text, created_at
		FROM inbox_messages WHERE id = $1`, id).
		Scan(&m.id, &m.thread, &m.fromKind, &m.fromAgent, &m.fromIdentity, &m.fromRuntime, &m.fromPlugin, &m.senderName,
			&m.toKind, &m.toAgent, &m.toIdentity, &m.toRuntime, &m.toPlugin, &m.channel, &m.text, &m.createdAt)
	return m, err
}

// questionWire 是提问在事件里的形状；没有提问时为 nil。
func (e *pluginEmitter) questionWire(ctx context.Context, id uuid.UUID) (map[string]any, error) {
	var options json.RawMessage
	var deadline *time.Time
	err := e.tx.QueryRow(ctx, `SELECT options, expires_at FROM inbox_questions WHERE message_id = $1`, id).Scan(&options, &deadline)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "options": options, "allowText": true, "deadline": deadline}, nil
}

func (e *pluginEmitter) inboxInserted(ctx context.Context, id uuid.UUID) error {
	m, err := e.inboxRow(ctx, id)
	if err != nil {
		return err
	}
	off, err := e.pluginsDisabled(ctx)
	if err != nil {
		return err
	}
	exclude := ""
	// 发给代管身份的消息以 identity.inbound 只送给代管的插件（plugins/SPEC.md §12），不看订阅与项目启用；
	// 这个插件不再收到这条消息的 inbox.received。
	if m.toKind == "external" && !off && m.toIdentity != nil {
		var plugin, channel *string
		err := e.tx.QueryRow(ctx, `SELECT plugin_id, channel_id FROM identity_managers WHERE identity_id = $1`, *m.toIdentity).Scan(&plugin, &channel)
		if err != nil && !db.IsNoRows(err) {
			return err
		}
		if plugin != nil {
			exclude = *plugin
			if err := e.identityInbound(ctx, m, *plugin, channel); err != nil {
				return err
			}
		}
	}
	// 发给插件的消息（Agent 回复插件发来的线索）只给那个插件，以 channel.outbound 送达，不看订阅。
	if m.toKind == "plugin" && !off && m.toPlugin != nil {
		var plugin string
		err := e.tx.QueryRow(ctx, `SELECT pp.plugin_id FROM plugin_parties pp JOIN plugins p ON p.id = pp.plugin_id WHERE pp.id = $1`, *m.toPlugin).Scan(&plugin)
		if err != nil && !db.IsNoRows(err) {
			return err
		}
		if err == nil {
			if err := e.channelOutbound(ctx, m, plugin); err != nil {
				return err
			}
		}
	}
	return e.inboxGenericRow(ctx, m, "inbox.received", m.createdAt, exclude)
}

func (e *pluginEmitter) fromProjectAndTask(ctx context.Context, m inboxEventRow) (project, task *uuid.UUID, err error) {
	if project, err = e.agentProject(ctx, m.fromAgent); err != nil {
		return nil, nil, err
	}
	if project == nil && m.fromRuntime != nil {
		err = e.tx.QueryRow(ctx, `SELECT r.project_id FROM task_instances ti JOIN task_records r ON r.id = ti.task_id WHERE ti.id = $1`, *m.fromRuntime).Scan(&project)
		if err != nil && !db.IsNoRows(err) {
			return nil, nil, err
		}
	}
	task, err = e.runtimeTask(ctx, m.fromRuntime)
	return project, task, err
}

func (e *pluginEmitter) identityInbound(ctx context.Context, m inboxEventRow, plugin string, channel *string) error {
	project, task, err := e.fromProjectAndTask(ctx, m)
	if err != nil {
		return err
	}
	var name *string
	switch m.fromKind {
	case "agent":
		err = e.tx.QueryRow(ctx, `SELECT name FROM agents WHERE id = $1`, m.fromAgent).Scan(&name)
	case "external":
		err = e.tx.QueryRow(ctx, `SELECT name FROM external_identities WHERE id = $1`, m.fromIdentity).Scan(&name)
	case "plugin":
		name = m.senderName
		if name == nil {
			err = e.tx.QueryRow(ctx, `SELECT display_name FROM plugin_parties WHERE id = $1`, m.fromPlugin).Scan(&name)
		}
	case "console":
		v := "控制台"
		name = &v
	default:
		v := "平台"
		name = &v
	}
	if err != nil && !db.IsNoRows(err) {
		return err
	}
	var node *uuid.UUID
	err = e.tx.QueryRow(ctx, `SELECT coalesce((SELECT node_run_id FROM task_questions WHERE question_message_id = $1),
		(SELECT node_run_id FROM task_node_inbox_messages WHERE message_id = $1))`, m.id).Scan(&node)
	if err != nil {
		return err
	}
	q, err := e.questionWire(ctx, m.id)
	if err != nil {
		return err
	}
	data := map[string]any{"identityId": m.toIdentity, "channelId": channel, "messageId": m.id, "threadId": m.thread,
		"from": map[string]any{"kind": m.fromKind, "agentId": m.fromAgent, "name": name}, "projectId": project, "taskId": task,
		"nodeRunId": node, "isQuestion": q != nil, "question": q, "excerpt": excerpt(m.text)}
	return e.write(ctx, pluginEvent{typ: "identity.inbound", at: m.createdAt, project: project, agent: m.fromAgent, task: task, data: data}, []string{plugin})
}

func (e *pluginEmitter) channelOutbound(ctx context.Context, m inboxEventRow, plugin string) error {
	project, err := e.agentProject(ctx, m.fromAgent)
	if err != nil {
		return err
	}
	task, err := e.runtimeTask(ctx, m.fromRuntime)
	if err != nil {
		return err
	}
	from, err := e.party(ctx, m.fromKind, m.fromAgent, m.fromIdentity, m.fromRuntime, nil, nil)
	if err != nil {
		return err
	}
	q, err := e.questionWire(ctx, m.id)
	if err != nil {
		return err
	}
	data := map[string]any{"messageId": m.id, "threadId": m.thread, "channelId": m.channel, "from": from,
		"isQuestion": q != nil, "question": q, "excerpt": excerpt(m.text)}
	return e.write(ctx, pluginEvent{typ: "channel.outbound", at: m.createdAt, project: project, agent: m.fromAgent, task: task, data: data}, []string{plugin})
}

func (e *pluginEmitter) inboxGeneric(ctx context.Context, id uuid.UUID, typ string, at time.Time, exclude string) error {
	m, err := e.inboxRow(ctx, id)
	if err != nil {
		return err
	}
	return e.inboxGenericRow(ctx, m, typ, at, exclude)
}

// inboxGenericRow 是 inbox.received / inbox.read / inbox.cancelled：按订阅送达，归属取收件或发件的 Agent。
func (e *pluginEmitter) inboxGenericRow(ctx context.Context, m inboxEventRow, typ string, at time.Time, exclude string) error {
	if ok, err := e.subscribed(ctx, typ); err != nil || !ok {
		return err
	}
	var agent, runtime *uuid.UUID
	switch {
	case m.toKind == "agent":
		agent, runtime = m.toAgent, m.toRuntime
	case m.fromKind == "agent":
		agent, runtime = m.fromAgent, m.fromRuntime
	}
	project, err := e.agentProject(ctx, agent)
	if err != nil {
		return err
	}
	task, err := e.runtimeTask(ctx, runtime)
	if err != nil {
		return err
	}
	from, err := e.party(ctx, m.fromKind, m.fromAgent, m.fromIdentity, m.fromRuntime, m.fromPlugin, m.channel)
	if err != nil {
		return err
	}
	to, err := e.party(ctx, m.toKind, m.toAgent, m.toIdentity, m.toRuntime, m.toPlugin, m.channel)
	if err != nil {
		return err
	}
	var isQuestion bool
	if err := e.tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM inbox_questions WHERE message_id = $1)`, m.id).Scan(&isQuestion); err != nil {
		return err
	}
	data := map[string]any{"messageId": m.id, "threadId": m.thread, "from": from, "to": to, "isQuestion": isQuestion, "excerpt": excerpt(m.text)}
	return e.emit(ctx, pluginEvent{typ: typ, at: at, project: project, agent: agent, task: task, data: data}, exclude)
}

// ---------- 提问（plugin_on_inbox_question） ----------

func (e *pluginEmitter) question(ctx context.Context, c pluginChange) error {
	if ok, err := e.subscribed(ctx, c.kind); err != nil || !ok {
		return err
	}
	m, err := e.inboxRow(ctx, c.rowID)
	if err != nil {
		return err
	}
	at := m.createdAt
	if c.kind == "question.answered" {
		if v := jsonTime(c, "at"); v != nil {
			at = v.(time.Time)
		}
	}
	var agent, runtime *uuid.UUID
	switch {
	case m.fromKind == "agent":
		agent, runtime = m.fromAgent, m.fromRuntime
	case m.toKind == "agent":
		agent, runtime = m.toAgent, m.toRuntime
	}
	project, err := e.agentProject(ctx, agent)
	if err != nil {
		return err
	}
	task, err := e.runtimeTask(ctx, runtime)
	if err != nil {
		return err
	}
	from, err := e.party(ctx, m.fromKind, m.fromAgent, m.fromIdentity, m.fromRuntime, m.fromPlugin, m.channel)
	if err != nil {
		return err
	}
	to, err := e.party(ctx, m.toKind, m.toAgent, m.toIdentity, m.toRuntime, m.toPlugin, m.channel)
	if err != nil {
		return err
	}
	data := map[string]any{"messageId": m.id, "threadId": m.thread, "from": from, "to": to}
	if c.kind == "question.answered" {
		var option *string
		_ = json.Unmarshal(c.data["optionId"], &option)
		data["optionId"] = option
	}
	return e.emit(ctx, pluginEvent{typ: c.kind, at: at, project: project, agent: agent, task: task, data: data}, "")
}

// ---------- 代码直接发出的观察类事件 ----------

// emitPluginEventTx 在当前写事务里写一条观察类事件（按订阅与启用生成投递，原 plugin_emit）。
func emitPluginEventTx(ctx context.Context, tx *db.Tx, typ string, project, agent, task, run *uuid.UUID, data map[string]any) error {
	e := &pluginEmitter{tx: tx}
	return e.emit(ctx, pluginEvent{typ: typ, at: tx.Now(), project: project, agent: agent, task: task, run: run, data: data}, "")
}

// EmitPluginEvent 写一条观察类事件（事务外的改动，例如文件夹变化）。
func (s *Store) EmitPluginEvent(ctx context.Context, typ string, project, agent, task, run *uuid.UUID, data map[string]any) error {
	return s.inTx(ctx, func(tx *db.Tx) error { return emitPluginEventTx(ctx, tx, typ, project, agent, task, run, data) })
}
