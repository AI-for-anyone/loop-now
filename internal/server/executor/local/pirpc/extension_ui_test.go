package pirpc

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// An extension's dialog requests are answered as cancelled right away (the same default as when the request's timeout
// expires), whether or not there is a timeout; notify and setStatus are passed to the observer, and the other
// fire-and-forget methods are ignored.
func TestExtensionUIDialogsAnsweredWithDefault(t *testing.T) {
	f := newPipeFake(t)
	o := &obsRec{}
	f.c.SetObserver(o)
	f.send(
		`{"type":"extension_ui_request","id":"d1","method":"confirm","title":"Clear the session?","message":"It will be lost"}`,
		`{"type":"extension_ui_request","id":"d2","method":"select","title":"Pick one","options":["a","b"],"timeout":10000}`,
		`{"type":"extension_ui_request","id":"n1","method":"notify","message":"Blocked","notifyType":"warning"}`,
		`{"type":"extension_ui_request","id":"s1","method":"setStatus","statusKey":"ext","statusText":"Turn 3"}`,
		`{"type":"extension_ui_request","id":"w1","method":"setWidget","widgetKey":"ext","widgetLines":["x"]}`,
	)
	got := map[string]bool{}
	for range 2 {
		m := f.next()
		if m["type"] != "extension_ui_response" || m["cancelled"] != true {
			t.Fatalf("responses: %v", m)
		}
		got[m["id"].(string)] = true
	}
	if !got["d1"] || !got["d2"] {
		t.Fatalf("both dialogs should get a response: %v", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	var notices []Notice
	for time.Now().Before(deadline) {
		if _, _, notices, _ = o.snapshot(); len(notices) == 4 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(notices) != 4 {
		t.Fatalf("notices: %+v", notices)
	}
	want := []map[string]any{
		{"method": "confirm", "message": "Clear the session?", "answered": "cancelled"},
		{"method": "select", "message": "Pick one", "answered": "cancelled"},
		{"method": "notify", "message": "Blocked", "level": "warning"},
		{"method": "setStatus", "statusKey": "ext"},
	}
	for i, n := range notices {
		if n.Type != NoticeExtensionUI {
			t.Fatalf("type: %+v", n)
		}
		for k, v := range want[i] {
			if n.Data[k] != v {
				t.Fatalf("notice %d %s: %v, want %v", i, k, n.Data[k], v)
			}
		}
	}
	if s, _ := notices[3].Data["message"].(*string); s == nil || *s != "Turn 3" {
		t.Fatalf("status text: %v", notices[3].Data["message"])
	}
}

// When Dialogs is set, confirm, select and input during a turn are handed to it, and Pi gets its answer (confirmed for
// confirm, value for select and input); the run event is recorded only after the answer (with answered and
// questionId); editor and dialogs outside a turn are still cancelled right away and not handed to it.
func TestExtensionUIDialogsGoToHandler(t *testing.T) {
	f := newPipeFake(t)
	o := &obsRec{}
	f.c.SetObserver(o)
	question := uuid.New()
	var mu sync.Mutex
	var asked []DialogRequest
	f.c.SetDialogs(func(_ context.Context, req DialogRequest) DialogResponse {
		mu.Lock()
		asked = append(asked, req)
		mu.Unlock()
		switch req.Method {
		case "confirm":
			return DialogResponse{Confirmed: false, Outcome: "answered", QuestionID: &question}
		case "select":
			return DialogResponse{Value: "b", Outcome: "answered", QuestionID: &question}
		}
		return DialogResponse{Cancelled: true, Outcome: "expired", QuestionID: &question}
	})
	f.send(`{"type":"extension_ui_request","id":"before","method":"confirm","title":"Outside a turn"}`)
	if m := f.next(); m["id"] != "before" || m["cancelled"] != true {
		t.Fatalf("a dialog outside a turn should be cancelled right away: %v", m)
	}
	ctx := testCtx(t)
	ch := goPrompt(ctx, f.c, "hi", nil)
	id := f.expect("prompt")
	f.send(okResp(id, "prompt", map[string]any{"disposition": "started"}),
		`{"type":"extension_ui_request","id":"c","method":"confirm","title":"Continue?","timeout":5000}`,
		`{"type":"extension_ui_request","id":"s","method":"select","title":"Pick one","options":["a","b"]}`,
		`{"type":"extension_ui_request","id":"i","method":"input","title":"Name","placeholder":"Alice"}`,
		`{"type":"extension_ui_request","id":"e","method":"editor","title":"Edit"}`)
	got := map[string]map[string]any{}
	for range 4 {
		m := f.next()
		got[m["id"].(string)] = m
	}
	if got["c"]["confirmed"] != false || got["c"]["cancelled"] != nil || got["s"]["value"] != "b" || got["i"]["cancelled"] != true || got["e"]["cancelled"] != true {
		t.Fatalf("responses: %v", got)
	}
	mu.Lock()
	if len(asked) != 3 {
		t.Fatalf("dialogs handed to the handler: %+v", asked)
	}
	for _, a := range asked {
		if a.Method == "confirm" && a.Timeout != 5*time.Second || a.Method == "select" && (len(a.Options) != 2 || a.Title != "Pick one" || a.Timeout != 0) ||
			a.Method == "input" && a.Placeholder != "Alice" {
			t.Fatalf("dialog contents: %+v", a)
		}
	}
	mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	var notices []Notice
	for time.Now().Before(deadline) {
		if _, _, notices, _ = o.snapshot(); len(notices) == 5 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	answered := map[string]any{}
	for _, n := range notices {
		answered[n.Data["method"].(string)] = n.Data["answered"]
		if n.Data["method"] == "select" && n.Data["questionId"] != question {
			t.Fatalf("the event should carry the question: %v", n.Data)
		}
	}
	if len(notices) != 5 || answered["select"] != "answered" || answered["input"] != "expired" || answered["editor"] != "cancelled" {
		t.Fatalf("events: %+v", notices)
	}
	f.send(jsonLine(map[string]any{"type": "agent_end", "messages": []any{}, "willRetry": false}), evSettled(false))
	recvPrompt(t, ch)
}
