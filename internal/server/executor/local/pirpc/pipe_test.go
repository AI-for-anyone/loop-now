package pirpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// pipeFake 是进程内的 Pi 替身：读取客户端写出的命令，按脚本写出响应与事件。
type pipeFake struct {
	t    *testing.T
	c    *Client
	cmds chan map[string]any
	out  *io.PipeWriter
}

func newPipeFake(t *testing.T) *pipeFake {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := newClient(inW, outR, nil)
	f := &pipeFake{t: t, c: c, cmds: make(chan map[string]any, 16), out: outW}
	go func() {
		defer close(f.cmds)
		br := bufio.NewReader(inR)
		for {
			line, err := br.ReadBytes('\n')
			if len(line) > 0 {
				var m map[string]any
				_ = json.Unmarshal(line, &m)
				f.cmds <- m
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = outW.Close()
		_ = inR.Close()
		<-c.Done()
	})
	return f
}

func (f *pipeFake) next() map[string]any {
	f.t.Helper()
	select {
	case m, ok := <-f.cmds:
		if !ok {
			f.t.Fatal("命令流已结束")
		}
		return m
	case <-time.After(5 * time.Second):
		f.t.Fatal("等待命令超时")
		return nil
	}
}

func (f *pipeFake) expect(typ string) string {
	f.t.Helper()
	m := f.next()
	if m["type"] != typ {
		f.t.Fatalf("期望命令 %s，实际 %v", typ, m)
	}
	id, _ := m["id"].(string)
	if id == "" {
		f.t.Fatalf("命令缺少 id：%v", m)
	}
	return id
}

func (f *pipeFake) send(lines ...string) {
	f.t.Helper()
	for _, l := range lines {
		if _, err := io.WriteString(f.out, l+"\n"); err != nil {
			f.t.Fatal(err)
		}
	}
}

func jsonLine(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func okResp(id, cmd string, data any) string {
	m := map[string]any{"type": "response", "id": id, "command": cmd, "success": true}
	if data != nil {
		m["data"] = data
	}
	return jsonLine(m)
}

func evStart() string {
	return `{"type":"message_start","message":{"role":"assistant","content":[],"stopReason":"pending"}}`
}

func evDelta(s string) string {
	return jsonLine(map[string]any{"type": "message_update", "usage": map[string]any{},
		"assistantMessageEvent": map[string]any{"type": "text_delta", "contentIndex": 0, "delta": s}})
}

func evEnd(text, stop, errMsg string) string {
	m := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}, "stopReason": stop}
	if errMsg != "" {
		m["errorMessage"] = errMsg
	}
	return jsonLine(map[string]any{"type": "message_end", "message": m})
}

func evSettled(aborted bool) string {
	return fmt.Sprintf(`{"type":"agent_settled","aborted":%v}`, aborted)
}

func TestPipeOutOfOrderResponses(t *testing.T) {
	f := newPipeFake(t)
	ctx := testCtx(t)
	type out struct {
		data json.RawMessage
		err  error
	}
	ch1, ch2 := make(chan out, 1), make(chan out, 1)
	go func() { d, err := f.c.Call(ctx, "get_state", nil); ch1 <- out{d, err} }()
	id1 := f.expect("get_state")
	go func() { d, err := f.c.Call(ctx, "get_state", nil); ch2 <- out{d, err} }()
	id2 := f.expect("get_state")
	if id1 == id2 {
		t.Fatal("命令 id 应唯一")
	}
	f.send(
		`{"type":"response","command":"parse","success":false,"error":"no id"}`,
		`{"type":"queue_update","steering":[],"followUp":[]}`,
		okResp(id2, "get_state", map[string]any{"n": 2}),
		okResp(id1, "get_state", map[string]any{"n": 1}),
	)
	for i, ch := range []chan out{ch1, ch2} {
		o := <-ch
		if o.err != nil {
			t.Fatal(o.err)
		}
		var d struct{ N int }
		if err := json.Unmarshal(o.data, &d); err != nil || d.N != i+1 {
			t.Fatalf("第 %d 个调用拿到了错误的响应：%s", i+1, o.data)
		}
	}
}

func TestPipeAbortedAppendsPartialDeltas(t *testing.T) {
	f := newPipeFake(t)
	rec := newRecorder()
	ch := goPrompt(testCtx(t), f.c, "x", rec)
	id := f.expect("prompt")
	f.send(
		okResp(id, "prompt", map[string]any{"disposition": "started"}),
		`{"type":"agent_start"}`,
		`{"type":"message_start","message":{"role":"user","content":"x"}}`,
		`{"type":"message_end","message":{"role":"user","content":"x"}}`,
		evStart(), evDelta("first"), evEnd("first", "toolUse", ""),
		`{"type":"tool_execution_start","toolCallId":"c1","toolName":"bash","args":{}}`,
		`{"type":"tool_execution_end","toolCallId":"c1","toolName":"bash","result":{},"isError":true}`,
		`{"type":"some_future_event","x":1}`,
		evStart(), evDelta("par"), evDelta("tial"),
		evSettled(true),
	)
	o := recvPrompt(t, ch)
	if o.err != nil {
		t.Fatal(o.err)
	}
	if o.res != (TurnResult{Outcome: OutcomeAborted, Text: "first\n\npartial"}) {
		t.Fatalf("结果不符：%+v", o.res)
	}
	_, tools := rec.snapshot()
	if strings.Join(tools, ",") != "start c1 bash,end c1 bash true" {
		t.Fatalf("工具事件不符：%q", tools)
	}
}

func TestPipeErrorSupersededAndFinalError(t *testing.T) {
	f := newPipeFake(t)
	ch := goPrompt(testCtx(t), f.c, "x", nil)
	id := f.expect("prompt")
	f.send(
		okResp(id, "prompt", map[string]any{"disposition": "started"}),
		evStart(), evDelta("x1"), evEnd("x1", "error", "overloaded"),
		`{"type":"auto_retry_start","attempt":1,"maxAttempts":3,"delayMs":10,"errorMessage":"overloaded"}`,
		evStart(), evDelta("y1"), evEnd("y1", "error", "final boom"),
		`{"type":"auto_retry_end","success":false,"attempt":1,"finalError":"final boom"}`,
		evSettled(false),
	)
	o := recvPrompt(t, ch)
	want := TurnResult{Outcome: OutcomeFailed, Text: "y1", ErrorCode: "pi_error", ErrorMessage: "final boom"}
	if o.err != nil || o.res != want {
		t.Fatalf("结果不符：%+v %v", o.res, o.err)
	}
}

func TestPipeExitMidTurn(t *testing.T) {
	f := newPipeFake(t)
	ch := goPrompt(testCtx(t), f.c, "x", nil)
	id := f.expect("prompt")
	f.send(okResp(id, "prompt", map[string]any{"disposition": "started"}),
		evStart(), evDelta("done"), evEnd("done", "toolUse", ""),
		evStart(), evDelta("half"))
	_ = f.out.Close()
	o := recvPrompt(t, ch)
	want := TurnResult{Outcome: OutcomeFailed, Text: "done\n\nhalf", ErrorCode: "pi_exited", ErrorMessage: "Pi 进程意外退出"}
	if o.err != nil || o.res != want {
		t.Fatalf("结果不符：%+v %v", o.res, o.err)
	}
	waitDone(t, f.c)
}

func TestPipeExitBeforePromptResponse(t *testing.T) {
	f := newPipeFake(t)
	ch := goPrompt(testCtx(t), f.c, "x", nil)
	f.expect("prompt")
	_ = f.out.Close()
	o := recvPrompt(t, ch)
	if o.err != nil || o.res.ErrorCode != "pi_exited" {
		t.Fatalf("已发出 prompt 后进程退出应返回 pi_exited 结果：%+v %v", o.res, o.err)
	}
}

func TestPipeMalformedLinesSkipped(t *testing.T) {
	f := newPipeFake(t)
	rec := newRecorder()
	ch := goPrompt(testCtx(t), f.c, "x", rec)
	id := f.expect("prompt")
	f.send(
		"not json",
		okResp(id, "prompt", map[string]any{"disposition": "started"}),
		`{"type":"message_update","assistantMessageEvent":`,
		"",
		evStart(), evDelta("ok"), evEnd("ok", "stop", ""),
		`{"type":"message_end","message":"oops"}`,
		evSettled(false),
	)
	o := recvPrompt(t, ch)
	if o.err != nil || o.res != (TurnResult{Outcome: OutcomeCompleted, Text: "ok"}) {
		t.Fatalf("结果不符：%+v %v", o.res, o.err)
	}
	if n := f.c.malformedLines(); n != 3 {
		t.Fatalf("应跳过 3 行无法解析的内容，实际 %d", n)
	}
}

func TestPipeCRLFAndUnicodeSeparators(t *testing.T) {
	f := newPipeFake(t)
	rec := newRecorder()
	ch := goPrompt(testCtx(t), f.c, "x", rec)
	id := f.expect("prompt")
	// U+2028/U+2029 是 JSON 字符串内的合法字符，不能作为分帧依据。
	raw := "{\"type\":\"message_update\",\"assistantMessageEvent\":{\"type\":\"text_delta\",\"delta\":\"a b c\"}}\r"
	f.send(okResp(id, "prompt", nil)+"\r", evStart(), raw,
		"{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"a b c\"}],\"stopReason\":\"stop\"}}\r",
		evSettled(false)+"\r")
	o := recvPrompt(t, ch)
	if o.err != nil || o.res.Text != "a b c" || rec.text() != "a b c" {
		t.Fatalf("结果不符：%+v %q %v", o.res, rec.text(), o.err)
	}
}

func TestPipeLargeLine(t *testing.T) {
	f := newPipeFake(t)
	ch := goPrompt(testCtx(t), f.c, "x", nil)
	id := f.expect("prompt")
	big := strings.Repeat("长", 100<<10) // 约 300KB
	f.send(okResp(id, "prompt", map[string]any{"disposition": "started"}),
		evStart(), evDelta(big), evEnd(big, "stop", ""), evSettled(false))
	o := recvPrompt(t, ch)
	if o.err != nil || o.res.Text != big {
		t.Fatalf("超长行应完整读取：len=%d %v", len(o.res.Text), o.err)
	}
}

func TestPipeStaleSettledBeforeResponse(t *testing.T) {
	f := newPipeFake(t)
	ch := goPrompt(testCtx(t), f.c, "x", nil)
	id := f.expect("prompt")
	// prompt 响应之前的 agent_settled 属于上一次运行，应被忽略。
	f.send(evStart(), evEnd("stale", "stop", ""), evSettled(false),
		okResp(id, "prompt", map[string]any{"disposition": "started"}),
		evStart(), evEnd("fresh", "stop", ""), evSettled(false))
	o := recvPrompt(t, ch)
	if o.err != nil || o.res != (TurnResult{Outcome: OutcomeCompleted, Text: "fresh"}) {
		t.Fatalf("结果不符：%+v %v", o.res, o.err)
	}
}

func TestPipeAbortWaitsForPromptResponse(t *testing.T) {
	f := newPipeFake(t)
	ctx := testCtx(t)
	ch := goPrompt(ctx, f.c, "x", nil)
	id := f.expect("prompt")
	abortErr := make(chan error, 1)
	go func() { abortErr <- f.c.Abort(ctx) }()
	select {
	case m := <-f.cmds:
		t.Fatalf("prompt 响应前不应发出打断命令：%v", m)
	case <-time.After(150 * time.Millisecond):
	}
	f.send(okResp(id, "prompt", map[string]any{"disposition": "started"}), evStart(), evDelta("p"))
	cq := f.expect("clear_queue")
	f.send(okResp(cq, "clear_queue", map[string]any{"steering": []string{}, "followUp": []string{}}))
	ab := f.expect("abort")
	f.send(evEnd("p", "aborted", "Request was aborted"), evSettled(true), okResp(ab, "abort", nil))
	if err := <-abortErr; err != nil {
		t.Fatal(err)
	}
	o := recvPrompt(t, ch)
	if o.err != nil || o.res != (TurnResult{Outcome: OutcomeAborted, Text: "p"}) {
		t.Fatalf("结果不符：%+v %v", o.res, o.err)
	}
}

func TestPipePromptCtxCancelDetaches(t *testing.T) {
	f := newPipeFake(t)
	ctx, cancel := context.WithCancel(testCtx(t))
	rec := newRecorder()
	ch := goPrompt(ctx, f.c, "x", rec)
	id := f.expect("prompt")
	f.send(okResp(id, "prompt", map[string]any{"disposition": "started"}), evStart(), evDelta("a"))
	rec.waitFirst(t)
	cancel()
	o := recvPrompt(t, ch)
	if !errors.Is(o.err, context.Canceled) {
		t.Fatalf("ctx 取消应返回 ctx.Err()：%v", o.err)
	}
	f.send(evDelta("late"), evSettled(false))
	// 下一轮正常进行，且 Prompt 返回后不再有回调。
	ch2 := goPrompt(testCtx(t), f.c, "y", nil)
	id2 := f.expect("prompt")
	f.send(okResp(id2, "prompt", map[string]any{"disposition": "started"}), evStart(), evEnd("y", "stop", ""), evSettled(false))
	if o := recvPrompt(t, ch2); o.err != nil || o.res.Text != "y" {
		t.Fatalf("结果不符：%+v %v", o.res, o.err)
	}
	if rec.text() != "a" {
		t.Fatalf("Prompt 返回后不应再回调 sink：%q", rec.text())
	}
}

func TestPipeConcurrentPromptRejected(t *testing.T) {
	f := newPipeFake(t)
	ch := goPrompt(testCtx(t), f.c, "x", nil)
	id := f.expect("prompt")
	if _, err := f.c.Prompt(testCtx(t), "y", nil); err == nil {
		t.Fatal("并发 Prompt 应报错")
	}
	f.send(okResp(id, "prompt", map[string]any{"disposition": "started"}), evSettled(false))
	if o := recvPrompt(t, ch); o.err != nil || o.res.Outcome != OutcomeCompleted {
		t.Fatalf("%+v %v", o.res, o.err)
	}
}

func TestPipeCallExitWhilePending(t *testing.T) {
	f := newPipeFake(t)
	errCh := make(chan error, 1)
	go func() { _, err := f.c.Call(testCtx(t), "get_state", nil); errCh <- err }()
	f.expect("get_state")
	_ = f.out.Close()
	if err := <-errCh; !errors.Is(err, ErrExited) {
		t.Fatalf("应返回 ErrExited：%v", err)
	}
}

func TestPipeCallCtxCancel(t *testing.T) {
	f := newPipeFake(t)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { _, err := f.c.Call(ctx, "get_state", nil); errCh <- err }()
	id := f.expect("get_state")
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("应返回 ctx.Err()：%v", err)
	}
	// 迟到的响应被忽略，不阻塞读协程。
	f.send(okResp(id, "get_state", nil))
	go func() { _, err := f.c.Call(testCtx(t), "get_state", nil); errCh <- err }()
	id2 := f.expect("get_state")
	f.send(okResp(id2, "get_state", map[string]any{}))
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

func TestRingBufferKeepsTail(t *testing.T) {
	r := newRingBuffer(8)
	_, _ = r.Write([]byte("abcdef"))
	_, _ = r.Write([]byte("ghij"))
	if r.String() != "cdefghij" {
		t.Fatalf("%q", r.String())
	}
	_, _ = r.Write([]byte("0123456789"))
	if r.String() != "23456789" {
		t.Fatalf("%q", r.String())
	}
	r2 := newRingBuffer(4)
	_, _ = r2.Write([]byte("x长")) // 截断落在多字节字符中间时丢弃残缺前缀
	_, _ = r2.Write([]byte("ab"))
	if r2.String() != "ab" {
		t.Fatalf("%q", r2.String())
	}
}
