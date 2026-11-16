package commands_test

import (
	"archive/zip"
	"bytes"
	"encoding/pem"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"loop-now/internal/loopctl/cli"
	"loop-now/internal/loopctl/commands"
	"loop-now/internal/loopctl/conn"
	"loop-now/internal/loopctl/output"
)

// recorder is a server that does not check the contract, used for the generic checks that go over all commands: it
// responds with status and records all requests.
type recorder struct {
	srv    *httptest.Server
	status int
	mu     sync.Mutex
	reqs   []recorded
}

type recorded struct {
	method, url, auth string
	body              []byte
}

func newRecorder(t *testing.T, tlsOn bool) *recorder {
	rec := &recorder{status: 200}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, recorded{r.Method, r.URL.String(), r.Header.Get("Authorization"), b})
		status := rec.status
		rec.mu.Unlock()
		w.Header().Set(conn.HeaderVersion, testVersion)
		w.WriteHeader(status)
		if status == 200 {
			io.WriteString(w, `{"agents":[{"id":"`+agentUUID+`"}]}`)
		} else {
			io.WriteString(w, `{"error":{"code":"x","message":"拒绝"}}`)
		}
	})
	if tlsOn {
		rec.srv = httptest.NewUnstartedServer(h)
		rec.srv.Config.ErrorLog = log.New(io.Discard, "", 0)
		rec.srv.StartTLS()
	} else {
		rec.srv = httptest.NewServer(h)
	}
	t.Cleanup(rec.srv.Close)
	return rec
}

func (r *recorder) caFile(t *testing.T) string {
	return writeFile(t, "ca.pem", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: r.srv.Certificate().Raw})))
}

func (r *recorder) take() []recorded {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.reqs
	r.reqs = nil
	return out
}

// sampleArgv generates argument lists for a command that pass parsing: one option is taken from each mutually
// exclusive group, and one list is generated for each option in a mutually exclusive group.
// All optional options are included so that it can be checked that they are all actually used.
func sampleArgv(t *testing.T, c *cli.Command) [][]string {
	t.Helper()
	exclusive := map[string][]string{} // option -> the other options in the same mutually exclusive group
	var groups [][]string
	for _, k := range c.Constraints {
		if k.Kind == cli.ExactlyOne || k.Kind == cli.AtMostOne {
			groups = append(groups, k.Flags)
			for _, f := range k.Flags {
				for _, g := range k.Flags {
					if g != f {
						exclusive[f] = append(exclusive[f], g)
					}
				}
			}
		}
	}
	// --run and --run-id are synonyms and commands refuse both at once, so sample them as a mutually exclusive
	// group.
	if slices.ContainsFunc(c.Flags, func(f cli.Flag) bool { return f.Name == "run" }) && slices.ContainsFunc(c.Flags, func(f cli.Flag) bool { return f.Name == "run-id" }) {
		groups = append(groups, []string{"run", "run-id"})
	}
	choices := [][]string{nil} // the choice for each group; nil means take the first of each group
	for _, g := range groups {
		for _, alt := range g[1:] {
			choices = append(choices, []string{alt})
		}
	}
	var out [][]string
	for _, choice := range choices {
		chosen := map[string]bool{}
		for _, g := range groups {
			pick := g[0]
			for _, alt := range choice {
				for _, f := range g {
					if f == alt {
						pick = alt
					}
				}
			}
			chosen[pick] = true
			for _, f := range g {
				if f != pick {
					chosen[f] = false
				}
			}
		}
		// Options that must be given together: when any of them is excluded, none of the group is given.
		for _, k := range c.Constraints {
			if k.Kind != cli.Together {
				continue
			}
			for _, f := range k.Flags {
				if v, ok := chosen[f]; ok && !v {
					for _, g := range k.Flags {
						chosen[g] = false
					}
				}
			}
		}
		argv := append([]string(nil), c.Path...)
		for _, a := range c.Args {
			if !a.Variadic {
				argv = append(argv, sampleArg(a.Name))
			}
		}
		for _, f := range c.Flags {
			if v, ok := chosen[f.Name]; ok && !v {
				continue
			}
			if f.Type == cli.TypeBool {
				argv = append(argv, "--"+f.Name)
				continue
			}
			argv = append(argv, "--"+f.Name, sampleFlag(t, c, f))
			if f.Repeatable {
				second := sampleFlag(t, c, f) + "-second"
				if f.Name == "account-id" {
					second = "0192f1a2-0000-7000-8000-0000000000bb"
				}
				argv = append(argv, "--"+f.Name, second)
			}
		}
		out = append(out, argv)
	}
	return out
}

func sampleArg(name string) string {
	switch name {
	case "agent":
		return agentUUID
	case "provider":
		return "anthropic"
	case "cli":
		return "gh"
	case "path":
		return "docs/a.md"
	case "address":
		return "默认项目/dev#0192f1a2"
	case "profile":
		return "default"
	case "name":
		return "新项目"
	case "source":
		return "builtin:hello"
	case "plugin":
		return "hello"
	default:
		return "0192f1a2-0000-7000-8000-0000000000aa"
	}
}

func sampleFlag(t *testing.T, c *cli.Command, f cli.Flag) string {
	if f.Type == cli.TypeTime {
		return "2026-10-13T00:00:00+08:00"
	}
	switch f.Name {
	case "from-file":
		if strings.HasPrefix(c.Name(), "project model-account ") {
			return writeFile(t, "credential.json", `{"type":"api_key","key":"synthetic-key-only"}`)
		}
		if strings.HasPrefix(c.Name(), "project cli-account ") {
			return writeFile(t, "credential.json", `{"type":"cli","fields":{"token":"synthetic-token-only"}}`)
		}
		if c.Name() == "agent credential set-model" {
			return jsonFile(t, map[string]any{"provider": "anthropic", "key": "fake-key"})
		}
		if c.Name() == "task create" {
			return jsonFile(t, taskCreateInput())
		}
		if c.Name() == "task submit" {
			return jsonFile(t, map[string]any{"actionId": actionUUID, "output": map[string]any{}, "edgeId": nil})
		}
		return writeFile(t, "cred.json", `{"k":"v"}`)
	case "expected-prompt-version":
		return "1"
	case "account-id", "operation-id", "run", "run-id", "node-run", "action-id", "expected-node-run":
		return actionUUID
	case "reason-file", "note-file":
		return writeFile(t, "note.txt", "继续当前节点")
	case "file":
		return writeFile(t, "artifact.txt", "abc")
	case "skill-md", "prompt-file", "text-file", "from":
		return writeFile(t, "in.txt", "---\nname: x\n---\n")
	case "dir":
		d := t.TempDir()
		os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("x"), 0o644)
		return d
	case "zip":
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create("SKILL.md")
		w.Write([]byte("x"))
		zw.Close()
		return writeFile(t, "s.zip", buf.String())
	case "out":
		return filepath.Join(t.TempDir(), "out.bin")
	case "ca-file":
		return writeCAFile(t)
	case "server":
		return "https://loop.example.com:8781"
	case "project":
		return "0192f1a2-0000-7000-8000-0000000000a1"
	}
	switch f.Type {
	case cli.TypeEnum:
		return f.Values[0]
	case cli.TypeInt:
		return "5"
	case cli.TypeDuration:
		return "30s"
	}
	return "x-" + f.Name
}

var (
	caOnce sync.Once
	caPEM  []byte
)

func writeCAFile(t *testing.T) string {
	caOnce.Do(func() {
		srv := httptest.NewTLSServer(http.NotFoundHandler())
		caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
		srv.Close()
	})
	return writeFile(t, "ca.pem", string(caPEM))
}

// identityEnv returns an identity environment that can run the command: the external identity for external
// commands, the Agent identity for the rest.
func identityEnv(t *testing.T, c *cli.Command, ext, agent *recorder) *env {
	e := newEnv(t)
	if c.AllowedFor(cli.IdentityExternal) && (len(c.Identities) == 1 || !c.AllowedFor(cli.IdentityAgent)) {
		e.vars[conn.EnvURL] = ext.srv.URL
		e.vars[conn.EnvToken] = extToken
		e.vars[conn.EnvCAFile] = ext.caFile(t)
		return e
	}
	return e.agent(agent.srv.URL)
}

func all() []*cli.Command { return commands.Registry().Commands() }

// Commands that delete or overwrite send no request and do not change the config file when --yes is missing.
func TestDestructiveRequiresYes(t *testing.T) {
	ext, ag := newRecorder(t, true), newRecorder(t, false)
	n := 0
	for _, c := range all() {
		if !c.Destructive {
			continue
		}
		n++
		e := identityEnv(t, c, ext, ag)
		if c.Path[0] == "config" {
			e.stdin = extToken + "\n"
			e.run("config", "set", "default", "--server", "https://a.example", "--token-stdin").ok(t)
		}
		before, _ := os.ReadFile(filepath.Join(e.home, ".config", "loopctl", "config.json"))
		for _, argv := range sampleArgv(t, c) {
			argv = without(argv, "--yes")
			e.run(argv...).fails(t, output.CodeConfirmationRequired, 2)
		}
		after, _ := os.ReadFile(filepath.Join(e.home, ".config", "loopctl", "config.json"))
		if !bytes.Equal(before, after) {
			t.Errorf("%s：缺少 --yes 时改动了配置文件", c.Name())
		}
		if got := append(ext.take(), ag.take()...); len(got) != 0 {
			t.Errorf("%s：缺少 --yes 时发出了请求 %v", c.Name(), got)
		}
	}
	for _, name := range []string{"agent session reset", "config delete", "agent delete", "project delete", "skill replace", "agent skill recopy"} {
		if c := commands.Registry().Find(strings.Fields(name)...); c == nil || !c.Destructive {
			t.Errorf("%s 应标为删除或覆盖类", name)
		}
	}
	if n == 0 {
		t.Fatal("没有删除类命令")
	}
}

func without(argv []string, drop string) []string {
	var out []string
	for _, a := range argv {
		if a != drop {
			out = append(out, a)
		}
	}
	return out
}

// Commands the Agent identity may not use are refused locally and send no request.
func TestAgentIdentityRestrictions(t *testing.T) {
	ag := newRecorder(t, false)
	for _, c := range all() {
		if c.AllowedFor(cli.IdentityAgent) {
			continue
		}
		e := newEnv(t).agent(ag.srv.URL)
		e.block = true
		for _, argv := range sampleArgv(t, c) {
			e.run(argv...).fails(t, output.CodeCommandNotAllowed, 2)
		}
		if e.dialCount() != 0 {
			t.Errorf("%s：Agent 身份被拒绝时仍发出了请求", c.Name())
		}
	}
	want := map[string]bool{"agent status": true, "inbox list": true, "inbox read": true, "inbox send": true, "inbox reply": true, "project list": true, "whoami": true, "version": true, "schema": true}
	for _, c := range all() {
		if c.Path[0] == "task" || c.Path[0] == "sleep" {
			want[c.Name()] = true
		}
		if c.AllowedFor(cli.IdentityAgent) != want[c.Name()] {
			t.Errorf("%s：Agent 身份可用为 %v，与规格不符", c.Name(), c.AllowedFor(cli.IdentityAgent))
		}
	}
}

// version, schema, --help and config need no connection config and send no network requests.
func TestLocalCommandsDoNotUseNetwork(t *testing.T) {
	e := newEnv(t)
	e.block = true
	e.run("version").ok(t)
	e.run("schema").ok(t)
	e.run().ok(t)
	e.run("schema", "--identity", "agent").ok(t)
	e.run("schema", "agent").ok(t)
	e.run("schema", "inbox", "archive").fails(t, output.CodeUnknownCommand, 2)
	for _, c := range all() {
		e.run("schema", c.Name()).ok(t)
		for _, argv := range sampleArgv(t, c) {
			r := e.run(append(argv, "--help")...)
			r.ok(t)
			s := e.run(append([]string{"schema"}, c.Path...)...)
			if string(r.Env.Data) != string(s.Env.Data) {
				t.Errorf("%s --help 与 schema 输出不同", c.Name())
			}
		}
	}
	e.stdin = extToken + "\n"
	e.run("config", "set", "default", "--server", "https://a.example", "--token-stdin").ok(t)
	e.run("config", "list").ok(t)
	e.run("config", "delete", "default", "--yes").ok(t)
	if e.dialCount() != 0 {
		t.Fatalf("本地命令发出了网络请求：%v", e.dials)
	}
}

// Only read requests are retried automatically: commands marked as retryable only send GET or read messages; other
// commands send only one request when they get a 503.
func TestRetryOnlyReads(t *testing.T) {
	ext, ag := newRecorder(t, true), newRecorder(t, false)
	ext.status, ag.status = 503, 503
	for _, c := range all() {
		if !c.Network {
			continue
		}
		e := identityEnv(t, c, ext, ag)
		e.vars[conn.EnvRetryMax] = "1"
		for _, argv := range sampleArgv(t, c) {
			e.run(argv...)
			reqs := append(ext.take(), ag.take()...)
			want := 1
			if c.Retry {
				want = 2
			}
			if len(reqs) != want {
				t.Errorf("%s：503 时请求了 %d 次，应为 %d 次", c.Name(), len(reqs), want)
			}
			for _, r := range reqs {
				isRead := r.method == http.MethodGet || (r.method == http.MethodPost && strings.HasSuffix(strings.Split(r.url, "?")[0], "/read"))
				if c.Retry != isRead {
					t.Errorf("%s：可重试为 %v，但请求是 %s %s", c.Name(), c.Retry, r.method, r.url)
				}
			}
		}
	}
}

// The token only appears in the Authorization header: on success and in all failure cases, the token is in neither
// the URL, the request body nor the output.
func TestTokenNeverLeaks(t *testing.T) {
	for _, status := range []int{200, 401, 403, 500, 0} {
		for _, fromFile := range []bool{false, true} {
			ext, ag := newRecorder(t, true), newRecorder(t, false)
			if status == 0 {
				ext.srv.Close()
				ag.srv.Close()
			} else {
				ext.status, ag.status = status, status
			}
			for _, c := range all() {
				if !c.Network {
					continue
				}
				e := identityEnv(t, c, ext, ag)
				if fromFile && e.vars[conn.EnvToken] != "" {
					delete(e.vars, conn.EnvToken)
					e.stdin = extToken + "\n"
					e.run("config", "set", "default", "--token-stdin")
				}
				for _, argv := range sampleArgv(t, c) {
					r := e.run(argv...)
					if strings.Contains(r.Stdout, tokenSecret) {
						t.Errorf("%s（HTTP %d）：输出中出现了令牌：%s", c.Name(), status, r.Stdout)
					}
				}
				for _, req := range append(ext.take(), ag.take()...) {
					if strings.Contains(req.url, tokenSecret) || bytes.Contains(req.body, []byte(tokenSecret)) {
						t.Errorf("%s：URL 或请求体中出现了令牌", c.Name())
					}
					if !strings.HasPrefix(req.auth, "Bearer ") || !strings.HasSuffix(req.auth, tokenSecret) {
						t.Errorf("%s：Authorization 头不对", c.Name())
					}
				}
			}
		}
	}
	// The output of local commands does not contain the secret part of the token either.
	e := newEnv(t)
	e.stdin = extToken + "\n"
	for _, argv := range [][]string{{"config", "set", "default", "--token-stdin"}, {"config", "list"}, {"schema"}} {
		if r := e.run(argv...); strings.Contains(r.Stdout, tokenSecret) {
			t.Errorf("%v：输出中出现了令牌：%s", argv, r.Stdout)
		}
	}
}

// Every declared option is read when the command runs: the test fails when an option is declared but ignored.
func TestDeclaredFlagsAreUsed(t *testing.T) {
	ext, ag := newRecorder(t, true), newRecorder(t, false)
	var inv *cli.Invocation
	cli.Hook = func(i *cli.Invocation) { inv = i }
	t.Cleanup(func() { cli.Hook = nil })
	for _, c := range all() {
		e := identityEnv(t, c, ext, ag)
		if c.Name() == "config set" || c.Name() == "config delete" {
			e.stdin = extToken + "\n"
		}
		for _, argv := range sampleArgv(t, c) {
			if c.Name() == "config delete" {
				e.run("config", "set", "default", "--server", "https://a.example")
			}
			inv = nil
			e.run(argv...)
			if inv == nil {
				t.Errorf("%v：命令没有执行", argv)
				continue
			}
			for _, a := range argv {
				if name, ok := strings.CutPrefix(a, "--"); ok && !inv.Accessed()[name] {
					t.Errorf("%s：选项 --%s 声明了但执行时没有读取", c.Name(), name)
				}
			}
		}
		ext.take()
		ag.take()
	}
}

// There are no commands for conversations, Agent inboxes, identity management or generic state changes.
func TestNoForbiddenCommands(t *testing.T) {
	ext, ag := newRecorder(t, true), newRecorder(t, false)
	// For conversation messages only the history can be read (GET); the event stream, the inboxes of Agents and
	// external identities, and external identity management are all console-only.
	forbidden := []string{"/events", "/external-identities", "/agents/" + agentUUID + "/inbox"}
	for _, c := range all() {
		for _, f := range c.Flags {
			switch f.Name {
			case "status", "state", "session-state", "run-status", "read-at", "mark-read", "set-status":
				t.Errorf("%s：选项 --%s 会直接设置状态", c.Name(), f.Name)
			}
		}
		if !c.Network {
			continue
		}
		e := identityEnv(t, c, ext, ag)
		for _, argv := range sampleArgv(t, c) {
			e.run(argv...)
		}
		for _, r := range append(ext.take(), ag.take()...) {
			path := strings.Split(r.url, "?")[0]
			for _, f := range forbidden {
				if strings.Contains(path, f) {
					t.Errorf("%s 调用了只限控制台的接口 %s %s", c.Name(), r.method, path)
				}
			}
			if strings.HasSuffix(path, "/agents/"+agentUUID+"/messages") && r.method != http.MethodGet {
				t.Errorf("%s 向 Agent 发送对话消息", c.Name())
			}
			// Project write operations are limited to this allowlist: creating, renaming and deleting a project and
			// its Manager, plus writing shared accounts/OAuth.
			credentialPath := strings.TrimPrefix(path, "/api/v1/projects/"+sampleArg("project"))
			governance := path == "/api/v1/projects" || credentialPath == "" || credentialPath == "/manager"
			credentialWrite := credentialPath == "/credential-accounts" || credentialPath == "/credential-accounts/"+sampleArg("accountId") || credentialPath == "/credential-accounts/"+sampleArg("accountId")+"/credential" || credentialPath == "/model-auth/operations" || credentialPath == "/model-auth/operations/"+sampleArg("operationId")+"/cancel"
			credentialWrite = credentialWrite || credentialPath == "/cli-auth/operations" || credentialPath == "/cli-auth/operations/"+sampleArg("operationId")+"/cancel"
			if strings.Contains(path, "/projects") && r.method != http.MethodGet && !strings.HasSuffix(path, "/agents") && !credentialWrite && !governance {
				t.Errorf("%s 调用了只限控制台的项目接口 %s %s", c.Name(), r.method, path)
			}
			if governance && r.method != http.MethodGet && c.Path[0] != "project" {
				t.Errorf("%s 调用了项目治理接口 %s %s", c.Name(), r.method, path)
			}
		}
	}
}
