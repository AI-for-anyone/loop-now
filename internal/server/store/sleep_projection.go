package store

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/taskmodel"
)

// ProjectTaskSleepsTx reads under the same transaction snapshot/owner lock as
// task.version. It is a projection only; agent_sleeps remains the authority.
func (s *Store) ProjectTaskSleepsTx(ctx context.Context, tx pgx.Tx, d *taskmodel.Detail) error {
	d.SleepingInstances = []sleepmodel.Record{}
	revisions := map[uuid.UUID]int64{}
	rows, e := tx.Query(ctx, `SELECT id,sleep_revision FROM task_instances WHERE task_id=$1`, d.Task.ID)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id uuid.UUID
		var rev int64
		if e = rows.Scan(&id, &rev); e != nil {
			rows.Close()
			return e
		}
		revisions[id] = rev
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for i := range d.Nodes {
		d.Nodes[i].Sleep = nil
		d.Nodes[i].SleepRevision = revisions[d.Nodes[i].RuntimeID]
	}
	rows, e = tx.Query(ctx, `SELECT `+sleepColumns+` FROM agent_sleeps WHERE task_id=$1 AND state='sleeping' ORDER BY runtime_id`, d.Task.ID)
	if e != nil {
		return e
	}
	records, e := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Sleep, error) { return scanSleep(row) })
	if e != nil {
		return e
	}
	for _, r := range records {
		d.SleepingInstances = append(d.SleepingInstances, r.Record)
		if r.Mode != "task_node" || r.NodeRunID == nil {
			continue
		}
		for i := range d.Nodes {
			if d.Nodes[i].ID == *r.NodeRunID {
				record := r.Record
				d.Nodes[i].Sleep = &record
			}
		}
	}
	return nil
}
