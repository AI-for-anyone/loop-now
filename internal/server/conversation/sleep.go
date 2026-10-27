package conversation

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"loop-now/internal/server/sleepmodel"
	"time"
)

// StartSleepMaintenance starts only after normal and task restart recovery.
func (s *Service) StartSleepMaintenance(interval time.Duration, batch int, kickTask func(uuid.UUID)) {
	if interval <= 0 {
		interval = time.Second
	}
	if batch <= 0 {
		batch = 100
	}
	s.wg.Go(func() {
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			if err := s.st.ReconcileSleeps(s.ctx, batch); err != nil {
				s.log.Warn("sleep对账失败", "code", "sleep_reconcile_failed")
			}
			rows, err := s.st.Pool.Query(s.ctx, `SELECT DISTINCT agent_id,task_id FROM agent_sleeps`)
			if err == nil {
				type owner struct {
					agent uuid.UUID
					task  *uuid.UUID
				}
				var owners []owner
				for rows.Next() {
					var o owner
					if rows.Scan(&o.agent, &o.task) == nil {
						owners = append(owners, o)
					}
				}
				rows.Close()
				for _, o := range owners {
					if o.task == nil {
						s.EmitAgentStatus(o.agent)
						s.kick(s.ctx, o.agent)
					} else if kickTask != nil {
						kickTask(*o.task)
					}
				}
			}
			select {
			case <-s.ctx.Done():
				return
			case <-tick.C:
			}
		}
	})
}
func (s *Service) emitSleep(ctx context.Context, agent uuid.UUID) {
	var raw []byte
	// Read the latest state and its revision together, including a terminal reference.
	err := s.st.Pool.QueryRow(ctx, `SELECT jsonb_build_object('scopeKind',z.scope_kind,'agentId',z.agent_id,'taskId',z.task_id,'runtimeId',z.runtime_id,'nodeRunId',z.node_run_id,'sleepRevision',a.sleep_revision,'sleep',NULL,'endedSleepId',CASE WHEN z.state IN ('woken','cancelled') THEN z.id ELSE NULL END) FROM agent_sessions a JOIN LATERAL(SELECT * FROM agent_sleeps WHERE agent_id=a.agent_id AND task_id IS NULL ORDER BY revision DESC LIMIT 1) z ON true WHERE a.agent_id=$1`, agent).Scan(&raw)
	if err != nil {
		return
	}
	var event sleepmodel.Updated
	if json.Unmarshal(raw, &event) != nil {
		return
	}
	if old, ok := s.sleepRevisions.Load(agent); ok && old.(int64) >= event.SleepRevision {
		return
	}
	rec, revision, err := s.st.ActiveSleepProjection(ctx, agent, uuid.Nil)
	if err != nil || revision != event.SleepRevision {
		return
	}
	event.Sleep = rec
	if s.emit(ctx, agent, "sleep.updated", event) {
		s.sleepRevisions.Store(agent, event.SleepRevision)
	}
}
