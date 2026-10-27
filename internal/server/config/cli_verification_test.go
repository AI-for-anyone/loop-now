package config

import (
	"strings"
	"testing"
	"time"
)

func TestCLIVerificationRuntimeDefaultsAndValidation(t *testing.T) {
	env := requiredEnv()
	env[EnvDatabaseURL] = "postgres://fixture@localhost/loop_now_test"
	cfg, e := Load(envOf(env))
	if e != nil || cfg.CLIProbeTimeout != 30*time.Second || cfg.CLIProbeConcurrency != 4 {
		t.Fatal("verification defaults invalid", e)
	}
	env[EnvCLIProbeTimeout] = "2s"
	env[EnvCLIProbeConcurrency] = "2"
	cfg, e = Load(envOf(env))
	if e != nil || cfg.CLIProbeTimeout != 2*time.Second || cfg.CLIProbeConcurrency != 2 {
		t.Fatal("verification budgets not applied", e)
	}
	for _, key := range []string{EnvCLIProbeTimeout, EnvCLIProbeConcurrency} {
		t.Run(key, func(t *testing.T) {
			bad := requiredEnv()
			bad[EnvDatabaseURL] = "postgres://fixture@localhost/loop_now_test"
			bad[key] = "0"
			if _, e := Load(envOf(bad)); e == nil || !strings.Contains(e.Error(), key) {
				t.Fatal("nonpositive verification budget accepted or rejected for unrelated reason", e)
			}
		})
	}
}
