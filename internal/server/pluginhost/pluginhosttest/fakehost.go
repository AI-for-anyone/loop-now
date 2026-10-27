// Package pluginhosttest 是测试用的假插件宿主：Node 宿主就绪前，测试用测试二进制自身充当宿主进程
// （以 Env=1 运行一个只调用 Run 的测试）。每个插件的行为由 load 载荷决定，收到的消息记入 FAKE_HOST_JOURNAL 供测试核对。
package pluginhosttest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"
)

// Env 为 1 时测试二进制作为假宿主运行。
const Env = "LOOPNOW_FAKE_PLUGIN_HOST"

// fakePlugin 是 load 载荷里给假宿主的行为说明。
type fakePlugin struct {
	Fail        string         `json:"fail"`
	StateProbe  bool           `json:"stateProbe"`
	FailEvents  []string       `json:"failEvents"`
	HangEvents  []string       `json:"hangEvents"`
	CrashEvents []string       `json:"crashEvents"`
	Intercept   string         `json:"intercept"` // allow、block、hang、error
	Patch       map[string]any `json:"patch"`
	ModifyHang  bool           `json:"modifyHang"`
	Claim       string         `json:"claim"` // yes、no、hang
	// HangSchedules 中的定时条目收到后不回应。
	HangSchedules []string `json:"hangSchedules"`
}

type fakeMsg struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Plugin  string          `json:"plugin,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Run 以假宿主运行：先发 ready，再处理 Go 发来的消息，标准输入关闭后返回。
// FAKE_HOST_JOURNAL 是记录收到的消息的文件，FAKE_HOST_MODE 可为 no-ready、bad-protocol、crash-after-ready。
func Run() {
	var mu sync.Mutex
	out := bufio.NewWriter(os.Stdout)
	write := func(m fakeMsg) {
		b, _ := json.Marshal(m)
		mu.Lock()
		out.Write(append(b, '\n'))
		out.Flush()
		mu.Unlock()
	}
	journal, _ := os.OpenFile(os.Getenv("FAKE_HOST_JOURNAL"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	record := func(v any) {
		b, _ := json.Marshal(v)
		mu.Lock()
		journal.Write(append(b, '\n'))
		mu.Unlock()
	}
	reply := func(id, typ, plugin string, ok bool, value any, errText string) {
		p := map[string]any{"ok": ok}
		if value != nil {
			p["value"] = value
		}
		if errText != "" {
			p["error"] = errText
		}
		raw, _ := json.Marshal(p)
		write(fakeMsg{ID: id, Type: typ, Plugin: plugin, Payload: raw})
	}
	fmt.Fprintln(os.Stderr, "fake host booting")
	switch os.Getenv("FAKE_HOST_MODE") {
	case "no-ready":
		select {}
	case "bad-protocol":
		write(fakeMsg{Type: "ready", Payload: json.RawMessage(`{"protocol":99,"hostVersion":"fake"}`)})
	default:
		write(fakeMsg{Type: "ready", Payload: json.RawMessage(`{"protocol":1,"hostVersion":"fake-1"}`)})
	}

	plugins := map[string]fakePlugin{}
	pending := map[string]chan json.RawMessage{}
	next := 0
	// askRaw 发出宿主→Go 的请求并等待 result，返回整个回应（ok、value、error）。
	askRaw := func(typ, plugin string, payload any) json.RawMessage {
		mu.Lock()
		next++
		id := "h" + strconv.Itoa(next)
		ch := make(chan json.RawMessage, 1)
		pending[id] = ch
		mu.Unlock()
		raw, _ := json.Marshal(payload)
		write(fakeMsg{ID: id, Type: typ, Plugin: plugin, Payload: raw})
		select {
		case v := <-ch:
			return v
		case <-time.After(5 * time.Second):
			return nil
		}
	}
	// ask 同 askRaw，只返回 value。
	ask := func(typ, plugin string, payload any) json.RawMessage {
		var r struct {
			Value json.RawMessage `json:"value"`
		}
		_ = json.Unmarshal(askRaw(typ, plugin, payload), &r)
		return r.Value
	}
	handle := func(m fakeMsg) {
		mu.Lock()
		cfg := plugins[m.Plugin]
		mu.Unlock()
		switch m.Type {
		case "load":
			var p fakePlugin
			_ = json.Unmarshal(m.Payload, &p)
			record(map[string]any{"type": "load", "plugin": m.Plugin})
			if p.Fail != "" {
				reply(m.ID, "result", m.Plugin, false, nil, p.Fail)
				return
			}
			mu.Lock()
			plugins[m.Plugin] = p
			mu.Unlock()
			if p.StateProbe {
				ask("state_set", m.Plugin, map[string]any{"key": "probe/a", "value": map[string]int{"n": 1}})
				got := ask("state_get", m.Plugin, map[string]any{"key": "probe/a"})
				list := ask("state_list", m.Plugin, map[string]any{"prefix": "probe/"})
				raw, _ := json.Marshal(map[string]any{"level": "info", "message": "state " + string(got) + " " + string(list)})
				write(fakeMsg{Type: "log", Plugin: m.Plugin, Payload: raw})
			}
			fmt.Fprintf(os.Stderr, "[%s] loaded\n", m.Plugin)
			reply(m.ID, "result", m.Plugin, true, nil, "")
		case "unload", "config_changed":
			record(map[string]any{"type": m.Type, "plugin": m.Plugin})
			mu.Lock()
			if m.Type == "unload" {
				delete(plugins, m.Plugin)
			}
			mu.Unlock()
			reply(m.ID, "result", m.Plugin, true, nil, "")
		case "event":
			var p struct {
				Event   map[string]any `json:"event"`
				Attempt int            `json:"attempt"`
			}
			_ = json.Unmarshal(m.Payload, &p)
			typ, _ := p.Event["type"].(string)
			record(map[string]any{"type": "event", "plugin": m.Plugin, "eventId": p.Event["id"], "eventType": typ, "attempt": p.Attempt, "event": p.Event})
			switch {
			case slices.Contains(cfg.CrashEvents, typ):
				os.Exit(5)
			case slices.Contains(cfg.HangEvents, typ):
			case slices.Contains(cfg.FailEvents, typ):
				reply(m.ID, "ack", m.Plugin, false, nil, "boom")
			default:
				reply(m.ID, "ack", m.Plugin, true, nil, "")
			}
		case "call":
			var p struct {
				Kind  string         `json:"kind"`
				Event map[string]any `json:"event"`
			}
			_ = json.Unmarshal(m.Payload, &p)
			record(map[string]any{"type": "call", "plugin": m.Plugin, "kind": p.Kind, "event": p.Event})
			switch p.Kind {
			case "intercept":
				switch cfg.Intercept {
				case "block":
					reply(m.ID, "result", m.Plugin, true, map[string]any{"block": true, "reason": "被 " + m.Plugin + " 阻止"}, "")
				case "hang":
				case "error":
					reply(m.ID, "result", m.Plugin, false, nil, "检查出错")
				default:
					reply(m.ID, "result", m.Plugin, true, map[string]any{}, "")
				}
			case "modify":
				if !cfg.ModifyHang {
					reply(m.ID, "result", m.Plugin, true, map[string]any{"patch": cfg.Patch}, "")
				}
			case "claim":
				switch cfg.Claim {
				case "yes":
					reply(m.ID, "result", m.Plugin, true, map[string]any{"handled": true, "by": m.Plugin}, "")
				case "hang":
				default:
					reply(m.ID, "result", m.Plugin, true, map[string]any{"handled": false}, "")
				}
			}
		case "action":
			var p struct {
				ActionID string         `json:"actionId"`
				Where    map[string]any `json:"where"`
				Input    map[string]any `json:"input"`
			}
			_ = json.Unmarshal(m.Payload, &p)
			record(map[string]any{"type": "action", "plugin": m.Plugin, "actionId": p.ActionID, "where": p.Where, "input": p.Input})
			if p.ActionID == "probe-accounts" {
				// 用操作驱动一次 accounts_list，把 Go 的整个回应作为提示返回。
				raw := askRaw("accounts_list", m.Plugin, map[string]any{"projectId": p.Input["projectId"], "kind": p.Input["kind"]})
				reply(m.ID, "result", m.Plugin, true, map[string]any{"notice": string(raw)}, "")
				return
			}
			switch p.Input["mode"] {
			case "fail":
				reply(m.ID, "result", m.Plugin, false, nil, "操作失败")
			case "hang":
			default:
				reply(m.ID, "result", m.Plugin, true, map[string]any{"notice": "已执行 " + p.ActionID, "url": "https://example.test/" + p.ActionID}, "")
			}
		case "http":
			var p struct {
				Via     string              `json:"via"`
				Method  string              `json:"method"`
				Path    string              `json:"path"`
				Query   string              `json:"query"`
				Headers map[string][]string `json:"headers"`
				Body    []byte              `json:"body"`
			}
			_ = json.Unmarshal(m.Payload, &p)
			record(map[string]any{"type": "http", "plugin": m.Plugin, "via": p.Via, "method": p.Method, "path": p.Path, "headers": p.Headers})
			switch p.Path {
			case "/hang":
			case "/fail":
				reply(m.ID, "result", m.Plugin, false, nil, "处理失败")
			default:
				echo, _ := json.Marshal(map[string]any{"via": p.Via, "method": p.Method, "path": p.Path, "query": p.Query, "body": string(p.Body),
					"signature": p.Headers["x-signature"], "authorization": p.Headers["authorization"]})
				reply(m.ID, "result", m.Plugin, true, map[string]any{"status": 202, "body": echo,
					"headers": map[string][]string{"content-type": {"application/json"}, "x-plugin": {m.Plugin}, "connection": {"close"}}}, "")
			}
		case "schedule":
			var p struct {
				ScheduleID string `json:"scheduleId"`
				At         string `json:"at"`
			}
			_ = json.Unmarshal(m.Payload, &p)
			record(map[string]any{"type": "schedule", "plugin": m.Plugin, "scheduleId": p.ScheduleID, "at": p.At})
			if !slices.Contains(cfg.HangSchedules, p.ScheduleID) {
				reply(m.ID, "result", m.Plugin, true, nil, "")
			}
		case "shutdown":
			record(map[string]any{"type": "shutdown"})
			os.Exit(0)
		}
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 16<<20)
	for in.Scan() {
		var m fakeMsg
		if json.Unmarshal(in.Bytes(), &m) != nil {
			continue
		}
		if m.Type == "result" {
			mu.Lock()
			ch := pending[m.ID]
			delete(pending, m.ID)
			mu.Unlock()
			if ch != nil {
				ch <- m.Payload
			}
			continue
		}
		if os.Getenv("FAKE_HOST_MODE") == "crash-after-ready" {
			os.Exit(3)
		}
		go handle(m)
	}
}
