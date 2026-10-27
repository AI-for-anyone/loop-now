package config

import (
	"strings"
	"testing"
	"time"
)

func TestCLIImportBudgets(t *testing.T) {
	env := requiredEnv()
	env[EnvDatabaseURL] = "postgres://fixture@localhost/loop_now_test"
	c, e := Load(envOf(env))
	if e != nil {
		t.Fatal(e)
	}
	if c.CLIImportScanTTL != 10*time.Minute || c.CLIImportTimeout != 2*time.Minute || c.CLIImportRecordTimeout != 5*time.Second || c.CLIImportMaxBatch != 20 || c.CLIImportMaxActive != 4 || c.CLIImportMaxScans != 4 {
		t.Fatal("import defaults changed")
	}
	for _, key := range []string{EnvCLIImportScanTTL, EnvCLIImportTimeout, EnvCLIImportRecordTimeout, EnvCLIImportMaxBatch, EnvCLIImportMaxActive, EnvCLIImportMaxScans} {
		t.Run(key, func(t *testing.T) {
			v := requiredEnv()
			v[EnvDatabaseURL] = env[EnvDatabaseURL]
			v[key] = "0"
			if _, e := Load(envOf(v)); e == nil || !strings.Contains(e.Error(), key) {
				t.Fatal("invalid limit accepted", e)
			}
		})
	}
	env[EnvCLIImportMaxBatch] = "21"
	if _, e := Load(envOf(env)); e == nil || !strings.Contains(e.Error(), EnvCLIImportMaxBatch) {
		t.Fatal("wire batch limit enlarged", e)
	}
}
