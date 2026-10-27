package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
)

// TaskNodeRecord reads historical authority from the turn, never current Agent
// configuration or a live instance. Cleanup does not invalidate this history.
func (s *Store) TaskNodeRecord(ctx context.Context, task, node, run uuid.UUID) (TaskTurn, uuid.UUID, error) {
	var agent uuid.UUID
	t, err := scanTaskTurn(s.Pool.QueryRow(ctx, `SELECT `+taskTurnColumns+` FROM task_turns WHERE id=$1 AND task_id=$2 AND node_run_id=$3`, run, task, node))
	if err != nil {
		return t, agent, notFoundIfNoRows(err, "任务执行")
	}
	err = s.Pool.QueryRow(ctx, `SELECT agent_id FROM task_instances WHERE id=$1 AND task_id=$2`, t.RuntimeID, task).Scan(&agent)
	return t, agent, notFoundIfNoRows(err, "任务实例")
}

// TaskNodeEvents pages actual task observations. Cursor is (at,id), not a Redis
// cursor or the unrelated decimal task SSE event ID.
func (s *Store) TaskNodeEvents(ctx context.Context, t TaskTurn, cursor string, limit int) ([]LifecycleEvent, string, error) {
	if limit < 1 || limit > 200 {
		return nil, "", apperr.Invalid("limit", "必须为1..200")
	}
	var at *time.Time
	var id *uuid.UUID
	if cursor != "" {
		a, b, e := parseTaskCursor(cursor)
		if e != nil {
			return nil, "", e
		}
		at = &a
		id = &b
	}
	rows, e := s.Pool.Query(ctx, `SELECT id,at,agent_id,type,data FROM lifecycle_events WHERE task_id=$1 AND task_turn_id=$2 AND workflow_node=$3 AND ($4::timestamptz IS NULL OR (at,id)>($4,$5)) ORDER BY at,id LIMIT $6`, t.TaskID, t.ID, t.NodeRunID.String(), at, id, limit+1)
	if e != nil {
		return nil, "", e
	}
	events, e := pgx.CollectRows(rows, func(r pgx.CollectableRow) (LifecycleEvent, error) {
		var v LifecycleEvent
		var raw []byte
		e := r.Scan(&v.ID, &v.At, &v.AgentID, &v.Type, &raw)
		if e == nil {
			e = json.Unmarshal(raw, &v.Data)
		}
		return v, e
	})
	if e != nil {
		return nil, "", e
	}
	next := ""
	if len(events) > limit {
		events = events[:limit]
		last := events[len(events)-1]
		next = taskCursor(last.At, last.ID)
	}
	return events, next, nil
}

func (s *Store) TaskNodeUsage(ctx context.Context, t TaskTurn) (UsageTotals, error) {
	var v UsageTotals
	e := s.Pool.QueryRow(ctx, `SELECT count(*),coalesce(sum(input),0),coalesce(sum(output),0),coalesce(sum(cache_read),0),coalesce(sum(cache_write),0),coalesce(sum(total),0) FROM usage_records WHERE task_id=$1 AND task_turn_id=$2`, t.TaskID, t.ID).Scan(&v.Calls, &v.Input, &v.Output, &v.CacheRead, &v.CacheWrite, &v.Total)
	return v, e
}

func (s *Store) TaskNodeUsageRecords(ctx context.Context, t TaskTurn) ([]UsageRecord, error) {
	rows, e := s.Pool.Query(ctx, `SELECT `+usageColumns+` FROM usage_records WHERE task_id=$1 AND task_turn_id=$2 ORDER BY at,id`, t.TaskID, t.ID)
	if e != nil {
		return nil, e
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (UsageRecord, error) { return scanUsage(r) })
}

// TaskNodeAllEvents takes a single statement snapshot so tool start/end facts
// can be coalesced before paging projected timeline entries.
func (s *Store) TaskNodeAllEvents(ctx context.Context, t TaskTurn) ([]LifecycleEvent, error) {
	rows, e := s.Pool.Query(ctx, `SELECT id,at,agent_id,type,data FROM lifecycle_events WHERE task_id=$1 AND task_turn_id=$2 AND workflow_node=$3 ORDER BY at,id`, t.TaskID, t.ID, t.NodeRunID.String())
	if e != nil {
		return nil, e
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (LifecycleEvent, error) {
		var ev LifecycleEvent
		var raw []byte
		e := r.Scan(&ev.ID, &ev.At, &ev.AgentID, &ev.Type, &raw)
		if e == nil {
			e = json.Unmarshal(raw, &ev.Data)
		}
		return ev, e
	})
}
func TaskNodeRecordCursor(at time.Time, id uuid.UUID) string { return taskCursor(at, id) }
func ParseTaskNodeRecordCursor(cursor string) (time.Time, uuid.UUID, error) {
	return parseTaskCursor(cursor)
}
