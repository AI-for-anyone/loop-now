package conversation

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"loop-now/internal/loopctl/guide"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/reqid"
	"loop-now/internal/server/store"
)

// ReminderPlaceholder 是平台提醒那一轮排队时用户消息的正文；开始执行时改写为提醒文字。
const ReminderPlaceholder = "平台提醒"

// ReminderText 生成提醒文字：点名本轮承载的待处理消息（ID、发件人与摘要），其余未读消息说明稍后另行提醒。
// 命令取自同版本 loopctl。
func ReminderText(c store.ReminderCarried) string {
	if len(c.Messages) == 0 {
		return "平台提醒：本轮没有需要处理的 Inbox 消息。"
	}
	text := fmt.Sprintf("平台提醒：本轮请处理以下 %d 条 Inbox 消息，用 `%s <消息ID>` 逐条读取并处理，用法见 Skill %s：\n%s",
		len(c.Messages), guide.InboxReadCommand(), guide.SkillName, store.ReminderList(c.Messages))
	if c.Others > 0 {
		text += fmt.Sprintf("\n另有 %d 条未读消息已在排队，稍后会另行提醒，本轮不必处理。", c.Others)
	}
	return text
}

// DeliverInbox 在一个事务中写入发给 Agent 的 Inbox 消息并安排一轮平台提醒，与控制台消息共用排队、忙闲锁与驱动循环：
// Agent 空闲时立即开始；忙时按 whenBusy 排队，已有同优先级、排队中的提醒时并入它；打断消息仍打断当前执行。
// 会话丢失时只保存消息，不安排提醒；人确认重开会话后由 remindUnread 补上。
func (s *Service) DeliverInbox(ctx context.Context, msg store.NewInboxMessage) error {
	if msg.To.Kind != "agent" {
		return errors.New("DeliverInbox 只用于发给 Agent 的消息")
	}
	return s.scheduleReminder(ctx, msg.To.ID, msg.ID, msg.WhenBusy, &msg)
}

// remindUnread 在人确认重开会话后调用：Agent 还有未读的 Inbox 消息时立即安排一轮提醒，
// 触发消息记为最早的那条未读消息（它在会话丢失期间没能触发提醒）。失败只记日志，不影响重开会话。
func (s *Service) remindUnread(ctx context.Context, agentID uuid.UUID) {
	id, ok, err := s.st.EarliestUnreadInbox(ctx, agentID)
	if err == nil && ok {
		err = s.scheduleReminder(ctx, agentID, id, "queue", nil)
	}
	if err != nil {
		s.log.Warn("重开会话后安排平台提醒失败", "agentId", agentID, "err", err)
	}
}

// scheduleReminder 安排一轮平台提醒；msg 不为空时在同一事务中写入这条 Inbox 消息。
func (s *Service) scheduleReminder(ctx context.Context, agentID, triggerID uuid.UUID, whenBusy string, msg *store.NewInboxMessage) error {
	if s.ctx.Err() != nil {
		return errors.New("服务端正在停止")
	}
	runID := uuid.Must(uuid.NewV7())
	owner := coord.LockValue(runID.String(), s.o.InstanceID.String())
	acquired, err := s.co.AcquireIfIdle(ctx, agentID.String(), owner)
	if err != nil {
		return err
	}
	rem := store.NewRun{AgentID: agentID, RunID: runID, Text: ReminderPlaceholder, InstanceID: s.o.InstanceID, RequestID: reqid.From(ctx),
		TriggerInboxID: &triggerID}
	if acquired {
		rem.Start = true
	} else {
		s.queueReminder(ctx, &rem, whenBusy)
	}
	out, err := s.st.InsertInboxWithReminder(ctx, msg, rem)
	if acquired && (err != nil || !out.Created) {
		_, _ = s.co.Release(ctx, agentID.String(), owner)
		if errors.Is(err, store.ErrActiveRunExists) {
			// Redis 丢了锁：按数据库中的进行中执行恢复锁，然后按“忙”排队。
			s.restoreLock(ctx, agentID)
			rem.Start, rem.RunID = false, uuid.Must(uuid.NewV7())
			s.queueReminder(ctx, &rem, whenBusy)
			out, err = s.st.InsertInboxWithReminder(ctx, msg, rem)
			acquired = false
		}
	}
	if err != nil {
		return err
	}
	switch {
	case out.SessionMissing:
		s.log.Warn("Agent 会话丢失，Inbox 消息已保存，未安排提醒；重开会话后补上", "agentId", agentID, "inboxMessageId", triggerID)
	case out.Created:
		s.emitMessage(ctx, agentID, *out.Message, *out.Run)
		s.emitRun(ctx, agentID, out.Run.ID)
		if acquired {
			s.startDriver(agentID, *out.Run, owner)
			return nil
		}
		s.emitQueue(ctx, agentID)
		if whenBusy == "interrupt" {
			s.interruptActive(ctx, agentID, out.Run.ID)
		}
	case out.Merged:
		s.emitQueue(ctx, agentID)
		if whenBusy == "interrupt" {
			s.interruptActive(ctx, agentID, out.Run.ID)
		}
	}
	s.kick(ctx, agentID)
	return nil
}

// queueReminder 把提醒设为排队：按 whenBusy 进入打断队列或普通队列。
func (s *Service) queueReminder(ctx context.Context, rem *store.NewRun, whenBusy string) {
	agentID := rem.AgentID
	rem.WhenBusy = whenBusy
	rem.Enqueue = func(r store.Run) error {
		if whenBusy == "interrupt" {
			return s.co.PushInterrupt(ctx, agentID.String(), r.ID.String())
		}
		return s.co.PushNormal(ctx, agentID.String(), r.ID.String())
	}
	rem.OnEnqueueError = func(err error) {
		s.log.Warn("写入 Redis 队列失败，等待对账补回", "agentId", agentID, "runId", rem.RunID, "err", err)
	}
}

// prepareReminder 在平台提醒那一轮开始执行时调用：先收养没有承载提醒的待处理消息，再在提醒文字中点名本轮承载的消息，
// 把用户消息改写为提醒文字并推送，返回交给执行器的文字。本轮已没有待处理消息、也不承载 sleep 唤醒时返回 void：
// 这一轮作废（no_pending_messages），不启动 Pi。
func (s *Service) prepareReminder(ctx context.Context, agentID uuid.UUID, run store.Run) (text string, void bool, err error) {
	c, err := s.st.PrepareReminderStart(ctx, agentID, run.ID)
	if err != nil {
		return "", false, err
	}
	if len(c.Messages) == 0 && run.WakeSleepID == nil {
		return "", true, nil
	}
	text = ReminderText(c)
	m, err := s.st.SetMessageText(ctx, run.UserMessageID, text)
	if err != nil {
		return "", false, err
	}
	s.emitMessage(ctx, agentID, m, run) // 同一消息 ID 再推送一次，控制台按 ID 更新正文
	return text, false, nil
}

// outcomeVoided 表示提醒开始时已没有待处理消息：这一轮作废（no_pending_messages），不启动 Pi。
const outcomeVoided = "voided"

// InboxCommitted wakes durable queue work prepared by the shared Inbox transaction.
func (s *Service) InboxCommitted(ctx context.Context, agentID uuid.UUID) {
	s.emitQueue(ctx, agentID)
	s.kick(ctx, agentID)
}
