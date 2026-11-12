package conversation_test

import (
	"testing"

	"loop-now/internal/server/conversation"
)

func TestPiCommandParse(t *testing.T) {
	for _, c := range []struct{ in, name, msg string }{
		{"/pi:grok-cli-accounts gui", "grok-cli-accounts", "/grok-cli-accounts gui"},
		{"/pi:skill:review 看看\n第二行", "skill:review", "/skill:review 看看\n第二行"},
		{"/pi:usage", "usage", "/usage"},
		{"/pi:", "", ""},
		{"/pi: x", "", ""},
		{"/usage", "", ""},
		{" /pi:usage", "", ""},
		{"你好 /pi:usage", "", ""},
	} {
		name, msg := conversation.PiCommand(c.in)
		if name != c.name || msg != c.msg {
			t.Errorf("%q → %q %q，期望 %q %q", c.in, name, msg, c.name, c.msg)
		}
	}
}

// 对话页的 /pi:<name>：保存与显示用原文，发给 Pi 的是 /<name>（不加运行 ID）；Pi 直接处理时这一轮 completed、没有回复、piCommand.handled；
// 不带 pi: 的 /xxx 照旧加运行 ID 交给 Agent。
func TestPiCommandRuns(t *testing.T) {
	e := newEnv(t)
	e.mustSend("/pi:handled-cmd gui", "")
	e.waitStarted("/handled-cmd gui")
	e.waitIdle()
	e.mustSend("/pi:skill:review 看看", "")
	e.waitStarted("/skill:review 看看")
	e.waitIdle()
	e.mustSend("/usage", "")
	e.waitStarted("/usage")
	e.waitIdle()
	h := e.history()
	if len(h) != 6 {
		t.Fatalf("历史：%+v", h)
	}
	if h[0].Text != "/pi:handled-cmd gui" || h[1].Text != "" || h[1].Run.Status != "completed" || h[1].Run.PiCommand == nil || h[1].Run.PiCommand.Name != "handled-cmd" || !h[1].Run.PiCommand.Handled {
		t.Fatalf("扩展命令：%+v %+v", h[0], h[1].Run)
	}
	if n := h[1].Run.PiCommand.Notices; len(n) != 2 || n[1].Level != "warning" || n[1].Message != "Weekly Limit\n   Used       1%" || !h[1].Run.PiCommand.Truncated {
		t.Fatalf("命令输出随执行记录保存：%+v", h[1].Run.PiCommand)
	}
	if h[3].Run.PiCommand != nil && (len(h[3].Run.PiCommand.Notices) != 0 || h[3].Run.PiCommand.Truncated) {
		t.Fatalf("没被直接处理的命令不收通知：%+v", h[3].Run.PiCommand)
	}
	if h[2].Text != "/pi:skill:review 看看" || h[3].Run.PiCommand == nil || h[3].Run.PiCommand.Name != "skill:review" || h[3].Run.PiCommand.Handled || h[3].Text != "回复：/skill:review 看看" {
		t.Fatalf("技能命令：%+v %+v", h[2], h[3].Run)
	}
	if h[5].Run.PiCommand != nil {
		t.Fatalf("不带 pi: 的不是 Pi 命令：%+v", h[5].Run)
	}
}
