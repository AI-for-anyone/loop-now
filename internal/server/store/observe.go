package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// UsageRow 是按（Agent、提供方、模型）分组的用量统计；Provider 与 Model 都为空表示未知模型。
type UsageRow struct {
	AgentID      uuid.UUID
	AgentName    string
	AgentDeleted bool
	ProjectID    *uuid.UUID // 平台 Agent 为空；项目可能已删除
	Provider     string
	Model        string
	UsageTotals
}

// UsageSummary 统计范围内全部 Agent（含已删除的 Agent 与已删除项目中的 Agent）在 [from, to) 内的用量；
// from、to 为空表示不限。按总数降序。
func (s *Store) UsageSummary(ctx context.Context, f ScopeFilter, from, to *time.Time) ([]UsageRow, error) {
	pid, platform := f.args()
	rows, err := s.Pool.Query(ctx, `SELECT a.id, a.name, a.deleted_at IS NOT NULL, a.project_id, coalesce(u.provider, ''), coalesce(u.model, ''),
			count(*), sum(u.input), sum(u.output), sum(u.cache_read), sum(u.cache_write), sum(u.total)
		FROM usage_records u JOIN agents a ON a.id = u.agent_id
		WHERE ($1::uuid IS NULL OR a.project_id = $1) AND (NOT $2 OR a.project_id IS NULL)
			AND ($3::timestamptz IS NULL OR u.at >= $3) AND ($4::timestamptz IS NULL OR u.at < $4)
		GROUP BY a.id, a.name, a.deleted_at, a.project_id, u.provider, u.model
		ORDER BY sum(u.total) DESC, a.name, u.provider NULLS LAST, u.model NULLS LAST`, pid, platform, from, to)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (UsageRow, error) {
		var r UsageRow
		err := row.Scan(&r.AgentID, &r.AgentName, &r.AgentDeleted, &r.ProjectID, &r.Provider, &r.Model,
			&r.Calls, &r.Input, &r.Output, &r.CacheRead, &r.CacheWrite, &r.Total)
		return r, err
	})
}

// AnomalyRow 是一条未正常完成的执行。
type AnomalyRow struct {
	Run       Run
	AgentName string
	ProjectID *uuid.UUID // 平台 Agent 为空
}

// AnomalyCursor 是异常列表的翻页位置：上一页最后一项的结束时间与执行 ID。
type AnomalyCursor struct {
	EndedAt time.Time
	RunID   uuid.UUID
}

// Anomalies 按结束时间倒序返回范围内未删除 Agent 的失败与被打断的执行，以及 from 之后的总条数。
// 已删除项目中的 Agent 都已软删除，因此不出现。
func (s *Store) Anomalies(ctx context.Context, f ScopeFilter, from *time.Time, cursor *AnomalyCursor, limit int) ([]AnomalyRow, int, *AnomalyCursor, error) {
	pid, platform := f.args()
	const scope = `FROM runs r JOIN agents a ON a.id = r.agent_id
		WHERE a.deleted_at IS NULL AND r.status IN ('failed', 'interrupted')
			AND ($1::uuid IS NULL OR a.project_id = $1) AND (NOT $2 OR a.project_id IS NULL)
			AND ($3::timestamptz IS NULL OR r.ended_at >= $3)`
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) `+scope, pid, platform, from).Scan(&total); err != nil {
		return nil, 0, nil, err
	}
	var cAt *time.Time
	var cID *uuid.UUID
	if cursor != nil {
		cAt, cID = &cursor.EndedAt, &cursor.RunID
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+runColumnsOf("r.")+`, a.name, a.project_id `+scope+`
			AND ($4::timestamptz IS NULL OR (r.ended_at, r.id) < ($4, $5::uuid))
		ORDER BY r.ended_at DESC, r.id DESC LIMIT $6`, pid, platform, from, cAt, cID, limit+1)
	if err != nil {
		return nil, 0, nil, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (AnomalyRow, error) {
		var it AnomalyRow
		err := row.Scan(append(runFields(&it.Run), &it.AgentName, &it.ProjectID)...)
		it.Run.Tools = []ToolEvent{}
		return it, err
	})
	if err != nil {
		return nil, 0, nil, err
	}
	var next *AnomalyCursor
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1].Run
		next = &AnomalyCursor{EndedAt: *last.EndedAt, RunID: last.ID}
	}
	return items, total, next, nil
}

// LastEndedRuns 返回每个 Agent 最近一次已结束的执行。
func (s *Store) LastEndedRuns(ctx context.Context, agentIDs []uuid.UUID) (map[uuid.UUID]Run, error) {
	out := map[uuid.UUID]Run{}
	if len(agentIDs) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT ON (agent_id) `+runColumns+` FROM runs
		WHERE agent_id = ANY($1) AND status IN ('completed', 'failed', 'interrupted') ORDER BY agent_id, ended_at DESC, id DESC`, agentIDs)
	if err != nil {
		return nil, err
	}
	runs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Run, error) { return scanRun(r) })
	if err != nil {
		return nil, err
	}
	ptrs := make([]*Run, len(runs))
	for i := range runs {
		ptrs[i] = &runs[i]
	}
	if err := s.fillRuns(ctx, ptrs...); err != nil {
		return nil, err
	}
	for _, r := range runs {
		out[r.AgentID] = r
	}
	return out, nil
}

// ProcessExit 是 Pi 进程最近一次停止或意外退出。
type ProcessExit struct {
	At     time.Time
	Reason string // pi.stopped 的原因，意外退出为 unexpected_exit
}

// LastProcessExits 返回每个 Agent 最近一次 Pi 进程停止或意外退出。
func (s *Store) LastProcessExits(ctx context.Context, agentIDs []uuid.UUID) (map[uuid.UUID]ProcessExit, error) {
	out := map[uuid.UUID]ProcessExit{}
	if len(agentIDs) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT ON (agent_id) agent_id, at,
			CASE WHEN type = 'pi.exited' THEN 'unexpected_exit' ELSE coalesce(data ->> 'reason', '') END
		FROM lifecycle_events WHERE agent_id = ANY($1) AND task_id IS NULL AND type IN ('pi.stopped', 'pi.exited') ORDER BY agent_id, at DESC, id DESC`, agentIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var e ProcessExit
		if err := rows.Scan(&id, &e.At, &e.Reason); err != nil {
			return nil, err
		}
		out[id] = e
	}
	return out, rows.Err()
}

// CLIInstallResult 是某个 CLI 版本最近一次安装尝试的结果。
type CLIInstallResult struct {
	CLI       string
	Version   string
	Succeeded bool
	Error     string
}

// LatestCLIInstalls 返回每个 CLI 版本最近一次安装尝试的结果。
func (s *Store) LatestCLIInstalls(ctx context.Context) ([]CLIInstallResult, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT ON (data ->> 'cli', data ->> 'version') data ->> 'cli', data ->> 'version',
			data ->> 'result' = 'succeeded', coalesce(data ->> 'error', '')
		FROM lifecycle_events WHERE type = 'cli.install' ORDER BY data ->> 'cli', data ->> 'version', at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (CLIInstallResult, error) {
		var r CLIInstallResult
		err := row.Scan(&r.CLI, &r.Version, &r.Succeeded, &r.Error)
		return r, err
	})
}
