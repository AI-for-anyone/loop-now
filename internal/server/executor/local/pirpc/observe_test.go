package pirpc

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// obsRec 记录 Observer 回调。
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
	t.Fatalf("等待 %d 条用量超时", n)
}

func usageJSON(in, out, cr, cw int64) map[string]any {
	return map[string]any{"input": in, "output": out, "cacheRead": cr, "cacheWrite": cw, "totalTokens": in + out + cr + cw,
		"cost": map[string]any{"input": 0.1, "output": 0.2, "cacheRead": 0, "cacheWrite": 0, "total": 0.3}}
}

// messageOf 取出 message_end 事件中的消息，用于构造重复携带同一消息的 turn_end 与 agent_end。
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

// 按 Pi 1.1.0 源码与文档（docs/json.md、dist/types.d.ts）构造的一轮事件：工具循环、工具自身用量、压缩、预热、
// 自动重试；turn_end 与 agent_end 中重复出现的消息不得重复计数，message_update 中的阶段性用量不计。
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
		t.Fatalf("本轮结果不对：%+v %v", out.res, out.err)
	}
	// 两轮之间的预热：不属于任何一轮。
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
		t.Fatalf("用量条数应为 %d，实际 %d：%+v", len(wants), len(usage), usage)
	}
	for i, w := range wants {
		u := usage[i]
		if u.Kind != w.kind || u.Provider != w.provider || u.Model != w.model || u.ResponseModel != w.resp || u.StopReason != w.stop ||
			u.Input != w.in || u.Output != w.out || u.CacheRead != w.cr || u.CacheWrite != w.cw || inTurn[i] != w.turn {
			t.Errorf("第 %d 条用量不对：%+v（inTurn=%v），期望 %+v", i, u, inTurn[i], w)
		}
	}
	if usage[0].DurationMs == nil || *usage[0].DurationMs != 1200 {
		t.Errorf("助手消息应带 durationMs：%+v", usage[0].DurationMs)
	}
	if usage[5].Reasoning == nil || *usage[5].Reasoning != 7 {
		t.Errorf("推理 token 应保留：%+v", usage[5].Reasoning)
	}
	if usage[3].Reasoning != nil {
		t.Errorf("未报告推理 token 时应为空")
	}

	if len(notices) != 3 {
		t.Fatalf("事件条数应为 3，实际 %d：%+v", len(notices), notices)
	}
	if notices[0].Type != NoticeCompaction || notices[0].Data["result"] != "succeeded" || notices[0].Data["tokensBefore"] != int64(150000) {
		t.Errorf("压缩事件不对：%+v", notices[0])
	}
	if _, ok := notices[0].Data["durationMs"]; !ok {
		t.Errorf("压缩事件应带用时：%+v", notices[0])
	}
	if notices[1].Type != NoticeRetryStarted || notices[1].Data["attempt"] != 1 || notices[1].Data["errorMessage"] != "529 overloaded" {
		t.Errorf("重试开始事件不对：%+v", notices[1])
	}
	if notices[2].Type != NoticeRetryEnded || notices[2].Data["success"] != true {
		t.Errorf("重试结束事件不对：%+v", notices[2])
	}
	for i, in := range nTurn {
		if !in {
			t.Errorf("第 %d 个事件应在一轮之中", i)
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
		t.Fatalf("失败或中止的压缩不应产生用量：%+v", usage)
	}
	if len(notices) != 2 || notices[0].Data["result"] != "failed" || notices[0].Data["errorMessage"] != "boom" || notices[1].Data["result"] != "aborted" {
		t.Fatalf("压缩事件不对：%+v", notices)
	}
	if nTurn[0] || nTurn[1] {
		t.Fatal("没有进行中的一轮时 inTurn 应为 false")
	}
}

// 同一 fakepi 进程中，观察到的用量之和等于 get_session_stats 的增量。
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
		t.Fatalf("Prompt 失败：%+v %v", res, err)
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
		t.Fatalf("观察到的用量与 get_session_stats 的增量不一致：观察 %v/%v/%v/%v，统计 %v/%v/%v/%v（%d 条）",
			in, out, cr, cw, diff("input"), diff("output"), diff("cacheRead"), diff("cacheWrite"), len(usage))
	}
	// 重试失败的一条、工具调用一条、最终回复一条 = 3 条 assistant；另有工具、压缩、预热各一条。
	kinds := map[string]int{}
	for _, u := range usage {
		kinds[u.Kind]++
	}
	if kinds["assistant"] != 3 || kinds["tool"] != 1 || kinds["compaction"] != 1 || kinds["cache_warm"] != 1 {
		t.Fatalf("各类用量条数不对：%v", kinds)
	}
}
