package local

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
)

// Actual CLI/RPC and native Pi credential storage; the model stream and refresh
// provider are synthetic. The child also has an OS network deny policy, allowing
// only a parent-owned loopback expiration barrier. No native provider SDK runs.
func TestActualPiRPCUsesPGCoordinatorAcrossTwoProcessesAndLongRound(t *testing.T) {
	const node = "/Users/ljl/.local/node-v24.21.0/bin/node"
	const cli = "/opt/homebrew/lib/node_modules/@earendil-works/pi-coding-agent/dist/bundle/cli.js"
	for _, path := range []string{node, cli, "/usr/bin/sandbox-exec"} {
		if _, err := os.Stat(path); err != nil {
			t.Skip("requires installed Pi 1.1.0, pinned Node and OS network sandbox")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st := pgtest.NewStore(t)
	_, err := st.EnsureDefaults(ctx, "认证测试组织", "认证测试项目")
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.CreateAgent(ctx, store.PlatformScope(), "绑定账号", "openai-codex", "gpt-6.1-sol", "")
	if err != nil {
		t.Fatal(err)
	}
	var clock atomic.Int64
	clock.Store(time.Now().UnixMilli())
	credential := func(account string, generation int32, expires int64) modelcredential.Credential {
		data, _ := json.Marshal(map[string]any{"type": "oauth", "access": fmt.Sprintf("proof-access-%d", generation), "refresh": fmt.Sprintf("proof-refresh-%d", generation), "expires": expires, "accountId": account})
		value, err := modelcredential.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	saveGrant := func(value modelcredential.Credential) store.ModelAuthVersion {
		agent, err := st.GetAgent(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		op, err := st.NewModelAuthOperation(ctx, a.ID, "openai-codex", agent.ConfigVersion, time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		plain, _ := value.Encode()
		defer clear(plain)
		_, version, err := st.CompleteModelAuthOperation(ctx, op.ID, nil, plain)
		if err != nil || version == nil {
			t.Fatal(err)
		}
		return *version
	}
	oldCredential := credential("proof-account-A", 0, 1)
	old := saveGrant(oldCredential)
	current := saveGrant(credential("proof-account-B", 100, time.Now().Add(time.Hour).UnixMilli()))
	var rotations atomic.Int32
	coordinator := modelauth.NewCoordinator(st, localRefreshFunc(func(context.Context, modelcredential.Credential) (modelcredential.Credential, error) {
		n := rotations.Add(1)
		return credential("proof-account-A", n, clock.Load()+time.Hour.Milliseconds()), nil
	}), modelauth.CoordinatorOptions{Now: func() time.Time { return time.UnixMilli(clock.Load()) }, PollInterval: time.Millisecond}, nil)
	defer coordinator.Close(ctx)
	barrier := make(chan struct{})
	var requests atomic.Int32
	gate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/probe" {
			fmt.Fprint(w, "capability")
			return
		}
		if r.URL.Path != "/expire" {
			http.NotFound(w, r)
			return
		}
		n := requests.Add(1)
		if n > 2 {
			http.Error(w, "unexpected extra execution", 500)
			return
		}
		if n == 2 {
			clock.Add(2 * time.Hour.Milliseconds())
			close(barrier)
		}
		select {
		case <-barrier:
		case <-r.Context().Done():
			return
		case <-ctx.Done():
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer gate.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(gate.URL, "http://"))
	pluginRoot, err := filepath.Abs("../../../../auth-helper")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	entry := filepath.Join(root, "controlled-test-extension.mjs")
	data := fmt.Sprintf("import refresh from %q; import model from %q; export default pi=>{if(process.env.LOOP_NOW_AUTH_REFRESH_TIMEOUT_MS!=='41000')throw new Error('runtime timeout not propagated');refresh(pi);model(pi);};\n", "file://"+filepath.Join(pluginRoot, "runtime-refresh.mjs"), "file://"+filepath.Join(pluginRoot, "test/rpc-model.mjs"))
	if err := os.WriteFile(entry, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(root, "network-report")
	if err := os.WriteFile(report, nil, 0600); err != nil {
		t.Fatal(err)
	}
	profile := fmt.Sprintf("(version 1)\n(allow default)\n(deny network*)\n(allow network-outbound (remote ip \"localhost:%s\"))\n", port)
	var deniedHits atomic.Int32
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { deniedHits.Add(1); fmt.Fprint(w, "denied-fixture") }))
	defer denied.Close()
	_, deniedPort, _ := net.SplitHostPort(strings.TrimPrefix(denied.URL, "http://"))
	guard := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-p", profile, node, filepath.Join(pluginRoot, "test/sandbox-port-probe.mjs"), port, deniedPort)
	guard.Env = []string{"HOME=" + root, "TMPDIR=" + root, "PATH=/usr/bin:/bin", "NODE_ENV=production"}
	guardData, err := guard.CombinedOutput()
	if err != nil {
		t.Fatal("OS network isolation capability failed; Pi was not started", err)
	}
	var capability struct {
		Allowed struct{ Status int }
		Denied  struct{ ErrorCode string }
	}
	if json.Unmarshal(guardData, &capability) != nil || capability.Allowed.Status != 200 || capability.Denied.ErrorCode != "EPERM" || deniedHits.Load() != 0 {
		t.Fatal("OS network isolation was not established; Pi was not started")
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	piBin := filepath.Join(root, "pi-network-denied")
	script := "#!/bin/sh\nexec /usr/bin/sandbox-exec -p " + quote(profile) + " " + quote(node) + " --import " + quote(filepath.Join(pluginRoot, "test/deny-network.mjs")) + " " + quote(cli) + " --thinking off --no-mcp --no-prompt-templates --no-themes \"$@\"\n"
	if err := os.WriteFile(piBin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, func(o *Options) {
		o.PiBin = piBin
		o.AuthResolver = coordinator
		o.AuthExtension = entry
		o.AuthIPCTimeout = 41 * time.Second
		o.Passthrough = append(o.Passthrough, "AUTH_PROOF_GATE", "AUTH_PROOF_NETWORK_REPORT")
	}, "")
	h.env["AUTH_PROOF_GATE"] = gate.URL + "/expire"
	h.env["AUTH_PROOF_NETWORK_REPORT"] = report
	ref := modelcredential.Binding{VersionID: old.ID, AgentID: a.ID, Provider: "openai-codex"}
	ids := []uuid.UUID{}
	for range 2 {
		ids = append(ids, h.agent(func(s *agentconfig.Snapshot) {
			s.ConfigurationAgentID = a.ID
			s.Provider = "openai-codex"
			s.ModelID = "gpt-6.1-sol"
			s.ModelCredential = oldCredential
			s.ModelBinding = &ref
		}))
	}
	type outcome struct {
		result executor.Result
		err    error
	}
	results := make(chan outcome, 2)
	for _, id := range ids {
		go func(id uuid.UUID) {
			result, err := h.e.Execute(ctx, executor.Request{AgentID: id, RunID: uuid.New(), Message: "offline long round proof"}, newSink())
			results <- outcome{result, err}
		}(id)
	}
	for range 2 {
		select {
		case got := <-results:
			if got.err != nil || got.result.Outcome != executor.OutcomeCompleted || !strings.Contains(got.result.Text, "offline authenticated") {
				t.Fatalf("actual Pi round failed: outcome=%s code=%s err=%v", got.result.Outcome, got.result.ErrorCode, got.err)
			}
		case <-ctx.Done():
			t.Fatal("actual Pi round timed out")
		}
	}
	if rotations.Load() != 2 || requests.Load() != 2 {
		t.Fatal("duplicate rotation or missing mid-round tool refresh", rotations.Load(), requests.Load())
	}
	row, err := st.GetModelAuthVersion(ctx, old.ID)
	if err != nil || row.Generation != 3 || row.RefreshState != "ready" {
		t.Fatal("refresh was not committed", err)
	}
	pointer, _, err := st.CurrentModelAuthVersion(ctx, a.ID)
	if err != nil || pointer.ID != current.ID {
		t.Fatal("old runtime changed current account B", err)
	}
	for _, id := range ids {
		data, err := os.ReadFile(filepath.Join(h.root, "agents", id.String(), "pi", "auth.json"))
		if err != nil || !bytes.Contains(data, []byte("proof-refresh-2")) || bytes.Contains(data, []byte("proof-account-B")) {
			t.Fatal("Pi cache did not receive committed old-account token", err)
		}
	}
	networkData, err := os.ReadFile(report)
	if err != nil || len(networkData) != 0 {
		t.Fatal("provider network attempt detected", err)
	}
}

type localRefreshFunc func(context.Context, modelcredential.Credential) (modelcredential.Credential, error)

func (f localRefreshFunc) Refresh(ctx context.Context, c modelcredential.Credential) (modelcredential.Credential, error) {
	return f(ctx, c)
}
