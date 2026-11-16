package main

import "testing"

// The default mode echoes as before; neutral mode removes the run ID line the platform adds (the English one, and the
// Chinese one in old sessions) and the directive words, keeps newlines and uses an English prefix; !text wins in both
// modes.
func TestReply(t *testing.T) {
	const runLine = "Run ID for this turn: 0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b.\n"
	const legacyLine = "本轮运行ID：0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b。\n" // check-english: zh (the run ID line older sessions start with)
	cases := []struct {
		msg           string
		normal, neutr string
	}{
		{runLine + "Summarize the deploy notes", "echo: " + runLine + "Summarize the deploy notes", "Got it: Summarize the deploy notes"},
		{runLine + "Check the build !tool=bash\nthen report back", "echo: Run ID for this turn: 0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b. Check the build then report back", "Got it: Check the build\nthen report back"},
		{runLine + "!tool=bash !sleep=10ms", "echo: Run ID for this turn: 0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b.", "Got it."},
		{runLine + "!text=Deployed_to_staging. !tool=bash", "Deployed to staging.", "Deployed to staging."},
		{legacyLine + "Summarize the deploy notes", "echo: " + legacyLine + "Summarize the deploy notes", "Got it: Summarize the deploy notes"},
	}
	for _, c := range cases {
		for _, mode := range []bool{false, true} {
			neutral = mode
			want := c.normal
			if mode {
				want = c.neutr
			}
			if got := parseDirectives(c.msg).reply(c.msg); got != want {
				t.Errorf("neutral=%v %q: got %q, want %q", mode, c.msg, got, want)
			}
		}
	}
	neutral = false
}
