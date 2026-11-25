package acp

import (
	"encoding/json"
	"math"
)

// Usage is the token usage an agent reports for one prompt, as it reports it: the prompt result's usage (the ACP field,
// still unstable: "token usage for this turn"), else the result's _meta.usage (Grok). What the counts include differs
// between agents (whether the input count has the cached part), so they are passed on as they are.
type Usage struct {
	// StopReason is the prompt's stop reason.
	StopReason string
	// Counts the agent leaves out are 0; Total and Thought are nil then.
	Input, Output, CacheRead, CacheWrite int64
	Total, Thought                       *int64
	// Model is the model the agent says it used: the one model of Grok's modelUsage, else its _meta.modelId; empty
	// when it says none.
	Model string
}

// Observer receives the usage an agent reports for each prompt (during the prompt, before it ends). Calls must return
// quickly.
type Observer interface {
	Usage(u Usage)
}

// SetObserver sets the observer of the usage the agent reports.
func (c *Client) SetObserver(o Observer) {
	c.mu.Lock()
	c.observer = o
	c.mu.Unlock()
}

// observe passes on the usage of a prompt's result, when it has any.
func (c *Client) observe(result json.RawMessage) {
	var meta struct {
		Meta struct {
			PromptID string `json:"promptId"`
		} `json:"_meta"`
	}
	if json.Unmarshal(result, &meta) == nil {
		c.ownPrompt(meta.Meta.PromptID)
	}
	u, ok := parseUsage(result)
	if !ok {
		return
	}
	c.mu.Lock()
	o := c.observer
	c.mu.Unlock()
	if o != nil {
		o.Usage(u)
	}
}

// parseUsage reads the usage of a prompt's result field by field: from its usage (ACP names: cachedWriteTokens,
// thoughtTokens), else from its _meta.usage (Grok's: cacheCreationTokens, reasoningTokens; either naming is read in
// both). A field of the wrong type, or a negative or fractional count, is left out and the others are still used. There
// is no usage when neither has a count, or when every count is zero (that says nothing about what the prompt used).
func parseUsage(result json.RawMessage) (Usage, bool) {
	var r map[string]json.RawMessage
	if json.Unmarshal(result, &r) != nil {
		return Usage{}, false
	}
	meta := object(r["_meta"])
	fields := object(r["usage"])
	if !hasCount(fields) {
		fields = object(meta["usage"])
	}
	if !hasCount(fields) {
		return Usage{}, false
	}
	var u Usage
	_ = json.Unmarshal(r["stopReason"], &u.StopReason)
	u.Input, _ = count(fields, "inputTokens")
	u.Output, _ = count(fields, "outputTokens")
	u.CacheRead, _ = count(fields, "cachedReadTokens")
	u.CacheWrite, _ = count(fields, "cachedWriteTokens", "cacheCreationTokens")
	if v, ok := count(fields, "totalTokens"); ok {
		u.Total = &v
	}
	if v, ok := count(fields, "thoughtTokens", "reasoningTokens"); ok {
		u.Thought = &v
	}
	if u.Input+u.Output+u.CacheRead+u.CacheWrite == 0 && (u.Total == nil || *u.Total == 0) {
		return Usage{}, false
	}
	if models := object(fields["modelUsage"]); len(models) == 1 {
		for name := range models {
			u.Model = name
		}
	}
	if u.Model == "" {
		_ = json.Unmarshal(meta["modelId"], &u.Model)
	}
	return u, true
}

// object decodes a JSON object into its fields; anything else is no fields.
func object(raw json.RawMessage) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m)
	return m
}

// hasCount reports whether fields has an input, output or total count.
func hasCount(fields map[string]json.RawMessage) bool {
	for _, name := range []string{"inputTokens", "outputTokens", "totalTokens"} {
		if _, ok := count(fields, name); ok {
			return true
		}
	}
	return false
}

// count is the first of names that holds a token count (a whole number, not negative).
func count(fields map[string]json.RawMessage, names ...string) (int64, bool) {
	for _, name := range names {
		raw := fields[name]
		var f float64
		if string(raw) == "null" || json.Unmarshal(raw, &f) != nil || f < 0 || f != math.Trunc(f) || f >= math.MaxInt64 {
			continue
		}
		return int64(f), true
	}
	return 0, false
}
