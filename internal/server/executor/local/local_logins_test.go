package local

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

// fakeRealHome 建一个假的“用户真实主目录”，里面放一些与 Loop Now 自己的文件同名的东西，返回目录与其中全部文件的快照。
func fakeRealHome(t *testing.T) (string, map[string]string) {
	t.Helper()
	home := t.TempDir()
	files := map[string]string{
		"notes.txt":                          "用户自己的文件",
		".gitconfig":                         "[url \"file:///nonexistent-user-rewrite/\"]\n\tinsteadOf = file:///\n",
		".lark-cli/config.json":              `{"apps":[{"appId":"user-own"}]}`,
		".loop-now/gcloud/config/x":          "user",
		".loop-now/db/psql/connections.json": "[]",
		"home/keep":                          "same name as the agent home",
		"Library/Application Support/lark-cli/master.key.file": "user-key",
	}
	for rel, body := range files {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home, snapshotTree(t, home)
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			out[rel+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(p)
		out[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameTree(t *testing.T, root string, want map[string]string) {
	t.Helper()
	got := snapshotTree(t, root)
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Errorf("用户真实主目录里的 %s 被改动或删除：%q", k, g)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("用户真实主目录里多出了 %s", k)
		}
	}
}

// lookPath 按 PATH（dump 里的值）找命令，和 shell 的查找一致。
func lookPath(path, name string) string {
	for _, dir := range filepath.SplitList(path) {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// 用本机的登录状态：Pi 的 HOME 是用户真实的主目录，SSH_AUTH_SOCK 与 XDG_* 透传；平台目录不变；配了账号的受管 CLI 经小脚本
// 以 Agent 的独立 HOME 运行（环境与开关关着时一致），没配的 CLI 不被拦住；提示词提醒交互授权；开关变化即重启，关掉后环境复原、旧脚本清掉。
func TestLoginsEnvShimsAndPrompt(t *testing.T) {
	skipWithoutCLITemplates(t)
	realHome, before := fakeRealHome(t)
	userBin := t.TempDir()
	for _, name := range []string{"gh", "tea"} { // 用户本机装的 gh 与 tea
		if err := os.WriteFile(filepath.Join(userBin, name), []byte("#!/bin/sh\necho user-"+name+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// 执行基础 PATH 用空的临时目录：命令的解析结果不受宿主机上装没装 gh、tea 影响。
	base := strings.Join([]string{t.TempDir(), t.TempDir()}, string(os.PathListSeparator))
	h := newHarness(t, func(o *Options) {
		o.ContextDir = t.TempDir()
		o.ExecBasePath = base
		o.RealHome = func() (string, error) { return realHome, nil }
		o.UserPath = func(context.Context) []string { return []string{userBin} }
	}, "")
	dumpPath := filepath.Join(t.TempDir(), "dump.json")
	h.env["FAKEPI_DUMP"] = dumpPath
	h.env["SSH_AUTH_SOCK"] = "/tmp/ssh-agent.sock"
	h.env["XDG_CONFIG_HOME"] = filepath.Join(realHome, ".xdg-config")
	ghDir := preinstall(t, h.root, "gh", "2.100.0", "gh")
	// 受管 gh 打印它看到的 HOME 与登录状态才透传的变量。
	if err := os.WriteFile(filepath.Join(ghDir, "gh"), []byte("#!/bin/sh\nprintf '%s|%s|%s|%s' \"$HOME\" \"${XDG_CONFIG_HOME-unset}\" \"${SSH_AUTH_SOCK-unset}\" \"$*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	id := h.agent(func(s *agentconfig.Snapshot) {
		s.CLIs = []string{"gh"}
		s.CLIPayloads["gh"] = clitemplate.Payload{Env: map[string]string{"GH_TOKEN": "synthetic-gh"}}
		s.Local = agentconfig.LocalAccess{Logins: true, UserPath: true}
	})
	if res, _ := h.exec(id, "first"); res.Outcome != executor.OutcomeCompleted {
		t.Fatal(res)
	}
	d := readDump(t, dumpPath)
	dirs := h.e.dirs(id)
	for k, v := range map[string]string{
		"HOME": realHome, "SSH_AUTH_SOCK": "/tmp/ssh-agent.sock", "XDG_CONFIG_HOME": filepath.Join(realHome, ".xdg-config"),
		"TMPDIR": dirs.tmp, "PI_CODING_AGENT_DIR": dirs.pi, "GH_TOKEN": "synthetic-gh", "GIT_TERMINAL_PROMPT": "0",
	} {
		if d.Env[k] != v {
			t.Errorf("%s = %q，应为 %q", k, d.Env[k], v)
		}
	}
	shims := filepath.Join(dirs.root, cliShimDir)
	if path := d.Env["PATH"]; path != strings.Join([]string{shims, base, userBin}, string(os.PathListSeparator)) {
		t.Fatalf("PATH 应为小脚本目录、执行 PATH、用户 PATH（不再直接放模板目录）：%s", path)
	}
	// 配了账号的 gh 找到的是小脚本；它以 Agent 的独立 HOME、开关关着时的环境运行原来的 gh。
	gh := lookPath(d.Env["PATH"], "gh")
	if gh != filepath.Join(shims, "gh") {
		t.Fatalf("gh 应解析到小脚本：%s", gh)
	}
	if fi, err := os.Stat(shims); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("小脚本目录应只有自己可访问：%v %v", fi, err)
	}
	cmd := exec.Command(gh, "auth", "status")
	for k, v := range d.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.Output()
	if err != nil || string(out) != dirs.home+"|unset|unset|auth status" {
		t.Fatalf("受管 gh 的环境不对：%q %v", out, err)
	}
	// 没配的 tea 按 PATH 落到用户本机那份。
	if tea := lookPath(d.Env["PATH"], "tea"); tea != filepath.Join(userBin, "tea") {
		t.Fatalf("没配的 CLI 应落到用户本机：%s", tea)
	}
	if !strings.Contains(d.SystemPrompt, "Local logins: your HOME is the user's own home directory "+realHome) || !strings.Contains(d.SystemPrompt, "do not keep retrying") {
		t.Fatalf("提示词应提醒本机登录与交互授权：\n%s", d.SystemPrompt)
	}
	pid := h.pid(id)

	// 关掉：下一次派发重启，环境与原来一致，旧脚本清掉，不再提醒。
	h.src.mu.Lock()
	h.src.snaps[id].Local.Logins = false
	h.src.mu.Unlock()
	if res, _ := h.exec(id, "second"); res.Outcome != executor.OutcomeCompleted || h.pid(id) == pid {
		t.Fatal("登录状态开关变化应在下一次派发重启 Pi")
	}
	d = readDump(t, dumpPath)
	if d.Env["HOME"] != dirs.home || d.Env["SSH_AUTH_SOCK"] != "" || d.Env["XDG_CONFIG_HOME"] != "" || strings.Contains(d.SystemPrompt, "Local logins") {
		t.Fatalf("关掉后应复原：HOME=%s SSH=%s XDG=%s", d.Env["HOME"], d.Env["SSH_AUTH_SOCK"], d.Env["XDG_CONFIG_HOME"])
	}
	if path := d.Env["PATH"]; path != strings.Join([]string{ghDir, base, userBin}, string(os.PathListSeparator)) {
		t.Fatalf("关掉后 PATH 应回到模板目录：%s", path)
	}
	if _, err := os.Stat(shims); !os.IsNotExist(err) {
		t.Fatal("关掉后不应留下旧脚本")
	}
	sameTree(t, realHome, before)
}

// 只有这个 Agent 配了账号的受管 CLI 才生成小脚本：没配 gh 的 Agent 不拦 gh，按 PATH 落到用户本机的 gh 并用真实 HOME。
func TestLoginsDoNotShimUnconfiguredCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("小脚本只在 Unix 上")
	}
	realHome, _ := fakeRealHome(t)
	userBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(userBin, "gh"), []byte("#!/bin/sh\necho user-gh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir() // 空的执行基础 PATH：宿主机上装的 gh（例如 CI runner 的 /usr/bin/gh）不会先命中
	h := newHarness(t, func(o *Options) {
		o.ExecBasePath = base
		o.RealHome = func() (string, error) { return realHome, nil }
		o.UserPath = func(context.Context) []string { return []string{userBin} }
	}, "")
	dumpPath := filepath.Join(t.TempDir(), "dump.json")
	h.env["FAKEPI_DUMP"] = dumpPath
	preinstall(t, h.root, "gh", "2.100.0", "gh") // 装过，但这个 Agent 没配
	id := h.agent(func(s *agentconfig.Snapshot) { s.Local = agentconfig.LocalAccess{Logins: true, UserPath: true} })
	if res, _ := h.exec(id, "hi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatal(res)
	}
	d := readDump(t, dumpPath)
	if path := d.Env["PATH"]; path != base+string(os.PathListSeparator)+userBin {
		t.Fatalf("没有受管 CLI 时 PATH 只有执行 PATH 与用户 PATH，不含小脚本或模板目录：%s", path)
	}
	if gh := lookPath(d.Env["PATH"], "gh"); gh != filepath.Join(userBin, "gh") || d.Env["HOME"] != realHome {
		t.Fatalf("没配的 gh 应落到用户本机并用真实 HOME：%s HOME=%s", gh, d.Env["HOME"])
	}
	if _, err := os.Stat(filepath.Join(h.e.dirs(id).root, cliShimDir)); !os.IsNotExist(err) {
		t.Fatal("没有受管 CLI 时不应有小脚本目录")
	}
}

// 开关开着跑完整的启动—释放—CleanupStale—删除 Agent：清理只认 Agent 自己的目录，用户真实主目录原样不动；
// 平台替 Agent 做的克隆（旧文件夹来源）不读用户的 git 配置。
func TestLoginsNeverTouchRealHome(t *testing.T) {
	skipWithoutCLITemplates(t)
	realHome, before := fakeRealHome(t)
	h := newHarness(t, func(o *Options) {
		o.ContextDir = t.TempDir()
		o.RealHome = func() (string, error) { return realHome, nil }
	}, "")
	dumpPath := filepath.Join(t.TempDir(), "dump.json")
	h.env["FAKEPI_DUMP"] = dumpPath
	preinstall(t, h.root, "lark-cli", "1.0.94", "lark-cli")
	bare := gitRepo(t)
	id := h.agent(func(s *agentconfig.Snapshot) {
		s.Local = agentconfig.LocalAccess{Logins: true}
		s.CLIs = []string{"lark-cli"}
		s.CLIPayloads["lark-cli"] = clitemplate.Payload{Files: []clitemplate.PayloadFile{{Path: ".lark-cli/config.json", Content: []byte(`{"apps":[{"appId":"agent"}]}`)}}}
		// 用户的 ~/.gitconfig 把 file:/// 改写到不存在的地方：克隆若读了它就会失败。
		s.Sources = []store.FolderSource{{ID: uuid.Must(uuid.NewV7()), Path: "app", Kind: "git", GitURL: "file://" + bare, GitBranch: "main"}}
	})
	if res, _ := h.exec(id, "hi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("启动应成功（克隆不读用户的 git 配置）：%+v", res)
	}
	dirs := h.e.dirs(id)
	if d := readDump(t, dumpPath); d.Env["HOME"] != realHome {
		t.Fatalf("HOME = %s", d.Env["HOME"])
	}
	if b, err := os.ReadFile(filepath.Join(dirs.home, ".lark-cli", "config.json")); err != nil || !strings.Contains(string(b), `"agent"`) {
		t.Fatalf("受管 CLI 的凭证文件应写在 Agent 的独立 HOME：%q %v", b, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dirs.root, cliShimDir, "lark-cli")); !strings.Contains(string(b), "HOME="+shellQuote(dirs.home)) {
		t.Fatalf("lark-cli 的小脚本应恢复独立 HOME：%s", b)
	}
	// 释放（删除 Agent 时调用）与启动清理：只删 Agent 自己的目录。
	if err := h.e.Release(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	h.src.mu.Lock()
	h.src.deleted = append(h.src.deleted, id)
	h.src.mu.Unlock()
	if err := h.e.CleanupStale(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dirs.home); !os.IsNotExist(err) {
		t.Fatalf("Agent 的独立 HOME 应已回收：%v", err)
	}
	sameTree(t, realHome, before)
	if !slices.Contains(h.src.deleted, id) {
		t.Fatal("unreachable")
	}
}

// buildEnv：开关关着时与不传 loginHome 完全相同；开着时只换 HOME 一组变量并透传 SSH_AUTH_SOCK／XDG_*，
// Windows 上 USERPROFILE 等按真实主目录、APPDATA／LOCALAPPDATA 取进程环境的真实值，TEMP 仍是 Agent 的。
func TestLoginsBuildEnv(t *testing.T) {
	real := filepath.Join(t.TempDir(), "real-home")
	env := map[string]string{"SSH_AUTH_SOCK": "/tmp/agent.sock", "XDG_CACHE_HOME": filepath.Join(real, "cache"), "APPDATA": `D:\Roaming`, "LOCALAPPDATA": `D:\Local`, "LANG": "C"}
	tmpls, err := clitemplate.Load("")
	if err != nil {
		t.Fatal(err)
	}
	e := &Executor{tmpl: tmpls, o: Options{ExecBasePath: "/usr/bin", Username: "u", WorkRoot: t.TempDir(), Passthrough: []string{"LANG"}, Getenv: func(k string) string { return env[k] }}}
	d := e.dirs([16]byte{2})
	snap := agentconfig.Snapshot{}
	asMap := func(list []string) map[string]string {
		m := map[string]string{}
		for _, kv := range list {
			k, v, _ := strings.Cut(kv, "=")
			m[k] = v
		}
		return m
	}
	log := slog.New(slog.DiscardHandler)
	off := asMap(e.buildEnv(d, snap, nil, nil, "", log))
	on := asMap(e.buildEnv(d, snap, nil, nil, real, log))
	if off["HOME"] != d.home || off["SSH_AUTH_SOCK"] != "" || off["XDG_CACHE_HOME"] != "" {
		t.Fatalf("关着时：%v", off)
	}
	if on["HOME"] != real || on["SSH_AUTH_SOCK"] != "/tmp/agent.sock" || on["XDG_CACHE_HOME"] != env["XDG_CACHE_HOME"] || on["TMPDIR"] != d.tmp || on["PI_CODING_AGENT_DIR"] != d.pi {
		t.Fatalf("开着时：%v", on)
	}
	changed := []string{"HOME", "SSH_AUTH_SOCK", "XDG_CACHE_HOME"}
	if runtime.GOOS == "windows" {
		if on["USERPROFILE"] != real || on["APPDATA"] != `D:\Roaming` || on["LOCALAPPDATA"] != `D:\Local` || on["TEMP"] != d.tmp || on["TMP"] != d.tmp {
			t.Fatalf("Windows 上的 HOME 一组变量：%v", on)
		}
		changed = append(changed, "USERPROFILE", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA")
	}
	// 除了这一组，其余变量与关着时逐个相同。
	for k, v := range on {
		if !slices.Contains(changed, k) && off[k] != v {
			t.Errorf("%s 不应随开关变化：%q → %q", k, off[k], v)
		}
	}
	if got := e.loginOnlyEnv(); slices.Contains(got, "LANG") || !slices.Contains(got, "SSH_AUTH_SOCK") {
		t.Fatalf("小脚本要去掉的变量：%v", got)
	}
}
