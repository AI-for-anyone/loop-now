package store

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/taskmodel"
	"slices"
	"strings"
)

type InboxLockPlan struct {
	AllowEndedTaskHistory bool
	Parties               []Party
	Tasks, Projects       []uuid.UUID
	MessageID             uuid.UUID
}
type InboxTransaction struct {
	Tx    pgx.Tx
	Tasks map[uuid.UUID]*TaskMutation
}

func sortedIDs(ids []uuid.UUID) []uuid.UUID {
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return slices.Compact(ids)
}

// InboxTransaction locks the complete project/normal-agent/task set once. No
// callback may enter another Store transaction or acquire an earlier lock.
func (s *Store) InboxTransaction(ctx context.Context, plan InboxLockPlan, fn func(*InboxTransaction) error) error {
	changed := false
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if plan.MessageID != uuid.Nil {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "inbox-write:"+plan.MessageID.String()); err != nil {
				return err
			}
		}
		projects := slices.Clone(plan.Projects)
		tasks := slices.Clone(plan.Tasks)
		agents := []uuid.UUID{}
		identities := []uuid.UUID{}
		for _, p := range plan.Parties {
			switch p.Kind {
			case "agent":
				if p.RuntimeID != uuid.Nil {
					var task uuid.UUID
					if err := tx.QueryRow(ctx, `SELECT task_id FROM task_instances WHERE id=$1 AND agent_id=$2`, p.RuntimeID, p.ID).Scan(&task); err != nil {
						return notFoundIfNoRows(err, "任务实例")
					}
					tasks = append(tasks, task)
				} else {
					agents = append(agents, p.ID)
					var pid *uuid.UUID
					if err := tx.QueryRow(ctx, `SELECT project_id FROM agents WHERE id=$1 AND deleted_at IS NULL`, p.ID).Scan(&pid); err != nil {
						return notFoundIfNoRows(err, "Agent")
					}
					if pid != nil {
						projects = append(projects, *pid)
					}
				}
			case "external":
				identities = append(identities, p.ID)
			case "console", "platform", "plugin":
			default:
				return apperr.Invalid("identity", "不支持的Inbox身份")
			}
		}
		tasks = sortedIDs(tasks)
		for _, id := range tasks {
			var pid uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT project_id FROM task_records WHERE id=$1`, id).Scan(&pid); err != nil {
				return notFoundIfNoRows(err, "任务")
			}
			projects = append(projects, pid)
		}
		names := map[uuid.UUID]string{}
		managers := map[uuid.UUID]*uuid.UUID{}
		for _, pid := range sortedIDs(projects) {
			var name string
			var manager *uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT name,manager_agent_id FROM projects WHERE id=$1 AND (deleted_at IS NULL OR $2) FOR SHARE`, pid, plan.AllowEndedTaskHistory).Scan(&name, &manager); err != nil {
				return notFoundIfNoRows(err, "项目")
			}
			names[pid] = name
			managers[pid] = manager
		}
		for _, id := range sortedIDs(agents) {
			if _, err := lockAgent(ctx, tx, id); err != nil {
				return err
			}
		}
		for _, id := range sortedIDs(identities) {
			if err := lockLiveIdentity(ctx, tx, id); err != nil {
				return err
			}
		}
		scope := &InboxTransaction{Tx: tx, Tasks: map[uuid.UUID]*TaskMutation{}}
		for _, id := range tasks {
			var data []byte
			if err := tx.QueryRow(ctx, `SELECT document FROM task_records WHERE id=$1 FOR UPDATE`, id).Scan(&data); err != nil {
				return err
			}
			var record taskmodel.Record
			if err := decodeTaskJSON(data, &record); err != nil {
				return err
			}
			if plan.AllowEndedTaskHistory && !record.Ended() {
				return apperr.Conflict("task_conflict", "仅允许读取已结束任务的问题历史")
			}
			record.View.Task.Link = taskLink(names[record.View.Task.ProjectID], id)
			scope.Tasks[id] = &TaskMutation{Tx: tx, Record: &record, ManagerID: managers[record.View.Task.ProjectID], unchanged: true}
		}
		if err := fn(scope); err != nil {
			return err
		}
		for _, id := range tasks {
			m := scope.Tasks[id]
			if !m.unchanged {
				if err := s.ValidateTaskSleepsTx(ctx, m); err != nil {
					return err
				}
				if err := saveTask(ctx, tx, m.Record); err != nil {
					return err
				}
				if err := emitTaskEvents(ctx, tx, m); err != nil {
					return err
				}
				changed = true
			}
		}
		return nil
	})
	if err == nil && changed && s.TaskCommitted != nil {
		s.TaskCommitted()
	}
	return err
}

// QueueInboxReminderTx requires the normal Agent lock acquired by InboxTransaction.
func (s *Store) QueueInboxReminderTx(ctx context.Context, tx pgx.Tx, msg NewInboxMessage) error {
	var state string
	if err := tx.QueryRow(ctx, `SELECT state FROM agent_sessions WHERE agent_id=$1`, msg.To.ID).Scan(&state); err != nil {
		return err
	}
	if state == "missing" {
		return nil
	}
	priority := 1
	if msg.WhenBusy == "interrupt" {
		priority = 0
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE agent_id=$1 AND status='queued' AND origin='inbox_reminder' AND priority=$2)`, msg.To.ID, priority).Scan(&exists); err != nil {
		return err
	}
	if exists {
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM runs WHERE agent_id=$1 AND status='queued' AND origin='inbox_reminder' AND priority=$2`, msg.To.ID, priority).Scan(&id); err != nil {
			return err
		}
		if err := LinkInboxReminderTx(ctx, tx, msg.ID, id); err != nil {
			return err
		}
		return s.WakeSleepForWorkTx(ctx, tx, nil, msg.To.ID, uuid.Nil, id)
	}
	instance := uuid.Nil
	if s.InstanceID != nil {
		instance = *s.InstanceID
	}
	_, r, err := s.insertMessageAndRunTx(ctx, tx, NewRun{AgentID: msg.To.ID, RunID: uuid.New(), InstanceID: instance, Text: "平台提醒", WhenBusy: msg.WhenBusy, TriggerInboxID: &msg.ID})
	if err != nil {
		return err
	}
	return LinkInboxReminderTx(ctx, tx, msg.ID, r.ID)
}
func InboxMessageExistsTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (bool, error) {
	var found bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM inbox_messages WHERE id=$1)`, id).Scan(&found)
	return found, err
}
