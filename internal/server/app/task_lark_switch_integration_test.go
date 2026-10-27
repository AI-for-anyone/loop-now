package app_test

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"loop-now/internal/server/config"
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
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/app"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/contracttest"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/executor/local"
	"loop-now/internal/server/inbox"
	"loop-now/internal/server/larkauth/larktest"
	"loop-now/internal/server/larksidecar"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks"
	"loop-now/internal/server/workflow"
)

// Real App/TEST PG/Redis and canonical HTTP account/login/task capture, with a
// loopback-only official-protocol provider fixture. Original blocked/session
// facts are explicitly synthetic; this is not native execution or public POST.
func taskLarkSwitchAppReady(t *testing.T) (*server, store.TaskLarkFenceReservation, map[uuid.UUID][32]byte, *larktest.Provider) {
	t.Helper()
	w := taskNodeInboxWorld(t)
	provider := larktest.New(t)
	s := w.start(nil)
	t.Cleanup(s.stop)
	ctx := context.Background()
	// Stop only this self-owned App's driver before constructing synthetic
	// blocked history. No fakePi/model/native command should run in this slice.
	if err := s.a.Tasks.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	s.a.CLIAuth.Lark = provider.Client
	project := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil)).Project.ID
	accounts := "/projects/" + project.String() + "/credential-accounts"
	base := "/projects/" + project.String() + "/cli-auth/operations"
	s.must(201, "POST", accounts, map[string]any{"purpose": "model", "provider": "anthropic", "name": "offline model", "credential": map[string]string{"type": "api_key", "key": "synthetic-model"}})
	bot := decode[cm.Account](t, s.must(201, "POST", accounts, map[string]any{"purpose": "cli", "cli": "lark-cli", "name": "original bot", "credential": map[string]any{"type": "cli", "fields": map[string]string{"appId": "app-fixture", "appSecret": "fixture-app-secret", "brand": "feishu"}}}))
	path, publisher := s.createAgent("SwitchPublisher")
	s.must(200, "PUT", path+"/clis/lark-cli", nil)
	path, followup := s.createAgent("SwitchFollowup")
	s.must(200, "PUT", path+"/clis/lark-cli", nil)
	s.must(200, "PUT", "/projects/"+project.String()+"/manager", map[string]any{"agentId": publisher.ID})
	raw, err := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: publisher.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "isolated switch capture", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "qa", Steps: []workflow.Step{{ID: "qa", Title: "QA", AgentID: publisher.ID}, {ID: "followup", Title: "Followup", AgentID: followup.ID}}, Edges: []workflow.Edge{{ID: "qa-done", From: "qa", To: "followup", Label: "done"}}}})
	if err != nil {
		t.Fatal(err)
	}
	detail := decode[taskmodel.Detail](t, raw)
	instances, err := s.a.Store.TaskInstances(ctx, detail.Task.ID)
	if err != nil || len(instances) != 2 {
		t.Fatal("real task capture", err)
	}
	in := store.TaskLarkFenceReservation{TaskID: detail.Task.ID, ActionID: uuid.New(), NodeRunID: uuid.New(), Capture: &store.TaskLarkSwitchCapture{}}
	before := map[uuid.UUID][32]byte{}
	for _, inst := range instances {
		role := "followup"
		if inst.AgentID == publisher.ID {
			role = "publisher"
		}
		in.Members = append(in.Members, store.TaskLarkFenceMember{AgentID: inst.AgentID, RuntimeID: inst.ID, Role: role})
		before[inst.ID] = sha256.Sum256(inst.Snapshot)
	}
	var pub store.TaskLarkFenceMember
	for _, m := range in.Members {
		if m.Role == "publisher" {
			pub = m
		}
	}
	session := filepath.Join(t.TempDir(), "synthetic-retained.jsonl")
	if err = os.WriteFile(session, []byte("synthetic original retained session\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = s.a.Store.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Record.View.Task.Status = "blocked"
		m.Record.View.Task.CurrentNodeRunID = &in.NodeRunID
		m.Record.View.Nodes = []taskmodel.NodeRun{{ID: in.NodeRunID, TaskID: in.TaskID, StepID: "qa", Visit: 1, Attempt: 1, AgentID: pub.AgentID, RuntimeID: pub.RuntimeID, Status: "blocked", RunIDs: []uuid.UUID{}}}
		_, err := m.Tx.Exec(ctx, `UPDATE task_instances SET prepared=true,session_state='active',session_file=$2 WHERE id=$1`, pub.RuntimeID, session)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture := provider.Add("ou-switch", "Switch User", nil, "")
	fixture.Release()
	operation := uuid.New()
	s.must(202, "POST", base, map[string]any{"operationId": operation, "cli": "lark-cli", "authType": "lark_user", "sourceAccountId": bot.ID, "expectedSourceRevision": bot.Revision})
	user := waitLarkApp(t, s, base, operation, "succeeded").Account
	if user == nil || user.LarkIdentity == nil || user.LarkIdentity.GrantedScopes != nil {
		t.Fatal("canonical unknown grant metadata changed")
	}
	_, err = s.a.Store.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		in.ExpectedTaskVersion = m.Record.View.Task.Version
		var err error
		in.Capture.Target, err = s.a.Store.CaptureTaskLarkTargetTx(ctx, m, user.ID, *user.CurrentVersionID, user.Revision)
		if err != nil {
			return nil, err
		}
		for _, member := range in.Members {
			actual, err := s.a.Store.CaptureTaskLarkMemberTx(ctx, m, member)
			if err != nil {
				return nil, err
			}
			in.Capture.Members = append(in.Capture.Members, actual)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, in, before, provider
}

// Adapter only delegates to the actual manager, matching production app's
// private adapter. This slice has no preexisting native scopes/processes and
// does not prove native execution assets, sandbox, or production assembly.
type taskLarkAppNative struct{ manager *larksidecar.Manager }
type taskLarkAppLease struct{ gate *larksidecar.RuntimeGate }

func (n *taskLarkAppNative) Acquire(ctx context.Context, ids []uuid.UUID) (tasks.TaskLarkNativeLease, error) {
	g, err := n.manager.AcquireRuntimeGate(ctx, ids)
	if err != nil {
		return nil, err
	}
	return &taskLarkAppLease{g}, nil
}
func (l *taskLarkAppLease) Seal(ctx context.Context) error { return l.gate.Seal(ctx) }
func (l *taskLarkAppLease) Release()                       { l.gate.Release() }

func TestTaskLarkSwitchAppHTTPActualEmptyScopeManagers(t *testing.T) {
	s, in, before, provider := taskLarkSwitchAppReady(t)
	ctx := context.Background()
	input := taskmodel.TaskLarkSwitchInput{ActionID: in.ActionID, ExpectedNodeRunID: in.NodeRunID, ExpectedTaskVersion: in.ExpectedTaskVersion, ExpectedLarkBindingRevision: in.Capture.ExpectedBindingRevision, TargetAccountID: in.Capture.Target.AccountID, TargetVersionID: in.Capture.Target.VersionID, ExpectedAccountRevision: in.Capture.Target.AccountRevision}
	for _, member := range in.Capture.Members {
		input.ExpectedMembers = append(input.ExpectedMembers, taskmodel.TaskLarkSwitchMemberInput{AgentID: member.AgentID, RuntimeID: member.RuntimeID, ExpectedBindingRevision: member.PreviousRevision})
		if member.Role == "publisher" {
			input.ExpectedAgentID, input.ExpectedRuntimeID = member.AgentID, member.RuntimeID
		}
	}
	path := "/tasks/" + in.TaskID.String() + "/lark-account-switches"
	// Actual default composition has no native factory/manager installed yet.
	// Prove it fails before A rather than silently accepting a success callback.
	s.a.Tasks.LarkSwitchNative = nil
	s.must(503, "POST", path, input)
	var count int
	if err := s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_lark_switch_actions`).Scan(&count); err != nil || count != 0 {
		t.Fatal("unready native created an action", err)
	}
	s.a.Tasks.LarkSwitchNative = &taskLarkAppNative{larksidecar.NewManager()}
	raw := s.must(202, "POST", path, input)
	receipt := decode[taskmodel.TaskLarkSwitchReceipt](t, raw)
	if receipt.Status != "succeeded" || receipt.Continuation == nil || receipt.BindingRevision == nil || receipt.DispatchStatus == nil || *receipt.DispatchStatus != "queued" {
		t.Fatal("actual HTTP did not publish unique intent")
	}
	doc := contracttest.Load(t)
	var wire any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if err := doc.Components.Schemas["TaskLarkSwitchReceipt"].Value.VisitJSON(wire, openapi3.EnableJSONSchema2020()); err != nil {
		t.Fatal("real receipt differs from formal contract", err)
	}
	again := decode[taskmodel.TaskLarkSwitchReceipt](t, s.must(202, "POST", path, input))
	got := decode[taskmodel.TaskLarkSwitchReceipt](t, s.must(200, "GET", path+"/"+in.ActionID.String(), nil))
	if again.Continuation == nil || got.Continuation == nil || again.Continuation.RunID != receipt.Continuation.RunID || got.Continuation.RunID != receipt.Continuation.RunID {
		t.Fatal("HTTP replay/GET repeated C")
	}
	out, err := s.a.Tasks.Source.ExecutionSnapshot(ctx, input.ExpectedRuntimeID)
	if err != nil || out.LarkBinding == nil || out.LarkBinding.VersionID != input.TargetVersionID || !strings.Contains(out.CredentialIdentity, ";lark-overlay:1:"+out.LarkBinding.RuntimeVariantVersion) || len(out.CLIPayloads["lark-cli"].Files) != 0 {
		t.Fatal("actual Source did not select fixed overlay", err)
	}
	for _, member := range in.Members {
		inst, err := s.a.Store.TaskInstance(ctx, member.RuntimeID)
		if err != nil || sha256.Sum256(inst.Snapshot) != before[member.RuntimeID] {
			t.Fatal("HTTP C changed original snapshot", err)
		}
		if member.Role == "followup" && (inst.Prepared || inst.SessionFile != nil) {
			t.Fatal("C prepared or woke followup")
		}
	}
	var groups, members, turns, runs int
	if err = s.a.Store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_credential_bindings),(SELECT count(*) FROM task_lark_binding_members),(SELECT count(*) FROM task_turns),(SELECT count(*) FROM runs)`).Scan(&groups, &members, &turns, &runs); err != nil || groups != 1 || members != 2 || turns != 1 || runs != 0 || provider.DeniedDials.Load() != 0 {
		t.Fatal("actual App repeated publication or invoked model", groups, members, turns, runs, err)
	}
	// Controlled real CLI with an isolated task-runtime fixture token. No Pi
	// autonomously calls it; no host HOME/credentials are inherited.
	bin := inboxActualCLI(t)
	home := t.TempDir()
	tok, err := auth.NewToken(auth.AgentPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.a.Store.InsertAgentToken(ctx, tok.ID, input.ExpectedRuntimeID, s.a.InstanceID, tok.Hash); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "submit.json")
	if err = os.WriteFile(file, []byte(`{"actionId":"`+uuid.NewString()+`","output":{},"edgeId":"qa-done"}`), 0600); err != nil {
		t.Fatal(err)
	}
	runCLI := func(exit int, args ...string) map[string]any {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "LOOPCTL_AGENT_URL=" + strings.TrimSuffix(s.url, "/api/v1") + "/agent/v1", "LOOPCTL_AGENT_TOKEN=" + tok.Plain}
		raw, err := cmd.Output()
		code := 0
		if err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				code = e.ExitCode()
			} else {
				t.Fatal("controlled CLI did not start")
			}
		}
		if code != exit {
			t.Fatalf("controlled CLI exit=%d want=%d", code, exit)
		}
		var out map[string]any
		if json.Unmarshal(raw, &out) != nil {
			t.Fatal("controlled CLI output was not JSON")
		}
		return out
	}
	runCLI(8, "task", "submit", in.TaskID.String(), receipt.Continuation.NodeRunID.String(), "--from-file", file, "--run", receipt.Continuation.RunID.String())
	claimed, found, err := s.a.Store.ClaimTaskTurn(ctx, in.TaskID, input.ExpectedRuntimeID)
	if err != nil || !found || claimed.ID != receipt.Continuation.RunID {
		t.Fatal("actual recovery claim", err)
	}
	if err = s.a.Store.MarkTaskRunning(ctx, claimed, 1); err != nil {
		t.Fatal(err)
	}
	runCLI(8, "task", "submit", in.TaskID.String(), receipt.Continuation.NodeRunID.String(), "--from-file", file)
	submitted := runCLI(0, "task", "submit", in.TaskID.String(), receipt.Continuation.NodeRunID.String(), "--from-file", file, "--run", claimed.ID.String())
	if submitted["data"].(map[string]any)["status"] != "submitted" {
		t.Fatal("actual CLI did not submit exact run")
	}
}

func TestTaskLarkSwitchAppInternalPhaseAAndOrdinaryInbox(t *testing.T) {
	s, in, before, provider := taskLarkSwitchAppReady(t)
	ctx := context.Background()
	var pub store.TaskLarkFenceMember
	for _, member := range in.Members {
		if member.Role == "publisher" {
			pub = member
		}
	}
	state, created, err := s.a.Tasks.ReserveLarkSwitch(ctx, auth.Console(), in)
	if err != nil || !created || state.Status != "pending" {
		t.Fatal("actual App internal A", err)
	}
	if _, created, err = s.a.Tasks.ReserveLarkSwitch(ctx, auth.Console(), in); err != nil || created {
		t.Fatal("App replay repeated reservation", err)
	}
	msg, err := s.a.API.Inbox.Send(ctx, auth.Console(), "默认项目/SwitchPublisher#"+in.TaskID.String(), "durable message during switch", "interrupt", inbox.QuestionInput{})
	if err != nil {
		t.Fatal("actual Inbox transaction", err)
	}
	read, err := s.a.Store.GetInboxMessage(ctx, msg.ID, 160)
	if err != nil || read.ReadAt != nil || read.To.RuntimeID != pub.RuntimeID {
		t.Fatal("Inbox target/read changed", err)
	}
	var deferred, runs, turns, bindings int
	if err = s.a.Store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_deferred_inbox WHERE message_id=$1),(SELECT count(*) FROM runs),(SELECT count(*) FROM task_turns),(SELECT count(*) FROM task_lark_credential_bindings)`, msg.ID).Scan(&deferred, &runs, &turns, &bindings); err != nil || deferred != 1 || runs+turns+bindings != 0 {
		t.Fatal("A/Inbox launched work or published scope", deferred, runs, turns, bindings, err)
	}
	for _, member := range in.Members {
		if _, found, err := s.a.Store.ClaimTaskTurn(ctx, in.TaskID, member.RuntimeID); err != nil || found {
			t.Fatal("workflow claim bypassed fence", err)
		}
		if _, found, err := s.a.Store.ClaimTaskInboxTurn(ctx, in.TaskID, member.RuntimeID); err != nil || found {
			t.Fatal("Inbox claim bypassed fence", err)
		}
		inst, err := s.a.Store.TaskInstance(ctx, member.RuntimeID)
		if err != nil || sha256.Sum256(inst.Snapshot) != before[member.RuntimeID] {
			t.Fatal("A rewrote original snapshot", err)
		}
	}
	if provider.DeniedDials.Load() != 0 {
		t.Fatal("fixture attempted non-loopback provider")
	}
}

// The pinned native executable runs only against an owned loopback TLS fixture.
// This exercises production App composition and a live idle publisher scope;
// fakePi remains a deterministic executor, not a real model or QA provider.
func TestTaskLarkSwitchAppActualNativeScope(t *testing.T) {
	taskLarkActualNativeScope(t, false)
}

func TestTaskLarkSwitchAppActualNativeUnknownC(t *testing.T) {
	taskLarkActualNativeScope(t, true)
}

func taskLarkActualNativeScope(t *testing.T, unknownC bool) {
	t.Helper()
	bundle := unpackLarkVariant(t)
	ctx := context.Background()
	if unknownC {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
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
	// The original native command has completed, while its real Pi/session and
	// native scope remain alive. Phase B must stop these exact old generations.
	if server.a.Executor.Process(publisher.ID).State == "stopped" {
		t.Fatal("expected live idle publisher Pi")
	}
	phase.Store(1)
	path := "/tasks/" + detail.Task.ID.String() + "/lark-account-switches"
	if unknownC {
		// Stop only this owned task driver before installing the commit barrier.
		// The actual idle Pi/native scope stays live for production handoff B.
		if e = server.a.Tasks.Stop(ctx); e != nil {
			t.Fatal(e)
		}
		var once atomic.Bool
		var held []*pgxpool.Conn
		var heldMu sync.Mutex
		unhold := func() {
			heldMu.Lock()
			defer heldMu.Unlock()
			for _, c := range held {
				c.Release()
			}
			held = nil
		}
		defer unhold()
		barrier := make(chan struct{})
		server.a.Store.TaskCommitted = func() {
			if once.Load() {
				return
			}
			var committed bool
			if e := server.a.Store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_lark_credential_bindings WHERE task_id=$1 AND action_id=$2)`, detail.Task.ID, input.ActionID).Scan(&committed); e != nil || !committed {
				return
			}
			if !once.CompareAndSwap(false, true) {
				return
			}
			defer close(barrier)
			bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			heldMu.Lock()
			defer heldMu.Unlock()
			for i := 0; i < int(server.a.Store.Pool.Config().MaxConns); i++ {
				c, e := server.a.Store.Pool.Acquire(bounded)
				if e != nil {
					t.Error("commit barrier did not saturate own TEST pool", e)
					return
				}
				held = append(held, c)
			}
		}
		// Invoke the real assembled task service directly for this injected DB
		// outage: HTTP audit uses the same starved pool and would itself block
		// response completion. HTTP write/recovery is covered by the other case.
		if _, e = server.a.Tasks.SwitchLarkAccount(ctx, auth.Console(), detail.Task.ID, input); !apperr.Is(e, apperr.CodeInternal) {
			t.Fatal("unconfirmed C did not fail closed", e)
		}
		select {
		case <-barrier:
		case <-time.After(6 * time.Second):
			t.Fatal("C commit barrier not reached")
		}
		ids := []uuid.UUID{input.ExpectedMembers[0].RuntimeID, input.ExpectedMembers[1].RuntimeID}
		if lease, e := server.a.Tasks.LarkSwitchNative.Acquire(ctx, ids); !errors.Is(e, tasks.ErrTaskLarkNativeBusy) || lease != nil {
			if lease != nil {
				lease.Release()
			}
			t.Fatal("unknown C released actual native creation/consumer gate", e)
		}
		if lease, e := server.a.Executor.AcquireRuntimeHandoff(ctx, ids); !errors.Is(e, local.ErrRuntimeBusy) || lease != nil {
			t.Fatal("unknown C released actual local epoch gate", e)
		}
		if server.a.Executor.Process(publisher.ID).State != "stopped" {
			t.Fatal("C published before actual old Pi stopped")
		}
		mu.Lock()
		later := requests[1]
		mu.Unlock()
		if later != 0 {
			t.Fatal("new native scope dispatched during unknown result")
		}
		unhold()
		var fences, groups, turns int
		if e = server.a.Store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_switch_fences WHERE task_id=$1),(SELECT count(*) FROM task_lark_credential_bindings WHERE task_id=$1),(SELECT count(*) FROM task_turns WHERE task_id=$1)`, detail.Task.ID).Scan(&fences, &groups, &turns); e != nil || fences != 0 || groups != 1 || turns != 2 {
			t.Fatal("unknown did not follow committed C/delete-fence invariant", e, fences, groups, turns)
		}
		original := decode[taskmodel.TaskLarkSwitchReceipt](t, server.must(200, "GET", path+"/"+input.ActionID.String(), nil))
		if original.Status != "succeeded" || original.Continuation == nil || original.DispatchStatus == nil || *original.DispatchStatus != "queued" {
			t.Fatal("original GET did not recover committed unique queued turn")
		}
		for i := 0; i < 2; i++ {
			if e = server.a.Tasks.ReconcileLarkSwitchOutcomes(ctx); e != nil {
				t.Fatal(e)
			}
		}
		if lease, e := server.a.Tasks.LarkSwitchNative.Acquire(ctx, ids); e != nil {
			t.Fatal("confirmed original action did not release native memory gate", e)
		} else {
			lease.Release()
		}
		if lease, e := server.a.Executor.AcquireRuntimeHandoff(ctx, ids); e != nil {
			t.Fatal("confirmed original action did not release local memory gate", e)
		} else if e = lease.Close(); e != nil {
			t.Fatal(e)
		}
		replayed := decode[taskmodel.TaskLarkSwitchReceipt](t, server.must(202, "POST", path, input))
		if replayed.Continuation.RunID != original.Continuation.RunID {
			t.Fatal("explicit original replay changed continuation")
		}
		if e = server.a.Store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_credential_bindings WHERE task_id=$1),(SELECT count(*) FROM task_turns WHERE task_id=$1)`, detail.Task.ID).Scan(&groups, &turns); e != nil || groups != 1 || turns != 2 {
			t.Fatal("confirmation/replay repeated publication", e)
		}
		for _, inst := range instances {
			latest, e := server.a.Store.TaskInstance(ctx, inst.ID)
			if e != nil || sha256.Sum256(latest.Snapshot) != before[inst.ID] || latest.Prepared != inst.Prepared {
				t.Fatal("unknown recovery rewrote original participant", e)
			}
		}
		if denied.Load() != 0 || provider.DeniedDials.Load() != 0 {
			t.Fatal("non-loopback fixture dial")
		}
		return
	}
	receipt := decode[taskmodel.TaskLarkSwitchReceipt](t, server.must(202, "POST", path, input))
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
}
