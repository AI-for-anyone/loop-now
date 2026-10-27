package commands_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"loop-now/internal/loopctl/conn"
)

func sleepArgs() []string {
	return []string{"sleep", "--run", agentUUID, "--seconds", "120", "--action-id", actionUUID}
}

func TestSleepHelpSchema(t *testing.T) {
	e := newEnv(t)
	e.block = true
	for _, path := range [][]string{{"sleep"}, {"sleep", "status"}} {
		help := e.run(append(append([]string{}, path...), "--help")...)
		help.ok(t)
		schema := e.run(append([]string{"schema"}, path...)...)
		schema.ok(t)
		if string(help.Env.Data) != string(schema.Env.Data) {
			t.Fatalf("help and schema differ: %s %s", help.Stdout, schema.Stdout)
		}
		if path[len(path)-1] == "sleep" {
			var d struct {
				Retry bool
				Flags []struct {
					Name     string
					Min, Max *int
				}
			}
			json.Unmarshal(help.Env.Data, &d)
			if d.Retry {
				t.Fatal("sleep schema advertises retry")
			}
			found := false
			for _, f := range d.Flags {
				if f.Name == "seconds" {
					found = f.Min != nil && *f.Min == 1 && f.Max != nil && *f.Max == 2147483647
				}
			}
			if !found {
				t.Fatal("seconds bounds missing")
			}
		} else if schema.ok(t)["retry"] != true {
			t.Fatal("status schema must advertise read retry")
		}
	}
	if e.dialCount() != 0 {
		t.Fatal("help/schema sent requests")
	}
}

func TestSleepInvalidZeroRequests(t *testing.T) {
	cases := [][]string{
		{"sleep"}, {"sleep", "status", "--run", agentUUID},
	}
	for _, pair := range [][2]string{{"run", "old"}, {"action-id", "new"}, {"seconds", "0"}, {"seconds", "-1"}, {"seconds", "1.5"}, {"seconds", "2147483648"}, {"seconds", "99999999999999999999999"}} {
		a := sleepArgs()
		for i := range a {
			if a[i] == "--"+pair[0] {
				a[i+1] = pair[1]
			}
		}
		cases = append(cases, a)
	}
	for _, name := range []string{"run", "seconds", "action-id"} {
		a := sleepArgs()
		for i := 1; i < len(a); i += 2 {
			if a[i] == "--"+name {
				cases = append(cases, append(append([]string{}, a[:i]...), a[i+2:]...))
				break
			}
		}
	}
	for _, extra := range [][]string{{"--seconds", "1"}, {"--run", agentUUID}, {"--action-id", actionUUID}, {"--reason", "a", "--reason", "b"}, {"--agent", agentUUID}, {"--task", agentUUID}, {"someone"}} {
		cases = append(cases, append(sleepArgs(), extra...))
	}
	for _, a := range cases {
		t.Run(strings.Join(a, " "), func(t *testing.T) {
			e := newEnv(t).agent("http://127.0.0.1:1")
			e.block = true
			r := e.run(a...)
			if r.Code != 2 || e.dialCount() != 0 {
				t.Fatalf("expected usage error without request: %s dials=%d", r.Stdout, e.dialCount())
			}
		})
	}
	for _, a := range [][]string{sleepArgs(), {"sleep", "status"}} {
		e := newEnv(t)
		e.block = true
		e.vars[conn.EnvURL] = "https://127.0.0.1:1"
		e.vars[conn.EnvToken] = extToken
		e.run(a...).fails(t, "command_not_allowed", 2)
		if e.dialCount() != 0 {
			t.Fatal("external identity sent request")
		}
	}
}

func TestSleepStatusPassthrough(t *testing.T) {
	for _, state := range []string{"requested", "sleeping"} {
		t.Run(state, func(t *testing.T) {
			var calls atomic.Int32
			currentRun, wakeAt := `"`+runUUID+`"`, `null`
			if state == "sleeping" {
				currentRun, wakeAt = `null`, `"2026-10-15T12:00:00Z"`
			}
			body := `{"currentRunId":` + currentRun + `,"sleep":{"state":"` + state + `","sourceRunId":"` + runUUID + `","taskId":null,"wakeAt":` + wakeAt + `,"version":9}}`
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "GET" || r.URL.String() != "/agent/v1/sleep" || r.ContentLength != 0 {
					t.Errorf("unexpected status request: %s %s", r.Method, r.URL)
				}
				io.WriteString(w, body)
			}))
			defer s.Close()
			r := newEnv(t).agent(s.URL).run("sleep", "status")
			r.ok(t)
			if string(r.Env.Data) != body || calls.Load() != 1 {
				t.Fatalf("status changed or wrote: %s", r.Stdout)
			}
		})
	}
}

func TestSleepRegistrationPassthrough(t *testing.T) {
	for _, seconds := range []string{"1", "120", "2147483647"} {
		for _, reason := range []string{"", "等待外部结果"} {
			t.Run(seconds+reason, func(t *testing.T) {
				var calls atomic.Int32
				response := `{"sleep":{"state":"requested","wakeAt":null,"version":9},"endTurn":true}`
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != "POST" || r.URL.String() != "/agent/v1/sleep" || r.Header.Get("Authorization") != "Bearer "+agentToken {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					}
					var b map[string]any
					json.NewDecoder(r.Body).Decode(&b)
					want := map[string]any{"runId": agentUUID, "actionId": actionUUID, "seconds": json.Number(seconds)}
					if reason != "" {
						want["reason"] = reason
					}
					encoded, _ := json.Marshal(want)
					var decoded map[string]any
					json.Unmarshal(encoded, &decoded)
					if !reflect.DeepEqual(b, decoded) {
						t.Errorf("body: %v want %v", b, decoded)
					}
					w.WriteHeader(202)
					io.WriteString(w, response)
				}))
				defer s.Close()
				a := sleepArgs()
				a[4] = seconds
				if reason != "" {
					a = append(a, "--reason", reason)
				}
				r := newEnv(t).agent(s.URL).run(a...)
				r.ok(t)
				if string(r.Env.Data) != response || calls.Load() != 1 {
					t.Fatalf("receipt changed or extra request: %s calls=%d", r.Stdout, calls.Load())
				}
			})
		}
	}
}

func TestSleepRetryAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body, code string
		exit       int
	}{
		{"conflict", 409, `{"error":{"code":"sleep_conflict","message":"旧运行","details":{"runId":"old"}}}`, "sleep_conflict", 8},
		{"unauthorized", 401, `{"error":{"code":"unauthenticated","message":"令牌失效"}}`, "unauthenticated", 5},
		{"forbidden", 403, `{"error":{"code":"forbidden","message":"身份无权"}}`, "forbidden", 6},
		{"invalid", 422, `{"error":{"code":"invalid","message":"参数错误","fields":[{"field":"reason","message":"过长"}]}}`, "invalid", 9},
		{"unavailable", 503, `{"error":{"code":"unavailable","message":"稍后"}}`, "unavailable", 4},
		{"lost", 0, "", "connection_failed", 4},
		{"lost_receipt", -1, "", "connection_failed", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if tc.status <= 0 {
					c, rw, _ := w.(http.Hijacker).Hijack()
					if tc.status == -1 {
						rw.WriteString("HTTP/1.1 202 Accepted\r\nContent-Length: 100\r\n\r\n{\"sleep\":")
						rw.Flush()
					}
					c.Close()
					return
				}
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer s.Close()
			r := newEnv(t).agent(s.URL).run(sleepArgs()...)
			r.fails(t, tc.code, tc.exit)
			if calls.Load() != 1 || r.Env.Meta.Attempts != 1 {
				t.Fatalf("write retried: %s calls=%d", r.Stdout, calls.Load())
			}
			if tc.status <= 0 && !strings.Contains(r.Env.Error.Message, "可能已登记") {
				t.Fatal(r.Stdout)
			}
			if tc.status == 409 && !strings.Contains(string(r.Env.Error.Details), `"runId":"old"`) {
				t.Fatal(r.Stdout)
			}
		})
	}
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.String() != "/agent/v1/sleep" {
			t.Errorf("unexpected status request: %s %s", r.Method, r.URL)
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			io.WriteString(w, `{"error":{"code":"unavailable","message":"retry"}}`)
			return
		}
		io.WriteString(w, `{"currentRunId":null,"sleep":null}`)
	}))
	defer s.Close()
	r := newEnv(t).agent(s.URL).run("sleep", "status")
	data := r.ok(t)
	if data["sleep"] != nil || data["currentRunId"] != nil || calls.Load() != 2 || r.Env.Meta.Attempts != 2 {
		t.Fatal(r.Stdout)
	}
}

func TestSleepTimeoutNoRetry(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.Copy(io.Discard, r.Body)
		// 等到客户端超时；不结束或重发任何平台轮次。
		<-r.Context().Done()
	}))
	defer s.Close()
	e := newEnv(t).agent(s.URL)
	e.vars[conn.EnvTimeout] = "50ms"
	r := e.run(sleepArgs()...)
	r.fails(t, "timeout", 4)
	if calls.Load() != 1 || r.Env.Meta.Attempts != 1 || !strings.Contains(r.Env.Error.Message, "可能已登记") {
		t.Fatalf("timeout changed identity or retried: %s calls=%d", r.Stdout, calls.Load())
	}
}
