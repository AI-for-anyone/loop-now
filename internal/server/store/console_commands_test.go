package store_test

import (
	"testing"

	"loop-now/internal/server/store"
)

// /copy 取最近一条有文字的成功回复：没有文字的回复（Pi 直接处理的命令）和失败的回复都跳过，都没有时为 nil。
func TestLatestCompletedAssistantSkipsEmptyReplies(t *testing.T) {
	e := newRecEnv(t)
	reply := func(status, text string) {
		t.Helper()
		r := e.send(true, "")
		if _, _, ok, err := e.s.FinishRun(e.ctx, r.ID, store.RunEnd{Status: status, Text: text}); err != nil || !ok {
			t.Fatalf("结束执行：%v %v", ok, err)
		}
	}
	latest := func() *store.Message {
		t.Helper()
		m, err := e.s.LatestCompletedAssistant(e.ctx, e.agent)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	reply("completed", "")
	reply("completed", " \n\t")
	if m := latest(); m != nil {
		t.Fatalf("只有空回复时应为 nil：%+v", m)
	}
	reply("completed", "有内容的回复")
	reply("completed", "")
	reply("failed", "失败时的部分回复")
	if m := latest(); m == nil || m.Text != "有内容的回复" {
		t.Fatalf("应取最近一条有文字的成功回复：%+v", m)
	}
}
