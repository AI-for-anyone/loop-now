package app_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/larkauth/larktest"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"loop-now/internal/server/config"
)

// The archive is an explicit frozen offline test input. It contains no grant,
// credentials or runtime configuration. Missing input never claims native pass.
func unpackLarkVariant(t *testing.T) string {
	t.Helper()
	source := os.Getenv("LARK_TEST_VARIANT_ARCHIVE")
	if source == "" {
		t.Skip("requires explicit frozen Lark variant archive")
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("only darwin-arm64 variant verified")
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != "84e0846a40dcb38a8f091c3402ccd657faf3ff60a54649b243c5854dddeb60a9" {
		t.Fatal("unreviewed native archive")
	}
	f, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	r := tar.NewReader(z)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"lark-cli": false, "lark-cli-launcher": false, "lark-cli-sandbox-probe": false, "bin/lark-cli": false, "manifest.json": false, "LICENSE": false}
	for {
		h, e := r.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		seen, ok := allowed[h.Name]
		if !ok || seen || h.Typeflag != tar.TypeReg || h.Size < 1 || h.Size > 512<<20 {
			t.Fatal("invalid frozen archive entry")
		}
		allowed[h.Name] = true
		p := filepath.Join(dir, h.Name)
		if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		out, e := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if e != nil {
			t.Fatal(e)
		}
		_, e = io.Copy(out, r)
		ce := out.Close()
		if e != nil || ce != nil {
			t.Fatal("archive extraction failed")
		}
	}
	for _, seen := range allowed {
		if !seen {
			t.Fatal("incomplete native archive")
		}
	}
	return dir
}

func TestLarkNativeBundleActualAppAdmission(t *testing.T) {
	bundle := unpackLarkVariant(t)
	w := githubAppWorld(t)
	s := w.start(func(c *config.Config) { c.LarkUserBundleDir = bundle })
	defer s.stop()
	s.must(200, "GET", "/health", nil)
	if s.a.Tasks.LarkSwitchNative == nil {
		t.Fatal("verified native manager not wired to task handoff")
	}
	entries, e := os.ReadDir(filepath.Join(w.cfg.WorkRoot, "tools", "lark-cli-user", "lark-cli-user-1.0.94-loop-authsidecar.1"))
	if e != nil || len(entries) != 1 {
		t.Fatal("verified native distribution was not installed")
	}
	for _, name := range []string{"lark-cli", "lark-cli-sandbox-probe", "bin/lark-cli", "manifest.json", "LICENSE"} {
		st, e := os.Stat(filepath.Join(w.cfg.WorkRoot, "tools", "lark-cli-user", "lark-cli-user-1.0.94-loop-authsidecar.1", entries[0].Name(), name))
		if e != nil || !st.Mode().IsRegular() {
			t.Fatal("installed bundle incomplete")
		}
	}
	var n int
	if e = s.a.Store.Pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM runs)+(SELECT count(*) FROM lark_cli_grants)`).Scan(&n); e != nil || n != 0 {
		t.Fatal("readiness launched a run or created a grant")
	}
}

// This exercises real App -> local Pi launch -> PATH shim -> server manager ->
// official native CLI -> PG token source. Only fake Pi replaces the model; all
// official HTTPS targets are routed to owned TLS and all other remote dials fail.
func TestLarkNativeAppCapturedAThenNextRoundB(t *testing.T) {
	bundle := unpackLarkVariant(t)
	firstPage, release := make(chan struct{}), make(chan struct{})
	var signal, unblock sync.Once
	defer unblock.Do(func() { close(release) })
	var pages, phase, denied atomic.Int32
	var mu sync.Mutex
	tokens := map[int32]string{}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
		out.Header().Set("Content-Type", "application/json")
		token := r.Header.Get("Authorization")
		if !strings.HasPrefix(token, "Bearer synthetic-access-") {
			t.Error("unexpected non-fixture authorization")
			out.WriteHeader(401)
			return
		}
		current := phase.Load()
		mu.Lock()
		old := tokens[current]
		if old == "" {
			tokens[current] = token
		} else if old != token {
			t.Error("captured round changed credential")
		}
		mu.Unlock()
		if r.URL.Path == "/open-apis/authen/v1/user_info" {
			id := "ou-native-A"
			if current == 1 {
				id = "ou-native-B"
			}
			json.NewEncoder(out).Encode(map[string]any{"code": 0, "data": map[string]string{"open_id": id, "name": "Synthetic"}})
			return
		}
		if r.URL.Path != "/open-apis/im/v1/messages" {
			t.Error("unexpected native path")
			out.WriteHeader(400)
			return
		}
		pages.Add(1)
		if r.URL.Query().Get("page_token") == "" {
			if current == 0 {
				signal.Do(func() { close(firstPage) })
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			io.WriteString(out, `{"code":0,"data":{"items":[{"page":1}],"has_more":true,"page_token":"next"}}`)
			return
		}
		io.WriteString(out, `{"code":0,"data":{"items":[{"page":2}],"has_more":false}}`)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	transport := upstream.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.ServerName = "127.0.0.1"
	transport.TLSClientConfig.MinVersion = tls.VersionTLS12
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == "open.feishu.cn:443" {
			return (&net.Dialer{}).DialContext(ctx, "tcp", target.Host)
		}
		host, _, e := net.SplitHostPort(address)
		ip := net.ParseIP(host)
		if e != nil || ip == nil || !ip.IsLoopback() {
			denied.Add(1)
			return nil, errors.New("non-loopback denied")
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	original := http.DefaultTransport
	http.DefaultTransport = transport
	defer func() { transport.CloseIdleConnections(); http.DefaultTransport = original }()
	w := githubAppWorld(t)
	// Execute the real controlled command before fake Pi starts its normal RPC.
	// Its output stays in the isolated workspace and never corrupts RPC stdout.
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	wrapper := filepath.Join(t.TempDir(), "native-before-fakepi")
	script := "#!/bin/sh\nset -eu\nif [ \"${1-}\" = --version ]; then exec " + quote(w.cfg.PiBin) + " \"$@\"; fi\nif [ -n \"${LOOP_NOW_LARK_EXEC_URL-}\" ]; then\nlark-cli api GET /open-apis/im/v1/messages --params '{\"container_id_type\":\"chat\",\"container_id\":\"oc_synthetic\"}' --page-all --page-delay 50 --page-limit 2 --as user > native-output.json 2> native-error.txt\nfi\nexec " + quote(w.cfg.PiBin) + " \"$@\"\n"
	if e := os.WriteFile(wrapper, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	w.cfg.PiBin = wrapper
	w.cfg.PiIdleTimeout = 100 * time.Millisecond
	s := w.start(func(c *config.Config) { c.LarkUserBundleDir = bundle })
	defer func() { s.stop() }()
	provider := larktest.New(t)
	s.a.CLIAuth.Lark = provider.Client
	project, base, accounts, source := larkAppSource(t, s)
	users := make([]cm.Account, 2)
	for i, id := range []string{"ou-native-A", "ou-native-B"} {
		a := provider.Add(id, id, nil, "")
		a.Release()
		op := uuid.New()
		s.must(202, "POST", base, map[string]any{"operationId": op, "cli": "lark-cli", "authType": "lark_user", "sourceAccountId": source.ID, "expectedSourceRevision": source.Revision})
		users[i] = *waitLarkApp(t, s, base, op, "succeeded").Account
	}
	s.must(201, "POST", accounts, map[string]any{"purpose": "model", "provider": "anthropic", "name": "native fixture model", "credential": map[string]string{"type": "api_key", "key": "synthetic-native-model"}})
	path, agent := s.createAgent("native scoped")
	s.must(200, "PUT", path+"/clis/lark-cli", nil)
	selectUser := func(account uuid.UUID) {
		current := decode[apitypes.Agent](t, s.must(200, "GET", path, nil))
		s.must(200, "PUT", path+"/credential-selections", map[string]any{"purpose": "cli", "cli": "lark-cli", "accountId": account, "expectedConfigVersion": current.ConfigVersion})
	}
	selectUser(users[0].ID)
	s.must(202, "POST", path+"/messages", map[string]any{"text": "native first round"})
	select {
	case <-firstPage:
	case <-time.After(15 * time.Second):
		t.Fatal("actual native first page not reached")
	}
	// The same manager used by execution must reject handoff during this command.
	if gate, e := s.a.Tasks.LarkSwitchNative.Acquire(context.Background(), []uuid.UUID{agent.ID}); e == nil {
		gate.Release()
		t.Fatal("active native command admitted handoff")
	}
	selectUser(users[1].ID)
	unblock.Do(func() { close(release) })
	s.waitIdle(path)
	phase.Store(1)
	s.must(202, "POST", path+"/messages", map[string]any{"text": "native second round"})
	s.waitIdle(path)
	mu.Lock()
	a, b := tokens[0], tokens[1]
	mu.Unlock()
	if pages.Load() != 4 || a == "" || b == "" || a == b || denied.Load() != 0 {
		t.Fatal("native capture/next-round/loopback matrix failed")
	}
	var completed int
	if e := s.a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM runs WHERE agent_id=$1 AND status='completed'`, agent.ID).Scan(&completed); e != nil || completed != 2 {
		t.Fatal("fake Pi rounds not completed")
	}
	// A real task Create captures A, then both account-current and Agent selection
	// change. Restart and explicit retry must still run native with the original A.
	phase.Store(0)
	selectUser(users[0].ID)
	_, manager := s.createAgent("native task manager")
	s.must(200, "PUT", "/projects/"+project.String()+"/manager", map[string]any{"agentId": manager.ID})
	raw, e := s.a.Tasks.Create(context.Background(), auth.Principal{Kind: auth.KindAgent, ID: manager.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "native fixed task", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "fixture", AgentID: agent.ID, Description: " !error !text=native_task", Inputs: map[string]workflow.Source{}, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}}}, Edges: []workflow.Edge{}}})
	if e != nil {
		t.Fatal(e)
	}
	task := decode[taskmodel.Detail](t, raw)
	blocked := taskWait(t, s, task.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
	instance, e := s.a.Store.TaskInstance(context.Background(), blocked.Nodes[0].RuntimeID)
	if e != nil {
		t.Fatal(e)
	}
	digest := sha256.Sum256(instance.Snapshot)
	originalVersion := *users[0].CurrentVersionID
	replaced := decode[cm.Account](t, s.must(200, "PUT", accounts+"/"+users[0].ID.String()+"/credential", map[string]any{"expectedRevision": users[0].Revision, "credential": map[string]any{"type": "cli", "fields": map[string]string{"appId": "new-static-app", "appSecret": "synthetic-new-secret", "brand": "feishu"}}}))
	if replaced.CurrentVersionID == nil || *replaced.CurrentVersionID == originalVersion {
		t.Fatal("account current did not change")
	}
	selectUser(users[1].ID)
	s.stop()
	s = w.start(func(c *config.Config) { c.LarkUserBundleDir = bundle })
	if _, e = s.a.Tasks.Retry(context.Background(), auth.Principal{Kind: auth.KindConsole}, task.Task.ID, taskmodel.RetryInput{ActionID: uuid.New(), ExpectedNodeRunID: *blocked.Task.CurrentNodeRunID, Note: "isolated original grant retry"}); e != nil {
		t.Fatal(e)
	}
	taskWait(t, s, task.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" && len(d.Nodes) == 2 })
	after, e := s.a.Store.TaskInstance(context.Background(), instance.ID)
	if e != nil || sha256.Sum256(after.Snapshot) != digest {
		t.Fatal("task original snapshot changed")
	}
	snap, e := s.a.Tasks.Source.ExecutionSnapshot(context.Background(), instance.ID)
	if e != nil || snap.LarkBinding == nil || snap.LarkBinding.VersionID != originalVersion || snap.LarkBinding.AccountID != users[0].ID {
		t.Fatal("task restart/retry followed current")
	}
	if pages.Load() != 8 || denied.Load() != 0 {
		t.Fatal("task native restart/retry did not reuse original grant")
	}
	// Ordinary blocked conversation and a sleep/restart/early-wake must also
	// keep the original grant, without advancing or retrying the workflow.
	waitStopped := func() {
		t.Helper()
		until := time.Now().Add(5 * time.Second)
		for time.Now().Before(until) {
			if s.a.Executor.Process(instance.ID).State == "stopped" {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("owned task Pi did not become idle/stopped")
	}
	waitStopped()
	latest, e := s.a.Store.GetTask(context.Background(), task.Task.ID)
	if e != nil {
		t.Fatal(e)
	}
	nodeBefore := *latest.View.Task.CurrentNodeRunID
	if _, e = s.a.API.Inbox.Send(context.Background(), auth.Console(), "默认项目/native scoped#"+task.Task.ID.String(), " !sleep=800ms !text=lark_blocked_conversation", "queue"); e != nil {
		t.Fatal(e)
	}
	turn := sleepWaitTurn(t, s, instance.ID, uuid.Nil)
	principal := auth.Principal{Kind: auth.KindAgent, ID: agent.ID, TaskID: task.Task.ID, RuntimeID: instance.ID}
	receipt, e := s.a.Store.RequestSleep(context.Background(), principal, sleepmodel.Input{ActionID: uuid.New(), RunID: turn.ID, Seconds: 120}, 1000)
	if e != nil {
		t.Fatal(e)
	}
	sleeping := taskWait(t, s, task.Task.ID, func(d taskmodel.Detail) bool { return len(d.SleepingInstances) == 1 })
	if sleeping.Task.Status != "blocked" || *sleeping.Task.CurrentNodeRunID != nodeBefore || sleeping.SleepingInstances[0].ID != receipt.Sleep.ID {
		t.Fatal("conversation sleep changed workflow")
	}
	wakeAt := *sleeping.SleepingInstances[0].WakeAt
	s.stop()
	s = w.start(func(c *config.Config) { c.LarkUserBundleDir = bundle })
	restarted, e := s.a.Store.GetTask(context.Background(), task.Task.ID)
	if e != nil || len(restarted.View.SleepingInstances) != 1 || !restarted.View.SleepingInstances[0].WakeAt.Equal(wakeAt) {
		t.Fatal("sleep restart changed deadline")
	}
	if _, e = s.a.API.Inbox.Send(context.Background(), auth.Console(), "默认项目/native scoped#"+task.Task.ID.String(), "lark early wake", "queue"); e != nil {
		t.Fatal(e)
	}
	taskWait(t, s, task.Task.ID, func(d taskmodel.Detail) bool {
		var active int
		e := s.a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM task_turns WHERE runtime_id=$1 AND status IN ('queued','running')`, instance.ID).Scan(&active)
		return e == nil && active == 0 && len(d.SleepingInstances) == 0 && pages.Load() == 12
	})
	final, e := s.a.Store.GetTask(context.Background(), task.Task.ID)
	if e != nil || final.View.Task.Status != "blocked" || *final.View.Task.CurrentNodeRunID != nodeBefore || len(final.View.Nodes) != 2 {
		t.Fatal("blocked conversation advanced workflow")
	}
	snap, e = s.a.Tasks.Source.ExecutionSnapshot(context.Background(), instance.ID)
	if e != nil || snap.LarkBinding == nil || snap.LarkBinding.VersionID != originalVersion || denied.Load() != 0 {
		t.Fatal("conversation/sleep changed captured grant")
	}
}
