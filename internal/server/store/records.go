package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// 运行记录事件的类型（lifecycle_events.type）。
const (
	EventRunQueued         = "run.queued"
	EventRunPromoted       = "run.promoted"
	EventMessageCancelled  = "run.message_cancelled"
	EventRunDispatched     = "run.dispatched"
	EventRunPhase          = "run.phase"
	EventRunRunning        = "run.running"
	EventRunAbortRequested = "run.abort_requested"
	EventRunEnded          = "run.ended"
	EventCLIInstall        = "cli.install"
	EventWorkspaceSync     = "workspace.sync"
	EventPiStarted         = "pi.started"
	EventPiReused          = "pi.reused"
	EventPiStopped         = "pi.stopped"
	EventPiExited          = "pi.exited"
	EventModelRetryStarted = "model.retry_started"
	EventModelRetryEnded   = "model.retry_ended"
	EventContextCompaction = "context.compaction"
	EventSessionMissing    = "session.missing"
	EventSessionReset      = "session.reset"
	EventExtensionUI       = "extension.ui"
)

// 用量的类别（usage_records.kind）。
const (
	UsageAssistant  = "assistant"
	UsageTool       = "tool"
	UsageCompaction = "compaction"
	UsageCacheWarm  = "cache_warm"
	UsageOther      = "other"
)

// LifecycleEvent 是一条运行记录事件。
type LifecycleEvent struct {
	ID      uuid.UUID
	At      time.Time
	AgentID uuid.UUID
	RunID   *uuid.UUID
	Type    string
	Data    map[string]any
}

// UsageRecord 是一条用量记录。Provider 与 Model 都为空表示未知模型。
type UsageRecord struct {
	ID            uuid.UUID
	At            time.Time
	AgentID       uuid.UUID
	RunID         *uuid.UUID
	Kind          string
	Provider      string
	Model         string
	ResponseModel string
	StopReason    string
	Input         int64
	Output        int64
	CacheRead     int64
	CacheWrite    int64
	Reasoning     *int64
	Total         int64 // 只读：由数据库按四项之和生成
	DurationMs    *int64
}

// UsageTotals 是若干条用量之和。
type UsageTotals struct {
	Calls      int64
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Total      int64
}

// ModelRef 是提供方与模型 ID。
type ModelRef struct {
	Provider string
	Model    string
}

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// insertEvent 写入一条事件；At 为零时取当前时间。可以在事务中调用。
func (s *Store) insertEvent(ctx context.Context, q execer, ev LifecycleEvent) error {
	if ev.ID == uuid.Nil {
		ev.ID = uuid.Must(uuid.NewV7())
	}
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	data := ev.Data
	if data == nil {
		data = map[string]any{}
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	// 平台提醒那一轮的事件记下创建它的 Inbox 消息（runs.trigger_inbox_message_id）；普通对话为空。
	_, err = q.Exec(ctx, `INSERT INTO lifecycle_events (id, at, agent_id, run_id, type, data, instance_id, inbox_message_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, (SELECT trigger_inbox_message_id FROM runs WHERE id = $4))`,
		ev.ID, ev.At, ev.AgentID, ev.RunID, ev.Type, b, s.InstanceID)
	return err
}

// AddEvent 写入一条运行记录事件。
func (s *Store) AddEvent(ctx context.Context, ev LifecycleEvent) error {
	return s.insertEvent(ctx, s.Pool, ev)
}

// AddUsage 写入一条用量记录。
func (s *Store) AddUsage(ctx context.Context, u UsageRecord) error {
	if u.ID == uuid.Nil {
		u.ID = uuid.Must(uuid.NewV7())
	}
	if u.At.IsZero() {
		u.At = time.Now()
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO usage_records (id, at, agent_id, run_id, kind, provider, model, response_model, stop_reason,
			input, output, cache_read, cache_write, reasoning, duration_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		u.ID, u.At, u.AgentID, u.RunID, u.Kind, nullIfEmpty(u.Provider), nullIfEmpty(u.Model), nullIfEmpty(u.ResponseModel), nullIfEmpty(u.StopReason),
		u.Input, u.Output, u.CacheRead, u.CacheWrite, u.Reasoning, u.DurationMs)
	return err
}

// RunEvents 返回一次执行的全部事件，按时间排序。
func (s *Store) RunEvents(ctx context.Context, runID uuid.UUID) ([]LifecycleEvent, error) {
	return s.queryEvents(ctx, `SELECT id, at, agent_id, run_id, type, data FROM lifecycle_events WHERE run_id = $1 ORDER BY at, id`, runID)
}

// AgentEvents 返回一个 Agent 的全部事件，按时间排序（测试与排查用）。
func (s *Store) AgentEvents(ctx context.Context, agentID uuid.UUID) ([]LifecycleEvent, error) {
	return s.queryEvents(ctx, `SELECT id, at, agent_id, run_id, type, data FROM lifecycle_events WHERE agent_id = $1 ORDER BY at, id`, agentID)
}

func (s *Store) queryEvents(ctx context.Context, sql string, args ...any) ([]LifecycleEvent, error) {
	rows, err := s.Pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (LifecycleEvent, error) {
		var ev LifecycleEvent
		var raw []byte
		if err := row.Scan(&ev.ID, &ev.At, &ev.AgentID, &ev.RunID, &ev.Type, &raw); err != nil {
			return ev, err
		}
		err := json.Unmarshal(raw, &ev.Data)
		return ev, err
	})
}

const usageColumns = `id, at, agent_id, run_id, kind, coalesce(provider, ''), coalesce(model, ''), coalesce(response_model, ''),
	coalesce(stop_reason, ''), input, output, cache_read, cache_write, reasoning, total, duration_ms`

func scanUsage(row pgx.Row) (UsageRecord, error) {
	var u UsageRecord
	err := row.Scan(&u.ID, &u.At, &u.AgentID, &u.RunID, &u.Kind, &u.Provider, &u.Model, &u.ResponseModel,
		&u.StopReason, &u.Input, &u.Output, &u.CacheRead, &u.CacheWrite, &u.Reasoning, &u.Total, &u.DurationMs)
	return u, err
}

// RunUsage 返回一次执行的全部用量记录，按时间排序。
func (s *Store) RunUsage(ctx context.Context, runID uuid.UUID) ([]UsageRecord, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+usageColumns+` FROM usage_records WHERE run_id = $1 ORDER BY at, id`, runID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (UsageRecord, error) { return scanUsage(r) })
}

// AgentUsage 返回一个 Agent 的全部用量记录，按时间排序（测试与排查用）。
func (s *Store) AgentUsage(ctx context.Context, agentID uuid.UUID) ([]UsageRecord, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+usageColumns+` FROM usage_records WHERE agent_id = $1 ORDER BY at, id`, agentID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (UsageRecord, error) { return scanUsage(r) })
}

// usageFor 返回各执行的用量合计，以及最后一次 assistant 调用的提供方与模型。
func (s *Store) usageFor(ctx context.Context, runIDs []uuid.UUID) (map[uuid.UUID]UsageTotals, map[uuid.UUID]ModelRef, error) {
	totals := map[uuid.UUID]UsageTotals{}
	models := map[uuid.UUID]ModelRef{}
	if len(runIDs) == 0 {
		return totals, models, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT run_id, count(*), sum(input), sum(output), sum(cache_read), sum(cache_write), sum(total)
		FROM usage_records WHERE run_id = ANY($1) GROUP BY run_id`, runIDs)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var t UsageTotals
		if err := rows.Scan(&id, &t.Calls, &t.Input, &t.Output, &t.CacheRead, &t.CacheWrite, &t.Total); err != nil {
			return nil, nil, err
		}
		totals[id] = t
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	rows, err = s.Pool.Query(ctx, `SELECT DISTINCT ON (run_id) run_id, provider, model FROM usage_records
		WHERE run_id = ANY($1) AND kind = 'assistant' AND provider IS NOT NULL ORDER BY run_id, at DESC, id DESC`, runIDs)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var m ModelRef
		if err := rows.Scan(&id, &m.Provider, &m.Model); err != nil {
			return nil, nil, err
		}
		models[id] = m
	}
	return totals, models, rows.Err()
}

// fillRuns 为执行补上工具状态、用量合计与所用模型。
func (s *Store) fillRuns(ctx context.Context, runs ...*Run) error {
	ids := make([]uuid.UUID, 0, len(runs))
	for _, r := range runs {
		ids = append(ids, r.ID)
	}
	tools, err := s.toolsFor(ctx, ids)
	if err != nil {
		return err
	}
	totals, models, err := s.usageFor(ctx, ids)
	if err != nil {
		return err
	}
	for _, r := range runs {
		r.Tools = tools[r.ID]
		if r.Tools == nil {
			r.Tools = []ToolEvent{}
		}
		r.Usage = totals[r.ID]
		if m, ok := models[r.ID]; ok {
			r.Model = &m
		}
	}
	return nil
}
