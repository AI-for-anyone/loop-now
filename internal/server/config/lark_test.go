package config

import "testing"

func TestLarkUserBundleConfiguration(t *testing.T) {
	env := requiredEnv()
	cfg, e := Load(envOf(env))
	if e != nil || cfg.LarkUserBundleDir != "" {
		t.Fatal("default must retain adjacent-release discovery")
	}
	env[EnvLarkUserBundleDir] = "/isolated/release/lark-user-variant"
	cfg, e = Load(envOf(env))
	if e != nil || cfg.LarkUserBundleDir != env[EnvLarkUserBundleDir] {
		t.Fatal("explicit controlled distribution path lost")
	}
}
