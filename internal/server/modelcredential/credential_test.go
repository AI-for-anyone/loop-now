package modelcredential

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestCredentialSecretBoundaries(t *testing.T) {
	raw := []byte(`{"type":"oauth","access":"fixture-access-sentinel","refresh":"fixture-refresh-sentinel","expires":1900000000000,"accountId":"original-account"}`)
	c, e := Decode(raw)
	if e != nil {
		t.Fatal(e)
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		if strings.Contains(fmt.Sprintf(format, c), "sentinel") {
			t.Fatal("implicit secret print")
		}
	}
	if _, e = json.Marshal(c); e == nil {
		t.Fatal("implicit secret JSON")
	}
	file, e := c.AuthFile("openai-codex")
	if e != nil {
		t.Fatal(e)
	}
	var entries map[string]json.RawMessage
	if e = json.Unmarshal(file, &entries); e != nil {
		t.Fatal(e)
	}
	copy, e := Decode(entries["openai-codex"])
	if e != nil || !c.SameAccount(copy) {
		t.Fatal("credential changed identity", e)
	}
	for _, invalid := range []string{`{"type":"oauth","key":"x","access":"a","refresh":"r","expires":2,"accountId":"a"}`, `{"type":"oauth","access":"a","refresh":"r","expires":2}`, `{"type":"api_key","key":"x","env":{"GITHUB_TOKEN":"forbidden"}}`} {
		if _, e = Decode([]byte(invalid)); e == nil {
			t.Fatal("invalid credential accepted")
		}
	}
}

func TestKnownLegacyAnthropicPreservesAllAuthFieldsWithoutRewriting(t *testing.T) {
	for _, env := range []map[string]string{
		{"ANTHROPIC_AUTH_TOKEN": "synthetic-bearer"},
		{"ANTHROPIC_AUTH_TOKEN": "synthetic-bearer", "ANTHROPIC_OAUTH_TOKEN": "synthetic-oauth", "ANTHROPIC_API_KEY": "synthetic-key"},
		{"ANTHROPIC_OAUTH_TOKEN": "synthetic-oauth", "ANTHROPIC_API_KEY": "synthetic-key"},
	} {
		value, err := LegacySingleKey("anthropic", env)
		if err != nil || !value.NeedsLegacyAnthropic() {
			t.Fatal("known old authentication rejected", err)
		}
		encoded, err := value.AuthFile("anthropic")
		if err != nil {
			t.Fatal(err)
		}
		var auth map[string]struct {
			Type string
			Key  string
			Env  map[string]string
		}
		if err := json.Unmarshal(encoded, &auth); err != nil {
			t.Fatal(err)
		}
		got := auth["anthropic"]
		if got.Type != "api_key" || got.Key != "" || len(got.Env) != len(env) {
			t.Fatal("legacy field projected into wrong auth mode")
		}
		for name, original := range env {
			if got.Env[name] != original {
				t.Fatal("legacy auth priority input lost")
			}
		}
		env["ANTHROPIC_AUTH_TOKEN"] = "mutated-input"
		cloned, _ := value.AuthFile("anthropic")
		if strings.Contains(string(cloned), "mutated-input") {
			t.Fatal("credential shares mutable legacy input")
		}
		if _, err := value.AuthFile("openai-codex"); err == nil {
			t.Fatal("legacy auth moved to another provider")
		}
	}
	for _, env := range []map[string]string{
		{"ANTHROPIC_API_KEY": "synthetic", "HTTPS_PROXY": "synthetic-proxy"},
		{"UNKNOWN_TOKEN": "synthetic"},
	} {
		if _, err := LegacySingleKey("anthropic", env); err != ErrLegacyUnresolved {
			t.Fatal("unreviewed compatibility silently accepted")
		}
	}
}
