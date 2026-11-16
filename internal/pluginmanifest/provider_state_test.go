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
			t.Errorf("should be rejected: %v", bad)
		}
	}
}

func TestOverlappingStatePath(t *testing.T) {
	managed := []string{".config/gh/hosts.yml", ".lark-cli/config.json"}
	for _, c := range []struct {
		decl []string
		hit  bool
	}{
		{[]string{".pi/grok-cli"}, false},
		{[]string{".config/gh"}, true},              // declared path covers the template file
		{[]string{".config"}, true},                 // a higher ancestor counts too
		{[]string{".config/gh/hosts.yml"}, true},    // equal
		{[]string{".lark-cli/config.json/x"}, true}, // template file path covers the declared path
		{[]string{".config/ghx"}, false},            // only a sibling sharing a prefix
	} {
		if _, _, ok := OverlappingStatePath(c.decl, managed); ok != c.hit {
			t.Errorf("%v: overlap = %v, want %v", c.decl, ok, c.hit)
		}
	}
}
