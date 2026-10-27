package pirpc

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// 扩展的对话请求立即按取消回应（与请求超时到期的默认值相同），不管有没有 timeout；notify 与 setStatus 交给观察者，
// 其余 fire-and-forget 方法忽略。
func TestExtensionUIDialogsAnsweredWithDefault(t *testing.T) {
	f := newPipeFake(t)
	o := &obsRec{}
	f.c.SetObserver(o)
	f.send(
		`{"type":"extension_ui_request","id":"d1","method":"confirm","title":"清空会话？","message":"会丢失"}`,
		`{"type":"extension_ui_request","id":"d2","method":"select","title":"选一个","options":["a","b"],"timeout":10000}`,
		`{"type":"extension_ui_request","id":"n1","method":"notify","message":"已拦截","notifyType":"warning"}`,
		`{"type":"extension_ui_request","id":"s1","method":"setStatus","statusKey":"ext","statusText":"第 3 轮"}`,
		`{"type":"extension_ui_request","id":"w1","method":"setWidget","widgetKey":"ext","widgetLines":["x"]}`,
	)
	got := map[string]bool{}
	for range 2 {
		m := f.next()
		if m["type"] != "extension_ui_response" || m["cancelled"] != true {
			t.Fatalf("回应：%v", m)
		}
		got[m["id"].(string)] = true
	}
	if !got["d1"] || !got["d2"] {
		t.Fatalf("两个对话都应回应：%v", got)
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
		t.Fatalf("通知：%+v", notices)
	}
	want := []map[string]any{
		{"method": "confirm", "message": "清空会话？", "answered": "cancelled"},
		{"method": "select", "message": "选一个", "answered": "cancelled"},
		{"method": "notify", "message": "已拦截", "level": "warning"},
		{"method": "setStatus", "statusKey": "ext"},
	}
	for i, n := range notices {
		if n.Type != NoticeExtensionUI {
			t.Fatalf("类型：%+v", n)
		}
		for k, v := range want[i] {
			if n.Data[k] != v {
				t.Fatalf("第 %d 条 %s：%v，期望 %v", i, k, n.Data[k], v)
			}
		}
	}
	if s, _ := notices[3].Data["message"].(*string); s == nil || *s != "第 3 轮" {
		t.Fatalf("状态文字：%v", notices[3].Data["message"])
	}
}

// 设置了 Dialogs 时，一轮之中的 confirm、select、input 交给它，按它的回答回应 Pi（confirm 为 confirmed，select、input 为 value），
// 回答后才记为运行事件（带 answered 与 questionId）；editor 与一轮之外的对话仍立即取消，不交给它。
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
	f.send(`{"type":"extension_ui_request","id":"before","method":"confirm","title":"一轮之外"}`)
	if m := f.next(); m["id"] != "before" || m["cancelled"] != true {
		t.Fatalf("一轮之外的对话立即取消：%v", m)
	}
	ctx := testCtx(t)
	ch := goPrompt(ctx, f.c, "hi", nil)
	id := f.expect("prompt")
	f.send(okResp(id, "prompt", map[string]any{"disposition": "started"}),
		`{"type":"extension_ui_request","id":"c","method":"confirm","title":"继续？","timeout":5000}`,
		`{"type":"extension_ui_request","id":"s","method":"select","title":"选一个","options":["a","b"]}`,
		`{"type":"extension_ui_request","id":"i","method":"input","title":"名字","placeholder":"张三"}`,
		`{"type":"extension_ui_request","id":"e","method":"editor","title":"编辑"}`)
	got := map[string]map[string]any{}
	for range 4 {
		m := f.next()
		got[m["id"].(string)] = m
	}
	if got["c"]["confirmed"] != false || got["c"]["cancelled"] != nil || got["s"]["value"] != "b" || got["i"]["cancelled"] != true || got["e"]["cancelled"] != true {
		t.Fatalf("回应：%v", got)
	}
	mu.Lock()
	if len(asked) != 3 {
		t.Fatalf("交给处理者的对话：%+v", asked)
	}
	for _, a := range asked {
		if a.Method == "confirm" && a.Timeout != 5*time.Second || a.Method == "select" && (len(a.Options) != 2 || a.Title != "选一个" || a.Timeout != 0) ||
			a.Method == "input" && a.Placeholder != "张三" {
			t.Fatalf("对话内容：%+v", a)
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
			t.Fatalf("事件带提问：%v", n.Data)
		}
	}
	if len(notices) != 5 || answered["select"] != "answered" || answered["input"] != "expired" || answered["editor"] != "cancelled" {
		t.Fatalf("事件：%+v", notices)
	}
	f.send(jsonLine(map[string]any{"type": "agent_end", "messages": []any{}, "willRetry": false}), evSettled(false))
	recvPrompt(t, ch)
}
