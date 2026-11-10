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

func TestOverlappingStatePath(t *testing.T) {
	managed := []string{".config/gh/hosts.yml", ".lark-cli/config.json"}
	for _, c := range []struct {
		decl []string
		hit  bool
	}{
		{[]string{".pi/grok-cli"}, false},
		{[]string{".config/gh"}, true},              // 声明路径盖住模板文件
		{[]string{".config"}, true},                 // 更上层也算
		{[]string{".config/gh/hosts.yml"}, true},    // 相同
		{[]string{".lark-cli/config.json/x"}, true}, // 模板文件路径盖住声明路径
		{[]string{".config/ghx"}, false},            // 只是前缀相同的兄弟
	} {
		if _, _, ok := OverlappingStatePath(c.decl, managed); ok != c.hit {
			t.Errorf("%v：重叠 = %v，期望 %v", c.decl, ok, c.hit)
		}
	}
}
