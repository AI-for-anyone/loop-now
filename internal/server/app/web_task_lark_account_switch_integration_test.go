package app_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"io"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/app"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/config"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/larkauth/larktest"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/redistest"
	"loop-now/internal/testutil/testenv"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Web owns this test file only. Existing world/native helpers are read-only.
// The actual production Manager and pinned 014 executable are mandatory.
func TestWebTaskLarkAccountSwitchNativeIntegration(t *testing.T) {
	if os.Getenv("LOOP_NOW_WEB_TASK_LARK_NATIVE") != "1" {
		t.Skip("explicit TEST-only actual native desktop slice")
	}
	root := testenv.RepoRoot()
	evidence := filepath.Join(root, "web/test-results/allow-blocked-task-lark-account-switch/app-native", time.Now().UTC().Format("20060102T150405.000000000Z"))
	if os.MkdirAll(evidence, 0700) != nil {
		t.Fatal("evidence directory failed")
	}
	audit := map[string]any{"boundary": "actual desktop React -> temporary production App/TEST PG/Redis -> pinned014 native/private channel + fakePi -> owned TLS provider fixture; no real OAuth/model/QA"}
	var schema, prefix, workRoot, address string
	passed := t.Run("isolated", func(t *testing.T) {
		bundle := unpackLarkVariant(t)
		ctx := context.Background()
		var phase atomic.Int32
		var denied atomic.Int32
		var mu sync.Mutex
		observed := map[int32]string{}
		requests := map[int32]int{}
		upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			token := r.Header.Get("Authorization")
			if !strings.HasPrefix(token, "Bearer synthetic-access-") {
				t.Error("unexpected fixture identity")
				w.WriteHeader(401)
				return
			}
			p := phase.Load()
			mu.Lock()
			if old := observed[p]; old != "" && old != token {
				t.Error("native scope changed fixed identity")
			}
			observed[p] = token
			requests[p]++
			mu.Unlock()
			switch r.URL.Path {
			case "/open-apis/authen/v1/user_info":
				json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"open_id": "ou-switch-native", "name": "Offline"}})
			case "/open-apis/im/v1/messages":
				io.WriteString(w, `{"code":0,"data":{"items":[],"has_more":false}}`)
			default:
				t.Error("unexpected native fixture route")
				w.WriteHeader(400)
			}
		}))
		defer upstream.Close()
		target, _ := url.Parse(upstream.URL)
		transport := upstream.Client().Transport.(*http.Transport).Clone()
		transport.Proxy = nil
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
		transport.TLSClientConfig.ServerName = "127.0.0.1"
		transport.TLSClientConfig.MinVersion = tls.VersionTLS12
		transport.DialContext = func(c context.Context, network, address string) (net.Conn, error) {
			if address == "open.feishu.cn:443" {
				return (&net.Dialer{}).DialContext(c, "tcp", target.Host)
			}
			host, _, e := net.SplitHostPort(address)
			ip := net.ParseIP(host)
			if e != nil || ip == nil || !ip.IsLoopback() {
				denied.Add(1)
				return nil, errors.New("non-loopback denied")
			}
			return (&net.Dialer{}).DialContext(c, network, address)
		}
		defer transport.CloseIdleConnections()
		world := taskNodeInboxWorld(t)
		schema, prefix, workRoot = world.schema, "", world.cfg.WorkRoot
		quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
		wrapper := filepath.Join(t.TempDir(), "switch-native-before-fakepi")
		script := "#!/bin/sh\nset -eu\nif [ \"${1-}\" = --version ]; then exec " + quote(world.cfg.PiBin) + " \"$@\"; fi\nif [ -n \"${LOOP_NOW_LARK_EXEC_URL-}\" ]; then\nlark-cli api GET /open-apis/im/v1/messages --params '{\"container_id_type\":\"chat\",\"container_id\":\"oc_synthetic\"}' --as user > switch-native-output.json 2> switch-native-error.txt\nfi\nexec " + quote(world.cfg.PiBin) + " \"$@\"\n"
		if e := os.WriteFile(wrapper, []byte(script), 0700); e != nil {
			t.Fatal(e)
		}
		world.cfg.PiBin = wrapper
		world.cfg.PiIdleTimeout = time.Minute
		// Constructor-only injection, with no global transport or caller-controlled
		// runtime setter. Only this App can reach the owned upstream fixture.
		cfg := world.cfg
		cfg.LarkUserBundleDir = bundle
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		instanceApp, e := app.Build(ctx, &cfg, config.NewLogger(io.Discard, cfg.LogFormat, cfg.LogLevel), app.Options{SearchPath: world.schema, Listener: listener, HomeDirs: []string{"/nonexistent-home"}, LarkHTTPTransport: transport})
		if e != nil {
			listener.Close()
			t.Fatal(e)
		}
		go func() { _ = instanceApp.Serve() }()
		server := &server{t: t, a: instanceApp, url: "http://" + instanceApp.Addr() + "/api/v1"}
		defer server.stop()
		address = instanceApp.Addr()
		prefix = instanceApp.Coord.Keys().Prefix
		instanceApp.API.O.ConsoleOrigins = []string{"http://127.0.0.1:4336"}
		provider := larktest.New(t)
		server.a.CLIAuth.Lark = provider.Client
		project, base, accounts, source := larkAppSource(t, server)
		users := make([]cm.Account, 2)
		for i := range users {
			attempt := provider.Add("ou-switch-native", "Offline", nil, "")
			attempt.Release()
			op := uuid.New()
			server.must(202, "POST", base, map[string]any{"operationId": op, "cli": "lark-cli", "authType": "lark_user", "sourceAccountId": source.ID, "expectedSourceRevision": source.Revision})
			users[i] = *waitLarkApp(t, server, base, op, "succeeded").Account
		}
		server.must(201, "POST", accounts, map[string]any{"purpose": "model", "provider": "anthropic", "name": "switch native fixture", "credential": map[string]string{"type": "api_key", "key": "synthetic-native-model"}})
		_, manager := server.createAgent("NativeSwitchManager")
		server.must(200, "PUT", "/projects/"+project.String()+"/manager", map[string]any{"agentId": manager.ID})
		agents := make([]apitypes.Agent, 2)
		for i, name := range []string{"NativePublisher", "NativeFollowup"} {
			path, a := server.createAgent(name)
			agents[i] = a
			server.must(200, "PUT", path+"/clis/lark-cli", nil)
			current := decode[apitypes.Agent](t, server.must(200, "GET", path, nil))
			server.must(200, "PUT", path+"/credential-selections", map[string]any{"purpose": "cli", "cli": "lark-cli", "accountId": users[0].ID, "expectedConfigVersion": current.ConfigVersion})
		}
		raw, e := server.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: manager.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "actual native task switch", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "qa", Steps: []workflow.Step{{ID: "qa", Title: "QA", AgentID: agents[0].ID, Description: " !error !text=original_switch_fixture"}, {ID: "followup", Title: "Followup", AgentID: agents[1].ID}}, Edges: []workflow.Edge{{ID: "done", From: "qa", To: "followup", Label: "done"}}}})
		if e != nil {
			t.Fatal(e)
		}
		detail := decode[taskmodel.Detail](t, raw)
		blocked := taskWait(t, server, detail.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
		instances, e := server.a.Store.TaskInstances(ctx, detail.Task.ID)
		if e != nil || len(instances) != 2 {
			t.Fatal(e)
		}
		before := map[uuid.UUID][32]byte{}
		input := taskmodel.TaskLarkSwitchInput{ActionID: uuid.New(), ExpectedTaskVersion: blocked.Task.Version, ExpectedNodeRunID: *blocked.Task.CurrentNodeRunID, ExpectedAgentID: agents[0].ID, TargetAccountID: users[1].ID, TargetVersionID: *users[1].CurrentVersionID, ExpectedAccountRevision: users[1].Revision}
		var publisher store.TaskInstance
		for _, inst := range instances {
			before[inst.ID] = sha256.Sum256(inst.Snapshot)
			input.ExpectedMembers = append(input.ExpectedMembers, taskmodel.TaskLarkSwitchMemberInput{AgentID: inst.AgentID, RuntimeID: inst.ID})
			if inst.AgentID == agents[0].ID {
				publisher = inst
				input.ExpectedRuntimeID = inst.ID
			}
		}
		if publisher.SessionFile == nil {
			t.Fatal("publisher has no actual retained session")
		}
		session := *publisher.SessionFile
		// A non-secret retained artifact is explicitly seeded TEST history,
		// not presented as an outcome generated by the native command/model.
		artifactBytes := []byte("existing synthetic QA result; retain across account handoff\n")
		artifactSHA := sha256.Sum256(artifactBytes)
		artifactFile := filepath.Join(workRoot, "agents", publisher.ID.String(), "workspace", "existing-qa-result.txt")
		if e := os.WriteFile(artifactFile, artifactBytes, 0600); e != nil {
			t.Fatal("TEST retained file setup failed", e)
		}
		artifact := taskmodel.Artifact{ID: uuid.New(), TaskID: detail.Task.ID, NodeRunID: input.ExpectedNodeRunID, Name: "existing-qa-result.txt", ContentType: "text/plain", Size: int64(len(artifactBytes)), SHA256: fmt.Sprintf("%x", artifactSHA), CreatedAt: time.Now().UTC()}
		artifact.DownloadPath = "/tasks/" + detail.Task.ID.String() + "/artifacts/" + artifact.ID.String()
		_, e = server.a.Store.MutateTask(ctx, detail.Task.ID, nil, func(m *store.TaskMutation) (any, error) {
			if _, e := m.Tx.Exec(ctx, `INSERT INTO blobs(sha256,size,data) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, artifact.SHA256, artifact.Size, artifactBytes); e != nil {
				return nil, e
			}
			if _, e := m.Tx.Exec(ctx, `INSERT INTO task_artifacts(id,task_id,node_run_id,name,content_type,blob_sha256,size,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, artifact.ID, artifact.TaskID, artifact.NodeRunID, artifact.Name, artifact.ContentType, artifact.SHA256, artifact.Size, artifact.CreatedAt); e != nil {
				return nil, e
			}
			m.Record.View.Artifacts = append(m.Record.View.Artifacts, artifact)
			return nil, nil
		})
		if e != nil {
			t.Fatal("TEST retained artifact history setup failed", e)
		}
		// The original native command has completed, while its real Pi/session and
		// native scope remain alive. Phase B must stop these exact old generations.
		if server.a.Executor.Process(publisher.ID).State == "stopped" {
			t.Fatal("expected live idle publisher Pi")
		}
		phase.Store(1)
		path := "/tasks/" + detail.Task.ID.String() + "/lark-account-switches"
		// Read the real console projection, not a constructed participant response.
		view := decode[apitypes.ConsoleTaskDetail](t, server.must(200, "GET", "/tasks/"+detail.Task.ID.String(), nil))
		if view.LarkSwitchParticipants == nil || len(*view.LarkSwitchParticipants) != 2 {
			t.Fatal("actual participants missing")
		}
		for _, member := range *view.LarkSwitchParticipants {
			if member.LarkAllowed == nil || !*member.LarkAllowed || !member.Available {
				t.Fatal("actual participant not eligible")
			}
		}
		artifactRecordBefore, e := json.Marshal(view.Artifacts)
		if e != nil || len(view.Artifacts) != 1 {
			t.Fatal("retained artifact record missing before switch")
		}
		projectView := decode[apitypes.Context](t, server.must(200, "GET", "/context", nil)).Project
		manifest := map[string]any{"slice": "native-commit", "apiBase": server.url, "webOrigin": "http://127.0.0.1:4336", "projectName": projectView.Name, "taskId": detail.Task.ID, "expectedTaskVersion": view.Task.Version, "expectedNodeRunId": input.ExpectedNodeRunID, "expectedAgentId": input.ExpectedAgentID, "expectedRuntimeId": input.ExpectedRuntimeID, "expectedMembers": input.ExpectedMembers, "targetAccountId": input.TargetAccountID, "targetVersionId": input.TargetVersionID, "evidenceDir": evidence}
		manifestPath := filepath.Join(t.TempDir(), "manifest.json")
		data, e := json.Marshal(manifest)
		if e != nil || os.WriteFile(manifestPath, data, 0600) != nil {
			t.Fatal("browser manifest failed")
		}
		browserCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(browserCtx, "/Users/ljl/.local/node-v24.21.0/bin/node", filepath.Join(root, "web/scripts/lark-switch-app.mjs"))
		cmd.Dir = root
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LOOP_NOW_WEB_LARK_SWITCH_MANIFEST=" + manifestPath}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		t.Cleanup(func() {
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			}
		})
		if cmd.Run() != nil {
			t.Fatal("actual native desktop browser failed; safe evidence retained")
		}
		var browser struct {
			Passed bool        `json:"passed"`
			Starts []uuid.UUID `json:"starts"`
		}
		data, e = os.ReadFile(filepath.Join(evidence, "browser-results.json"))
		if e != nil || json.Unmarshal(data, &browser) != nil || !browser.Passed || len(browser.Starts) != 1 {
			t.Fatal("single browser POST receipt missing")
		}
		input.ActionID = browser.Starts[0]
		receipt := decode[taskmodel.TaskLarkSwitchReceipt](t, server.must(200, "GET", path+"/"+input.ActionID.String(), nil))
		if receipt.Status != "succeeded" || receipt.Continuation == nil {
			t.Fatal("switch not committed", receipt.Status)
		}
		taskWait(t, server, detail.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" && len(d.Nodes) == 2 })
		after, e := server.a.Store.TaskInstance(ctx, publisher.ID)
		if e != nil || after.SessionFile == nil || *after.SessionFile != session {
			t.Fatal("switch replaced retained session", e)
		}
		for _, inst := range instances {
			latest, e := server.a.Store.TaskInstance(ctx, inst.ID)
			if e != nil || sha256.Sum256(latest.Snapshot) != before[inst.ID] {
				t.Fatal("original snapshot rewritten", e)
			}
			if inst.ID != publisher.ID && latest.Prepared != inst.Prepared {
				t.Fatal("switch changed original followup preparation")
			}
		}
		snap, e := server.a.Tasks.Source.ExecutionSnapshot(ctx, publisher.ID)
		if e != nil || snap.LarkBinding == nil || snap.LarkBinding.VersionID != *users[1].CurrentVersionID {
			t.Fatal("effective source not fixed target", e)
		}
		mu.Lock()
		a, b := observed[0], observed[1]
		n0, n1 := requests[0], requests[1]
		mu.Unlock()
		if a == "" || b == "" || a == b || n0 == 0 || n1 == 0 || denied.Load() != 0 {
			t.Fatal("native fixed identity/loopback evidence missing")
		}
		var groups, members, followTurns int
		server.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_lark_credential_bindings WHERE task_id=$1`, detail.Task.ID).Scan(&groups)
		server.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_lark_binding_members WHERE task_id=$1`, detail.Task.ID).Scan(&members)
		server.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE task_id=$1 AND runtime_id<>$2`, detail.Task.ID, publisher.ID).Scan(&followTurns)
		if groups != 1 || members != 2 || followTurns != 0 {
			t.Fatal("partial/duplicate publication or followup dispatch")
		}
		old := decode[taskmodel.TaskLarkSwitchReceipt](t, server.must(200, "GET", path+"/"+input.ActionID.String(), nil))
		if old.Continuation.RunID != receipt.Continuation.RunID {
			t.Fatal("original GET created replacement")
		}

		retained := decode[apitypes.ConsoleTaskDetail](t, server.must(200, "GET", "/tasks/"+detail.Task.ID.String(), nil))
		artifactRecordAfter, e := json.Marshal(retained.Artifacts)
		if e != nil || !bytes.Equal(artifactRecordBefore, artifactRecordAfter) {
			t.Fatal("original artifact record changed")
		}
		retainedBytes, e := os.ReadFile(artifactFile)
		if e != nil || sha256.Sum256(retainedBytes) != artifactSHA {
			t.Fatal("original workspace artifact file changed")
		}
		blob, e := server.a.Store.ReadBlob(ctx, artifact.SHA256)
		if e != nil || sha256.Sum256(blob) != artifactSHA {
			t.Fatal("original artifact blob changed")
		}
		audit["originalArtifactRecordsRetained"] = true
		audit["originalArtifactBlobRetained"] = true
		audit["originalWorkspaceArtifactRetained"] = true
		audit["artifactFixtureBoundary"] = "one explicit non-secret TEST history record/blob/workspace file, not a model/native-generated QA outcome"
		audit["actualProductionManager"] = true
		audit["originalLiveIdlePi"] = true
		audit["originalSessionRetained"] = true
		audit["snapshotHashesUnchanged"] = true
		audit["nativeIdentityChanged"] = a != "" && b != "" && a != b
		audit["nativeRequestsBefore"] = n0
		audit["nativeRequestsAfter"] = n1
		audit["deniedProviderDials"] = denied.Load() + provider.DeniedDials.Load()
		audit["bindingGroups"] = groups
		audit["bindingMembers"] = members
		audit["followupTurns"] = followTurns
		audit["originalActionRestored"] = old.Continuation.RunID == receipt.Continuation.RunID
		if provider.DeniedDials.Load() != 0 {
			t.Error("login fixture attempted provider dial")
		}
		var actions, continuations int
		if server.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_lark_switch_actions WHERE task_id=$1`, detail.Task.ID).Scan(&actions) != nil || actions != 1 {
			t.Error("action duplicated")
		}
		if server.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE task_id=$1 AND node_run_id=$2`, detail.Task.ID, receipt.Continuation.NodeRunID).Scan(&continuations) != nil || continuations != 1 {
			t.Error("continuation duplicated")
		}
		audit["actions"] = actions
		audit["continuationTurns"] = continuations

	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if schema != "" {
		conn, e := pgx.Connect(ctx, pgtest.URL(t))
		if e != nil {
			t.Fatal("cleanup PG unavailable")
		}
		defer conn.Close(ctx)
		var exists bool
		e = conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)", schema).Scan(&exists)
		audit["schemaRemoved"] = e == nil && !exists
		if e != nil || exists {
			t.Error("schema remains")
		}
	}
	if prefix != "" {
		ro, e := redis.ParseURL(redistest.URL(t))
		if e != nil {
			t.Fatal("cleanup Redis unavailable")
		}
		rc := redis.NewClient(ro)
		defer rc.Close()
		it := rc.Scan(ctx, 0, prefix+"*", 100).Iterator()
		found := it.Next(ctx)
		audit["redisPrefixEmpty"] = !found && it.Err() == nil
		if found || it.Err() != nil {
			t.Error("prefix remains")
		}
	}
	if workRoot != "" {
		_, e := os.Stat(workRoot)
		audit["workRootRemoved"] = os.IsNotExist(e)
		if !os.IsNotExist(e) {
			t.Error("workroot remains")
		}
	}
	closed := true
	for _, addr := range []string{address, "127.0.0.1:4336"} {
		if addr == "" {
			continue
		}
		c, e := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if e == nil {
			closed = false
			c.Close()
			t.Error("listener remains")
		}
	}
	audit["listenersClosed"] = closed
	audit["passed"] = passed && !t.Failed()
	data, e := json.MarshalIndent(audit, "", "  ")
	if e != nil || os.WriteFile(filepath.Join(evidence, "driver-results.json"), data, 0600) != nil {
		t.Error("safe evidence write failed")
	}
}
