package store

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	cm "loop-now/internal/server/credentialmodel"
	"slices"
	"strings"
)

func isDBCLI(cli string) bool { return cli == "psql" || cli == "mysql" }
func dbSetExistsTx(ctx context.Context, tx pgx.Tx, agent uuid.UUID, cli string) (bool, error) {
	var present bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_db_connection_sets WHERE agent_id=$1 AND cli_name=$2)`, agent, cli).Scan(&present)
	return present, err
}
func requireDBCLI(ctx context.Context, tx pgx.Tx, a Agent, cli string) error {
	if !isDBCLI(cli) {
		return apperr.Invalid("cli", "数据库CLI须为psql或mysql")
	}
	var allowed bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_clis WHERE agent_id=$1 AND cli_name=$2)`, a.ID, cli).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return apperr.Invalid("cli", "Agent未允许该CLI")
	}
	return nil
}
func dbConnectionsTx(ctx context.Context, tx pgx.Tx, a Agent, cli string, rev int64) (cm.DBConnections, error) {
	out := cm.DBConnections{AgentID: a.ID, ProjectID: *a.ProjectID, CLI: cli, ConfigVersion: a.ConfigVersion, CredentialsRevision: rev, Mode: "legacy_single", AccountIDs: []uuid.UUID{}, Connections: []cm.DBConnection{}, Status: "not_configured"}
	if err := requireDBCLI(ctx, tx, a, cli); err != nil {
		return out, err
	}
	present, err := dbSetExistsTx(ctx, tx, a.ID, cli)
	if err != nil {
		return out, err
	}
	if !present {
		selection, r, e := resolveProjectCredentialTx(ctx, tx, a, PurposeCLI, cli)
		out.Status = selection.Status
		if r != nil {
			out.AccountIDs = append(out.AccountIDs, r.View.ID)
			out.Connections = append(out.Connections, cm.DBConnection{AccountID: r.View.ID, Name: r.View.Name, VersionID: r.View.CurrentVersionID, Status: selection.Status})
		}
		return out, e
	}
	out.Mode = "explicit_set"
	rows, err := tx.Query(ctx, `SELECT account_id FROM agent_db_connection_members WHERE agent_id=$1 AND cli_name=$2 ORDER BY account_id`, a.ID, cli)
	if err != nil {
		return out, err
	}
	out.AccountIDs, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if out.AccountIDs == nil {
		out.AccountIDs = []uuid.UUID{}
	}
	if err != nil {
		return out, err
	}
	for _, id := range out.AccountIDs {
		r, e := projectAccountTx(ctx, tx, *a.ProjectID, id, false)
		if e != nil {
			return out, e
		}
		status := "resolved"
		switch {
		case r.View.DeletedAt != nil:
			status = "account_deleted"
		case r.View.Purpose != PurposeCLI || r.View.CLI == nil || *r.View.CLI != cli:
			status = "scope_mismatch"
		case r.CLI == nil || r.View.CurrentVersionID == nil:
			status = "unavailable"
		}
		out.Connections = append(out.Connections, cm.DBConnection{AccountID: id, Name: r.View.Name, VersionID: r.View.CurrentVersionID, Status: status})
		if out.Status == "not_configured" {
			out.Status = "resolved"
		}
		if status != "resolved" {
			out.Status = status
		}
	}
	return out, nil
}
func (s *Store) GetDBConnections(ctx context.Context, agent uuid.UUID, cli string) (cm.DBConnections, error) {
	var out cm.DBConnections
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		a, rev, e := lockProjectCredentialAgent(ctx, tx, agent, false)
		if e != nil {
			return e
		}
		out, e = dbConnectionsTx(ctx, tx, a, cli, rev)
		return e
	})
	return out, err
}
func (s *Store) PutDBConnections(ctx context.Context, agent uuid.UUID, cli string, in cm.DBConnectionsPut) (cm.DBConnections, error) {
	return s.mutateDBConnections(ctx, agent, cli, in, false)
}
func (s *Store) DeleteDBConnections(ctx context.Context, agent uuid.UUID, cli string, expected int64) (cm.DBConnections, error) {
	return s.mutateDBConnections(ctx, agent, cli, cm.DBConnectionsPut{ExpectedConfigVersion: expected}, true)
}
func (s *Store) mutateDBConnections(ctx context.Context, agent uuid.UUID, cli string, in cm.DBConnectionsPut, remove bool) (cm.DBConnections, error) {
	var out cm.DBConnections
	ids := slices.Clone(in.AccountIDs)
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	if !remove && in.AccountIDs == nil {
		return out, apperr.Invalid("accountIds", "须提供连接ID数组，空数组表示无授权")
	}
	for i, id := range ids {
		if id == uuid.Nil || (i > 0 && id == ids[i-1]) {
			return out, apperr.Invalid("accountIds", "连接ID须非零且不重复")
		}
	}
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		a, rev, e := lockProjectCredentialAgent(ctx, tx, agent, true)
		if e != nil {
			return e
		}
		if e = credentialCAS("agent", a.ID, in.ExpectedConfigVersion, a.ConfigVersion); e != nil {
			return e
		}
		if e = requireDBCLI(ctx, tx, a, cli); e != nil {
			return e
		}
		present, e := dbSetExistsTx(ctx, tx, a.ID, cli)
		if e != nil {
			return e
		}
		if remove {
			if present {
				if _, e = tx.Exec(ctx, `DELETE FROM agent_db_connection_sets WHERE agent_id=$1 AND cli_name=$2`, agent, cli); e != nil {
					return e
				}
			}
		} else {
			for _, id := range ids {
				r, e := projectAccountTx(ctx, tx, *a.ProjectID, id, true)
				if e != nil {
					return e
				}
				if r.View.DeletedAt != nil {
					return projectAccountDeleted(*a.ProjectID, id)
				}
				if r.View.Purpose != PurposeCLI || r.View.CLI == nil || *r.View.CLI != cli {
					return apperr.Invalid("accountIds", "连接账号项目或用途不匹配")
				}
				if r.CLI == nil {
					return apperr.Invalid("accountIds", "连接账号凭证不可用")
				}
			}
			if _, e = tx.Exec(ctx, `INSERT INTO agent_db_connection_sets(agent_id,project_id,cli_name)VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, agent, *a.ProjectID, cli); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, `DELETE FROM agent_db_connection_members WHERE agent_id=$1 AND cli_name=$2`, agent, cli); e != nil {
				return e
			}
			for _, id := range ids {
				if _, e = tx.Exec(ctx, `INSERT INTO agent_db_connection_members(agent_id,project_id,cli_name,account_id)VALUES($1,$2,$3,$4)`, agent, *a.ProjectID, cli, id); e != nil {
					return e
				}
			}
		}
		if !remove || present {
			if e = bumpConfig(ctx, tx, agent); e != nil {
				return e
			}
			a.ConfigVersion++
		}
		out, e = dbConnectionsTx(ctx, tx, a, cli, rev)
		return e
	})
	return out, err
}
