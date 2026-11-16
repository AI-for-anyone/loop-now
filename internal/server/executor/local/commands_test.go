package local

import (
	"testing"
	"time"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
)

// After Pi starts, its command list is read and stored in the Agent directory (Pi is not started separately just to
// list commands); an extension command whose first character is / is handled by Pi itself, and the turn is completed,
// CommandHandled, with no reply; a /xxx that Pi does not know is passed to the model as usual.
func TestPiCommandsCachedAndHandled(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(func(s *agentconfig.Snapshot) {
		s.Plugins = agentconfig.PluginAssets{Extensions: []string{"/plugins/grokish/0.1.0/src/grok-accounts.ts"}, Skills: []string{"/plugins/grokish/0.1.0/skills/review"}}
	})
	if c, err := h.e.Commands(id); err != nil || c != nil {
		t.Fatalf("从没启动过：%+v %v", c, err)
	}
	if res, _ := h.exec(id, "hi"); res.Outcome != executor.OutcomeCompleted || res.CommandHandled {
		t.Fatalf("普通消息：%+v", res)
	}
	var cached *executor.PiCommands
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if c, err := h.e.Commands(id); err == nil && c != nil {
			cached = c
			break
		}
	}
	if cached == nil || cached.CapturedAt.IsZero() {
		t.Fatalf("命令列表：%+v", cached)
	}
	byName := map[string]executor.PiCommand{}
	for _, c := range cached.Commands {
		byName[c.Name] = c
	}
	ext, skill := byName["grok-accounts"], byName["skill:review"]
	if _, ok := byName["skill:loopctl"]; !ok {
		t.Fatalf("平台自己的技能也在：%+v", cached.Commands)
	}
	if ext.Name != "grok-accounts" || ext.Source != "extension" || ext.Path != "/plugins/grokish/0.1.0/src/grok-accounts.ts" || ext.Description == nil {
		t.Fatalf("扩展命令：%+v", ext)
	}
	if skill.Name != "skill:review" || skill.Source != "skill" {
		t.Fatalf("技能命令：%+v", skill)
	}
	res, _ := h.exec(id, "/grok-accounts gui")
	if res.Outcome != executor.OutcomeCompleted || !res.CommandHandled || res.Text != "" {
		t.Fatalf("扩展命令应由 Pi 直接处理：%+v", res)
	}
	// The command's output (notify) is handed over with the result, in order; setStatus does not count; multi-line text
	// is kept verbatim.
	want := []executor.CommandNotice{{Level: "info", Message: "Fetching grok-accounts…"}, {Level: "warning", Message: "Result of grok-accounts\n   Used       1%\n   Args       gui"}}
	if len(res.CommandNotices) != 2 || res.CommandNotices[0] != want[0] || res.CommandNotices[1] != want[1] || res.CommandNoticesTruncated {
		t.Fatalf("命令输出：%+v", res.CommandNotices)
	}
	if res, _ := h.exec(id, "/unknown x"); res.Outcome != executor.OutcomeCompleted || res.CommandHandled || res.Text == "" || len(res.CommandNotices) != 0 {
		t.Fatalf("不认识的命令交给模型：%+v", res)
	}
}
