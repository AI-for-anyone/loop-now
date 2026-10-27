package store

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/clitemplate"
)

// purgeAgentCredentials is shared by Agent and project soft deletion. Callers
// already hold all affected Agent locks and have passed task/idle checks. Keep
// login terminal history but remove active URLs and every account ciphertext in
// their existing transaction. Post-commit runtime cleanup stays with callers.
func purgeAgentCredentials(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	for _, query := range []string{
		`DELETE FROM agent_db_connection_sets WHERE agent_id=ANY($1)`,
		`DELETE FROM agent_credential_selections WHERE agent_id=ANY($1)`,
		`DELETE FROM agent_model_legacy_cleared WHERE agent_id=ANY($1)`,
		`DELETE FROM agent_credentials WHERE agent_id=ANY($1)`,
		`UPDATE model_auth_operations SET status='cancelled',authorization_url=NULL,error_code=NULL,completed_at=now() WHERE agent_id=ANY($1) AND status IN ('pending','awaiting_authorization')`,
		`DELETE FROM agent_model_auth WHERE agent_id=ANY($1)`,
		`DELETE FROM model_auth_versions WHERE agent_id=ANY($1)`,
	} {
		if _, err := tx.Exec(ctx, query, ids); err != nil {
			return err
		}
	}
	return nil
}

// Called after project/Agent locks and deletion guards, inside the same Tx.
// Keep operation/account metadata; clear every project-owned secret and pointer.
// No task of the project is unfinished, so every version is wiped and every
// task snapshot of the project loses its plaintext model keys and CLI payloads.
func purgeProjectCredentials(ctx context.Context, tx pgx.Tx, project uuid.UUID) error {
	rows, e := tx.Query(ctx, `SELECT id FROM project_credential_accounts WHERE project_id=$1 ORDER BY id FOR UPDATE`, project)
	if e != nil {
		return e
	}
	accounts, e := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if e != nil {
		return e
	}
	if e = terminateVerificationsTx(ctx, tx, project, nil, nil); e != nil {
		return e
	}
	for _, q := range []string{
		`UPDATE project_cli_auth_operations SET status='cancelled',authorization_url=NULL,user_code=NULL,error_code='auth_cancelled',completed_at=clock_timestamp() WHERE project_id=$1 AND status IN('pending','awaiting_authorization')`,
		`UPDATE project_model_auth_operations SET status='cancelled',authorization_url=NULL,error_code=NULL,completed_at=clock_timestamp() WHERE project_id=$1 AND status IN('pending','awaiting_authorization')`,
		`UPDATE project_credential_accounts SET deleted_at=coalesce(deleted_at,clock_timestamp()),updated_at=clock_timestamp(),revision=revision+1 WHERE project_id=$1`,
		`DELETE FROM project_credential_legacy_rows WHERE project_id=$1`,
		`DELETE FROM credential_pending_wipes WHERE project_id=$1`,
	} {
		if _, e = tx.Exec(ctx, q, project); e != nil {
			return e
		}
	}
	if e = wipeVersionsTx(ctx, tx, accounts, nil); e != nil {
		return e
	}
	rows, e = tx.Query(ctx, `SELECT i.id FROM task_instances i JOIN task_records r ON r.id=i.task_id WHERE r.project_id=$1`, project)
	if e != nil {
		return e
	}
	instances, e := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if e != nil {
		return e
	}
	if e = scrubSnapshotsTx(ctx, tx, instances, nil); e != nil {
		return e
	}
	return bumpCredentialProject(ctx, tx, project)
}

// scrubAgentSnapshotsTx clears the plaintext credentials of every ended task
// instance whose base Agent is agent. Agent deletion refuses while the Agent is
// in an unfinished task, so nothing is left for later.
func scrubAgentSnapshotsTx(ctx context.Context, tx pgx.Tx, agent uuid.UUID) error {
	rows, e := tx.Query(ctx, `SELECT i.id FROM task_instances i JOIN task_records r ON r.id=i.task_id WHERE i.agent_id=$1 AND r.ended_at IS NOT NULL`, agent)
	if e != nil {
		return e
	}
	instances, e := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if e != nil {
		return e
	}
	return scrubSnapshotsTx(ctx, tx, instances, nil)
}

// settleProjectWipesTx settles the project's deleted accounts that still wait
// for unfinished tasks. Every task save runs it, so the last task to end wipes
// what it kept, whichever terminal path ended it.
func settleProjectWipesTx(ctx context.Context, tx pgx.Tx, project uuid.UUID) error {
	rows, e := tx.Query(ctx, `SELECT account_id FROM credential_pending_wipes WHERE project_id=$1 ORDER BY account_id`, project)
	if e != nil {
		return e
	}
	accounts, e := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if e != nil {
		return e
	}
	for _, account := range accounts {
		if e = settleDeletedAccountTx(ctx, tx, project, account); e != nil {
			return e
		}
	}
	return nil
}

// SettleCredentialWipes runs at startup and finishes pending wipes whose last
// task ended without settling them.
func (s *Store) SettleCredentialWipes(ctx context.Context) error {
	rows, e := s.Pool.Query(ctx, `SELECT DISTINCT project_id FROM credential_pending_wipes ORDER BY project_id`)
	if e != nil {
		return e
	}
	projects, e := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if e != nil {
		return e
	}
	for _, project := range projects {
		if e = s.inTx(ctx, func(tx pgx.Tx) error {
			// Same order as task saves: project, then the per-account wipe lock.
			if _, e := tx.Exec(ctx, `SELECT 1 FROM projects WHERE id=$1 FOR SHARE`, project); e != nil {
				return e
			}
			return settleProjectWipesTx(ctx, tx, project)
		}); e != nil {
			return e
		}
	}
	return nil
}

// settleDeletedAccountTx wipes what it can of a deleted account. Versions an
// unfinished task still uses keep their secret: one its snapshot binds
// (model, Google or Lark), one its Lark task binding names, or one whose
// payload its snapshot copied. Ended snapshots lose the copies taken from the
// account; unfinished ones keep them until their task ends. The pending row
// exists exactly while something is kept.
func settleDeletedAccountTx(ctx context.Context, tx pgx.Tx, project, account uuid.UUID) error {
	if _, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('credential-wipe:'||$1::text,0))`, account); e != nil {
		return e
	}
	var cli *string
	if e := tx.QueryRow(ctx, `SELECT cli_name FROM project_credential_accounts WHERE id=$1 AND project_id=$2 AND deleted_at IS NOT NULL`, account, project).Scan(&cli); e == pgx.ErrNoRows {
		// Never a live account's versions; a stray row must not block task saves.
		_, e = tx.Exec(ctx, `DELETE FROM credential_pending_wipes WHERE account_id=$1`, account)
		return e
	} else if e != nil {
		return e
	}
	// Versions still holding secret material, with the payload a task could copy.
	payloads := map[uuid.UUID]*clitemplate.Payload{}
	rows, e := tx.Query(ctx, `SELECT id,secret,auth_method FROM project_cli_versions WHERE account_id=$1 AND (octet_length(secret)>0 OR field_names<>'{}' OR env_names<>'{}' OR file_paths<>'{}')
		UNION ALL SELECT id,NULL,NULL FROM model_auth_versions WHERE project_account_id=$1`, account)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id uuid.UUID
		var secret []byte
		var method *string
		if e = rows.Scan(&id, &secret, &method); e != nil {
			rows.Close()
			return e
		}
		payloads[id] = nil
		// Google and Lark OAuth versions are bound, never copied.
		if cli != nil && !(method != nil && *method == "oauth" && (*cli == "gcloud" || *cli == "lark-cli")) {
			payloads[id] = copyablePayload(secret)
		}
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	if len(payloads) == 0 {
		_, e = tx.Exec(ctx, `DELETE FROM credential_pending_wipes WHERE account_id=$1`, account)
		return e
	}
	versions := make([]string, 0, len(payloads))
	for id := range payloads {
		versions = append(versions, id.String())
	}
	rows, e = tx.Query(ctx, `SELECT DISTINCT v::uuid FROM (
			SELECT x.c->k->>'versionId' v FROM task_instances i JOIN task_records r ON r.id=i.task_id
			CROSS JOIN LATERAL (SELECT convert_from(i.snapshot,'UTF8')::jsonb->'credentials' c) x
			CROSS JOIN unnest(ARRAY['modelAuth','googleBinding','larkBinding']) k
			WHERE r.project_id=$1 AND r.ended_at IS NULL
			UNION ALL SELECT b.version_id::text FROM task_lark_credential_bindings b JOIN task_records r ON r.id=b.task_id WHERE r.project_id=$1 AND r.ended_at IS NULL
		) u WHERE v=ANY($2)`, project, versions)
	if e != nil {
		return e
	}
	keep, e := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if e != nil {
		return e
	}
	if cli != nil {
		var ended []uuid.UUID
		rows, e = tx.Query(ctx, `SELECT i.id,r.ended_at IS NOT NULL,x.s->'credentials'->'cli'->$2 FROM task_instances i JOIN task_records r ON r.id=i.task_id
			CROSS JOIN LATERAL (SELECT convert_from(i.snapshot,'UTF8')::jsonb s) x
			WHERE r.project_id=$1 AND x.s->'credentials'->'cli' ? $2 ORDER BY i.id`, project, *cli)
		if e != nil {
			return e
		}
		for rows.Next() {
			var id uuid.UUID
			var done bool
			var entry []byte
			if e = rows.Scan(&id, &done, &entry); e != nil {
				rows.Close()
				return e
			}
			from, copied := copiedFrom(*cli, account, entry, payloads)
			if done && copied {
				ended = append(ended, id)
			} else if copied {
				for _, v := range from {
					if !slices.Contains(keep, v) {
						keep = append(keep, v)
					}
				}
			}
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		if e = scrubSnapshotsTx(ctx, tx, ended, []string{*cli}); e != nil {
			return e
		}
	}
	if e = wipeVersionsTx(ctx, tx, []uuid.UUID{account}, keep); e != nil {
		return e
	}
	if len(keep) == 0 {
		_, e = tx.Exec(ctx, `DELETE FROM credential_pending_wipes WHERE account_id=$1`, account)
	} else {
		_, e = tx.Exec(ctx, `INSERT INTO credential_pending_wipes(account_id,project_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, account, project)
	}
	return e
}

// copyablePayload decodes a version secret that task capture copies into a
// snapshot (a CLI payload); nil when it is not one or carries nothing.
func copyablePayload(secret []byte) *clitemplate.Payload {
	var p clitemplate.Payload
	if strictDecode(secret, &p) != nil || len(p.Env)+len(p.Files) == 0 {
		return nil
	}
	return &p
}

// copiedFrom reports whether a snapshot CLI entry holds a copy taken from the
// account, and from which of its secret-holding versions. A database entry
// names each account it aggregates; any other entry equals a version payload.
func copiedFrom(cli string, account uuid.UUID, entry []byte, payloads map[uuid.UUID]*clitemplate.Payload) ([]uuid.UUID, bool) {
	var got clitemplate.Payload
	if strictDecode(entry, &got) != nil {
		return nil, false
	}
	var from []uuid.UUID
	if cli == "psql" || cli == "mysql" {
		if len(got.Files) != 1 {
			return nil, false
		}
		var connections []struct {
			AccountID uuid.UUID `json:"accountId"`
			VersionID uuid.UUID `json:"versionId"`
		}
		if json.Unmarshal(got.Files[0].Content, &connections) != nil {
			return nil, false
		}
		copied := false
		for _, c := range connections {
			if c.AccountID != account {
				continue
			}
			copied = true
			if _, ok := payloads[c.VersionID]; ok {
				from = append(from, c.VersionID)
			}
		}
		return from, copied
	}
	for id, p := range payloads {
		if p != nil && samePayload(*p, got) {
			from = append(from, id)
		}
	}
	return from, len(from) > 0
}

func samePayload(a, b clitemplate.Payload) bool {
	if len(a.Env) != len(b.Env) || len(a.Files) != len(b.Files) {
		return false
	}
	for k, v := range a.Env {
		if w, ok := b.Env[k]; !ok || w != v {
			return false
		}
	}
	byPath := func(x, y clitemplate.PayloadFile) int { return strings.Compare(x.Path, y.Path) }
	af, bf := slices.SortedFunc(slices.Values(a.Files), byPath), slices.SortedFunc(slices.Values(b.Files), byPath)
	for i := range af {
		if af[i].Path != bf[i].Path || !bytes.Equal(af[i].Content, bf[i].Content) {
			return false
		}
	}
	return true
}

func strictDecode(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return io.ErrUnexpectedEOF
	}
	return nil
}

// wipeVersionsTx removes the secret material of the accounts' versions except
// keep, which an unfinished task still uses. A version an ended task's Lark
// binding references stays as a tombstone without secret or layout (the
// binding is immutable); every other version row is deleted. Grants,
// identities and current pointers of wiped versions go with them.
func wipeVersionsTx(ctx context.Context, tx pgx.Tx, accounts, keep []uuid.UUID) error {
	if keep == nil {
		keep = []uuid.UUID{}
	}
	for _, q := range []string{
		`UPDATE project_credential_accounts SET current_model_version_id=NULL WHERE id=ANY($1) AND current_model_version_id<>ALL($2)`,
		`UPDATE project_credential_accounts SET current_cli_version_id=NULL WHERE id=ANY($1) AND current_cli_version_id<>ALL($2)`,
		`DELETE FROM google_cli_grants WHERE account_id=ANY($1) AND version_id<>ALL($2)`,
		`DELETE FROM github_cli_identities WHERE account_id=ANY($1) AND version_id<>ALL($2)`,
		`DELETE FROM lark_cli_identities WHERE account_id=ANY($1) AND version_id<>ALL($2)`,
		`DELETE FROM project_cli_versions v WHERE account_id=ANY($1) AND id<>ALL($2)
			AND NOT EXISTS(SELECT 1 FROM task_lark_credential_bindings b WHERE b.version_id=v.id AND b.account_id=v.account_id)`,
		`UPDATE project_cli_versions SET secret='\x'::bytea,field_names='{}',env_names='{}',file_paths='{}',updated_at=clock_timestamp()
			WHERE account_id=ANY($1) AND id<>ALL($2) AND (octet_length(secret)>0 OR field_names<>'{}' OR env_names<>'{}' OR file_paths<>'{}')`,
		`DELETE FROM model_auth_versions WHERE project_account_id=ANY($1) AND id<>ALL($2)`,
	} {
		if _, e := tx.Exec(ctx, q, accounts, keep); e != nil {
			return e
		}
	}
	return nil
}

// scrubSnapshotsTx drops plaintext credential copies from task snapshots:
// with cli nil, credentials.model and every credentials.cli entry; otherwise
// only the named credentials.cli entries. Bindings carry ids only and stay.
// snapshot_sha is the frozen capture identity and does not change; the
// snapshot still decodes, its credentials just empty.
func scrubSnapshotsTx(ctx context.Context, tx pgx.Tx, instances []uuid.UUID, cli []string) error {
	if len(instances) == 0 {
		return nil
	}
	set, args := `jsonb_set(jsonb_set(x.s,'{credentials,model}','{}'),'{credentials,cli}','{}')`, []any{instances}
	if cli != nil {
		set, args = `jsonb_set(x.s,'{credentials,cli}',coalesce(x.s->'credentials'->'cli','{}')-$2::text[])`, append(args, cli)
	}
	_, e := tx.Exec(ctx, `UPDATE task_instances i SET snapshot=convert_to(`+set+`::text,'UTF8')
		FROM (SELECT id,convert_from(snapshot,'UTF8')::jsonb s FROM task_instances WHERE id=ANY($1)) x WHERE i.id=x.id`, args...)
	return e
}
