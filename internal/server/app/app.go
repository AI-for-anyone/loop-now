// Package app assembles the modules into a server that can be started and stopped.
package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	authhelper "loop-now/auth-helper"
	"loop-now/internal/buildinfo"
	"loop-now/internal/execenv"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/cliauth"
	"loop-now/internal/server/cliimport"
	"loop-now/internal/server/clitemplate"
	cliverifyservice "loop-now/internal/server/cliverify/service"
	"loop-now/internal/server/config"
	"loop-now/internal/server/content"
	"loop-now/internal/server/conversation"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/db"
	"loop-now/internal/server/executor/local"
	"loop-now/internal/server/executor/local/pirpc"
	"loop-now/internal/server/folders"
	"loop-now/internal/server/httpapi"
	"loop-now/internal/server/inbox"
	"loop-now/internal/server/larkauth"
	"loop-now/internal/server/localmode"
	"loop-now/internal/server/loopctldist"
	"loop-now/internal/server/maintenance"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/modelconfig"
	"loop-now/internal/server/pluginhost"
	"loop-now/internal/server/plugins"
	"loop-now/internal/server/preflight"
	"loop-now/internal/server/providerstate"
	"loop-now/internal/server/runrecord"
	"loop-now/internal/server/runtimes"
	"loop-now/internal/server/selfsigned"
	"loop-now/internal/server/store"
	"loop-now/internal/server/tasks"
	"loop-now/internal/server/webui"
)

// WorkRootLockFile is the name of the instance lock file in the work root.
const WorkRootLockFile = ".loop-now-server.lock"

// App is the assembled server.
type App struct {
	CLIImport       *cliimport.Service
	CLIVerification *cliverifyservice.Service
	Installer       *clitemplate.Installer
	CLIAuth         *cliauth.Service
	ModelAuth       *modelauth.Service
	ModelSettings   *modelconfig.Service
	// ProviderStates holds the state directories of plugin provider subscription accounts.
	ProviderStates *providerstate.Manager
	Cfg            *config.Config
	InstanceID     uuid.UUID
	Log            *slog.Logger

	Store *store.Store
	Coord *coord.Coord
	// dirLock and workLock are the instance locks on the data directory and the work root; they are released last on
	// close.
	dirLock, workLock *db.DirLock
	// Maintenance periodically runs checkpoints and cleans up old logs and backups.
	Maintenance *maintenance.Service
	Executor    *local.Executor
	Conv        *conversation.Service
	Tasks       *tasks.Service
	Folders     *folders.Service
	Config      *agentconfig.Service
	API         *httpapi.Server
	// LocalMode holds the personal edition's "local mode" switches and list of local directories; UserPath resolves and
	// caches the user's PATH.
	LocalMode *localmode.Service
	UserPath  *localmode.UserPath
	// Plugins handles installation and configuration for plugins v2; PluginHost supervises the plugin host when a host
	// entry point is configured, and is nil otherwise.
	Plugins    *plugins.Service
	PluginHost *pluginhost.Supervisor

	listener net.Listener
	// readyWritten records that this process wrote the ready file, which must be removed on exit.
	readyWritten bool
	http         *http.Server
	baseCancel   context.CancelFunc

	// External port (nil when not configured): TLS, accepts only external tokens.
	extListener net.Listener
	external    *http.Server
}

// Options lets tests replace some dependencies.
type Options struct {
	// CLIImportReaderFactory is an explicit isolated synthetic-source fixture.
	CLIImportReaderFactory func(context.Context) (cliimport.Reader, error)
	// Explicit offline installer transport; nil keeps the production HTTP client.
	CLIHTTPClient          *http.Client
	CLIVerificationCommand cliverifyservice.CommandFactory
	// Auth dependencies are replaceable only in isolated offline integration tests.
	CLILoginHelper  cliauth.LoginHelper
	CLIRefresher    cliauth.Refresher
	LarkLoginHelper cliauth.LarkLoginHelper
	LarkRefresher   larkauth.Refresher
	// LarkHTTPTransport is constructor-only isolation for loopback integration tests.
	// Nil preserves the production fixed HTTPS transport; never configured via env/API.
	LarkHTTPTransport http.RoundTripper
	AuthLoginHelper   modelauth.LoginHelper
	// PluginLoginHelper replaces the login helper process for plugin providers (for tests).
	PluginLoginHelper modelauth.PluginLoginHelper
	AuthRefresher     modelauth.Refresher
	// Listener, when non-nil, is used as is (for tests); otherwise the server listens on the configured listen address.
	Listener net.Listener
	// ExternalListener, when non-nil, is the listener for the external port (for tests; TLS is still enabled with the
	// configured certificate); otherwise the server listens on the configured external listen address.
	ExternalListener net.Listener
	// HomeDirs overrides the home directories used when validating the work root directory (for tests).
	HomeDirs []string
}

// Build runs the checks and assembly in startup order: checks -> instance ID -> model catalog -> migrations -> default
// organization and project -> clean up leftover processes -> restart handling -> reconciliation goroutines. The HTTP
// service starts once Serve is called.
func Build(ctx context.Context, cfg *config.Config, log *slog.Logger, o Options) (a *App, err error) {
	if cfg.MaxMessageImages <= 0 {
		cfg.MaxMessageImages = 4
	}
	if cfg.MaxMessageImageBytes <= 0 {
		cfg.MaxMessageImageBytes = 5 << 20
	}
	if cfg.MaxMessageImagePixels <= 0 {
		cfg.MaxMessageImagePixels = 20_000_000
	}
	a = &App{Cfg: cfg, Log: log, InstanceID: uuid.Must(uuid.NewV7())}
	defer func() {
		if err != nil {
			a.closeResources()
		}
	}()
	log.Info("server instance starting", "instanceId", a.InstanceID, "config", cfg)
	for _, w := range cfg.Warnings {
		log.Warn(w)
	}

	// Dependency checks.
	dbDir := config.DBDir(cfg.DataDir)
	if err = os.MkdirAll(dbDir, 0o700); err != nil {
		return a, fmt.Errorf("创建数据目录失败：%w", err)
	}
	// One data directory serves only one server process: background maintenance, migrations and the in-memory
	// coordination state all assume this process is the only writer.
	if a.dirLock, err = db.LockDir(dbDir); err != nil {
		if errors.Is(err, db.ErrLocked) {
			return a, fmt.Errorf("数据目录 %s 已被另一个服务进程使用（%s）：%w", dbDir, config.EnvDataDir, err)
		}
		return a, err
	}
	res, err := preflight.Resolve(ctx, preflight.Options{
		PiBin: cfg.PiBin, PiVersion: cfg.PiVersion, PiAIDir: cfg.PiAIDir, NodeBin: cfg.NodeBin,
		ExecBasePath: cfg.ExecBasePath, WorkRoot: cfg.WorkRoot, HomeDirs: o.HomeDirs,
	})
	if err != nil {
		return a, err
	}
	// One work root also serves only one server process: Agent directories, the shared tool and platform files, and all
	// the cleanup done at startup assume this instance is the only one in the work root. Take the lock before creating
	// the database, so a rejected instance does not leave a new database in its own data directory.
	if a.workLock, err = db.LockPath(filepath.Join(res.WorkRoot, WorkRootLockFile)); err != nil {
		if errors.Is(err, db.ErrLocked) {
			return a, fmt.Errorf("工作根 %s 已被另一个服务进程使用（%s）：%w", res.WorkRoot, config.EnvWorkRoot, err)
		}
		return a, err
	}
	log.Info("startup checks passed", "piBin", res.PiBin, "piAIDir", res.PiAIDir, "workRoot", res.WorkRoot, "execBasePath", res.ExecBasePath)
	if a.Store, err = store.Open(ctx, db.Options{Dir: dbDir, Logger: log}); err != nil {
		return a, err
	}
	a.Store.InstanceID = &a.InstanceID

	// loopctl of the same version: verify the build output, install the binary for this platform, put it on the Agent's
	// PATH.
	dist, err := loopctldist.Load(cfg.LoopctlDistDir, buildinfo.Version())
	if err != nil {
		return a, err
	}
	loopctlBin, err := dist.Install(res.WorkRoot)
	if err != nil {
		return a, fmt.Errorf("安装 loopctl 失败：%w", err)
	}
	log.Info("loopctl installed", "version", dist.Manifest.Version, "binDir", loopctlBin)
	var extTLS *tls.Config
	if cfg.ExternalListenAddr != "" {
		var cert tls.Certificate
		if cfg.ExternalTLSSelfSigned != "" {
			// Self-signed certificate maintained by the server itself: it covers the loopback addresses and the
			// external listen host; loopctl uses cert.pem as --ca-file.
			host, _, _ := net.SplitHostPort(cfg.ExternalListenAddr)
			c, certPath, generated, err := selfsigned.Load(cfg.ExternalTLSSelfSigned, []string{host}, time.Now())
			if err != nil {
				return a, fmt.Errorf("准备对外端口的自签证书失败：%w", err)
			}
			log.Info("external port uses a self-signed certificate", "cert", certPath, "generated", generated)
			cert = c
		} else {
			c, err := tls.LoadX509KeyPair(cfg.ExternalTLSCert, cfg.ExternalTLSKey)
			if err != nil {
				return a, fmt.Errorf("读取对外端口的 TLS 证书失败：%w", err)
			}
			cert = c
		}
		extTLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	}

	packageRoot := res.PiPackageDir
	if packageRoot == "" {
		packageRoot = filepath.Dir(filepath.Dir(filepath.Dir(res.PiAIDir)))
	}
	modelOptions := modelcatalog.Options{NodeBin: res.NodeBin, PiAIDir: res.PiAIDir, PiPackageDir: packageRoot, PiVersion: cfg.PiVersion, Path: res.ExecBasePath}
	builtinModels, err := modelcatalog.Load(ctx, modelOptions)
	if err != nil {
		return a, err
	}
	models := modelcatalog.NewRegistry(builtinModels)
	log.Info("loaded the Pi model catalog", "providers", len(builtinModels.Providers))

	templates, err := clitemplate.Load(cfg.CLITemplateDir)
	if err != nil {
		return a, err
	}
	if err = a.Store.Migrate(ctx, log); err != nil {
		return a, err
	}
	a.ModelSettings = &modelconfig.Service{Store: a.Store, Models: models, Options: modelOptions}
	settings, reloadErr := a.ModelSettings.Reload(ctx)
	if reloadErr != nil {
		return a, fmt.Errorf("读取自定义模型配置失败：%w", reloadErr)
	}
	if settings.LoadError != "" {
		log.Warn("custom model configuration is invalid; fell back to the built-in catalog", "error", settings.LoadError)
	}
	defaults, err := a.Store.EnsureDefaults(ctx, cfg.DefaultOrgName, cfg.DefaultProjectName)
	if err != nil {
		return a, err
	}
	// Revoke all Agent tokens issued by the previous run: those Pi processes exited along with the server.
	if n, err := a.Store.RevokeAgentTokensNotFrom(ctx, a.InstanceID); err != nil {
		return a, err
	} else if n > 0 {
		log.Info("revoked the Agent tokens issued by the previous server run", "count", n)
	}
	// Listen first: the address of the Agent-only API must be known before any Pi starts.
	a.listener = o.Listener
	if a.listener == nil {
		if a.listener, err = net.Listen("tcp", cfg.ListenAddr); err != nil {
			return a, fmt.Errorf("监听 %s 失败：%w", cfg.ListenAddr, err)
		}
	}

	a.Coord = coord.New(coord.Options{StreamMaxLen: cfg.EventStreamMaxLen, OverviewMaxLen: cfg.OverviewMaxLen})

	installer := clitemplate.NewInstaller(res.WorkRoot, cfg.CLIInstallTimeout, o.CLIHTTPClient)
	a.Installer = installer
	if err = restoreInstallFailures(ctx, a.Store, installer, templates); err != nil {
		return a, err
	}
	recorder := &runrecord.Recorder{Store: a.Store, Log: log}
	text := content.TextRules{Extensions: cfg.TextEditExtensions, MaxBytes: cfg.TextEditMax}
	a.Config = &agentconfig.Service{
		Store: a.Store, Models: models, Templates: templates, OrgID: defaults.Organization.ID, Logger: log,
		Limits: agentconfig.Limits{MaxUploadFile: cfg.MaxUploadFile, MaxSkillArchive: cfg.MaxSkillArchive, MaxSkillEntries: cfg.MaxSkillEntries, MaxCredential: cfg.MaxCredential, Text: text},
	}
	a.Config.PluginAssets = a.pluginAssets
	a.LocalMode = &localmode.Service{Store: a.Store}
	a.Config.LocalAccess = a.LocalMode.Access
	a.Config.ProviderState = a.providerState
	a.UserPath = localmode.NewUserPath(log, localmode.DefaultUserPathTimeout)
	// When a switch that uses the user's PATH (cli, git) is on, resolve it in the background ahead of time, so the
	// first Pi start or pull does not wait for the login shell.
	if settings, err := a.Store.GetLocalModeSettings(ctx); err != nil {
		return a, err
	} else if settings.CLI || settings.Git {
		a.UserPath.Start()
	}

	// The login helper and platform extensions are installed content-addressed under platform/auth-helper/<digest>/:
	// files from different builds never overwrite each other.
	platformDir := filepath.Join(res.WorkRoot, "platform")
	if err = os.MkdirAll(platformDir, 0700); err != nil {
		return a, err
	}
	helperEntry, authExtension, err := authhelper.Install(filepath.Join(platformDir, "auth-helper"))
	if err != nil {
		return a, err
	}
	authRoot := filepath.Dir(helperEntry)
	helper := &modelauth.Helper{Options: modelauth.HelperOptions{NodeBin: res.NodeBin, Entry: helperEntry, WorkRoot: platformDir, BasePath: res.ExecBasePath, PiPackageRoot: packageRoot, PiAIDir: res.PiAIDir, PiVersion: cfg.PiVersion, Timeout: cfg.AuthLoginTimeout, StopGrace: cfg.PiStopGrace}}
	var login modelauth.LoginHelper = helper
	var refresher modelauth.Refresher = helper
	if o.AuthLoginHelper != nil {
		login = o.AuthLoginHelper
	}
	if o.AuthRefresher != nil {
		refresher = o.AuthRefresher
	}
	helperStopGrace := cfg.PiStopGrace
	if helperStopGrace <= 0 {
		helperStopGrace = 2 * time.Second
	}
	authCoordinator := modelauth.NewCoordinator(a.Store, refresher, modelauth.CoordinatorOptions{RefreshTimeout: cfg.AuthRefreshTimeout, RecordTimeout: cfg.AuthRecordTimeout, WaitTimeout: cfg.AuthIPCTimeout, RefreshDrainTimeout: helperStopGrace + 5*time.Second}, log)
	a.ModelAuth = modelauth.NewService(a.Store, login, authCoordinator, models, modelauth.ServiceOptions{LoginTimeout: cfg.AuthLoginTimeout, RecordTimeout: cfg.AuthRecordTimeout, MaxCredential: cfg.MaxCredential}, log)
	if err = a.Store.RecoverModelAuth(ctx); err != nil {
		return a, err
	}
	if err = a.Store.SettleCredentialWipes(ctx); err != nil {
		return a, err
	}
	projectCredentials := modelauth.NewProjectService(a.ModelAuth, templates, cfg.MaxCredential)
	// Subscription login for plugin providers (plugins/SPEC.md §13): state directories live under
	// platform/provider-states/; the helper process uses the same set of embedded assets as the Codex login.
	a.ProviderStates = &providerstate.Manager{Root: filepath.Join(platformDir, "provider-states")}
	var pluginLogin modelauth.PluginLoginHelper = &modelauth.PluginHelper{Options: modelauth.PluginHelperOptions{NodeBin: res.NodeBin, Entry: filepath.Join(authRoot, "provider-main.mjs"),
		WorkRoot: platformDir, BasePath: res.ExecBasePath, PiPackageRoot: packageRoot, PiVersion: cfg.PiVersion, Timeout: cfg.AuthLoginTimeout, StopGrace: cfg.PiStopGrace}}
	if o.PluginLoginHelper != nil {
		pluginLogin = o.PluginLoginHelper
	}
	projectCredentials.LocalLogin = a.providerLocalLogin
	projectCredentials.Plugins = &modelauth.PluginLogins{Helper: pluginLogin, States: a.ProviderStates, Extensions: func(ctx context.Context, plugin string) ([]string, error) {
		if a.Plugins == nil {
			return nil, apperr.NotFound("插件")
		}
		return a.Plugins.Extensions(ctx, plugin)
	}}
	googleHelper := &cliauth.ProcessHelper{Options: cliauth.HelperOptions{WorkRoot: platformDir, StopGrace: cfg.PiStopGrace, Runtime: func(ctx context.Context) (cliauth.HelperRuntime, error) {
		template, ok := templates.Get("gcloud")
		if !ok {
			return cliauth.HelperRuntime{}, cliauth.ErrUnavailable
		}
		if _, e := installer.Ensure(ctx, template); e != nil {
			return cliauth.HelperRuntime{}, e
		}
		sdk, python := installer.GoogleRuntimePaths(template)
		return cliauth.HelperRuntime{SDKRoot: sdk, PythonRoot: python, Script: filepath.Join(authRoot, "google_auth.py")}, nil
	}}}
	var googleLogin cliauth.LoginHelper = googleHelper
	var googleRefresh cliauth.Refresher = googleHelper
	if o.CLILoginHelper != nil {
		googleLogin = o.CLILoginHelper
	}
	if o.CLIRefresher != nil {
		googleRefresh = o.CLIRefresher
	}
	googleCoordinator := cliauth.NewCoordinator(a.Store, googleRefresh, cliauth.CoordinatorOptions{RefreshTimeout: cfg.AuthRefreshTimeout, RecordTimeout: cfg.AuthRecordTimeout, WaitTimeout: cfg.AuthIPCTimeout}, log)
	a.CLIAuth = cliauth.NewService(a.Store, googleLogin, googleCoordinator, cliauth.ServiceOptions{LoginTimeout: cfg.AuthLoginTimeout, RecordTimeout: cfg.AuthRecordTimeout}, log)
	var larkRefresh larkauth.Refresher = larkauth.NewClient(nil)
	if o.LarkLoginHelper != nil {
		a.CLIAuth.Lark = o.LarkLoginHelper
	}
	if o.LarkRefresher != nil {
		larkRefresh = o.LarkRefresher
	}
	a.CLIAuth.LarkCoordinator = larkauth.NewCoordinator(a.Store, larkRefresh, larkauth.CoordinatorOptions{RefreshTimeout: cfg.AuthRefreshTimeout, RecordTimeout: cfg.AuthRecordTimeout, WaitTimeout: cfg.AuthIPCTimeout}, log)
	larkRuntime, err := configureLarkExecution(ctx, cfg, res.WorkRoot, a.CLIAuth.LarkCoordinator, o.LarkHTTPTransport)
	if err != nil {
		return a, fmt.Errorf("Lark用户运行组件不可用: %w", err)
	}
	var larkFactory func(context.Context, local.LarkRuntimeRequest) (local.LarkRuntime, error)
	if larkRuntime != nil {
		larkFactory = larkRuntime.open
	}
	a.CLIVerification, err = cliverifyservice.New(ctx, a.Store, templates, installer, googleCoordinator, cliverifyservice.Options{Concurrency: cfg.CLIProbeConcurrency, WorkRoot: res.WorkRoot, GoogleEntry: filepath.Join(authRoot, "google_verify.py"), NodeBin: res.NodeBin, GuardEntry: filepath.Join(authRoot, "cli_verify_guard.mjs"), InstallTimeout: cfg.CLIInstallTimeout, ProbeTimeout: cfg.CLIProbeTimeout, RecordTimeout: cfg.AuthRecordTimeout, StopGrace: helperStopGrace, CommandFactory: o.CLIVerificationCommand}, log)
	if err != nil {
		return a, err
	}
	a.CLIAuth.Account = projectCredentials.Get
	projectCredentials.CancelCLILogins = a.CLIAuth.CancelLogins
	a.Config.CancelProjectCLILogins = a.CLIAuth.CancelProject
	if err = a.Store.RecoverProjectCLIAuthOperations(ctx); err != nil {
		return a, err
	}

	a.Config.ModelAuth = a.ModelAuth
	a.ModelAuth.Changed = func(id uuid.UUID) {
		if a.Config.AgentChanged != nil {
			a.Config.AgentChanged(id)
		}
	}
	host, _ := os.Hostname()
	username := ""
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	taskSource := &tasks.Source{ModelAuth: authCoordinator, Normal: a.Config, Store: a.Store, WorkRoot: res.WorkRoot, GitBin: res.GitBin, BasePath: res.ExecBasePath, MaxSessionBytes: cfg.TaskMaxSession, CloneTimeout: cfg.GitCloneTimeout, Templates: templates, Installer: installer}
	taskSource.EnableLarkOverlay()
	taskSource.Log = log
	taskSource.PluginAssets = a.pluginAssets
	taskSource.LocalAccess = a.LocalMode.Access
	taskSource.ProviderState = a.providerState
	taskObserver := &tasks.Observer{Store: a.Store, Normal: recorder, Timeout: cfg.TaskRecordTimeout, Log: log}
	// config.Load always provides the context folder root and the folder upload limit; they are empty when the config
	// is built directly (tests): the root then goes under the work root directory (never into the process's current
	// directory) and the limit takes its default.
	contextDir, maxFolderUpload := cfg.ContextDir, cfg.MaxFolderUpload
	if contextDir == "" {
		contextDir = filepath.Join(res.WorkRoot, "context")
	}
	if maxFolderUpload <= 0 {
		maxFolderUpload = 1 << 30
	}
	a.Executor = local.New(local.Options{Models: models, Hooks: appHooks{a}, Dialogs: a.askDialog, LarkFactory: larkFactory, GoogleResolver: googleCoordinator, GoogleRuntimeEntry: filepath.Join(authRoot, "google_runtime.py"),
		AuthResolver: authCoordinator, AuthExtension: authExtension, LegacyAuthExtension: filepath.Join(filepath.Dir(authExtension), "legacy-anthropic.mjs"), CodexServiceTierExtension: filepath.Join(filepath.Dir(authExtension), "codex-service-tier.mjs"), PiPackageRoot: packageRoot, PiAIDir: res.PiAIDir, AuthIPCTimeout: cfg.AuthIPCTimeout,
		WorkRoot: res.WorkRoot, PiBin: res.PiBin, NodeBin: res.NodeBin, GitBin: res.GitBin, ExecBasePath: res.ExecBasePath, Passthrough: cfg.ExecEnvPassthrough,
		Username: username, MaxProcesses: cfg.PiMaxProcesses, IdleTimeout: cfg.PiIdleTimeout, StartTimeout: cfg.PiStartTimeout,
		StopGrace: cfg.PiStopGrace, AbortTimeout: cfg.AbortTimeout, CloneTimeout: cfg.GitCloneTimeout, InstanceID: a.InstanceID, Hostname: host,
		StderrTailBytes: cfg.PiStderrTailBytes, Observer: taskObserver,
		Tokens: agentTokens{st: a.Store, instance: a.InstanceID}, AgentURL: agentURL(a.listener.Addr()), LoopctlBinDir: loopctlBin, ContextDir: contextDir, UserPath: a.UserPath.Dirs,
	}, taskSource, templates, installer, log)
	a.Config.Releaser = a.Executor
	taskSource.ClaudeProjects = a.Executor.ClaudeProjectsDir
	// Preserve unresolved handoff process evidence before generic cleanup can
	// remove stale PID records. Missing/invalid provenance fails closed.
	var protectedLark []local.PendingRuntimeHandoff
	var pendingLark []store.TaskLarkRecoveryAction
	var readErr error
	pendingLark, readErr = a.Store.PendingTaskLarkRecovery(ctx)
	if readErr != nil {
		return a, readErr
	}
	protectedLark, err = larkPendingHandoffs(pendingLark)
	if err != nil {
		return a, err
	}
	if err = a.Executor.CleanupStaleExceptPending(ctx, protectedLark); err != nil {
		return a, err
	}
	// State directories of plugin provider subscription accounts: at startup, delete versions that are no longer needed
	// and the staging directories of logins that did not finish last time.
	a.sweepProviderStates(ctx, true)
	if err = a.recoverPendingLarkSwitches(ctx, pendingLark, larkRuntime, cfg.TaskRecordTimeout); err != nil {
		return a, err
	}

	a.Conv = conversation.New(a.Store, a.Coord, a.Executor, conversation.Options{
		InstanceID: a.InstanceID, MaxMessage: cfg.MaxMessage, ReconcileInterval: cfg.ReconcileInterval,
	}, log)
	taskSource.PauseNormal = a.Conv.PauseDispatch
	taskSource.FreezeNormal = a.Executor.FreezeForFork
	a.Tasks = tasks.New(a.Store, a.Config, a.Executor, tasks.Options{InstanceID: a.InstanceID, MaxSteps: cfg.TaskMaxSteps, MaxEdges: cfg.TaskMaxEdges, MaxVisits: cfg.TaskMaxVisits, MaxRequest: cfg.TaskMaxRequest, MaxArtifact: cfg.TaskMaxArtifact, ReconcileInterval: cfg.TaskReconcileInterval, RecordTimeout: cfg.TaskRecordTimeout}, log)
	a.Tasks.Source = taskSource
	if larkRuntime != nil {
		a.Tasks.LarkSwitchNative = larkRuntime
	}
	a.Tasks.Coord = a.Coord
	recorder.StatusChanged = a.Conv.EmitAgentStatus
	a.Config.AgentChanged = a.Conv.EmitAgentStatus
	if err = a.configureCLIImport(ctx, cfg, o, templates, res.WorkRoot); err != nil {
		return a, err
	}
	a.Config.AgentRemoved = a.Conv.EmitAgentStatus // push a removal event when an Agent is deleted

	inboxSvc := &inbox.Service{Tasks: a.Tasks, St: a.Store, Co: a.Coord, Conv: a.Conv, Log: log, O: inbox.Options{
		MaxMessage: cfg.MaxInboxMessage, PreviewChars: cfg.InboxPreviewChars, PageDefault: 20, PageMax: cfg.InboxPageMax,
		DefaultOrgID: defaults.Organization.ID, QuestionTTL: cfg.InboxQuestionTTL,
	}}
	taskSource.CloseQuestion = store.CloseInboxQuestion
	taskSource.ViewQuestion = inboxSvc.ViewQuestion
	a.Tasks.SystemNotice = inboxSvc.SystemNotice
	a.Tasks.InboxCancelled = inboxSvc.EmitCancelled
	a.Conv.InboxCancelled = inboxSvc.EmitCancelled
	a.Tasks.QuestionMaintenance = inboxSvc.ReconcileQuestions

	a.Folders = folders.New(a.Store, folderGitAccess(a.Config, templates, installer, res.WorkRoot, res.ExecBasePath, &gitLocalIdentity{modes: a.LocalMode, userPath: a.UserPath, gitBin: res.GitBin}),
		folders.Options{WorkRoot: res.WorkRoot, ContextDir: contextDir, MaxUpload: maxFolderUpload, GitBin: res.GitBin, CloneTimeout: cfg.GitCloneTimeout}, log)

	a.Store.ImageMaxCount = cfg.MaxMessageImages
	a.Tasks.Hooks = appHooks{a}
	console, consoleSource, err := consoleFiles(cfg.WebDir)
	if err != nil {
		return a, err
	}
	if console != nil {
		log.Info("console is served by the server", "source", consoleSource)
	} else {
		log.Info("no console files on the server; serving the API only (use vite in development, or set " + config.EnvWebDir + ")")
	}
	a.API = &httpapi.Server{Hooks: appHooks{a}, CLIImport: a.CLIImport, CLIVerification: a.CLIVerification, CLIAuth: a.CLIAuth, ModelAuth: a.ModelAuth, ModelSettings: a.ModelSettings, ProjectCredentials: projectCredentials,
		O: httpapi.Options{
			AllowedHosts: cfg.AllowedHosts, ConsoleOrigins: cfg.ConsoleOrigins, Console: webui.Handler(console), SSEKeepalive: cfg.SSEKeepalive, TaskEventPoll: cfg.TaskEventPoll, Defaults: defaults,
			Limits: apitypes.Limits{MaxMessageImages: cfg.MaxMessageImages, MaxMessageImageBytes: cfg.MaxMessageImageBytes, MaxMessageImagePixels: cfg.MaxMessageImagePixels, MaxUploadFile: cfg.MaxUploadFile, MaxFolderUpload: maxFolderUpload, MaxMessage: cfg.MaxMessage, MaxInboxMessage: cfg.MaxInboxMessage, MaxSkillArchive: cfg.MaxSkillArchive,
				MaxSkillEntries: cfg.MaxSkillEntries, MaxCredential: cfg.MaxCredential,
				TextEdit: apitypes.TextEdit{MaxBytes: cfg.TextEditMax, Extensions: cfg.TextEditExtensions}},
		},
		Config: a.Config, Conv: a.Conv, Models: models, Templates: templates, Installer: installer, Store: a.Store, Coord: a.Coord,
		Tasks: a.Tasks, Records: &runrecord.Service{Store: a.Store}, Inbox: inboxSvc, Folders: a.Folders, LocalMode: a.LocalMode, Log: log,
		Runtimes:   &runtimes.Checker{PiVersion: func() string { return models.Snapshot().PiVersion }, UserPath: a.UserPath.Dirs, RealHome: os.UserHomeDir, ExecBasePath: res.ExecBasePath, HelperEnv: a.Executor.ClaudeHelperEnv},
		PiCommands: a.Executor.Commands,
	}
	if err = a.assemblePlugins(cfg, res, log); err != nil {
		return a, err
	}
	baseCtx, baseCancel := context.WithCancel(context.Background())
	a.baseCancel = baseCancel
	go a.providerStateSweeper(baseCtx, 10*time.Minute)
	// Folders: mark interrupted pulls as failed and empty the trash; before any Pi starts, replay once the manual edits
	// from retired folder sources that never reached disk.
	if err = a.Folders.Start(ctx); err != nil {
		return a, err
	}
	legacy, err := a.Folders.ApplyLegacyFolderSources(ctx)
	if err != nil {
		return a, fmt.Errorf("回放退役文件夹来源失败：%w", err)
	}
	if legacy.Sources > 0 || legacy.UnclonedGitDirs > 0 {
		log.Info("replayed retired folder sources", "sources", legacy.Sources, "filesWritten", legacy.FilesWritten, "filesDeleted", legacy.FilesDeleted,
			"filesSkipped", legacy.FilesSkipped, "unclonedGitSources", legacy.UnclonedGitDirs)
	}
	for _, p := range local.AncestorEntryFiles(res.WorkRoot) {
		log.Warn("a context entry file sits in a parent directory of the work root; Pi reads it into every Agent's system prompt, so move it away", "path", p)
	}
	if err = a.Conv.Start(ctx); err != nil {
		return a, err
	}
	if err = a.Tasks.Start(ctx); err != nil {
		return a, err
	}
	a.Maintenance = maintenance.Start(a.Store, maintenance.Options{BackupDir: cfg.BackupDir, BackupKeep: cfg.BackupKeep}, log)

	a.Conv.StartSleepMaintenance(cfg.SleepScanInterval, cfg.SleepScanBatch, func(id uuid.UUID) {
		a.Tasks.Kick(id)
		if instances, e := a.Store.TaskInstances(context.Background(), id); e == nil {
			for _, inst := range instances {
				a.Tasks.KickInbox(inst.ID)
			}
		}
	})
	a.http = &http.Server{Handler: a.API.Handler(), ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context { return baseCtx }}
	if extTLS != nil {
		ln := o.ExternalListener
		if ln == nil {
			if ln, err = net.Listen("tcp", cfg.ExternalListenAddr); err != nil {
				return a, fmt.Errorf("监听对外端口 %s 失败：%w", cfg.ExternalListenAddr, err)
			}
		}
		a.extListener = tls.NewListener(ln, extTLS)
		a.external = &http.Server{Handler: a.API.ExternalHandler(dist.Handler()), ReadHeaderTimeout: 10 * time.Second,
			BaseContext: func(net.Listener) context.Context { return baseCtx }}
	}
	return a, nil
}

// ExternalAddr returns the actual listen address of the external port; empty when it is not enabled.
func (a *App) ExternalAddr() string {
	if a.extListener == nil {
		return ""
	}
	return a.extListener.Addr().String()
}

// Addr returns the actual listen address.
func (a *App) Addr() string { return a.listener.Addr().String() }

// Serve serves HTTP until Shutdown.
func (a *App) Serve() error {
	if a.external != nil {
		a.Log.Info("serving the external port (TLS)", "addr", a.ExternalAddr())
		go func() {
			if err := a.external.Serve(a.extListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				a.Log.Error("external port stopped serving", "err", err)
			}
		}()
	}
	if a.PluginHost != nil {
		settings, err := a.Store.GetPluginSettings(context.Background())
		if err != nil {
			return fmt.Errorf("读取插件设置失败：%w", err)
		}
		a.PluginHost.Start(settings.Disabled)
		a.Plugins.WakeOnCommit()
	}
	a.Log.Info("serving", "addr", a.Addr())
	if a.Cfg.ReadyFile != "" {
		// The listener has long been open and connections queue up before Serve starts, so writing the ready file now
		// cannot lead anyone to connect to a port that does not answer yet.
		ready := Ready{URL: "http://" + a.Addr(), PID: os.Getpid(), Version: buildinfo.Version()}
		if a.external != nil {
			ready.ExternalURL = "https://" + a.ExternalAddr()
		}
		if err := writeReadyFile(a.Cfg.ReadyFile, ready); err != nil {
			return fmt.Errorf("写就绪文件 %s 失败：%w", a.Cfg.ReadyFile, err)
		}
		a.readyWritten = true
	}
	if err := a.http.Serve(a.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown exits gracefully: stop accepting requests, stop dispatching, end all Pi processes. Runs in progress keep
// their current state and are marked as interrupted by a server restart at the next startup.
func (a *App) Shutdown(ctx context.Context) error {
	var errs []error
	if a.http != nil {
		// SSE connections never end by themselves: cancel the context shared by all requests so event streams exit
		// quickly, then wait for the remaining requests.
		a.baseCancel()
		if a.external != nil {
			if err := a.external.Shutdown(ctx); err != nil {
				_ = a.external.Close()
			}
		}
		if err := a.http.Shutdown(ctx); err != nil {
			_ = a.http.Close()
		}
	}
	if a.PluginHost != nil {
		errs = append(errs, a.PluginHost.Stop(ctx))
	}
	if a.Tasks != nil {
		errs = append(errs, a.Tasks.Stop(ctx))
	}
	if a.Maintenance != nil {
		errs = append(errs, a.Maintenance.Stop(ctx))
	}
	if a.Folders != nil {
		errs = append(errs, a.Folders.Stop(ctx))
	}
	if a.Conv != nil {
		errs = append(errs, a.Conv.Stop(ctx))
	}
	if a.Executor != nil {
		errs = append(errs, a.Executor.Shutdown(ctx))
	}
	if a.ModelAuth != nil {
		errs = append(errs, a.ModelAuth.Close(ctx))
	}
	if a.readyWritten {
		_ = os.Remove(a.Cfg.ReadyFile)
	}
	a.closeResources()
	a.Log.Info("server stopped")
	return errors.Join(errs...)
}

// restoreInstallFailures restores into the installer the most recent failed install of each CLI version from the run
// records, so the failure reason is still visible after a restart.
func restoreInstallFailures(ctx context.Context, st *store.Store, inst *clitemplate.Installer, templates *clitemplate.Catalog) error {
	results, err := st.LatestCLIInstalls(ctx)
	if err != nil {
		return err
	}
	failures := map[string]string{}
	for _, r := range results {
		if !r.Succeeded {
			failures[r.CLI+"@"+r.Version] = r.Error
		}
	}
	inst.RestoreFailures(failures, templates.All())
	return nil
}

// agentTokens keeps Agent tokens in the database: it stores only digests and records the issuing server instance.
type agentTokens struct {
	st       *store.Store
	instance uuid.UUID
}

func (t agentTokens) IssueAgentToken(ctx context.Context, agentID uuid.UUID) (uuid.UUID, string, error) {
	tok, err := auth.NewToken(auth.AgentPrefix)
	if err != nil {
		return uuid.Nil, "", err
	}
	if err := t.st.InsertAgentToken(ctx, tok.ID, agentID, t.instance, tok.Hash); err != nil {
		return uuid.Nil, "", err
	}
	return tok.ID, tok.Plain, nil
}

func (t agentTokens) RevokeAgentToken(ctx context.Context, id uuid.UUID) error {
	return t.st.RevokeAgentToken(ctx, id)
}

// appHooks hands synchronous Hooks to the plugin service. The plugin service is assembled after the executor and the
// task service; when it is not assembled (tests on a historical schema) plugins are not called.
type appHooks struct{ a *App }

func (h appHooks) Hook(ctx context.Context, kind pluginhost.Kind, event map[string]any, project *uuid.UUID) (pluginhost.Outcome, error) {
	if h.a.Plugins == nil {
		return pluginhost.Outcome{}, nil
	}
	return h.a.Plugins.Hook(ctx, kind, event, project)
}

// askDialog turns a confirm, select or input sent by an extension during a turn into a question from this Agent to the
// console (plugins/SPEC.md §11), and waits for the answer, the deadline or the end of the turn; on error it responds as
// cancelled.
func (a *App) askDialog(ctx context.Context, agentID, runID uuid.UUID, req pirpc.DialogRequest) pirpc.DialogResponse {
	if a.API == nil || a.API.Inbox == nil {
		return pirpc.DialogResponse{Cancelled: true, Outcome: "cancelled"}
	}
	ans, err := a.API.Inbox.AskDialog(ctx, inbox.Dialog{AgentID: agentID, RunID: runID, RequestID: req.ID, Method: req.Method, Title: req.Title,
		Message: req.Message, Placeholder: req.Placeholder, Options: req.Options, Timeout: req.Timeout})
	if err != nil {
		a.Log.Warn("extension dialog question failed; answering as cancelled", "agentId", agentID, "runId", runID, "err", err)
	}
	return pirpc.DialogResponse{Cancelled: ans.Cancelled, Confirmed: ans.Confirmed, Value: ans.Value, Outcome: ans.Outcome, QuestionID: ans.QuestionID}
}

// providerState returns the state directory (per version) of a plugin provider subscription account and the paths that
// the plugin's current version declares as travelling with the account (plugins/SPEC.md §13).
func (a *App) providerState(ctx context.Context, plugin, provider string, version uuid.UUID) (*agentconfig.ProviderState, error) {
	if a.ProviderStates == nil {
		return nil, agentconfig.ErrCredentialUnavailable
	}
	var paths []string
	if a.Plugins != nil {
		var err error
		if paths, err = a.Plugins.ProviderStatePaths(ctx, plugin, provider); err != nil {
			return nil, err
		}
	}
	return &agentconfig.ProviderState{PluginID: plugin, VersionID: version, Dir: a.ProviderStates.Dir(version), Paths: paths}, nil
}

// providerLocalLogin reports the local login a plugin provider uses while "Use local logins" is on
// (plugins/SPEC.md §13): when the provider comes from a plugin, declares state paths that travel with the account, and
// the switch is on, it returns active or missing depending on whether all of those paths exist under the real home
// directory; otherwise nil. It only checks existence and never reads the contents.
func (a *App) providerLocalLogin(ctx context.Context, provider string) (*string, error) {
	if a.Plugins == nil || a.LocalMode == nil || a.ModelSettings == nil {
		return nil, nil
	}
	plugin, ok := plugins.ProviderPlugin(a.ModelSettings.Models, provider)
	if !ok {
		return nil, nil
	}
	settings, err := a.LocalMode.Settings(ctx)
	if err != nil || !settings.Logins {
		return nil, err
	}
	paths, err := a.Plugins.ProviderStatePaths(ctx, plugin, provider)
	if err != nil || len(paths) == 0 {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	state := "active"
	for _, p := range paths {
		if _, err := os.Lstat(filepath.Join(home, filepath.FromSlash(p))); err != nil {
			state = "missing"
		}
	}
	return &state, nil
}

// sweepProviderStates deletes plugin provider state directories that are no longer needed (plugins/SPEC.md §13). It
// keeps the current version of each account that is not deleted, versions bound to unfinished tasks, and versions in
// use by a running Pi. When pending is true (at startup) it also deletes login staging directories.
func (a *App) sweepProviderStates(ctx context.Context, pending bool) {
	if a.ProviderStates == nil {
		return
	}
	removed, err := a.ProviderStates.Sweep(func() (map[uuid.UUID]bool, error) {
		keep, err := a.Store.ProviderStateVersionsInUse(ctx)
		if err != nil {
			return nil, err
		}
		if a.Executor != nil {
			for id := range a.Executor.ProviderStatesInUse() {
				keep[id] = true
			}
		}
		return keep, nil
	}, pending)
	if err != nil {
		a.Log.Warn("failed to sweep plugin provider state directories", "err", err)
	}
	if len(removed) > 0 {
		a.Log.Info("deleted plugin provider state directories that are no longer needed", "count", len(removed))
	}
}

// providerStateSweeper periodically cleans up (old versions left after a re-login, an account deletion or the end of a
// task) until ctx ends.
func (a *App) providerStateSweeper(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.sweepProviderStates(ctx, false)
		}
	}
}

// deleteProviderLogins removes the subscription logins of these model providers when a plugin is uninstalled with
// "delete data" (plugins/SPEC.md §13). Project model accounts are deleted like a normal account deletion; a platform
// Agent's own login is cleared like "clear credential" (the pointer is emptied and the sweeper removes the state
// directory once nothing uses it).
func (a *App) deleteProviderLogins(ctx context.Context, providers []string) error {
	accounts, err := a.Store.ModelAccountsOf(ctx, providers)
	if err != nil {
		return err
	}
	if a.API != nil && a.API.ProjectCredentials != nil {
		for _, acc := range accounts {
			if _, err := a.API.ProjectCredentials.Delete(ctx, acc.ProjectID, acc.ID, acc.Revision); err != nil {
				return err
			}
		}
	}
	cleared, err := a.Store.ClearPlatformModelLogins(ctx, providers)
	if err != nil {
		return err
	}
	if a.ModelAuth != nil && a.ModelAuth.Changed != nil {
		for _, id := range cleared {
			a.ModelAuth.Changed(id)
		}
	}
	return nil
}

// pluginAssets returns what the Agent's granted plugins hand to Pi; empty when the plugin service is not assembled
// (tests on a historical schema).
func (a *App) pluginAssets(ctx context.Context, agentID uuid.UUID) (agentconfig.PluginAssets, error) {
	if a.Plugins == nil {
		return agentconfig.PluginAssets{}, nil
	}
	return a.Plugins.AgentAssets(ctx, agentID)
}

// assemblePlugins assembles plugins v2: the install directory is <work root>/platform/plugins/; when a host entry point
// is configured it prepares supervision of the plugin host (started in Serve). The host gets only the few environment
// variables it needs and does not inherit the server's environment (which contains the database address, among other
// things).
func (a *App) assemblePlugins(cfg *config.Config, res *preflight.Resolved, log *slog.Logger) error {
	root := filepath.Join(res.WorkRoot, "platform", "plugins")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	a.Plugins = &plugins.Service{Store: a.Store, Root: root, OfficialDir: cfg.PluginOfficialDir, OfficialMarket: cfg.PluginMarketURL, NodeBin: res.NodeBin, GitBin: res.GitBin,
		APIURL: strings.TrimSuffix(agentURL(a.listener.Addr()), "/agent/v1") + "/api/v1", PlatformVersion: buildinfo.Version(), Log: log}
	if a.Config != nil && a.Config.Templates != nil {
		a.Plugins.ManagedHomePaths = a.Config.Templates.AllCredentialFilePaths
	}
	a.Plugins.DeleteProviderAccounts = a.deleteProviderLogins
	if a.ModelSettings != nil {
		modelOptions := a.ModelSettings.Options
		a.Plugins.Models = a.ModelSettings.Models
		a.Plugins.CaptureProviders = func(ctx context.Context, dir string, extensions []string) (modelcatalog.Capture, error) {
			return modelcatalog.CaptureProviders(ctx, modelOptions, dir, extensions)
		}
	}
	if err := a.Plugins.Init(); err != nil {
		return err
	}
	// Merge the model providers registered by plugins into the model catalog; if they cannot be read, only log it, and
	// fail explicitly when an Agent uses one.
	if err := a.Plugins.PublishProviders(context.Background()); err != nil {
		log.Warn("failed to merge plugin-registered model providers into the model catalog", "err", err)
	}
	a.API.Plugins = a.Plugins
	if cfg.PluginHostEntry == "" {
		log.Info("no plugin host entry point configured; plugin platform sides do not run", "env", config.EnvPluginHostEntry)
		return nil
	}
	env := append(execenv.Base(root, ""), "PATH="+res.ExecBasePath, "HOME="+root, "LANG=C.UTF-8", "LOOP_NOW_PLUGINS_ROOT="+root, "LOOP_NOW_API_URL="+a.Plugins.APIURL)
	if tz := os.Getenv("TZ"); tz != "" {
		env = append(env, "TZ="+tz)
	}
	a.PluginHost = pluginhost.New(pluginhost.Options{Command: []string{res.NodeBin, cfg.PluginHostEntry}, Dir: root, Env: env, Store: a.Store,
		Specs: a.Plugins.Specs, Log: log.With("component", "plugin-host")})
	a.Plugins.Host = a.PluginHost
	return nil
}

// agentURL returns the address of the Agent-only API injected into Pi. Pi and the server run on the same machine: when
// listening on a loopback address, use that address; when listening on all addresses, use 127.0.0.1; when listening on
// a specific address, use that address. The port is the listener's port.
func agentURL(addr net.Addr) string {
	host, port := "127.0.0.1", ""
	if ta, ok := addr.(*net.TCPAddr); ok {
		port = strconv.Itoa(ta.Port)
		if !ta.IP.IsUnspecified() && ta.IP != nil {
			host = ta.IP.String()
		}
	}
	return "http://" + net.JoinHostPort(host, port) + "/agent/v1"
}

func (a *App) closeResources() {
	if a.CLIImport != nil {
		budget := a.Cfg.CLIImportRecordTimeout
		if budget <= 0 {
			budget = 5 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), budget+6*time.Second)
		if a.CLIImport.Close(ctx) != nil {
			a.Log.Warn("cli_import_drain_timeout")
		}
		cancel()
	}
	if a.CLIVerification != nil {
		ctx, cancel := context.WithTimeout(context.Background(), a.CLIVerification.Options.StopGrace+2*a.CLIVerification.Options.RecordTimeout+3*time.Second)
		_ = a.CLIVerification.Close(ctx)
		cancel()
	}

	if a.CLIAuth != nil {
		budget := a.CLIAuth.Options.RecordTimeout + 5*time.Second
		if a.CLIAuth.Coordinator != nil {
			budget += a.CLIAuth.Coordinator.Options.RefreshTimeout + 2*a.CLIAuth.Coordinator.Options.RecordTimeout
		}
		if a.CLIAuth.LarkCoordinator != nil {
			budget += a.CLIAuth.LarkCoordinator.Options.RefreshTimeout + 2*a.CLIAuth.LarkCoordinator.Options.RecordTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		_ = a.CLIAuth.Close(ctx)
		cancel()
	}

	if a.ModelAuth != nil {
		// Use effective component budgets, including defaults for isolated App
		// fixtures. A failed commit can consume its record budget before a fresh
		// bounded context records the uncertain terminal state.
		refreshBudget := 15 * time.Second
		if a.ModelAuth.Coordinator != nil {
			refreshBudget = a.ModelAuth.Coordinator.Options.RefreshTimeout
		}
		drainBudget := 7 * time.Second
		if a.ModelAuth.Coordinator != nil {
			drainBudget = a.ModelAuth.Coordinator.Options.RefreshDrainTimeout
		}
		budget := refreshBudget + 2*a.ModelAuth.Options.RecordTimeout + drainBudget + 2*time.Second
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		_ = a.ModelAuth.Close(ctx)
		cancel()
	}
	if a.Tasks != nil {
		timeout := a.Cfg.ShutdownTimeout
		if timeout <= 0 {
			timeout = 15 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		_ = a.Tasks.Stop(ctx)
		if a.Executor != nil && a.http == nil {
			_ = a.Executor.Shutdown(ctx)
		}
		cancel()
	}
	if a.listener != nil && a.http == nil {
		_ = a.listener.Close() // assembly failed: the listener has not been handed to the HTTP server yet
	}
	if a.extListener != nil && a.external == nil {
		_ = a.extListener.Close()
	}
	if a.Installer != nil {
		budget := a.Cfg.CLIInstallTimeout
		if budget <= 0 {
			budget = 5 * time.Minute
		}
		if a.Cfg.ShutdownTimeout > 0 && a.Cfg.ShutdownTimeout < budget {
			budget = a.Cfg.ShutdownTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		if e := a.Installer.Close(ctx); e != nil && a.Log != nil {
			a.Log.Warn("shared CLI installs have not settled yet", "errorCode", "installer_drain_timeout")
		}
		cancel()
	}
	if a.Store != nil {
		a.Store.Close()
	}
	_ = a.workLock.Release()
	_ = a.dirLock.Release()
}
