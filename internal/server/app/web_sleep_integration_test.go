package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"io"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/testenv"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Main granted this single new server test file to Web. Other helpers are read-only.
func TestWebSleepIntegration(t *testing.T) {
	if os.Getenv("LOOP_NOW_WEB_SLEEP_INTEGRATION") != "1" {
		t.Skip("explicit isolated sleep browser integration only")
	}
	if os.Getenv("LOOP_NOW_TEST_DATABASE_URL") == "" || os.Getenv("LOOP_NOW_TEST_REDIS_URL") == "" {
		t.Fatal("explicit TEST endpoints required")
	}
	root := testenv.RepoRoot()
	phase := os.Getenv("LOOP_NOW_WEB_SLEEP_PHASE")
	if phase == "" {
		phase = "ordinary"
	}
	if phase != "ordinary" && phase != "race" {
		t.Fatal("invalid isolated test phase")
	}
	evidence := filepath.Join(root, "web/test-results/add-agent-sleep", phase)
	if err := os.MkdirAll(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	var w *world
	var addr, controlAddr string
	audit := map[string]any{"mode": "actual React -> route.fetch -> app.Build -> isolated TEST PG/Redis; fakepi; sleep"}
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
		if err = os.WriteFile(filepath.Join(evidence, "sleep-app-driver-results.json"), data, 0600); err != nil {
			t.Error("cannot persist sanitized integration audit")
		}
	})

	w = newWorld(t)
	w.cfg.SleepScanInterval = 50 * time.Millisecond
	t.Setenv("FAKEPI_PROMPT_DELAY", "1200ms")
	w.cfg.ExecEnvPassthrough = []string{"FAKEPI_PROMPT_DELAY"}
	helper := &taskAuthOffline{}
	helper.account.Store("unused-offline-sleep")
	s := w.startOfflineAuth(helper)
	addr = s.a.Addr()
	t.Cleanup(s.stop)
	ctx := context.Background()
	managerPath, manager := s.createAgent("SleepManager")
	workerPath, worker := s.createAgent("SleepWorker")
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	s.must(200, "PUT", "/projects/"+dc.Project.ID.String()+"/manager", map[string]any{"agentId": manager.ID})
	for _, path := range []string{managerPath, workerPath} {
		s.must(200, "PUT", path+"/model-credential", map[string]any{"provider": "anthropic", "key": "synthetic-offline-sleep"})
	}
	normalSleep := func() sleepmodel.Record {
		s.waitIdle(workerPath)
		s.must(202, "POST", workerPath+"/messages", map[string]any{"text": " !sleep=800ms !text=normal-sleep-source"})
		run := s.waitRunning(workerPath)
		reason := " !sleep=400ms !text=timer-platform-source"
		receipt, err := s.a.Store.RequestSleep(ctx, auth.Principal{Kind: auth.KindAgent, ID: worker.ID}, sleepmodel.Input{ActionID: uuid.New(), RunID: run.ID, Seconds: 180, Reason: &reason}, 1000)
		if err != nil {
			t.Fatal(err)
		}
		status := s.waitIdle(workerPath)
		if status.Sleep == nil || status.Sleep.ID != receipt.Sleep.ID || status.Busy {
			t.Fatal("normal sleep not active")
		}
		return *status.Sleep
	}
	first := normalSleep()
	createTask := func(title string, blocked bool) taskmodel.Detail {
		description := " !sleep=800ms !text=task-sleep-source"
		if blocked {
			description += " !error=fixture"
		}
		raw, err := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: manager.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: title, Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "当前工作", Description: description, AgentID: worker.ID, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}})
		if err != nil {
			t.Fatal(err)
		}
		detail := decode[taskmodel.Detail](t, raw)
		node := detail.Nodes[0]
		if blocked {
			taskWait(t, s, detail.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
			if _, err = s.a.API.Inbox.Send(ctx, auth.Console(), dc.Project.Name+"/SleepWorker#"+detail.Task.ID.String(), " !sleep=800ms !text=ordinary-task-sleep", "queue"); err != nil {
				t.Fatal(err)
			}
		}
		var run uuid.UUID
		limit := time.Now().Add(8 * time.Second)
		for time.Now().Before(limit) {
			err = s.a.Store.Pool.QueryRow(ctx, `SELECT id FROM task_turns WHERE runtime_id=$1 AND status='running' ORDER BY queue_seq DESC LIMIT 1`, node.RuntimeID).Scan(&run)
			if err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err != nil {
			t.Fatal("task run not active", err)
		}
		reason := "task scoped wait"
		if _, err = s.a.Store.RequestSleep(ctx, auth.Principal{Kind: auth.KindAgent, ID: worker.ID, TaskID: detail.Task.ID, RuntimeID: node.RuntimeID}, sleepmodel.Input{ActionID: uuid.New(), RunID: run, Seconds: 180, Reason: &reason}, 1000); err != nil {
			t.Fatal(err)
		}
		result := taskWait(t, s, detail.Task.ID, func(d taskmodel.Detail) bool { return len(d.SleepingInstances) == 1 })
		if blocked && (result.Task.Status != "blocked" || result.Nodes[0].Sleep != nil) {
			t.Fatal("ordinary conversation changed blocked node")
		}
		if !blocked && (result.Task.Status != "waiting" || result.Nodes[0].Sleep == nil) {
			t.Fatal("node sleep not waiting")
		}
		return result
	}
	nodeTask := createTask("Web sleep 当前节点", false)
	ordinaryTask := createTask("Web sleep 普通任务会话", true)
	control := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(rw, "POST only", 405)
			return
		}
		switch r.URL.Path {
		case "/normal-sleep":
			record := normalSleep()
			_ = json.NewEncoder(rw).Encode(record)
		case "/normal-deadline":
			if _, err := s.a.Store.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=now()-interval '1 second' WHERE agent_id=$1 AND scope_kind='normal' AND state='sleeping'`, worker.ID); err != nil {
				http.Error(rw, "test deadline failed", 500)
				return
			}
			if err := s.a.Store.ReconcileSleeps(ctx, 100); err != nil {
				http.Error(rw, "test reconcile failed", 500)
				return
			}
			rw.WriteHeader(204)
		case "/node-wake":
			if _, err := s.a.API.Inbox.Send(ctx, auth.Console(), dc.Project.Name+"/SleepWorker#"+nodeTask.Task.ID.String(), "actual task inbox early wake", "queue"); err != nil {
				http.Error(rw, "test task wake failed", 500)
				return
			}
			rw.WriteHeader(204)
		case "/normal-busy":
			s.waitIdle(workerPath)
			s.must(202, "POST", workerPath+"/messages", map[string]any{"text": " !sleep=2s !text=real-busy-fixture"})
			s.waitRunning(workerPath)
			rw.WriteHeader(204)
		default:
			http.NotFound(rw, r)
		}
	}))
	controlAddr = strings.TrimPrefix(control.URL, "http://")
	t.Cleanup(control.Close)
	manifest := map[string]any{"phase": phase, "apiBase": s.url, "controlBase": control.URL, "projectName": dc.Project.Name, "agentId": worker.ID, "nodeTaskId": nodeTask.Task.ID, "ordinaryTaskId": ordinaryTask.Task.ID, "firstSleepId": first.ID, "wakeAt": first.WakeAt}
	manifestPath := filepath.Join(t.TempDir(), "sleep-browser-manifest.json")
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "/Users/ljl/.local/node-v24.21.0/bin/node", filepath.Join(root, "web/scripts/sleep-app-browser.mjs"))
	cmd.Dir = root
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LOOP_NOW_WEB_SLEEP_MANIFEST=" + manifestPath}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("actual sleep browser failed: %v; see sanitized failure artifact", err)
	}
	for _, line := range strings.Split(output.String(), "\n") {
		if strings.HasPrefix(line, "PASS ") {
			t.Log(line)
		}
	}
	// Database read-back corroborates browser actions; never model/provider evidence.
	var timers int
	if err := s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE agent_id=$1 AND origin='sleep_wake'`, worker.ID).Scan(&timers); err != nil || timers != 1 {
		t.Fatal("timer run count", timers, err)
	}
	var state, reason string
	if err := s.a.Store.Pool.QueryRow(ctx, `SELECT state,end_reason FROM agent_sleeps WHERE id=$1`, first.ID).Scan(&state, &reason); err != nil || state != "woken" || reason != "queued_work" {
		t.Fatal("early wake mismatch", state, reason, err)
	}
	taskWait(t, s, ordinaryTask.Task.ID, func(d taskmodel.Detail) bool {
		return d.Task.Status == "blocked" && d.Nodes[0].Sleep == nil && len(d.SleepingInstances) == 1
	})
	for _, id := range []uuid.UUID{nodeTask.Task.ID, ordinaryTask.Task.ID} {
		if _, err := s.a.Tasks.Cancel(ctx, auth.Console(), id, taskmodel.CancelInput{ActionID: uuid.New()}); err != nil {
			t.Fatal(err)
		}
		taskWait(t, s, id, func(d taskmodel.Detail) bool { return d.Cleanup == "archived" })
	}
	s.waitIdle(workerPath)
	session, err := s.a.Store.GetAgentSession(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	native, err := os.ReadFile(session.SessionFile)
	if err != nil {
		t.Fatal(err)
	}
	prompts := 0
	for _, line := range bytes.Split(native, []byte("\n")) {
		var entry struct {
			Message struct {
				Role string `json:"role"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &entry) == nil && entry.Message.Role == "user" {
			prompts++
		}
	}
	var runs int
	if err = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE agent_id=$1`, worker.ID).Scan(&runs); err != nil || runs != 6 || prompts != 6 {
		t.Fatal("normal run/native prompt mismatch", runs, prompts, err)
	}
	interrupt, normal, err := s.a.Coord.QueueContents(ctx, worker.ID.String())
	if err != nil || len(interrupt) != 0 || len(normal) != 0 {
		t.Fatal("normal queue not drained", err)
	}
	audit["normalRunCount"] = runs
	audit["fakepiNormalNativePrompts"] = prompts
	audit["normalQueueDrained"] = true
	audit["timerRuns"] = timers
	audit["initialSleepEndedByQueuedWork"] = true
	audit["blockedOrdinaryNodeUnchanged"] = true
	audit["offlineRefreshes"] = helper.refreshes.Load()
	audit["passed"] = !t.Failed()
}

// Test-only transport fault: bytes and event IDs come exclusively from the App.
type webSleepFlushWriter struct{ rw http.ResponseWriter }

func (f webSleepFlushWriter) Write(p []byte) (int, error) {
	n, e := f.rw.Write(p)
	if e == nil {
		e = http.NewResponseController(f.rw).Flush()
	}
	return n, e
}

func TestWebSleepReconnectIntegration(t *testing.T) {
	if os.Getenv("LOOP_NOW_WEB_SLEEP_INTEGRATION") != "1" {
		t.Skip("explicit isolated browser reconnect slice only")
	}
	if os.Getenv("LOOP_NOW_TEST_DATABASE_URL") == "" || os.Getenv("LOOP_NOW_TEST_REDIS_URL") == "" {
		t.Fatal("explicit TEST endpoints required")
	}
	root := testenv.RepoRoot()
	phase := os.Getenv("LOOP_NOW_WEB_SLEEP_PHASE")
	if phase != "ordinary" && phase != "race" {
		t.Fatal("explicit test phase required")
	}
	evidence := filepath.Join(root, "web/test-results/add-agent-sleep/reconnect", phase)
	if e := os.MkdirAll(evidence, 0700); e != nil {
		t.Fatal(e)
	}
	var w *world
	var addr, controlAddr, proxyAddr string
	audit := map[string]any{"mode": "actual React -> route.fetch -> app.Build -> isolated TEST PG/Redis; fakepi; sleep"}
	// Registered first, runs after App shutdown and existing schema/prefix/temp cleanup.
	t.Cleanup(func() {
		if w == nil {
			return
		}
		audit["schema"] = w.schema
		audit["redisPrefix"] = w.cfg.RedisKeyPrefix
		audit["appAddress"] = addr
		audit["controlAddress"] = controlAddr
		audit["proxyAddress"] = proxyAddr
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
		for _, address := range []string{addr, controlAddr, proxyAddr} {
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
		if err = os.WriteFile(filepath.Join(evidence, "sleep-reconnect-driver-results.json"), data, 0600); err != nil {
			t.Error("cannot persist sanitized integration audit")
		}
	})

	w = newWorld(t)
	w.cfg.SleepScanInterval = 50 * time.Millisecond
	t.Setenv("FAKEPI_PROMPT_DELAY", "1200ms")
	w.cfg.ExecEnvPassthrough = []string{"FAKEPI_PROMPT_DELAY"}
	helper := &taskAuthOffline{}
	helper.account.Store("unused-reconnect-offline")
	s := w.startOfflineAuth(helper)
	addr = s.a.Addr()
	t.Cleanup(s.stop)
	ctx := context.Background()
	managerPath, manager := s.createAgent("ReconnectManager")
	workerPath, worker := s.createAgent("ReconnectWorker")
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	s.must(200, "PUT", "/projects/"+dc.Project.ID.String()+"/manager", map[string]any{"agentId": manager.ID})
	for _, path := range []string{managerPath, workerPath} {
		s.must(200, "PUT", path+"/model-credential", map[string]any{"provider": "anthropic", "key": "synthetic-reconnect-only"})
	}
	normalSleep := func(text string) sleepmodel.Record {
		s.must(202, "POST", workerPath+"/messages", map[string]any{"text": text})
		run := s.waitRunning(workerPath)
		reason := "reconnect preserved timer"
		receipt, e := s.a.Store.RequestSleep(ctx, auth.Principal{Kind: auth.KindAgent, ID: worker.ID}, sleepmodel.Input{ActionID: uuid.New(), RunID: run.ID, Seconds: 180, Reason: &reason}, 1000)
		if e != nil {
			t.Fatal(e)
		}
		st := s.waitIdle(workerPath)
		if st.Sleep == nil || st.Sleep.ID != receipt.Sleep.ID {
			t.Fatal("normal not sleeping")
		}
		return *st.Sleep
	}
	first := normalSleep(" !sleep=800ms !text=reconnect-source")
	raw, e := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: manager.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "Web reconnect scope", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "Reconnect node", Description: " !sleep=800ms !text=reconnect-node", AgentID: worker.ID, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}})
	if e != nil {
		t.Fatal(e)
	}
	detail := decode[taskmodel.Detail](t, raw)
	node := detail.Nodes[0]
	var nodeRun uuid.UUID
	limit := time.Now().Add(8 * time.Second)
	for time.Now().Before(limit) {
		e = s.a.Store.Pool.QueryRow(ctx, `SELECT id FROM task_turns WHERE runtime_id=$1 AND status='running' LIMIT 1`, node.RuntimeID).Scan(&nodeRun)
		if e == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.a.Store.RequestSleep(ctx, auth.Principal{Kind: auth.KindAgent, ID: worker.ID, TaskID: detail.Task.ID, RuntimeID: node.RuntimeID}, sleepmodel.Input{ActionID: uuid.New(), RunID: nodeRun, Seconds: 180}, 1000); e != nil {
		t.Fatal(e)
	}
	detail = taskWait(t, s, detail.Task.ID, func(d taskmodel.Detail) bool { return d.Nodes[0].Sleep != nil })
	// Gate selected SSE retries while the real App commits missed events.
	var mu sync.Mutex
	gates := map[string]chan struct{}{}
	cancels := map[string]context.CancelFunc{}
	requests := []map[string]string{}
	target, _ := url.Parse(strings.TrimSuffix(s.url, "/api/v1"))
	reverse := httputil.NewSingleHostReverseProxy(target)
	normalStream := "/api/v1" + workerPath + "/events"
	taskStream := "/api/v1/tasks/" + detail.Task.ID.String() + "/events"
	proxy := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path != normalStream && path != taskStream {
			reverse.ServeHTTP(rw, r)
			return
		}
		mu.Lock()
		requests = append(requests, map[string]string{"path": path, "lastEventId": r.Header.Get("Last-Event-ID")})
		gate := gates[path]
		mu.Unlock()
		if gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		streamCtx, cancel := context.WithCancel(r.Context())
		defer cancel()
		upstream := r.Clone(streamCtx)
		upstream.URL.Scheme = target.Scheme
		upstream.URL.Host = target.Host
		upstream.RequestURI = ""
		upstream.Host = target.Host
		mu.Lock()
		cancels[path] = cancel
		mu.Unlock()
		response, err := http.DefaultClient.Do(upstream)
		if err != nil {
			return
		}
		defer response.Body.Close()
		for name, values := range response.Header {
			for _, value := range values {
				rw.Header().Add(name, value)
			}
		}
		rw.WriteHeader(response.StatusCode)
		_ = http.NewResponseController(rw).Flush()
		_, _ = io.Copy(webSleepFlushWriter{rw}, response.Body)
	}))
	proxyAddr = strings.TrimPrefix(proxy.URL, "http://")
	t.Cleanup(proxy.Close)
	control := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cut":
			if r.Method != "POST" {
				http.Error(rw, "POST only", 405)
				return
			}
			paths := []string{normalStream}
			if r.URL.Query().Get("scope") == "both" {
				paths = append(paths, taskStream)
			}
			mu.Lock()
			for _, path := range paths {
				if gates[path] == nil {
					gates[path] = make(chan struct{})
				}
				if cancel := cancels[path]; cancel != nil {
					cancel()
				}
			}
			mu.Unlock()
			rw.WriteHeader(204)
		case "/release":
			if r.Method != "POST" {
				http.Error(rw, "POST only", 405)
				return
			}
			mu.Lock()
			for path, gate := range gates {
				close(gate)
				delete(gates, path)
			}
			mu.Unlock()
			rw.WriteHeader(204)
		case "/requests":
			mu.Lock()
			defer mu.Unlock()
			_ = json.NewEncoder(rw).Encode(requests)
		case "/replace-normal":
			if r.Method != "POST" {
				http.Error(rw, "POST only", 405)
				return
			}
			next := normalSleep(" !sleep=800ms !text=reconnect-replacement")
			_ = json.NewEncoder(rw).Encode(next)
		default:
			http.NotFound(rw, r)
		}
	}))
	controlAddr = strings.TrimPrefix(control.URL, "http://")
	t.Cleanup(control.Close)
	t.Cleanup(func() {
		mu.Lock()
		for path, gate := range gates {
			close(gate)
			delete(gates, path)
		}
		for _, cancel := range cancels {
			cancel()
		}
		mu.Unlock()
	})
	manifest := map[string]any{"phase": phase, "apiBase": proxy.URL + "/api/v1", "controlBase": control.URL, "projectName": dc.Project.Name, "agentId": worker.ID, "taskId": detail.Task.ID, "normalSleep": first, "nodeSleep": detail.Nodes[0].Sleep}
	manifestPath := filepath.Join(t.TempDir(), "reconnect-browser-manifest.json")
	data, _ := json.Marshal(manifest)
	if e = os.WriteFile(manifestPath, data, 0600); e != nil {
		t.Fatal(e)
	}
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "/Users/ljl/.local/node-v24.21.0/bin/node", filepath.Join(root, "web/scripts/sleep-reconnect-browser.mjs"))
	cmd.Dir = root
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LOOP_NOW_WEB_SLEEP_RECONNECT_MANIFEST=" + manifestPath}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if e = cmd.Run(); e != nil {
		t.Fatalf("reconnect browser failed: %v; see slice failure artifact", e)
	}
	for _, line := range strings.Split(output.String(), "\n") {
		if strings.HasPrefix(line, "PASS ") {
			t.Log(line)
		}
	}
	var runs, timers, turns int
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE origin='sleep_wake') FROM runs WHERE agent_id=$1`, worker.ID).Scan(&runs, &timers); e != nil || runs != 2 || timers != 0 {
		t.Fatal("reconnect dispatched extra work", runs, timers, e)
	}
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE task_id=$1`, detail.Task.ID).Scan(&turns); e != nil || turns != 1 {
		t.Fatal("task reconnect dispatched extra work", turns, e)
	}
	unchanged := taskWait(t, s, detail.Task.ID, func(d taskmodel.Detail) bool { return d.Nodes[0].Sleep != nil })
	if unchanged.Nodes[0].Sleep.ID != detail.Nodes[0].Sleep.ID || !unchanged.Nodes[0].Sleep.WakeAt.Equal(*detail.Nodes[0].Sleep.WakeAt) {
		t.Fatal("task timer changed")
	}
	st := s.waitIdle(workerPath)
	if st.Sleep == nil || st.Sleep.Revision <= first.Revision {
		t.Fatal("new sleep lost")
	}
	if _, e = s.a.Tasks.Cancel(ctx, auth.Console(), detail.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()}); e != nil {
		t.Fatal(e)
	}
	taskWait(t, s, detail.Task.ID, func(d taskmodel.Detail) bool { return d.Cleanup == "archived" })
	mu.Lock()
	audit["actualSseRequests"] = requests
	mu.Unlock()
	audit["normalRuns"] = runs
	audit["normalTimers"] = timers
	audit["taskTurns"] = turns
	audit["passed"] = !t.Failed()
}
