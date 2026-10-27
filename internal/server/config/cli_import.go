package config

import (
	"log/slog"
	"time"
)

const (
	EnvCLIImportScanTTL       = "LOOP_NOW_CLI_IMPORT_SCAN_TTL"
	EnvCLIImportTimeout       = "LOOP_NOW_CLI_IMPORT_TIMEOUT"
	EnvCLIImportRecordTimeout = "LOOP_NOW_CLI_IMPORT_RECORD_TIMEOUT"
	EnvCLIImportMaxBatch      = "LOOP_NOW_CLI_IMPORT_MAX_BATCH"
	EnvCLIImportMaxActive     = "LOOP_NOW_CLI_IMPORT_MAX_ACTIVE"
	EnvCLIImportMaxScans      = "LOOP_NOW_CLI_IMPORT_MAX_SCANS_PER_PROJECT"
)

func (p *parser) cliImport(c *Config) {
	c.CLIImportScanTTL = p.duration(EnvCLIImportScanTTL, "10m")
	c.CLIImportTimeout = p.duration(EnvCLIImportTimeout, "2m")
	c.CLIImportRecordTimeout = p.duration(EnvCLIImportRecordTimeout, "5s")
	c.CLIImportMaxBatch = p.integer(EnvCLIImportMaxBatch, 20)
	c.CLIImportMaxActive = p.integer(EnvCLIImportMaxActive, 4)
	c.CLIImportMaxScans = p.integer(EnvCLIImportMaxScans, 4)
	for _, v := range []struct {
		k string
		v time.Duration
	}{{EnvCLIImportScanTTL, c.CLIImportScanTTL}, {EnvCLIImportTimeout, c.CLIImportTimeout}, {EnvCLIImportRecordTimeout, c.CLIImportRecordTimeout}} {
		if v.v <= 0 {
			p.fail(v.k, "必须为正")
		}
	}
	for _, v := range []struct {
		k string
		v int
	}{{EnvCLIImportMaxBatch, c.CLIImportMaxBatch}, {EnvCLIImportMaxActive, c.CLIImportMaxActive}, {EnvCLIImportMaxScans, c.CLIImportMaxScans}} {
		if v.v <= 0 {
			p.fail(v.k, "必须为正")
		}
	}
	if c.CLIImportMaxBatch > 20 {
		p.fail(EnvCLIImportMaxBatch, "不得大于20")
	}
}
func (c *Config) cliImportLog() slog.Attr {
	return slog.Group("cliImport", slog.Duration("scanTTL", c.CLIImportScanTTL), slog.Duration("timeout", c.CLIImportTimeout), slog.Duration("recordTimeout", c.CLIImportRecordTimeout), slog.Int("maxBatch", c.CLIImportMaxBatch), slog.Int("maxActive", c.CLIImportMaxActive), slog.Int("maxScansPerProject", c.CLIImportMaxScans))
}
