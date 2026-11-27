package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// fixturePath turns an absolute Unix path hard-coded in a test into an absolute path for this platform (on Windows it
// adds C: and switches separators); only for paths that are never actually accessed.
func fixturePath(p string) string {
	if runtime.GOOS == "windows" {
		return "C:" + filepath.FromSlash(p)
	}
	return p
}

func requiredEnv() map[string]string {
	return map[string]string{
		EnvLoopctlDistDir: fixturePath("/opt/loop-now/loopctl-dist"),
	}
}

func TestExternalPortConfig(t *testing.T) {
	env := requiredEnv()
	c, err := Load(envOf(env))
	if err != nil || c.ExternalListenAddr != "" {
		t.Fatalf("the external port should be off by default: %q %v", c.ExternalListenAddr, err)
	}
	env[EnvExternalListenAddr] = "0.0.0.0:8781"
	if _, err := Load(envOf(env)); err == nil || !strings.Contains(err.Error(), EnvExternalTLSCert) || !strings.Contains(err.Error(), EnvExternalTLSKey) {
		t.Fatalf("should refuse to start with the external port on but no certificate and private key: %v", err)
	}
	env[EnvExternalTLSCert], env[EnvExternalTLSKey] = "/etc/loop-now/cert.pem", "/etc/loop-now/key.pem"
	if c, err := Load(envOf(env)); err != nil || c.ExternalListenAddr != "0.0.0.0:8781" {
		t.Fatalf("should pass with a complete configuration: %v", err)
	}
	env[EnvExternalListenAddr] = "8781"
	if _, err := Load(envOf(env)); err == nil {
		t.Fatal("should refuse to start with a malformed external listen address")
	}
	// Self-signed certificate directory: replaces the certificate and private key, is mutually exclusive with them,
	// and must be a clean absolute path.
	env[EnvExternalListenAddr] = "127.0.0.1:0"
	env[EnvExternalTLSSelfSigned] = fixturePath("/var/loop-now/tls")
	if _, err := Load(envOf(env)); err == nil || !strings.Contains(err.Error(), EnvExternalTLSSelfSigned) {
		t.Fatalf("should refuse to start with both a self-signed certificate directory and a certificate and private key: %v", err)
	}
	delete(env, EnvExternalTLSCert)
	delete(env, EnvExternalTLSKey)
	if c, err := Load(envOf(env)); err != nil || c.ExternalTLSSelfSigned != fixturePath("/var/loop-now/tls") {
		t.Fatalf("should pass with only the self-signed certificate directory: %v", err)
	}
	env[EnvExternalTLSSelfSigned] = "relative/tls"
	if _, err := Load(envOf(env)); err == nil || !strings.Contains(err.Error(), EnvExternalTLSSelfSigned) {
		t.Fatalf("should refuse to start when the self-signed certificate directory is not absolute: %v", err)
	}
	delete(env, EnvExternalTLSSelfSigned)
	env[EnvExternalTLSCert], env[EnvExternalTLSKey] = "/etc/loop-now/cert.pem", "/etc/loop-now/key.pem"
	delete(env, EnvLoopctlDistDir)
	if _, err := Load(envOf(env)); err == nil || !strings.Contains(err.Error(), EnvLoopctlDistDir) {
		t.Fatalf("should refuse to start without the loopctl build output directory: %v", err)
	}
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(envOf(requiredEnv()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.PiVersion != "1.4.0" {
		t.Fatalf("Pi baseline = %q", c.PiVersion)
	}
	if c.ListenAddr != "127.0.0.1:8780" {
		t.Errorf("ListenAddr = %q", c.ListenAddr)
	}
	if len(c.Warnings) != 0 {
		t.Errorf("the default configuration should have no warnings: %v", c.Warnings)
	}
	if c.PiMaxProcesses != 0 || c.PiIdleTimeout != 10*time.Minute {
		t.Errorf("unexpected executor defaults: %+v", c)
	}
	if c.MaxUploadFile != 10<<20 || c.MaxMessage != 64<<10 || c.TextEditMax != 20<<20 || c.MaxInboxMessage != 64<<10 {
		t.Errorf("unexpected size defaults: upload=%d message=%d textEdit=%d inbox=%d", c.MaxUploadFile, c.MaxMessage, c.TextEditMax, c.MaxInboxMessage)
	}
	if c.InboxPreviewChars != 200 || c.InboxPageMax != 100 {
		t.Errorf("unexpected Inbox defaults: preview=%d pageMax=%d", c.InboxPreviewChars, c.InboxPageMax)
	}
	if got := strings.Join(c.AllowedHosts, ","); got != "localhost,127.0.0.1,::1" {
		t.Errorf("AllowedHosts = %q", got)
	}
	if c.WorkRoot != DefaultWorkRoot() {
		t.Errorf("WorkRoot = %q", c.WorkRoot)
	}
	if c.OverviewMaxLen != 1000 || c.PiStderrTailBytes != 1000 {
		t.Errorf("unexpected observability defaults: overview=%d stderrTail=%d", c.OverviewMaxLen, c.PiStderrTailBytes)
	}
	if c.LogFormat != "text" || c.LogLevel != slog.LevelInfo {
		t.Errorf("unexpected log defaults: %s %s", c.LogFormat, c.LogLevel)
	}
	if c.PluginMarketURL != "https://raw.githubusercontent.com/AI-for-anyone/loop-now-market/main/loop-now-market.json" {
		t.Errorf("unexpected default official plugin market URL: %q", c.PluginMarketURL)
	}
	if c.DefaultOrgName != "default" || c.DefaultProjectName != "default" {
		t.Errorf("default org and project names on a fresh install: %q %q", c.DefaultOrgName, c.DefaultProjectName)
	}
}

func TestLogConfig(t *testing.T) {
	env := requiredEnv()
	env[EnvLogFormat], env[EnvLogLevel] = "json", "DEBUG"
	c, err := Load(envOf(env))
	if err != nil {
		t.Fatal(err)
	}
	if c.LogFormat != "json" || c.LogLevel != slog.LevelDebug {
		t.Fatalf("unexpected log configuration: %s %s", c.LogFormat, c.LogLevel)
	}
	var b strings.Builder
	NewLogger(&b, c.LogFormat, c.LogLevel).Debug("hi", "runId", "r1")
	if !strings.HasPrefix(b.String(), "{") || !strings.Contains(b.String(), `"runId":"r1"`) {
		t.Fatalf("each JSON log line should be one JSON object: %s", b.String())
	}
	b.Reset()
	NewLogger(&b, "text", slog.LevelInfo).Debug("hidden")
	if b.Len() != 0 {
		t.Fatal("the info level should not output debug logs")
	}
}

func TestLoadErrors(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(map[string]string)
		wantVar string
	}{
		{"data root not absolute", func(m map[string]string) { m[EnvDataDir] = "relative/dir" }, EnvDataDir},
		{"data root not clean", func(m map[string]string) { m[EnvDataDir] = "/data/../loop" }, EnvDataDir},
		{"backup directory not absolute", func(m map[string]string) { m[EnvBackupDir] = "backups" }, EnvBackupDir},
		{"zero backups kept", func(m map[string]string) { m[EnvBackupKeep] = "0" }, EnvBackupKeep},
		{"malformed duration", func(m map[string]string) { m[EnvPiIdleTimeout] = "ten minutes" }, EnvPiIdleTimeout},
		{"zero duration", func(m map[string]string) { m[EnvAbortTimeout] = "0s" }, EnvAbortTimeout},
		{"malformed size", func(m map[string]string) { m[EnvMaxUploadFile] = "10MB" }, EnvMaxUploadFile},
		{"negative process limit", func(m map[string]string) { m[EnvPiMaxProcesses] = "-1" }, EnvPiMaxProcesses},
		{"text edit limit above the folder upload limit", func(m map[string]string) { m[EnvMaxFolderUpload] = "10MiB" }, EnvTextEditMax},
		{"malformed listen address", func(m map[string]string) { m[EnvListenAddr] = "localhost" }, EnvListenAddr},
		{"console origin with a path", func(m map[string]string) { m[EnvConsoleOrigins] = "http://localhost:5173/app" }, EnvConsoleOrigins},
		{"none mixed with origins", func(m map[string]string) { m[EnvConsoleOrigins] = "none,http://localhost:5173" }, EnvConsoleOrigins},
		{"console directory not absolute", func(m map[string]string) { m[EnvWebDir] = "web/dist" }, EnvWebDir},
		{"ready file not clean", func(m map[string]string) { m[EnvReadyFile] = "/tmp/../ready.json" }, EnvReadyFile},
		{"default org name with a slash", func(m map[string]string) { m[EnvDefaultOrgName] = "a/b" }, EnvDefaultOrgName},
		{"default project name in UUID format", func(m map[string]string) { m[EnvDefaultProjectName] = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b" }, EnvDefaultProjectName},
		{"zero overview stream length", func(m map[string]string) { m[EnvOverviewMaxLen] = "0" }, EnvOverviewMaxLen},
		{"zero Pi stderr length", func(m map[string]string) { m[EnvPiStderrTail] = "0" }, EnvPiStderrTail},
		{"invalid log format", func(m map[string]string) { m[EnvLogFormat] = "xml" }, EnvLogFormat},
		{"invalid log level", func(m map[string]string) { m[EnvLogLevel] = "verbose" }, EnvLogLevel},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := requiredEnv()
			tc.mutate(env)
			_, err := Load(envOf(env))
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.wantVar) {
				t.Errorf("the error should name %s: %v", tc.wantVar, err)
			}
			if strings.Contains(err.Error(), "s3cret") {
				t.Errorf("the error leaked a secret: %v", err)
			}
		})
	}
}

// The online preview and edit limit is independent of LOOP_NOW_MAX_UPLOAD_FILE: a saved text file goes through the folder
// write, bounded by LOOP_NOW_MAX_FOLDER_UPLOAD. The variable still overrides the default.
func TestLoadTextEditMax(t *testing.T) {
	env := requiredEnv()
	env[EnvTextEditMax] = "5MiB"
	c, err := Load(envOf(env))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.TextEditMax != 5<<20 {
		t.Errorf("TextEditMax = %d, want the override 5 MiB", c.TextEditMax)
	}
	env[EnvTextEditMax], env[EnvMaxUploadFile] = "30MiB", "10MiB"
	if c, err = Load(envOf(env)); err != nil || c.TextEditMax != 30<<20 {
		t.Errorf("a limit above the upload limit but within the folder upload limit should load: %v", err)
	}
}

func TestLoadNonLoopbackListenWarns(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8780", ":8780", "10.0.0.5:8780"} {
		env := requiredEnv()
		env[EnvListenAddr] = addr
		c, err := Load(envOf(env))
		if err != nil {
			t.Fatalf("%s: a non-loopback address should warn and start, not fail: %v", addr, err)
		}
		if len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], EnvAllowedHosts) {
			t.Errorf("%s: unexpected warnings: %v", addr, c.Warnings)
		}
	}
	for _, addr := range []string{"127.0.0.1:8780", "[::1]:8780", "localhost:8780"} {
		env := requiredEnv()
		env[EnvListenAddr] = addr
		c, err := Load(envOf(env))
		if err != nil || len(c.Warnings) != 0 {
			t.Errorf("%s: a loopback address should not warn: err=%v warnings=%v", addr, err, c.Warnings)
		}
	}
}

func TestLoadOverrides(t *testing.T) {
	env := requiredEnv()
	env[EnvPiMaxProcesses] = "2"
	env[EnvAllowedHosts] = "localhost, 10.0.0.5:8780 ,[::1]"
	env[EnvTextEditExtensions] = "md,.TXT"
	env[EnvMaxCredential] = "1KiB"
	c, err := Load(envOf(env))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.PiMaxProcesses != 2 {
		t.Errorf("PiMaxProcesses = %d", c.PiMaxProcesses)
	}
	if got := strings.Join(c.AllowedHosts, ","); got != "localhost,10.0.0.5,::1" {
		t.Errorf("AllowedHosts = %q", got)
	}
	if got := strings.Join(c.TextEditExtensions, ","); got != ".md,.txt" {
		t.Errorf("TextEditExtensions = %q", got)
	}
	if c.MaxCredential != 1024 {
		t.Errorf("MaxCredential = %d", c.MaxCredential)
	}
}

// TestStopOnStdinClose: stopping on stdin close is off by default, the usual true/false spellings are accepted, and an
// invalid value is an error that names the variable.
func TestStopOnStdinClose(t *testing.T) {
	env := requiredEnv()
	if c, err := Load(envOf(env)); err != nil || c.StopOnStdinClose {
		t.Fatalf("should be off by default: %v %v", err, c)
	}
	for raw, want := range map[string]bool{"1": true, "true": true, "YES": true, "on": true, "0": false, "false": false, "No": false, "off": false, " ": false} {
		env[EnvStopOnStdinClose] = raw
		c, err := Load(envOf(env))
		if err != nil || c.StopOnStdinClose != want {
			t.Fatalf("%q should parse as %v: %v", raw, want, err)
		}
	}
	env[EnvStopOnStdinClose] = "maybe"
	if _, err := Load(envOf(env)); err == nil || !strings.Contains(err.Error(), EnvStopOnStdinClose) {
		t.Fatalf("an invalid value should fail and name the variable: %v", err)
	}
}

// The console origins can be set to none (no cross-origin access at all); the console directory and the ready file
// are taken as given.
func TestConsoleSettings(t *testing.T) {
	env := requiredEnv()
	c, err := Load(envOf(env))
	if err != nil || len(c.ConsoleOrigins) != 2 || c.WebDir != "" || c.ReadyFile != "" {
		t.Fatalf("unexpected defaults: %v %+v", err, c.ConsoleOrigins)
	}
	env[EnvConsoleOrigins] = "NONE"
	env[EnvWebDir] = fixturePath("/opt/loop-now/web")
	env[EnvReadyFile] = fixturePath("/tmp/loop-now-ready.json")
	if c, err = Load(envOf(env)); err != nil || c.ConsoleOrigins != nil || c.WebDir != fixturePath("/opt/loop-now/web") || c.ReadyFile != fixturePath("/tmp/loop-now-ready.json") {
		t.Fatalf("unexpected overrides: %v %+v %q %q", err, c.ConsoleOrigins, c.WebDir, c.ReadyFile)
	}
}

func TestLogValueRedactsSecrets(t *testing.T) {
	c, err := Load(envOf(requiredEnv()))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	slog.New(slog.NewTextHandler(&b, nil)).Info("config", "config", c)
	out := b.String()
	if strings.Contains(out, "s3cret") {
		t.Errorf("the log leaked a secret: %s", out)
	}
	if !strings.Contains(out, "piMaxProcesses=0") {
		t.Errorf("the log lacks the effective configuration: %s", out)
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{"65536": 65536, "64KiB": 65536, "10MiB": 10 << 20, "1GiB": 1 << 30, " 2 MiB ": 2 << 20} {
		got, err := ParseSize(in)
		if err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"10MB", "abc", ""} {
		if _, err := ParseSize(in); err == nil {
			t.Errorf("ParseSize(%q) should fail", in)
		}
	}
}

func TestAuthIPCTimeoutMatchesPrivateClientBounds(t *testing.T) {
	env := requiredEnv()
	env[EnvAuthIPCTimeout] = "41s"
	c, err := Load(envOf(env))
	if err != nil || c.AuthIPCTimeout != 41*time.Second {
		t.Fatal("configured runtime timeout lost", err)
	}
	env[EnvAuthIPCTimeout] = "2147483648ms"
	if _, err := Load(envOf(env)); err == nil || !strings.Contains(err.Error(), EnvAuthIPCTimeout) {
		t.Fatal("timeout exceeds native client timer range", err)
	}
}

func TestInboxQuestionTTLConfig(t *testing.T) {
	values := requiredEnv()
	c, err := Load(envOf(values))
	if err != nil || c.InboxQuestionTTL != 7*24*time.Hour {
		t.Fatal("question TTL default", err)
	}
	values[EnvInboxQuestionTTL] = "2h"
	c, err = Load(envOf(values))
	if err != nil || c.InboxQuestionTTL != 2*time.Hour {
		t.Fatal("question TTL override", err)
	}
	values[EnvInboxQuestionTTL] = "0s"
	if _, err = Load(envOf(values)); err == nil {
		t.Fatal("nonpositive question TTL accepted")
	}
}

func TestSleepScanConfiguration(t *testing.T) {
	env := requiredEnv()
	c, err := Load(envOf(env))
	if err != nil || c.SleepScanInterval != time.Second || c.SleepScanBatch != 100 {
		t.Fatal("sleep defaults", err)
	}
	env[EnvSleepScanInterval] = "75ms"
	env[EnvSleepScanBatch] = "7"
	c, err = Load(envOf(env))
	if err != nil || c.SleepScanInterval != 75*time.Millisecond || c.SleepScanBatch != 7 {
		t.Fatal("sleep override", err)
	}
	for _, key := range []string{EnvSleepScanInterval, EnvSleepScanBatch} {
		old := env[key]
		env[key] = "0"
		if _, err = Load(envOf(env)); err == nil {
			t.Fatal("nonpositive accepted", key)
		}
		env[key] = old
	}
}

func TestImageInputLimits(t *testing.T) {
	c, e := Load(envOf(requiredEnv()))
	if e != nil || c.MaxMessageImages != 4 || c.MaxMessageImageBytes != 5<<20 || c.MaxMessageImagePixels != 20_000_000 {
		t.Fatal("image defaults", e)
	}
	for _, tc := range []struct {
		count, bytes, pixels string
		valid                bool
	}{{"8", "3MiB", "40000000", true}, {"0", "1MiB", "1", false}, {"9", "1MiB", "1", false}, {"4", "7MiB", "1", false}, {"1", "11MiB", "1", false}, {"1", "1MiB", "40000001", false}} {
		env := requiredEnv()
		env[EnvMaxMessageImages] = tc.count
		env[EnvMaxMessageImageBytes] = tc.bytes
		env[EnvMaxMessageImagePixels] = tc.pixels
		_, e := Load(envOf(env))
		if (e == nil) != tc.valid {
			t.Fatalf("%+v %v", tc, e)
		}
	}
}

func TestPiMaxProcessesZeroAndPositive(t *testing.T) {
	for value, want := range map[string]int{"0": 0, "1": 1, "8": 8} {
		t.Run(value, func(t *testing.T) {
			env := requiredEnv()
			env[EnvPiMaxProcesses] = value
			c, err := Load(envOf(env))
			if err != nil {
				t.Fatal(err)
			}
			if c.PiMaxProcesses != want {
				t.Fatalf("unexpected limit %d", c.PiMaxProcesses)
			}
		})
	}
}

// TestLegacyMasterKeyVariableIgnored checks that a master key variable left over in an old deployment's environment
// is no longer read and never reaches the logs.
func TestLegacyMasterKeyVariableIgnored(t *testing.T) {
	env := requiredEnv()
	env["LOOP_NOW_MASTER_KEY"] = "legacy-master-key-marker"
	c, err := Load(envOf(env))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	slog.New(slog.NewTextHandler(&b, nil)).Info("config", "config", c)
	if out := b.String(); strings.Contains(out, "legacy-master-key-marker") || strings.Contains(strings.ToLower(out), "masterkey") {
		t.Errorf("the log still contains the master key setting: %s", out)
	}
}

func TestDataDirDefaultAndDBDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("on Windows the default data directory is %LOCALAPPDATA%\\Loop Now\\data, not derived from HOME")
	}
	env := requiredEnv()
	env["HOME"] = "/home/fixture"
	c, err := Load(envOf(env))
	if err != nil {
		t.Fatal(err)
	}
	if c.DataDir != "/home/fixture/.loop_now" || DBDir(c.DataDir) != "/home/fixture/.loop_now/db" {
		t.Fatalf("the default data root should be .loop_now under HOME, with the database in its db subdirectory: %q", c.DataDir)
	}
	if c.BackupDir != "/home/fixture/.loop_now/backups" || c.BackupKeep != 7 {
		t.Fatalf("default backups go under backups in the data root, keeping 7: %q %d", c.BackupDir, c.BackupKeep)
	}
	env[EnvDataDir] = "/srv/loop-now"
	if c, err = Load(envOf(env)); err != nil || c.DataDir != "/srv/loop-now" || c.BackupDir != "/srv/loop-now/backups" {
		t.Fatalf("LOOP_NOW_DATA_DIR should override the default, and the backup directory follows it: %q %q %v", c.DataDir, c.BackupDir, err)
	}
	env[EnvBackupDir], env[EnvBackupKeep] = "/mnt/backup", "3"
	if c, err = Load(envOf(env)); err != nil || c.BackupDir != "/mnt/backup" || c.BackupKeep != 3 {
		t.Fatalf("the backup directory and count can be overridden: %q %d %v", c.BackupDir, c.BackupKeep, err)
	}
}

// TestBundledDefaults checks that the runtime, loopctl-dist and plugin directories next to loop-server in the release
// bundle are picked up automatically when not configured; environment variables still take precedence; with nothing
// present, loopctl-dist is a required setting.
func TestBundledDefaults(t *testing.T) {
	dir := t.TempDir()
	old := executableDir
	executableDir = func() string { return dir }
	t.Cleanup(func() { executableDir = old })
	if _, err := Load(envOf(map[string]string{})); err == nil || !strings.Contains(err.Error(), EnvLoopctlDistDir) {
		t.Fatalf("should require configuration without loopctl-dist: %v", err)
	}
	c, err := Load(envOf(requiredEnv()))
	if err != nil || c.NodeBin != "node" || c.PiBin != "pi" || c.PluginHostEntry != "" || c.PluginOfficialDir != "" {
		t.Fatalf("should keep the original defaults without bundled files: %v %+v", err, c)
	}
	for _, rel := range []string{BundledNode, BundledPi, BundledPluginHost} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{BundledLoopctlDist, BundledPluginOfficial} {
		if err := os.MkdirAll(filepath.Join(dir, rel), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if c, err = Load(envOf(map[string]string{})); err != nil {
		t.Fatal(err)
	}
	for got, want := range map[string]string{c.NodeBin: BundledNode, c.PiBin: BundledPi, c.LoopctlDistDir: BundledLoopctlDist, c.PluginHostEntry: BundledPluginHost, c.PluginOfficialDir: BundledPluginOfficial} {
		if got != filepath.Join(dir, want) {
			t.Errorf("should pick up the bundled %s: %q", want, got)
		}
	}
	if c, err = Load(envOf(map[string]string{EnvNodeBin: fixturePath("/opt/node/bin/node"), EnvLoopctlDistDir: fixturePath("/opt/loopctl-dist")})); err != nil || c.NodeBin != fixturePath("/opt/node/bin/node") || c.LoopctlDistDir != fixturePath("/opt/loopctl-dist") {
		t.Fatalf("environment variables should take precedence: %v %q %q", err, c.NodeBin, c.LoopctlDistDir)
	}
}

// How long a turn may wait for an agent's background commands without a word from the agent: 30 minutes unless set,
// and never 0.
func TestACPBackgroundWait(t *testing.T) {
	env := requiredEnv()
	if c, err := Load(envOf(env)); err != nil || c.ACPBackgroundWait != 30*time.Minute {
		t.Fatalf("default: %v %v", c.ACPBackgroundWait, err)
	}
	env[EnvACPBackgroundWait] = "45m"
	if c, err := Load(envOf(env)); err != nil || c.ACPBackgroundWait != 45*time.Minute {
		t.Fatalf("set: %v %v", c.ACPBackgroundWait, err)
	}
	env[EnvACPBackgroundWait] = "0s"
	if _, err := Load(envOf(env)); err == nil || !strings.Contains(err.Error(), EnvACPBackgroundWait) {
		t.Fatalf("0 is refused: %v", err)
	}
}
