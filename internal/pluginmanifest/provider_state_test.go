package pluginmanifest

import "testing"

func TestCheckProviderState(t *testing.T) {
	ok := map[string]ProviderState{"grok-cli": {Paths: []string{".pi/grok-cli", ".config/grok"}}}
	if err := CheckProviderState("providerState", ok); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []map[string]ProviderState{
		{"grok-cli": {Paths: nil}},
		{"grok-cli": {Paths: []string{"/etc/passwd"}}},
		{"grok-cli": {Paths: []string{"../escape"}}},
		{"grok-cli": {Paths: []string{".pi/./x"}}},
		{"grok-cli": {Paths: []string{".pi//x"}}},
		{"grok-cli": {Paths: []string{`.pi\x`}}},
		{"grok-cli": {Paths: []string{"C:x"}}},
		{"grok-cli": {Paths: []string{".loop-now/db"}}},
		{"grok-cli": {Paths: []string{".pi", ".pi/grok-cli"}}},
		{"grok-cli": {Paths: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}}},
		{"bad id!": {Paths: []string{".x"}}},
	} {
		if err := CheckProviderState("providerState", bad); err == nil {
			t.Errorf("应拒绝：%v", bad)
		}
	}
}
