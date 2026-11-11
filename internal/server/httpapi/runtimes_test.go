package httpapi_test

import (
	"testing"

	"loop-now/internal/server/runtimes"
	"loop-now/internal/testutil/fakeclaudebin"
)

// GET /runtimes 只给控制台：pi 与 claude-code 的状态按契约返回；refresh 只能是布尔值。
func TestListRuntimes(t *testing.T) {
	e := newEnv(t)
	home := t.TempDir()
	e.api.Runtimes = &runtimes.Checker{PiVersion: func() string { return "1.4.0" }, ClaudeBin: fakeclaudebin.Path(t), RealHome: func() (string, error) { return home, nil }}
	got := decode[struct {
		Runtimes []runtimes.Status `json:"runtimes"`
	}](t, e.expect(200, "GET", "/runtimes?refresh=true", nil))
	if len(got.Runtimes) != 2 || got.Runtimes[1].ID != "claude-code" || !got.Runtimes[1].Available || got.Runtimes[1].Login.State != "logged_in" {
		t.Fatalf("运行时：%+v", got)
	}
	e.expect(422, "GET", "/runtimes?refresh=maybe", nil, "X-Contract-Invalid-Request", "1")
}
