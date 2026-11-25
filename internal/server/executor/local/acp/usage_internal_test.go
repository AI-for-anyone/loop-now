package acp

import (
	"encoding/json"
	"testing"
)

func n(v int64) *int64 { return &v }

func sameUsage(a, b Usage) bool {
	eq := func(x, y *int64) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
	return a.StopReason == b.StopReason && a.Input == b.Input && a.Output == b.Output && a.CacheRead == b.CacheRead &&
		a.CacheWrite == b.CacheWrite && eq(a.Total, b.Total) && eq(a.Thought, b.Thought) && a.Model == b.Model
}

// A prompt's usage is read field by field from the result's usage (ACP), else from its _meta.usage (Grok), in either
// naming; a field of the wrong type or a negative or fractional count is left out and the others are still used.
// Without any count, or with every count zero, there is no usage.
func TestParseUsage(t *testing.T) {
	for _, c := range []struct {
		name, result string
		want         Usage
		ok           bool
	}{
		{"agy 1.5.14", `{"stopReason":"end_turn","usage":{"inputTokens":10997,"outputTokens":1,"cachedReadTokens":0,"cachedWriteTokens":0,"thoughtTokens":0,"totalTokens":10998}}`,
			Usage{StopReason: "end_turn", Input: 10997, Output: 1, Total: n(10998), Thought: n(0)}, true},
		{"grok 1.0.92", `{"stopReason":"end_turn","_meta":{"usage":{"inputTokens":19319,"outputTokens":31,"totalTokens":19350,"cachedReadTokens":2176,"cacheCreationTokens":0,"reasoningTokens":30,"modelCalls":1,"apiDurationMs":3111,"costUsdTicks":355600000,"modelUsage":{"grok-4.7-build":{"inputTokens":19319,"outputTokens":31}}},"totalTokens":19350,"modelId":"grok-4.7","inputTokens":19319}}`,
			Usage{StopReason: "end_turn", Input: 19319, Output: 31, CacheRead: 2176, Total: n(19350), Thought: n(30), Model: "grok-4.7-build"}, true},
		{"several models: the model in _meta", `{"stopReason":"end_turn","_meta":{"modelId":"grok-4.7","usage":{"inputTokens":5,"outputTokens":1,"modelUsage":{"a":{},"b":{}}}}}`,
			Usage{StopReason: "end_turn", Input: 5, Output: 1, Model: "grok-4.7"}, true},
		{"the ACP field first", `{"stopReason":"end_turn","usage":{"inputTokens":7,"outputTokens":2,"cachedWriteTokens":3},"_meta":{"usage":{"inputTokens":999,"outputTokens":999}}}`,
			Usage{StopReason: "end_turn", Input: 7, Output: 2, CacheWrite: 3}, true},
		{"an empty ACP field: _meta", `{"stopReason":"end_turn","usage":null,"_meta":{"usage":{"inputTokens":4,"cacheCreationTokens":2}}}`,
			Usage{StopReason: "end_turn", Input: 4, CacheWrite: 2}, true},
		{"bad fields are left out", `{"stopReason":7,"usage":{"inputTokens":"10","outputTokens":12.0,"cachedReadTokens":-1,"thoughtTokens":1.5,"totalTokens":20,"cachedWriteTokens":{}}}`,
			Usage{Output: 12, Total: n(20)}, true},
		{"_meta not an object", `{"stopReason":"end_turn","_meta":"x","usage":{"outputTokens":3}}`, Usage{StopReason: "end_turn", Output: 3}, true},
		{"all zero", `{"stopReason":"cancelled","usage":{"inputTokens":0,"outputTokens":0,"totalTokens":0}}`, Usage{}, false},
		{"no counts", `{"stopReason":"end_turn","usage":{"modelCalls":1},"_meta":{"totalTokens":5}}`, Usage{}, false},
		{"none", `{"stopReason":"end_turn"}`, Usage{}, false},
		{"not an object", `[1]`, Usage{}, false},
	} {
		got, ok := parseUsage(json.RawMessage(c.result))
		if ok != c.ok || (ok && !sameUsage(got, c.want)) {
			t.Errorf("%s: got %+v %v, want %+v %v", c.name, got, ok, c.want, c.ok)
		}
	}
}
