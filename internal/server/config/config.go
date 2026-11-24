// Package config parses and validates the server's environment variable configuration.
//
// Every item has a default and can be overridden;
// runtime parameters in business code such as timeouts and limits should all come from here rather than being
// hard-coded elsewhere.
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
	"slices"
	"strconv"
	"strings"
	"time"

	"loop-now/internal/server/naming"
)

// Environment variable names.
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
	EnvDataDir               = "LOOP_NOW_DATA_DIR"
	EnvBackupDir             = "LOOP_NOW_BACKUP_DIR"
	EnvBackupKeep            = "LOOP_NOW_BACKUP_KEEP"
	EnvListenAddr            = "LOOP_NOW_LISTEN_ADDR"
	EnvAllowedHosts          = "LOOP_NOW_ALLOWED_HOSTS"
	EnvConsoleOrigins        = "LOOP_NOW_CONSOLE_ORIGINS"
	EnvWebDir                = "LOOP_NOW_WEB_DIR"
	EnvReadyFile             = "LOOP_NOW_READY_FILE"
	EnvMaxMessageImages      = "LOOP_NOW_MAX_MESSAGE_IMAGES"
	EnvMaxMessageImageBytes  = "LOOP_NOW_MAX_MESSAGE_IMAGE_BYTES"
	EnvMaxMessageImagePixels = "LOOP_NOW_MAX_MESSAGE_IMAGE_PIXELS"
	EnvWorkRoot              = "LOOP_NOW_WORK_ROOT"
	EnvContextDir            = "LOOP_NOW_CONTEXT_DIR"
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

	EnvReconcileInterval = "LOOP_NOW_RECONCILE_INTERVAL"
	EnvSSEKeepalive      = "LOOP_NOW_SSE_KEEPALIVE"
	EnvEventStreamMaxLen = "LOOP_NOW_EVENT_STREAM_MAXLEN"
	EnvOverviewMaxLen    = "LOOP_NOW_OVERVIEW_STREAM_MAXLEN"
	EnvPiStderrTail      = "LOOP_NOW_PI_STDERR_TAIL_BYTES"
	EnvShutdownTimeout   = "LOOP_NOW_SHUTDOWN_TIMEOUT"
	// EnvStopOnStdinClose, when true, treats EOF (or an error) on standard input like receiving SIGTERM: the shell that
	// started the server can stop it gracefully by closing the write end. Windows has no termination signal that can be
	// delivered, so the desktop shell relies on this; off by default, and interactive runs do not read standard input.
	EnvStopOnStdinClose  = "LOOP_NOW_STOP_ON_STDIN_CLOSE"
	EnvMaxUploadFile     = "LOOP_NOW_MAX_UPLOAD_FILE"
	EnvMaxFolderUpload   = "LOOP_NOW_MAX_FOLDER_UPLOAD"
	EnvMaxSkillArchive   = "LOOP_NOW_MAX_SKILL_ARCHIVE"
	EnvMaxSkillEntries   = "LOOP_NOW_MAX_SKILL_ENTRIES"
	EnvMaxCredential     = "LOOP_NOW_MAX_CREDENTIAL"
	EnvMaxMessage        = "LOOP_NOW_MAX_MESSAGE"
	EnvMaxInboxMessage   = "LOOP_NOW_MAX_INBOX_MESSAGE"
	EnvInboxQuestionTTL  = "LOOP_NOW_INBOX_QUESTION_TTL"
	EnvInboxPreviewChars = "LOOP_NOW_INBOX_PREVIEW_CHARS"
	EnvInboxPageMax      = "LOOP_NOW_INBOX_PAGE_MAX"
	// EnvContextHandoffTokens is the budget, in tokens counted as one per UTF-8 byte, of the conversation carried to a
	// new runtime after a change of runtime (clamped to 1024..64000).
	EnvContextHandoffTokens = "LOOP_NOW_CONTEXT_HANDOFF_TOKENS"
	// EnvCodexModelsRefresh is how often the models the local Codex lists are read again (a short-lived codex
	// app-server); 0 turns it off.
	EnvCodexModelsRefresh = "LOOP_NOW_CODEX_MODELS_REFRESH"
	// EnvACPBackgroundWait is how long a turn that waits for an agent's background commands (Antigravity's, after its
	// reply has ended) may hear nothing from the agent before it ends and closes the commands without a result.
	EnvACPBackgroundWait  = "LOOP_NOW_ACP_BACKGROUND_WAIT"
	EnvLoopctlDistDir     = "LOOP_NOW_LOOPCTL_DIST_DIR"
	EnvExternalListenAddr = "LOOP_NOW_EXTERNAL_LISTEN_ADDR"
	EnvExternalTLSCert    = "LOOP_NOW_EXTERNAL_TLS_CERT"
	EnvExternalTLSKey     = "LOOP_NOW_EXTERNAL_TLS_KEY"
	// EnvExternalTLSSelfSigned is a directory: the external port uses a self-signed certificate maintained by the
	// server itself instead (cert.pem and key.pem in the directory, generated when missing or expired). It is mutually
	// exclusive with the certificate/private key pair. The desktop shell uses it to open the external port, and loopctl
	// uses cert.pem as its --ca-file.
	EnvExternalTLSSelfSigned = "LOOP_NOW_EXTERNAL_TLS_SELF_SIGNED"
	EnvTextEditExtensions    = "LOOP_NOW_TEXT_EDIT_EXTENSIONS"
	EnvTextEditMax           = "LOOP_NOW_TEXT_EDIT_MAX"
	EnvLogFormat             = "LOOP_NOW_LOG_FORMAT"
	EnvLogLevel              = "LOOP_NOW_LOG_LEVEL"
)

// Config is the complete server configuration.
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
	// DataDir is the data root: the database files are under DataDir/db (see DBDir). It defaults to .loop_now in the
	// HOME of the server process's user, computed once at startup.
	DataDir string
	// BackupDir is the directory for daily backups, default DataDir/backups; BackupKeep is how many to keep.
	BackupDir    string
	BackupKeep   int
	ListenAddr   string
	AllowedHosts []string
	// ConsoleOrigins are the origins allowed cross-origin access to the console API (the vite dev server); the console
	// served by the server itself is same-origin and need not be listed.
	// Set to none it is empty, and no cross-origin access is allowed.
	ConsoleOrigins []string
	// WebDir, when non-empty, serves the console from this directory instead of the one embedded in the binary.
	WebDir string
	// ReadyFile, when non-empty, is where {"url","pid","version"} is written atomically once the server starts serving
	// (the desktop shell finds the port from it); it is deleted on exit.
	ReadyFile string
	WorkRoot  string
	// ContextDir is the root of the project and global context folders (projects/<projectId>/, global/); it defaults to
	// .loop_now in the HOME of the server process's user.
	ContextDir         string
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
	LarkUserBundleDir   string // empty: look for the standalone user CLI bundle in the release of the same version
	ExecBasePath        string // empty: derived during the startup checks
	ExecEnvPassthrough  []string
	NodeBin             string
	PiAIDir             string // empty: derived during the startup checks
	// PluginHostEntry is the plugin host's entry file (run by node); when empty, the platform side of plugins does not
	// run.
	PluginHostEntry string
	// PluginOfficialDir is the directory of official plugins shipped with the platform; when empty there is no builtin
	// install source.
	PluginOfficialDir string
	// PluginMarketURL is the https URL of the official plugin market's loop-now-market.json; it defaults to the official
	// market's index URL and can be changed to another URL.
	PluginMarketURL string

	ReconcileInterval time.Duration
	SSEKeepalive      time.Duration
	EventStreamMaxLen int64
	OverviewMaxLen    int64
	PiStderrTailBytes int
	ShutdownTimeout   time.Duration
	StopOnStdinClose  bool // start a graceful shutdown when standard input closes (see EnvStopOnStdinClose)
	MaxUploadFile     int64
	MaxFolderUpload   int64 // byte limit for a single uploaded file in Agent folders and context folders
	MaxSkillArchive   int64
	MaxSkillEntries   int
	MaxCredential     int64
	MaxMessage        int64
	MaxInboxMessage   int64 // byte limit for an Inbox message body
	InboxQuestionTTL  time.Duration
	InboxPreviewChars int // number of body characters in an Inbox preview
	InboxPageMax      int // maximum number of items per page in the Inbox list
	// ContextHandoffTokens is the budget of the conversation carried to a new runtime (see EnvContextHandoffTokens).
	ContextHandoffTokens int
	// CodexModelsRefresh is how often the models the local Codex lists are read (once at startup, then at this
	// interval); 0 never reads them.
	CodexModelsRefresh time.Duration
	// ACPBackgroundWait: see EnvACPBackgroundWait.
	ACPBackgroundWait time.Duration
	// LoopctlDistDir is the root directory of the loopctl build output; <version>/manifest.json in it must match the
	// server's version.
	// When unset, loopctl-dist next to loop-server (the release bundle) is used; if neither exists, startup is refused.
	LoopctlDistDir string
	// ExternalListenAddr, when empty, keeps the external port closed; when it is open, a TLS certificate and private key
	// must be configured, or the server uses the self-signed certificate in the ExternalTLSSelfSigned directory.
	ExternalListenAddr    string
	ExternalTLSCert       string
	ExternalTLSKey        string
	ExternalTLSSelfSigned string
	TextEditExtensions    []string
	TextEditMax           int64
	LogFormat             string     // text or json
	LogLevel              slog.Level // debug, info, warn, error

	// Warnings are problems that do not block startup but need a prominent notice, such as listening on a non-loopback
	// address.
	Warnings []string
}

// DefaultDataDir returns the default data root: .loop_now under HOME on Unix, %LOCALAPPDATA%\Loop Now\data on
// Windows; empty when it cannot be determined.
func DefaultDataDir(getenv func(string) string) string { return defaultDataDir(getenv) }

// DBDir returns the directory under the data root that holds data.db and events.db.
func DBDir(dataDir string) string { return filepath.Join(dataDir, "db") }

// DefaultWorkRoot returns the platform's default work root: /Users/Shared/loop-now on macOS, /var/lib/loop-now on
// Linux, %ProgramData%\loop-now on Windows. All of them are outside the user's home.
func DefaultWorkRoot() string { return defaultWorkRoot() }

// Layout of the release bundle: these files sit in the same directory as loop-server (see
// scripts/release/build.sh). When the matching setting is unset and the file exists, it is used by default, so an
// unpacked release bundle needs no extra configuration; environment variables still take precedence.
// The Windows bundle has loop-server.exe, loopctl.exe and runtime/node/node.exe, with the other relative paths the
// same; the Pi entry is cli.js, run by the bundled node.
const (
	BundledNode           = bundledNode
	BundledPi             = "runtime/pi/node_modules/@earendil-works/pi-coding-agent/dist/bundle/cli.js"
	BundledLoopctlDist    = "loopctl-dist"
	BundledPluginHost     = "plugins/host/main.js"
	BundledPluginOfficial = "plugins/official"
)

// executableDir returns the directory of this program's executable (with symlinks resolved); empty when it cannot be
// determined. Tests may replace it.
var executableDir = func() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	return filepath.Dir(p)
}

// bundled returns rel next to the executable, or fallback when it does not exist.
func bundled(rel, fallback string) string {
	dir := executableDir()
	if dir == "" {
		return fallback
	}
	p := filepath.Join(dir, rel)
	if _, err := os.Stat(p); err != nil {
		return fallback
	}
	return p
}

// DefaultPluginMarketURL is loop-now-market.json in AI-for-anyone/loop-now-market, the official plugin market's
// index repository.
const DefaultPluginMarketURL = "https://raw.githubusercontent.com/AI-for-anyone/loop-now-market/main/loop-now-market.json"

const defaultTextEditExtensions = ".md,.markdown,.txt,.json,.yaml,.yml,.toml,.ini,.csv,.xml,.html,.css,.js,.ts,.py,.go,.sh,.sql"

// Load reads the configuration through getenv. getenv is normally os.Getenv and may be replaced in tests.
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
		ListenAddr:            p.str(EnvListenAddr, "127.0.0.1:8780"),
		AllowedHosts:          p.list(EnvAllowedHosts, "localhost,127.0.0.1,[::1]"),
		ConsoleOrigins:        p.list(EnvConsoleOrigins, "http://localhost:5173,http://127.0.0.1:5173"),
		WebDir:                p.str(EnvWebDir, ""),
		ReadyFile:             p.str(EnvReadyFile, ""),
		WorkRoot:              p.str(EnvWorkRoot, DefaultWorkRoot()),
		// Only used to create the default organization and project on a fresh install; both appear in Agent addresses
		// (the project name also appears in URLs), so they are English words that need no escaping.
		DefaultOrgName:     p.str(EnvDefaultOrgName, "default"),
		DefaultProjectName: p.str(EnvDefaultProjectName, "default"),

		PiBin:               p.str(EnvPiBin, bundled(BundledPi, "pi")),
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
		NodeBin:             p.str(EnvNodeBin, bundled(BundledNode, "node")),
		PiAIDir:             p.str(EnvPiAIDir, ""),

		ReconcileInterval: p.duration(EnvReconcileInterval, "5s"),
		SSEKeepalive:      p.duration(EnvSSEKeepalive, "15s"),
		EventStreamMaxLen: int64(p.integer(EnvEventStreamMaxLen, 5000)),
		OverviewMaxLen:    int64(p.integer(EnvOverviewMaxLen, 1000)),
		PiStderrTailBytes: p.integer(EnvPiStderrTail, 1000),
		ShutdownTimeout:   p.duration(EnvShutdownTimeout, "15s"),
		StopOnStdinClose:  p.boolean(EnvStopOnStdinClose, false),
		MaxUploadFile:     p.size(EnvMaxUploadFile, "10MiB"),
		MaxFolderUpload:   p.size(EnvMaxFolderUpload, "1GiB"),
		MaxSkillArchive:   p.size(EnvMaxSkillArchive, "20MiB"),
		MaxSkillEntries:   p.integer(EnvMaxSkillEntries, 500),
		MaxCredential:     p.size(EnvMaxCredential, "64KiB"),
		MaxMessage:        p.size(EnvMaxMessage, "64KiB"),
		MaxMessageImages:  p.integer(EnvMaxMessageImages, 4), MaxMessageImageBytes: p.size(EnvMaxMessageImageBytes, "5MiB"), MaxMessageImagePixels: int64(p.integer(EnvMaxMessageImagePixels, 20_000_000)),
		MaxInboxMessage:       p.size(EnvMaxInboxMessage, "64KiB"),
		InboxQuestionTTL:      p.duration(EnvInboxQuestionTTL, "168h"),
		InboxPreviewChars:     p.integer(EnvInboxPreviewChars, 200),
		InboxPageMax:          p.integer(EnvInboxPageMax, 100),
		ContextHandoffTokens:  p.integer(EnvContextHandoffTokens, 16000),
		CodexModelsRefresh:    p.duration(EnvCodexModelsRefresh, "10m"),
		ACPBackgroundWait:     p.duration(EnvACPBackgroundWait, "30m"),
		LoopctlDistDir:        p.str(EnvLoopctlDistDir, bundled(BundledLoopctlDist, "")),
		ExternalListenAddr:    p.str(EnvExternalListenAddr, ""),
		ExternalTLSCert:       p.str(EnvExternalTLSCert, ""),
		ExternalTLSKey:        p.str(EnvExternalTLSKey, ""),
		ExternalTLSSelfSigned: p.str(EnvExternalTLSSelfSigned, ""),
		TextEditExtensions:    p.list(EnvTextEditExtensions, defaultTextEditExtensions),
		TextEditMax:           p.size(EnvTextEditMax, "1MiB"),
		LogFormat:             p.str(EnvLogFormat, "text"),
	}
	c.LogLevel = p.logLevel(EnvLogLevel, "info")
	if c.LogFormat != "text" && c.LogFormat != "json" {
		p.fail(EnvLogFormat, "Must be text or json (got %q).", c.LogFormat)
	}
	p.cliImport(c)
	c.DataDir = p.str(EnvDataDir, "")
	if c.DataDir == "" {
		c.DataDir = DefaultDataDir(getenv)
	}
	if !filepath.IsAbs(c.DataDir) || filepath.Clean(c.DataDir) != c.DataDir || c.DataDir == string(filepath.Separator) {
		p.fail(EnvDataDir, "Must be a clean absolute path (got %q; when unset, the platform's default directory is used).", c.DataDir)
	}
	c.BackupDir = p.str(EnvBackupDir, "")
	if c.BackupDir == "" && c.DataDir != "" {
		c.BackupDir = filepath.Join(c.DataDir, "backups")
	}
	if !filepath.IsAbs(c.BackupDir) || filepath.Clean(c.BackupDir) != c.BackupDir || c.BackupDir == string(filepath.Separator) {
		p.fail(EnvBackupDir, "Must be a clean absolute path (got %q; when unset, backups under the data root is used).", c.BackupDir)
	}
	c.BackupKeep = p.integer(EnvBackupKeep, 7)
	if c.BackupKeep < 1 {
		p.fail(EnvBackupKeep, "Must be at least 1.")
	}
	c.ContextDir = p.str(EnvContextDir, "")
	if c.ContextDir == "" {
		c.ContextDir = DefaultDataDir(getenv) // the same directory as the data root (.loop_now under HOME on Unix)
	}
	if !filepath.IsAbs(c.ContextDir) || filepath.Clean(c.ContextDir) != c.ContextDir || c.ContextDir == string(filepath.Separator) {
		p.fail(EnvContextDir, "Must be a clean absolute path (got %q; when unset, the same default directory as the data root is used).", c.ContextDir)
	}

	if c.LoopctlDistDir == "" {
		p.fail(EnvLoopctlDistDir, "Required but not set (release packages default to %s next to loop-server).", BundledLoopctlDist)
	}
	c.PluginHostEntry = p.str(EnvPluginHostEntry, bundled(BundledPluginHost, ""))
	c.PluginOfficialDir = p.str(EnvPluginOfficialDir, bundled(BundledPluginOfficial, ""))
	for env, v := range map[string]string{EnvPluginHostEntry: c.PluginHostEntry, EnvPluginOfficialDir: c.PluginOfficialDir} {
		if v != "" && (!filepath.IsAbs(v) || filepath.Clean(v) != v) {
			p.fail(env, "Must be a clean absolute path (got %q).", v)
		}
	}
	c.PluginMarketURL = p.str(EnvPluginMarketURL, DefaultPluginMarketURL)
	if c.PluginMarketURL != "" {
		p.checkURL(EnvPluginMarketURL, c.PluginMarketURL, "https")
	}

	c.Warnings = append(c.Warnings, p.checkListenAddr(c.ListenAddr)...)
	p.checkExternal(c)
	c.AllowedHosts = p.normalizeHosts(c.AllowedHosts)
	if len(c.ConsoleOrigins) == 1 && strings.EqualFold(c.ConsoleOrigins[0], "none") {
		c.ConsoleOrigins = nil
	}
	p.checkOrigins(c.ConsoleOrigins)
	for env, v := range map[string]string{EnvWebDir: c.WebDir, EnvReadyFile: c.ReadyFile} {
		if v != "" && (!filepath.IsAbs(v) || filepath.Clean(v) != v) {
			p.fail(env, "Must be a clean absolute path (got %q).", v)
		}
	}
	c.TextEditExtensions = p.normalizeExtensions(c.TextEditExtensions)
	if c.MaxMessageImages < 1 || c.MaxMessageImages > 8 {
		p.fail(EnvMaxMessageImages, "Must be between 1 and 8.")
	}
	if c.MaxMessageImageBytes < 1 || c.MaxMessageImageBytes > 10<<20 || int64(c.MaxMessageImages)*c.MaxMessageImageBytes > 24<<20 {
		p.fail(EnvMaxMessageImageBytes, "Must be between 1 byte and 10MiB per image, and this value times LOOP_NOW_MAX_MESSAGE_IMAGES must be at most 24MiB.")
	}
	if c.MaxMessageImagePixels < 1 || c.MaxMessageImagePixels > 40_000_000 {
		p.fail(EnvMaxMessageImagePixels, "Must be between 1 and 40000000.")
	}
	p.checkConstraints(c)

	if err := errors.Join(p.errs...); err != nil {
		return nil, err
	}
	return c, nil
}

// LogValue outputs the effective configuration, with passwords in connection strings redacted.
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
		slog.String("dataDir", c.DataDir),
		slog.String("backupDir", c.BackupDir),
		slog.Int("backupKeep", c.BackupKeep),
		slog.String("listenAddr", c.ListenAddr),
		slog.Any("allowedHosts", c.AllowedHosts),
		slog.Any("consoleOrigins", c.ConsoleOrigins),
		slog.String("webDir", c.WebDir),
		slog.String("readyFile", c.ReadyFile),
		slog.String("workRoot", c.WorkRoot),
		slog.String("contextDir", c.ContextDir),
		slog.String("pluginHostEntry", c.PluginHostEntry),
		slog.String("pluginOfficialDir", c.PluginOfficialDir),
		slog.String("pluginMarketURL", c.PluginMarketURL),
		slog.String("piBin", c.PiBin),
		slog.String("piVersion", c.PiVersion),
		slog.Int("piMaxProcesses", c.PiMaxProcesses),
		slog.Duration("piIdleTimeout", c.PiIdleTimeout),
		slog.Duration("piStartTimeout", c.PiStartTimeout),
		slog.Duration("piStopGrace", c.PiStopGrace),
		slog.Duration("abortTimeout", c.AbortTimeout),
		slog.Duration("acpBackgroundWait", c.ACPBackgroundWait),
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
		slog.Duration("reconcileInterval", c.ReconcileInterval),
		slog.Duration("sseKeepalive", c.SSEKeepalive),
		slog.Int64("eventStreamMaxLen", c.EventStreamMaxLen),
		slog.Int64("overviewStreamMaxLen", c.OverviewMaxLen),
		slog.Int("piStderrTailBytes", c.PiStderrTailBytes),
		slog.Duration("shutdownTimeout", c.ShutdownTimeout),
		slog.Bool("stopOnStdinClose", c.StopOnStdinClose),
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
		slog.String("externalTLSSelfSigned", c.ExternalTLSSelfSigned),
		slog.Any("textEditExtensions", c.TextEditExtensions),
		slog.Int64("textEditMax", c.TextEditMax),
		slog.String("logFormat", c.LogFormat),
		slog.String("logLevel", c.LogLevel.String()),
	)
}

// NewLogger creates a logger writing to w with the configured format and level (the server only writes to stderr,
// never to log files).
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
	p.fail(name, "Must be debug, info, warn or error (got %q).", v)
	return slog.LevelInfo
}

// RedactURL replaces the password in a connection string with xxxxx; when it cannot be parsed, the whole string is
// hidden.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable>"
	}
	return u.Redacted()
}

// IsLoopbackHost reports whether the host name is a local loopback address.
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
		p.fail(name, "Not an integer: %q", v)
		return def
	}
	return n
}

// boolean reads a switch: 1/true/yes/on is true, 0/false/no/off is false (case-insensitive), unset or empty gives
// def, and any other value is an error.
func (p *parser) boolean(name string, def bool) bool {
	v, ok := p.raw(name)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	p.fail(name, "Must be 1/true/yes/on or 0/false/no/off (got %q).", v)
	return def
}

func (p *parser) duration(name, def string) time.Duration {
	v := p.str(name, def)
	d, err := time.ParseDuration(v)
	if err != nil {
		p.fail(name, "Not a valid duration (for example 30s or 10m): %q", v)
		return 0
	}
	if d <= 0 {
		p.fail(name, "Must be greater than 0.")
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
		p.fail(name, "Must be greater than 0.")
	}
	return n
}

// ParseSize parses a byte count, either a plain number or one with a KiB, MiB or GiB unit.
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
		return 0, fmt.Errorf("not a valid size (for example 10MiB or 65536): %q", v)
	}
	return n * mult, nil
}

func (p *parser) checkURL(name, raw string, schemes ...string) {
	if raw == "" {
		return
	}
	u, err := url.Parse(raw)
	if err != nil || !slices.Contains(schemes, u.Scheme) || u.Host == "" {
		p.fail(name, "Not a valid URL. It must start with %s:// and include a host.", schemes[0])
	}
}

func (p *parser) checkListenAddr(addr string) []string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		p.fail(EnvListenAddr, "Not a valid host:port: %q", addr)
		return nil
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		p.fail(EnvListenAddr, "Invalid port: %q", port)
		return nil
	}
	if host == "" || !IsLoopbackHost(host) {
		return []string{fmt.Sprintf("The listen address %s is not a loopback address. The console has no login, so anyone who can reach this address can operate the server, and Agent credentials are also handed out through this server. Make sure this is intended, and set %s as well.", addr, EnvAllowedHosts)}
	}
	return nil
}

// checkExternal validates the external port: when a listen address is configured, either both certificate and
// private key must be configured, or only the self-signed certificate directory (a clean absolute path).
func (p *parser) checkExternal(c *Config) {
	if c.ExternalTLSSelfSigned != "" {
		if !filepath.IsAbs(c.ExternalTLSSelfSigned) || filepath.Clean(c.ExternalTLSSelfSigned) != c.ExternalTLSSelfSigned {
			p.fail(EnvExternalTLSSelfSigned, "Must be a clean absolute path (got %q).", c.ExternalTLSSelfSigned)
		}
		if c.ExternalTLSCert != "" || c.ExternalTLSKey != "" {
			p.fail(EnvExternalTLSSelfSigned, "Must not be set together with %s or %s.", EnvExternalTLSCert, EnvExternalTLSKey)
		}
	}
	if c.ExternalListenAddr == "" {
		return
	}
	if _, port, err := net.SplitHostPort(c.ExternalListenAddr); err != nil {
		p.fail(EnvExternalListenAddr, "Not a valid host:port: %q", c.ExternalListenAddr)
	} else if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		p.fail(EnvExternalListenAddr, "Invalid port: %q", port)
	}
	if c.ExternalTLSSelfSigned != "" {
		return
	}
	if c.ExternalTLSCert == "" {
		p.fail(EnvExternalTLSCert, "Required when the external port is enabled (%s is set). Alternatively, set %s to use a self-signed certificate.", EnvExternalListenAddr, EnvExternalTLSSelfSigned)
	}
	if c.ExternalTLSKey == "" {
		p.fail(EnvExternalTLSKey, "Required when the external port is enabled (%s is set). Alternatively, set %s to use a self-signed certificate.", EnvExternalListenAddr, EnvExternalTLSSelfSigned)
	}
}

func (p *parser) normalizeHosts(hosts []string) []string {
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		h = strings.ToLower(h)
		if strings.ContainsAny(h, "/ ") {
			p.fail(EnvAllowedHosts, "Not a valid host name: %q", h)
			continue
		}
		// Allowed hosts compare only the host part; strip a port if one was written.
		if hh, _, err := net.SplitHostPort(h); err == nil {
			h = hh
		}
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(h, "["), "]"))
	}
	if len(out) == 0 {
		p.fail(EnvAllowedHosts, "At least one host name is required.")
	}
	return out
}

func (p *parser) checkOrigins(origins []string) {
	for _, o := range origins {
		u, err := url.Parse(o)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
			p.fail(EnvConsoleOrigins, "Not a valid origin (expected a form like http://localhost:5173): %q", o)
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
			p.fail(name, "Must be positive.")
		}
	}
	if c.AuthIPCTimeout.Milliseconds() > 2147483647 {
		p.fail(EnvAuthIPCTimeout, "Exceeds the timer range of the private IPC client (at most 2147483647 milliseconds).")
	}
	if c.SleepScanInterval <= 0 {
		p.fail(EnvSleepScanInterval, "Must be positive.")
	}
	if c.SleepScanBatch <= 0 {
		p.fail(EnvSleepScanBatch, "Must be positive.")
	}
	if c.TaskRecordTimeout <= 0 {
		p.fail(EnvTaskRecordTimeout, "Must be positive.")
	}
	if c.TaskEventPoll <= 0 {
		p.fail(EnvTaskEventPoll, "Must be positive.")
	}
	if c.CLIProbeConcurrency <= 0 {
		p.fail(EnvCLIProbeConcurrency, "Must be positive.")
	}
	if c.CLIProbeTimeout <= 0 {
		p.fail(EnvCLIProbeTimeout, "Must be positive.")
	}
	if c.TaskReconcileInterval <= 0 {
		p.fail(EnvTaskReconcileInterval, "Must be positive.")
	}
	if c.TaskMaxSession <= 0 {
		p.fail(EnvTaskMaxSession, "Must be positive.")
	}
	if c.TaskMaxArtifact <= 0 {
		p.fail(EnvTaskMaxArtifact, "Must be positive.")
	}
	if c.TaskMaxRequest <= 0 {
		p.fail(EnvTaskMaxRequest, "Must be positive.")
	}
	if c.TaskMaxVisits <= 0 {
		p.fail(EnvTaskMaxVisits, "Must be positive.")
	}
	if c.TaskMaxEdges <= 0 {
		p.fail(EnvTaskMaxEdges, "Must be positive.")
	}
	if c.TaskMaxSteps <= 0 {
		p.fail(EnvTaskMaxSteps, "Must be positive.")
	}
	if msg := naming.Validate("Default organization name", c.DefaultOrgName); msg != "" {
		p.fail(EnvDefaultOrgName, "%s", msg)
	}
	if msg := naming.Validate("Default project name", c.DefaultProjectName); msg != "" {
		p.fail(EnvDefaultProjectName, "%s", msg)
	}
	if c.PiMaxProcesses < 0 {
		p.fail(EnvPiMaxProcesses, "Must not be negative. 0 means no limit.")
	}
	if c.MaxSkillEntries < 1 {
		p.fail(EnvMaxSkillEntries, "Must be at least 1.")
	}
	if c.EventStreamMaxLen < 1 {
		p.fail(EnvEventStreamMaxLen, "Must be at least 1.")
	}
	if c.OverviewMaxLen < 1 {
		p.fail(EnvOverviewMaxLen, "Must be at least 1.")
	}
	if c.PiStderrTailBytes < 1 {
		p.fail(EnvPiStderrTail, "Must be at least 1.")
	}
	if c.InboxPreviewChars < 1 {
		p.fail(EnvInboxPreviewChars, "Must be at least 1.")
	}
	if c.InboxPageMax < 1 {
		p.fail(EnvInboxPageMax, "Must be at least 1.")
	}
	if c.TextEditMax > 0 && c.MaxUploadFile > 0 && c.TextEditMax > c.MaxUploadFile {
		p.fail(EnvTextEditMax, "Must not be greater than %s.", EnvMaxUploadFile)
	}
}
