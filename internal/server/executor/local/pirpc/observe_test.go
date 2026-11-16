package pirpc

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// obsRec records Observer callbacks.
type obsRec struct {
	mu      sync.Mutex
	usage   []Usage
	inTurn  []bool
	notices []Notice
	nTurn   []bool
}

func (o *obsRec) Usage(u Usage, inTurn bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.usage = append(o.usage, u)
	o.inTurn = append(o.inTurn, inTurn)
}

func (o *obsRec) Notice(n Notice, inTurn bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.notices = append(o.notices, n)
	o.nTurn = append(o.nTurn, inTurn)
}

func (o *obsRec) snapshot() ([]Usage, []bool, []Notice, []bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]Usage(nil), o.usage...), append([]bool(nil), o.inTurn...), append([]Notice(nil), o.notices...), append([]bool(nil), o.nTurn...)
}

func (o *obsRec) waitUsage(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		o.mu.Lock()
		got := len(o.usage)
		o.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d usage entries", n)
}

func usageJSON(in, out, cr, cw int64) map[string]any {
	return map[string]any{"input": in, "output": out, "cacheRead": cr, "cacheWrite": cw, "totalTokens": in + out + cr + cw,
		"cost": map[string]any{"input": 0.1, "output": 0.2, "cacheRead": 0, "cacheWrite": 0, "total": 0.3}}
}

// messageOf extracts the message from a message_end event, used to build turn_end and agent_end that carry the same
// message again.
func messageOf(line string) any {
	var ev map[string]any
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		panic(err)
	}
	return ev["message"]
}

func assistantEnd(stop string, u map[string]any, extra map[string]any) string {
	m := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "x"}}, "api": "anthropic-messages",
		"provider": "anthropic", "model": "claude-sonnet-4-5", "usage": u, "stopReason": stop, "timestamp": 1, "durationMs": 1200}
	for k, v := range extra {
		m[k] = v
	}
	return jsonLine(map[string]any{"type": "message_end", "message": m})
}

// A turn of events built from the Pi 1.1.0 source and docs (docs/json.md, dist/types.d.ts): a tool loop, the tool's own
// usage, compaction, warm-up, automatic retry; messages repeated in turn_end and agent_end must not be counted twice,
// and interim usage in message_update is not counted.
func TestObserveUsageAndNotices(t *testing.T) {
	f := newPipeFake(t)
	obs := &obsRec{}
	f.c.SetObserver(obs)
	ctx := testCtx(t)
	ch := goPrompt(ctx, f.c, "hi", nil)
	id := f.expect("prompt")
	reasoning := int64(7)
	toolUse := assistantEnd("toolUse", usageJSON(100, 20, 1000, 0), map[string]any{"responseModel": "claude-sonnet-4-5-20250929"})
	toolResult := jsonLine(map[string]any{"type": "message_end", "message": map[string]any{"role": "toolResult", "toolCallId": "c1",
		"toolName": "codemode", "content": []any{}, "isError": false, "timestamp": 2, "durationMs": 30, "usage": usageJSON(5, 1, 0, 0)}})
	plainResult := jsonLine(map[string]any{"type": "message_end", "message": map[string]any{"role": "toolResult", "toolCallId": "c2",
		"toolName": "bash", "content": []any{}, "isError": false, "timestamp": 3}})
	failed := assistantEnd("error", usageJSON(50, 0, 0, 0), map[string]any{"errorMessage": "529 overloaded"})
	final := assistantEnd("stop", map[string]any{"input": 10, "output": 40, "cacheRead": 2000, "cacheWrite": 300, "reasoning": reasoning, "totalTokens": 2350}, nil)
	f.send(
		okResp(id, "prompt", map[string]any{"disposition": "started"}),
		jsonLine(map[string]any{"type": "message_update", "usage": usageJSON(1, 1, 0, 0), "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": "x"}}),
		toolUse,
		toolResult,
		plainResult,
		jsonLine(map[string]any{"type": "turn_end", "message": messageOf(toolUse), "toolResults": []any{messageOf(toolResult)}}),
		`{"type":"compaction_start","reason":"threshold"}`,
		jsonLine(map[string]any{"type": "compaction_end", "reason": "threshold", "aborted": false, "willRetry": false,
			"result": map[string]any{"summary": "s", "firstKeptEntryId": "e1", "tokensBefore": 150000, "estimatedTokensAfter": 32000, "usage": usageJSON(30000, 800, 0, 0)}}),
		jsonLine(map[string]any{"type": "entry_appended", "entry": map[string]any{"type": "usage", "id": "u1", "kind": "cache_warm",
			"provider": "anthropic", "model": "claude-sonnet-4-5", "usage": usageJSON(0, 1, 50000, 0)}}),
		jsonLine(map[string]any{"type": "entry_appended", "entry": map[string]any{"type": "custom", "id": "x1"}}),
		failed,
		`{"type":"auto_retry_start","attempt":1,"maxAttempts":3,"delayMs":2000,"errorMessage":"529 overloaded"}`,
		final,
		`{"type":"auto_retry_end","success":true,"attempt":1}`,
		jsonLine(map[string]any{"type": "agent_end", "messages": []any{messageOf(final)}, "willRetry": false}),
		evSettled(false),
	)
	if out := recvPrompt(t, ch); out.err != nil || out.res.Outcome != OutcomeCompleted {
		t.Fatalf("wrong result for this turn: %+v %v", out.res, out.err)
	}
	// Warm-up between two turns: belongs to no turn.
	f.send(jsonLine(map[string]any{"type": "entry_appended", "entry": map[string]any{"type": "usage", "kind": "cache_warm",
		"provider": "anthropic", "model": "claude-sonnet-4-5", "usage": usageJSON(0, 1, 60000, 0)}}))
	obs.waitUsage(t, 7)

	usage, inTurn, notices, nTurn := obs.snapshot()
	type want struct {
		kind, provider, model, resp, stop string
		in, out, cr, cw                   int64
		turn                              bool
	}
	wants := []want{
		{"assistant", "anthropic", "claude-sonnet-4-5", "claude-sonnet-4-5-20250929", "toolUse", 100, 20, 1000, 0, true},
		{"tool", "", "", "", "", 5, 1, 0, 0, true},
		{"compaction", "", "", "", "", 30000, 800, 0, 0, true},
		{"cache_warm", "anthropic", "claude-sonnet-4-5", "", "", 0, 1, 50000, 0, true},
		{"assistant", "anthropic", "claude-sonnet-4-5", "", "error", 50, 0, 0, 0, true},
		{"assistant", "anthropic", "claude-sonnet-4-5", "", "stop", 10, 40, 2000, 300, true},
		{"cache_warm", "anthropic", "claude-sonnet-4-5", "", "", 0, 1, 60000, 0, false},
	}
	if len(usage) != len(wants) {
		t.Fatalf("want %d usage entries, got %d: %+v", len(wants), len(usage), usage)
	}
	for i, w := range wants {
		u := usage[i]
		if u.Kind != w.kind || u.Provider != w.provider || u.Model != w.model || u.ResponseModel != w.resp || u.StopReason != w.stop ||
			u.Input != w.in || u.Output != w.out || u.CacheRead != w.cr || u.CacheWrite != w.cw || inTurn[i] != w.turn {
			t.Errorf("wrong usage entry %d: %+v (inTurn=%v), want %+v", i, u, inTurn[i], w)
		}
	}
	if usage[0].DurationMs == nil || *usage[0].DurationMs != 1200 {
		t.Errorf("assistant messages should carry durationMs: %+v", usage[0].DurationMs)
	}
	if usage[5].Reasoning == nil || *usage[5].Reasoning != 7 {
		t.Errorf("reasoning tokens should be kept: %+v", usage[5].Reasoning)
	}
	if usage[3].Reasoning != nil {
		t.Errorf("should be empty when no reasoning tokens are reported")
	}

	if len(notices) != 3 {
		t.Fatalf("want 3 events, got %d: %+v", len(notices), notices)
	}
	if notices[0].Type != NoticeCompaction || notices[0].Data["result"] != "succeeded" || notices[0].Data["tokensBefore"] != int64(150000) {
		t.Errorf("wrong compaction event: %+v", notices[0])
	}
	if _, ok := notices[0].Data["durationMs"]; !ok {
		t.Errorf("the compaction event should carry the duration: %+v", notices[0])
	}
	if notices[1].Type != NoticeRetryStarted || notices[1].Data["attempt"] != 1 || notices[1].Data["errorMessage"] != "529 overloaded" {
		t.Errorf("wrong retry start event: %+v", notices[1])
	}
	if notices[2].Type != NoticeRetryEnded || notices[2].Data["success"] != true {
		t.Errorf("wrong retry end event: %+v", notices[2])
	}
	for i, in := range nTurn {
		if !in {
			t.Errorf("event %d should be within a turn", i)
		}
	}
}

func TestObserveCompactionFailedAndAborted(t *testing.T) {
	f := newPipeFake(t)
	obs := &obsRec{}
	f.c.SetObserver(obs)
	f.send(
		`{"type":"compaction_end","reason":"overflow","aborted":false,"willRetry":false,"errorMessage":"boom"}`,
		`{"type":"compaction_end","reason":"manual","aborted":true,"willRetry":false}`,
	)
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, _, n, _ := obs.snapshot()
		if len(n) == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	usage, _, notices, nTurn := obs.snapshot()
	if len(usage) != 0 {
		t.Fatalf("a failed or aborted compaction should produce no usage: %+v", usage)
	}
	if len(notices) != 2 || notices[0].Data["result"] != "failed" || notices[0].Data["errorMessage"] != "boom" || notices[1].Data["result"] != "aborted" {
		t.Fatalf("wrong compaction event: %+v", notices)
	}
	if nTurn[0] || nTurn[1] {
		t.Fatal("inTurn should be false when no turn is in progress")
	}
}

// Within the same fakepi process, the sum of observed usage equals the increase in get_session_stats.
func TestObserveMatchesSessionStats(t *testing.T) {
	p := startFake(t, nil)
	obs := &obsRec{}
	p.SetObserver(obs)
	ctx := testCtx(t)
	stats := func() map[string]any {
		data, err := p.Call(ctx, "get_session_stats", nil)
		if err != nil {
			t.Fatal(err)
		}
		var m struct {
			Tokens map[string]any `json:"tokens"`
		}
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		return m.Tokens
	}
	before := stats()
	res, err := p.Prompt(ctx, "!usage=100,20,1000,5 !tool=bash !toolusage=3,1 !compact=500,50 !warm=0,1,2000 !retry hello", nil)
	if err != nil || res.Outcome != OutcomeCompleted {
		t.Fatalf("Prompt failed: %+v %v", res, err)
	}
	after := stats()
	usage, _, _, _ := obs.snapshot()
	var in, out, cr, cw float64
	for _, u := range usage {
		in += float64(u.Input)
		out += float64(u.Output)
		cr += float64(u.CacheRead)
		cw += float64(u.CacheWrite)
	}
	diff := func(k string) float64 { return after[k].(float64) - before[k].(float64) }
	if in != diff("input") || out != diff("output") || cr != diff("cacheRead") || cw != diff("cacheWrite") {
		t.Fatalf("observed usage does not match the get_session_stats increase: observed %v/%v/%v/%v, stats %v/%v/%v/%v (%d entries)",
			in, out, cr, cw, diff("input"), diff("output"), diff("cacheRead"), diff("cacheWrite"), len(usage))
	}
	// One for the failed attempt that was retried, one for the tool call, one for the final reply = 3 assistant
	// entries; plus one each for tool, compaction and warm-up.
	kinds := map[string]int{}
	for _, u := range usage {
		kinds[u.Kind]++
	}
	if kinds["assistant"] != 3 || kinds["tool"] != 1 || kinds["compaction"] != 1 || kinds["cache_warm"] != 1 {
		t.Fatalf("wrong usage entry counts per kind: %v", kinds)
	}
}
