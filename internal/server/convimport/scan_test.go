package convimport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fixture is a fake HOME with Claude Code and Codex histories, plus the folders and git repositories they worked in.
type fixture struct {
	t    *testing.T
	home string
	now  time.Time
	org  uuid.UUID // the default organization, once a store is made
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	f := &fixture{t: t, home: home, now: time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)}
	// Files a scan must never open.
	f.write(".claude.json", `{"oauthAccount":{"emailAddress":"private@example.com"}}`)
	f.write(".claude/.credentials.json", `{"claudeAiOauth":{"accessToken":"secret"}}`)
	f.write(".codex/auth.json", `{"tokens":{"access_token":"secret"}}`)
	return f
}

func (f *fixture) path(rel string) string { return filepath.Join(f.home, rel) }

func (f *fixture) write(rel, content string) string {
	f.t.Helper()
	p := f.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
	return p
}

// repo makes a git repository at rel with an origin remote (empty: none).
func (f *fixture) repo(rel, remote string) string {
	f.t.Helper()
	cfg := "[core]\n\tbare = false\n"
	if remote != "" {
		cfg += "[remote \"origin\"]\n\turl = " + remote + "\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	}
	f.write(rel+"/.git/config", cfg)
	return f.path(rel)
}

// worktree makes a worktree of the repository main at rel.
func (f *fixture) worktree(main, rel string) string {
	f.t.Helper()
	name := filepath.Base(rel)
	f.write(filepath.Join(strings.TrimPrefix(main, f.home+"/"), ".git/worktrees", name, "gitdir"), f.path(rel)+"/.git\n")
	f.write(rel+"/.git", "gitdir: "+filepath.Join(main, ".git/worktrees", name)+"\n")
	return f.path(rel)
}

func lines(records ...map[string]any) string {
	var b strings.Builder
	for _, r := range records {
		j, _ := json.Marshal(r)
		b.Write(j)
		b.WriteByte('\n')
	}
	return b.String()
}

func (f *fixture) at(ago time.Duration) string { return f.now.Add(-ago).Format(time.RFC3339Nano) }

// claude writes a Claude Code session of the given prompts (each answered) in cwd, last active ago.
func (f *fixture) claude(id, cwd string, ago time.Duration, prompts ...string) string {
	f.t.Helper()
	var recs []map[string]any
	for i, p := range prompts {
		at := f.at(ago + time.Duration(len(prompts)-1-i)*time.Minute)
		recs = append(recs,
			map[string]any{"type": "user", "sessionId": id, "cwd": cwd, "timestamp": at, "message": map[string]any{"role": "user", "content": p}},
			map[string]any{"type": "assistant", "sessionId": id, "cwd": cwd, "timestamp": at, "message": map[string]any{"role": "assistant", "model": "claude-opus-5-5",
				"content": []any{map[string]any{"type": "text", "text": "answer to " + p}}}})
	}
	p := f.write(".claude/projects/"+strings.ReplaceAll(cwd, "/", "-")+"/"+id+".jsonl", lines(recs...))
	f.touch(p, ago)
	return p
}

func (f *fixture) touch(p string, ago time.Duration) {
	f.t.Helper()
	at := f.now.Add(-ago)
	if err := os.Chtimes(p, at, at); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) scan(o ScanOptions) []Folder {
	f.t.Helper()
	if o.ClaudeProjects == "" {
		o.ClaudeProjects = f.path(".claude/projects")
	}
	if o.CodexSessions == "" {
		o.CodexSessions = f.path(".codex/sessions")
	}
	if o.CodexIndex == "" {
		o.CodexIndex = f.path(".codex/session_index.jsonl")
	}
	if o.Now == nil {
		o.Now = func() time.Time { return f.now }
	}
	folders, err := Scan(context.Background(), o)
	if err != nil {
		f.t.Fatal(err)
	}
	return folders
}

func find(t *testing.T, folders []Folder, id string) (Folder, Conversation) {
	t.Helper()
	for _, fo := range folders {
		for _, c := range fo.Conversations {
			if c.SessionID == id {
				return fo, c
			}
		}
	}
	t.Fatalf("no conversation %s in %+v", id, folders)
	return Folder{}, Conversation{}
}

// A Claude Code session gives its folder, its title (a rename first, then Claude's own title, else the first prompt),
// its first prompt, its last model, how many prompts and replies have text, and when it was last active. Claude Code's
// own records (sub-agents, meta, command output, tool results) are not messages.
func TestClaudeConversationFields(t *testing.T) {
	f := newFixture(t)
	cwd := f.repo("src/app", "")
	id := "11111111-1111-4111-8111-111111111111"
	at := f.at(2 * time.Hour)
	f.write(".claude/projects/-src-app/"+id+".jsonl", lines(
		map[string]any{"type": "summary", "summary": "old summary"},
		map[string]any{"type": "user", "sessionId": id, "cwd": cwd, "timestamp": at, "isMeta": true, "message": map[string]any{"role": "user", "content": "<local-command-caveat>x</local-command-caveat>"}},
		map[string]any{"type": "user", "sessionId": id, "cwd": cwd, "timestamp": at, "message": map[string]any{"role": "user", "content": "<command-name>/clear</command-name><command-message>clear</command-message><command-args></command-args>"}},
		map[string]any{"type": "user", "sessionId": id, "cwd": cwd, "timestamp": at, "message": map[string]any{"role": "user", "content": "Fix the login page\nand its tests"}},
		map[string]any{"type": "assistant", "sessionId": id, "cwd": cwd, "timestamp": at, "message": map[string]any{"role": "assistant", "model": "claude-sonnet-5-5",
			"content": []any{map[string]any{"type": "thinking", "thinking": "hmm"}, map[string]any{"type": "text", "text": "On it."}, map[string]any{"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{"command": "ls"}}}}},
		map[string]any{"type": "user", "sessionId": id, "cwd": cwd, "timestamp": at, "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "a b"}}}},
		map[string]any{"type": "assistant", "sessionId": id, "cwd": cwd, "timestamp": at, "isSidechain": true, "message": map[string]any{"role": "assistant", "model": "claude-haiku-5-5", "content": "sub-agent"}},
		map[string]any{"type": "ai-title", "aiTitle": "Login page fix", "sessionId": id},
		map[string]any{"type": "assistant", "sessionId": id, "cwd": cwd, "timestamp": f.at(time.Hour), "message": map[string]any{"role": "assistant", "model": "claude-opus-5-5", "content": []any{map[string]any{"type": "text", "text": "Done."}}}},
		map[string]any{"type": "assistant", "sessionId": id, "cwd": cwd, "timestamp": f.at(time.Hour), "message": map[string]any{"role": "assistant", "model": "<synthetic>", "content": []any{map[string]any{"type": "text", "text": "API Error"}}}},
	))
	_, c := find(t, f.scan(ScanOptions{}), id)
	if c.CLI != "claude" || c.Cwd != cwd || c.Title != "Login page fix" || c.FirstPrompt != "Fix the login page\nand its tests" || c.Model != "claude-opus-5-5" {
		t.Fatalf("fields: %+v", c)
	}
	if c.MessageCount != 5 || c.SkipReason != "" || !c.LastActiveAt.Equal(f.now.Add(-time.Hour)) || c.SizeBytes == 0 {
		t.Fatalf("counts: %+v", c)
	}
	f.write(".claude/projects/-src-app/"+id+".jsonl", lines(
		map[string]any{"type": "user", "sessionId": id, "cwd": cwd, "timestamp": at, "message": map[string]any{"role": "user", "content": "Fix the login page"}},
		map[string]any{"type": "custom-title", "customTitle": "My rename", "sessionId": id},
		map[string]any{"type": "ai-title", "aiTitle": "Later AI title", "sessionId": id},
	))
	if _, c := find(t, f.scan(ScanOptions{}), id); c.Title != "My rename" {
		t.Fatalf("a rename wins: %+v", c)
	}
	f.write(".claude/projects/-src-app/"+id+".jsonl", lines(
		map[string]any{"type": "user", "sessionId": id, "cwd": cwd, "timestamp": at, "message": map[string]any{"role": "user", "content": strings.Repeat("word ", 40) + "\nsecond line"}},
	))
	if _, c := find(t, f.scan(ScanOptions{}), id); c.Title != strings.TrimSpace(strings.Repeat("word ", 20)) {
		t.Fatalf("else the first line of the first prompt, at most 100 characters: %q", c.Title)
	}
}

// A Codex rollout gives its thread id and folder from session_meta, its model from turn_context, its prompts from
// user_message events and its replies from assistant messages, and its name from session_index.jsonl (the newest
// one); a sub-agent's rollout is not a conversation.
func TestCodexConversationFields(t *testing.T) {
	f := newFixture(t)
	cwd := f.repo("src/svc", "git@github.com:acme/svc.git")
	id := "22222222-2222-4222-8222-222222222222"
	at := f.at(3 * time.Hour)
	f.write(".codex/sessions/2026/11/25/rollout-2026-11-25T09-00-00-"+id+".jsonl", lines(
		map[string]any{"type": "session_meta", "timestamp": at, "payload": map[string]any{"id": id, "cwd": cwd, "originator": "codex_cli_rs"}},
		map[string]any{"type": "turn_context", "timestamp": at, "payload": map[string]any{"cwd": cwd, "model": "gpt-6-luna"}},
		map[string]any{"type": "response_item", "timestamp": at, "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "<environment_context>x</environment_context>"}}}},
		map[string]any{"type": "event_msg", "timestamp": at, "payload": map[string]any{"type": "user_message", "message": "Add a health check"}},
		map[string]any{"type": "response_item", "timestamp": at, "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Add a health check"}}}},
		map[string]any{"type": "response_item", "timestamp": at, "payload": map[string]any{"type": "function_call", "name": "shell", "arguments": "{}"}},
		map[string]any{"type": "response_item", "timestamp": f.at(2 * time.Hour), "payload": map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Added /healthz."}}}},
		map[string]any{"type": "turn_context", "timestamp": f.at(2 * time.Hour), "payload": map[string]any{"cwd": cwd, "model": "gpt-6"}},
	))
	f.write(".codex/session_index.jsonl", lines(
		map[string]any{"id": id, "thread_name": "Health check"},
		map[string]any{"id": id, "thread_name": "Health check endpoint"},
	))
	sub := "33333333-3333-4333-8333-333333333333"
	f.write(".codex/sessions/2026/11/25/rollout-2026-11-25T09-30-00-"+sub+".jsonl", lines(
		map[string]any{"type": "session_meta", "timestamp": at, "payload": map[string]any{"id": sub, "cwd": cwd, "source": map[string]any{"subagent": map[string]any{"thread_spawn": true}}}},
		map[string]any{"type": "event_msg", "timestamp": at, "payload": map[string]any{"type": "user_message", "message": "sub-agent work"}},
	))
	folders := f.scan(ScanOptions{})
	fo, c := find(t, folders, id)
	if c.CLI != "codex" || c.Cwd != cwd || c.Title != "Health check endpoint" || c.FirstPrompt != "Add a health check" || c.Model != "gpt-6" || c.MessageCount != 2 {
		t.Fatalf("fields: %+v", c)
	}
	if !c.LastActiveAt.Equal(f.now.Add(-2*time.Hour)) || fo.Path != cwd || !fo.Git || fo.Remote != "git@github.com:acme/svc.git" {
		t.Fatalf("folder: %+v / %+v", fo, c)
	}
	for _, fo := range folders {
		for _, c := range fo.Conversations {
			if c.SessionID == sub {
				t.Fatal("a sub-agent's rollout is not a conversation")
			}
		}
	}
}

// Conversations group by git repository: a worktree counts toward its main checkout, and checkouts with the same
// remote are one group, under the most recently active checkout; each conversation keeps its own folder. Outside git
// each folder is its own group. Git repositories come first, then newest first. A remote's credentials are left out.
func TestGroupsWorktreesUnderMainCheckout(t *testing.T) {
	f := newFixture(t)
	main := f.repo("src/web", "https://user:token@example.com/acme/web.git")
	wt := f.worktree(main, "src/web-feature")
	clone := f.repo("other/web", "https://user:token@example.com/acme/web.git")
	plain := f.path("notes")
	if err := os.MkdirAll(plain, 0o700); err != nil {
		t.Fatal(err)
	}
	f.claude("a1111111-1111-4111-8111-111111111111", main, 3*time.Hour, "one")
	f.claude("a2222222-2222-4222-8222-222222222222", wt, 2*time.Hour, "two")
	f.claude("a3333333-3333-4333-8333-333333333333", clone, 4*time.Hour, "three")
	f.claude("a4444444-4444-4444-8444-444444444444", plain, time.Minute, "four")
	folders := f.scan(ScanOptions{})
	if len(folders) != 2 || !folders[0].Git || folders[0].Path != main || folders[0].MainCheckout != main || folders[1].Git || folders[1].Path != plain {
		t.Fatalf("groups: %+v", folders)
	}
	web := folders[0]
	if web.Remote != "https://example.com/acme/web.git" {
		t.Fatalf("the remote without credentials: %q", web.Remote)
	}
	if len(web.Conversations) != 3 || web.Conversations[0].Cwd != wt || web.Conversations[1].Cwd != main || web.Conversations[2].Cwd != clone {
		t.Fatalf("newest first, each with its own folder: %+v", web.Conversations)
	}
	if !web.LastActiveAt.Equal(f.now.Add(-2 * time.Hour)) {
		t.Fatalf("last active: %v", web.LastActiveAt)
	}
}

// A git repository with at least 3 conversations active in the last 30 days is offered checked; fewer, older or
// outside git are not.
func TestPreCheckRule(t *testing.T) {
	f := newFixture(t)
	busy := f.repo("busy", "")
	quiet := f.repo("quiet", "")
	old := f.repo("old", "")
	plain := f.path("plain")
	_ = os.MkdirAll(plain, 0o700)
	for i, ago := range []time.Duration{time.Hour, 48 * time.Hour, 29 * 24 * time.Hour} {
		f.claude("b000000"+string(rune('1'+i))+"-1111-4111-8111-111111111111", busy, ago, "x")
		f.claude("c000000"+string(rune('1'+i))+"-1111-4111-8111-111111111111", plain, ago, "x")
	}
	f.claude("d0000001-1111-4111-8111-111111111111", quiet, time.Hour, "x")
	f.claude("d0000002-1111-4111-8111-111111111111", quiet, time.Hour, "x")
	for i := range 3 {
		f.claude("e000000"+string(rune('1'+i))+"-1111-4111-8111-111111111111", old, 31*24*time.Hour, "x")
	}
	checked := map[string]bool{}
	for _, fo := range f.scan(ScanOptions{}) {
		checked[fo.Path] = fo.PreChecked
	}
	if !checked[busy] || checked[quiet] || checked[old] || checked[plain] {
		t.Fatalf("pre-checked: %v", checked)
	}
}

// Since and Path narrow the scan: older conversations, and those outside the folder, are left out.
func TestScanFilters(t *testing.T) {
	f := newFixture(t)
	a := f.repo("a", "")
	b := f.repo("b", "")
	f.claude("f0000001-1111-4111-8111-111111111111", a, time.Hour, "new")
	f.claude("f0000002-1111-4111-8111-111111111111", a, 40*24*time.Hour, "old")
	f.claude("f0000003-1111-4111-8111-111111111111", b, time.Hour, "elsewhere")
	folders := f.scan(ScanOptions{Since: f.now.Add(-30 * 24 * time.Hour), Path: a})
	if len(folders) != 1 || folders[0].Path != a || len(folders[0].Conversations) != 1 || folders[0].Conversations[0].SessionID != "f0000001-1111-4111-8111-111111111111" {
		t.Fatalf("filtered: %+v", folders)
	}
}

// A file over 4 GiB or 100,000 records, one with no prompt, and one that cannot be read are listed with why they
// cannot be imported; malformed lines are skipped.
func TestSkipsHugeEmptyMalformed(t *testing.T) {
	f := newFixture(t)
	cwd := f.repo("x", "")
	f.claude("g0000001-1111-4111-8111-111111111111", cwd, time.Hour, "fine")
	empty := "g0000002-1111-4111-8111-111111111111"
	f.write(".claude/projects/-x/"+empty+".jsonl", lines(map[string]any{"type": "assistant", "sessionId": empty, "cwd": cwd, "message": map[string]any{"role": "assistant", "content": "hello?"}}))
	many := "g0000003-1111-4111-8111-111111111111"
	f.write(".claude/projects/-x/"+many+".jsonl", lines(map[string]any{"type": "user", "sessionId": many, "cwd": cwd, "message": map[string]any{"role": "user", "content": "hi"}})+strings.Repeat("{}\n", 30))
	noisy := "g0000004-1111-4111-8111-111111111111"
	f.write(".claude/projects/-x/"+noisy+".jsonl", "not json\n"+lines(map[string]any{"type": "user", "sessionId": noisy, "cwd": cwd, "message": map[string]any{"role": "user", "content": "still fine"}}))
	folders := f.scan(ScanOptions{MaxRecords: 20, MaxBytes: 1 << 20})
	reasons := map[string]string{}
	for _, fo := range folders {
		for _, c := range fo.Conversations {
			reasons[c.SessionID] = c.SkipReason
		}
	}
	if reasons["g0000001-1111-4111-8111-111111111111"] != "" || reasons[empty] != SkipEmpty || reasons[many] != SkipTooManyRecords || reasons[noisy] != "" {
		t.Fatalf("reasons: %v", reasons)
	}
	big := "g0000005-1111-4111-8111-111111111111"
	f.write(".claude/projects/-x/"+big+".jsonl", lines(map[string]any{"type": "user", "sessionId": big, "cwd": cwd, "message": map[string]any{"role": "user", "content": strings.Repeat("y", 2000)}}))
	for _, fo := range f.scan(ScanOptions{MaxBytes: 1000}) {
		for _, c := range fo.Conversations {
			if c.SessionID == big && c.SkipReason != SkipTooLarge {
				t.Fatalf("too large: %+v", c)
			}
		}
	}
}

// A session the terminal is still writing ends in half a line: the scan reads the complete records only.
func TestHalfWrittenLastLine(t *testing.T) {
	f := newFixture(t)
	cwd := f.repo("y", "")
	id := "h0000001-1111-4111-8111-111111111111"
	p := f.claude(id, cwd, time.Hour, "first", "second")
	fh, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fh.WriteString(`{"type":"user","sessionId":"` + id + `","message":{"role":"user","content":"thir`)
	fh.Close()
	if _, c := find(t, f.scan(ScanOptions{}), id); c.MessageCount != 4 || c.SkipReason != "" {
		t.Fatalf("complete records only: %+v", c)
	}
}

// The scan opens conversation records under the two roots, Codex's session index and the git metadata of the
// conversations' folders, and nothing else: never a login, token or credential file.
func TestReadsNoCredentialFiles(t *testing.T) {
	f := newFixture(t)
	cwd := f.repo("z", "https://example.com/z.git")
	f.claude("i0000001-1111-4111-8111-111111111111", cwd, time.Hour, "x")
	f.write(".codex/session_index.jsonl", "")
	var mu sync.Mutex
	var opened []string
	f.scan(ScanOptions{Open: func(p string) (*os.File, error) {
		mu.Lock()
		opened = append(opened, p)
		mu.Unlock()
		return os.Open(p)
	}})
	if len(opened) == 0 {
		t.Fatal("nothing opened")
	}
	for _, p := range opened {
		base := filepath.Base(p)
		conversation := strings.HasSuffix(p, ".jsonl") && (strings.HasPrefix(p, f.path(".claude/projects")+"/") || strings.HasPrefix(p, f.path(".codex")+"/"))
		gitMeta := base == ".git" || (base == "config" && filepath.Base(filepath.Dir(p)) == ".git")
		if !conversation && !gitMeta || strings.Contains(p, "credentials") || base == "auth.json" || base == ".claude.json" {
			t.Fatalf("opened %s", p)
		}
	}
}
