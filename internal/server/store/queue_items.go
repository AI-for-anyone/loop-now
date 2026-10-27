package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/messageimage"
	"loop-now/internal/server/naming"
	"loop-now/internal/server/taskmodel"
)

// unreadCond 是“发给收件方、仍待处理”的条件：未读且未被人取消。已取消的消息不计为未读。
func unreadCond(alias string) string {
	return alias + ".read_at IS NULL AND " + alias + ".cancelled_at IS NULL"
}

// queueQuerier 是事务或连接池。
type queueQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// lockAgentQueue 以 FOR NO KEY UPDATE 锁住未删除的 Agent 行：与出队（StartQueuedRun）、结束（FinishRun）、
// 写入消息的 FOR UPDATE 互斥，但不挡外键需要的 KEY SHARE。排队动作与读取都先取这把锁，再锁执行行与消息行。
func lockAgentQueue(ctx context.Context, tx pgx.Tx, agentID uuid.UUID) error {
	var live bool
	err := tx.QueryRow(ctx, `SELECT true FROM agents WHERE id = $1 AND deleted_at IS NULL FOR NO KEY UPDATE`, agentID).Scan(&live)
	return notFoundIfNoRows(err, "Agent")
}

// QueueItem 是排队列表中的一项：一条仍待处理的消息。
type QueueItem struct {
	Kind      string // run：控制台在对话中发出的消息；inbox：Inbox 消息
	RunID     uuid.UUID
	MessageID *uuid.UUID
	From      PartyView
	Text      string
	Images    []messageimage.Ref
	WhenBusy  string
	QueuedAt  time.Time
}

func whenBusyOf(priority int) string {
	if priority == 0 {
		return "interrupt"
	}
	return "queue"
}

func inboxItem(m InboxMessage, run uuid.UUID, priority int) QueueItem {
	id := m.ID
	images := m.Images
	if images == nil {
		images = []messageimage.Ref{}
	}
	return QueueItem{Kind: "inbox", RunID: run, MessageID: &id, From: m.From, Text: m.Text, Images: images, WhenBusy: whenBusyOf(priority), QueuedAt: m.CreatedAt}
}

// AgentQueue 返回常态 Agent 的排队列表，按实际处理顺序：执行按 priority、queue_seq，
// 同一提醒内的消息按发送先后。sleep 唤醒执行不列出。
func (s *Store) AgentQueue(ctx context.Context, agentID uuid.UUID) ([]QueueItem, error) {
	return agentQueue(ctx, s.Pool, agentID)
}

// AgentQueueTx 在事务中读取排队列表（见 AgentQueue）。
func (s *Store) AgentQueueTx(ctx context.Context, tx pgx.Tx, agentID uuid.UUID) ([]QueueItem, error) {
	return agentQueue(ctx, tx, agentID)
}

func agentQueue(ctx context.Context, q queueQuerier, agentID uuid.UUID) ([]QueueItem, error) {
	type queued struct {
		id       uuid.UUID
		origin   string
		priority int
		queuedAt time.Time
		text     string
		images   []byte
	}
	rows, err := q.Query(ctx, `SELECT r.id, r.origin, r.priority, r.queued_at, cm.text, cm.images FROM runs r
		JOIN conversation_messages cm ON cm.id = r.user_message_id
		WHERE r.agent_id = $1 AND r.status = 'queued' AND r.origin <> 'sleep_wake' ORDER BY r.priority, r.queue_seq`, agentID)
	if err != nil {
		return nil, err
	}
	runs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (queued, error) {
		var x queued
		return x, r.Scan(&x.id, &x.origin, &x.priority, &x.queuedAt, &x.text, &x.images)
	})
	if err != nil {
		return nil, err
	}
	reminders := []uuid.UUID{}
	for _, r := range runs {
		if r.origin == "inbox_reminder" {
			reminders = append(reminders, r.id)
		}
	}
	byRun := map[uuid.UUID][]InboxMessage{}
	if len(reminders) > 0 {
		rows, err := q.Query(ctx, inboxSelect(200)+` WHERE m.reminder_run_id = ANY($1) AND `+unreadCond("m")+` ORDER BY m.created_at, m.id`, reminders)
		if err != nil {
			return nil, err
		}
		msgs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (InboxMessage, error) { return scanInbox(r) })
		if err != nil {
			return nil, err
		}
		for _, m := range msgs {
			byRun[*m.ReminderRunID] = append(byRun[*m.ReminderRunID], m)
		}
	}
	out := []QueueItem{}
	for _, r := range runs {
		if r.origin == "console" {
			it := QueueItem{Kind: "run", RunID: r.id, From: PartyView{Party: ConsoleParty}, Text: r.text, Images: []messageimage.Ref{}, WhenBusy: whenBusyOf(r.priority), QueuedAt: r.queuedAt}
			if err := json.Unmarshal(r.images, &it.Images); err != nil {
				return nil, err
			}
			out = append(out, it)
			continue
		}
		for _, m := range byRun[r.id] {
			out = append(out, inboxItem(m, r.id, r.priority))
		}
	}
	return out, nil
}

// consoleRunItem 返回控制台执行对应的排队项（取消时的回执内容）。
func consoleRunItem(ctx context.Context, q queueQuerier, r Run) (QueueItem, error) {
	m, err := scanMessage(q.QueryRow(ctx, `SELECT `+messageColumns+` FROM conversation_messages WHERE id = $1`, r.UserMessageID))
	if err != nil {
		return QueueItem{}, err
	}
	images := m.Images
	if images == nil {
		images = []messageimage.Ref{}
	}
	return QueueItem{Kind: "run", RunID: r.ID, From: PartyView{Party: ConsoleParty}, Text: m.Text, Images: images, WhenBusy: whenBusyOf(r.Priority), QueuedAt: r.QueuedAt}, nil
}

// voidRunIfEmptyTx 作废没有待处理消息、也不承载 sleep 唤醒的排队提醒，并写入结束事件；返回是否作废。
// 调用方已按“先 Agent、后执行”持锁。
func (s *Store) voidRunIfEmptyTx(ctx context.Context, tx pgx.Tx, runID uuid.UUID, reason string) (bool, error) {
	r, err := scanRun(tx.QueryRow(ctx, `UPDATE runs r SET status = 'interrupted', end_reason = $2, ended_at = now()
		WHERE r.id = $1 AND r.status = 'queued' AND r.origin = 'inbox_reminder' AND r.wake_sleep_id IS NULL
			AND NOT EXISTS (SELECT 1 FROM inbox_messages m WHERE m.reminder_run_id = r.id AND `+unreadCond("m")+`)
		RETURNING `+runColumns, runID, reason))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, s.insertEvent(ctx, tx, LifecycleEvent{AgentID: r.AgentID, RunID: &r.ID, Type: EventRunEnded, At: *r.EndedAt,
		Data: map[string]any{"status": r.Status, "endReason": reason}})
}

// taskTurnPendingSQL 是“轮次 t 仍承载待处理消息”的条件：非严格消息看 reminder_turn_id，严格节点消息看绑定。
const taskTurnPendingSQL = `(EXISTS (SELECT 1 FROM inbox_messages m WHERE m.reminder_turn_id = t.id AND m.read_at IS NULL AND m.cancelled_at IS NULL)
	OR EXISTS (SELECT 1 FROM task_node_inbox_messages b JOIN inbox_messages m ON m.id = b.message_id
		WHERE b.reminder_run_id = t.id AND b.not_executed_at IS NULL AND m.read_at IS NULL AND m.cancelled_at IS NULL))`

// voidTaskTurnIfEmptyTx 作废没有待处理消息、也不承载 sleep 唤醒或问题结束唤醒的排队提醒轮次（TaskMessage.voidReason=reason）；返回是否作废。
func (s *Store) voidTaskTurnIfEmptyTx(ctx context.Context, m *TaskMutation, turnID uuid.UUID, reason string) (bool, error) {
	t, err := scanTaskTurn(m.Tx.QueryRow(ctx, `UPDATE task_turns t SET status = 'interrupted', ended_at = now(), void_reason = $2
		WHERE t.id = $1 AND t.status = 'queued' AND t.origin = 'inbox_reminder' AND t.wake_sleep_id IS NULL AND NOT t.question_wakeup AND NOT `+taskTurnPendingSQL+`
		RETURNING `+taskTurnColumns, turnID, reason))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	m.Changed()
	if err = s.taskEndedEvent(ctx, m, t); err != nil {
		return false, err
	}
	if t.NodeRunID != uuid.Nil {
		return true, m.Emit("message.created", taskMessage(t, false))
	}
	return true, nil
}

// LinkInboxReminderTx 记下承载这条 Inbox 消息的平台提醒（常态 Agent）。
func LinkInboxReminderTx(ctx context.Context, tx pgx.Tx, messageID, runID uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE inbox_messages SET reminder_run_id = $2, reminder_turn_id = NULL WHERE id = $1`, messageID, runID)
	return err
}

// LinkInboxTurnTx 记下承载这条 Inbox 消息的任务轮次（任务实例的非严格消息）。
func LinkInboxTurnTx(ctx context.Context, tx pgx.Tx, messageID, turnID uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE inbox_messages SET reminder_turn_id = $2, reminder_run_id = NULL WHERE id = $1`, messageID, turnID)
	return err
}

// AdoptTaskInboxOrphansTx 把发给任务实例、仍待处理但没有排队中承载轮次的非严格消息挂到 turn 上
// （例如所在轮次被打断或随节点变化结束）。
func AdoptTaskInboxOrphansTx(ctx context.Context, tx pgx.Tx, runtime, turn uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE inbox_messages m SET reminder_turn_id = $2 WHERE m.to_kind = 'agent' AND m.to_runtime_id = $1 AND `+unreadCond("m")+`
		AND NOT EXISTS (SELECT 1 FROM task_node_inbox_messages b WHERE b.message_id = m.id)
		AND (m.reminder_turn_id IS NULL OR EXISTS (SELECT 1 FROM task_turns o WHERE o.id = m.reminder_turn_id AND o.status IN ('completed', 'failed', 'interrupted')))`, runtime, turn)
	return err
}

// adoptAgentInboxOrphansTx 把发给常态 Agent、仍待处理但没有排队中或进行中承载提醒的消息挂到 run 上。
func adoptAgentInboxOrphansTx(ctx context.Context, q queueQuerier, agentID, run uuid.UUID) error {
	_, err := q.Exec(ctx, `UPDATE inbox_messages m SET reminder_run_id = $2 WHERE m.to_kind = 'agent' AND m.to_agent_id = $1 AND m.to_runtime_id IS NULL AND `+unreadCond("m")+`
		AND (m.reminder_run_id IS NULL OR EXISTS (SELECT 1 FROM runs o WHERE o.id = m.reminder_run_id AND o.status IN ('completed', 'failed', 'interrupted')))`, agentID, run)
	return err
}

// ReminderCarried 是平台提醒开始时点名的内容：本轮承载的待处理消息，以及其余仍未读的消息数。
type ReminderCarried struct {
	Messages []InboxMessage
	Others   int
}

// PrepareReminderStart 在提醒执行开始时调用：先收养没有承载提醒的待处理消息，再返回本轮承载的消息与其余未读数。
func (s *Store) PrepareReminderStart(ctx context.Context, agentID, run uuid.UUID) (ReminderCarried, error) {
	var out ReminderCarried
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := adoptAgentInboxOrphansTx(ctx, tx, agentID, run); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, inboxSelect(200)+` WHERE m.reminder_run_id = $1 AND `+unreadCond("m")+` ORDER BY m.created_at, m.id`, run)
		if err != nil {
			return err
		}
		if out.Messages, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (InboxMessage, error) { return scanInbox(r) }); err != nil {
			return err
		}
		n, err := countUnread(ctx, tx, AgentParty(agentID))
		out.Others = n - len(out.Messages)
		return err
	})
	return out, err
}

// taskTurnCarriedTx 返回任务轮次承载的待处理消息（严格节点消息与非严格消息），按发送先后。
func taskTurnCarriedTx(ctx context.Context, tx pgx.Tx, turn uuid.UUID) ([]InboxMessage, error) {
	rows, err := tx.Query(ctx, inboxSelect(200)+` WHERE (m.reminder_turn_id = $1 OR m.id IN (SELECT b.message_id FROM task_node_inbox_messages b WHERE b.reminder_run_id = $1 AND b.not_executed_at IS NULL))
		AND `+unreadCond("m")+` ORDER BY m.created_at, m.id`, turn)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (InboxMessage, error) { return scanInbox(r) })
}

// PartyLabel 是提醒中点名发件人的写法：地址优先，没有地址时用名称。
func PartyLabel(v PartyView) string {
	switch v.Kind {
	case "console":
		return "@console"
	case "external":
		return "@" + v.Name
	case "plugin":
		return PluginAddress(v)
	case "agent":
		if addr, ok := naming.AgentAddress(v.OrgName, v.ProjectName, v.Name, v.Platform); ok {
			if v.TaskID != nil {
				addr += "#" + v.TaskID.String()
			}
			return addr
		}
	}
	return v.Name
}

// ReminderList 是提醒中点名的消息列表：每条一行，含消息 ID、发件人与正文摘要。
func ReminderList(msgs []InboxMessage) string {
	var b strings.Builder
	for _, m := range msgs {
		summary := strings.Join(strings.Fields(m.Text), " ")
		if r := []rune(summary); len(r) > 60 {
			summary = string(r[:60]) + "…"
		}
		if summary == "" && len(m.Images) > 0 {
			summary = fmt.Sprintf("[%d 张图片]", len(m.Images))
		}
		fmt.Fprintf(&b, "- %s（来自 %s）：%s\n", m.ID, PartyLabel(m.From), summary)
	}
	return strings.TrimRight(b.String(), "\n")
}

// taskReminderText 在轮次开始时给出点名的内容；base 为轮次原有的提示。
func taskReminderText(base string, carried []InboxMessage, others int) string {
	text := base + "\n本轮请先处理以下 " + fmt.Sprint(len(carried)) + " 条 Inbox 消息：\n" + ReminderList(carried)
	if others > 0 {
		text += fmt.Sprintf("\n另有 %d 条未读消息已在排队，稍后会另行提醒，本轮不必处理。", others)
	}
	return text
}

// prepareTaskReminderClaimTx 在提醒轮次被领取时调用：非严格提醒先收养没有承载轮次的待处理消息；
// 没有待处理消息、也不承载唤醒时作废这一轮并返回 skip；否则在提示中点名本轮承载的消息。
func (s *Store) prepareTaskReminderClaimTx(ctx context.Context, m *TaskMutation, t *TaskTurn) (bool, error) {
	if t.Origin != "inbox_reminder" {
		return false, nil
	}
	var scoped, question bool
	if err := m.Tx.QueryRow(ctx, `SELECT inbox_scope_node_id IS NOT NULL, question_wakeup FROM task_turns WHERE id = $1`, t.ID).Scan(&scoped, &question); err != nil {
		return false, err
	}
	if !scoped {
		if err := AdoptTaskInboxOrphansTx(ctx, m.Tx, t.RuntimeID, t.ID); err != nil {
			return false, err
		}
	}
	carried, err := taskTurnCarriedTx(ctx, m.Tx, t.ID)
	if err != nil {
		return false, err
	}
	if len(carried) == 0 {
		if t.WakeSleepID != nil || question {
			return false, nil
		}
		voided, err := s.voidTaskTurnIfEmptyTx(ctx, m, t.ID, "no_pending_messages")
		return voided, err
	}
	var others int
	if err = m.Tx.QueryRow(ctx, `SELECT count(*) FROM inbox_messages m WHERE m.to_kind = 'agent' AND m.to_runtime_id = $1 AND `+unreadCond("m"), t.RuntimeID).Scan(&others); err != nil {
		return false, err
	}
	t.Text = taskReminderText(t.Text, carried, others-len(carried))
	_, err = m.Tx.Exec(ctx, `UPDATE task_turns SET text = $2 WHERE id = $1`, t.ID, t.Text)
	return false, err
}

// TaskQueueItem 把排队项转为节点排队列表与回执的结构。
func (s *Store) TaskQueueItem(it QueueItem) taskmodel.QueueItem { return taskQueueItem(it) }

// taskQueueItem 是节点排队列表中的一项（任务事件与接口的结构）。
func taskQueueItem(it QueueItem) taskmodel.QueueItem {
	from, _ := json.Marshal(partyWire(it.From))
	images := it.Images
	if images == nil {
		images = []messageimage.Ref{}
	}
	return taskmodel.QueueItem{Kind: it.Kind, RunID: it.RunID, MessageID: it.MessageID, From: from, Text: it.Text, Images: images, WhenBusy: it.WhenBusy, QueuedAt: it.QueuedAt}
}

// taskQueueItemsTx 返回节点的排队列表：该节点排队中的提醒轮次承载的待处理消息，逐条，按领取顺序。
func taskQueueItemsTx(ctx context.Context, tx pgx.Tx, task, node uuid.UUID) ([]QueueItem, error) {
	type turn struct {
		id       uuid.UUID
		priority int
	}
	rows, err := tx.Query(ctx, `SELECT t.id, t.priority FROM task_turns t WHERE t.task_id = $1 AND t.node_run_id = $2 AND t.status = 'queued' AND t.origin = 'inbox_reminder'
		ORDER BY t.priority, t.queue_seq`, task, node)
	if err != nil {
		return nil, err
	}
	turns, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (turn, error) {
		var x turn
		return x, r.Scan(&x.id, &x.priority)
	})
	if err != nil {
		return nil, err
	}
	out := []QueueItem{}
	for _, t := range turns {
		msgs, err := taskTurnCarriedTx(ctx, tx, t.id)
		if err != nil {
			return nil, err
		}
		for _, m := range msgs {
			out = append(out, inboxItem(m, t.id, t.priority))
		}
	}
	return out, nil
}

// queueNotPending 返回 run_not_queued：排队项已不在队列。
func queueNotPending(runID *uuid.UUID, status, whenBusy string, message *uuid.UUID, state string) error {
	e := apperr.Conflict(apperr.CodeRunNotQueued, "该消息已开始处理、已读或已取消，不在排队中")
	d := RunNotQueuedDetails{RunID: runID, MessageID: message, MessageState: state}
	if status != "" {
		d.Status = &status
	}
	if whenBusy != "" {
		d.WhenBusy = &whenBusy
	}
	e.Details = d
	return e
}

// MarkQuestionWakeupTx 记下这一提醒轮次还负责在节点自己的问题结束后唤醒节点：没有待处理消息时也照常执行。
func MarkQuestionWakeupTx(ctx context.Context, tx pgx.Tx, turn uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE task_turns SET question_wakeup = true WHERE id = $1`, turn)
	return err
}
