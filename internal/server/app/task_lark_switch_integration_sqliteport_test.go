//go:build sqliteport

// SQLITE-PORT(G11): 依赖 PostgreSQL 专有机制（行锁/咨询锁/plpgsql/自建连接池/schema），SQLite 下需改写。
// 从 task_lark_switch_integration_test.go 移出，默认不编译（go test -tags sqliteport 可单独查看）；改写后移回原文件并删掉本文件。
// 移出的函数：TestTaskLarkSwitchAppActualNativeScope、TestTaskLarkSwitchAppActualNativeUnknownC、taskLarkActualNativeScope

package app_test

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/app"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/config"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/executor/local"
	"loop-now/internal/server/larkauth/larktest"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks"
	"loop-now/internal/server/workflow"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

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
	instanceApp, e := app.Build(ctx, &cfg, config.NewLogger(io.Discard, cfg.LogFormat, cfg.LogLevel), app.Options{Listener: listener, HomeDirs: []string{"/nonexistent-home"}, LarkHTTPTransport: transport})
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
