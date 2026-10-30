package main

import "testing"

// 默认模式照旧回显；中性模式去掉“本轮运行ID”一行与指令词、保留换行，用英文前缀，!text 两种模式都优先。
func TestReply(t *testing.T) {
	const runLine = "本轮运行ID：0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b。\n"
	cases := []struct {
		msg           string
		normal, neutr string
	}{
		{runLine + "Summarize the deploy notes", "echo: " + runLine + "Summarize the deploy notes", "Got it: Summarize the deploy notes"},
		{runLine + "Check the build !tool=bash\nthen report back", "echo: 本轮运行ID：0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b。 Check the build then report back", "Got it: Check the build\nthen report back"},
		{runLine + "!tool=bash !sleep=10ms", "echo: 本轮运行ID：0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b。", "Got it."},
		{runLine + "!text=Deployed_to_staging. !tool=bash", "Deployed to staging.", "Deployed to staging."},
	}
	for _, c := range cases {
		for _, mode := range []bool{false, true} {
			neutral = mode
			want := c.normal
			if mode {
				want = c.neutr
			}
			if got := parseDirectives(c.msg).reply(c.msg); got != want {
				t.Errorf("neutral=%v %q：%q，期望 %q", mode, c.msg, got, want)
			}
		}
	}
	neutral = false
	if say("问", "asks") != "问" {
		t.Fatal("默认模式用中文")
	}
	neutral = true
	defer func() { neutral = false }()
	if say("问", "asks") != "asks" {
		t.Fatal("中性模式用英文")
	}
}
