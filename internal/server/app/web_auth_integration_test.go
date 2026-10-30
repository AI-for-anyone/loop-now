package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"loop-now/internal/server/app"
	"loop-now/internal/server/config"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/testenv"
)

// Exclusive Web-owned integration driver. All control routes belong to this
// httptest fixture, never to the App's production router.
type webAuthResolution struct {
	Outcome     string `json:"outcome"`
	Account     string `json:"account"`
	OperationID string `json:"operationId"`
}
type webAuthOfflineHelper struct {
	mu        sync.Mutex
	active    map[uuid.UUID]chan webAuthResolution
	logins    atomic.Int64
	refreshes atomic.Int64
}

func (h *webAuthOfflineHelper) Login(ctx context.Context, id uuid.UUID, notify func(string) error) (modelcredential.Credential, error) {
	ch := make(chan webAuthResolution, 1)
	h.mu.Lock()
	h.active[id] = ch
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.active, id); h.mu.Unlock() }()
	h.logins.Add(1)
	if err := notify("https://auth.openai.com/oauth/authorize?state=web-offline-state&code_challenge=web-offline-challenge"); err != nil {
		return modelcredential.Credential{}, err
	}
	select {
	case <-ctx.Done():
		return modelcredential.Credential{}, modelauth.ErrAuthCancelled
	case result := <-ch:
		if result.Outcome == "fail" {
			return modelcredential.Credential{}, modelauth.ErrCallbackPortBusy
		}
		raw, _ := json.Marshal(map[string]any{"type": "oauth", "access": "web-offline-access-sentinel", "refresh": "web-offline-refresh-sentinel", "accountId": result.Account, "expires": time.Now().Add(time.Hour).UnixMilli()})
		defer clear(raw)
		return modelcredential.Decode(raw)
	}
}
func webAuthSyntheticCredential(account string) (modelcredential.Credential, error) {
	raw, _ := json.Marshal(map[string]any{"type": "oauth", "access": "web-offline-access-sentinel", "refresh": "web-offline-refresh-sentinel", "accountId": account, "expires": time.Now().Add(time.Hour).UnixMilli()})
	defer clear(raw)
	return modelcredential.Decode(raw)
}

func (h *webAuthOfflineHelper) Refresh(context.Context, modelcredential.Credential) (modelcredential.Credential, error) {
	h.refreshes.Add(1)
	return modelcredential.Credential{}, errors.New("unexpected offline refresh")
}

// Run explicitly; normal Go tests do not require a browser/Vite process.
func TestWebAuthIntegration(t *testing.T) {
	if os.Getenv("LOOP_NOW_WEB_AUTH_INTEGRATION") != "1" {
		t.Skip("explicit real-browser isolated-App integration only")
	}
	root := testenv.RepoRoot()
	evidence := filepath.Join(root, "web/scripts/integration/codex-auth")
	var w *world
	var addr, controlAddr string
	audit := map[string]any{"mode": "actual React -> route.fetch -> app.Build -> isolated PG; DI offline auth helper; no provider/model calls"}
	// Registered first, runs after App shutdown and existing schema/prefix/temp cleanup.
	t.Cleanup(func() {
		if w == nil {
			return
		}
		audit["schema"] = w.schema
		audit["redisPrefix"] = w.cfg.RedisKeyPrefix
		audit["appAddress"] = addr
		audit["controlAddress"] = controlAddr
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, pgtest.URL(t))
		if err != nil {
			t.Error("cleanup PG verification failed")
			return
		}
		defer conn.Close(ctx)
		var exists bool
		if err = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, w.schema).Scan(&exists); err != nil || exists {
			t.Error("isolated schema cleanup not verified")
		}
		audit["schemaRemoved"] = !exists && err == nil
		options, err := redis.ParseURL(w.cfg.RedisURL)
		if err != nil {
			t.Error("cleanup Redis configuration invalid")
			return
		}
		rc := redis.NewClient(options)
		defer rc.Close()
		iterator := rc.Scan(ctx, 0, w.cfg.RedisKeyPrefix+"*", 100).Iterator()
		found := iterator.Next(ctx)
		err = iterator.Err()
		audit["redisPrefixEmpty"] = err == nil && !found
		if err != nil || found {
			t.Error("isolated Redis cleanup not verified")
		}
		_, err = os.Stat(w.cfg.WorkRoot)
		audit["workRootRemoved"] = os.IsNotExist(err)
		if !os.IsNotExist(err) {
			t.Error("isolated work root cleanup not verified")
		}
		closed := true
		for _, address := range []string{addr, controlAddr} {
			if address == "" {
				continue
			}
			c, e := net.DialTimeout("tcp", address, 200*time.Millisecond)
			if e == nil {
				closed = false
				c.Close()
				t.Error("temporary listener still open")
			}
		}
		audit["temporaryListenersClosed"] = closed
		data, _ := json.MarshalIndent(audit, "", "  ")
		if err = os.WriteFile(filepath.Join(evidence, "app-driver-results.json"), data, 0600); err != nil {
			t.Error("cannot persist sanitized integration audit")
		}
	})
	w = newWorld(t)
	w.cfg.AuthLoginTimeout = time.Minute
	w.cfg.AuthRecordTimeout = 5 * time.Second
	helper := &webAuthOfflineHelper{active: map[uuid.UUID]chan webAuthResolution{}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("cannot create temporary App listener")
	}
	a, err := app.Build(context.Background(), &w.cfg, config.NewLogger(io.Discard, "text", w.cfg.LogLevel), app.Options{SearchPath: w.schema, Listener: ln, HomeDirs: []string{"/nonexistent-home"}, AuthLoginHelper: helper, AuthRefresher: helper})
	if err != nil {
		ln.Close()
		t.Fatal("isolated App Build failed")
	}
	addr = a.Addr()
	go func() { _ = a.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if a.Shutdown(ctx) != nil {
			t.Error("temporary App shutdown failed")
		}
	})
	s := &server{t: t, a: a, url: "http://" + addr + "/api/v1"}
	var contextView map[string]any
	if json.Unmarshal(s.must(200, "GET", "/context", nil), &contextView) != nil {
		t.Fatal("invalid temporary App context")
	}
	project := contextView["project"].(map[string]any)
	create := func(name, provider string) uuid.UUID {
		model := ""
		for _, p := range a.Config.Models.Snapshot().Providers {
			if p.ID == provider && len(p.Models) > 0 {
				model = p.Models[0].ID
				break
			}
		}
		if model == "" {
			t.Fatal("required offline provider model not available")
		}
		var agent map[string]any
		data := s.must(201, "POST", "/projects/"+project["id"].(string)+"/agents", map[string]any{"name": name, "model": map[string]string{"provider": provider, "modelId": model}, "prompt": ""})
		if json.Unmarshal(data, &agent) != nil {
			t.Fatal("invalid created Agent")
		}
		return uuid.MustParse(agent["id"].(string))
	}
	codexID, keyID := create("Web联调Codex", "openai-codex"), create("Web联调Key", "anthropic")
	var checkpoints []map[string]any
	var checkpointMu sync.Mutex
	control := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/resolve":
			var input webAuthResolution
			if json.NewDecoder(http.MaxBytesReader(rw, r.Body, 1024)).Decode(&input) != nil || (input.Outcome != "success" && input.Outcome != "fail") || (input.Account != "web-account-A" && input.Account != "web-account-B" && input.Account != "") {
				http.Error(rw, "invalid fixture control", 400)
				return
			}
			id, e := uuid.Parse(input.OperationID)
			if e != nil {
				http.Error(rw, "invalid operation", 400)
				return
			}
			helper.mu.Lock()
			ch := helper.active[id]
			helper.mu.Unlock()
			if ch == nil {
				http.Error(rw, "operation inactive", 409)
				return
			}
			select {
			case ch <- input:
				_ = json.NewEncoder(rw).Encode(map[string]bool{"ok": true})
			default:
				http.Error(rw, "already resolved", 409)
			}
		case r.Method == "GET" && r.URL.Path == "/checkpoint":
			id, e := uuid.Parse(r.URL.Query().Get("agentId"))
			if e != nil || (id != codexID && id != keyID) {
				http.Error(rw, "invalid Agent", 400)
				return
			}
			v, managed, e := a.Store.CurrentModelAuthVersion(r.Context(), id)
			if e != nil || !managed {
				http.Error(rw, "account absent", 409)
				return
			}
			if v == nil {
				result := map[string]any{"agentId": id.String(), "cleared": true, "managed": true}
				checkpointMu.Lock()
				checkpoints = append(checkpoints, result)
				checkpointMu.Unlock()
				_ = json.NewEncoder(rw).Encode(result)
				return
			}

			// PG readback exposes only allowed metadata and whether a credential is stored.
			result := map[string]any{"agentId": id.String(), "versionId": v.ID.String(), "accountLabel": v.AccountLabel, "authMethod": v.AuthMethod, "stored": len(v.Secret) > 0}
			expectedAccount := r.URL.Query().Get("expectedAccount")
			if expectedAccount != "" {
				if expectedAccount != "web-account-A" && expectedAccount != "web-account-B" {
					http.Error(rw, "invalid expected account", 400)
					return
				}
				stored, e := modelcredential.Decode(v.Secret)
				expected, _ := webAuthSyntheticCredential(expectedAccount)
				result["accountMatches"] = e == nil && stored.SameAccount(expected)
			}
			checkpointMu.Lock()
			checkpoints = append(checkpoints, result)
			checkpointMu.Unlock()
			_ = json.NewEncoder(rw).Encode(result)
		default:
			http.NotFound(rw, r)
		}
	}))
	controlAddr = strings.TrimPrefix(control.URL, "http://")
	t.Cleanup(control.Close)
	manifest := map[string]any{"apiBase": s.url, "controlBase": control.URL, "codexAgentId": codexID.String(), "keyAgentId": keyID.String(), "projectName": project["name"]}
	path := filepath.Join(t.TempDir(), "web-auth-manifest.json")
	data, _ := json.Marshal(manifest)
	if os.WriteFile(path, data, 0600) != nil {
		t.Fatal("cannot write private manifest")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/Users/ljl/.local/node-v24.21.0/bin/node", filepath.Join(evidence, "app-browser-verify.mjs"))
	cmd.Dir = root
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LOOP_NOW_WEB_AUTH_MANIFEST=" + path}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err = cmd.Run(); err != nil {
		audit["helperLogins"] = helper.logins.Load()
		if op, e := a.Store.LatestModelAuthOperation(context.Background(), codexID); e == nil && op != nil {
			audit["lastOperationStatus"] = op.Status
		}
		t.Fatalf("browser integration failed (%v); sanitized browser failure artifact records phase", err)
	}
	for _, line := range strings.Split(output.String(), "\n") {
		if strings.HasPrefix(line, "PASS ") {
			t.Log(line)
		}
	}
	checkpointMu.Lock()
	audit["pgCheckpoints"] = checkpoints
	checkpointMu.Unlock()
	audit["helperLogins"] = helper.logins.Load()
	audit["helperRefreshes"] = helper.refreshes.Load()
	if helper.logins.Load() != 4 || helper.refreshes.Load() != 0 {
		t.Fatal("unexpected helper activity")
	}
	current, _, err := a.Store.CurrentModelAuthVersion(context.Background(), codexID)
	if err != nil || current == nil {
		t.Fatal("final PG OAuth account mismatch")
	}
	saved, err := modelcredential.Decode(current.Secret)
	expected, _ := webAuthSyntheticCredential("web-account-B")
	if err != nil || !saved.SameAccount(expected) {
		t.Fatal("final stored account binding mismatch")
	}

	var versions int
	if err = a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM model_auth_versions WHERE agent_id=$1`, codexID).Scan(&versions); err != nil || versions != 2 {
		t.Fatal("failed/cancelled login created or removed account versions")
	}
	audit["oauthVersionCount"] = versions
	key, managed, err := a.Store.CurrentModelAuthVersion(context.Background(), keyID)
	if err != nil || !managed || key != nil {
		t.Fatal("API Key clear marker did not persist through actual App")
	}
	var keyVersions int
	if err = a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM model_auth_versions WHERE agent_id=$1`, keyID).Scan(&keyVersions); err != nil || keyVersions != 1 {
		t.Fatal("API Key clear removed historical version")
	}
	audit["keyHistoricalVersionCount"] = keyVersions
	var runs int
	if err = a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatal("unexpected model run")
	}
	audit["runCount"] = runs
	audit["passed"] = true
	t.Log(fmt.Sprintf("actual Web/App/PG verified: helper logins=%d, OAuth versions=%d, model runs=%d", helper.logins.Load(), versions, runs))
}
