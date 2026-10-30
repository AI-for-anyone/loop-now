package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func requiredEnv() map[string]string {
	return map[string]string{
		EnvDatabaseURL:    "postgres://loop_now:s3cret@127.0.0.1:5432/loop_now_dev?sslmode=disable",
		EnvRedisURL:       "redis://127.0.0.1:6379/10",
		EnvLoopctlDistDir: "/opt/loop-now/loopctl-dist",
	}
}

func TestExternalPortConfig(t *testing.T) {
	env := requiredEnv()
	c, err := Load(envOf(env))
	if err != nil || c.ExternalListenAddr != "" {
		t.Fatalf("默认不开启对外端口：%q %v", c.ExternalListenAddr, err)
	}
	env[EnvExternalListenAddr] = "0.0.0.0:8781"
	if _, err := Load(envOf(env)); err == nil || !strings.Contains(err.Error(), EnvExternalTLSCert) || !strings.Contains(err.Error(), EnvExternalTLSKey) {
		t.Fatalf("开启对外端口却缺少证书与私钥时应拒绝启动：%v", err)
	}
	env[EnvExternalTLSCert], env[EnvExternalTLSKey] = "/etc/loop-now/cert.pem", "/etc/loop-now/key.pem"
	if c, err := Load(envOf(env)); err != nil || c.ExternalListenAddr != "0.0.0.0:8781" {
		t.Fatalf("配置齐全时应通过：%v", err)
	}
	env[EnvExternalListenAddr] = "8781"
	if _, err := Load(envOf(env)); err == nil {
		t.Fatal("对外监听地址格式不对时应拒绝启动")
	}
	delete(env, EnvLoopctlDistDir)
	if _, err := Load(envOf(env)); err == nil || !strings.Contains(err.Error(), EnvLoopctlDistDir) {
		t.Fatalf("缺少 loopctl 构建产物目录时应拒绝启动：%v", err)
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
		t.Errorf("默认配置不应有告警：%v", c.Warnings)
	}
	if c.PiMaxProcesses != 0 || c.PiIdleTimeout != 10*time.Minute || c.LockTTL != 30*time.Second {
		t.Errorf("执行器默认值不对：%+v", c)
	}
	if c.MaxUploadFile != 10<<20 || c.MaxMessage != 64<<10 || c.TextEditMax != 1<<20 || c.MaxInboxMessage != 64<<10 {
		t.Errorf("大小默认值不对：upload=%d message=%d textEdit=%d inbox=%d", c.MaxUploadFile, c.MaxMessage, c.TextEditMax, c.MaxInboxMessage)
	}
	if c.InboxPreviewChars != 200 || c.InboxPageMax != 100 {
		t.Errorf("Inbox 默认值不对：preview=%d pageMax=%d", c.InboxPreviewChars, c.InboxPageMax)
	}
	if got := strings.Join(c.AllowedHosts, ","); got != "localhost,127.0.0.1,::1" {
		t.Errorf("AllowedHosts = %q", got)
	}
	if c.WorkRoot != DefaultWorkRoot() {
		t.Errorf("WorkRoot = %q", c.WorkRoot)
	}
	if c.OverviewMaxLen != 1000 || c.PiStderrTailBytes != 1000 {
		t.Errorf("可观测默认值不对：overview=%d stderrTail=%d", c.OverviewMaxLen, c.PiStderrTailBytes)
	}
	if c.LogFormat != "text" || c.LogLevel != slog.LevelInfo {
		t.Errorf("日志默认值不对：%s %s", c.LogFormat, c.LogLevel)
	}
	if c.PluginMarketURL != "https://raw.githubusercontent.com/AI-for-anyone/loop-now-market/main/loop-now-market.json" {
		t.Errorf("官方插件市场默认地址不对：%q", c.PluginMarketURL)
	}
	if c.DefaultOrgName != "default" || c.DefaultProjectName != "default" {
		t.Errorf("全新安装的默认组织与项目名：%q %q", c.DefaultOrgName, c.DefaultProjectName)
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
		t.Fatalf("日志配置不对：%s %s", c.LogFormat, c.LogLevel)
	}
	var b strings.Builder
	NewLogger(&b, c.LogFormat, c.LogLevel).Debug("hi", "runId", "r1")
	if !strings.HasPrefix(b.String(), "{") || !strings.Contains(b.String(), `"runId":"r1"`) {
		t.Fatalf("JSON 格式的日志每行应是一个 JSON 对象：%s", b.String())
	}
	b.Reset()
	NewLogger(&b, "text", slog.LevelInfo).Debug("hidden")
	if b.Len() != 0 {
		t.Fatal("info 级别不应输出 debug 日志")
	}
}

func TestLoadErrors(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(map[string]string)
		wantVar string
	}{
		{"缺少数据库连接", func(m map[string]string) { delete(m, EnvDatabaseURL) }, EnvDatabaseURL},
		{"数据库连接协议不对", func(m map[string]string) { m[EnvDatabaseURL] = "mysql://x" }, EnvDatabaseURL},
		{"缺少 Redis 连接", func(m map[string]string) { delete(m, EnvRedisURL) }, EnvRedisURL},
		{"时长格式错误", func(m map[string]string) { m[EnvPiIdleTimeout] = "ten minutes" }, EnvPiIdleTimeout},
		{"时长为零", func(m map[string]string) { m[EnvAbortTimeout] = "0s" }, EnvAbortTimeout},
		{"大小格式错误", func(m map[string]string) { m[EnvMaxUploadFile] = "10MB" }, EnvMaxUploadFile},
		{"续期间隔不小于锁 TTL", func(m map[string]string) { m[EnvLockTTL] = "10s"; m[EnvLockRenewInterval] = "10s" }, EnvLockRenewInterval},
		{"对账周期不小于锁 TTL", func(m map[string]string) { m[EnvReconcileInterval] = "40s" }, EnvReconcileInterval},
		{"进程上限为负", func(m map[string]string) { m[EnvPiMaxProcesses] = "-1" }, EnvPiMaxProcesses},
		{"在线编辑上限大于上传上限", func(m map[string]string) { m[EnvTextEditMax] = "20MiB" }, EnvTextEditMax},
		{"监听地址格式错误", func(m map[string]string) { m[EnvListenAddr] = "localhost" }, EnvListenAddr},
		{"控制台来源带路径", func(m map[string]string) { m[EnvConsoleOrigins] = "http://localhost:5173/app" }, EnvConsoleOrigins},
		{"默认组织名含斜杠", func(m map[string]string) { m[EnvDefaultOrgName] = "a/b" }, EnvDefaultOrgName},
		{"默认项目名是 UUID 格式", func(m map[string]string) { m[EnvDefaultProjectName] = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b" }, EnvDefaultProjectName},
		{"概览事件流保留条数为零", func(m map[string]string) { m[EnvOverviewMaxLen] = "0" }, EnvOverviewMaxLen},
		{"Pi 错误输出长度为零", func(m map[string]string) { m[EnvPiStderrTail] = "0" }, EnvPiStderrTail},
		{"日志格式不合法", func(m map[string]string) { m[EnvLogFormat] = "xml" }, EnvLogFormat},
		{"日志级别不合法", func(m map[string]string) { m[EnvLogLevel] = "verbose" }, EnvLogLevel},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := requiredEnv()
			tc.mutate(env)
			_, err := Load(envOf(env))
			if err == nil {
				t.Fatal("期望返回错误")
			}
			if !strings.Contains(err.Error(), tc.wantVar) {
				t.Errorf("错误信息应指出 %s：%v", tc.wantVar, err)
			}
			if strings.Contains(err.Error(), "s3cret") {
				t.Errorf("错误信息泄露了机密：%v", err)
			}
		})
	}
}

func TestLoadNonLoopbackListenWarns(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8780", ":8780", "10.0.0.5:8780"} {
		env := requiredEnv()
		env[EnvListenAddr] = addr
		c, err := Load(envOf(env))
		if err != nil {
			t.Fatalf("%s: 非回环地址应告警后启动，而不是报错：%v", addr, err)
		}
		if len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], EnvAllowedHosts) {
			t.Errorf("%s: 告警不对：%v", addr, c.Warnings)
		}
	}
	for _, addr := range []string{"127.0.0.1:8780", "[::1]:8780", "localhost:8780"} {
		env := requiredEnv()
		env[EnvListenAddr] = addr
		c, err := Load(envOf(env))
		if err != nil || len(c.Warnings) != 0 {
			t.Errorf("%s: 回环地址不应告警：err=%v warnings=%v", addr, err, c.Warnings)
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

func TestLogValueRedactsSecrets(t *testing.T) {
	c, err := Load(envOf(requiredEnv()))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	slog.New(slog.NewTextHandler(&b, nil)).Info("config", "config", c)
	out := b.String()
	if strings.Contains(out, "s3cret") {
		t.Errorf("日志泄露了机密：%s", out)
	}
	if !strings.Contains(out, "piMaxProcesses=0") {
		t.Errorf("日志缺少生效配置：%s", out)
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{"65536": 65536, "64KiB": 65536, "10MiB": 10 << 20, "1GiB": 1 << 30, " 2 MiB ": 2 << 20} {
		got, err := ParseSize(in)
		if err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v；期望 %d", in, got, err, want)
		}
	}
	for _, in := range []string{"10MB", "abc", ""} {
		if _, err := ParseSize(in); err == nil {
			t.Errorf("ParseSize(%q) 应报错", in)
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

// 旧部署环境里残留的主密钥变量不再读取，也不会进入日志。
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
		t.Errorf("日志仍包含主密钥配置：%s", out)
	}
}
