package runtimes_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"loop-now/internal/server/runtimes"
	"loop-now/internal/testutil/fakeclaudebin"
)

// pi 总是可用；claude-code 报告 claude 的路径、版本与登录状态（只有是否登录、方式与订阅类型，没有邮箱与组织）；结果缓存，refresh 时重查；
// 找不到 claude 时 not_found、login 为 null。
func TestChecker(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	c := &runtimes.Checker{PiVersion: func() string { return "1.4.0" }, ClaudeBin: fakeclaudebin.Path(t), RealHome: func() (string, error) { return home, nil },
		ExecBasePath: "/usr/bin:/bin", Now: func() time.Time { return now }}
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
	missing := &runtimes.Checker{PiVersion: func() string { return "1.4.0" }, UserPath: func(context.Context) []string { return []string{t.TempDir()} },
		RealHome: func() (string, error) { return home, nil }}
	if cc := missing.List(context.Background(), true)[1]; cc.Available || cc.UnavailableReason == nil || *cc.UnavailableReason != "not_found" || cc.Login != nil || cc.BinaryPath != nil {
		t.Fatalf("找不到 claude：%+v", cc)
	}
}
