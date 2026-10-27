// Package app 把各模块装配成可启动、可停止的服务端。
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
	"github.com/redis/go-redis/v9"

	authhelper "loop-now/auth-helper"
	"loop-now/internal/buildinfo"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/cliauth"
	"loop-now/internal/server/cliimport"
	"loop-now/internal/server/clitemplate"
	cliverifyservice "loop-now/internal/server/cliverify/service"
	"loop-now/internal/server/config"
	"loop-now/internal/server/content"
	"loop-now/internal/server/conversation"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/executor/local"
	"loop-now/internal/server/executor/local/pirpc"
	"loop-now/internal/server/folders"
	"loop-now/internal/server/httpapi"
	"loop-now/internal/server/inbox"
	"loop-now/internal/server/larkauth"
	"loop-now/internal/server/loopctldist"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/pluginhost"
	"loop-now/internal/server/plugins"
	"loop-now/internal/server/preflight"
	"loop-now/internal/server/runrecord"
	"loop-now/internal/server/store"
	"loop-now/internal/server/tasks"
)

// App 是装配好的服务端。
type App struct {
	CLIImport       *cliimport.Service
	CLIVerification *cliverifyservice.Service
	Installer       *clitemplate.Installer
	CLIAuth         *cliauth.Service
	ModelAuth       *modelauth.Service
	Cfg             *config.Config
	InstanceID      uuid.UUID
	Log             *slog.Logger

	Store    *store.Store
	Redis    *redis.Client
	Coord    *coord.Coord
	Executor *local.Executor
	Conv     *conversation.Service
	Tasks    *tasks.Service
	Folders  *folders.Service
	Config   *agentconfig.Service
	API      *httpapi.Server
	// Plugins 是插件第二版的安装与配置；PluginHost 在配置了宿主入口时监护插件宿主，否则为空。
	Plugins      *plugins.Service
	PluginHost   *pluginhost.Supervisor
	pluginCancel context.CancelFunc

	listener   net.Listener
	http       *http.Server
	baseCancel context.CancelFunc

	// 对外端口（未配置时为空）：TLS，只认外部令牌。
	extListener net.Listener
	external    *http.Server
}

// Options 允许测试替换部分依赖。
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
	AuthRefresher     modelauth.Refresher
	// SearchPath 非空时，数据库连接限定在该 schema（测试用）。
	SearchPath string
	// MigrationTarget is an explicit Go-only isolated fixture ceiling; production uses all migrations.
	MigrationTarget int64
	// Listener 非空时直接使用（测试用），否则按配置的监听地址监听。
	Listener net.Listener
	// ExternalListener 非空时作为对外端口的监听器（测试用，仍按配置的证书启用 TLS），否则按配置的对外监听地址监听。
	ExternalListener net.Listener
	// HomeDirs 覆盖工作根目录校验时使用的 home 目录（测试用）。
	HomeDirs []string
}

// Build 按启动顺序完成检查与装配：检查 → 实例 ID → 模型目录 → 迁移 → 默认组织与项目 →
// 清理遗留进程 → 重启处理 → 对账协程。调用 Serve 后开始提供 HTTP 服务。
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
	log.Info("服务端实例启动", "instanceId", a.InstanceID, "config", cfg)
	for _, w := range cfg.Warnings {
		log.Warn(w)
	}

	// 依赖检查。
	if a.Store, err = store.Open(ctx, cfg.DatabaseURL, store.OpenOptions{SearchPath: o.SearchPath}); err != nil {
		return a, err
	}
	a.Store.InstanceID = &a.InstanceID
	if err = preflight.CheckRedis(ctx, cfg.RedisURL); err != nil {
		return a, err
	}
	res, err := preflight.Resolve(ctx, preflight.Options{
		PiBin: cfg.PiBin, PiVersion: cfg.PiVersion, PiAIDir: cfg.PiAIDir, NodeBin: cfg.NodeBin,
		ExecBasePath: cfg.ExecBasePath, WorkRoot: cfg.WorkRoot, HomeDirs: o.HomeDirs,
	})
	if err != nil {
		return a, err
	}
	log.Info("启动检查通过", "piBin", res.PiBin, "piAIDir", res.PiAIDir, "workRoot", res.WorkRoot, "execBasePath", res.ExecBasePath)

	// 同版本的 loopctl：校验构建产物，安装本机平台的二进制，放进 Agent 的 PATH。
	dist, err := loopctldist.Load(cfg.LoopctlDistDir, buildinfo.Version())
	if err != nil {
		return a, err
	}
	loopctlBin, err := dist.Install(res.WorkRoot)
	if err != nil {
		return a, fmt.Errorf("安装 loopctl 失败：%w", err)
	}
	log.Info("已安装 loopctl", "version", dist.Manifest.Version, "binDir", loopctlBin)
	var extTLS *tls.Config
	if cfg.ExternalListenAddr != "" {
		cert, err := tls.LoadX509KeyPair(cfg.ExternalTLSCert, cfg.ExternalTLSKey)
		if err != nil {
			return a, fmt.Errorf("读取对外端口的 TLS 证书失败：%w", err)
		}
		extTLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	}

	models, err := modelcatalog.Load(ctx, modelcatalog.Options{NodeBin: res.NodeBin, PiAIDir: res.PiAIDir, PiVersion: cfg.PiVersion, Path: res.ExecBasePath})
	if err != nil {
		return a, err
	}
	log.Info("已读取 Pi 模型目录", "providers", len(models.Providers))

	templates, err := clitemplate.Load(cfg.CLITemplateDir)
	if err != nil {
		return a, err
	}
	if o.MigrationTarget > 0 {
		err = a.Store.MigrateThrough(ctx, log, o.MigrationTarget)
	} else {
		err = a.Store.Migrate(ctx, log)
	}
	if err != nil {
		return a, err
	}
	defaults, err := a.Store.EnsureDefaults(ctx, cfg.DefaultOrgName, cfg.DefaultProjectName)
	if err != nil {
		return a, err
	}
	// 上一次运行签发的 Agent 令牌全部作废：那些 Pi 进程已随服务端退出。
	if n, err := a.Store.RevokeAgentTokensNotFrom(ctx, a.InstanceID); err != nil {
		return a, err
	} else if n > 0 {
		log.Info("已作废上一次运行签发的 Agent 令牌", "count", n)
	}
	// 先监听：Agent 专用接口的地址要在启动任何 Pi 之前确定。
	a.listener = o.Listener
	if a.listener == nil {
		if a.listener, err = net.Listen("tcp", cfg.ListenAddr); err != nil {
			return a, fmt.Errorf("监听 %s 失败：%w", cfg.ListenAddr, err)
		}
	}

	redisOpts, _ := redis.ParseURL(cfg.RedisURL)
	a.Redis = redis.NewClient(redisOpts)
	a.Coord = coord.New(a.Redis, coord.Options{Prefix: cfg.RedisKeyPrefix, LockTTL: cfg.LockTTL, StreamMaxLen: cfg.EventStreamMaxLen,
		RunTextTTL: cfg.RunTextTTL, OverviewMaxLen: cfg.OverviewMaxLen})

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

	authRoot := filepath.Join(res.WorkRoot, "platform", "auth-helper-v"+cfg.PiVersion)
	if err = os.MkdirAll(filepath.Dir(authRoot), 0700); err != nil {
		return a, err
	}
	helperEntry, authExtension, err := authhelper.Install(authRoot)
	if err != nil {
		return a, err
	}
	packageRoot := res.PiPackageDir
	if packageRoot == "" {
		packageRoot = filepath.Dir(filepath.Dir(filepath.Dir(res.PiAIDir)))
	}
	helper := &modelauth.Helper{Options: modelauth.HelperOptions{NodeBin: res.NodeBin, Entry: helperEntry, WorkRoot: filepath.Dir(authRoot), BasePath: res.ExecBasePath, PiPackageRoot: packageRoot, PiVersion: cfg.PiVersion, Timeout: cfg.AuthLoginTimeout, StopGrace: cfg.PiStopGrace}}
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
	googleHelper := &cliauth.ProcessHelper{Options: cliauth.HelperOptions{WorkRoot: filepath.Dir(authRoot), StopGrace: cfg.PiStopGrace, Runtime: func(ctx context.Context) (cliauth.HelperRuntime, error) {
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
	var modelEnvNames []string
	for _, provider := range models.Providers {
		modelEnvNames = append(modelEnvNames, provider.APIKeyEnvVars...)
	}
	host, _ := os.Hostname()
	username := ""
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	taskSource := &tasks.Source{ModelAuth: authCoordinator, Normal: a.Config, Store: a.Store, WorkRoot: res.WorkRoot, GitBin: res.GitBin, BasePath: res.ExecBasePath, MaxSessionBytes: cfg.TaskMaxSession, CloneTimeout: cfg.GitCloneTimeout, Templates: templates, Installer: installer}
	// Production always enables the canonical task-switch resolver. Only an
	// explicit pre-021 historical-schema fixture omits the module.
	if o.MigrationTarget == 0 || o.MigrationTarget >= 21 {
		taskSource.EnableLarkOverlay()
	}
	taskSource.Log = log
	taskSource.PluginAssets = a.pluginAssets
	taskObserver := &tasks.Observer{Store: a.Store, Normal: recorder, Timeout: cfg.TaskRecordTimeout, Log: log}
	// config.Load 总会给出上下文文件夹的根与文件夹上传上限；直接构造配置（测试）时为空：根放在工作根目录下
	// （不写到进程的当前目录），上限取默认值。
	contextDir, maxFolderUpload := cfg.ContextDir, cfg.MaxFolderUpload
	if contextDir == "" {
		contextDir = filepath.Join(res.WorkRoot, "context")
	}
	if maxFolderUpload <= 0 {
		maxFolderUpload = 1 << 30
	}
	a.Executor = local.New(local.Options{Models: models, Hooks: appHooks{a}, Dialogs: a.askDialog, LarkFactory: larkFactory, GoogleResolver: googleCoordinator, GoogleRuntimeEntry: filepath.Join(authRoot, "google_runtime.py"),
		AuthResolver: authCoordinator, AuthExtension: authExtension, LegacyAuthExtension: filepath.Join(filepath.Dir(authExtension), "legacy-anthropic.mjs"), CodexServiceTierExtension: filepath.Join(filepath.Dir(authExtension), "codex-service-tier.mjs"), PiPackageRoot: packageRoot, AuthIPCTimeout: cfg.AuthIPCTimeout, ModelSecretEnvNames: modelEnvNames,
		WorkRoot: res.WorkRoot, PiBin: res.PiBin, GitBin: res.GitBin, ExecBasePath: res.ExecBasePath, Passthrough: cfg.ExecEnvPassthrough,
		Username: username, MaxProcesses: cfg.PiMaxProcesses, IdleTimeout: cfg.PiIdleTimeout, StartTimeout: cfg.PiStartTimeout,
		StopGrace: cfg.PiStopGrace, AbortTimeout: cfg.AbortTimeout, CloneTimeout: cfg.GitCloneTimeout, InstanceID: a.InstanceID, Hostname: host,
		StderrTailBytes: cfg.PiStderrTailBytes, Observer: taskObserver,
		Tokens: agentTokens{st: a.Store, instance: a.InstanceID}, AgentURL: agentURL(a.listener.Addr()), LoopctlBinDir: loopctlBin, ContextDir: contextDir,
	}, taskSource, templates, installer, log)
	a.Config.Releaser = a.Executor
	// Preserve unresolved handoff process evidence before generic cleanup can
	// remove stale PID records. Missing/invalid provenance fails closed.
	var protectedLark []local.PendingRuntimeHandoff
	var pendingLark []store.TaskLarkRecoveryAction
	// Explicit historical-schema fixtures have no switch tables. Production
	// always migrates fully and must never swallow a recovery-query failure.
	if o.MigrationTarget == 0 || o.MigrationTarget >= 21 {
		var readErr error
		pendingLark, readErr = a.Store.PendingTaskLarkRecovery(ctx)
		if readErr != nil {
			return a, readErr
		}
		protectedLark, err = larkPendingHandoffs(pendingLark)
		if err != nil {
			return a, err
		}
	}
	if err = a.Executor.CleanupStaleExceptPending(ctx, protectedLark); err != nil {
		return a, err
	}
	if err = a.recoverPendingLarkSwitches(ctx, pendingLark, larkRuntime, cfg.TaskRecordTimeout); err != nil {
		return a, err
	}

	a.Conv = conversation.New(a.Store, a.Coord, a.Executor, conversation.Options{
		InstanceID: a.InstanceID, MaxMessage: cfg.MaxMessage, LockRenewInterval: cfg.LockRenewInterval,
		ReconcileInterval: cfg.ReconcileInterval,
	}, log)
	taskSource.PauseNormal = a.Conv.PauseDispatch
	taskSource.FreezeNormal = a.Executor.FreezeForFork
	a.Tasks = tasks.New(a.Store, a.Config, a.Executor, tasks.Options{InstanceID: a.InstanceID, MaxSteps: cfg.TaskMaxSteps, MaxEdges: cfg.TaskMaxEdges, MaxVisits: cfg.TaskMaxVisits, MaxRequest: cfg.TaskMaxRequest, MaxArtifact: cfg.TaskMaxArtifact, ReconcileInterval: cfg.TaskReconcileInterval, LockRenewInterval: cfg.LockRenewInterval, RecordTimeout: cfg.TaskRecordTimeout}, log)
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
	a.Config.AgentRemoved = a.Conv.EmitAgentStatus // Agent 已删除时推送移除事件

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

	a.Folders = folders.New(a.Store, folderGitAccess(a.Config, templates, installer, res.WorkRoot, res.ExecBasePath),
		folders.Options{WorkRoot: res.WorkRoot, ContextDir: contextDir, MaxUpload: maxFolderUpload, GitBin: res.GitBin, CloneTimeout: cfg.GitCloneTimeout}, log)

	a.Store.ImageMaxCount = cfg.MaxMessageImages
	a.Tasks.Hooks = appHooks{a}
	a.API = &httpapi.Server{Hooks: appHooks{a}, CLIImport: a.CLIImport, CLIVerification: a.CLIVerification, CLIAuth: a.CLIAuth, ModelAuth: a.ModelAuth, ProjectCredentials: projectCredentials,
		O: httpapi.Options{
			AllowedHosts: cfg.AllowedHosts, ConsoleOrigins: cfg.ConsoleOrigins, SSEKeepalive: cfg.SSEKeepalive, TaskEventPoll: cfg.TaskEventPoll, Defaults: defaults,
			Limits: apitypes.Limits{MaxMessageImages: cfg.MaxMessageImages, MaxMessageImageBytes: cfg.MaxMessageImageBytes, MaxMessageImagePixels: cfg.MaxMessageImagePixels, MaxUploadFile: cfg.MaxUploadFile, MaxFolderUpload: maxFolderUpload, MaxMessage: cfg.MaxMessage, MaxInboxMessage: cfg.MaxInboxMessage, MaxSkillArchive: cfg.MaxSkillArchive,
				MaxSkillEntries: cfg.MaxSkillEntries, MaxCredential: cfg.MaxCredential,
				TextEdit: apitypes.TextEdit{MaxBytes: cfg.TextEditMax, Extensions: cfg.TextEditExtensions}},
		},
		Config: a.Config, Conv: a.Conv, Models: models, Templates: templates, Installer: installer, Store: a.Store, Coord: a.Coord,
		Tasks: a.Tasks, Records: &runrecord.Service{Store: a.Store}, Inbox: inboxSvc, Folders: a.Folders, Log: log,
	}
	if o.MigrationTarget == 0 || o.MigrationTarget >= 31 {
		if err = a.assemblePlugins(cfg, res, log); err != nil {
			return a, err
		}
	}
	baseCtx, baseCancel := context.WithCancel(context.Background())
	a.baseCancel = baseCancel
	// 文件夹：中断的拉取标为失败、清空回收区；在任何 Pi 启动之前一次性回放退役文件夹来源中未落到磁盘的人工修改。
	if o.MigrationTarget == 0 || o.MigrationTarget >= 29 {
		if err = a.Folders.Start(ctx); err != nil {
			return a, err
		}
		legacy, err := a.Folders.ApplyLegacyFolderSources(ctx)
		if err != nil {
			return a, fmt.Errorf("回放退役文件夹来源失败：%w", err)
		}
		if legacy.Sources > 0 || legacy.UnclonedGitDirs > 0 {
			log.Info("已回放退役文件夹来源", "sources", legacy.Sources, "filesWritten", legacy.FilesWritten, "filesDeleted", legacy.FilesDeleted,
				"filesSkipped", legacy.FilesSkipped, "unclonedGitSources", legacy.UnclonedGitDirs)
		}
	}
	for _, p := range local.AncestorEntryFiles(res.WorkRoot) {
		log.Warn("工作根目录的上级目录中有上下文入口文件，Pi 会把它读进每个 Agent 的系统提示；请移走", "path", p)
	}
	if err = a.Conv.Start(ctx); err != nil {
		return a, err
	}
	if err = a.Tasks.Start(ctx); err != nil {
		return a, err
	}

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

// ExternalAddr 返回对外端口的实际监听地址；未开启时为空。
func (a *App) ExternalAddr() string {
	if a.extListener == nil {
		return ""
	}
	return a.extListener.Addr().String()
}

// Addr 返回实际监听地址。
func (a *App) Addr() string { return a.listener.Addr().String() }

// Serve 开始提供 HTTP 服务，直到 Shutdown。
func (a *App) Serve() error {
	if a.external != nil {
		a.Log.Info("开始提供对外端口（TLS）", "addr", a.ExternalAddr())
		go func() {
			if err := a.external.Serve(a.extListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				a.Log.Error("对外端口停止服务", "err", err)
			}
		}()
	}
	if a.PluginHost != nil {
		settings, err := a.Store.GetPluginSettings(context.Background())
		if err != nil {
			return fmt.Errorf("读取插件设置失败：%w", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		a.pluginCancel = cancel
		a.PluginHost.Start(settings.Disabled)
		go a.Plugins.Listen(ctx)
	}
	a.Log.Info("开始提供服务", "addr", a.Addr())
	if err := a.http.Serve(a.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown 优雅退出：停止接收请求、停止派发、结束全部 Pi 进程。
// 进行中的执行保持原状态，下次启动时标为“服务端重启中断”。
func (a *App) Shutdown(ctx context.Context) error {
	var errs []error
	if a.http != nil {
		// SSE 连接不会自行结束：取消所有请求共用的上下文，让事件流尽快退出，再等待其余请求。
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
		if a.pluginCancel != nil {
			a.pluginCancel()
		}
		errs = append(errs, a.PluginHost.Stop(ctx))
	}
	if a.Tasks != nil {
		errs = append(errs, a.Tasks.Stop(ctx))
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
	a.closeResources()
	a.Log.Info("服务端已停止")
	return errors.Join(errs...)
}

// restoreInstallFailures 把运行记录中各 CLI 版本最近一次失败的安装恢复到安装器，重启后仍能看到失败原因。
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

// agentTokens 用数据库保存 Agent 令牌：只存摘要，记下签发的服务端实例。
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

// appHooks 把同步 Hook 交给插件服务；插件服务在执行器与任务服务之后装配，没有装配时（历史 schema 的测试）不调用插件。
type appHooks struct{ a *App }

func (h appHooks) Hook(ctx context.Context, kind pluginhost.Kind, event map[string]any, project *uuid.UUID) (pluginhost.Outcome, error) {
	if h.a.Plugins == nil {
		return pluginhost.Outcome{}, nil
	}
	return h.a.Plugins.Hook(ctx, kind, event, project)
}

// askDialog 把扩展在一轮之中发来的 confirm、select、input 变成这个 Agent 发给控制台的提问（plugins/SPEC.md §11），
// 等到回答、到期或这一轮结束；出错时按取消回应。
func (a *App) askDialog(ctx context.Context, agentID, runID uuid.UUID, req pirpc.DialogRequest) pirpc.DialogResponse {
	if a.API == nil || a.API.Inbox == nil {
		return pirpc.DialogResponse{Cancelled: true, Outcome: "cancelled"}
	}
	ans, err := a.API.Inbox.AskDialog(ctx, inbox.Dialog{AgentID: agentID, RunID: runID, RequestID: req.ID, Method: req.Method, Title: req.Title,
		Message: req.Message, Placeholder: req.Placeholder, Options: req.Options, Timeout: req.Timeout})
	if err != nil {
		a.Log.Warn("扩展对话提问失败，按取消回应", "agentId", agentID, "runId", runID, "err", err)
	}
	return pirpc.DialogResponse{Cancelled: ans.Cancelled, Confirmed: ans.Confirmed, Value: ans.Value, Outcome: ans.Outcome, QuestionID: ans.QuestionID}
}

// pluginAssets 返回 Agent 已授权插件交给 Pi 的东西；插件服务没有装配时（历史 schema 的测试）为空。
func (a *App) pluginAssets(ctx context.Context, agentID uuid.UUID) (agentconfig.PluginAssets, error) {
	if a.Plugins == nil {
		return agentconfig.PluginAssets{}, nil
	}
	return a.Plugins.AgentAssets(ctx, agentID)
}

// assemblePlugins 装配插件第二版：安装目录在 <work root>/platform/plugins/；配置了宿主入口时准备宿主监护（Serve 时启动）。
// 宿主的环境变量只给必要的几项，不继承服务端的环境（其中有数据库地址等）。
func (a *App) assemblePlugins(cfg *config.Config, res *preflight.Resolved, log *slog.Logger) error {
	root := filepath.Join(res.WorkRoot, "platform", "plugins")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	a.Plugins = &plugins.Service{Store: a.Store, Root: root, OfficialDir: cfg.PluginOfficialDir, OfficialMarket: cfg.PluginMarketURL, NodeBin: res.NodeBin, GitBin: res.GitBin,
		APIURL: strings.TrimSuffix(agentURL(a.listener.Addr()), "/agent/v1") + "/api/v1", PlatformVersion: buildinfo.Version(), Log: log}
	if err := a.Plugins.Init(); err != nil {
		return err
	}
	a.API.Plugins = a.Plugins
	if cfg.PluginHostEntry == "" {
		log.Info("没有配置插件宿主入口，插件的平台半边不运行", "env", config.EnvPluginHostEntry)
		return nil
	}
	env := []string{"PATH=" + res.ExecBasePath, "HOME=" + root, "LANG=C.UTF-8", "LOOP_NOW_PLUGINS_ROOT=" + root, "LOOP_NOW_API_URL=" + a.Plugins.APIURL}
	if tz := os.Getenv("TZ"); tz != "" {
		env = append(env, "TZ="+tz)
	}
	a.PluginHost = pluginhost.New(pluginhost.Options{Command: []string{res.NodeBin, cfg.PluginHostEntry}, Dir: root, Env: env, Store: a.Store,
		Specs: a.Plugins.Specs, Log: log.With("component", "plugin-host")})
	a.Plugins.Host = a.PluginHost
	return nil
}

// agentURL 返回注入给 Pi 的 Agent 专用接口地址。Pi 与服务端在同一台机器上：
// 监听回环地址时用它本身；监听全部地址时用 127.0.0.1；监听某个具体地址时用该地址。端口与监听器相同。
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
		_ = a.listener.Close() // 装配失败：监听器还没交给 HTTP 服务
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
			a.Log.Warn("CLI共享安装尚未收敛", "errorCode", "installer_drain_timeout")
		}
		cancel()
	}
	if a.Redis != nil {
		_ = a.Redis.Close()
	}
	if a.Store != nil {
		a.Store.Close()
	}
}
