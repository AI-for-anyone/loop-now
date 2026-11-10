package local

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
)

// makeState 建一个订阅账号的状态版本目录：auth.json 与扩展自己的状态。
func makeState(t *testing.T, root string) (*agentconfig.ProviderState, string) {
	t.Helper()
	v := uuid.New()
	dir := filepath.Join(root, v.String())
	if err := os.MkdirAll(filepath.Join(dir, ".pi", "fake-oauth"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"fake-oauth":{"type":"oauth","access":"m","refresh":"m","expires":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".pi", "fake-oauth", "accounts.json"), []byte(`{"token":"in-state"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return &agentconfig.ProviderState{PluginID: "grokish", VersionID: v, Dir: dir, Paths: []string{".pi/fake-oauth"}}, dir
}

func readLink(t *testing.T, p string) string {
	t.Helper()
	got, err := os.Readlink(p)
	if err != nil {
		t.Fatalf("%s 应是链接：%v", p, err)
	}
	return got
}

// 订阅账号的状态：auth.json 与声明的路径链接到账号的状态目录，两个 Agent 共用一份；换掉后链接清干净、状态目录不动；
// 没挂载插件或状态目录不在时明确失败；释放 Agent 不会经链接删掉状态。
func TestProviderStateLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上是目录联接与硬链接，另行验证")
	}
	h := newHarness(t, nil, "")
	state, dir := makeState(t, t.TempDir())
	withState := func(s *agentconfig.Snapshot) {
		st := *state
		s.ProviderState = &st
		s.Plugins = agentconfig.PluginAssets{IDs: []string{"grokish"}}
	}
	a, b := h.agent(withState), h.agent(withState)
	for _, id := range []uuid.UUID{a, b} {
		if res, _ := h.exec(id, "hi"); res.Outcome != executor.OutcomeCompleted {
			t.Fatalf("启动：%+v", res)
		}
		d := h.e.dirs(id)
		if got := readLink(t, filepath.Join(d.pi, "auth.json")); got != filepath.Join(dir, "auth.json") {
			t.Fatalf("auth.json 链接：%s", got)
		}
		if got := readLink(t, filepath.Join(d.home, ".pi", "fake-oauth")); got != filepath.Join(dir, ".pi", "fake-oauth") {
			t.Fatalf("状态路径链接：%s", got)
		}
	}
	// 经一个 Agent 的 HOME 写入，另一个 Agent 与状态目录都看得到（扩展自己刷新的令牌由同项目的 Agent 共用）。
	if err := os.WriteFile(filepath.Join(h.e.dirs(a).home, ".pi", "fake-oauth", "refreshed.json"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(h.e.dirs(b).home, ".pi", "fake-oauth", "refreshed.json")); string(got) != "new" {
		t.Fatalf("共用一份状态：%q", got)
	}
	if !h.e.ProviderStatesInUse()[state.VersionID] {
		t.Fatal("运行中的 Pi 在用的版本要报给清理器")
	}

	// 换成不用订阅账号：重启，链接清掉，auth.json 回到平台自己写的普通文件，状态目录原样。
	h.src.update(a, func(s *agentconfig.Snapshot) { s.ProviderState, s.Plugins = nil, agentconfig.PluginAssets{} })
	if res, _ := h.exec(a, "hi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("换掉后：%+v", res)
	}
	d := h.e.dirs(a)
	if info, err := os.Lstat(filepath.Join(d.pi, "auth.json")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("auth.json 应是普通文件：%v %v", info, err)
	}
	if _, err := os.Lstat(filepath.Join(d.home, ".pi", "fake-oauth")); !os.IsNotExist(err) {
		t.Fatalf("旧链接应已删掉：%v", err)
	}
	if _, err := os.Stat(filepath.Join(d.root, providerLinksFile)); !os.IsNotExist(err) {
		t.Fatal("链接记录应已删掉")
	}

	// 释放 Agent：只删它自己的目录，状态目录里的东西都还在。
	if err := h.e.Release(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"auth.json", ".pi/fake-oauth/accounts.json", ".pi/fake-oauth/refreshed.json"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			t.Fatalf("释放 Agent 不能动状态目录：%s %v", f, err)
		}
	}

	// 没挂载插件：明确失败。
	c := h.agent(func(s *agentconfig.Snapshot) { st := *state; s.ProviderState = &st })
	if res, _ := h.exec(c, "hi"); res.Outcome != executor.OutcomeFailed || res.ErrorCode != executor.ErrCredentialUnavailable || !strings.Contains(res.ErrorMessage, "grokish") {
		t.Fatalf("没挂载插件：%+v", res)
	}
	// 状态目录不在：明确失败。
	e := h.agent(func(s *agentconfig.Snapshot) {
		st := *state
		st.Dir = filepath.Join(t.TempDir(), "gone")
		s.ProviderState, s.Plugins = &st, agentconfig.PluginAssets{IDs: []string{"grokish"}}
	})
	if res, _ := h.exec(e, "hi"); res.Outcome != executor.OutcomeFailed || res.ErrorCode != executor.ErrCredentialUnavailable || !strings.Contains(res.ErrorMessage, "重新登录") {
		t.Fatalf("状态目录不在：%+v", res)
	}
	// 声明的路径登录时还没写：在状态目录里建空目录再链接。
	f := h.agent(func(s *agentconfig.Snapshot) {
		st := *state
		st.Paths = []string{".config/fake-later"}
		s.ProviderState, s.Plugins = &st, agentconfig.PluginAssets{IDs: []string{"grokish"}}
	})
	if res, _ := h.exec(f, "hi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("还没写的路径：%+v", res)
	}
	if info, err := os.Stat(filepath.Join(dir, ".config", "fake-later")); err != nil || !info.IsDir() {
		t.Fatalf("应在状态目录里建空目录：%v", err)
	}
}

// 开着“用本机的登录状态”：不在用户真实家目录里建任何链接、不创建也不删除；声明的路径在那里不存在时启动前就失败；
// 存在时启动，用的是本机自己的状态，auth.json 仍链接到账号。
func TestProviderStateWithLocalLogins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上另行验证")
	}
	realHome, before := fakeRealHome(t)
	h := newHarness(t, func(o *Options) { o.RealHome = func() (string, error) { return realHome, nil } }, "")
	state, dir := makeState(t, t.TempDir())
	id := h.agent(func(s *agentconfig.Snapshot) {
		s.Local = agentconfig.LocalAccess{Logins: true}
		s.ProviderState, s.Plugins = state, agentconfig.PluginAssets{IDs: []string{"grokish"}}
	})
	res, _ := h.exec(id, "hi")
	if res.Outcome != executor.OutcomeFailed || res.ErrorCode != executor.ErrCredentialUnavailable || !strings.Contains(res.ErrorMessage, "用本机的登录状态") || !strings.Contains(res.ErrorMessage, ".pi/fake-oauth") {
		t.Fatalf("本机没有该扩展的登录：%+v", res)
	}
	sameTree(t, realHome, before)

	own := filepath.Join(realHome, ".pi", "fake-oauth")
	if err := os.MkdirAll(own, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(own, "accounts.json"), []byte("users-own"), 0o600); err != nil {
		t.Fatal(err)
	}
	before = snapshotTree(t, realHome)
	if res, _ := h.exec(id, "hi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("本机有登录时启动：%+v", res)
	}
	d := h.e.dirs(id)
	if got := readLink(t, filepath.Join(d.pi, "auth.json")); got != filepath.Join(dir, "auth.json") {
		t.Fatalf("auth.json 仍链接到账号：%s", got)
	}
	if _, err := os.Lstat(filepath.Join(d.home, ".pi", "fake-oauth")); !os.IsNotExist(err) {
		t.Fatalf("开关开着时不在 Agent HOME 里建状态链接：%v", err)
	}
	if err := h.e.Release(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	sameTree(t, realHome, before)
}
