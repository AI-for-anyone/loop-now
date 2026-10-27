package app_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"loop-now/internal/server/contracttest"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/app"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/cliimport"
	"loop-now/internal/server/cliimport/source"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/config"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/taskruntime"
	"loop-now/internal/server/workflow"
)

func TestCLIImportActualAppExplicitScanAtomicSaveReplayAndRecovery(t *testing.T) {
	w := newWorld(t)
	home := t.TempDir()
	put := func(name, value string) {
		t.Helper()
		p := filepath.Join(home, name)
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte(value), 0600); e != nil {
			t.Fatal(e)
		}
	}
	put(".config/gh/hosts.yml", "github.com:\n  user: synthetic\n  oauth_token: SYNTHETIC_PRIVATE_GH_TOKEN\n")
	catalog, e := clitemplate.Load("")
	if e != nil {
		t.Fatal(e)
	}
	var opened atomic.Int32
	factory := func(ctx context.Context) (cliimport.Reader, error) { opened.Add(1); return source.New(home, catalog) }
	var validators []*contracttest.Validator
	start := func() *server {
		t.Helper()
		ln, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		a, e := app.Build(context.Background(), &w.cfg, config.NewLogger(io.Discard, "json", 0), app.Options{SearchPath: w.schema, Listener: ln, HomeDirs: []string{"/nonexistent-home"}, CLIImportReaderFactory: factory})
		if e != nil {
			t.Fatal(e)
		}
		go a.Serve()
		validators = append(validators, contracttest.New(t, "http://"+a.Addr()))
		return &server{t: t, a: a, url: "http://" + a.Addr() + "/api/v1"}
	}
	s := start()
	must := func(status int, method, path string, value any) []byte {
		t.Helper()
		var body io.Reader
		if value != nil {
			raw, e := json.Marshal(value)
			if e != nil {
				t.Fatal(e)
			}
			body = bytes.NewReader(raw)
		}
		req, e := http.NewRequest(method, s.url+path, body)
		if e != nil {
			t.Fatal(e)
		}
		if value != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, e := validators[len(validators)-1].Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		raw, e := io.ReadAll(resp.Body)
		if e != nil || resp.StatusCode != status {
			t.Fatalf("%s %s expected %d got %d", method, path, status, resp.StatusCode)
		}
		return raw
	}
	defer func() {
		if s != nil {
			s.stop()
		}
	}()
	_, agent := s.createAgent("import owner")
	project := *agent.ProjectID
	base := "/projects/" + project.String() + "/cli-credential-import"
	must(200, "GET", "/projects/"+project.String()+"/credential-accounts", nil)
	if opened.Load() != 0 {
		t.Fatal("startup/page GET opened source")
	}
	raw := must(201, "POST", base+"/scans", map[string]any{})
	if strings.Contains(string(raw), "SYNTHETIC_PRIVATE") || strings.Contains(string(raw), home) {
		t.Fatal("scan exposed private source")
	}
	var scan cliimport.Scan
	if json.Unmarshal(raw, &scan) != nil || len(scan.Candidates) != 1 || scan.Candidates[0].Status != "may_import" {
		t.Fatal("expected exact synthetic GH candidate")
	}
	in := cliimport.StartInput{OperationID: uuid.New(), ScanID: scan.ScanID, CandidateIDs: []uuid.UUID{scan.Candidates[0].CandidateID}, ExpectedCredentialsRevision: scan.CredentialsRevision}
	must(202, "POST", base+"/imports", in)
	wait := func(id uuid.UUID) cliimport.Operation {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			var state cliimport.State
			b := must(200, "GET", base+"/imports/"+id.String(), nil)
			if json.Unmarshal(b, &state) != nil {
				t.Fatal("invalid operation")
			}
			if strings.Contains(string(b), "SYNTHETIC_PRIVATE") {
				t.Fatal("secret in operation")
			}
			if state.Operation.Terminal() {
				return state.Operation
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("operation did not settle")
		return cliimport.Operation{}
	}
	op := wait(in.OperationID)
	if op.Status != "succeeded" || len(op.Items) != 1 || op.Items[0].Status != "imported" || op.Items[0].AccountID == nil || op.Items[0].VersionID == nil {
		t.Fatal("atomic import not saved", op.Status)
	}
	row, _, e := s.a.Store.GetProjectCLIVersion(context.Background(), *op.Items[0].AccountID, *op.Items[0].VersionID)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(row.Secret), "SYNTHETIC_PRIVATE_GH_TOKEN") {
		t.Fatal("wrong private credential")
	}
	must(202, "POST", base+"/imports", in)
	if opened.Load() != 1 {
		t.Fatal("replay opened source")
	}
	var count int
	if e = s.a.Store.Pool.QueryRow(context.Background(), "SELECT count(*) FROM project_credential_accounts WHERE project_id=$1", project).Scan(&count); e != nil || count != 1 {
		t.Fatal("duplicate account", e)
	}

	// Persist an accepted operation from the prior instance without a worker.
	// Rebuilding App must terminate it, never reconstruct its private Reader.
	var keyID string
	if e = s.a.Store.Pool.QueryRow(context.Background(), "SELECT provenance_key_id FROM cli_credential_import_receipts WHERE project_id=$1 LIMIT 1", project).Scan(&keyID); e != nil {
		t.Fatal(e)
	}
	abandonedID := uuid.New()
	fingerprint := sha256.Sum256([]byte("synthetic prior-instance accepted request"))
	_, created, e := s.a.Store.BeginCLIImport(context.Background(), store.CLIImportBegin{ID: abandonedID, ProjectID: project, ScanID: uuid.New(), InstanceID: s.a.InstanceID, RequestFingerprint: fingerprint[:], ExpectedRevision: *op.CredentialsRevision, ProvenanceKeyID: keyID, Deadline: time.Now().Add(time.Minute), Items: []store.CLIImportItem{{CandidateID: uuid.New(), CLI: "gh", Label: "prior instance", Status: "pending"}}})
	if e != nil || !created {
		t.Fatal("failed prior-instance fixture", e)
	}
	s.stop()
	s = nil
	s = start()
	recovered := wait(abandonedID)
	if recovered.Status != "failed" || recovered.ErrorCode == nil || *recovered.ErrorCode != "worker_restarted" || opened.Load() != 1 {
		t.Fatal("App restart replayed or failed to terminate prior operation")
	}

	restored := wait(in.OperationID)
	if restored.Status != "succeeded" || opened.Load() != 1 {
		t.Fatal("restart replayed extraction")
	}
	must(202, "POST", base+"/imports", in)
	if opened.Load() != 1 {
		t.Fatal("same operation after restart rescanned")
	}
	var runs, turns int
	if e = s.a.Store.Pool.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM runs),(SELECT count(*) FROM task_turns)").Scan(&runs, &turns); e != nil || runs != 0 || turns != 0 {
		t.Fatal("import dispatched runtime", e)
	}
	for _, v := range validators {
		if len(v.Failures()) != 0 {
			t.Fatal("formal HTTP contract failures")
		}
	}
	t.Log("actual TEST App/current schema: startup/account GET source0; explicit synthetic GH scan/save original version; replay and restart source1/account1; models/task0; no host/provider")
}

func startCLIImportWorld(t *testing.T, w *world, factory func(context.Context) (cliimport.Reader, error)) *server {
	t.Helper()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	a, e := app.Build(context.Background(), &w.cfg, config.NewLogger(io.Discard, "json", 0), app.Options{SearchPath: w.schema, Listener: ln, HomeDirs: []string{"/nonexistent-home"}, CLIImportReaderFactory: factory})
	if e != nil {
		t.Fatal(e)
	}
	go a.Serve()
	return &server{t: t, a: a, url: "http://" + a.Addr() + "/api/v1"}
}

type appImportWaitReader struct {
	cliimport.Reader
	entered chan *appImportWaitReader
	done    chan struct{}
}

func (r *appImportWaitReader) Extract(ctx context.Context, s source.Selection, x source.Supplement, f func(map[string]string) error) error {
	r.entered <- r
	defer close(r.done)
	<-ctx.Done()
	return ctx.Err()
}
func TestCLIImportActualAppProjectDeleteCommitAndRollback(t *testing.T) {
	w := newWorld(t)
	home := t.TempDir()
	dir := filepath.Join(home, ".config/gh")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "hosts.yml"), []byte("github.com:\n  user: synthetic\n  oauth_token: NEVER_OUTPUT_SYNTHETIC\n"), 0600); e != nil {
		t.Fatal(e)
	}
	entered := make(chan *appImportWaitReader, 2)
	factory := func(ctx context.Context) (cliimport.Reader, error) {
		r, e := source.New(home, nil)
		if e != nil {
			return nil, e
		}
		return &appImportWaitReader{Reader: r, entered: entered, done: make(chan struct{})}, nil
	}
	s := startCLIImportWorld(t, w, factory)
	defer s.stop()
	ctx := context.Background()
	org := s.a.API.O.Defaults.Organization.ID
	p, e := s.a.Store.CreateProject(ctx, org, "delete import")
	if e != nil {
		t.Fatal(e)
	}
	q, e := s.a.Store.CreateProject(ctx, org, "retain import")
	if e != nil {
		t.Fatal(e)
	}
	start := func(project uuid.UUID) (uuid.UUID, *appImportWaitReader) {
		t.Helper()
		base := "/projects/" + project.String() + "/cli-credential-import"
		scan := decode[cliimport.Scan](t, s.must(201, "POST", base+"/scans", map[string]any{}))
		if len(scan.Candidates) != 1 {
			t.Fatal("candidate count")
		}
		id := uuid.New()
		s.must(202, "POST", base+"/imports", cliimport.StartInput{OperationID: id, ScanID: scan.ScanID, CandidateIDs: []uuid.UUID{scan.Candidates[0].CandidateID}, ExpectedCredentialsRevision: scan.CredentialsRevision})
		select {
		case r := <-entered:
			return id, r
		case <-time.After(3 * time.Second):
			t.Fatal("extract barrier not reached")
			return id, nil
		}
	}
	id, r := start(p.ID)
	other, neighbor := start(q.ID)
	_, e = s.a.Store.Pool.Exec(ctx, `CREATE FUNCTION fail_import_project_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.deleted_at IS NOT NULL THEN RAISE EXCEPTION 'synthetic commit refusal'; END IF; RETURN NEW; END $$; CREATE CONSTRAINT TRIGGER fail_import_project_delete AFTER UPDATE ON projects DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_import_project_delete()`)
	if e != nil {
		t.Fatal(e)
	}
	s.must(500, "DELETE", "/projects/"+p.ID.String(), nil)
	select {
	case <-r.done:
		t.Fatal("rollback cancelled worker")
	default:
	}
	var status string
	if e = s.a.Store.Pool.QueryRow(ctx, "SELECT status FROM cli_credential_import_operations WHERE id=$1", id).Scan(&status); e != nil || status != "running" {
		t.Fatal("rollback ended original operation", e)
	}
	if _, e = s.a.Store.Pool.Exec(ctx, "DROP TRIGGER fail_import_project_delete ON projects; DROP FUNCTION fail_import_project_delete()"); e != nil {
		t.Fatal(e)
	}
	s.must(204, "DELETE", "/projects/"+p.ID.String(), nil)
	select {
	case <-r.done:
	case <-time.After(time.Second):
		t.Fatal("commit did not drain target reader")
	}
	select {
	case <-neighbor.done:
		t.Fatal("other project's worker cancelled")
	default:
	}
	var code string
	var accounts int
	if e = s.a.Store.Pool.QueryRow(ctx, "SELECT status,error_code,(SELECT count(*) FROM project_credential_accounts WHERE project_id=$2) FROM cli_credential_import_operations WHERE id=$1", id, p.ID).Scan(&status, &code, &accounts); e != nil || status != "cancelled" || code != "project_deleted" || accounts != 0 {
		t.Fatal("late import survived project delete", e)
	}
	s.must(404, "GET", "/projects/"+p.ID.String()+"/cli-credential-import/imports/"+id.String(), nil)
	state := decode[cliimport.State](t, s.must(200, "POST", "/projects/"+q.ID.String()+"/cli-credential-import/imports/"+other.String()+"/cancel", map[string]any{}))
	if state.Operation.Status != "cancelled" {
		t.Fatal("neighbor cancel state")
	}
	select {
	case <-neighbor.done:
	case <-time.After(time.Second):
		t.Fatal("neighbor reader did not end")
	}
	t.Log("actual TEST App DELETE: deferred COMMIT refusal preserves running worker; committed delete same-Tx cancelled/project_deleted + immediate reader drain; other project unchanged; accounts0; no host/provider")
}

func TestCLIImportActualAppFiveStaticSourcesAtomicAndNoRetrofit(t *testing.T) {
	w := newWorld(t)
	home := t.TempDir()
	original := map[string][32]byte{}
	put := func(name, value string) {
		t.Helper()
		p := filepath.Join(home, name)
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte(value), 0600); e != nil {
			t.Fatal(e)
		}
		original[name] = sha256.Sum256([]byte(value))
	}
	put(".config/gh/hosts.yml", "github.com:\n  user: synthetic\n  oauth_token: PRIVATE_SYNTHETIC_GH\n")
	put(".lark-cli/config.json", `{"strictMode":"off","currentApp":"fixture","apps":[{"name":"fixture","appId":"cli_fixture","appSecret":"PRIVATE_SYNTHETIC_LARK","brand":"feishu","defaultAs":"bot","users":[]}]}`)
	put(".pg_service.conf", "[fixture]\nhost=offline.invalid\nport=5432\nuser=fixture\ndbname=fixture\nsslmode=disable\npassword=PRIVATE_SYNTHETIC_PG\n")
	put(".my.cnf", "[client]\nhost=offline.invalid\nport=3306\nuser=fixture\npassword=PRIVATE_SYNTHETIC_MYSQL\ndatabase=fixture\nssl-mode=DISABLED\n")
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	encodedKey, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	private := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey})
	clear(encodedKey)
	sa, e := json.Marshal(map[string]string{"type": "service_account", "project_id": "synthetic-project", "private_key_id": "synthetic-key", "private_key": string(private), "client_email": "robot@synthetic-project.iam.gserviceaccount.com", "client_id": "123456", "token_uri": "https://oauth2.googleapis.com/token"})
	clear(private)
	if e != nil {
		t.Fatal(e)
	}
	put(".config/gcloud/application_default_credentials.json", string(sa))
	clear(sa)
	put(".config/gcloud/legacy_credentials/synthetic/adc.json", `{"type":"authorized_user","refresh_token":"DO_NOT_COPY_PRIVATE_PERSONAL"}`)
	factory := func(ctx context.Context) (cliimport.Reader, error) { return source.New(home, nil) }
	s := startCLIImportWorld(t, w, factory)
	defer s.stop()
	_, agent := s.createAgent("permission unchanged")
	project := *agent.ProjectID
	base := "/projects/" + project.String() + "/cli-credential-import"
	scan := decode[cliimport.Scan](t, s.must(201, "POST", base+"/scans", map[string]any{}))
	ids := []uuid.UUID{}
	seen := map[string]bool{}
	personal := false
	for _, c := range scan.Candidates {
		if c.Status == "may_import" {
			ids = append(ids, c.CandidateID)
			seen[c.CLI] = true
		} else if c.Status == "requires_platform_login" {
			personal = true
		} else {
			t.Fatal("unexpected synthetic source classification", c.CLI, c.Status)
		}
	}
	if len(ids) != 5 || len(seen) != 5 || !personal {
		t.Fatal("five static sources or personal distinction missing")
	}
	in := cliimport.StartInput{OperationID: uuid.New(), ScanID: scan.ScanID, CandidateIDs: ids, ExpectedCredentialsRevision: scan.CredentialsRevision}
	s.must(202, "POST", base+"/imports", in)
	var op cliimport.Operation
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		op = decode[cliimport.State](t, s.must(200, "GET", base+"/imports/"+in.OperationID.String(), nil)).Operation
		if op.Terminal() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if op.Status != "succeeded" || len(op.Items) != 5 || op.CredentialsRevision == nil || *op.CredentialsRevision != scan.CredentialsRevision+1 {
		t.Log("safe operation status", op.Status, "errorCode", op.ErrorCode)
		for _, item := range op.Items {
			if item.ErrorCode != nil {
				t.Log("safe item", item.CLI, *item.ErrorCode)
			}
		}
		t.Fatal("whole batch not one revision", op.Status)
	}
	for _, item := range op.Items {
		if item.Status != "imported" || item.AccountID == nil || item.VersionID == nil {
			t.Fatal("item not saved")
		}
		row, _, e := s.a.Store.GetProjectCLIVersion(context.Background(), *item.AccountID, *item.VersionID)
		if e != nil {
			t.Fatal(e)
		}
		var payload clitemplate.Payload
		if json.Unmarshal(row.Secret, &payload) != nil {
			t.Fatal("bad canonical credential")
		}
		if strings.Contains(string(s.must(200, "GET", "/projects/"+project.String()+"/credential-accounts/"+item.AccountID.String(), nil)), "PRIVATE_SYNTHETIC") {
			t.Fatal("secret in management projection")
		}
	}
	var accounts, grants, runs, turns, allowed, sets int
	e = s.a.Store.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM project_credential_accounts WHERE project_id=$1),(SELECT count(*) FROM google_cli_grants),(SELECT count(*) FROM runs),(SELECT count(*) FROM task_turns),(SELECT count(*) FROM agent_clis WHERE agent_id=$2),(SELECT count(*) FROM agent_db_connection_sets WHERE agent_id=$2)`, project, agent.ID).Scan(&accounts, &grants, &runs, &turns, &allowed, &sets)
	if e != nil {
		t.Fatal(e)
	}
	if accounts != 5 || grants != 0 || runs != 0 || turns != 0 || allowed != 0 || sets != 0 {
		t.Fatal("import expanded permissions, grants or runtime")
	}
	for name, sum := range original {
		raw, e := os.ReadFile(filepath.Join(home, name))
		if e != nil || sha256.Sum256(raw) != sum {
			clear(raw)
			t.Fatal("synthetic source modified")
		}
		clear(raw)
	}
	t.Log("actual TEST App five static source batch: accounts5/revision+1/original version, personal requires_platform_login excluded; Agent allowed/set0; OAuthgrant/model/task0; source hashes unchanged; no host/provider")
}

func TestCLIImportActualAppSecondAccountKeepsCapturedTaskAndPrivate(t *testing.T) {
	w := newWorld(t)
	home := t.TempDir()
	dir := filepath.Join(home, ".config/gh")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "hosts.yml"), []byte("github.com:\n  user: synthetic-B\n  oauth_token: synthetic-import-B\n"), 0600); e != nil {
		t.Fatal(e)
	}
	s := startCLIImportWorld(t, w, func(ctx context.Context) (cliimport.Reader, error) { return source.New(home, nil) })
	defer s.stop()
	ctx := context.Background()
	if e := s.a.Tasks.Stop(ctx); e != nil {
		t.Fatal(e)
	}
	_, agent := s.createAgent("captured import owner")
	project := *agent.ProjectID
	s.must(201, "POST", "/projects/"+project.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "synthetic model", "credential": map[string]any{"type": "api_key", "key": "synthetic-model-key"}})
	s.must(201, "POST", "/projects/"+project.String()+"/credential-accounts", map[string]any{"purpose": "cli", "cli": "gh", "name": "original A", "credential": map[string]any{"type": "cli", "fields": map[string]string{"token": "synthetic-task-A"}}})
	if e := s.a.Store.AddAgentCLI(ctx, agent.ID, "gh"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.a.Store.SetProjectManager(ctx, project, agent.ID); e != nil {
		t.Fatal(e)
	}
	raw, e := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: agent.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "original capture", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "work", AgentID: agent.ID, Inputs: map[string]workflow.Source{}, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{{Name: "answer", Type: "string", Required: true}}}}, Edges: []workflow.Edge{}}})
	if e != nil {
		t.Fatal(e)
	}
	detail := decode[taskmodel.Detail](t, raw)
	instances, e := s.a.Store.TaskInstances(ctx, detail.Task.ID)
	if e != nil || len(instances) != 1 || instances[0].ID == detail.Task.ID {
		t.Fatal("real runtime capture missing", e)
	}
	original := sha256.Sum256(instances[0].Snapshot)
	platform, e := s.a.Store.CreateAgent(ctx, store.PlatformScope(), "private import untouched", "anthropic", "metadata-only", "")
	if e != nil {
		t.Fatal(e)
	}
	if e := s.a.Store.AddAgentCLI(ctx, platform.ID, "gh"); e != nil {
		t.Fatal(e)
	}
	payload, _ := json.Marshal(clitemplate.Payload{Env: map[string]string{"GH_TOKEN": "synthetic-private"}, Files: []clitemplate.PayloadFile{}})
	defer clear(payload)
	private, e := s.a.Store.PutCredential(ctx, platform.ID, store.PurposeCLI, "gh", store.CredentialMeta{TemplateVersion: "1"}, payload)
	if e != nil {
		t.Fatal(e)
	}
	privateHash := sha256.Sum256(private.Secret)
	base := "/projects/" + project.String() + "/cli-credential-import"
	scan := decode[cliimport.Scan](t, s.must(201, "POST", base+"/scans", map[string]any{}))
	if len(scan.Candidates) != 1 {
		t.Fatal("candidate missing")
	}
	in := cliimport.StartInput{OperationID: uuid.New(), ScanID: scan.ScanID, CandidateIDs: []uuid.UUID{scan.Candidates[0].CandidateID}, ExpectedCredentialsRevision: scan.CredentialsRevision}
	s.must(202, "POST", base+"/imports", in)
	var op cliimport.Operation
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		op = decode[cliimport.State](t, s.must(200, "GET", base+"/imports/"+in.OperationID.String(), nil)).Operation
		if op.Terminal() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if op.Status != "succeeded" {
		t.Fatal("import did not succeed")
	}
	after, e := s.a.Store.TaskInstances(ctx, detail.Task.ID)
	if e != nil || len(after) != 1 || sha256.Sum256(after[0].Snapshot) != original {
		t.Fatal("import retrofitted task snapshot", e)
	}
	snapshot, e := taskruntime.Decode(after[0].Snapshot)
	if e != nil {
		t.Fatal(e)
	}
	credentials, e := snapshot.Credentials()
	if e != nil || credentials.CLI["gh"].Env["GH_TOKEN"] != "synthetic-task-A" {
		t.Fatal("original captured A replaced", e)
	}
	unchanged, e := s.a.Store.ListCredentials(ctx, platform.ID)
	if e != nil || len(unchanged) != 1 || unchanged[0].ID != private.ID || sha256.Sum256(unchanged[0].Secret) != privateHash {
		t.Fatal("platform private credential changed", e)
	}
	selections, e := s.a.Store.GetCredentialSelections(ctx, agent.ID)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, selection := range selections.Selections {
		if selection.CLI != nil && *selection.CLI == "gh" {
			found = true
			if selection.Mode != "automatic" || selection.ResolvedAccountID != nil || len(selection.Candidates) != 2 {
				t.Fatal("second account silently selected or hid ambiguity")
			}
		}
	}
	if !found {
		t.Fatal("selection projection missing")
	}
	var runs, turns int
	if e = s.a.Store.Pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM runs),(SELECT count(*) FROM task_turns)").Scan(&runs, &turns); e != nil || runs != 0 || turns != 0 {
		t.Fatal("import woke task/model", e)
	}
	t.Log("actual Tasks.Create distinct task/runtime: import B keeps original A/schema/hash; automatic next-use has two candidates/no resolved account; private platform credential/ID unchanged; model/task0")
}
