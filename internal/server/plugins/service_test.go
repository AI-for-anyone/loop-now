package plugins_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/plugins"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"
)

type env struct {
	t        *testing.T
	st       *store.Store
	svc      *plugins.Service
	official string
	project  uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st := sqlitetest.NewStore(t)
	dc, err := st.EnsureDefaults(context.Background(), "org", "project")
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, st: st, official: t.TempDir(), project: dc.Project.ID}
	e.svc = &plugins.Service{Store: st, Root: filepath.Join(t.TempDir(), "plugins"), OfficialDir: e.official, GitBin: "git", NodeBin: nodeBin(),
		APIURL: "http://127.0.0.1:1/api/v1", PlatformVersion: "2026.11", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := e.svc.Init(); err != nil {
		t.Fatal(err)
	}
	return e
}

// nodeBin returns the local node (needed by the npm source tests); empty when there is none.
func nodeBin() string {
	p := filepath.Join(os.Getenv("HOME"), ".local/node-v24.21.0/bin/node")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// plugin writes a plugin directory that has everything: a platform side, an Agent extension, a Skill, a CLI
// directory, an MCP server, config and accounts.
func plugin(t *testing.T, dir, id, version string) string {
	t.Helper()
	pkg := map[string]any{
		"name": "@test/" + id, "version": version, "description": "Test plugin " + id,
		"pi": map[string]any{"extensions": []any{"./agent/index.ts"}, "skills": []any{"./skills"}},
		"loopNow": map[string]any{
			"id": id, "displayName": strings.ToUpper(id), "platform": "./platform/index.ts",
			"events": []any{"inbox.received", "agent.created"}, "permissions": []any{"agents.read", "state"},
			"configSchema": map[string]any{"type": "object", "properties": map[string]any{
				"url": map[string]any{"type": "string"}, "token": map[string]any{"type": "string", "x-secret": true},
				"channel": map[string]any{"type": "string", "x-scope": "project"}, "key": map[string]any{"type": "string", "x-secret": true, "x-scope": "project"}}},
			"accounts": []any{
				map[string]any{"kind": "lark", "label": "Lark", "multiple": false, "fields": map[string]any{"type": "object", "required": []any{"appId"},
					"properties": map[string]any{"appId": map[string]any{"type": "string"}, "secret": map[string]any{"type": "string", "x-secret": true}}}},
				map[string]any{"kind": "mail", "label": "Mail", "multiple": true, "fields": map[string]any{"type": "object"}},
			},
			"mcpServers": map[string]any{"browser": map[string]any{"command": "node", "args": []any{"./mcp/server.js", "--data", "${LOOP_NOW_PLUGIN_DATA}/profiles/${LOOP_NOW_AGENT_ID}"},
				"env": map[string]any{"HOME_DIR": "${LOOP_NOW_PLUGIN_ROOT}", "OTHER": "${KEEP_ME}"}},
				"design": map[string]any{"type": "http", "url": "http://127.0.0.1:3845/mcp", "headers": map[string]any{"X-Agent": "${LOOP_NOW_AGENT_ID}", "Authorization": "Bearer ${FIGMA_TOKEN}"}}},
			"bin": []any{"./bin"},
		},
	}
	b, _ := json.Marshal(pkg)
	for f, content := range map[string]string{"package.json": string(b), "agent/index.ts": "export default () => {}", "platform/index.ts": "export default () => {}",
		"skills/demo/SKILL.md": "---\nname: demo\n---\n", "mcp/server.js": "", "bin/tool": "#!/bin/sh\n"} {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func (e *env) install(src plugins.Source, confirm bool) plugins.View {
	e.t.Helper()
	pv, err := e.svc.Preview(context.Background(), plugins.PreviewRequest{Source: &src})
	if err != nil {
		e.t.Fatal(err)
	}
	v, err := e.svc.Install(context.Background(), plugins.InstallInput{PreviewID: pv.PreviewID, Confirm: confirm}, "")
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func code(err error) string {
	if ae, ok := apperr.As(err); ok {
		return ae.Code
	}
	return ""
}

// A local directory is used in place and needs confirmation; an official source installs directly and is copied to
// <Root>/<id>/<version>/; an update with the same version and content reuses the existing directory, while different
// content requires a new version number. Uninstall deletes the platform's install directories (not a local
// directory), and the data directory only when asked.
func TestInstallLocalAndBuiltin(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	local := plugin(t, t.TempDir(), "devkit", "0.1.0")
	pv, err := e.svc.Preview(ctx, plugins.PreviewRequest{Source: &plugins.Source{Kind: "local", Location: local}})
	if err != nil || pv.Trusted || pv.Source.Location != local || pv.Manifest.ID != "devkit" || pv.InstalledVersion != nil {
		t.Fatalf("local preview: %+v %v", pv, err)
	}
	if _, err := e.svc.Install(ctx, plugins.InstallInput{PreviewID: pv.PreviewID}, ""); code(err) != apperr.CodePluginConfirmationRequired {
		t.Fatalf("an unofficial source needs confirmation: %v", err)
	}
	v, err := e.svc.Install(ctx, plugins.InstallInput{PreviewID: pv.PreviewID, Confirm: true}, "")
	if err != nil || v.Status != "installed" || !v.HasPlatform || v.Source.Kind != "local" {
		t.Fatalf("local install: %+v %v", v, err)
	}
	d, err := e.svc.Get(ctx, "devkit")
	if err != nil || d.InstallDir != local || len(d.Versions) != 1 || !d.Versions[0].Current {
		t.Fatalf("a local source should be used in place: %+v %v", d, err)
	}
	if _, err := e.svc.Install(ctx, plugins.InstallInput{PreviewID: pv.PreviewID, Confirm: true}, ""); code(err) != apperr.CodeNotFound {
		t.Fatalf("a preview should expire once used: %v", err)
	}
	pv, _ = e.svc.Preview(ctx, plugins.PreviewRequest{Source: &plugins.Source{Kind: "local", Location: local}})
	if pv.InstalledVersion == nil || *pv.InstalledVersion != "0.1.0" {
		t.Fatalf("the preview should give the installed version: %+v", pv)
	}
	if _, err := e.svc.Install(ctx, plugins.InstallInput{PreviewID: pv.PreviewID, Confirm: true}, ""); code(err) != apperr.CodeNameConflict {
		t.Fatalf("installing the same id again should ask for an update: %v", err)
	}

	official := plugin(t, filepath.Join(e.official, "hello"), "hello", "1.0.0")
	if got := e.svc.BuiltinSources(); len(got) != 1 || got[0].ID != "hello" || got[0].Version != "1.0.0" {
		t.Fatalf("official plugin list: %+v", got)
	}
	v = e.install(plugins.Source{Kind: "builtin", Location: "hello"}, false)
	v1 := filepath.Join(e.svc.Root, "hello", "1.0.0")
	if d, _ := e.svc.Get(ctx, "hello"); d.InstallDir != v1 || v.Source.Kind != "builtin" {
		t.Fatalf("an official source should be copied to the version directory: %+v", d)
	}
	if _, err := os.Stat(filepath.Join(v1, "platform/index.ts")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.svc.Root, "hello", "data")); err != nil {
		t.Fatalf("data directory: %v", err)
	}
	update := func() error {
		pv, err := e.svc.Preview(ctx, plugins.PreviewRequest{PluginID: "hello"})
		if err != nil {
			return err
		}
		_, err = e.svc.Install(ctx, plugins.InstallInput{PreviewID: pv.PreviewID}, "hello")
		return err
	}
	if err := update(); err != nil {
		t.Fatalf("the same version with the same content can be reinstalled: %v", err)
	}
	if err := os.WriteFile(filepath.Join(official, "platform/index.ts"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := update(); code(err) != apperr.CodePluginVersionConflict {
		t.Fatalf("the same version with different content should be rejected: %v", err)
	}
	plugin(t, official, "hello", "1.1.0")
	if err := update(); err != nil {
		t.Fatal(err)
	}
	d, _ = e.svc.Get(ctx, "hello")
	if d.Plugin.Version != "1.1.0" || len(d.Versions) != 2 || d.InstallDir != filepath.Join(e.svc.Root, "hello", "1.1.0") {
		t.Fatalf("after the update: %+v", d)
	}
	if _, err := os.Stat(v1); err != nil {
		t.Fatalf("the old version directory should be kept for Pi processes already started: %v", err)
	}
	if err := e.svc.Uninstall(ctx, "hello", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(v1); !os.IsNotExist(err) {
		t.Fatalf("uninstall should delete the version directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.svc.Root, "hello", "data")); err != nil {
		t.Fatalf("the data directory should be kept by default: %v", err)
	}
	if err := e.svc.Uninstall(ctx, "devkit", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(local, "package.json")); err != nil {
		t.Fatalf("the directory of a local source must not be deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.svc.Root, "devkit")); !os.IsNotExist(err) {
		t.Fatalf("the plugin directory should be gone after deleteData: %v", err)
	}
	if _, err := e.svc.Preview(ctx, plugins.PreviewRequest{Source: &plugins.Source{Kind: "builtin", Location: "nope"}}); code(err) != apperr.CodeNotFound {
		t.Fatalf("official plugin that does not exist: %v", err)
	}
	if _, err := e.svc.Preview(ctx, plugins.PreviewRequest{Source: &plugins.Source{Kind: "local", Location: "relative/dir"}}); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("a local directory must be an absolute path: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(e.svc.Root, ".staging")); len(entries) != 0 {
		t.Fatalf("the staging area should be empty: %v", entries)
	}
}

// A git source is cloned by branch, its commit is recorded and .git is removed; an npm source (when node is available
// locally) is packed and then unpacked. Both are unofficial sources.
func TestInstallGitAndNPM(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	repo := plugin(t, t.TempDir(), "gitty", "2.0.0")
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(repo, "init", "-q", "-b", "main")
	run(repo, "add", ".")
	run(repo, "commit", "-q", "-m", "init")
	run(repo, "checkout", "-q", "-b", "release")
	commit := run(repo, "rev-parse", "HEAD")
	ref := "release"
	v := e.install(plugins.Source{Kind: "git", Location: "file://" + repo, Ref: &ref}, true)
	if v.Source.Commit == nil || *v.Source.Commit != commit || *v.Source.Ref != "release" {
		t.Fatalf("git source: %+v", v.Source)
	}
	dir := filepath.Join(e.svc.Root, "gitty", "2.0.0")
	if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Fatalf(".git should be removed: %v", err)
	}
	if _, err := e.svc.Preview(ctx, plugins.PreviewRequest{Source: &plugins.Source{Kind: "git", Location: "file:///nonexistent/repo"}}); code(err) != apperr.CodePluginSourceFailed {
		t.Fatalf("git source that cannot be fetched: %v", err)
	}
	if e.svc.NodeBin == "" {
		t.Log("no local node, skipping the npm source")
		return
	}
	pkg := plugin(t, t.TempDir(), "npmmy", "3.1.0")
	v = e.install(plugins.Source{Kind: "npm", Location: pkg}, true)
	if v.Source.NpmVersion == nil || *v.Source.NpmVersion != "3.1.0" {
		t.Fatalf("npm source: %+v", v.Source)
	}
	if _, err := os.Stat(filepath.Join(e.svc.Root, "npmmy", "3.1.0", "platform/index.ts")); err != nil {
		t.Fatalf("the npm package should be unpacked into the version directory: %v", err)
	}
}

// Global config: secrets are write-only and never echoed back, values are validated against the schema, a revision
// mismatch gives 412, and saving sends plugin.config_changed; project config can only write x-scope project fields.
func TestConfigAccountsAndGrants(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.install(plugins.Source{Kind: "local", Location: plugin(t, t.TempDir(), "cfg", "1.0.0")}, true)
	if _, err := e.st.Pool.Exec(ctx, `UPDATE plugins SET manifest=json_set(manifest,'$.events',json('["plugin.config_changed","plugin.enabled"]'))`); err != nil {
		t.Fatal(err)
	}
	tok := "s3cret"
	c, err := e.svc.PutConfig(ctx, "cfg", plugins.ConfigInput{Values: map[string]any{"url": "https://x"}, Secrets: map[string]*string{"token": &tok}})
	if err != nil || c.Revision != 1 || c.SecretsSet[0] != "token" || c.Values["token"] != nil {
		t.Fatalf("save global config: %+v %v", c, err)
	}
	if _, err := e.svc.PutConfig(ctx, "cfg", plugins.ConfigInput{Values: map[string]any{}, ExpectedRevision: 0}); code(err) != apperr.CodePreconditionFailed {
		t.Fatalf("revision mismatch: %v", err)
	}
	if _, err := e.svc.PutConfig(ctx, "cfg", plugins.ConfigInput{Values: map[string]any{"url": 3}, ExpectedRevision: 1}); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("wrong type: %v", err)
	}
	if _, err := e.svc.PutConfig(ctx, "cfg", plugins.ConfigInput{Values: map[string]any{"token": "x"}, ExpectedRevision: 1}); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("secret in values: %v", err)
	}
	var global string
	if err := e.st.Pool.QueryRow(ctx, `SELECT secrets->>'token' FROM plugin_config WHERE plugin_id='cfg' AND project_id IS NULL`).Scan(&global); err != nil || global != tok {
		t.Fatalf("secret should be saved: %q %v", global, err)
	}
	if _, err := e.svc.PutProject(ctx, e.project, "cfg", plugins.ProjectInput{Enabled: true, Values: map[string]any{"url": "x"}}); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("a non-project field cannot be overridden in a project: %v", err)
	}
	key := "pk"
	pp, err := e.svc.PutProject(ctx, e.project, "cfg", plugins.ProjectInput{Enabled: true, Values: map[string]any{"channel": "#ops"}, Secrets: map[string]*string{"key": &key}})
	// The manifest was changed with json_set above; here we only check which fields exist (for their order see
	// TestPluginSchemaOrder in httpapi).
	if err != nil || !pp.Enabled || pp.Config.Revision != 1 || pp.Config.SecretsSet[0] != "key" || !slices.Equal(slices.Sorted(slices.Values(pp.ProjectFields)), []string{"channel", "key"}) {
		t.Fatalf("project config: %+v %v", pp, err)
	}
	var events []string
	rows, _ := e.st.Pool.Query(ctx, `SELECT type||':'||coalesce(project_id,'-') FROM plugin_events ORDER BY id`)
	for rows.Next() {
		var s string
		rows.Scan(&s)
		events = append(events, s)
	}
	rows.Close()
	want := []string{"plugin.config_changed:-", "plugin.enabled:" + e.project.String(), "plugin.config_changed:" + e.project.String()}
	if !slices.Equal(events, want) {
		t.Fatalf("events: %v", events)
	}

	// Accounts: a single-account kind can only have one; secret fields are not echoed back; required is enforced.
	sec := map[string]string{"secret": "lark-secret"}
	if _, err := e.svc.CreateAccount(ctx, e.project, "cfg", plugins.AccountCreate{Kind: "lark", Name: "Main", Values: map[string]any{}, Secrets: sec}); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("missing required field: %v", err)
	}
	lark, err := e.svc.CreateAccount(ctx, e.project, "cfg", plugins.AccountCreate{Kind: "lark", Name: "Main", Values: map[string]any{"appId": "cli_1"}, Secrets: sec})
	if err != nil || lark.Values["secret"] != nil || lark.SecretsSet[0] != "secret" || lark.Values["appId"] != "cli_1" {
		t.Fatalf("add account: %+v %v", lark, err)
	}
	if _, err := e.svc.CreateAccount(ctx, e.project, "cfg", plugins.AccountCreate{Kind: "lark", Name: "Secondary", Values: map[string]any{"appId": "cli_2"}}); code(err) != apperr.CodeNameConflict {
		t.Fatalf("single-account kind: %v", err)
	}
	if _, err := e.svc.CreateAccount(ctx, e.project, "cfg", plugins.AccountCreate{Kind: "github", Name: "x", Values: map[string]any{}}); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("undeclared kind: %v", err)
	}
	mail1, _ := e.svc.CreateAccount(ctx, e.project, "cfg", plugins.AccountCreate{Kind: "mail", Name: "a", Values: map[string]any{"addr": "a@x"}})
	mail2, _ := e.svc.CreateAccount(ctx, e.project, "cfg", plugins.AccountCreate{Kind: "mail", Name: "b", Values: map[string]any{"addr": "b@x"}})
	lark, err = e.svc.UpdateAccount(ctx, e.project, "cfg", lark.ID, plugins.AccountUpdate{Name: "Main account", Values: map[string]any{"appId": "cli_9"}, ExpectedRevision: 1})
	if err != nil || lark.Revision != 2 || lark.SecretsSet[0] != "secret" {
		t.Fatalf("update account (secret kept): %+v %v", lark, err)
	}

	// Grants: a project Agent needs the plugin enabled in the project; a platform Agent is unrestricted and has no
	// accounts.
	agent, err := e.st.CreateAgent(ctx, store.ProjectScope(e.project), "worker", "anthropic", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	platform, _ := e.st.CreateAgent(ctx, store.PlatformScope(), "patrol", "anthropic", "m", "")
	if _, err := e.svc.PutAgentPlugin(ctx, agent.ID, "cfg", plugins.AgentInput{Granted: true, AccountIDs: []uuid.UUID{lark.ID, mail1.ID, mail2.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PutAgentPlugin(ctx, platform.ID, "cfg", plugins.AgentInput{Granted: true, AccountIDs: []uuid.UUID{lark.ID}}); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("a platform Agent has no project accounts: %v", err)
	}
	if _, err := e.svc.PutAgentPlugin(ctx, platform.ID, "cfg", plugins.AgentInput{Granted: true}); err != nil {
		t.Fatal(err)
	}

	// What is handed to Pi: paths are absolute, the placeholders and relative paths in mcp.json have been replaced
	// with the values for this plugin and Agent, the config contains no secrets, and accounts are injected per kind.
	assets, err := e.svc.AgentAssets(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := e.svc.Get(ctx, "cfg")
	root := d.InstallDir
	if !slices.Equal(assets.Extensions, []string{filepath.Join(root, "agent/index.ts")}) || !slices.Equal(assets.Skills, []string{filepath.Join(root, "skills")}) ||
		!slices.Equal(assets.Bin, []string{filepath.Join(root, "bin")}) {
		t.Fatalf("paths: %+v", assets)
	}
	var mcp struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	if err := json.Unmarshal(assets.MCPServers["cfg-browser"], &mcp); err != nil {
		t.Fatal(err)
	}
	if mcp.Command != "node" || mcp.Args[0] != filepath.Join(root, "mcp/server.js") || mcp.Args[2] != d.DataDir+"/profiles/"+agent.ID.String() ||
		mcp.Env["HOME_DIR"] != root || mcp.Env["OTHER"] != "${KEEP_ME}" {
		t.Fatalf("mcp: %+v", mcp)
	}
	// A url entry is handed to Pi as-is: only these three placeholders are replaced (in headers too); any other ${...}
	// is left for Pi and is not resolved against the install directory.
	var design map[string]any
	if err := json.Unmarshal(assets.MCPServers["cfg-design"], &design); err != nil {
		t.Fatal(err)
	}
	if h, _ := design["headers"].(map[string]any); len(design) != 3 || design["type"] != "http" || design["url"] != "http://127.0.0.1:3845/mcp" ||
		h["X-Agent"] != agent.ID.String() || h["Authorization"] != "Bearer ${FIGMA_TOKEN}" {
		t.Fatalf("url entry: %v", design)
	}
	var cfg map[string]any
	_ = json.Unmarshal([]byte(assets.Env["LOOP_NOW_PLUGIN_CFG_CONFIG"]), &cfg)
	if cfg["url"] != "https://x" || cfg["channel"] != "#ops" || cfg["token"] != nil || cfg["key"] != nil {
		t.Fatalf("config variables: %v", assets.Env)
	}
	var mails []map[string]any
	_ = json.Unmarshal([]byte(assets.Env["LOOP_NOW_ACCOUNTS_MAIL"]), &mails)
	if assets.Env["LOOP_NOW_ACCOUNT_LARK_APPID"] != "cli_9" || assets.Env["LOOP_NOW_ACCOUNT_LARK_SECRET"] != "lark-secret" || len(mails) != 2 || mails[0]["name"] == nil {
		t.Fatalf("account variables: %v", assets.Env)
	}
	if err := e.st.DeletePluginAccount(ctx, e.project, "cfg", mail1.ID); err != nil {
		t.Fatal(err)
	}
	views, _ := e.svc.AgentPlugins(ctx, agent.ID)
	if len(views) != 1 || !views[0].Granted || !views[0].Available || len(views[0].AccountIDs) != 2 {
		t.Fatalf("deleting an account should remove it from grants: %+v", views)
	}
	// Disabled in the project: the grant is kept but no longer mounted, and new grants are rejected. Disabled
	// globally: nothing is mounted.
	if _, err := e.svc.PutProject(ctx, e.project, "cfg", plugins.ProjectInput{Enabled: false, Values: map[string]any{"channel": "#ops"}, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if a, _ := e.svc.AgentAssets(ctx, agent.ID); len(a.Extensions) != 0 {
		t.Fatalf("should not be mounted once disabled in the project: %+v", a)
	}
	if _, err := e.svc.PutAgentPlugin(ctx, agent.ID, "cfg", plugins.AgentInput{Granted: true}); code(err) != apperr.CodePluginNotEnabled {
		t.Fatalf("cannot grant while not enabled in the project: %v", err)
	}
	if a, _ := e.svc.AgentAssets(ctx, platform.ID); len(a.Extensions) != 1 {
		t.Fatalf("a platform Agent should still be mounted: %+v", a)
	}
	if _, err := e.svc.PutSettings(ctx, store.PluginSettings{Disabled: true}); err != nil {
		t.Fatal(err)
	}
	if a, _ := e.svc.AgentAssets(ctx, platform.ID); len(a.Extensions) != 0 || len(a.Env) != 0 {
		t.Fatalf("should not be mounted once disabled globally: %+v", a)
	}
}

// The plugins for the plugin host to load come in install order; each plugin gets its token issued only right
// before loading, and only its own old token is revoked.
func TestSpecsIssueTokensPerPlugin(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.install(plugins.Source{Kind: "local", Location: plugin(t, t.TempDir(), "first", "1.0.0")}, true)
	e.install(plugins.Source{Kind: "local", Location: plugin(t, t.TempDir(), "second", "1.0.0")}, true)
	specs, err := e.svc.Specs(ctx)
	if err != nil || len(specs) != 2 || specs[0].ID != "first" || specs[1].ID != "second" || !slices.Equal(specs[0].Permissions, []string{"agents.read", "state"}) {
		t.Fatalf("specs: %+v %v", specs, err)
	}
	load := func(i int) map[string]any {
		raw, err := specs[i].Prepare(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		return p
	}
	a, b := load(0), load(1)
	if a["entry"] != filepath.Join(a["root"].(string), "platform/index.ts") || a["apiUrl"] != e.svc.APIURL || !strings.HasPrefix(a["token"].(string), auth.PluginPrefix) {
		t.Fatalf("load payload: %v", a)
	}
	again := load(0)
	live := func(token string) bool {
		_, id, _, _ := auth.ParseToken(token)
		rec, err := e.st.LookupPluginToken(ctx, id)
		return err == nil && !rec.Revoked
	}
	if live(a["token"].(string)) || !live(again["token"].(string)) || !live(b["token"].(string)) {
		t.Fatal("reloading a plugin should revoke only its own old token")
	}
}
