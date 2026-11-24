package httpapi_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/contracttest"
)

// muxEvent is one event of GET /events, unwrapped.
type muxEvent struct {
	sseID  string
	kind   string          // event or stream.error
	Stream string          `json:"stream"`
	ID     string          `json:"id"`
	Event  string          `json:"event"`
	Data   json.RawMessage `json:"data"`
	Error  struct {
		Code string `json:"code"`
	} `json:"error"`
}

// ownPaths are the endpoints whose x-sse-events the events of each kind of stream follow.
func ownPath(stream string) string {
	switch {
	case strings.HasPrefix(stream, "agent:"):
		return contracttest.AgentEventsPath
	case strings.HasPrefix(stream, "overview"):
		return contracttest.OverviewEventsPath
	case stream == "inbox":
		return contracttest.InboxEventsPath
	case strings.HasPrefix(stream, "task:"):
		return "/tasks/{taskId}/events"
	default:
		return "/projects/{projectId}/tasks/events"
	}
}

// openMux opens GET /events for the streams, with cursor as Last-Event-ID, and returns its events unwrapped; every
// event is checked against the contract, the wrapper and the data inside it.
func openMux(t *testing.T, e *env, cursor string, streams ...string) (func() muxEvent, func()) {
	q := url.Values{"stream": streams}
	ch, stop := readSSEPath(t, e, "/events?"+q.Encode(), cursor)
	return func() muxEvent {
		t.Helper()
		ev := next(t, ch)
		e.v.ValidateStreamSSE("/events", ev.event, []byte(ev.data))
		m := muxEvent{sseID: ev.id, kind: ev.event}
		if err := json.Unmarshal([]byte(ev.data), &m); err != nil {
			t.Fatal(err)
		}
		if m.kind == "event" {
			e.v.ValidateStreamSSE(ownPath(m.Stream), m.Event, m.Data)
		}
		return m
	}, stop
}

// until reads events until one of stream named event comes, and returns the events of stream read on the way.
func until(t *testing.T, read func() muxEvent, stream, event string) []muxEvent {
	t.Helper()
	var got []muxEvent
	for range 500 {
		ev := read()
		if ev.Stream == stream {
			got = append(got, ev)
			if ev.Event == event {
				return got
			}
		}
	}
	t.Fatalf("no %s on %s", event, stream)
	return nil
}

// Several streams over one connection: each starts with its snapshot, events keep their own ids and their order within
// the stream, and each event's SSE id is the combined cursor of every stream that has started.
func TestMultiplexedStreams(t *testing.T) {
	e := newEnv(t)
	agent := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "a", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	a, project := "agent:"+agent.ID.String(), "overview:project:"+e.dc.Project.ID.String()
	// A first turn, so the Agent's snapshot id is a position its own stream can resume from below (an empty stream's
	// snapshot id, 0-0, cannot be resumed later).
	e.expect(202, "POST", "/agents/"+agent.ID.String()+"/messages", map[string]any{"text": "First"})
	waitIdle(t, e, "/agents/"+agent.ID.String())
	read, stop := openMux(t, e, "", a, "inbox", project, a)
	defer stop()
	snapshots := map[string]string{}
	for len(snapshots) < 3 {
		ev := read()
		switch {
		case ev.kind != "event":
			t.Fatalf("unexpected %+v", ev)
		case strings.HasSuffix(ev.Event, "snapshot"):
			snapshots[ev.Stream] = ev.ID
		case snapshots[ev.Stream] == "":
			t.Fatalf("each stream starts with its snapshot: %+v", ev)
		}
	}
	if snapshots[a] == "" || snapshots["inbox"] == "" || snapshots[project] == "" {
		t.Fatalf("snapshots: %v", snapshots)
	}

	e.expect(202, "POST", "/agents/"+agent.ID.String()+"/messages", map[string]any{"text": "Hello"})
	got := until(t, read, a, "run.ended")
	single, stopSingle := readSSE(t, e, "/agents/"+agent.ID.String(), snapshots[a])
	defer stopSingle()
	for i, ev := range got {
		own := next(t, single)
		if own.id != ev.ID || own.event != ev.Event || own.data != string(ev.Data) {
			t.Fatalf("event %d differs from the Agent's own stream: %+v vs %+v", i, ev, own)
		}
		if !strings.Contains(ev.sseID, a+"="+ev.ID) || !strings.Contains(ev.sseID, "inbox="+snapshots["inbox"]) {
			t.Fatalf("the SSE id is the combined cursor: %s", ev.sseID)
		}
	}
}

// A reconnect with the combined cursor resumes every stream after its own id, without snapshots; a stream without a
// pair in the cursor starts with its snapshot.
func TestMultiplexedResume(t *testing.T) {
	e := newEnv(t)
	agent := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "a", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	a := "agent:" + agent.ID.String()
	read, stop := openMux(t, e, "", a, "inbox")
	defer stop()
	until(t, read, a, "snapshot")
	e.expect(202, "POST", "/agents/"+agent.ID.String()+"/messages", map[string]any{"text": "Hello"})
	got := until(t, read, a, "run.ended")
	stop()
	mid := got[1]

	read2, stop2 := openMux(t, e, mid.sseID, a, "inbox", "overview")
	defer stop2()
	var overviewSnapshot bool
	for range 50 {
		ev := read2()
		if ev.Stream == "overview" {
			overviewSnapshot = overviewSnapshot || ev.Event == "snapshot"
			continue
		}
		if ev.Stream == "inbox" {
			// An Inbox stream that has had no event yet cannot be resumed (its snapshot id is 0-0), so it starts with
			// a snapshot again, exactly as GET /inbox/events does; nothing else may come from it.
			if ev.Event != "snapshot" {
				t.Fatalf("the Inbox has nothing new: %+v", ev)
			}
			continue
		}
		if ev.Event == "snapshot" || ev.ID != got[2].ID {
			t.Fatalf("the Agent's stream resumes right after %s: %+v", mid.ID, ev)
		}
		break
	}
	if !overviewSnapshot {
		// The overview snapshot may come after the Agent's event; read on for it.
		until(t, read2, "overview", "snapshot")
	}
}

// Changing the set of streams: close, then open with the new set and the last id as cursor. Events that came while
// there was no connection arrive on the new one, once; the stream added starts with its snapshot.
func TestMultiplexedChangeSet(t *testing.T) {
	e := newEnv(t)
	agent := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "a", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	a := "agent:" + agent.ID.String()
	read, stop := openMux(t, e, "", a)
	defer stop()
	until(t, read, a, "snapshot")
	// A first turn, so the Agent's stream has a position to resume from (an empty stream's snapshot id cannot be
	// resumed later, on its own endpoint as here).
	e.expect(202, "POST", "/agents/"+agent.ID.String()+"/messages", map[string]any{"text": "First"})
	got := until(t, read, a, "run.ended")
	last := got[len(got)-1]
	stop()
	waitIdle(t, e, "/agents/"+agent.ID.String())

	// While there is no connection, a whole turn happens.
	e.expect(202, "POST", "/agents/"+agent.ID.String()+"/messages", map[string]any{"text": "Hello"})
	waitIdle(t, e, "/agents/"+agent.ID.String())
	own, stopOwn := readSSE(t, e, "/agents/"+agent.ID.String(), last.ID)
	defer stopOwn()
	var missed []sseEvent
	for {
		ev := next(t, own)
		if ev.event == "snapshot" {
			t.Fatalf("the Agent's own stream resumes: %+v", ev)
		}
		missed = append(missed, ev)
		if ev.event == "run.ended" {
			break
		}
	}
	stopOwn()

	q := url.Values{"stream": {a, "inbox"}, "cursor": {last.sseID}}
	ch, stop2 := readSSEPath(t, e, "/events?"+q.Encode(), "")
	defer stop2()
	var agentEvents []muxEvent
	inboxSnapshot := false
	for len(agentEvents) < len(missed) || !inboxSnapshot {
		ev := next(t, ch)
		var m muxEvent
		if err := json.Unmarshal([]byte(ev.data), &m); err != nil {
			t.Fatal(err)
		}
		switch m.Stream {
		case "inbox":
			inboxSnapshot = inboxSnapshot || m.Event == "snapshot"
		case a:
			agentEvents = append(agentEvents, m)
		}
	}
	for i, ev := range agentEvents {
		if ev.Event == "snapshot" || ev.ID != missed[i].id || ev.Event != missed[i].event {
			t.Fatalf("event %d: the events missed while closed, in order and once: %+v vs %+v", i, ev, missed[i])
		}
	}
}

// The task streams come over it too: a task's snapshot and its events, a project's task list changes.
func TestMultiplexedTaskStreams(t *testing.T) {
	e, _, d := taskEnv(t)
	task, project := "task:"+d.Task.ID.String(), "project-tasks:"+e.dc.Project.ID.String()
	read, stop := openMux(t, e, "", task, project)
	defer stop()
	seen := map[string]bool{}
	for !seen[task] || !seen[project] {
		ev := read()
		if ev.Event != "snapshot" {
			t.Fatalf("snapshots first: %+v", ev)
		}
		seen[ev.Stream] = true
	}
	e.expect(200, "POST", "/tasks/"+d.Task.ID.String()+"/cancel", map[string]any{"actionId": uuid.New()})
	until(t, read, project, "task.updated")
}

// Each stream keeps its own access rules: one that cannot be opened ends with stream.error and the others go on.
// Requests that name no stream, an unknown one or too many are refused.
func TestMultiplexedAccess(t *testing.T) {
	e := newEnv(t)
	missingAgent, missingProject, missingTask := "agent:"+uuid.NewString(), "overview:project:"+uuid.NewString(), "task:"+uuid.NewString()
	read, stop := openMux(t, e, "", missingAgent, missingProject, missingTask, "inbox")
	defer stop()
	errs := map[string]string{}
	inbox := false
	for len(errs) < 3 || !inbox {
		ev := read()
		switch ev.kind {
		case "stream.error":
			errs[ev.Stream] = ev.Error.Code
			if strings.Contains(ev.sseID, ev.Stream) {
				t.Fatalf("a failed stream has no position: %s", ev.sseID)
			}
		default:
			inbox = inbox || (ev.Stream == "inbox" && ev.Event == "snapshot")
		}
	}
	for _, s := range []string{missingAgent, missingProject, missingTask} {
		if errs[s] != "not_found" {
			t.Fatalf("%s: %v", s, errs)
		}
	}
	e.expectError(422, "validation_failed", "GET", "/events?stream=nonsense", nil)
	e.expectError(422, "validation_failed", "GET", "/events?stream=agent:not-a-uuid", nil)
	many := url.Values{}
	for range 65 {
		many.Add("stream", "agent:"+uuid.NewString())
	}
	e.expect(422, "GET", "/events?"+many.Encode(), nil, "X-Contract-Invalid-Request", "1")
	e.expect(422, "GET", "/events", nil, "X-Contract-Invalid-Request", "1")
}
