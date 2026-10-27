package httpapi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"net/http"
	"strings"
	"testing"
)

func TestSleepHTTPContractBindingReplayAndValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.platformAgent("sleep owner")
	b := e.platformAgent("other")
	_, run, err := e.st.InsertMessageAndRun(ctx, store.NewRun{AgentID: a.ID, RunID: uuid.New(), Text: "source", Start: true, InstanceID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.st.SetRunRunning(ctx, run.ID, 1); err != nil {
		t.Fatal(err)
	}
	owner, other := e.agentCaller(a.ID), e.agentCaller(b.ID)
	anonymous := owner
	anonymous.token = ""
	anonymous.expectError(401, "unauthenticated", "GET", "/sleep", nil)
	input := map[string]any{"actionId": uuid.New(), "runId": run.ID, "seconds": 120, "reason": nil}
	other.expectError(409, "sleep_conflict", "POST", "/sleep", input)
	got := decode[sleepmodel.Receipt](t, owner.expect(202, "POST", "/sleep", input))
	if !got.EndTurn || got.Sleep.State != "requested" || got.Sleep.WakeAt != nil {
		t.Fatal(got)
	}
	state := decode[sleepmodel.State](t, owner.expect(200, "GET", "/sleep", nil))
	if state.CurrentRunID == nil || *state.CurrentRunID != run.ID || state.Sleep.ID != got.Sleep.ID {
		t.Fatal(state)
	}
	for _, field := range []string{"actionId", "runId", "seconds"} {
		bad := map[string]any{"actionId": uuid.New(), "runId": run.ID, "seconds": 120}
		bad[field] = nil
		owner.expect(422, "POST", "/sleep", bad, "X-Contract-Invalid-Request", "1")
	}
	for _, seconds := range []any{0, -1, 1.5, 2147483648, "1"} {
		owner.expect(422, "POST", "/sleep", map[string]any{"actionId": uuid.New(), "runId": run.ID, "seconds": seconds}, "X-Contract-Invalid-Request", "1")
	}
	owner.expect(422, "POST", "/sleep", map[string]any{"actionId": uuid.New(), "runId": run.ID, "seconds": 120, "scope": "other"}, "X-Contract-Invalid-Request", "1")
	owner.expectError(409, "sleep_conflict", "POST", "/sleep", map[string]any{"actionId": uuid.New(), "runId": run.ID, "seconds": 120})
	if err = e.st.RecordSessionFile(ctx, a.ID, "offline-session.jsonl"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = e.st.FinishRun(ctx, run.ID, store.RunEnd{Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	again := decode[sleepmodel.Receipt](t, owner.expect(202, "POST", "/sleep", input))
	if again.Sleep.ID != got.Sleep.ID || again.Sleep.State != "requested" || again.Sleep.WakeAt != nil {
		t.Fatal("replay not original receipt", again)
	}
	input["seconds"] = 121
	owner.expectError(409, "sleep_conflict", "POST", "/sleep", input)
	st := decode[apitypes.AgentStatus](t, e.expect(200, "GET", "/agents/"+a.ID.String()+"/status", nil))
	if st.Sleep == nil || st.Busy || st.SleepRevision <= got.Sleep.Revision {
		t.Fatal("normal projection", st)
	}
	ch, stop := readSSE(t, e, "/agents/"+a.ID.String(), "")
	defer stop()
	snapshot := next(t, ch)
	e.v.ValidateSSE(snapshot.event, []byte(snapshot.data))
	e.api.Conv.EmitAgentStatus(a.ID)
	for {
		event := next(t, ch)
		if event.event != "sleep.updated" {
			continue
		}
		e.v.ValidateSSE(event.event, []byte(event.data))
		update := decode[sleepmodel.Updated](t, []byte(event.data))
		if update.Sleep == nil || update.Sleep.ID != got.Sleep.ID || update.TaskVersion != nil || update.SleepRevision != st.SleepRevision {
			t.Fatal("sleep SSE", update)
		}
		break
	}
	timeline := decode[apitypes.RunTimeline](t, e.expect(200, "GET", "/agents/"+a.ID.String()+"/runs/"+run.ID.String()+"/timeline", nil))
	transitions := 0
	for _, entry := range timeline.Entries {
		m, ok := entry.(map[string]any)
		if ok && m["type"] == "sleep" {
			transitions++
		}
	}
	if transitions != 2 {
		t.Fatal("sleep lifecycle missing from timeline", transitions)
	}
	var count int
	if err = e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM agent_sleeps WHERE agent_id=$1`, a.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("invalid requests wrote sleeps", count, err)
	}
}

func TestSleepTaskSnapshotUsesFormalEnvelope(t *testing.T) {
	e, _, detail := taskEnv(t)
	req, err := http.NewRequestWithContext(context.Background(), "GET", e.srv.URL+"/api/v1/tasks/"+detail.Task.ID.String()+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := e.v.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scan := bufio.NewScanner(response.Body)
	event := ""
	var data []byte
	for scan.Scan() {
		line := scan.Text()
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			data = []byte(strings.TrimPrefix(line, "data: "))
		}
		if line == "" && event != "" {
			break
		}
	}
	if event != "snapshot" {
		t.Fatal("snapshot absent", event, scan.Err())
	}
	e.v.ValidateStreamSSE("/tasks/{taskId}/events", event, data)
	var snapshot struct {
		Task taskmodel.Detail `json:"task"`
	}
	if err = json.Unmarshal(data, &snapshot); err != nil || snapshot.Task.Task.ID != detail.Task.ID || snapshot.Task.SleepingInstances == nil {
		t.Fatal("formal nested detail absent", err, string(data))
	}
}
