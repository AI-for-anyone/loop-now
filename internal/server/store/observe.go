package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
)

// UsageRow is a usage summary grouped by (Agent, provider, model); empty Provider and Model mean an unknown model.
type UsageRow struct {
	AgentID      uuid.UUID
	AgentName    string
	AgentDeleted bool
	ProjectID    *uuid.UUID // nil for a platform Agent; the project may have been deleted
	Provider     string
	Model        string
	UsageTotals
}

// UsageSummary sums the usage in [from, to) of all Agents in the scope (including deleted Agents and Agents in deleted
// projects); a nil from or to means unbounded. Ordered by total, descending.
func (s *Store) UsageSummary(ctx context.Context, f ScopeFilter, from, to *time.Time) ([]UsageRow, error) {
	pid, platform := f.args()
	rows, err := s.Pool.Query(ctx, `SELECT a.id, a.name, a.deleted_at IS NOT NULL, a.project_id, coalesce(u.provider, ''), coalesce(u.model, ''),
			count(*), sum(u.input), sum(u.output), sum(u.cache_read), sum(u.cache_write), sum(u.total)
		FROM usage_records u JOIN agents a ON a.id = u.agent_id
		WHERE ($1 IS NULL OR a.project_id = $1) AND (NOT $2 OR a.project_id IS NULL)
			AND ($3 IS NULL OR u.at >= $3) AND ($4 IS NULL OR u.at < $4)
		GROUP BY a.id, a.name, a.deleted_at, a.project_id, u.provider, u.model
		ORDER BY sum(u.total) DESC, a.name COLLATE NOCASE, a.name, u.provider NULLS LAST, u.model NULLS LAST`, pid, platform, from, to)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(row db.CollectableRow) (UsageRow, error) {
		var r UsageRow
		err := row.Scan(&r.AgentID, &r.AgentName, &r.AgentDeleted, &r.ProjectID, &r.Provider, &r.Model,
			&r.Calls, &r.Input, &r.Output, &r.CacheRead, &r.CacheWrite, &r.Total)
		return r, err
	})
}

// AnomalyRow is one run that did not complete normally.
type AnomalyRow struct {
	Run       Run
	AgentName string
	ProjectID *uuid.UUID // nil for a platform Agent
}

// AnomalyCursor is the paging position in the anomaly list: the end time and run ID of the last item on the previous
// page.
type AnomalyCursor struct {
	EndedAt time.Time
	RunID   uuid.UUID
}

// Anomalies returns, ordered by end time descending, the failed and interrupted runs of non-deleted Agents in the
// scope, plus the total count after from. Agents in deleted projects are all soft-deleted, so they do not appear. A
// manual compaction (/compact) cancelled by a person or interrupted by a server restart is not an anomaly; only failed
// ones are listed.
func (s *Store) Anomalies(ctx context.Context, f ScopeFilter, from *time.Time, cursor *AnomalyCursor, limit int) ([]AnomalyRow, int, *AnomalyCursor, error) {
	pid, platform := f.args()
	const scope = `FROM runs r JOIN agents a ON a.id = r.agent_id
		WHERE a.deleted_at IS NULL AND r.status IN ('failed', 'interrupted') AND NOT (r.origin = 'console_compact' AND r.status = 'interrupted')
			AND ($1 IS NULL OR a.project_id = $1) AND (NOT $2 OR a.project_id IS NULL)
			AND ($3 IS NULL OR r.ended_at >= $3)`
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
			AND ($4 IS NULL OR (r.ended_at, r.id) < ($4, $5))
		ORDER BY r.ended_at DESC, r.id DESC LIMIT $6`, pid, platform, from, cAt, cID, limit+1)
	if err != nil {
		return nil, 0, nil, err
	}
	items, err := db.CollectRows(rows, func(row db.CollectableRow) (AnomalyRow, error) {
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

// LastEndedRuns returns each Agent's most recent ended run (the "previous reply" in the overview). A manual compaction
// (/compact) is not a reply and does not count.
func (s *Store) LastEndedRuns(ctx context.Context, agentIDs []uuid.UUID) (map[uuid.UUID]Run, error) {
	out := map[uuid.UUID]Run{}
	if len(agentIDs) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+runColumns+` FROM (
			SELECT *, row_number() OVER (PARTITION BY agent_id ORDER BY ended_at DESC, id DESC) AS n FROM runs
			WHERE agent_id IN (SELECT value FROM json_each($1)) AND status IN ('completed', 'failed', 'interrupted') AND origin <> 'console_compact')
		WHERE n = 1`, agentIDs)
	if err != nil {
		return nil, err
	}
	runs, err := db.CollectRows(rows, func(r db.CollectableRow) (Run, error) { return scanRun(r) })
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

// ProcessExit is the most recent stop or unexpected exit of a Pi process.
type ProcessExit struct {
	At     time.Time
	Reason string // reason from pi.stopped; unexpected_exit for an unexpected exit
}

// LastProcessExits returns each Agent's most recent Pi process stop or unexpected exit.
func (s *Store) LastProcessExits(ctx context.Context, agentIDs []uuid.UUID) (map[uuid.UUID]ProcessExit, error) {
	out := map[uuid.UUID]ProcessExit{}
	if len(agentIDs) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT agent_id, at, reason FROM (
			SELECT agent_id, at, CASE WHEN type = 'pi.exited' THEN 'unexpected_exit' ELSE coalesce(data ->> 'reason', '') END AS reason,
				row_number() OVER (PARTITION BY agent_id ORDER BY at DESC, id DESC) AS n
			FROM lifecycle_events WHERE agent_id IN (SELECT value FROM json_each($1)) AND task_id IS NULL AND type IN ('pi.stopped', 'pi.exited'))
		WHERE n = 1`, agentIDs)
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

// CLIInstallResult is the result of the most recent install attempt of a CLI version.
type CLIInstallResult struct {
	CLI       string
	Version   string
	Succeeded bool
	Error     string
}

// LatestCLIInstalls returns the result of the most recent install attempt of each CLI version.
func (s *Store) LatestCLIInstalls(ctx context.Context) ([]CLIInstallResult, error) {
	rows, err := s.Pool.Query(ctx, `SELECT cli, version, succeeded, error FROM (
			SELECT data ->> 'cli' AS cli, data ->> 'version' AS version, data ->> 'result' = 'succeeded' AS succeeded, coalesce(data ->> 'error', '') AS error,
				row_number() OVER (PARTITION BY data ->> 'cli', data ->> 'version' ORDER BY at DESC, id DESC) AS n
			FROM lifecycle_events WHERE type = 'cli.install')
		WHERE n = 1 ORDER BY cli, version`)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(row db.CollectableRow) (CLIInstallResult, error) {
		var r CLIInstallResult
		err := row.Scan(&r.CLI, &r.Version, &r.Succeeded, &r.Error)
		return r, err
	})
}
