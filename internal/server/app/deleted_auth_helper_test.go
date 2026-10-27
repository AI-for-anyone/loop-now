package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/app"
	"loop-now/internal/server/config"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/store"
)

func (w *world) startDeletionAuth(helper modelauth.LoginHelper) *server {
	w.t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		w.t.Fatal(err)
	}
	a, err := app.Build(context.Background(), &w.cfg, config.NewLogger(io.Discard, "text", w.cfg.LogLevel), app.Options{SearchPath: w.schema, Listener: ln, HomeDirs: []string{"/nonexistent-home"}, AuthLoginHelper: helper, AuthRefresher: &taskAuthOffline{}})
	if err != nil {
		ln.Close()
		w.t.Fatal(err)
	}
	go func() { _ = a.Serve() }()
	return &server{t: w.t, a: a, url: "http://" + a.Addr() + "/api/v1"}
}
func deletionLogin(t *testing.T, s *server, project uuid.UUID, name string) (apitypes.Agent, modelauth.Operation) {
	t.Helper()
	path := "/platform/agents"
	if project != uuid.Nil {
		path = "/projects/" + project.String() + "/agents"
	}
	a := decode[apitypes.Agent](t, s.must(201, "POST", path, map[string]any{"name": name, "model": map[string]string{"provider": "openai-codex", "modelId": "gpt-6.1-sol"}, "prompt": "offline"}))
	if project == uuid.Nil {
		return a, decode[modelauth.Operation](t, s.must(200, "POST", "/agents/"+a.ID.String()+"/model-auth/operations", map[string]any{"provider": "openai-codex", "expectedConfigVersion": a.ConfigVersion}))
	}
	// Two independent target accounts allow testing a project's multiple live
	// helper workers without pretending two first-login operations can coexist.
	raw, _ := json.Marshal(map[string]any{"type": "oauth", "access": "synthetic-old-access", "refresh": "synthetic-old-refresh", "accountId": "synthetic-" + name, "expires": time.Now().Add(time.Hour).UnixMilli()})
	defer clear(raw)
	account, err := s.a.Store.CreateProjectCredential(context.Background(), project, "model", "openai-codex", name, store.ProjectCredentialWrite{AuthMethod: "oauth", Secret: raw})
	if err != nil {
		t.Fatal(err)
	}
	state := decode[cm.LoginState](t, s.must(202, "POST", "/projects/"+project.String()+"/model-auth/operations", map[string]any{"provider": "openai-codex", "accountId": account.View.ID, "expectedRevision": account.View.Revision}))
	return a, modelauth.Operation{ID: state.Operation.ID, AgentID: a.ID, Status: state.Operation.Status}
}
func deletionOperation(s *server, a apitypes.Agent, id uuid.UUID) (modelauth.Operation, error) {
	if a.ProjectID == nil {
		op, e := s.a.Store.GetModelAuthOperation(context.Background(), a.ID, id)
		return modelauth.Operation{ID: op.ID, Status: op.Status, AuthorizationURL: op.AuthorizationURL}, e
	}
	op := modelauth.Operation{ID: id}
	e := s.a.Store.Pool.QueryRow(context.Background(), `SELECT status,authorization_url FROM project_model_auth_operations WHERE id=$1 AND project_id=$2`, id, *a.ProjectID).Scan(&op.Status, &op.AuthorizationURL)
	return op, e
}
func nodeDeletionHelper(t *testing.T) (*modelauth.Helper, string) {
	t.Helper()
	root := t.TempDir()
	entry := filepath.Join(root, "delete-fixture.mjs")
	marker, _ := json.Marshal(root)
	code := `import fs from 'node:fs';import readline from 'node:readline';import net from 'node:net';
 const root=` + string(marker) + `;const send=x=>fs.writeSync(4,JSON.stringify(x)+'\n');let op,server;
 send({type:'ready',protocolVersion:1,expectedPiVersion:'1.1.0'});
 readline.createInterface({input:fs.createReadStream('',{fd:3})}).on('line',line=>{
 const f=JSON.parse(line);
 if(f.type==='login'){op=f;server=net.createServer(c=>c.end());server.listen(0,'127.0.0.1',()=>{
 fs.writeFileSync(root+'/'+f.id+'.json',JSON.stringify({pid:process.pid,port:server.address().port,home:process.env.HOME}));
 send({type:'notify',id:f.id,event:{type:'auth_url',url:'https://auth.openai.com/oauth/authorize?state=offline-delete&code_challenge=offline-delete'}});});}
 if(f.type==='abort'){server.close(()=>{send({type:'result',id:op.id,status:'cancelled'});process.exit(0);});}
 });`
	if err := os.WriteFile(entry, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	return &modelauth.Helper{Options: modelauth.HelperOptions{NodeBin: "/Users/ljl/.local/node-v24.21.0/bin/node", Entry: entry, WorkRoot: root, BasePath: "/usr/bin:/bin", Timeout: time.Minute, StopGrace: 50 * time.Millisecond}}, root
}

type deletionResource struct {
	PID  int    `json:"pid"`
	Port int    `json:"port"`
	Home string `json:"home"`
}

func deletionReady(t *testing.T, s *server, a apitypes.Agent, op modelauth.Operation, root string) deletionResource {
	t.Helper()
	var resource deletionResource
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		b, err := os.ReadFile(filepath.Join(root, op.ID.String()+".json"))
		if err == nil && json.Unmarshal(b, &resource) == nil {
			record, e := deletionOperation(s, a, op.ID)
			if e == nil && record.Status == "awaiting_authorization" {
				return resource
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("offline Node helper did not bind resource")
	return resource
}
func assertDeletionResourceGone(t *testing.T, r deletionResource) {
	t.Helper()
	if !errors.Is(syscall.Kill(r.PID, 0), syscall.ESRCH) {
		t.Fatal("deleted login child still exists")
	}
	if _, err := os.Stat(r.Home); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("deleted login retained temporary HOME", err)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(r.Port)))
	if err != nil {
		t.Fatal("deleted login retained callback fixture port", err)
	}
	ln.Close()
}
func TestDeletedLoginHelperResources(t *testing.T) {
	for _, mode := range []string{"agent", "project", "project-rollback"} {
		t.Run(mode, func(t *testing.T) {
			w := newWorld(t)
			helper, root := nodeDeletionHelper(t)
			s := w.startDeletionAuth(helper)
			defer s.stop()
			project := decode[apitypes.Project](t, s.must(201, "POST", "/projects", map[string]any{"name": "资源清理项目"}))
			loginProject := project.ID
			if mode == "agent" {
				loginProject = uuid.Nil
			}
			a, op := deletionLogin(t, s, loginProject, "资源一")
			first := deletionReady(t, s, a, op, root)
			agents, ops, resources := []apitypes.Agent{a}, []modelauth.Operation{op}, []deletionResource{first}
			if mode != "agent" {
				b, bop := deletionLogin(t, s, project.ID, "资源二")
				agents = append(agents, b)
				ops = append(ops, bop)
				resources = append(resources, deletionReady(t, s, b, bop, root))
			}
			outside, outsideOp := deletionLogin(t, s, uuid.Nil, "无关登录")
			outsideResource := deletionReady(t, s, outside, outsideOp, root)
			path := "/projects/" + project.ID.String()
			if mode == "agent" {
				path = "/agents/" + a.ID.String()
			}
			if mode == "project-rollback" {
				for _, q := range []string{`CREATE FUNCTION reject_helper_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.deleted_at IS NOT NULL THEN RAISE EXCEPTION 'synthetic rollback'; END IF; RETURN NEW; END $$`, `CREATE TRIGGER reject_helper_delete BEFORE UPDATE ON projects FOR EACH ROW EXECUTE FUNCTION reject_helper_delete()`} {
					if _, err := s.a.Store.Pool.Exec(context.Background(), q); err != nil {
						t.Fatal(err)
					}
				}
				s.must(500, "DELETE", path, nil)
				for i, r := range resources {
					if err := syscall.Kill(r.PID, 0); err != nil {
						t.Fatal("rollback cancelled login child", err)
					}
					ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(r.Port)))
					if err == nil {
						ln.Close()
						t.Fatal("rollback released active callback resource")
					}
					record, err := deletionOperation(s, agents[i], ops[i].ID)
					if err != nil || record.Status != "awaiting_authorization" || record.AuthorizationURL == nil {
						t.Fatal("rollback changed active login", err)
					}
				}
				if _, err := s.a.Store.Pool.Exec(context.Background(), `DROP TRIGGER reject_helper_delete ON projects`); err != nil {
					t.Fatal(err)
				}
			}
			s.must(204, "DELETE", path, nil)
			for i, r := range resources {
				assertDeletionResourceGone(t, r)
				record, err := deletionOperation(s, agents[i], ops[i].ID)
				if err != nil || record.Status != "cancelled" || record.AuthorizationURL != nil {
					t.Fatal("deleted login not cancelled", err)
				}
			}
			if err := syscall.Kill(outsideResource.PID, 0); err != nil {
				t.Fatal("unrelated Agent login was cancelled", err)
			}
			unaffected, err := deletionOperation(s, outside, outsideOp.ID)
			if err != nil || unaffected.Status != "awaiting_authorization" || unaffected.AuthorizationURL == nil {
				t.Fatal("unrelated login state changed", err)
			}
			s.must(204, "DELETE", "/agents/"+outside.ID.String(), nil)
			assertDeletionResourceGone(t, outsideResource)
		})
	}
}

type lateDeletionLogin struct{ started, cancelled, release chan struct{} }

func (f *lateDeletionLogin) Login(ctx context.Context, _ uuid.UUID, notify func(string) error) (modelcredential.Credential, error) {
	if err := notify("https://auth.openai.com/oauth/authorize?state=offline-late&code_challenge=offline-late"); err != nil {
		return modelcredential.Credential{}, err
	}
	close(f.started)
	<-ctx.Done()
	close(f.cancelled)
	<-f.release
	return modelcredential.Decode([]byte(`{"type":"oauth","access":"synthetic-late-access","refresh":"synthetic-late-refresh","accountId":"synthetic-late-account","expires":9999999999999}`))
}

func TestDeletedLoginLateSuccessCannotSaveAndDrainIsBounded(t *testing.T) {
	w := newWorld(t)
	helper := &lateDeletionLogin{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	s := w.startDeletionAuth(helper)
	defer s.stop()
	defer func() {
		select {
		case <-helper.release:
		default:
			close(helper.release)
		}
	}()
	s.a.ModelAuth.Options.LoginDrainTimeout = 50 * time.Millisecond
	a, op := deletionLogin(t, s, uuid.Nil, "迟到授权")
	select {
	case <-helper.started:
	case <-time.After(3 * time.Second):
		t.Fatal("login did not start")
	}
	start := time.Now()
	s.must(204, "DELETE", "/agents/"+a.ID.String(), nil)
	if time.Since(start) > time.Second {
		t.Fatal("deleted login drain unbounded")
	}
	select {
	case <-helper.cancelled:
	case <-time.After(time.Second):
		t.Fatal("deleted login never cancelled")
	}
	close(helper.release)
	s.a.ModelAuth.Options.LoginDrainTimeout = 3 * time.Second
	err := s.a.ModelAuth.CancelAgentLogins(context.Background(), []uuid.UUID{a.ID})
	if err != nil {
		t.Fatal(err)
	}
	var versions int
	if err = s.a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM model_auth_versions WHERE agent_id=$1`, a.ID).Scan(&versions); err != nil || versions != 0 {
		t.Fatal("late success recreated grant", err)
	}
	record, err := deletionOperation(s, a, op.ID)
	if err != nil || record.Status != "cancelled" || record.AuthorizationURL != nil {
		t.Fatal("late success resurrected login", err)
	}
}
