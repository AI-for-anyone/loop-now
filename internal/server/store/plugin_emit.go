package store

// Generating plugin events. Under PostgreSQL, triggers (plugin_on_agent, plugin_on_sleep, plugin_on_lifecycle,
// plugin_on_inbox_message, plugin_on_inbox_question) and plugin_emit wrote plugin_events directly in the same
// transaction as the write; under SQLite, capture triggers only record "what happened" in plugin_changes (lifecycle
// events of events.db go to plugin_lifecycle_changes), and before each write transaction commits, beforeCommit reads
// them, generates the events in the same transaction and clears them. The rules correspond one to one to the
// original PL/pgSQL.

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

// SetPluginEventsCommitted sets the function to call after a transaction that wrote plugin events commits (the plugin
// service sets it to wake the plugin host for delivery, replacing PostgreSQL's LISTEN/NOTIFY).
func (s *Store) SetPluginEventsCommitted(f func()) { s.pluginWake.Store(&f) }

func (s *Store) pluginEventsCommitted() {
	if f := s.pluginWake.Load(); f != nil && *f != nil {
		(*f)()
	}
}

// pluginEventsWritten is a marker on a transaction: this transaction wrote plugin events, so the plugin host must be
// woken after commit.
type pluginEventsWritten struct{}

type pluginChange struct {
	kind  string
	rowID uuid.UUID
	data  map[string]json.RawMessage
}

// deferred reports whether this kind of change was originally handled by a trigger deferred to commit time (Inbox
// messages and questions).
func (c pluginChange) deferred() bool {
	return strings.HasPrefix(c.kind, "inbox.") || strings.HasPrefix(c.kind, "question.")
}

// beforeCommit runs before each write transaction commits (see db.SetWriteHook): it turns the changes recorded by the
// capture triggers into plugin events.
// When there are no changes it costs a single query.
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
	// The original immediate triggers ran at the end of each statement, and the deferred triggers (Inbox messages and
	// questions) at commit: handle the former first in write order (changes from both databases are merged by ord),
	// then the latter.
	for _, c := range append(immediate, deferred...) {
		if err := e.change(ctx, c); err != nil {
			return fmt.Errorf("generate plugin event (%s %s): %w", c.kind, c.rowID, err)
		}
	}
	return nil
}

// pluginEmitter generates plugin events within one write transaction.
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
	return fmt.Errorf("unknown change kind %q", c.kind)
}

// ---------- Basics: subscriptions, enablement, writing ----------

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

// subscribed corresponds to plugin_subscribed: some plugin subscribes to this kind of event in its manifest.
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

// emit corresponds to plugin_emit_excluding: writes nothing when the plugin master switch is off; picks plugins by
// subscription and project enablement (excluding exclude), and writes nothing when there are no subscribers; otherwise
// writes the event and a delivery record for each subscriber.
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

// write writes one event and the delivery records for the given plugins, and wakes the plugin host after commit.
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

// ---------- Fetching data ----------

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

// party corresponds to plugin_party: the kind, id and address of one side of a message (for an Agent
// org/project/name#task, for an external identity @name, for a plugin plugin:id/channel).
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

// jsonTime turns a captured microsecond time into the time value in the event; null when empty.
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

// ---------- Agent (plugin_on_agent) ----------

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

// ---------- sleep (plugin_on_sleep) ----------

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

// ---------- Lifecycle (plugin_on_lifecycle) ----------

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
	// text corresponds to data ->> key: a string yields its value, other JSON values yield their text, and a missing
	// key or null yields nil.
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

// ---------- Inbox messages (plugin_on_inbox_message) ----------

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

// questionWire is the shape of a question in an event; nil when there is no question.
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
	// A message to a managed identity goes as identity.inbound only to the managing plugin (plugins/SPEC.md §12),
	// regardless of subscriptions and project enablement; that plugin no longer receives inbox.received for this
	// message.
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
	// A message to a plugin (an Agent replying to a thread the plugin started) goes only to that plugin, delivered as
	// channel.outbound, regardless of subscriptions.
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
		v := "Console"
		name = &v
	default:
		v := "Platform"
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

// inboxGenericRow handles inbox.received / inbox.read / inbox.cancelled: delivered by subscription, attributed to the
// recipient or sender Agent.
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

// ---------- Questions (plugin_on_inbox_question) ----------

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

// ---------- Observation events emitted directly by code ----------

// emitPluginEventTx writes one observation event in the current write transaction (generating deliveries by
// subscription and enablement; formerly plugin_emit).
func emitPluginEventTx(ctx context.Context, tx *db.Tx, typ string, project, agent, task, run *uuid.UUID, data map[string]any) error {
	e := &pluginEmitter{tx: tx}
	return e.emit(ctx, pluginEvent{typ: typ, at: tx.Now(), project: project, agent: agent, task: task, run: run, data: data}, "")
}

// EmitPluginEvent writes one observation event (for changes outside a transaction, e.g. folder changes).
func (s *Store) EmitPluginEvent(ctx context.Context, typ string, project, agent, task, run *uuid.UUID, data map[string]any) error {
	return s.inTx(ctx, func(tx *db.Tx) error { return emitPluginEventTx(ctx, tx, typ, project, agent, task, run, data) })
}
