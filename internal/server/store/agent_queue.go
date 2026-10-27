package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"loop-now/internal/server/apperr"
)

// AgentAction 是一次控制台排队动作的幂等键与内容，契约同任务动作：同一动作 ID 重放返回首次回执，换了内容返回 409。
type AgentAction struct {
	ActorKey  string
	ID        uuid.UUID
	Operation string
	Body      []byte
}

// AgentQueueAction 在一个事务里执行排队动作 fn 并记下它返回的回执。同一动作重放时返回首次回执（replayed=true），
// 不再调用 fn；同一动作 ID 换了内容返回 409 action_conflict。fn 返回错误时事务回滚，不记回执。
func (s *Store) AgentQueueAction(ctx context.Context, agentID uuid.UUID, a AgentAction, fn func(tx pgx.Tx) (any, error)) (json.RawMessage, bool, error) {
	if a.ID == uuid.Nil || a.ActorKey == "" || a.Operation == "" {
		return nil, false, apperr.Invalid("actionId", "必须提供有效动作ID")
	}
	hash := sha256.Sum256(append(append([]byte(nil), a.Body...), agentID.String()...))
	var out json.RawMessage
	replayed := false
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		out, replayed = nil, false
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "agent-action:"+a.ActorKey+":"+a.ID.String()); err != nil {
			return err
		}
		var op string
		var stored, response []byte
		err := tx.QueryRow(ctx, `SELECT operation, body_hash, response FROM agent_actions WHERE actor_key = $1 AND action_id = $2`, a.ActorKey, a.ID).Scan(&op, &stored, &response)
		switch {
		case err == nil:
			if op != a.Operation || !bytes.Equal(stored, hash[:]) {
				return apperr.Conflict(apperr.CodeActionConflict, "动作ID已用于其他请求")
			}
			out, replayed = response, true
			return nil
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		value, err := fn(tx)
		if err != nil {
			return err
		}
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		// 返回存下的回执，首次响应与重放逐字节相同。
		return tx.QueryRow(ctx, `INSERT INTO agent_actions (actor_key, action_id, operation, agent_id, body_hash, response) VALUES ($1, $2, $3, $4, $5, $6) RETURNING response`,
			a.ActorKey, a.ID, a.Operation, agentID, hash[:], b).Scan(&out)
	})
	return out, replayed, err
}

func lockAgentRunTx(ctx context.Context, tx pgx.Tx, agentID, runID uuid.UUID) (Run, error) {
	if err := lockAgentQueue(ctx, tx, agentID); err != nil {
		return Run{}, err
	}
	r, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id = $1 AND agent_id = $2 FOR UPDATE`, runID, agentID))
	return r, notFoundIfNoRows(err, "执行")
}

func runNotQueued(r Run) error {
	return queueNotPending(&r.ID, r.Status, r.WhenBusy, nil, "")
}

// PromoteQueuedRunTx 把控制台发出、仍在普通排队的执行改为打断：queue_seq 取新值，排到已有打断之后、全部普通排队之前。
// 已经是打断时 changed=false（不做任何改动）。item 是转换前的排队项。
func (s *Store) PromoteQueuedRunTx(ctx context.Context, tx pgx.Tx, agentID, runID uuid.UUID) (Run, QueueItem, bool, error) {
	r, err := lockAgentRunTx(ctx, tx, agentID, runID)
	if err != nil {
		return r, QueueItem{}, false, err
	}
	if r.Origin != "console" {
		return r, QueueItem{}, false, apperr.Invalid("runId", "只有控制台发出的排队消息可以整条转为打断；Inbox 消息请逐条转换")
	}
	item, err := consoleRunItem(ctx, tx, r)
	if err != nil {
		return r, item, false, err
	}
	if r.Status != "queued" {
		return r, item, false, runNotQueued(r)
	}
	if r.Priority == 0 {
		return r, item, false, nil
	}
	if r, err = scanRun(tx.QueryRow(ctx, `UPDATE runs SET priority = 0, when_busy = 'interrupt', queue_seq = DEFAULT WHERE id = $1 RETURNING `+runColumns, runID)); err != nil {
		return r, item, false, err
	}
	return r, item, true, s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, RunID: &r.ID, Type: EventRunPromoted, Data: map[string]any{"from": "queue", "to": "interrupt"}})
}

// CancelQueuedRunTx 撤回控制台发出、仍在排队的执行：以 interrupted、user_cancelled 结束，Agent 不会收到。
// 已经撤回时 changed=false。item 是这条消息的内容，供“编辑”回填。
func (s *Store) CancelQueuedRunTx(ctx context.Context, tx pgx.Tx, agentID, runID uuid.UUID) (Run, QueueItem, bool, error) {
	r, err := lockAgentRunTx(ctx, tx, agentID, runID)
	if err != nil {
		return r, QueueItem{}, false, err
	}
	if r.Origin != "console" {
		return r, QueueItem{}, false, apperr.Invalid("runId", "只有控制台发出的排队消息可以撤回；Inbox 消息请逐条取消")
	}
	item, err := consoleRunItem(ctx, tx, r)
	if err != nil {
		return r, item, false, err
	}
	if r.Status == "interrupted" && r.EndReason == "user_cancelled" {
		return r, item, false, nil
	}
	if r.Status != "queued" {
		return r, item, false, runNotQueued(r)
	}
	if r, err = scanRun(tx.QueryRow(ctx, `UPDATE runs SET status = 'interrupted', end_reason = 'user_cancelled', ended_at = now() WHERE id = $1 RETURNING `+runColumns, runID)); err != nil {
		return r, item, false, err
	}
	return r, item, true, s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, RunID: &r.ID, Type: EventRunEnded, At: *r.EndedAt,
		Data: map[string]any{"status": r.Status, "endReason": r.EndReason}})
}

// StopRunTx 停止这一次进行中的执行：置为 aborting 并记下停止（abort_requested 带 stop:true），结束原因随后记为 user_stopped。
// 已在停止或打断中时 changed=false；执行不在进行中时返回 409 run_not_active。
func (s *Store) StopRunTx(ctx context.Context, tx pgx.Tx, agentID, runID uuid.UUID) (Run, bool, error) {
	r, err := lockAgentRunTx(ctx, tx, agentID, runID)
	if err != nil {
		return r, false, err
	}
	switch r.Status {
	case "aborting":
		return r, false, nil
	case "starting", "running":
	default:
		e := apperr.Conflict(apperr.CodeRunNotActive, "这次回复已经结束，不能再停止")
		e.Details = RunNotActiveDetails{RunID: r.ID, Status: r.Status}
		return r, false, e
	}
	if r, err = scanRun(tx.QueryRow(ctx, `UPDATE runs SET status = 'aborting', abort_requested_at = coalesce(abort_requested_at, now()) WHERE id = $1 RETURNING `+runColumns, runID)); err != nil {
		return r, false, err
	}
	return r, true, s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, RunID: &r.ID, Type: EventRunAbortRequested, Data: map[string]any{"stop": true}})
}

// lockAgentInboxTx 锁住发给常态 Agent 的一条 Inbox 消息；不存在或不是发给它时返回 not_found。
func lockAgentInboxTx(ctx context.Context, tx pgx.Tx, agentID, messageID uuid.UUID) (InboxMessage, error) {
	m, err := scanInbox(tx.QueryRow(ctx, inboxSelect(200)+` WHERE m.id = $1 AND m.to_kind = 'agent' AND m.to_agent_id = $2 AND m.to_runtime_id IS NULL FOR UPDATE OF m`, messageID, agentID))
	return m, notFoundIfNoRows(err, "消息")
}

// queuedCarrierTx 返回承载这条消息、仍在排队的提醒（已锁）；消息已取消、已读、没有承载提醒或提醒已开始时返回 run_not_queued。
func queuedCarrierTx(ctx context.Context, tx pgx.Tx, m InboxMessage) (Run, error) {
	var run *Run
	if m.ReminderRunID != nil {
		r, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id = $1 FOR UPDATE`, *m.ReminderRunID))
		if err != nil {
			return Run{}, err
		}
		run = &r
	}
	fail := func(state string) error {
		if run == nil {
			return queueNotPending(nil, "", "", &m.ID, state)
		}
		return queueNotPending(&run.ID, run.Status, run.WhenBusy, &m.ID, state)
	}
	switch {
	case m.CancelledAt != nil:
		return Run{}, fail("cancelled")
	case m.ReadAt != nil:
		return Run{}, fail("read")
	case run == nil:
		return Run{}, fail("not_queued")
	case run.Status != "queued":
		return Run{}, fail("dispatched")
	}
	return *run, nil
}

// InboxPromotion 是一条 Inbox 消息逐条转为打断的结果。
type InboxPromotion struct {
	Item    QueueItem  // 转换前的排队项
	Target  Run        // 现在承载它的打断提醒
	Created bool       // Target 是这次新建的
	Voided  *uuid.UUID // 因此没有待处理消息而作废的原提醒
	Changed bool
}

// PromoteInboxMessageTx 把一条排队中的 Inbox 消息（任何发件人）移到排队中的打断提醒：没有就新建一个，
// queue_seq 新取值，排在已有打断之后、全部普通排队之前；消息的 whenBusy 改为 interrupt，原提醒空了即作废（merged_into_interrupt）。
// 已经由打断提醒承载时 Changed=false。新建提醒时 enqueue 在事务内被调用（推入 Redis 打断队列）。
func (s *Store) PromoteInboxMessageTx(ctx context.Context, tx pgx.Tx, agentID, messageID, instanceID uuid.UUID, enqueue func(Run) error) (InboxPromotion, error) {
	var out InboxPromotion
	if err := lockAgentQueue(ctx, tx, agentID); err != nil {
		return out, err
	}
	m, err := lockAgentInboxTx(ctx, tx, agentID, messageID)
	if err != nil {
		return out, err
	}
	from, err := queuedCarrierTx(ctx, tx, m)
	if err != nil {
		return out, err
	}
	out.Item = inboxItem(m, from.ID, from.Priority)
	if from.Priority == 0 {
		out.Target = from
		return out, nil
	}
	out.Target, err = scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE agent_id = $1 AND status = 'queued' AND origin = 'inbox_reminder' AND priority = 0
		ORDER BY queue_seq LIMIT 1 FOR UPDATE`, agentID))
	if errors.Is(err, pgx.ErrNoRows) {
		_, out.Target, err = s.insertMessageAndRunTx(ctx, tx, NewRun{AgentID: agentID, RunID: uuid.Must(uuid.NewV7()), Text: "平台提醒", WhenBusy: "interrupt",
			InstanceID: instanceID, TriggerInboxID: &messageID, Enqueue: enqueue})
		out.Created = true
	}
	if err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `UPDATE inbox_messages SET reminder_run_id = $2, when_busy = 'interrupt' WHERE id = $1`, messageID, out.Target.ID); err != nil {
		return out, err
	}
	if err = s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, RunID: &out.Target.ID, Type: EventRunPromoted,
		Data: map[string]any{"from": "queue", "to": "interrupt", "messageId": messageID.String(), "fromRunId": from.ID.String()}}); err != nil {
		return out, err
	}
	voided, err := s.voidRunIfEmptyTx(ctx, tx, from.ID, "merged_into_interrupt")
	if err != nil {
		return out, err
	}
	if voided {
		out.Voided = &from.ID
	}
	out.Changed = true
	return out, nil
}

// InboxCancellation 是取消一条排队 Inbox 消息的结果。
type InboxCancellation struct {
	Message InboxMessage // 取消后的消息
	Item    QueueItem    // 取消前的排队项，供“编辑”回填
	Run     *Run         // 承载它的提醒（取消后的状态）
	Voided  bool         // 承载提醒因此作废
	Changed bool
}

// CancelInboxMessageTx 由人取消一条排队中的 Inbox 消息（任何发件人）：记 cancelled_at，不再计为未读，收件方不会处理；
// 带选项的问题同时变为 cancelled。承载它的提醒没有其他待处理消息时作废（user_cancelled）。已经取消时 Changed=false。
func (s *Store) CancelInboxMessageTx(ctx context.Context, tx pgx.Tx, agentID, messageID uuid.UUID) (InboxCancellation, error) {
	var out InboxCancellation
	if err := lockAgentQueue(ctx, tx, agentID); err != nil {
		return out, err
	}
	m, err := lockAgentInboxTx(ctx, tx, agentID, messageID)
	if err != nil {
		return out, err
	}
	if m.CancelledAt != nil {
		out.Message = m
		out.Item = inboxItem(m, uuid.Nil, 1)
		if m.ReminderRunID != nil {
			r, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id = $1`, *m.ReminderRunID))
			if err != nil {
				return out, err
			}
			out.Run, out.Item = &r, inboxItem(m, r.ID, r.Priority)
		}
		return out, nil
	}
	run, err := queuedCarrierTx(ctx, tx, m)
	if err != nil {
		return out, err
	}
	out.Item = inboxItem(m, run.ID, run.Priority)
	if err = cancelInboxTx(ctx, tx, messageID); err != nil {
		return out, err
	}
	if err = s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, RunID: &run.ID, Type: EventMessageCancelled, Data: map[string]any{"messageId": messageID.String()}}); err != nil {
		return out, err
	}
	if out.Voided, err = s.voidRunIfEmptyTx(ctx, tx, run.ID, "user_cancelled"); err != nil {
		return out, err
	}
	if run, err = scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id = $1`, run.ID)); err != nil {
		return out, err
	}
	out.Run = &run
	if out.Message, err = GetInboxMessageTx(ctx, tx, messageID, 200); err != nil {
		return out, err
	}
	out.Changed = true
	return out, nil
}

// cancelInboxTx 记下消息被人取消；带选项且仍开放的问题同时变为 cancelled。
func cancelInboxTx(ctx context.Context, tx pgx.Tx, messageID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `UPDATE inbox_messages SET cancelled_at = now() WHERE id = $1 AND read_at IS NULL AND cancelled_at IS NULL`, messageID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE inbox_questions SET state = 'cancelled' WHERE message_id = $1 AND state = 'open'`, messageID)
	return err
}
