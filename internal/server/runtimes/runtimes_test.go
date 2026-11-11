package runtimes_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"loop-now/internal/server/claudecli"
	"loop-now/internal/server/runtimes"
	"loop-now/internal/testutil/fakeclaudebin"
)

// pi 总是可用；claude-code 报告 claude 的路径、版本与登录状态（只有是否登录、方式与订阅类型，没有邮箱与组织）；结果缓存，refresh 时重查；
// 找不到 claude 时 not_found、login 为 null。
func TestChecker(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	helperEnv := func(_ context.Context, home string) []string {
		return claudecli.Env(claudecli.EnvOptions{Home: home, User: "tester", Path: []string{"/usr/bin", "/bin"}})
	}
	c := &runtimes.Checker{PiVersion: func() string { return "1.4.0" }, ClaudeBin: fakeclaudebin.Path(t), RealHome: func() (string, error) { return home, nil },
		ExecBasePath: "/usr/bin:/bin", HelperEnv: helperEnv, Now: func() time.Time { return now }}
	list := c.List(context.Background(), false)
	if len(list) != 2 || list[0].ID != "pi" || !list[0].Available || *list[0].Version != "1.4.0" || list[0].Login != nil {
		t.Fatalf("pi：%+v", list)
	}
	cc := list[1]
	if cc.ID != "claude-code" || !cc.Available || *cc.Version != "9.9.9" || *cc.BinaryPath != c.ClaudeBin || cc.Login == nil || cc.Login.State != "logged_in" ||
		*cc.Login.AuthMethod != "claude.ai" || *cc.Login.SubscriptionType != "max" || cc.UnavailableReason != nil {
		t.Fatalf("claude-code：%+v %+v", cc, cc.Login)
	}
	b, _ := json.Marshal(list)
	for _, secret := range []string{"private@example.com", "Private Org", "org-private-id"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("不返回 %q", secret)
		}
	}
	_ = os.WriteFile(filepath.Join(home, ".fakeclaude-logged-out"), nil, 0o600)
	if got := c.List(context.Background(), false)[1].Login.State; got != "logged_in" {
		t.Fatalf("缓存时长内用缓存：%s", got)
	}
	if got := c.List(context.Background(), true)[1].Login.State; got != "logged_out" {
		t.Fatalf("refresh 重查：%s", got)
	}
	// 辅助环境没有 USER 时 claude 答未登录（以 1 退出）：是 logged_out，不是 unknown。
	noUser := &runtimes.Checker{PiVersion: func() string { return "1.4.0" }, ClaudeBin: c.ClaudeBin, RealHome: func() (string, error) { return t.TempDir(), nil }, ExecBasePath: "/usr/bin:/bin"}
	if got := noUser.List(context.Background(), true)[1].Login; got == nil || got.State != "logged_out" {
		t.Fatalf("未登录应为 logged_out：%+v", got)
	}
	missing := &runtimes.Checker{PiVersion: func() string { return "1.4.0" }, UserPath: func(context.Context) []string { return []string{t.TempDir()} },
		RealHome: func() (string, error) { return home, nil }}
	if cc := missing.List(context.Background(), true)[1]; cc.Available || cc.UnavailableReason == nil || *cc.UnavailableReason != "not_found" || cc.Login != nil || cc.BinaryPath != nil {
		t.Fatalf("找不到 claude：%+v", cc)
	}
}
