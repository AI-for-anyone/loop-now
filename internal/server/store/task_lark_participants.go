package store

import (
	"context"
	"os"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/taskruntime"
)

// TaskLarkParticipants is a console read model, never an authorization to
// dispatch. The task version and every participant come from one read-only
// snapshot. It does not consult current Agent config/account/grant state.
func (s *Store) TaskLarkParticipants(ctx context.Context, id uuid.UUID) (taskmodel.Detail, []taskmodel.LarkSwitchParticipant, error) {
	var r taskmodel.Record
	out := []taskmodel.LarkSwitchParticipant{}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return r.View, nil, err
	}
	defer tx.Rollback(ctx)
	var raw []byte
	var projectName string
	if err = tx.QueryRow(ctx, `SELECT t.document,p.name FROM task_records t JOIN projects p ON p.id=t.project_id WHERE t.id=$1`, id).Scan(&raw, &projectName); err != nil {
		return r.View, nil, notFoundIfNoRows(err, "任务")
	}
	if err = decodeTaskJSON(raw, &r); err != nil {
		return r.View, nil, err
	}
	r.View.Task.Link = taskLink(projectName, id)
	if err = s.ProjectTaskSleepsTx(ctx, tx, &r.View); err != nil {
		return r.View, nil, err
	}
	type row struct {
		agent, runtime    uuid.UUID
		snapshot          []byte
		snapshotSHA       string
		prepared, retired bool
		sessionState      string
		sessionFile       *string
		revision          int64
		bindingSHA        *string
		bindingAgent      *uuid.UUID
		fenced            bool
		hadTurn           bool
	}
	rows, err := tx.Query(ctx, `SELECT i.agent_id,i.id,i.snapshot,i.snapshot_sha,i.prepared,i.retired,i.session_state,i.session_file,coalesce(b.revision,0),b.base_snapshot_sha,b.agent_id,EXISTS(SELECT 1 FROM task_lark_switch_fences f WHERE f.runtime_id=i.id),EXISTS(SELECT 1 FROM task_turns t WHERE t.task_id=i.task_id AND t.runtime_id=i.id) FROM task_instances i LEFT JOIN LATERAL (SELECT p.revision,p.base_snapshot_sha,p.agent_id FROM task_lark_binding_members p JOIN task_lark_credential_bindings b ON b.task_id=p.task_id AND b.revision=p.revision JOIN task_lark_switch_actions a ON a.task_id=b.task_id AND a.actor_key=b.actor_key AND a.action_id=b.action_id AND a.status='succeeded' WHERE p.task_id=i.task_id AND p.runtime_id=i.id ORDER BY p.revision DESC LIMIT 1) b ON true WHERE i.task_id=$1 ORDER BY i.id`, id)
	if err != nil {
		return r.View, nil, err
	}
	instances, err := pgx.CollectRows(rows, func(q pgx.CollectableRow) (row, error) {
		var v row
		e := q.Scan(&v.agent, &v.runtime, &v.snapshot, &v.snapshotSHA, &v.prepared, &v.retired, &v.sessionState, &v.sessionFile, &v.revision, &v.bindingSHA, &v.bindingAgent, &v.fenced, &v.hadTurn)
		return v, e
	})
	if err != nil {
		return r.View, nil, err
	}
	for _, v := range instances {
		p := taskmodel.LarkSwitchParticipant{AgentID: v.agent, RuntimeID: v.runtime, BaseSnapshotSHA: v.snapshotSHA, BindingRevision: v.revision, Prepared: v.prepared, Available: true, StepIDs: []string{}}
		for _, step := range r.View.Workflow.Steps {
			if step.AgentID == v.agent {
				p.StepIDs = append(p.StepIDs, step.ID)
			}
		}
		deny := func(reason string) {
			if p.UnavailableReason == nil {
				p.UnavailableReason = &reason
			}
			p.Available = false
		}
		snap, e := taskruntime.Decode(v.snapshot)
		if e != nil || snap.Scope().TaskID() != id || snap.Scope().AgentID() != v.agent {
			deny("snapshot_unavailable")
		} else {
			cfg := snap.Configuration()
			if _, e = snap.Credentials(); e != nil || (cfg.Prompts != nil && cfg.Prompts.ProjectID != nil && *cfg.Prompts.ProjectID != r.View.Task.ProjectID) {
				deny("snapshot_unavailable")
			} else {
				allowed := slices.Contains(cfg.CLIs, "lark-cli")
				p.LarkAllowed = &allowed
				if !allowed {
					deny("lark_not_allowed")
				}
			}
		}
		if v.revision > 0 && (v.bindingSHA == nil || *v.bindingSHA != p.BaseSnapshotSHA || v.bindingAgent == nil || *v.bindingAgent != v.agent) {
			deny("binding_unavailable")
		}
		if r.Ended() || v.retired {
			deny("runtime_unavailable")
		}
		if v.fenced {
			deny("handoff_in_progress")
		}
		current := r.Current()
		future := (current == nil || current.RuntimeID != v.runtime) && !v.hadTurn && v.sessionState == "none" && v.sessionFile == nil
		if !future {
			if v.sessionState != "active" || v.sessionFile == nil || *v.sessionFile == "" {
				deny("session_unavailable")
			} else if info, e := os.Stat(*v.sessionFile); e != nil || !info.Mode().IsRegular() {
				deny("session_unavailable")
			}
		}
		out = append(out, p)
	}
	return r.View, out, tx.Commit(ctx)
}
