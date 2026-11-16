package e2e_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/executor"
)

// 9.1 Agent identity: the server injects the address and token for Pi; the Agent uses loopctl to look up its
// identity, list, read, reply and send messages on its own initiative.
func TestAgentIdentity(t *testing.T) {
	w := newWorld(t, false)
	s := w.start(nil)
	defer s.stop()
	dev := s.createAgent("Dev assistant")
	reviewer := s.createAgent("Review assistant")

	// The console sends an Inbox message to the dev assistant Agent: the Agent is woken, and the Agent token is
	// injected when Pi starts.
	sent := s.console(201, "POST", "/inbox/messages", map[string]any{"to": "Default project/Dev assistant", "text": "Please take a look at the build"})
	s.waitIdle(dev)
	ag := newLoopctl(t, w.agentEnv())

	who := ag.run("whoami")
	if d := who.ok(t); d["kind"] != "agent" || d["address"] != "Default org/Default project/Dev assistant" || who.Meta.Identity != "agent" {
		t.Fatalf("%s", who.Stdout)
	}

	// Listing the summaries twice does not change the read state either time.
	for i := 0; i < 2; i++ {
		msgs := ag.run("inbox", "list", "--unread").ok(t)["messages"].([]any)
		if len(msgs) != 1 || msgs[0].(map[string]any)["id"] != sent["id"] {
			t.Fatalf("unread listing #%d: %v", i+1, msgs)
		}
	}
	// Reading marks the message read; reading again gives the same result and the read time does not change.
	first := ag.run("inbox", "read", sent["id"].(string)).ok(t)
	if first["text"] != "Please take a look at the build" || first["from"].(map[string]any)["address"] != "@console" || first["readAt"] == nil {
		t.Fatalf("%v", first)
	}
	if again := ag.run("inbox", "read", sent["id"].(string)).ok(t); again["readAt"] != first["readAt"] {
		t.Fatalf("read time should not change on a repeated read: %v → %v", first["readAt"], again["readAt"])
	}
	if msgs := ag.run("inbox", "list", "--unread").ok(t)["messages"].([]any); len(msgs) != 0 {
		t.Fatalf("message should no longer be unread after reading: %v", msgs)
	}
	if msgs := ag.run("inbox", "list").ok(t)["messages"].([]any); len(msgs) != 1 || msgs[0].(map[string]any)["readAt"] == nil {
		t.Fatalf("default listing should include all messages and mark read ones: %v", msgs)
	}

	// The reply stays in the original thread, and the recipient is the console operator.
	ag.stdin = "Build is fine\n"
	reply := ag.run("inbox", "reply", sent["id"].(string), "--text-file", "-").ok(t)
	if reply["threadId"] != sent["threadId"] || reply["to"].(map[string]any)["kind"] != "console" || reply["whenBusy"] != nil {
		t.Fatalf("%v", reply)
	}
	consoleInbox := s.console(200, "GET", "/inbox/messages?unread=true", nil)["messages"].([]any)
	if len(consoleInbox) != 1 || consoleInbox[0].(map[string]any)["id"] != reply["id"] {
		t.Fatalf("console should receive the reply: %v", consoleInbox)
	}

	// Sending to the console operator on the Agent's own initiative starts a new thread.
	ag.stdin = ""
	toConsole := ag.run("inbox", "send", "@console", "--text", "Build finished").ok(t)
	if toConsole["threadId"] == sent["threadId"] || toConsole["whenBusy"] != nil {
		t.Fatalf("%v", toConsole)
	}
	// Sending to another Agent: queue when not specified, interrupt when specified.
	if m := ag.run("inbox", "send", "Default project/Review assistant", "--text", "Please review").ok(t); m["whenBusy"] != "queue" {
		t.Fatalf("%v", m)
	}
	if m := ag.run("inbox", "send", "Default project/Review assistant", "--when-busy", "interrupt", "--text", "Urgent").ok(t); m["whenBusy"] != "interrupt" {
		t.Fatalf("%v", m)
	}
	s.waitIdle(reviewer)
	// An address that cannot be resolved, and an address with #taskID (not supported in the first version).
	ag.run("inbox", "send", "Default project/missing", "--text", "x").fails(t, "not_found", 7)
	ag.run("inbox", "send", "Default project/Review assistant#0192f1a2", "--text", "x").fails(t, "not_found", 7)
	// The Agent identity cannot use admin commands, and cannot view by thread either.
	ag.run("agent", "list", "--platform").fails(t, "command_not_allowed", 2)
	ag.run("inbox", "threads").fails(t, "command_not_allowed", 2)
}

// 9.2 Through the TLS external port the external identity manages Agents, enters credentials, manages Skills,
// views status and history, confirms a session reset, and exchanges messages with the Agent;
// 9.3 for every write operation the audit record can be found by the request ID loopctl printed, and the actor is
// that external identity.
func TestExternalIdentity(t *testing.T) {
	w := newWorld(t, true)
	s := w.start(nil)
	defer s.stop()
	identity, _, secret := s.issueToken("", "ci-bot", nil)

	ext := newLoopctl(t, nil)
	ext.stdin = secret + "\n"
	cfg := ext.run("config", "set", "default", "--server", s.ext, "--ca-file", w.caFile, "--token-stdin")
	if cfg.Code != 0 || strings.Contains(cfg.Stdout, secret) {
		t.Fatalf("%s", cfg.Stdout)
	}
	if info, _ := os.Stat(filepath.Join(ext.home, ".config", "loopctl", "config.json")); info.Mode().Perm() != 0o600 {
		t.Fatalf("config file permissions %o", info.Mode().Perm())
	}
	ext.stdin = ""
	if d := ext.run("whoami").ok(t); d["kind"] != "external" || d["address"] != "@ci-bot" {
		t.Fatalf("%v", d)
	}

	var writes []string // request IDs of write operations
	write := func(args ...string) map[string]any {
		t.Helper()
		r := ext.run(args...)
		d := r.ok(t)
		if r.Meta.RequestID == "" {
			t.Fatalf("%v has no request ID", args)
		}
		writes = append(writes, r.Meta.RequestID)
		return d
	}

	provider, model := cliKeyProvider(t, ext)
	ext.run("agent", "list").fails(t, "missing_argument", 2)
	ext.stdin = "You are an assistant created externally"
	created := write("agent", "create", "--project", "Default project", "--name", "外部助手", "--provider", provider, "--model", model, "--prompt-file", "-") // check-english: zh (non-ASCII Agent name end to end: create, address lookup, Inbox send)
	ext.stdin = ""
	agentID := created["id"].(string)
	const addr = "Default project/外部助手"                                                            // check-english: zh (same non-ASCII Agent name in its address)
	if created["address"] != "Default org/Default project/外部助手" || created["scope"] != "project" { // check-english: zh (same non-ASCII Agent name in its address)
		t.Fatalf("%v", created)
	}
	if list := ext.run("agent", "list", "--project", "Default project").ok(t)["agents"].([]any); len(list) != 1 {
		t.Fatalf("%v", list)
	}
	// Refer to the Agent by address (GET /agents?address=).
	if d := ext.run("agent", "get", addr).ok(t); d["id"] != agentID {
		t.Fatalf("%v", d)
	}

	// Project accounts are write-only; the only account is picked up automatically by the project Agent.
	ext.stdin = `{"type":"api_key","key":"model-secret-value"}`
	cred := ext.run("project", "model-account", "create", "Default project", "--provider", provider, "--name", "External assistant account", "--from-file", "-")
	ext.stdin = ""
	if cred.Code != 0 || strings.Contains(cred.Stdout, "model-secret-value") {
		t.Fatalf("%s", cred.Stdout)
	}
	writes = append(writes, cred.Meta.RequestID)
	detail := ext.run("agent", "get", agentID)
	if detail.Code != 0 || strings.Contains(detail.Stdout, "model-secret-value") || !strings.Contains(detail.Stdout, provider) {
		t.Fatalf("Agent details should contain only credential metadata: %s", detail.Stdout)
	}

	// Upload a Skill and copy it to the Agent.
	skillDir := t.TempDir()
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: e2e-review\ndescription: Skill for end-to-end tests\n---\nBody\n"), 0o644)
	skill := write("skill", "create", "--dir", skillDir)
	copied := write("agent", "skill", "add", addr, "--template", skill["id"].(string))
	if copied["name"] != "e2e-review" {
		t.Fatalf("%v", copied)
	}

	// Status, and confirming a session reset.
	if st := ext.run("agent", "status", addr).ok(t); st["busy"] != false {
		t.Fatalf("%v", st)
	}
	write("agent", "session", "reset", addr, "--yes")

	// Send the Agent an Inbox message; once woken, the Agent replies with its own token; the external identity views
	// the exchange by thread.
	sent := write("inbox", "send", addr, "--text", "Please check the build")
	s.waitIdle(agentID)
	ag := newLoopctl(t, w.agentEnv())
	ag.run("inbox", "read", sent["id"].(string)).ok(t)
	ag.run("inbox", "reply", sent["id"].(string), "--text", "Build is fine").ok(t)
	threads := ext.run("inbox", "threads").ok(t)
	if threads["unreadTotal"] != float64(1) {
		t.Fatalf("%v", threads)
	}
	thread := ext.run("inbox", "thread", sent["threadId"].(string)).ok(t)
	msgs := thread["messages"].([]any)
	if len(msgs) != 2 || msgs[1].(map[string]any)["text"] != "Build is fine" || msgs[1].(map[string]any)["readAt"] != nil {
		t.Fatalf("thread should contain the Agent's reply, still unread: %v", msgs)
	}
	if unread := ext.run("inbox", "list", "--unread").ok(t)["messages"].([]any); len(unread) != 1 {
		t.Fatalf("viewing a thread should not change the read state: %v", unread)
	}

	// Conversation history, run records, usage, anomalies and health status.
	history := ext.run("agent", "history", addr, "--limit", "10").ok(t)["messages"].([]any)
	if len(history) == 0 {
		t.Fatal("the reminder turn should appear in the conversation history")
	}
	runID := history[len(history)-1].(map[string]any)["run"].(map[string]any)["id"].(string)
	if tl := ext.run("agent", "timeline", addr, runID).ok(t); len(tl["entries"].([]any)) == 0 {
		t.Fatalf("%v", tl)
	}
	ext.run("usage", "list", "--project", "Default project").ok(t)
	ext.run("anomaly", "list", "--platform").ok(t)
	h := ext.run("health")
	if d := h.ok(t); d["ok"] != true || strings.Contains(h.Stdout, `"error"`) {
		t.Fatalf("health status on the external port should have no error field: %s", h.Stdout)
	}
	// Console-only operations are refused on the external port; the console API on loopback still needs no login.
	s.console(200, "GET", "/context", nil)

	// 9.3 Audit: for every write operation the record can be found by request ID, the actor is this external
	// identity, and the record contains no tokens or credentials.
	for _, id := range writes {
		var kind, actor string
		var status int
		err := s.a.Store.Pool.QueryRow(context.Background(),
			`SELECT actor_kind, actor_id, status FROM audit_log WHERE request_id = $1`, id).Scan(&kind, &actor, &status)
		if err != nil || kind != "external" || actor != identity || status >= 300 {
			t.Errorf("audit record of request %s: %s %s %d %v", id, kind, actor, status, err)
		}
	}
	var leaked int
	s.a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE audit_log LIKE '%' || $1 || '%' OR audit_log LIKE '%model-secret-value%'`,
		secret[strings.LastIndex(secret, "_")+1:]).Scan(&leaked)
	if leaked != 0 {
		t.Errorf("audit records contain a token or credential")
	}
}

// 10.1 The server stops in the middle of a command: read requests are retried as configured and then return exit
// code 4, write requests are not retried; once the server is back, commands run as usual.
// 10.3 An external token that has been revoked or has expired returns exit code 5; other tokens of the same
// identity are not affected.
func TestExternalRecovery(t *testing.T) {
	w := newWorld(t, true)
	s := w.start(nil)
	identity, revokeID, revoked := s.issueToken("", "ci-bot", nil)
	_, _, keep := s.issueToken(identity, "", nil)
	_, _, short := s.issueToken(identity, "", map[string]any{"expiresAt": time.Now().Add(3 * time.Second).UTC().Format(time.RFC3339)})
	conn := func(token string) *loopctl {
		return newLoopctl(t, map[string]string{"LOOPCTL_URL": s.ext, "LOOPCTL_TOKEN": token, "LOOPCTL_CA_FILE": w.caFile,
			"LOOPCTL_RETRY_INITIAL": "20ms", "LOOPCTL_TIMEOUT": "5s"})
	}
	ext := conn(keep)
	ext.run("skill", "list").ok(t)

	s.stop()
	r := ext.run("skill", "list")
	r.fails(t, "connection_failed", 4)
	if r.Meta.Attempts != 3 {
		t.Fatalf("read requests should be retried 2 times as configured: %s", r.Stdout)
	}
	r = ext.run("inbox", "send", "@console", "--text", "During the outage")
	r.fails(t, "connection_failed", 4)
	if r.Meta.Attempts != 1 {
		t.Fatalf("write requests should not be retried: %s", r.Stdout)
	}
	s = w.start(nil)
	defer s.stop()
	ext.run("skill", "list").ok(t)
	if d := ext.run("inbox", "send", "@console", "--text", "After recovery").ok(t); d["to"].(map[string]any)["kind"] != "console" {
		t.Fatalf("%v", d)
	}

	// Revoke one token: it returns 401, while the other tokens of the same identity keep working.
	conn(revoked).run("whoami").ok(t)
	s.console(204, "DELETE", "/external-identities/"+identity+"/tokens/"+revokeID, nil)
	conn(revoked).run("whoami").fails(t, "unauthenticated", 5)
	conn(keep).run("whoami").ok(t)
	// An expired token returns 401.
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if conn(short).run("whoami").Code == 5 {
			conn(keep).run("whoami").ok(t)
			return
		}
	}
	t.Fatal("an expired token should return 401")
}

// 10.2 After the Pi process restarts, the old Agent token returns exit code 5 and the newly injected token works.
func TestAgentTokenAfterPiRestart(t *testing.T) {
	w := newWorld(t, false)
	s := w.start(nil)
	defer s.stop()
	dev := s.createAgent("Dev assistant")
	s.console(202, "POST", "/agents/"+dev+"/messages", map[string]any{"text": "Hello"})
	s.waitIdle(dev)
	old := w.agentEnv()
	newLoopctl(t, old).run("whoami").ok(t)

	// Pi exits abnormally in the middle of a reply (fakepi's !crash); the process is discarded and its token becomes
	// invalid with it.
	s.console(202, "POST", "/agents/"+dev+"/messages", map[string]any{"text": "!crash this turn crashes"})
	s.waitIdle(dev)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if s.a.Executor.Process(uuid.MustParse(dev)).State == executor.ProcessStopped {
			break
		}
	}
	var r result
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if r = newLoopctl(t, old).run("whoami"); r.Code == 5 {
			break
		}
	}
	r.fails(t, "unauthenticated", 5)

	// The next message starts Pi again and injects a new token; the new token works and the old one stays invalid.
	s.console(202, "POST", "/agents/"+dev+"/messages", map[string]any{"text": "One more time"})
	s.waitIdle(dev)
	fresh := w.agentEnv()
	if fresh["LOOPCTL_AGENT_TOKEN"] == old["LOOPCTL_AGENT_TOKEN"] {
		t.Fatal("a new token should be injected after Pi restarts")
	}
	if d := newLoopctl(t, fresh).run("whoami").ok(t); d["address"] != "Default org/Default project/Dev assistant" {
		t.Fatalf("%v", d)
	}
	newLoopctl(t, old).run("whoami").fails(t, "unauthenticated", 5)
}
