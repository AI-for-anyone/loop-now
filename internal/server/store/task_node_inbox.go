package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/messageimage"
	"loop-now/internal/server/naming"
	"loop-now/internal/server/taskmodel"
	"time"
)

type TaskNodeInboxBinding struct {
	MessageID, RootID uuid.UUID
	Target            TaskConsoleLineage
	ReminderID        *uuid.UUID
	Inactive          bool
}

type taskInboxQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *Store) TaskNodeInboxBinding(ctx context.Context, id uuid.UUID) (TaskNodeInboxBinding, bool, error) {
	return s.taskNodeInboxBinding(ctx, s.Pool, id)
}

// partyWire 是 InboxParticipant 的结构（节点 Inbox 与节点排队列表共用）。
func partyWire(v PartyView) map[string]any {
	var id *uuid.UUID
	var addr *string
	name := v.Name
	switch v.Kind {
	case "console":
		a := "@console"
		addr = &a
		name = "控制台"
	case "agent":
		id = &v.ID
		if a, ok := naming.AgentAddress(v.OrgName, v.ProjectName, v.Name, v.Platform); ok {
			if v.TaskID != nil {
				a += "#" + v.TaskID.String()
			}
			addr = &a
		}
		if v.Deleted {
			name = "已删除 Agent"
		}
	case "external":
		id = &v.ID
		a := "@" + v.Name
		addr = &a
		if v.Deleted {
			name = "已删除身份"
		}
	case "plugin":
		a := PluginAddress(v)
		addr = &a
	}
	var plugin, channel *string
	if v.Kind == "plugin" {
		plugin = &v.PluginID
		if v.ChannelID != "" {
			channel = &v.ChannelID
		}
	}
	return map[string]any{"kind": v.Kind, "id": id, "address": addr, "name": name, "deleted": v.Deleted, "pluginId": plugin, "channelId": channel}
}

func TaskInboxMessageWire(m InboxMessage) json.RawMessage {
	var busy *string
	if m.WhenBusy != "" {
		busy = &m.WhenBusy
	}
	v := map[string]any{"id": m.ID, "threadId": m.ThreadID, "inReplyTo": m.InReplyTo, "from": partyWire(m.From), "to": partyWire(m.To), "preview": m.Preview, "whenBusy": busy, "createdAt": m.CreatedAt, "readAt": m.ReadAt, "cancelledAt": m.CancelledAt, "text": m.Text, "task": m.Task}
	if len(m.Images) > 0 {
		v["images"] = m.Images
	}
	if m.Question != nil {
		v["question"] = m.Question
	}
	if m.AnswerTo != nil {
		v["answerTo"] = m.AnswerTo
	}
	if m.Notice != nil {
		v["notice"] = m.Notice
	}
	b, _ := json.Marshal(v)
	return b
}

func (s *Store) TaskNodeInboxBindingTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (TaskNodeInboxBinding, bool, error) {
	return s.taskNodeInboxBinding(ctx, tx, id)
}
func (s *Store) taskNodeInboxBinding(ctx context.Context, tx taskInboxQuerier, id uuid.UUID) (TaskNodeInboxBinding, bool, error) {
	var b TaskNodeInboxBinding
	e := tx.QueryRow(ctx, `SELECT message_id,root_message_id,task_id,node_run_id,agent_id,runtime_id,execution_mode,reminder_run_id,(not_executed_at IS NOT NULL) FROM task_node_inbox_messages WHERE message_id=$1`, id).Scan(&b.MessageID, &b.RootID, &b.Target.TaskID, &b.Target.NodeRunID, &b.Target.AgentID, &b.Target.RuntimeID, &b.Target.ExecutionMode, &b.ReminderID, &b.Inactive)
	if e == pgx.ErrNoRows {
		return b, false, nil
	}
	return b, e == nil, e
}

// TaskNodeInboxAccess runs a strict action under the original task aggregate
// lock. Ordinary Inbox callers receive handled=false and retain old behavior.
func (s *Store) TaskNodeInboxAccess(ctx context.Context, p Party, id, run uuid.UUID, fn func(*TaskMutation, TaskNodeInboxBinding) error) (bool, error) {
	b, found, e := s.TaskNodeInboxBinding(ctx, id)
	if e != nil || !found {
		return found, e
	}
	_, e = s.MutateTask(ctx, b.Target.TaskID, nil, func(m *TaskMutation) (any, error) {
		m.Unchanged()
		if p.Kind != "console" && (p.Kind != "agent" || p.ID != b.Target.AgentID || p.RuntimeID != b.Target.RuntimeID) {
			return nil, apperr.NotFound("消息")
		}
		reason := ""
		var e error
		if p.Kind == "agent" {
			reason, e = s.TaskConsoleGuardTx(ctx, m, b.Target)
		}
		if e != nil {
			return nil, e
		}
		if reason != "" {
			return nil, TaskConsoleConflict(m, b.Target, reason)
		}
		if p.Kind == "agent" {
			if b.Inactive {
				return nil, TaskConsoleConflict(m, b.Target, "chat_unavailable")
			}
			if run == uuid.Nil {
				return nil, apperr.Invalid("runId", "严格节点Inbox需要明确本轮运行ID")
			}
			var valid bool
			if e = m.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_turns WHERE id=$1 AND task_id=$2 AND node_run_id=$3 AND runtime_id=$4 AND execution_mode=$5 AND status='running')`, run, b.Target.TaskID, b.Target.NodeRunID, b.Target.RuntimeID, b.Target.ExecutionMode).Scan(&valid); e != nil {
				return nil, e
			}
			if !valid {
				return nil, TaskConsoleConflict(m, b.Target, "target_mismatch")
			}
		}
		return nil, fn(m, b)
	})
	return true, e
}

func (s *Store) LinkTaskNodeInboxRunTx(ctx context.Context, m *TaskMutation, id, run uuid.UUID, role string) error {
	tag, e := m.Tx.Exec(ctx, `INSERT INTO task_node_inbox_run_links(message_id,run_id,role) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, run, role)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	return s.EmitTaskNodeInboxTx(ctx, m, id)
}

func (s *Store) ReadTaskNodeInbox(ctx context.Context, p Party, id, run uuid.UUID) (InboxMessage, bool, bool, error) {
	var out InboxMessage
	changed := false
	handled, e := s.TaskNodeInboxAccess(ctx, p, id, run, func(m *TaskMutation, b TaskNodeInboxBinding) error {
		var e error
		out, e = scanInbox(m.Tx.QueryRow(ctx, inboxSelect(160)+` WHERE m.id=$1`, id))
		if e != nil {
			return e
		}
		if out.To.Party != p {
			return apperr.NotFound("消息")
		}
		tag, e := m.Tx.Exec(ctx, `UPDATE inbox_messages SET read_at=clock_timestamp() WHERE id=$1 AND read_at IS NULL AND cancelled_at IS NULL`, id)
		if e != nil {
			return e
		}
		changed = tag.RowsAffected() > 0
		if changed {
			if e = s.voidReadTaskTurnTx(ctx, m, id); e != nil {
				return e
			}
		}
		out, e = scanInbox(m.Tx.QueryRow(ctx, inboxSelect(160)+` WHERE m.id=$1`, id))
		if e != nil {
			return e
		}
		if p.Kind == "agent" {
			return s.LinkTaskNodeInboxRunTx(ctx, m, id, run, "read")
		}
		if changed {
			return s.EmitTaskNodeInboxTx(ctx, m, id)
		}
		return nil
	})
	return out, handled, changed, e
}

// Summary visibility is scoped by the persisted target, not the runtime alone.
// Full reads/replies additionally validate the explicit active run under lock.
func taskNodeInboxVisibleSQL(alias string, p Party) string {
	if p.Kind != "agent" || p.RuntimeID == uuid.Nil {
		return "true"
	}
	return `NOT EXISTS(SELECT 1 FROM task_node_inbox_messages nb JOIN task_records nt ON nt.id=nb.task_id WHERE nb.message_id=` + alias + `.id AND (nt.ended_at IS NOT NULL OR nb.not_executed_at IS NOT NULL OR nt.document->'view'->'task'->>'currentNodeRunId' IS DISTINCT FROM nb.node_run_id::text OR (CASE WHEN nt.document->'view'->'nodes' @> jsonb_build_array(jsonb_build_object('id',nb.node_run_id::text,'status','blocked')) THEN 'task_conversation' ELSE 'task_node' END)<>nb.execution_mode))`
}

func (s *Store) TaskNodeInboxPageTx(ctx context.Context, m *TaskMutation, node uuid.UUID, cursor string, limit int) (taskmodel.TaskNodeInboxPage, error) {
	out := taskmodel.TaskNodeInboxPage{Items: []taskmodel.TaskNodeInboxRecord{}}
	if limit < 1 || limit > 100 {
		return out, apperr.Invalid("limit", "必须为1..100")
	}
	var at *time.Time
	var id *uuid.UUID
	if cursor != "" {
		a, b, e := parseTaskCursor(cursor)
		if e != nil {
			return out, e
		}
		at = &a
		id = &b
	}
	var runtime *uuid.UUID
	for _, n := range m.Record.View.Nodes {
		if n.ID == node && n.RuntimeID != uuid.Nil {
			r := n.RuntimeID
			runtime = &r
		}
	}
	// Strict node messages, plus the node runtime's other messages with the
	// console or an external identity (its own sends and questions, their
	// replies and answers) attributed here.
	rows, e := m.Tx.Query(ctx, `SELECT x.id,x.created_at FROM (
 SELECT b.message_id id,i.created_at FROM task_node_inbox_messages b JOIN inbox_messages i ON i.id=b.message_id WHERE b.task_id=$1 AND b.node_run_id=$2
 UNION ALL
 SELECT m.id,m.created_at FROM inbox_messages m WHERE ((m.from_runtime_id=$6 AND m.to_kind IN ('console','external')) OR (m.to_runtime_id=$6 AND m.from_kind IN ('console','external')))
  AND NOT EXISTS(SELECT 1 FROM task_node_inbox_messages nb WHERE nb.message_id=m.id) AND `+inboxNodeSQL("m")+`=$2
) x WHERE ($3::timestamptz IS NULL OR (x.created_at,x.id)>($3,$4)) ORDER BY x.created_at,x.id LIMIT $5`, m.Record.View.Task.ID, node, at, id, limit+1, runtime)
	if e != nil {
		return out, e
	}
	type key struct {
		id uuid.UUID
		at time.Time
	}
	keys, e := pgx.CollectRows(rows, func(r pgx.CollectableRow) (key, error) { var k key; e := r.Scan(&k.id, &k.at); return k, e })
	if e != nil {
		return out, e
	}
	if len(keys) > limit {
		keys = keys[:limit]
		k := keys[len(keys)-1]
		next := taskCursor(k.at, k.id)
		out.NextCursor = &next
	}
	for _, k := range keys {
		_, strict, e := s.TaskNodeInboxBindingTx(ctx, m.Tx, k.id)
		if e != nil {
			return out, e
		}
		var v taskmodel.TaskNodeInboxRecord
		if strict {
			v, e = s.TaskNodeInboxRecordTx(ctx, m.Tx, k.id)
		} else {
			v, e = taskNodeConsoleRecordTx(ctx, m.Tx, k.id)
		}
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	return out, nil
}

// taskNodeConsoleRecordTx projects a non-strict message between a node runtime
// and the console: it has no run links and is never marked not executed.
func taskNodeConsoleRecordTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (taskmodel.TaskNodeInboxRecord, error) {
	m, e := scanInbox(tx.QueryRow(ctx, inboxSelect(160)+` WHERE m.id=$1`, id))
	if e != nil {
		return taskmodel.TaskNodeInboxRecord{}, e
	}
	agent := m.From
	if agent.RuntimeID == uuid.Nil {
		agent = m.To
	}
	out := taskmodel.TaskNodeInboxRecord{AgentID: agent.ID, RuntimeID: agent.RuntimeID, Message: TaskInboxMessageWire(m), RunLinks: []taskmodel.TaskNodeInboxRunLink{}}
	if m.Task != nil && m.Task.NodeRunID != nil {
		out.TaskID, out.NodeRunID = m.Task.TaskID, *m.Task.NodeRunID
	}
	return out, nil
}

func (s *Store) TaskNodeInboxRecordTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (taskmodel.TaskNodeInboxRecord, error) {
	var out taskmodel.TaskNodeInboxRecord
	b, ok, e := s.TaskNodeInboxBindingTx(ctx, tx, id)
	if e != nil {
		return out, e
	}
	if !ok {
		return out, apperr.NotFound("节点Inbox消息")
	}
	m, e := scanInbox(tx.QueryRow(ctx, inboxSelect(160)+` WHERE m.id=$1`, id))
	if e != nil {
		return out, e
	}
	out = taskmodel.TaskNodeInboxRecord{TaskID: b.Target.TaskID, NodeRunID: b.Target.NodeRunID, AgentID: b.Target.AgentID, RuntimeID: b.Target.RuntimeID, Message: TaskInboxMessageWire(m), RunLinks: []taskmodel.TaskNodeInboxRunLink{}}
	var reason *string
	var at *time.Time
	if e = tx.QueryRow(ctx, `SELECT not_executed_reason,not_executed_at FROM task_node_inbox_messages WHERE message_id=$1`, id).Scan(&reason, &at); e != nil {
		return out, e
	}
	if reason != nil && at != nil {
		out.NotExecuted = &taskmodel.TaskNodeInboxNotExecuted{Reason: *reason, At: *at}
	}
	rows, e := tx.Query(ctx, `SELECT run_id,role FROM task_node_inbox_run_links WHERE message_id=$1 ORDER BY run_id,role`, id)
	if e != nil {
		return out, e
	}
	out.RunLinks, e = pgx.CollectRows(rows, func(r pgx.CollectableRow) (taskmodel.TaskNodeInboxRunLink, error) {
		var l taskmodel.TaskNodeInboxRunLink
		e := r.Scan(&l.RunID, &l.Role)
		return l, e
	})
	return out, e
}

func (s *Store) EmitTaskNodeInboxTx(ctx context.Context, m *TaskMutation, id uuid.UUID) error {
	rec, e := s.TaskNodeInboxRecordTx(ctx, m.Tx, id)
	if e != nil {
		return e
	}
	m.Changed()
	return m.Emit("task.inbox.updated", rec)
}

func (s *Store) EnqueueTaskNodeInboxTx(ctx context.Context, m *TaskMutation, target TaskConsoleLineage, text, whenBusy string, imageIDs ...uuid.UUID) (taskmodel.TaskNodeInboxRecord, []uuid.UUID, error) {
	return s.enqueueTaskNodeInboxTx(ctx, m, target, text, whenBusy, nil, imageIDs...)
}
func (s *Store) enqueueTaskNodeInboxTx(ctx context.Context, m *TaskMutation, target TaskConsoleLineage, text, whenBusy string, reply *InboxMessage, imageIDs ...uuid.UUID) (taskmodel.TaskNodeInboxRecord, []uuid.UUID, error) {
	var out taskmodel.TaskNodeInboxRecord
	reason, e := s.TaskConsoleGuardTx(ctx, m, target)
	if e != nil {
		return out, nil, e
	}
	if reason != "" {
		return out, nil, TaskConsoleConflict(m, target, reason)
	}
	mode := "task_node"
	if m.Record.Current().Status == "blocked" {
		mode = "task_conversation"
	}
	target.ExecutionMode = mode
	id := uuid.Must(uuid.NewV7())
	msg := NewInboxMessage{ID: id, From: ConsoleParty, To: Party{Kind: "agent", ID: target.AgentID, RuntimeID: target.RuntimeID}, Text: text, WhenBusy: whenBusy}
	rootID := id
	if reply != nil {
		rootID = reply.ThreadID
		msg.ThreadID = &rootID
		msg.InReplyTo = &reply.ID
	}
	if len(imageIDs) > 0 {
		msg.Images, e = s.lockMessageImagesTx(ctx, m.Tx, ImageOwner{AgentID: target.AgentID, TaskID: target.TaskID, NodeRunID: target.NodeRunID, RuntimeID: target.RuntimeID}, imageIDs, s.ImageMaxCount)
		if e != nil {
			return out, nil, e
		}
	}
	if e = InsertInboxMessage(ctx, m.Tx, msg); e != nil {
		return out, nil, e
	}

	if len(imageIDs) > 0 {
		images, e := s.BindMessageImagesTx(ctx, m.Tx, ImageOwner{AgentID: target.AgentID, TaskID: target.TaskID, NodeRunID: target.NodeRunID, RuntimeID: target.RuntimeID}, id, imageIDs, s.ImageMaxCount)
		if e != nil {
			return out, nil, e
		}
		raw, _ := json.Marshal(images)
		if _, e = m.Tx.Exec(ctx, `UPDATE inbox_messages SET images=$2 WHERE id=$1`, id, raw); e != nil {
			return out, nil, e
		}
	}
	priority := 1
	if whenBusy == "interrupt" {
		priority = 0
	}
	turn, found, e := s.findTaskNodeReminderTx(ctx, m, target, mode, priority, len(imageIDs))
	if e != nil {
		return out, nil, e
	}
	if !found {
		if turn, e = s.newTaskNodeReminderTx(ctx, m, target, mode, priority, id); e != nil {
			return out, nil, e
		}
	}
	if _, e = m.Tx.Exec(ctx, `INSERT INTO task_node_inbox_messages(message_id,root_message_id,task_id,node_run_id,agent_id,runtime_id,execution_mode,reminder_run_id) VALUES($1,$8,$2,$3,$4,$5,$6,$7)`, id, target.TaskID, target.NodeRunID, target.AgentID, target.RuntimeID, mode, turn.ID, rootID); e != nil {
		return out, nil, e
	}
	if e = s.WakeSleepForWorkTx(ctx, m.Tx, m, target.AgentID, target.RuntimeID, turn.ID); e != nil {
		return out, nil, e
	}
	interrupted := []uuid.UUID{}
	if priority == 0 {
		if interrupted, e = s.interruptTaskNodeTx(ctx, m, target); e != nil {
			return out, nil, e
		}
	}
	out, e = s.TaskNodeInboxRecordTx(ctx, m.Tx, id)
	if e != nil {
		return out, nil, e
	}
	e = m.Emit("task.inbox.updated", out)
	return out, interrupted, e
}

const taskNodeReminderPrompt = "你有原任务节点的严格Inbox消息。使用 loopctl inbox list，再用 inbox read <id> --run 本轮运行ID 逐条读取，并用 inbox reply <id> --run 本轮运行ID 回复；普通消息不是原问题答案。只处理原节点，不改投其他节点。"

// findTaskNodeReminderTx returns the earliest queued node notification turn of
// this priority and mode that can still take images more message images.
func (s *Store) findTaskNodeReminderTx(ctx context.Context, m *TaskMutation, target TaskConsoleLineage, mode string, priority, images int) (TaskTurn, bool, error) {
	imageLimit := s.ImageMaxCount
	if imageLimit <= 0 {
		imageLimit = messageimage.DefaultCount
	}
	turn, e := scanTaskTurn(m.Tx.QueryRow(ctx, `SELECT `+taskTurnColumns+` FROM task_turns WHERE runtime_id=$1 AND node_run_id=$2 AND inbox_scope_node_id=$2 AND execution_mode=$3 AND status='queued' AND origin='inbox_reminder' AND priority=$4 AND (SELECT coalesce(sum(jsonb_array_length(im.images)),0) FROM task_node_inbox_messages b JOIN inbox_messages im ON im.id=b.message_id WHERE b.reminder_run_id=task_turns.id)+$5<=$6 ORDER BY queue_seq LIMIT 1`, target.RuntimeID, target.NodeRunID, mode, priority, images, imageLimit))
	if e == pgx.ErrNoRows {
		return TaskTurn{}, false, nil
	}
	return turn, e == nil, e
}

// newTaskNodeReminderTx queues a fresh node notification turn for trigger.
func (s *Store) newTaskNodeReminderTx(ctx context.Context, m *TaskMutation, target TaskConsoleLineage, mode string, priority int, trigger uuid.UUID) (TaskTurn, error) {
	turn, e := scanTaskTurn(m.Tx.QueryRow(ctx, `INSERT INTO task_turns(id,task_id,runtime_id,node_run_id,user_message_id,assistant_message_id,status,origin,priority,text,trigger_inbox_message_id,execution_mode,inbox_scope_node_id,request_id) VALUES($1,$2,$3,$4,$5,$6,'queued','inbox_reminder',$7,$8,$9,$10,$4,$11) RETURNING `+taskTurnColumns, uuid.Must(uuid.NewV7()), target.TaskID, target.RuntimeID, target.NodeRunID, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), priority, taskNodeReminderPrompt, trigger, mode, taskRequestID(ctx, m, "inbox_reminder")))
	if e != nil {
		return turn, e
	}
	if e = s.AttachTaskConsoleLineageTx(ctx, m, turn, target.AgentID, mode); e != nil {
		return turn, e
	}
	if e = s.taskLifecycle(ctx, m.Tx, turn, EventRunQueued, nil); e != nil {
		return turn, e
	}
	m.Record.Current().RunIDs = append(m.Record.Current().RunIDs, turn.ID)
	return turn, m.Emit("message.created", taskMessage(turn, false))
}

// interruptTaskNodeTx marks the node's active turn aborting; callers signal the
// returned turns to the executor after commit.
func (s *Store) interruptTaskNodeTx(ctx context.Context, m *TaskMutation, target TaskConsoleLineage) ([]uuid.UUID, error) {
	interrupted := []uuid.UUID{}
	rows, e := m.Tx.Query(ctx, `UPDATE task_turns SET status='aborting' WHERE task_id=$1 AND runtime_id=$2 AND node_run_id=$3 AND status IN ('starting','running') RETURNING `+taskTurnColumns, target.TaskID, target.RuntimeID, target.NodeRunID)
	if e != nil {
		return nil, e
	}
	old, e := pgx.CollectRows(rows, func(r pgx.CollectableRow) (TaskTurn, error) { return scanTaskTurn(r) })
	if e != nil {
		return nil, e
	}
	for _, t := range old {
		interrupted = append(interrupted, t.ID)
		if e = s.taskAbortEvent(ctx, m, t); e != nil {
			return nil, e
		}
		if e = m.Emit("message.created", taskMessage(t, false)); e != nil {
			return nil, e
		}
	}
	return interrupted, nil
}

// taskNodeMessageConflict 返回节点排队动作的 task_conflict；message 为空时（停止）不带 messageId。
func taskNodeMessageConflict(m *TaskMutation, target TaskConsoleLineage, reason string, message uuid.UUID, run *TaskTurn) error {
	text := map[string]string{"message_read": "这条节点消息已被读取，不在排队中", "message_cancelled": "这条节点消息已被取消",
		"run_not_active": "这一轮回复已经结束，不能再停止"}[reason]
	if text == "" {
		text = "这条节点消息已开始处理或已失效，不在排队中"
	}
	e := apperr.Conflict(apperr.CodeTaskConflict, text)
	d := taskmodel.TaskNodeMessageConflictDetails{TaskID: target.TaskID, ExpectedNodeRunID: target.NodeRunID, CurrentNodeRunID: m.Record.View.Task.CurrentNodeRunID, ExpectedAgentID: target.AgentID, ExpectedRuntimeID: target.RuntimeID, TaskVersion: m.Record.View.Task.Version, Reason: reason}
	if message != uuid.Nil {
		d.MessageID = &message
	}
	if run != nil {
		id, status := run.ID, run.Status
		d.RunID, d.RunStatus = &id, &status
	}
	e.Details = d
	return e
}

// TaskNodeQueueTx lists the node's pending messages one by one, in claim
// order (turn priority, queue_seq; sending order inside a turn).
func (s *Store) TaskNodeQueueTx(ctx context.Context, m *TaskMutation, node uuid.UUID) (taskmodel.TaskNodeQueue, error) {
	out := taskmodel.TaskNodeQueue{TaskID: m.Record.View.Task.ID, NodeRunID: node, Items: []taskmodel.QueueItem{}}
	for _, n := range m.Record.View.Nodes {
		if n.ID == node {
			out.RuntimeID = n.RuntimeID
		}
	}
	items, e := taskQueueItemsTx(ctx, m.Tx, out.TaskID, node)
	if e != nil {
		return out, e
	}
	for _, it := range items {
		out.Items = append(out.Items, taskQueueItem(it))
	}
	return out, nil
}

// emitTaskNodeQueuesTx runs once per changed task mutation. It re-reads the
// queue of the current node and of every node last reported non-empty, and
// emits task.node.queue.updated only for those whose queue changed.
func (s *Store) emitTaskNodeQueuesTx(ctx context.Context, m *TaskMutation) error {
	nodes := []uuid.UUID{}
	if n := m.Record.Current(); n != nil {
		nodes = append(nodes, n.ID)
	}
	for id := range m.Record.NodeQueueDigests {
		if !slices.Contains(nodes, id) {
			nodes = append(nodes, id)
		}
	}
	slices.SortFunc(nodes, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	for _, id := range nodes {
		q, e := s.TaskNodeQueueTx(ctx, m, id)
		if e != nil {
			return e
		}
		digest := ""
		if len(q.Items) > 0 {
			raw, e := json.Marshal(q)
			if e != nil {
				return e
			}
			sum := sha256.Sum256(raw)
			digest = hex.EncodeToString(sum[:])
		}
		if digest == m.Record.NodeQueueDigests[id] {
			continue
		}
		if digest == "" {
			delete(m.Record.NodeQueueDigests, id)
		} else {
			if m.Record.NodeQueueDigests == nil {
				m.Record.NodeQueueDigests = map[uuid.UUID]string{}
			}
			m.Record.NodeQueueDigests[id] = digest
		}
		if e = m.Emit("task.node.queue.updated", q); e != nil {
			return e
		}
	}
	return nil
}

// Called in the claim transaction. This is notification authority, not proof of
// actual reading or replying; those links are recorded by their own API actions.
func (s *Store) BindTaskNodeInboxBatchTx(ctx context.Context, m *TaskMutation, turn TaskTurn) error {
	rows, e := m.Tx.Query(ctx, `INSERT INTO task_node_inbox_run_links(message_id,run_id,role) SELECT message_id,$1,'notification' FROM task_node_inbox_messages WHERE reminder_run_id=$1 ON CONFLICT DO NOTHING RETURNING message_id`, turn.ID)
	if e != nil {
		return e
	}
	ids, e := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if e != nil {
		return e
	}
	for _, id := range ids {
		if e = s.EmitTaskNodeInboxTx(ctx, m, id); e != nil {
			return e
		}
	}
	return nil
}

func (s *Store) ExpireTaskNodeInboxTx(ctx context.Context, m *TaskMutation) error {
	var current *uuid.UUID
	mode := "task_node"
	if n := m.Record.Current(); n != nil && !m.Record.Ended() {
		current = &n.ID
		if n.Status == "blocked" {
			mode = "task_conversation"
		}
	}
	rows, e := m.Tx.Query(ctx, `SELECT b.message_id FROM task_node_inbox_messages b JOIN task_turns t ON b.reminder_run_id=t.id WHERE b.task_id=$1 AND b.not_executed_at IS NULL AND (t.status='interrupted' OR $2::uuid IS NULL OR b.node_run_id<>$2 OR b.execution_mode<>$3) AND NOT EXISTS(SELECT 1 FROM task_node_inbox_run_links l WHERE l.message_id=b.message_id AND l.role IN ('read','reply'))`, m.Record.View.Task.ID, current, mode)
	if e != nil {
		return e
	}
	ids, e := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if e != nil {
		return e
	}
	for _, id := range ids {
		binding, _, e := s.TaskNodeInboxBindingTx(ctx, m.Tx, id)
		if e != nil {
			return e
		}
		if fenced, e := s.TaskLarkRuntimeFencedTx(ctx, m, binding.Target.RuntimeID); e != nil {
			return e
		} else if fenced && !m.Record.Ended() {
			continue
		}
		reason, e := s.TaskConsoleGuardTx(ctx, m, binding.Target)
		if e != nil {
			return e
		}
		// A notification interrupted by another message is not message expiry.
		if reason == "" {
			continue
		}
		if _, e = m.Tx.Exec(ctx, `UPDATE task_node_inbox_messages SET not_executed_reason=$2,not_executed_at=clock_timestamp() WHERE message_id=$1`, id, reason); e != nil {
			return e
		}
		if e = s.EmitTaskNodeInboxTx(ctx, m, id); e != nil {
			return e
		}
	}
	return nil
}
