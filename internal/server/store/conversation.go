package store

import (
	"context"
	"encoding/json"
	"errors"
	"loop-now/internal/server/messageimage"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"loop-now/internal/server/apperr"
)

// Run 是对一条用户消息的处理记录。
type Run struct {
	ID               uuid.UUID
	AgentID          uuid.UUID
	UserMessageID    uuid.UUID
	ConfigVersion    int64
	InstanceID       *uuid.UUID
	Priority         int
	WhenBusy         string
	QueueSeq         int64
	Status           string
	Phase            string
	EndReason        string
	ErrorCode        string
	ErrorMessage     string
	QueuedAt         time.Time
	StartedAt        *time.Time // 派发时间
	RunningAt        *time.Time // 消息交给 Pi 的时间
	AbortRequestedAt *time.Time
	EndedAt          *time.Time
	Origin           string // console / inbox_reminder
	WakeSleepID      *uuid.UUID
	TriggerInboxID   *uuid.UUID // 平台提醒：创建这次排队提醒的 Inbox 消息
	Tools            []ToolEvent
	Usage            UsageTotals
	Model            *ModelRef // 最后一次 assistant 调用的提供方与模型；没有时为空
}

// ToolEvent 是一次工具调用的状态记录（不含参数与输出）。
type ToolEvent struct {
	ToolCallID string
	ToolName   string
	State      string
	At         time.Time
}

// Message 是对话中的一条消息。
type Message struct {
	Images       []messageimage.Ref
	ID           uuid.UUID
	AgentID      uuid.UUID
	Seq          int64
	Role         string
	Text         string
	RunID        uuid.UUID
	SessionEpoch int
	CreatedAt    time.Time
}

// ErrActiveRunExists 表示该 Agent 已有进行中的执行（单一执行约束）。
var ErrActiveRunExists = errors.New("Agent 已有进行中的执行")

// runColumnsOf 返回 runs 的列，按 runFields 的顺序；prefix 为表别名加点，例如 "r."。
func runColumnsOf(prefix string) string {
	return strings.NewReplacer("$.", prefix).Replace(`$.id, $.agent_id, coalesce($.user_message_id, '00000000-0000-0000-0000-000000000000'::uuid), $.config_version, $.instance_id, $.priority,
	coalesce($.when_busy, ''), $.queue_seq, $.status, coalesce($.phase, ''), coalesce($.end_reason, ''), coalesce($.error_code, ''),
	coalesce($.error_message, ''), $.queued_at, $.started_at, $.running_at, $.abort_requested_at, $.ended_at,
	$.origin, $.trigger_inbox_message_id, $.wake_sleep_id`)
}

var runColumns = runColumnsOf("")

func runFields(r *Run) []any {
	return []any{&r.ID, &r.AgentID, &r.UserMessageID, &r.ConfigVersion, &r.InstanceID, &r.Priority, &r.WhenBusy, &r.QueueSeq, &r.Status,
		&r.Phase, &r.EndReason, &r.ErrorCode, &r.ErrorMessage, &r.QueuedAt, &r.StartedAt,
		&r.RunningAt, &r.AbortRequestedAt, &r.EndedAt, &r.Origin, &r.TriggerInboxID, &r.WakeSleepID}
}

func scanRun(row pgx.Row) (Run, error) {
	var r Run
	err := row.Scan(runFields(&r)...)
	r.Tools = []ToolEvent{}
	return r, err
}

const messageColumns = `id, agent_id, seq, role, text, run_id, session_epoch, created_at, images`

func scanMessage(row pgx.Row) (Message, error) {
	var m Message
	var raw []byte
	err := row.Scan(&m.ID, &m.AgentID, &m.Seq, &m.Role, &m.Text, &m.RunID, &m.SessionEpoch, &m.CreatedAt, &raw)
	if err == nil {
		err = json.Unmarshal(raw, &m.Images)
	}
	return m, err
}

// NewRun 是插入用户消息与执行时的参数。
type NewRun struct {
	ImageIDs   []uuid.UUID
	MaxImages  int
	AgentID    uuid.UUID
	RunID      uuid.UUID
	Text       string
	Start      bool   // true：直接以 starting 开始；false：排队
	WhenBusy   string // interrupt / queue / 空
	InstanceID uuid.UUID
	// RequestID 是发起这条消息的接口请求编号，记入入队或派发事件；可为空。
	RequestID string
	// Enqueue 在同一事务内、提交前调用（仅排队时），用于把执行推入 Redis 队列。
	// 事务锁住了 Agent 行，同一 Agent 的入队因此串行，Redis 队列顺序与数据库编号一致。
	// 返回的错误只记录、不回滚：Redis 中缺少的排队执行由对账补回。
	Enqueue        func(run Run) error
	OnEnqueueError func(error)
	// TriggerInboxID 不为空表示这是一轮平台提醒（origin = inbox_reminder），记下创建它的 Inbox 消息。
	TriggerInboxID *uuid.UUID
	Origin         string
	WakeSleepID    *uuid.UUID
}

// InsertMessageAndRun 在一个事务中写入用户消息、对应执行，以及入队或派发事件。
// Agent 不存在或已删除返回 not_found；会话丢失返回 session_missing；
// Start 为 true 但已有进行中的执行时返回 ErrActiveRunExists。
func (s *Store) InsertMessageAndRun(ctx context.Context, in NewRun) (Message, Run, error) {
	var msg Message
	var run Run
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		msg, run, err = s.insertMessageAndRunTx(ctx, tx, in)
		return err
	})
	return msg, run, err
}

func (s *Store) insertMessageAndRunTx(ctx context.Context, tx pgx.Tx, in NewRun) (Message, Run, error) {
	var msg Message
	var run Run
	err := func() error {
		var version int64
		if err := tx.QueryRow(ctx, `SELECT config_version FROM agents WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, in.AgentID).Scan(&version); err != nil {
			return notFoundIfNoRows(err, "Agent")
		}
		var state string
		var epoch int
		if err := tx.QueryRow(ctx, `SELECT state, epoch FROM agent_sessions WHERE agent_id = $1`, in.AgentID).Scan(&state, &epoch); err != nil {
			return err
		}
		if state == "missing" {
			return apperr.Conflict(apperr.CodeSessionMissing, "会话文件丢失，请先在控制台确认重开会话")
		}
		status, priority := "queued", 1
		if in.WhenBusy == "interrupt" {
			priority = 0
		}
		var startedAt *time.Time
		var instance *uuid.UUID
		if in.Start {
			now := time.Now()
			status, startedAt, instance = "starting", &now, &in.InstanceID
		}
		msgID := uuid.Must(uuid.NewV7())
		var err error
		origin := "console"
		if in.Origin != "" {
			origin = in.Origin
		}
		if in.TriggerInboxID != nil {
			origin = "inbox_reminder"
		}
		run, err = scanRun(tx.QueryRow(ctx, `INSERT INTO runs (id, agent_id, user_message_id, config_version, instance_id, priority, when_busy, status, started_at,
				origin, trigger_inbox_message_id,wake_sleep_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,$12) RETURNING `+runColumns,
			in.RunID, in.AgentID, msgID, version, instance, priority, nullIfEmpty(in.WhenBusy), status, startedAt, origin, in.TriggerInboxID, in.WakeSleepID))
		if err != nil {
			if isUniqueViolation(err, "runs_one_active_per_agent") {
				return ErrActiveRunExists
			}
			return err
		}
		msg, err = scanMessage(tx.QueryRow(ctx, `INSERT INTO conversation_messages (id, agent_id, role, text, run_id, session_epoch)
			VALUES ($1, $2, 'user', $3, $4, $5) RETURNING `+messageColumns, msgID, in.AgentID, in.Text, run.ID, epoch))
		if err != nil {
			return err
		}
		if len(in.ImageIDs) > 0 {
			msg.Images, err = s.BindMessageImagesTx(ctx, tx, ImageOwner{AgentID: in.AgentID}, msgID, in.ImageIDs, in.MaxImages)
			if err != nil {
				return err
			}
			raw, _ := json.Marshal(msg.Images)
			if _, err = tx.Exec(ctx, `UPDATE conversation_messages SET images=$2 WHERE id=$1`, msgID, raw); err != nil {
				return err
			}
		}

		ev := LifecycleEvent{AgentID: in.AgentID, RunID: &run.ID, Data: map[string]any{}}
		if in.RequestID != "" {
			ev.Data["requestId"] = in.RequestID
		}
		if in.Start {
			ev.Type, ev.At = EventRunDispatched, *startedAt
		} else {
			ev.Type, ev.At = EventRunQueued, run.QueuedAt
			ev.Data["whenBusy"] = in.WhenBusy
		}
		if err := s.insertEvent(ctx, tx, ev); err != nil {
			return err
		}
		if origin != "sleep_wake" {
			if err = s.WakeSleepForWorkTx(ctx, tx, nil, in.AgentID, uuid.Nil, run.ID); err != nil {
				return err
			}
			run, err = scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id=$1`, run.ID))
			if err != nil {
				return err
			}
		}
		if !in.Start && in.Enqueue != nil {
			if qerr := in.Enqueue(run); qerr != nil && in.OnEnqueueError != nil {
				in.OnEnqueueError(qerr)
			}
		}
		return nil
	}()
	return msg, run, err
}

// ReminderOutcome 是写入 Inbox 消息时安排提醒的结果。
type ReminderOutcome struct {
	Created        bool     // 新建了一轮提醒：Message 与 Run 为它的用户消息与执行
	Merged         bool     // 已有同优先级的排队提醒，本条消息并入其中：Run 为那一轮
	SessionMissing bool     // 会话丢失，没有安排提醒；消息照常保存
	Message        *Message // 新建时的用户消息
	Run            *Run
}

// InsertInboxWithReminder 在一个事务中写入发给 Agent 的 Inbox 消息（msg 为空时不写消息）并安排提醒：
// 锁住 Agent 行后，已有同优先级、排队中的提醒时并入它（不改动其触发消息）；否则按 rem 新建一轮。
// 会话丢失时不安排提醒，消息照常保存。rem.Start 为 true 时调用方已取得忙闲锁。
func (s *Store) InsertInboxWithReminder(ctx context.Context, msg *NewInboxMessage, rem NewRun) (ReminderOutcome, error) {
	var out ReminderOutcome
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		out = ReminderOutcome{}
		if msg != nil {
			if err := InsertInboxMessage(ctx, tx, *msg); err != nil {
				return err
			}
		} else if _, err := lockAgent(ctx, tx, rem.AgentID); err != nil {
			return err
		}
		var state string
		if err := tx.QueryRow(ctx, `SELECT state FROM agent_sessions WHERE agent_id = $1`, rem.AgentID).Scan(&state); err != nil {
			return err
		}
		if state == "missing" {
			out.SessionMissing = true
			return nil
		}
		priority := 1
		if rem.WhenBusy == "interrupt" {
			priority = 0
		}
		existing, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs
			WHERE agent_id = $1 AND status = 'queued' AND origin = 'inbox_reminder' AND priority = $2`, rem.AgentID, priority))
		switch {
		case err == nil:
			out.Merged, out.Run = true, &existing
			if msg != nil {
				if err := LinkInboxReminderTx(ctx, tx, msg.ID, existing.ID); err != nil {
					return err
				}
			}
			return s.WakeSleepForWorkTx(ctx, tx, nil, rem.AgentID, uuid.Nil, existing.ID)
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		m, r, err := s.insertMessageAndRunTx(ctx, tx, rem)
		if err != nil {
			return err
		}
		out.Created, out.Message, out.Run = true, &m, &r
		if msg != nil {
			return LinkInboxReminderTx(ctx, tx, msg.ID, r.ID)
		}
		return nil
	})
	return out, err
}

// SetMessageText 改写用户消息的正文（平台提醒那一轮开始执行时写入提醒文字）。
func (s *Store) SetMessageText(ctx context.Context, id uuid.UUID, text string) (Message, error) {
	m, err := scanMessage(s.Pool.QueryRow(ctx, `UPDATE conversation_messages SET text = $2 WHERE id = $1 RETURNING `+messageColumns, id, text))
	return m, notFoundIfNoRows(err, "消息")
}

// GetRun 返回执行记录及其工具状态、用量合计与所用模型。
func (s *Store) GetRun(ctx context.Context, id uuid.UUID) (Run, error) {
	r, err := scanRun(s.Pool.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id = $1`, id))
	if err != nil {
		return r, notFoundIfNoRows(err, "执行")
	}
	return r, s.fillRuns(ctx, &r)
}

// GetMessage 返回一条消息。
func (s *Store) GetMessage(ctx context.Context, id uuid.UUID) (Message, error) {
	m, err := scanMessage(s.Pool.QueryRow(ctx, `SELECT `+messageColumns+` FROM conversation_messages WHERE id = $1`, id))
	return m, notFoundIfNoRows(err, "消息")
}

// ActiveRun 返回 Agent 进行中的执行；没有时返回 nil。
func (s *Store) ActiveRun(ctx context.Context, agentID uuid.UUID) (*Run, error) {
	r, err := scanRun(s.Pool.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE agent_id = $1 AND status IN `+activeRunStatuses, agentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// QueuedRuns 按处理顺序（先打断、后普通，各自先到先处理）返回排队中的执行。
func (s *Store) QueuedRuns(ctx context.Context, agentID uuid.UUID) ([]Run, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+runColumns+` FROM runs WHERE agent_id = $1 AND status = 'queued' ORDER BY priority, queue_seq`, agentID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Run, error) { return scanRun(r) })
}

// AgentsWithQueuedRuns 返回有排队执行的 Agent。
func (s *Store) AgentsWithQueuedRuns(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT agent_id FROM runs WHERE status = 'queued'`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// ActiveRuns 返回全部进行中的执行。
func (s *Store) ActiveRuns(ctx context.Context) ([]Run, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+runColumns+` FROM runs WHERE status IN `+activeRunStatuses)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Run, error) { return scanRun(r) })
}

// StartQueuedRun 把排队中的执行转为 starting 并记录派发事件；已不在排队状态时返回 false（已被处理），保证恰好处理一次。
func (s *Store) StartQueuedRun(ctx context.Context, runID, instanceID uuid.UUID) (Run, bool, error) {
	var r Run
	started := false
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var owner uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT agent_id FROM runs WHERE id=$1`, runID).Scan(&owner); err != nil {
			return err
		}
		if _, err := lockAgent(ctx, tx, owner); err != nil {
			return err
		}
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT origin<>'sleep_wake' OR EXISTS(SELECT 1 FROM agent_sleeps z WHERE z.id=runs.wake_sleep_id AND z.wake_run_id=runs.id AND z.state='woken') FROM runs WHERE id=$1`, runID).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return nil
		}
		var err error
		r, err = scanRun(tx.QueryRow(ctx, `UPDATE runs SET status = 'starting', started_at = now(), instance_id = $2
			WHERE id = $1 AND status = 'queued' RETURNING `+runColumns, runID, instanceID))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		started = true
		return s.insertEvent(ctx, tx, LifecycleEvent{AgentID: r.AgentID, RunID: &r.ID, Type: EventRunDispatched, At: *r.StartedAt})
	})
	if err != nil {
		if isUniqueViolation(err, "runs_one_active_per_agent") {
			return r, false, ErrActiveRunExists
		}
		return r, false, err
	}
	return r, started, nil
}

// SetRunPhase 更新 starting 状态下的阶段，并记录阶段开始事件。
func (s *Store) SetRunPhase(ctx context.Context, runID uuid.UUID, phase string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var agentID uuid.UUID
		err := tx.QueryRow(ctx, `UPDATE runs SET phase = $2 WHERE id = $1 AND status = 'starting' RETURNING agent_id`, runID, phase).Scan(&agentID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, RunID: &runID, Type: EventRunPhase, Data: map[string]any{"phase": phase}})
	})
}

// SetRunRunning 把执行转为 running（打断中的执行保持 aborting），记录实际使用的配置版本、进入回复的时间与事件。
func (s *Store) SetRunRunning(ctx context.Context, runID uuid.UUID, configVersion int64) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var agentID uuid.UUID
		var runningAt time.Time
		err := tx.QueryRow(ctx, `UPDATE runs SET status = CASE WHEN status = 'aborting' THEN status ELSE 'running' END,
				phase = NULL, config_version = $2, running_at = coalesce(running_at, now())
			WHERE id = $1 AND status IN ('starting', 'aborting') AND running_at IS NULL RETURNING agent_id, running_at`, runID, configVersion).Scan(&agentID, &runningAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, RunID: &runID, Type: EventRunRunning, At: runningAt,
			Data: map[string]any{"configVersion": configVersion}})
	})
}

// MarkAborting 把 Agent 进行中的执行转为 aborting，记录发起打断的时间与事件（byRunID 为引发打断的消息所属的执行），
// 返回该执行；没有进行中的执行时返回 nil。引发打断的执行若已被出队并开始，它不会打断自己。
//
// 先以 FOR NO KEY UPDATE 锁 Agent 行，与 FinishRun、StartQueuedRun 一样按"先 Agent、后执行"加锁：
// 否则这里持有执行行、再为写事件等 Agent 的 KEY SHARE，而 FinishRun 持有 Agent、等这一执行行，会成死锁。
func (s *Store) MarkAborting(ctx context.Context, agentID, byRunID uuid.UUID) (*Run, error) {
	var out *Run
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT true FROM agents WHERE id = $1 FOR NO KEY UPDATE`, agentID).Scan(&exists); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		r, err := scanRun(tx.QueryRow(ctx, `UPDATE runs SET status = 'aborting', abort_requested_at = coalesce(abort_requested_at, now())
			WHERE agent_id = $1 AND status IN `+activeRunStatuses+` AND id <> $2 RETURNING `+runColumns, agentID, byRunID))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out = &r
		return s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, RunID: &r.ID, Type: EventRunAbortRequested,
			Data: map[string]any{"byRunId": byRunID.String()}})
	})
	return out, err
}

// RunNotQueuedDetails 是 run_not_queued 冲突的详情：排队项的当前状态。针对 Inbox 消息时带 MessageID 与 MessageState
// （read / cancelled / dispatched / not_queued），RunID 为消息当前的承载执行，没有时为空。
type RunNotQueuedDetails struct {
	RunID        *uuid.UUID `json:"runId"`
	Status       *string    `json:"status"`
	WhenBusy     *string    `json:"whenBusy"`
	MessageID    *uuid.UUID `json:"messageId,omitempty"`
	MessageState string     `json:"messageState,omitempty"`
}

// RunNotActiveDetails 是 run_not_active 冲突的详情：要停止的执行当前的状态。
type RunNotActiveDetails struct {
	RunID  uuid.UUID `json:"runId"`
	Status string    `json:"status"`
}

// RunStatus 返回执行的当前状态。
func (s *Store) RunStatus(ctx context.Context, runID uuid.UUID) (string, error) {
	var st string
	err := s.Pool.QueryRow(ctx, `SELECT status FROM runs WHERE id = $1`, runID).Scan(&st)
	return st, err
}

// RunEnd 是执行结束时的结果。
type RunEnd struct {
	Status        string // completed / failed / interrupted
	EndReason     string // user_interrupt / server_restart（仅 interrupted）
	ErrorCode     string
	ErrorMessage  string
	Text          string
	ConfigVersion int64 // 为 0 时不改
}

// FinishRun 结束一次进行中的执行，写入回复与结束事件。执行已结束时返回 (run, nil, false, nil)，不重复写入。
func (s *Store) FinishRun(ctx context.Context, runID uuid.UUID, end RunEnd) (Run, *Message, bool, error) {
	var run Run
	var msg *Message
	finished := false
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var owner uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT agent_id FROM runs WHERE id=$1`, runID).Scan(&owner); err != nil {
			return err
		}
		if _, err := lockAgent(ctx, tx, owner); err != nil {
			return err
		}
		var err error
		// 被人停止的执行（abort_requested 带 stop:true）以 user_stopped 结束，而不是被新消息打断的 user_interrupt。
		run, err = scanRun(tx.QueryRow(ctx, `UPDATE runs SET status = $2, end_reason = CASE WHEN $3 = 'user_interrupt' AND EXISTS (SELECT 1 FROM lifecycle_events e
				WHERE e.run_id = runs.id AND e.type = 'run.abort_requested' AND e.data->>'stop' = 'true') THEN 'user_stopped' ELSE $3 END, error_code = $4, error_message = $5,
				phase = NULL, ended_at = now(), config_version = CASE WHEN $6 > 0 THEN $6 ELSE config_version END
			WHERE id = $1 AND status IN `+activeRunStatuses+` RETURNING `+runColumns,
			runID, end.Status, nullIfEmpty(end.EndReason), nullIfEmpty(end.ErrorCode), nullIfEmpty(end.ErrorMessage), end.ConfigVersion))
		if errors.Is(err, pgx.ErrNoRows) {
			run, err = scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id = $1`, runID))
			return notFoundIfNoRows(err, "执行")
		}
		if err != nil {
			return err
		}
		finished = true
		if run.Origin != "console_compact" {
			var epoch int
			if err := tx.QueryRow(ctx, `SELECT epoch FROM agent_sessions WHERE agent_id = $1`, run.AgentID).Scan(&epoch); err != nil {
				return err
			}
			m, err := scanMessage(tx.QueryRow(ctx, `INSERT INTO conversation_messages (id, agent_id, role, text, run_id, session_epoch)
				VALUES ($1, $2, 'assistant', $3, $4, $5) RETURNING `+messageColumns, uuid.Must(uuid.NewV7()), run.AgentID, end.Text, run.ID, epoch))
			if err != nil {
				return err
			}
			msg = &m
		}
		data := map[string]any{"status": end.Status}
		if run.EndReason != "" {
			data["endReason"] = run.EndReason
		}
		if end.ErrorCode != "" {
			data["errorCode"] = end.ErrorCode
		}
		if err = s.insertEvent(ctx, tx, LifecycleEvent{AgentID: run.AgentID, RunID: &run.ID, Type: EventRunEnded, At: *run.EndedAt, Data: data}); err != nil {
			return err
		}
		_, err = s.FinishSleepTx(ctx, tx, nil, run.ID, end.Status)
		return err
	})
	if err == nil {
		err = s.fillRuns(ctx, &run)
	}
	return run, msg, finished, err
}

// AddToolEvent 记录一次工具状态。
func (s *Store) AddToolEvent(ctx context.Context, runID uuid.UUID, ev ToolEvent) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO run_tool_events (run_id, seq, tool_call_id, tool_name, state, at)
		VALUES ($1, coalesce((SELECT max(seq) FROM run_tool_events WHERE run_id = $1), 0) + 1, $2, $3, $4, $5)`,
		runID, ev.ToolCallID, ev.ToolName, ev.State, ev.At)
	return err
}

func (s *Store) toolsFor(ctx context.Context, runIDs []uuid.UUID) (map[uuid.UUID][]ToolEvent, error) {
	out := map[uuid.UUID][]ToolEvent{}
	if len(runIDs) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT run_id, tool_call_id, tool_name, state, at FROM run_tool_events WHERE run_id = ANY($1) ORDER BY run_id, seq`, runIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var ev ToolEvent
		if err := rows.Scan(&id, &ev.ToolCallID, &ev.ToolName, &ev.State, &ev.At); err != nil {
			return nil, err
		}
		out[id] = append(out[id], ev)
	}
	return out, rows.Err()
}

// MessageWithRun 是历史中的一条消息及其所属执行。
type MessageWithRun struct {
	Message
	Run Run
}

// MessageQuery 是对话历史的查询方式：Before、After、AroundRun 至多一个；都为空时取最新一页。
type MessageQuery struct {
	Before    *uuid.UUID
	After     *uuid.UUID
	AroundRun *uuid.UUID
	Limit     int
}

// MessagePage 是一页消息及前后是否还有消息。
type MessagePage struct {
	Items        []MessageWithRun
	HasMore      bool // 这一页之前还有更早的消息
	HasMoreAfter bool // 这一页之后还有更新的消息
}

var historySelect = `SELECT m.id, m.agent_id, m.seq, m.role, m.text, m.run_id, m.session_epoch, m.created_at, m.images,
		` + runColumnsOf("r.") + `
	FROM conversation_messages m JOIN runs r ON r.id = m.run_id`

// ListMessages 按查询方式返回一页消息，按时间正序。
func (s *Store) ListMessages(ctx context.Context, agentID uuid.UUID, q MessageQuery) (MessagePage, error) {
	var page MessagePage
	if _, err := s.GetAgent(ctx, agentID); err != nil {
		return page, err
	}
	seqOf := func(id uuid.UUID) (int64, error) {
		var seq int64
		err := s.Pool.QueryRow(ctx, `SELECT seq FROM conversation_messages WHERE id = $1 AND agent_id = $2`, id, agentID).Scan(&seq)
		return seq, notFoundIfNoRows(err, "消息")
	}
	var items []MessageWithRun
	switch {
	case q.AroundRun != nil:
		var anchor int64
		err := s.Pool.QueryRow(ctx, `SELECT m.seq FROM runs r JOIN conversation_messages m ON m.id = r.user_message_id
			WHERE r.id = $1 AND r.agent_id = $2`, *q.AroundRun, agentID).Scan(&anchor)
		if err != nil {
			return page, notFoundIfNoRows(err, "执行")
		}
		nBefore := (q.Limit - 1) / 2
		after, err := s.historyPage(ctx, agentID, `m.seq >= $2 ORDER BY m.seq ASC`, anchor, q.Limit-nBefore+1)
		if err != nil {
			return page, err
		}
		if len(after) > q.Limit-nBefore {
			after, page.HasMoreAfter = after[:q.Limit-nBefore], true
		}
		nBefore = q.Limit - len(after)
		before, err := s.historyPage(ctx, agentID, `m.seq < $2 ORDER BY m.seq DESC`, anchor, nBefore+1)
		if err != nil {
			return page, err
		}
		if len(before) > nBefore {
			before, page.HasMore = before[:nBefore], true
		}
		reverse(before)
		items = append(before, after...)
	case q.After != nil:
		seq, err := seqOf(*q.After)
		if err != nil {
			return page, err
		}
		if items, err = s.historyPage(ctx, agentID, `m.seq > $2 ORDER BY m.seq ASC`, seq, q.Limit+1); err != nil {
			return page, err
		}
		if len(items) > q.Limit {
			items, page.HasMoreAfter = items[:q.Limit], true
		}
		page.HasMore = true // 锚点消息本身更早
	default:
		seq := int64(1<<62 - 1)
		if q.Before != nil {
			var err error
			if seq, err = seqOf(*q.Before); err != nil {
				return page, err
			}
			page.HasMoreAfter = true // 锚点消息本身更新
		}
		var err error
		if items, err = s.historyPage(ctx, agentID, `m.seq < $2 ORDER BY m.seq DESC`, seq, q.Limit+1); err != nil {
			return page, err
		}
		if len(items) > q.Limit {
			items, page.HasMore = items[:q.Limit], true
		}
		reverse(items)
	}
	runs := make([]*Run, len(items))
	for i := range items {
		runs[i] = &items[i].Run
	}
	if err := s.fillRuns(ctx, runs...); err != nil {
		return page, err
	}
	page.Items = items
	if page.Items == nil {
		page.Items = []MessageWithRun{}
	}
	return page, nil
}

func (s *Store) historyPage(ctx context.Context, agentID uuid.UUID, cond string, seq int64, limit int) ([]MessageWithRun, error) {
	rows, err := s.Pool.Query(ctx, historySelect+` WHERE m.agent_id = $1 AND `+cond+` LIMIT $3`, agentID, seq, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (MessageWithRun, error) {
		var it MessageWithRun
		m := &it.Message
		var raw []byte
		dest := append([]any{&m.ID, &m.AgentID, &m.Seq, &m.Role, &m.Text, &m.RunID, &m.SessionEpoch, &m.CreatedAt, &raw}, runFields(&it.Run)...)
		if e := row.Scan(dest...); e != nil {
			return it, e
		}
		e := json.Unmarshal(raw, &m.Images)
		return it, e
	})
}

func reverse[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
