package store_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/dbcli"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/prompts"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/taskruntime"
	"loop-now/internal/server/workflow"
	"loop-now/internal/testutil/pgtest"
)

type wipeFixture struct {
	s       *store.Store
	project uuid.UUID
	manager store.Agent
}

func newWipeFixture(t *testing.T) wipeFixture {
	t.Helper()
	ctx := context.Background()
	s := taskConsoleStore(t)
	d, err := s.EnsureDefaults(ctx, "org", "project")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := s.CreateAgent(ctx, store.ProjectScope(d.Project.ID), "manager", "openai", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetProjectManager(ctx, d.Project.ID, manager.ID); err != nil {
		t.Fatal(err)
	}
	return wipeFixture{s, d.Project.ID, manager}
}

func (f wipeFixture) agent(t *testing.T, name string) store.Agent {
	t.Helper()
	a, err := f.s.CreateAgent(context.Background(), store.ProjectScope(f.project), name, "openai", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// account creates a project account whose versions hold the given secrets in
// order; the last one is current. It returns the account and its version ids.
func (f wipeFixture) account(t *testing.T, purpose, slot string, secrets ...[]byte) (uuid.UUID, []uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	write := func(secret []byte) store.ProjectCredentialWrite {
		if purpose == "model" {
			return store.ProjectCredentialWrite{AuthMethod: "api_key", Secret: secret}
		}
		return store.ProjectCredentialWrite{AuthMethod: "static", TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1", EnvNames: []string{"SYNTHETIC"}}, Secret: secret}
	}
	r, err := f.s.CreateProjectCredential(ctx, f.project, purpose, slot, slot+"-"+uuid.NewString()[:8], write(secrets[0]))
	if err != nil {
		t.Fatal(err)
	}
	versions := []uuid.UUID{*r.View.CurrentVersionID}
	for _, secret := range secrets[1:] {
		if r, err = f.s.MutateProjectCredential(ctx, f.project, r.View.ID, r.View.Revision, nil, ptr(write(secret)), false); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, *r.View.CurrentVersionID)
	}
	return r.View.ID, versions
}

func ptr[T any](v T) *T { return &v }

func (f wipeFixture) deleteAccount(t *testing.T, id uuid.UUID) {
	t.Helper()
	r, err := f.s.GetProjectCredential(context.Background(), f.project, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.MutateProjectCredential(context.Background(), f.project, id, r.View.Revision, nil, nil, true); err != nil {
		t.Fatal("account deletion", err)
	}
}

// task creates an unfinished task with one instance of agent whose snapshot
// carries creds, built as task capture builds it.
func (f wipeFixture) task(t *testing.T, agent store.Agent, clis []string, creds taskruntime.Credentials) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	id, node, runtime := uuid.New(), uuid.New(), uuid.New()
	if creds.Model == nil {
		creds.Model = map[string]string{}
	}
	if creds.CLI == nil {
		creds.CLI = map[string]taskruntime.CredentialPayload{}
	}
	scope, _ := taskruntime.TaskScope(agent.ID, id)
	project := f.project
	snap, err := taskruntime.Capture(scope, uuid.New(), taskruntime.CaptureInput{Configuration: taskruntime.Configuration{AgentID: agent.ID, ConfigVersion: 1, Provider: "openai", ModelID: "fixture", Prompts: &prompts.Layers{Format: prompts.FormatV1, ProjectID: &project, ProjectVersion: 1, AgentVersion: 1}, Skills: []taskruntime.Skill{}, CLIs: clis}, Credentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := snap.Encode()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	n := taskmodel.NodeRun{ID: node, TaskID: id, StepID: "work", AgentID: agent.ID, AgentName: agent.Name, RuntimeID: runtime, Visit: 1, Attempt: 1, Status: "queued", Inputs: map[string]any{}, InputVersions: []workflow.InputVersion{}, RunIDs: []uuid.UUID{}}
	r := taskmodel.Record{View: taskmodel.Detail{Task: taskmodel.Summary{ID: id, ProjectID: f.project, Status: "preparing", Title: "fixture", Version: 1, CreatedAt: now, UpdatedAt: now, CurrentNodeRunID: &node}, Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "work", AgentID: agent.ID}}, Edges: []workflow.Edge{}}, Nodes: []taskmodel.NodeRun{n}, Artifacts: []taskmodel.Artifact{}, Cleanup: "none"}, EdgeCounts: map[string]int{}}
	_, err = f.s.CreateTask(ctx, f.project, f.manager.ID, store.TaskAction{ActorKey: f.manager.ID.String(), ID: uuid.New(), Operation: "fixture", Body: []byte(`{}`)}, func(pgx.Tx) (taskmodel.Record, []store.TaskInstance, error) {
		return r, []store.TaskInstance{{ID: runtime, TaskID: id, AgentID: agent.ID, AgentName: agent.Name, Snapshot: encoded, Sources: []store.TaskFrozenSource{}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return id, runtime
}

// endTask ends a task through the normal task save path.
func endTask(t *testing.T, s *store.Store, id uuid.UUID) {
	t.Helper()
	if _, err := s.MutateTask(context.Background(), id, nil, func(m *store.TaskMutation) (any, error) {
		now := time.Now().UTC()
		m.Record.View.Task.Status, m.Record.View.Task.EndedAt = "cancelled", &now
		return nil, nil
	}); err != nil {
		t.Fatal("end task", err)
	}
}

func cliPayload(t *testing.T, env map[string]string) ([]byte, taskruntime.CredentialPayload) {
	t.Helper()
	b, err := json.Marshal(clitemplate.Payload{Env: env, Files: []clitemplate.PayloadFile{}})
	if err != nil {
		t.Fatal(err)
	}
	return b, taskruntime.CredentialPayload{Env: env, Files: []taskruntime.File{}}
}

func modelSecret(t *testing.T, key string) []byte {
	t.Helper()
	c, err := modelcredential.APIKey(key)
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type snapshotRow struct {
	raw []byte
	sha string
}

func snapshotOf(t *testing.T, s *store.Store, runtime uuid.UUID) snapshotRow {
	t.Helper()
	var r snapshotRow
	if err := s.Pool.QueryRow(context.Background(), `SELECT snapshot,snapshot_sha FROM task_instances WHERE id=$1`, runtime).Scan(&r.raw, &r.sha); err != nil {
		t.Fatal(err)
	}
	return r
}

// decodedCredentials proves the snapshot still decodes and returns its credentials.
func decodedCredentials(t *testing.T, raw []byte) taskruntime.Credentials {
	t.Helper()
	snap, err := taskruntime.Decode(raw)
	if err != nil {
		t.Fatal("snapshot should decode", err)
	}
	c, err := snap.Credentials()
	if err != nil {
		t.Fatal("snapshot credentials should validate", err)
	}
	return c
}

func scalar(t *testing.T, s *store.Store, q string, args ...any) int {
	t.Helper()
	var n int
	if err := s.Pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(q, err)
	}
	return n
}

// secretBytes counts the occurrences of marker in every column that can hold a
// credential copy: version secrets, and task snapshots.
func secretBytes(t *testing.T, s *store.Store, marker string) int {
	t.Helper()
	return scalar(t, s, `SELECT (SELECT count(*) FROM project_cli_versions WHERE position(convert_to($1,'UTF8') IN secret)>0)
		+(SELECT count(*) FROM model_auth_versions WHERE position(convert_to($1,'UTF8') IN secret)>0)
		+(SELECT count(*) FROM task_instances WHERE position(convert_to($1,'UTF8') IN snapshot)>0)`, marker)
}

func pending(t *testing.T, s *store.Store, account uuid.UUID) bool {
	t.Helper()
	return scalar(t, s, `SELECT count(*) FROM credential_pending_wipes WHERE account_id=$1`, account) == 1
}

func versionRows(t *testing.T, s *store.Store, ids ...uuid.UUID) int {
	t.Helper()
	return scalar(t, s, `SELECT (SELECT count(*) FROM project_cli_versions WHERE id=ANY($1))+(SELECT count(*) FROM model_auth_versions WHERE id=ANY($1))`, ids)
}

// Deleting single accounts: versions no unfinished task uses are deleted in the
// same transaction and ended task snapshots lose the copies taken from them.
// Versions an unfinished task still binds or copied stay usable until the last
// such task ends, across a restart; the task end (or the startup settle when a
// task ended without one) then wipes them and scrubs the remaining copies.
func TestDeleteAccountWipesNowAndDefersVersionsOfUnfinishedTasks(t *testing.T) {
	f := newWipeFixture(t)
	ctx := context.Background()
	worker := f.agent(t, "worker")

	ghA, _ := cliPayload(t, map[string]string{"GH_TOKEN": "synthetic-gh-unreferenced"})
	ghB, ghBCopy := cliPayload(t, map[string]string{"GH_TOKEN": "synthetic-gh-ended"})
	ghC, ghCCopy := cliPayload(t, map[string]string{"GH_TOKEN": "synthetic-gh-unfinished"})
	gh, ghVersions := f.account(t, "cli", "gh", ghA, ghB, ghC)
	model, modelVersions := f.account(t, "model", "openai", modelSecret(t, "synthetic-model-unfinished"))
	keptLark, keptLarkCopy := cliPayload(t, map[string]string{"LARK_APP_SECRET": "synthetic-lark-kept"})
	f.account(t, "cli", "lark-cli", keptLark)

	// A psql account copied into the ended task only.
	fields, _ := json.Marshal(dbcli.Fields{Host: "db.example", Port: "5432", User: "u", Password: "synthetic-db-ended", Database: "d", SSLMode: "require"})
	dbPayload := clitemplate.Payload{Env: map[string]string{}, Files: []clitemplate.PayloadFile{{Path: dbcli.InputPath("psql"), Content: fields}}}
	dbSecret, _ := json.Marshal(dbPayload)
	db, dbVersions := f.account(t, "cli", "psql", dbSecret)
	project := f.project
	aggregated, err := clitemplate.AggregateDatabase("psql", []clitemplate.DatabaseEntry{{Connection: dbcli.Connection{ProjectID: &project, AccountID: db, Name: "db", VersionID: dbVersions[0]}, Payload: dbPayload}})
	if err != nil {
		t.Fatal(err)
	}
	dbCopy := taskruntime.CredentialPayload{Env: aggregated.Env, Files: []taskruntime.File{{Path: aggregated.Files[0].Path, Content: aggregated.Files[0].Content}}}
	// File contents are base64 in both JSON documents.
	dbMarkers := []string{base64.StdEncoding.EncodeToString(fields), base64.StdEncoding.EncodeToString(aggregated.Files[0].Content)}

	modelBinding := &modelcredential.Binding{VersionID: modelVersions[0], AgentID: worker.ID, Provider: "openai", OwnerKind: "project", ProjectID: &project, AccountID: &model}
	ended, endedRuntime := f.task(t, worker, []string{"gh", "lark-cli", "psql"}, taskruntime.Credentials{CLI: map[string]taskruntime.CredentialPayload{"gh": ghBCopy, "lark-cli": keptLarkCopy, "psql": dbCopy}})
	endTask(t, f.s, ended)
	first, firstRuntime := f.task(t, worker, []string{"gh"}, taskruntime.Credentials{ModelAuth: modelBinding, CLI: map[string]taskruntime.CredentialPayload{"gh": ghCCopy}})
	second, secondRuntime := f.task(t, worker, []string{"gh"}, taskruntime.Credentials{CLI: map[string]taskruntime.CredentialPayload{"gh": ghCCopy}})
	before := map[uuid.UUID]snapshotRow{endedRuntime: snapshotOf(t, f.s, endedRuntime), firstRuntime: snapshotOf(t, f.s, firstRuntime), secondRuntime: snapshotOf(t, f.s, secondRuntime)}
	for _, marker := range append([]string{"synthetic-gh-unreferenced", "synthetic-gh-ended", "synthetic-gh-unfinished", "synthetic-model-unfinished", "synthetic-lark-kept"}, dbMarkers...) {
		if secretBytes(t, f.s, marker) == 0 {
			t.Fatal("fixture should hold", marker)
		}
	}

	f.deleteAccount(t, gh)
	f.deleteAccount(t, model)
	f.deleteAccount(t, db)

	// Wiped now: versions nothing unfinished uses, and ended copies from them.
	for _, marker := range append([]string{"synthetic-gh-unreferenced", "synthetic-gh-ended"}, dbMarkers...) {
		if n := secretBytes(t, f.s, marker); n != 0 {
			t.Fatal("secret material remains after account deletion", marker, n)
		}
	}
	if n := versionRows(t, f.s, ghVersions[0], ghVersions[1], dbVersions[0]); n != 0 {
		t.Fatal("unused versions should be deleted", n)
	}
	if pending(t, f.s, db) {
		t.Fatal("nothing of the psql account is kept")
	}
	c := decodedCredentials(t, snapshotOf(t, f.s, endedRuntime).raw)
	if _, ok := c.CLI["gh"]; ok {
		t.Fatal("ended copy of the deleted gh account should be scrubbed")
	}
	if _, ok := c.CLI["psql"]; ok {
		t.Fatal("ended copy of the deleted psql account should be scrubbed")
	}
	if c.CLI["lark-cli"].Env["LARK_APP_SECRET"] != "synthetic-lark-kept" {
		t.Fatal("copies from live accounts must stay")
	}
	if snapshotOf(t, f.s, endedRuntime).sha != before[endedRuntime].sha {
		t.Fatal("snapshot identity must not change")
	}

	// Kept: the unfinished tasks still use the current gh copy and model binding.
	stillUsable := func(s *store.Store) {
		t.Helper()
		if n := versionRows(t, s, ghVersions[2], modelVersions[0]); n != 2 {
			t.Fatal("versions in use must stay", n)
		}
		if !pending(t, s, gh) || !pending(t, s, model) {
			t.Fatal("pending wipes must be persisted")
		}
		if !s.ValidateModelAuthBinding(ctx, *modelBinding) {
			t.Fatal("the unfinished task's model binding must stay valid")
		}
		v, err := s.GetModelAuthVersion(ctx, modelVersions[0])
		if err != nil || !bytes.Contains(v.Secret, []byte("synthetic-model-unfinished")) {
			t.Fatal("the bound model version must keep its secret", err)
		}
		for _, runtime := range []uuid.UUID{firstRuntime, secondRuntime} {
			row := snapshotOf(t, s, runtime)
			if !bytes.Equal(row.raw, before[runtime].raw) {
				t.Fatal("unfinished snapshots must not change")
			}
			if decodedCredentials(t, row.raw).CLI["gh"].Env["GH_TOKEN"] != "synthetic-gh-unfinished" {
				t.Fatal("the unfinished task's gh copy must stay usable")
			}
		}
	}
	stillUsable(f.s)

	// Restart: a new Store on the same schema, as after a server restart.
	var schema string
	if err := f.s.Pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	restarted, err := store.Open(ctx, pgtest.URL(t), store.OpenOptions{SearchPath: schema})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	if err = restarted.SettleCredentialWipes(ctx); err != nil {
		t.Fatal(err)
	}
	stillUsable(restarted)

	// The first task ends: its own copy goes, but the second task still holds
	// the gh version. The model version was only the first task's.
	endTask(t, restarted, first)
	if decodedCredentials(t, snapshotOf(t, restarted, firstRuntime).raw).CLI["gh"].Env != nil {
		t.Fatal("the ended task's copy should be scrubbed at task end")
	}
	if n := versionRows(t, restarted, modelVersions[0]); n != 0 || pending(t, restarted, model) {
		t.Fatal("the model version should be wiped once its last task ended", n)
	}
	if n := versionRows(t, restarted, ghVersions[2]); n != 1 || !pending(t, restarted, gh) {
		t.Fatal("the gh version is still used by the second task", n)
	}

	// The second task ends without a settle (as a crash would leave it); the
	// startup settle finishes the wipe.
	mustExec(t, restarted, `UPDATE task_records SET status='cancelled',ended_at=clock_timestamp(),document=jsonb_set(document,'{view,task,status}','"cancelled"') WHERE id=$1`, second)
	if err = restarted.SettleCredentialWipes(ctx); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"synthetic-gh-unfinished", "synthetic-model-unfinished"} {
		if n := secretBytes(t, restarted, marker); n != 0 {
			t.Fatal("secret material remains after the last task ended", marker, n)
		}
	}
	if n := versionRows(t, restarted, ghVersions[2]); n != 0 || pending(t, restarted, gh) {
		t.Fatal("the gh version should be wiped and the pending row gone", n)
	}
	if n := scalar(t, restarted, `SELECT count(*) FROM credential_pending_wipes`); n != 0 {
		t.Fatal("no pending wipe should remain", n)
	}
	for runtime, b := range before {
		row := snapshotOf(t, restarted, runtime)
		if row.sha != b.sha {
			t.Fatal("snapshot identity must not change", runtime)
		}
		decodedCredentials(t, row.raw)
	}
	if secretBytes(t, restarted, "synthetic-lark-kept") != 2 {
		t.Fatal("the live Lark account's version and copy must stay")
	}
	for _, id := range []uuid.UUID{gh, model, db} {
		r, err := restarted.GetProjectCredential(ctx, f.project, id)
		if err != nil || r.View.DeletedAt == nil || r.View.Status != "deleted" || r.View.CurrentVersionID != nil {
			t.Fatal("deleted account should read as deleted without a version", id, err)
		}
	}
}

// A Lark account bound by a task's account switch: while the task is
// unfinished the version, identity and grant stay; once it ends the immutable
// binding keeps the version only as a tombstone.
func TestDeleteAccountTombstonesLarkVersionBoundByTask(t *testing.T) {
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
	project, account, bound := r.View.Task.ProjectID, in.Capture.Target.AccountID, in.Capture.Target.VersionID
	unused := larkSwitchTargetWithScopes(t, s, project, time.Now().Add(time.Hour), []string{"offline_access"})
	var secret []byte
	if err = s.Pool.QueryRow(ctx, `SELECT secret FROM project_cli_versions WHERE id=$1`, bound).Scan(&secret); err != nil || len(secret) == 0 {
		t.Fatal("fixture should hold the bound secret", err)
	}
	identity := `SELECT (SELECT count(*) FROM lark_cli_identities WHERE version_id=$1)+(SELECT count(*) FROM lark_cli_grants WHERE version_id=$1)`
	remove := func(id uuid.UUID) {
		t.Helper()
		a, err := s.GetProjectCredential(ctx, project, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.MutateProjectCredential(ctx, project, id, a.View.Revision, nil, nil, true); err != nil {
			t.Fatal(err)
		}
	}

	remove(unused.View.ID)
	if n := versionRows(t, s, *unused.View.CurrentVersionID); n != 0 {
		t.Fatal("a version no task binds is deleted at once", n)
	}
	remove(account)
	var kept []byte
	if err = s.Pool.QueryRow(ctx, `SELECT secret FROM project_cli_versions WHERE id=$1`, bound).Scan(&kept); err != nil || !bytes.Equal(kept, secret) {
		t.Fatal("the unfinished task's bound version must keep its secret", err)
	}
	if n := scalar(t, s, identity, bound); n != 2 || !pending(t, s, account) {
		t.Fatal("identity and grant must stay while the task is unfinished", n)
	}

	endTask(t, s, in.TaskID)
	var fields, envs, files []string
	var label *string
	if err = s.Pool.QueryRow(ctx, `SELECT secret,field_names,env_names,file_paths,account_label FROM project_cli_versions WHERE id=$1`, bound).Scan(&kept, &fields, &envs, &files, &label); err != nil {
		t.Fatal("the version an ended binding references stays as a tombstone", err)
	}
	if len(kept) != 0 || len(fields)+len(envs)+len(files) != 0 || label == nil {
		t.Fatal("tombstone keeps only metadata", len(kept), fields, envs, files)
	}
	if n := scalar(t, s, identity, bound); n != 0 || pending(t, s, account) {
		t.Fatal("identity, grant and pending row should be gone", n)
	}
	if n := scalar(t, s, `SELECT (SELECT count(*) FROM task_lark_credential_bindings WHERE task_id=$1)*10+(SELECT count(*) FROM task_lark_binding_members WHERE task_id=$1)`, in.TaskID); n != 12 {
		t.Fatal("immutable task binding history must be kept", n)
	}
	a, err := s.GetProjectCredential(ctx, project, account)
	if err != nil || a.View.Status != "deleted" || a.View.CurrentVersionID != nil {
		t.Fatal("deleted Lark account should read without a version", err)
	}
}

// Deleting an Agent scrubs the credentials of its ended task snapshots. While
// the Agent is in an unfinished task its deletion is refused, so no scrub is
// deferred.
func TestDeleteAgentScrubsEndedTaskSnapshots(t *testing.T) {
	f := newWipeFixture(t)
	ctx := context.Background()
	worker, other := f.agent(t, "worker"), f.agent(t, "other")
	ghSecret, ghCopy := cliPayload(t, map[string]string{"GH_TOKEN": "synthetic-gh-agent"})
	f.account(t, "cli", "gh", ghSecret)
	_, otherCopy := cliPayload(t, map[string]string{"GH_TOKEN": "synthetic-gh-other"})

	ended, endedRuntime := f.task(t, worker, []string{"gh"}, taskruntime.Credentials{CLI: map[string]taskruntime.CredentialPayload{"gh": ghCopy}})
	endTask(t, f.s, ended)
	open, openRuntime := f.task(t, worker, []string{"gh"}, taskruntime.Credentials{CLI: map[string]taskruntime.CredentialPayload{"gh": ghCopy}})
	otherTask, otherRuntime := f.task(t, other, []string{"gh"}, taskruntime.Credentials{CLI: map[string]taskruntime.CredentialPayload{"gh": otherCopy}})
	endTask(t, f.s, otherTask)
	before := map[uuid.UUID]snapshotRow{endedRuntime: snapshotOf(t, f.s, endedRuntime), openRuntime: snapshotOf(t, f.s, openRuntime), otherRuntime: snapshotOf(t, f.s, otherRuntime)}

	err := f.s.SoftDeleteAgent(ctx, worker.ID)
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != apperr.CodeTaskNotIdle {
		t.Fatal("an Agent in an unfinished task cannot be deleted", err)
	}
	for runtime, b := range before {
		if !bytes.Equal(snapshotOf(t, f.s, runtime).raw, b.raw) {
			t.Fatal("a refused deletion must not touch snapshots", runtime)
		}
	}

	endTask(t, f.s, open)
	if err = f.s.SoftDeleteAgent(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	for _, runtime := range []uuid.UUID{endedRuntime, openRuntime} {
		row := snapshotOf(t, f.s, runtime)
		if bytes.Contains(row.raw, []byte("synthetic-gh-agent")) {
			t.Fatal("the deleted Agent's ended snapshot still holds plaintext", runtime)
		}
		if row.sha != before[runtime].sha {
			t.Fatal("snapshot identity must not change", runtime)
		}
		if c := decodedCredentials(t, row.raw); len(c.Model) != 0 || len(c.CLI) != 0 {
			t.Fatal("scrubbed snapshot should carry no model or CLI credentials", runtime)
		}
	}
	if !bytes.Equal(snapshotOf(t, f.s, otherRuntime).raw, before[otherRuntime].raw) {
		t.Fatal("other Agents' snapshots must stay")
	}
	if secretBytes(t, f.s, "synthetic-gh-agent") != 1 {
		t.Fatal("the project account's own version is not the Agent's to wipe")
	}
}

// 030 rolls back only while no deleted account waits for its wipe.
func TestCredentialPendingWipesDownRefusesData(t *testing.T) {
	f := newWipeFixture(t)
	ctx := context.Background()
	secret, _ := cliPayload(t, map[string]string{"GH_TOKEN": "synthetic-gh-down"})
	account, _ := f.account(t, "cli", "gh", secret)
	mustExec(t, f.s, `INSERT INTO credential_pending_wipes(account_id,project_id) VALUES($1,$2)`, account, f.project)
	if err := f.s.MigrateDownTo(ctx, 29); err == nil || !strings.Contains(err.Error(), "credential_pending_wipes_down_refused") {
		t.Fatal("Down must refuse while a wipe is pending", err)
	}
	if !pending(t, f.s, account) {
		t.Fatal("the refused Down must keep the pending row")
	}
	mustExec(t, f.s, `DELETE FROM credential_pending_wipes`)
	if err := f.s.MigrateDownTo(ctx, 29); err != nil {
		t.Fatal(err)
	}
	if n := scalar(t, f.s, `SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='credential_pending_wipes'`); n != 0 {
		t.Fatal("Down should drop the table", n)
	}
}
