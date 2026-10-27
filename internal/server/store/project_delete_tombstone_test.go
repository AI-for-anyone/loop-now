package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskruntime"
)

// Deleting a project whose ended task switched Lark accounts: the task's binding
// is immutable and keeps referencing its CLI version, which stays as a tombstone
// without secret; every other version is deleted, and the project's task
// snapshots lose their plaintext model keys and CLI payloads. Before the fix the
// version delete hit the binding FK and the whole deletion rolled back.
func TestDeleteProjectKeepsLarkBoundVersionAsTombstone(t *testing.T) {
	s, in, next, turn := larkPublishFixture(t)
	ctx := context.Background()
	if _, err := s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		_, _, err := s.PublishTaskLarkSwitchTx(ctx, m, in.ActorKey, in.ActionID, next, turn, "remaining QA only", json.RawMessage(`{"status":"succeeded"}`))
		return nil, err
	}); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetTask(ctx, in.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	project := r.View.Task.ProjectID
	bound := in.Capture.Target.VersionID

	// More project accounts: a model key with history, a static CLI, and a Lark
	// account no task ever bound.
	c, _ := modelcredential.APIKey("synthetic-model-secret")
	plain, _ := c.Encode()
	model, err := s.CreateProjectCredential(ctx, project, "model", "openai", "模型", store.ProjectCredentialWrite{AuthMethod: "api_key", Secret: plain})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MutateProjectCredential(ctx, project, model.View.ID, model.View.Revision, nil, &store.ProjectCredentialWrite{AuthMethod: "api_key", Secret: plain}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateProjectCredential(ctx, project, "cli", "gh", "静态CLI", store.ProjectCredentialWrite{AuthMethod: "static", TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1"}, Secret: []byte(`{"GH_TOKEN":"synthetic-cli-secret"}`)}); err != nil {
		t.Fatal(err)
	}
	unbound := larkSwitchTargetWithScopes(t, s, project, time.Now().Add(time.Hour), []string{"offline_access"})

	// End the task and make the fixture's project an ordinary deletable one.
	if _, err = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		now := time.Now().UTC()
		m.Record.View.Task.Status, m.Record.View.Task.EndedAt = "cancelled", &now
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	mustExec(t, s, `UPDATE projects SET is_default=false WHERE id=$1`, project)

	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := s.Pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	snapshots := func() map[uuid.UUID][2][]byte {
		t.Helper()
		rows, err := s.Pool.Query(ctx, `SELECT i.id,i.snapshot,i.snapshot_sha FROM task_instances i JOIN task_records r ON r.id=i.task_id WHERE r.project_id=$1`, project)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[uuid.UUID][2][]byte{}
		for rows.Next() {
			var id uuid.UUID
			var raw []byte
			var sha string
			if err := rows.Scan(&id, &raw, &sha); err != nil {
				t.Fatal(err)
			}
			out[id] = [2][]byte{raw, []byte(sha)}
		}
		return out
	}
	const secretVersions = `SELECT (SELECT count(*) FROM project_cli_versions v JOIN project_credential_accounts a ON a.id=v.account_id WHERE a.project_id=$1 AND octet_length(v.secret)>0)
		+(SELECT count(*) FROM model_auth_versions v JOIN project_credential_accounts a ON a.id=v.project_account_id WHERE a.project_id=$1)`
	if n := count(secretVersions, project); n != 5 {
		t.Fatal("fixture should hold 2 Lark, 1 static CLI and 2 model versions with secrets", n)
	}
	before := snapshots()
	if len(before) != 2 {
		t.Fatal("fixture should have two task instances", len(before))
	}
	for _, v := range before {
		if !bytes.Contains(v[0], []byte("synthetic-key")) || !bytes.Contains(v[0], []byte("synthetic-secret")) {
			t.Fatal("fixture snapshots should hold plaintext model key and Lark CLI payload")
		}
	}

	if _, err = s.DeleteProject(ctx, project); err != nil {
		t.Fatal("project deletion must succeed with an ended task's Lark binding", err)
	}
	if _, err = s.GetProject(ctx, project); err == nil {
		t.Fatal("project still live")
	}
	if n := count(secretVersions, project); n != 0 {
		t.Fatal("secret material remains for the project's accounts", n)
	}
	var secret []byte
	var fields, envs, files []string
	var label *string
	if err = s.Pool.QueryRow(ctx, `SELECT secret,field_names,env_names,file_paths,account_label FROM project_cli_versions WHERE id=$1`, bound).Scan(&secret, &fields, &envs, &files, &label); err != nil {
		t.Fatal("version referenced by the binding should stay as a tombstone", err)
	}
	if len(secret) != 0 || len(fields)+len(envs)+len(files) != 0 || label == nil || *label == "" {
		t.Fatal("tombstone keeps only metadata", len(secret), fields, envs, files, label)
	}
	if n := count(`SELECT count(*) FROM project_cli_versions v JOIN project_credential_accounts a ON a.id=v.account_id WHERE a.project_id=$1`, project); n != 1 {
		t.Fatal("unreferenced versions should be deleted", n)
	}
	if n := count(`SELECT count(*) FROM project_cli_versions WHERE id=$1`, *unbound.View.CurrentVersionID); n != 0 {
		t.Fatal("unbound Lark version should be deleted")
	}
	if n := count(`SELECT (SELECT count(*) FROM lark_cli_identities i JOIN project_credential_accounts a ON a.id=i.account_id WHERE a.project_id=$1)
		+(SELECT count(*) FROM lark_cli_grants g WHERE g.version_id=$2)
		+(SELECT count(*) FROM project_credential_legacy_rows WHERE project_id=$1)`, project, bound); n != 0 {
		t.Fatal("identities, grants and legacy rows should be gone", n)
	}
	if n := count(`SELECT (SELECT count(*) FROM task_lark_credential_bindings WHERE task_id=$1)*10+(SELECT count(*) FROM task_lark_binding_members WHERE task_id=$1)`, in.TaskID); n != 12 {
		t.Fatal("immutable task binding history must be kept", n)
	}
	after := snapshots()
	for id, v := range after {
		if bytes.Contains(v[0], []byte("synthetic-key")) || bytes.Contains(v[0], []byte("synthetic-secret")) {
			t.Fatal("task snapshot still holds plaintext credentials", id)
		}
		if !bytes.Equal(v[1], before[id][1]) {
			t.Fatal("snapshot identity must not change", id)
		}
		snap, err := taskruntime.Decode(v[0])
		if err != nil {
			t.Fatal("scrubbed snapshot should still decode", err)
		}
		creds, err := snap.Credentials()
		if err != nil || len(creds.Model) != 0 || len(creds.CLI) != 0 {
			t.Fatal("scrubbed snapshot should carry no model or CLI credentials", err, creds.Model, len(creds.CLI))
		}
	}
}

// Deletion purges version rows inside one transaction. A new FK into a version
// table that neither cascades nor nulls would block it again (as the Lark task
// binding did), so every such reference must be one the purge handles.
func TestCredentialVersionReferencesAreHandledByPurge(t *testing.T) {
	s := taskConsoleStore(t)
	rows, err := s.Pool.Query(context.Background(), `SELECT conrelid::regclass::text||' -> '||confrelid::regclass::text FROM pg_constraint
		WHERE contype='f' AND confdeltype IN ('a','r') AND confrelid::regclass::text IN ('project_cli_versions','model_auth_versions','project_credential_legacy_rows','agent_credentials')
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			t.Fatal(err)
		}
		got = append(got, ref)
	}
	rows.Close()
	want := []string{
		"agent_model_auth -> model_auth_versions",               // deleted first by purgeAgentCredentials
		"project_credential_accounts -> model_auth_versions",    // current pointer cleared first
		"project_credential_accounts -> project_cli_versions",   // current pointer cleared first
		"task_lark_credential_bindings -> project_cli_versions", // immutable: version kept as tombstone
	}
	if !slices.Equal(got, want) {
		t.Fatal(fmt.Sprintf("blocking references changed; update the credential purge:\n got %q\nwant %q", got, want))
	}
}
