package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"loop-now/internal/server/apperr"
)

// InsertConsoleCompactRun 在 Agent 行锁内再次确认空闲和活动会话，插入不带对话消息的压缩运行。
func (s *Store) InsertConsoleCompactRun(ctx context.Context, agentID, runID, instanceID uuid.UUID) (Run, error) {
	var run Run
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		agent, err := lockAgent(ctx, tx, agentID)
		if err != nil {
			return err
		}
		if err := ensureIdle(ctx, tx, agentID); err != nil {
			return err
		}
		var state string
		var file *string
		if err := tx.QueryRow(ctx, `SELECT state, session_file FROM agent_sessions WHERE agent_id=$1`, agentID).Scan(&state, &file); err != nil {
			return err
		}
		if state == "missing" {
			return apperr.Conflict(apperr.CodeSessionMissing, "会话文件丢失，请先在控制台确认重开会话")
		}
		if state != "active" || file == nil {
			return apperr.Conflict(apperr.CodeSessionNone, "当前还没有会话，无需压缩")
		}
		now := time.Now()
		run, err = scanRun(tx.QueryRow(ctx, `INSERT INTO runs (id, agent_id, user_message_id, config_version, instance_id, priority, status, started_at, origin)
			VALUES ($1,$2,NULL,$3,$4,1,'starting',$5,'console_compact') RETURNING `+runColumns,
			runID, agentID, agent.ConfigVersion, instanceID, now))
		if err != nil {
			if isUniqueViolation(err, "runs_one_active_per_agent") {
				return ErrActiveRunExists
			}
			return err
		}
		return s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, RunID: &run.ID, Type: EventRunDispatched, At: now})
	})
	return run, err
}

// IdleSession 在数据库内确认 Agent 没有进行中或排队工作，并返回当前会话。
func (s *Store) IdleSession(ctx context.Context, agentID uuid.UUID) (AgentSession, error) {
	var sess AgentSession
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := lockAgent(ctx, tx, agentID); err != nil {
			return err
		}
		if err := ensureIdle(ctx, tx, agentID); err != nil {
			return err
		}
		var file *string
		if err := tx.QueryRow(ctx, `SELECT session_file, state, epoch, updated_at FROM agent_sessions WHERE agent_id=$1`, agentID).
			Scan(&file, &sess.State, &sess.Epoch, &sess.UpdatedAt); err != nil {
			return err
		}
		if file != nil {
			sess.SessionFile = *file
		}
		return nil
	})
	return sess, err
}

// LatestCompletedAssistant 返回最近一条属于成功运行的助手回复；没有时返回 nil。
func (s *Store) LatestCompletedAssistant(ctx context.Context, agentID uuid.UUID) (*Message, error) {
	if _, err := s.GetAgent(ctx, agentID); err != nil {
		return nil, err
	}
	m, err := scanMessage(s.Pool.QueryRow(ctx, `SELECT `+messageColumns+` FROM conversation_messages
		WHERE agent_id=$1 AND role='assistant' AND run_id IN (SELECT id FROM runs WHERE agent_id=$1 AND status='completed')
		ORDER BY seq DESC LIMIT 1`, agentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}
