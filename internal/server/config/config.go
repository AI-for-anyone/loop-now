// Package config 解析与校验服务端的环境变量配置。
//
// 除数据库连接与 Redis 连接外，每一项都有默认值且可以覆盖；
// 业务代码中的超时、上限等运行参数都应取自这里，不另行写死。
package config

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"loop-now/internal/server/naming"
)

// 环境变量名。
const (
	EnvSleepScanInterval     = "LOOP_NOW_SLEEP_SCAN_INTERVAL"
	EnvSleepScanBatch        = "LOOP_NOW_SLEEP_SCAN_BATCH"
	EnvAuthLoginTimeout      = "LOOP_NOW_AUTH_LOGIN_TIMEOUT"
	EnvAuthRefreshTimeout    = "LOOP_NOW_AUTH_REFRESH_TIMEOUT"
	EnvAuthRecordTimeout     = "LOOP_NOW_AUTH_RECORD_TIMEOUT"
	EnvAuthIPCTimeout        = "LOOP_NOW_AUTH_IPC_TIMEOUT"
	EnvTaskRecordTimeout     = "LOOP_NOW_TASK_RECORD_TIMEOUT"
	EnvTaskEventPoll         = "LOOP_NOW_TASK_EVENT_POLL"
	EnvTaskReconcileInterval = "LOOP_NOW_TASK_RECONCILE_INTERVAL"
	EnvTaskMaxSession        = "LOOP_NOW_TASK_MAX_SESSION"
	EnvTaskMaxArtifact       = "LOOP_NOW_TASK_MAX_ARTIFACT"
	EnvTaskMaxRequest        = "LOOP_NOW_TASK_MAX_REQUEST"
	EnvTaskMaxVisits         = "LOOP_NOW_TASK_MAX_VISITS"
	EnvTaskMaxEdges          = "LOOP_NOW_TASK_MAX_EDGES"
	EnvTaskMaxSteps          = "LOOP_NOW_TASK_MAX_STEPS"
	EnvDatabaseURL           = "LOOP_NOW_DATABASE_URL"
	EnvRedisURL              = "LOOP_NOW_REDIS_URL"
	EnvListenAddr            = "LOOP_NOW_LISTEN_ADDR"
	EnvAllowedHosts          = "LOOP_NOW_ALLOWED_HOSTS"
	EnvConsoleOrigins        = "LOOP_NOW_CONSOLE_ORIGINS"
	EnvMaxMessageImages      = "LOOP_NOW_MAX_MESSAGE_IMAGES"
	EnvMaxMessageImageBytes  = "LOOP_NOW_MAX_MESSAGE_IMAGE_BYTES"
	EnvMaxMessageImagePixels = "LOOP_NOW_MAX_MESSAGE_IMAGE_PIXELS"
	EnvWorkRoot              = "LOOP_NOW_WORK_ROOT"
	EnvContextDir            = "LOOP_NOW_CONTEXT_DIR"
	EnvRedisKeyPrefix        = "LOOP_NOW_REDIS_KEY_PREFIX"
	EnvDefaultOrgName        = "LOOP_NOW_DEFAULT_ORG_NAME"
	EnvDefaultProjectName    = "LOOP_NOW_DEFAULT_PROJECT_NAME"

	EnvPiBin               = "LOOP_NOW_PI_BIN"
	EnvPiVersion           = "LOOP_NOW_PI_VERSION"
	EnvPiMaxProcesses      = "LOOP_NOW_PI_MAX_PROCESSES"
	EnvPiIdleTimeout       = "LOOP_NOW_PI_IDLE_TIMEOUT"
	EnvPiStartTimeout      = "LOOP_NOW_PI_START_TIMEOUT"
	EnvPiStopGrace         = "LOOP_NOW_PI_STOP_GRACE"
	EnvAbortTimeout        = "LOOP_NOW_ABORT_TIMEOUT"
	EnvGitCloneTimeout     = "LOOP_NOW_GIT_CLONE_TIMEOUT"
	EnvCLIProbeConcurrency = "LOOP_NOW_CLI_PROBE_CONCURRENCY"
	EnvCLIProbeTimeout     = "LOOP_NOW_CLI_PROBE_TIMEOUT"
	EnvCLIInstallTimeout   = "LOOP_NOW_CLI_INSTALL_TIMEOUT"
	EnvCLITemplateDir      = "LOOP_NOW_CLI_TEMPLATE_DIR"
	EnvLarkUserBundleDir   = "LOOP_NOW_LARK_USER_BUNDLE_DIR"
	EnvExecBasePath        = "LOOP_NOW_EXEC_BASE_PATH"
	EnvExecEnvPassthrough  = "LOOP_NOW_EXEC_ENV_PASSTHROUGH"
	EnvNodeBin             = "LOOP_NOW_NODE_BIN"
	EnvPluginHostEntry     = "LOOP_NOW_PLUGIN_HOST_ENTRY"
	EnvPluginOfficialDir   = "LOOP_NOW_PLUGIN_OFFICIAL_DIR"
	EnvPluginMarketURL     = "LOOP_NOW_PLUGIN_MARKET_URL"
	EnvPiAIDir             = "LOOP_NOW_PI_AI_DIR"

	EnvLockTTL            = "LOOP_NOW_LOCK_TTL"
	EnvLockRenewInterval  = "LOOP_NOW_LOCK_RENEW_INTERVAL"
	EnvReconcileInterval  = "LOOP_NOW_RECONCILE_INTERVAL"
	EnvSSEKeepalive       = "LOOP_NOW_SSE_KEEPALIVE"
	EnvEventStreamMaxLen  = "LOOP_NOW_EVENT_STREAM_MAXLEN"
	EnvRunTextTTL         = "LOOP_NOW_RUN_TEXT_TTL"
	EnvOverviewMaxLen     = "LOOP_NOW_OVERVIEW_STREAM_MAXLEN"
	EnvPiStderrTail       = "LOOP_NOW_PI_STDERR_TAIL_BYTES"
	EnvShutdownTimeout    = "LOOP_NOW_SHUTDOWN_TIMEOUT"
	EnvMaxUploadFile      = "LOOP_NOW_MAX_UPLOAD_FILE"
	EnvMaxFolderUpload    = "LOOP_NOW_MAX_FOLDER_UPLOAD"
	EnvMaxSkillArchive    = "LOOP_NOW_MAX_SKILL_ARCHIVE"
	EnvMaxSkillEntries    = "LOOP_NOW_MAX_SKILL_ENTRIES"
	EnvMaxCredential      = "LOOP_NOW_MAX_CREDENTIAL"
	EnvMaxMessage         = "LOOP_NOW_MAX_MESSAGE"
	EnvMaxInboxMessage    = "LOOP_NOW_MAX_INBOX_MESSAGE"
	EnvInboxQuestionTTL   = "LOOP_NOW_INBOX_QUESTION_TTL"
	EnvInboxPreviewChars  = "LOOP_NOW_INBOX_PREVIEW_CHARS"
	EnvInboxPageMax       = "LOOP_NOW_INBOX_PAGE_MAX"
	EnvLoopctlDistDir     = "LOOP_NOW_LOOPCTL_DIST_DIR"
	EnvExternalListenAddr = "LOOP_NOW_EXTERNAL_LISTEN_ADDR"
	EnvExternalTLSCert    = "LOOP_NOW_EXTERNAL_TLS_CERT"
	EnvExternalTLSKey     = "LOOP_NOW_EXTERNAL_TLS_KEY"
	EnvTextEditExtensions = "LOOP_NOW_TEXT_EDIT_EXTENSIONS"
	EnvTextEditMax        = "LOOP_NOW_TEXT_EDIT_MAX"
	EnvLogFormat          = "LOOP_NOW_LOG_FORMAT"
	EnvLogLevel           = "LOOP_NOW_LOG_LEVEL"
)

// Config 是服务端的全部配置。
type Config struct {
	MaxMessageImages                                           int
	MaxMessageImageBytes, MaxMessageImagePixels                int64
	CLIImportScanTTL, CLIImportTimeout, CLIImportRecordTimeout time.Duration
	CLIImportMaxBatch, CLIImportMaxActive, CLIImportMaxScans   int
	SleepScanInterval                                          time.Duration
	SleepScanBatch                                             int
	AuthLoginTimeout                                           time.Duration
	AuthRefreshTimeout                                         time.Duration
	AuthRecordTimeout                                          time.Duration
	AuthIPCTimeout                                             time.Duration
	TaskRecordTimeout                                          time.Duration
	TaskEventPoll                                              time.Duration
	TaskReconcileInterval                                      time.Duration
	TaskMaxSession                                             int64
	TaskMaxArtifact                                            int64
	TaskMaxRequest                                             int64
	TaskMaxVisits                                              int
	TaskMaxEdges                                               int
	TaskMaxSteps                                               int
	DatabaseURL                                                string
	RedisURL                                                   string
	ListenAddr                                                 string
	AllowedHosts                                               []string
	ConsoleOrigins                                             []string
	WorkRoot                                                   string
	// ContextDir 是项目与全局上下文文件夹的根（projects/<projectId>/、global/），默认是服务进程用户 HOME 下的 .loop_now。
	ContextDir         string
	RedisKeyPrefix     string
	DefaultOrgName     string
	DefaultProjectName string

	PiBin               string
	PiVersion           string
	PiMaxProcesses      int
	PiIdleTimeout       time.Duration
	PiStartTimeout      time.Duration
	PiStopGrace         time.Duration
	AbortTimeout        time.Duration
	GitCloneTimeout     time.Duration
	CLIProbeConcurrency int
	CLIProbeTimeout     time.Duration
	CLIInstallTimeout   time.Duration
	CLITemplateDir      string
	LarkUserBundleDir   string // 为空时查找同版发行内的独立用户CLI包
	ExecBasePath        string // 为空时在启动检查中推导
	ExecEnvPassthrough  []string
	NodeBin             string
	PiAIDir             string // 为空时在启动检查中推导
	// PluginHostEntry 是插件宿主的入口文件（node 运行它）；为空时插件的平台半边不运行。
	PluginHostEntry string
	// PluginOfficialDir 是随平台发布的官方插件目录；为空时没有 builtin 安装来源。
	PluginOfficialDir string
	// PluginMarketURL 是官方插件市场 loop-now-market.json 的 https 地址；默认是官方市场的索引地址，可改成别的地址。
	PluginMarketURL string

	LockTTL           time.Duration
	LockRenewInterval time.Duration
	ReconcileInterval time.Duration
	SSEKeepalive      time.Duration
	EventStreamMaxLen int64
	RunTextTTL        time.Duration
	OverviewMaxLen    int64
	PiStderrTailBytes int
	ShutdownTimeout   time.Duration
	MaxUploadFile     int64
	MaxFolderUpload   int64 // Agent 文件夹与上下文文件夹单个上传文件的字节上限
	MaxSkillArchive   int64
	MaxSkillEntries   int
	MaxCredential     int64
	MaxMessage        int64
	MaxInboxMessage   int64 // Inbox 消息正文的字节上限
	InboxQuestionTTL  time.Duration
	InboxPreviewChars int // Inbox 摘要中正文的字符数
	InboxPageMax      int // Inbox 列表每页条数的上限
	// LoopctlDistDir 是 loopctl 构建产物的根目录，其中 <版本>/manifest.json 必须与服务端同版本。
	LoopctlDistDir string
	// ExternalListenAddr 为空时不开启对外端口；开启时必须配置 TLS 证书与私钥。
	ExternalListenAddr string
	ExternalTLSCert    string
	ExternalTLSKey     string
	TextEditExtensions []string
	TextEditMax        int64
	LogFormat          string     // text 或 json
	LogLevel           slog.Level // debug、info、warn、error

	// Warnings 是不阻止启动、但需要醒目提示的问题，例如监听非回环地址。
	Warnings []string
}

// DefaultWorkRoot 返回当前平台的工作根目录默认值。
func DefaultWorkRoot() string {
	if runtime.GOOS == "darwin" {
		return "/Users/Shared/loop-now"
	}
	return "/var/lib/loop-now"
}

// DefaultPluginMarketURL 是官方插件市场索引仓库 AI-for-anyone/loop-now-market 的 loop-now-market.json。
const DefaultPluginMarketURL = "https://raw.githubusercontent.com/AI-for-anyone/loop-now-market/main/loop-now-market.json"

const defaultTextEditExtensions = ".md,.markdown,.txt,.json,.yaml,.yml,.toml,.ini,.csv,.xml,.html,.css,.js,.ts,.py,.go,.sh,.sql"

// Load 按 getenv 读取配置。getenv 通常为 os.Getenv，测试中可替换。
func Load(getenv func(string) string) (*Config, error) {
	p := parser{getenv: getenv}
	c := &Config{
		AuthLoginTimeout: p.duration(EnvAuthLoginTimeout, "15m"), AuthRefreshTimeout: p.duration(EnvAuthRefreshTimeout, "15s"), AuthRecordTimeout: p.duration(EnvAuthRecordTimeout, "5s"), AuthIPCTimeout: p.duration(EnvAuthIPCTimeout, "30s"),
		SleepScanInterval:     p.duration(EnvSleepScanInterval, "1s"),
		SleepScanBatch:        p.integer(EnvSleepScanBatch, 100),
		TaskRecordTimeout:     p.duration(EnvTaskRecordTimeout, "5s"),
		TaskEventPoll:         p.duration(EnvTaskEventPoll, "100ms"),
		TaskReconcileInterval: p.duration(EnvTaskReconcileInterval, "1s"),
		TaskMaxSession:        p.size(EnvTaskMaxSession, "100MiB"),
		TaskMaxArtifact:       p.size(EnvTaskMaxArtifact, "20MiB"),
		TaskMaxRequest:        p.size(EnvTaskMaxRequest, "1MiB"),
		TaskMaxVisits:         p.integer(EnvTaskMaxVisits, 1000),
		TaskMaxEdges:          p.integer(EnvTaskMaxEdges, 300),
		TaskMaxSteps:          p.integer(EnvTaskMaxSteps, 100),
		DatabaseURL:           p.required(EnvDatabaseURL),
		RedisURL:              p.required(EnvRedisURL),
		ListenAddr:            p.str(EnvListenAddr, "127.0.0.1:8780"),
		AllowedHosts:          p.list(EnvAllowedHosts, "localhost,127.0.0.1,[::1]"),
		ConsoleOrigins:        p.list(EnvConsoleOrigins, "http://localhost:5173,http://127.0.0.1:5173"),
		WorkRoot:              p.str(EnvWorkRoot, DefaultWorkRoot()),
		RedisKeyPrefix:        p.str(EnvRedisKeyPrefix, "loopnow:"),
		// 只在全新安装时用来创建默认组织与项目；两者都出现在 Agent 地址里（项目名还出现在 URL 里），所以用不需要转义的英文。
		DefaultOrgName:     p.str(EnvDefaultOrgName, "default"),
		DefaultProjectName: p.str(EnvDefaultProjectName, "default"),

		PiBin:               p.str(EnvPiBin, "pi"),
		PiVersion:           p.str(EnvPiVersion, "1.4.0"),
		PiMaxProcesses:      p.integer(EnvPiMaxProcesses, 0),
		PiIdleTimeout:       p.duration(EnvPiIdleTimeout, "10m"),
		PiStartTimeout:      p.duration(EnvPiStartTimeout, "60s"),
		PiStopGrace:         p.duration(EnvPiStopGrace, "5s"),
		AbortTimeout:        p.duration(EnvAbortTimeout, "30s"),
		GitCloneTimeout:     p.duration(EnvGitCloneTimeout, "10m"),
		CLIProbeConcurrency: p.integer(EnvCLIProbeConcurrency, 4),
		CLIProbeTimeout:     p.duration(EnvCLIProbeTimeout, "30s"),
		CLIInstallTimeout:   p.duration(EnvCLIInstallTimeout, "5m"),
		CLITemplateDir:      p.str(EnvCLITemplateDir, ""),
		LarkUserBundleDir:   p.str(EnvLarkUserBundleDir, ""),
		ExecBasePath:        p.str(EnvExecBasePath, ""),
		ExecEnvPassthrough:  p.list(EnvExecEnvPassthrough, "LANG,LC_ALL,TZ"),
		NodeBin:             p.str(EnvNodeBin, "node"),
		PiAIDir:             p.str(EnvPiAIDir, ""),

		LockTTL:           p.duration(EnvLockTTL, "30s"),
		LockRenewInterval: p.duration(EnvLockRenewInterval, "10s"),
		ReconcileInterval: p.duration(EnvReconcileInterval, "5s"),
		SSEKeepalive:      p.duration(EnvSSEKeepalive, "15s"),
		EventStreamMaxLen: int64(p.integer(EnvEventStreamMaxLen, 5000)),
		RunTextTTL:        p.duration(EnvRunTextTTL, "24h"),
		OverviewMaxLen:    int64(p.integer(EnvOverviewMaxLen, 1000)),
		PiStderrTailBytes: p.integer(EnvPiStderrTail, 1000),
		ShutdownTimeout:   p.duration(EnvShutdownTimeout, "15s"),
		MaxUploadFile:     p.size(EnvMaxUploadFile, "10MiB"),
		MaxFolderUpload:   p.size(EnvMaxFolderUpload, "1GiB"),
		MaxSkillArchive:   p.size(EnvMaxSkillArchive, "20MiB"),
		MaxSkillEntries:   p.integer(EnvMaxSkillEntries, 500),
		MaxCredential:     p.size(EnvMaxCredential, "64KiB"),
		MaxMessage:        p.size(EnvMaxMessage, "64KiB"),
		MaxMessageImages:  p.integer(EnvMaxMessageImages, 4), MaxMessageImageBytes: p.size(EnvMaxMessageImageBytes, "5MiB"), MaxMessageImagePixels: int64(p.integer(EnvMaxMessageImagePixels, 20_000_000)),
		MaxInboxMessage:    p.size(EnvMaxInboxMessage, "64KiB"),
		InboxQuestionTTL:   p.duration(EnvInboxQuestionTTL, "168h"),
		InboxPreviewChars:  p.integer(EnvInboxPreviewChars, 200),
		InboxPageMax:       p.integer(EnvInboxPageMax, 100),
		LoopctlDistDir:     p.required(EnvLoopctlDistDir),
		ExternalListenAddr: p.str(EnvExternalListenAddr, ""),
		ExternalTLSCert:    p.str(EnvExternalTLSCert, ""),
		ExternalTLSKey:     p.str(EnvExternalTLSKey, ""),
		TextEditExtensions: p.list(EnvTextEditExtensions, defaultTextEditExtensions),
		TextEditMax:        p.size(EnvTextEditMax, "1MiB"),
		LogFormat:          p.str(EnvLogFormat, "text"),
	}
	c.LogLevel = p.logLevel(EnvLogLevel, "info")
	if c.LogFormat != "text" && c.LogFormat != "json" {
		p.fail(EnvLogFormat, "只能是 text 或 json（当前 %q）", c.LogFormat)
	}
	p.cliImport(c)
	c.ContextDir = p.str(EnvContextDir, "")
	if c.ContextDir == "" {
		home := getenv("HOME")
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		if home != "" {
			c.ContextDir = filepath.Join(home, ".loop_now")
		}
	}
	if !filepath.IsAbs(c.ContextDir) || filepath.Clean(c.ContextDir) != c.ContextDir || c.ContextDir == string(filepath.Separator) {
		p.fail(EnvContextDir, "须为规范的绝对路径（当前 %q；未设置时取 HOME 下的 .loop_now）", c.ContextDir)
	}

	c.PluginHostEntry = p.str(EnvPluginHostEntry, "")
	c.PluginOfficialDir = p.str(EnvPluginOfficialDir, "")
	for env, v := range map[string]string{EnvPluginHostEntry: c.PluginHostEntry, EnvPluginOfficialDir: c.PluginOfficialDir} {
		if v != "" && (!filepath.IsAbs(v) || filepath.Clean(v) != v) {
			p.fail(env, "须为规范的绝对路径（当前 %q）", v)
		}
	}
	c.PluginMarketURL = p.str(EnvPluginMarketURL, DefaultPluginMarketURL)
	if c.PluginMarketURL != "" {
		p.checkURL(EnvPluginMarketURL, c.PluginMarketURL, "https")
	}

	p.checkURL(EnvDatabaseURL, c.DatabaseURL, "postgres", "postgresql")
	p.checkURL(EnvRedisURL, c.RedisURL, "redis", "rediss")
	c.Warnings = append(c.Warnings, p.checkListenAddr(c.ListenAddr)...)
	p.checkExternal(c)
	c.AllowedHosts = p.normalizeHosts(c.AllowedHosts)
	p.checkOrigins(c.ConsoleOrigins)
	c.TextEditExtensions = p.normalizeExtensions(c.TextEditExtensions)
	if c.MaxMessageImages < 1 || c.MaxMessageImages > 8 {
		p.fail(EnvMaxMessageImages, "须为1到8")
	}
	if c.MaxMessageImageBytes < 1 || c.MaxMessageImageBytes > 10<<20 || int64(c.MaxMessageImages)*c.MaxMessageImageBytes > 24<<20 {
		p.fail(EnvMaxMessageImageBytes, "单图须<=10MiB且每消息合计<=24MiB")
	}
	if c.MaxMessageImagePixels < 1 || c.MaxMessageImagePixels > 40_000_000 {
		p.fail(EnvMaxMessageImagePixels, "须为1到40000000")
	}
	p.checkConstraints(c)

	if err := errors.Join(p.errs...); err != nil {
		return nil, err
	}
	return c, nil
}

// LogValue 输出生效配置，连接串中的密码脱敏。
func (c *Config) LogValue() slog.Value {
	return slog.GroupValue(
		c.cliImportLog(),

		slog.Duration("sleepScanInterval", c.SleepScanInterval), slog.Int("sleepScanBatch", c.SleepScanBatch),
		slog.Int("taskMaxSteps", c.TaskMaxSteps),
		slog.Int("taskMaxEdges", c.TaskMaxEdges),
		slog.Int("taskMaxVisits", c.TaskMaxVisits),
		slog.Int64("taskMaxRequest", c.TaskMaxRequest),
		slog.Int64("taskMaxArtifact", c.TaskMaxArtifact),
		slog.Int64("taskMaxSession", c.TaskMaxSession),
		slog.Duration("taskReconcileInterval", c.TaskReconcileInterval),
		slog.Duration("taskEventPoll", c.TaskEventPoll),
		slog.Duration("taskRecordTimeout", c.TaskRecordTimeout),
		slog.Duration("authLoginTimeout", c.AuthLoginTimeout), slog.Duration("authRefreshTimeout", c.AuthRefreshTimeout), slog.Duration("authRecordTimeout", c.AuthRecordTimeout), slog.Duration("authIPCTimeout", c.AuthIPCTimeout),
		slog.String("databaseURL", RedactURL(c.DatabaseURL)),
		slog.String("redisURL", RedactURL(c.RedisURL)),
		slog.String("listenAddr", c.ListenAddr),
		slog.Any("allowedHosts", c.AllowedHosts),
		slog.Any("consoleOrigins", c.ConsoleOrigins),
		slog.String("workRoot", c.WorkRoot),
		slog.String("contextDir", c.ContextDir),
		slog.String("pluginHostEntry", c.PluginHostEntry),
		slog.String("pluginOfficialDir", c.PluginOfficialDir),
		slog.String("pluginMarketURL", c.PluginMarketURL),
		slog.String("redisKeyPrefix", c.RedisKeyPrefix),
		slog.String("piBin", c.PiBin),
		slog.String("piVersion", c.PiVersion),
		slog.Int("piMaxProcesses", c.PiMaxProcesses),
		slog.Duration("piIdleTimeout", c.PiIdleTimeout),
		slog.Duration("piStartTimeout", c.PiStartTimeout),
		slog.Duration("piStopGrace", c.PiStopGrace),
		slog.Duration("abortTimeout", c.AbortTimeout),
		slog.Duration("gitCloneTimeout", c.GitCloneTimeout),
		slog.Duration("cliInstallTimeout", c.CLIInstallTimeout),
		slog.Duration("cliProbeTimeout", c.CLIProbeTimeout),
		slog.Int("cliProbeConcurrency", c.CLIProbeConcurrency),
		slog.String("cliTemplateDir", c.CLITemplateDir),
		slog.String("larkUserBundleDir", c.LarkUserBundleDir),
		slog.String("execBasePath", c.ExecBasePath),
		slog.Any("execEnvPassthrough", c.ExecEnvPassthrough),
		slog.String("nodeBin", c.NodeBin),
		slog.String("piAIDir", c.PiAIDir),
		slog.Duration("lockTTL", c.LockTTL),
		slog.Duration("lockRenewInterval", c.LockRenewInterval),
		slog.Duration("reconcileInterval", c.ReconcileInterval),
		slog.Duration("sseKeepalive", c.SSEKeepalive),
		slog.Int64("eventStreamMaxLen", c.EventStreamMaxLen),
		slog.Duration("runTextTTL", c.RunTextTTL),
		slog.Int64("overviewStreamMaxLen", c.OverviewMaxLen),
		slog.Int("piStderrTailBytes", c.PiStderrTailBytes),
		slog.Duration("shutdownTimeout", c.ShutdownTimeout),
		slog.Int64("maxUploadFile", c.MaxUploadFile),
		slog.Int64("maxFolderUpload", c.MaxFolderUpload),
		slog.Int64("maxSkillArchive", c.MaxSkillArchive),
		slog.Int("maxSkillEntries", c.MaxSkillEntries),
		slog.Int64("maxCredential", c.MaxCredential),
		slog.Int64("maxMessage", c.MaxMessage),
		slog.Int64("maxInboxMessage", c.MaxInboxMessage),
		slog.Int("inboxPreviewChars", c.InboxPreviewChars),
		slog.Duration("inboxQuestionTTL", c.InboxQuestionTTL),
		slog.Int("inboxPageMax", c.InboxPageMax),
		slog.String("loopctlDistDir", c.LoopctlDistDir),
		slog.String("externalListenAddr", c.ExternalListenAddr),
		slog.String("externalTLSCert", c.ExternalTLSCert),
		slog.String("externalTLSKey", c.ExternalTLSKey),
		slog.Any("textEditExtensions", c.TextEditExtensions),
		slog.Int64("textEditMax", c.TextEditMax),
		slog.String("logFormat", c.LogFormat),
		slog.String("logLevel", c.LogLevel.String()),
	)
}

// NewLogger 按配置的格式与级别创建写到 w 的日志器（服务端只写 stderr，不写日志文件）。
func NewLogger(w io.Writer, format string, level slog.Level) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	if format == "json" {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

func (p *parser) logLevel(name, def string) slog.Level {
	v := strings.ToLower(p.str(name, def))
	switch v {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	p.fail(name, "只能是 debug、info、warn 或 error（当前 %q）", v)
	return slog.LevelInfo
}

// RedactURL 把连接串中的密码替换为 xxxxx；无法解析时整体隐藏。
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable>"
	}
	return u.Redacted()
}

// IsLoopbackHost 判断主机名是否为本机回环地址。
func IsLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type parser struct {
	getenv func(string) string
	errs   []error
}

func (p *parser) fail(name, format string, args ...any) {
	p.errs = append(p.errs, fmt.Errorf("%s: %s", name, fmt.Sprintf(format, args...)))
}

func (p *parser) raw(name string) (string, bool) {
	v := strings.TrimSpace(p.getenv(name))
	return v, v != ""
}

func (p *parser) required(name string) string {
	v, ok := p.raw(name)
	if !ok {
		p.fail(name, "必需配置，未设置")
	}
	return v
}

func (p *parser) str(name, def string) string {
	if v, ok := p.raw(name); ok {
		return v
	}
	return def
}

func (p *parser) list(name, def string) []string {
	v := p.str(name, def)
	var out []string
	for item := range strings.SplitSeq(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func (p *parser) integer(name string, def int) int {
	v, ok := p.raw(name)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		p.fail(name, "不是整数：%q", v)
		return def
	}
	return n
}

func (p *parser) duration(name, def string) time.Duration {
	v := p.str(name, def)
	d, err := time.ParseDuration(v)
	if err != nil {
		p.fail(name, "不是合法的时长（例如 30s、10m）：%q", v)
		return 0
	}
	if d <= 0 {
		p.fail(name, "必须大于 0")
	}
	return d
}

func (p *parser) size(name, def string) int64 {
	v := p.str(name, def)
	n, err := ParseSize(v)
	if err != nil {
		p.fail(name, "%v", err)
		return 0
	}
	if n <= 0 {
		p.fail(name, "必须大于 0")
	}
	return n
}

// ParseSize 解析字节数，支持纯数字或带 KiB、MiB、GiB 单位。
func ParseSize(v string) (int64, error) {
	s := strings.TrimSpace(v)
	mult := int64(1)
	for _, u := range []struct {
		suffix string
		mult   int64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}} {
		if strings.HasSuffix(s, u.suffix) {
			s, mult = strings.TrimSpace(strings.TrimSuffix(s, u.suffix)), u.mult
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("不是合法的大小（例如 10MiB、65536）：%q", v)
	}
	return n * mult, nil
}

func (p *parser) checkURL(name, raw string, schemes ...string) {
	if raw == "" {
		return
	}
	u, err := url.Parse(raw)
	if err != nil || !slices.Contains(schemes, u.Scheme) || u.Host == "" {
		p.fail(name, "不是合法的连接串，需以 %s:// 开头并包含主机", schemes[0])
	}
}

func (p *parser) checkListenAddr(addr string) []string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		p.fail(EnvListenAddr, "不是合法的 host:port：%q", addr)
		return nil
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		p.fail(EnvListenAddr, "端口不合法：%q", port)
		return nil
	}
	if host == "" || !IsLoopbackHost(host) {
		return []string{fmt.Sprintf("监听地址 %s 不是本机回环地址：控制台没有登录，任何能访问该地址的人都能操作服务端，Agent 凭证也会经此服务端下发；请确认这是有意为之，并同时配置 %s", addr, EnvAllowedHosts)}
	}
	return nil
}

// checkExternal 校验对外端口：配置了监听地址时必须同时配置证书与私钥。
func (p *parser) checkExternal(c *Config) {
	if c.ExternalListenAddr == "" {
		return
	}
	if _, port, err := net.SplitHostPort(c.ExternalListenAddr); err != nil {
		p.fail(EnvExternalListenAddr, "不是合法的 host:port：%q", c.ExternalListenAddr)
	} else if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		p.fail(EnvExternalListenAddr, "端口不合法：%q", port)
	}
	if c.ExternalTLSCert == "" {
		p.fail(EnvExternalTLSCert, "开启对外端口时必需（%s 已设置）", EnvExternalListenAddr)
	}
	if c.ExternalTLSKey == "" {
		p.fail(EnvExternalTLSKey, "开启对外端口时必需（%s 已设置）", EnvExternalListenAddr)
	}
}

func (p *parser) normalizeHosts(hosts []string) []string {
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		h = strings.ToLower(h)
		if strings.ContainsAny(h, "/ ") {
			p.fail(EnvAllowedHosts, "不是合法的主机名：%q", h)
			continue
		}
		// 允许的 Host 只比较主机部分，去掉可能写上的端口。
		if hh, _, err := net.SplitHostPort(h); err == nil {
			h = hh
		}
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(h, "["), "]"))
	}
	if len(out) == 0 {
		p.fail(EnvAllowedHosts, "至少需要一个主机名")
	}
	return out
}

func (p *parser) checkOrigins(origins []string) {
	for _, o := range origins {
		u, err := url.Parse(o)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
			p.fail(EnvConsoleOrigins, "不是合法的来源（形如 http://localhost:5173）：%q", o)
		}
	}
}

func (p *parser) normalizeExtensions(exts []string) []string {
	out := make([]string, 0, len(exts))
	for _, e := range exts {
		e = strings.ToLower(e)
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		out = append(out, e)
	}
	return out
}

func (p *parser) checkConstraints(c *Config) {
	for name, value := range map[string]time.Duration{EnvAuthLoginTimeout: c.AuthLoginTimeout, EnvAuthRefreshTimeout: c.AuthRefreshTimeout, EnvAuthRecordTimeout: c.AuthRecordTimeout, EnvAuthIPCTimeout: c.AuthIPCTimeout} {
		if value <= 0 {
			p.fail(name, "必须为正")
		}
	}
	if c.AuthIPCTimeout.Milliseconds() > 2147483647 {
		p.fail(EnvAuthIPCTimeout, "超过私有IPC客户端计时范围")
	}
	if c.SleepScanInterval <= 0 {
		p.fail(EnvSleepScanInterval, "必须为正")
	}
	if c.SleepScanBatch <= 0 {
		p.fail(EnvSleepScanBatch, "必须为正")
	}
	if c.TaskRecordTimeout <= 0 {
		p.fail(EnvTaskRecordTimeout, "必须为正")
	}
	if c.TaskEventPoll <= 0 {
		p.fail(EnvTaskEventPoll, "必须为正")
	}
	if c.CLIProbeConcurrency <= 0 {
		p.fail(EnvCLIProbeConcurrency, "必须为正")
	}
	if c.CLIProbeTimeout <= 0 {
		p.fail(EnvCLIProbeTimeout, "必须为正")
	}
	if c.TaskReconcileInterval <= 0 {
		p.fail(EnvTaskReconcileInterval, "必须为正")
	}
	if c.TaskMaxSession <= 0 {
		p.fail(EnvTaskMaxSession, "必须为正")
	}
	if c.TaskMaxArtifact <= 0 {
		p.fail(EnvTaskMaxArtifact, "必须为正")
	}
	if c.TaskMaxRequest <= 0 {
		p.fail(EnvTaskMaxRequest, "必须为正")
	}
	if c.TaskMaxVisits <= 0 {
		p.fail(EnvTaskMaxVisits, "必须为正")
	}
	if c.TaskMaxEdges <= 0 {
		p.fail(EnvTaskMaxEdges, "必须为正")
	}
	if c.TaskMaxSteps <= 0 {
		p.fail(EnvTaskMaxSteps, "必须为正")
	}
	if c.LockTTL > 0 && c.LockRenewInterval >= c.LockTTL {
		p.fail(EnvLockRenewInterval, "必须小于 %s（当前 %s ≥ %s）", EnvLockTTL, c.LockRenewInterval, c.LockTTL)
	}
	if c.LockTTL > 0 && c.ReconcileInterval >= c.LockTTL {
		p.fail(EnvReconcileInterval, "必须小于 %s（当前 %s ≥ %s）", EnvLockTTL, c.ReconcileInterval, c.LockTTL)
	}
	if msg := naming.Validate("默认组织名", c.DefaultOrgName); msg != "" {
		p.fail(EnvDefaultOrgName, "%s", msg)
	}
	if msg := naming.Validate("默认项目名", c.DefaultProjectName); msg != "" {
		p.fail(EnvDefaultProjectName, "%s", msg)
	}
	if c.PiMaxProcesses < 0 {
		p.fail(EnvPiMaxProcesses, "不能为负数；0 表示不限")
	}
	if c.MaxSkillEntries < 1 {
		p.fail(EnvMaxSkillEntries, "必须至少为 1")
	}
	if c.EventStreamMaxLen < 1 {
		p.fail(EnvEventStreamMaxLen, "必须至少为 1")
	}
	if c.OverviewMaxLen < 1 {
		p.fail(EnvOverviewMaxLen, "必须至少为 1")
	}
	if c.PiStderrTailBytes < 1 {
		p.fail(EnvPiStderrTail, "必须至少为 1")
	}
	if c.InboxPreviewChars < 1 {
		p.fail(EnvInboxPreviewChars, "必须至少为 1")
	}
	if c.InboxPageMax < 1 {
		p.fail(EnvInboxPageMax, "必须至少为 1")
	}
	if c.TextEditMax > 0 && c.MaxUploadFile > 0 && c.TextEditMax > c.MaxUploadFile {
		p.fail(EnvTextEditMax, "不能大于 %s", EnvMaxUploadFile)
	}
	if c.RedisKeyPrefix == "" {
		p.fail(EnvRedisKeyPrefix, "不能为空")
	}
}
