// Package httpapi implements the console HTTP API (with api/openapi.yaml as the contract) and the SSE event streams.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/buildinfo"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/cliauth"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/cliverify"
	"loop-now/internal/server/conversation"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/folders"
	"loop-now/internal/server/inbox"
	"loop-now/internal/server/localmode"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/modelconfig"
	"loop-now/internal/server/pluginhost"
	"loop-now/internal/server/plugins"
	"loop-now/internal/server/reqid"
	"loop-now/internal/server/runrecord"
	"loop-now/internal/server/runtimes"
	"loop-now/internal/server/store"
	"loop-now/internal/server/tasks"
)

// Options are the parameters of the HTTP layer.
type Options struct {
	AllowedHosts   []string
	ConsoleOrigins []string
	// Console answers paths outside the API on the console port (the console's static files); when nil these paths get
	// a 404.
	Console       http.Handler
	SSEKeepalive  time.Duration
	TaskEventPoll time.Duration
	Limits        apitypes.Limits
	Defaults      store.DefaultContext
}

// Server holds the business services.
type Server struct {
	// Runtimes reports whether Agent runtimes are available on this machine; when nil the list is empty.
	Runtimes *runtimes.Checker
	Plugins  *plugins.Service
	// Hooks is the entry point for synchronous Hooks (plugins.Service); when nil, plugins are not called.
	Hooks              pluginhost.Hooker
	CLIImport          CLIImportController
	CLIVerification    cliverify.Controller
	CLIAuth            *cliauth.Service
	ProjectCredentials *modelauth.ProjectService
	ModelAuth          modelauth.Controller
	ModelSettings      *modelconfig.Service
	O                  Options
	Config             *agentconfig.Service
	Conv               *conversation.Service
	Models             modelcatalog.Reader
	Templates          *clitemplate.Catalog
	Installer          *clitemplate.Installer
	Store              *store.Store
	Coord              *coord.Coord
	Records            *runrecord.Service
	Inbox              *inbox.Service
	Folders            *folders.Service
	LocalMode          *localmode.Service
	Tasks              *tasks.Service
	Log                *slog.Logger
	// PiCommands returns the command list Pi reported the last time the Agent started it (nil if Pi has never been
	// started); when PiCommands is nil the command list is always empty.
	PiCommands func(uuid.UUID) (*executor.PiCommands, error)
}

// Access is the set of identities a route allows; the zero value is console only, so new routes are not externally open
// by default.
type Access int

const (
	ConsoleOnly        Access = iota // console only (x-access: console in the contract)
	ConsoleAndExternal               // console and external identities (x-access: console-and-external in the contract)
)

// String returns how x-access is written in the contract.
func (a Access) String() string {
	if a == ConsoleAndExternal {
		return "console-and-external"
	}
	return "console"
}

// Route is one API route, for comparison by the contract tests.
type Route struct {
	Method  string
	Pattern string // without the /api/v1 or /agent/v1 prefix
	Op      string // operationId in the contract, used by audit records
	Access  Access
	handler func(http.ResponseWriter, *http.Request)
}

// Routes returns all console API routes.
func (s *Server) Routes() []Route {
	return []Route{
		{"PUT", "/agents/{agentId}/message-images/{imageId}", "uploadAgentMessageImage", ConsoleOnly, s.uploadMessageImage},
		{"PUT", "/tasks/{taskId}/nodes/{nodeRunId}/message-images/{imageId}", "uploadTaskNodeMessageImage", ConsoleOnly, s.uploadMessageImage},
		{"GET", "/message-images/{imageId}", "getMessageImage", ConsoleOnly, s.getMessageImage},
		{"GET", "/plugins", "listPlugins", ConsoleAndExternal, s.listPlugins},
		{"POST", "/plugins", "installPlugin", ConsoleAndExternal, s.installPlugin},
		{"POST", "/plugin-previews", "previewPlugin", ConsoleAndExternal, s.previewPlugin},
		{"GET", "/plugin-previews/{previewId}/readme", "getPluginPreviewReadme", ConsoleAndExternal, s.getPluginPreviewReadme},
		{"GET", "/plugins/{pluginId}", "getPlugin", ConsoleAndExternal, s.getPlugin},
		{"GET", "/plugins/{pluginId}/readme", "getPluginReadme", ConsoleAndExternal, s.getPluginReadme},
		{"DELETE", "/plugins/{pluginId}", "uninstallPlugin", ConsoleAndExternal, s.uninstallPlugin},
		{"POST", "/plugins/{pluginId}/update", "updatePlugin", ConsoleAndExternal, s.updatePlugin},
		{"GET", "/plugins/{pluginId}/config", "getPluginConfig", ConsoleAndExternal, s.getPluginConfig},
		{"PUT", "/plugins/{pluginId}/config", "putPluginConfig", ConsoleAndExternal, s.putPluginConfig},
		{"GET", "/plugins/{pluginId}/logs", "listPluginLogs", ConsoleAndExternal, s.listPluginLogs},
		{"GET", "/plugins/{pluginId}/deliveries", "listPluginDeliveries", ConsoleAndExternal, s.listPluginDeliveries},
		{"GET", "/plugins/{pluginId}/state", "listPluginState", ConsoleAndExternal, s.listPluginState},
		{"POST", "/plugins/{pluginId}/actions/{actionId}", "invokePluginAction", ConsoleAndExternal, s.invokePluginAction},
		{"GET", "/plugin-markets", "listPluginMarkets", ConsoleAndExternal, s.listPluginMarkets},
		{"GET", "/plugin-sources/builtin", "listBuiltinPluginSources", ConsoleAndExternal, s.listBuiltinPluginSources},
		{"GET", "/plugin-host", "getPluginHost", ConsoleAndExternal, s.getPluginHost},
		{"POST", "/plugin-host/restart", "restartPluginHost", ConsoleAndExternal, s.restartPluginHost},
		{"GET", "/local-mode-settings", "getLocalModeSettings", ConsoleOnly, s.getLocalModeSettings},
		{"GET", "/runtimes", "listRuntimes", ConsoleOnly, s.listRuntimes},
		{"PATCH", "/local-mode-settings", "updateLocalModeSettings", ConsoleOnly, s.updateLocalModeSettings},
		{"GET", "/projects/{projectId}/local-directories", "getProjectLocalDirectories", ConsoleOnly, s.getProjectLocalDirectories},
		{"PUT", "/projects/{projectId}/local-directories", "putProjectLocalDirectories", ConsoleOnly, s.putProjectLocalDirectories},
		{"GET", "/agents/{agentId}/local-directories", "getAgentLocalDirectories", ConsoleOnly, s.getAgentLocalDirectories},
		{"PUT", "/agents/{agentId}/local-directories", "putAgentLocalDirectories", ConsoleOnly, s.putAgentLocalDirectories},
		{"GET", "/plugin-settings", "getPluginSettings", ConsoleAndExternal, s.getPluginSettings},
		{"PUT", "/plugin-settings", "putPluginSettings", ConsoleAndExternal, s.putPluginSettings},
		{"GET", "/projects/{projectId}/plugins", "listProjectPlugins", ConsoleAndExternal, s.listProjectPlugins},
		{"PUT", "/projects/{projectId}/plugins/{pluginId}", "putProjectPlugin", ConsoleAndExternal, s.putProjectPlugin},
		{"GET", "/projects/{projectId}/plugins/{pluginId}/accounts", "listPluginAccounts", ConsoleAndExternal, s.listPluginAccounts},
		{"POST", "/projects/{projectId}/plugins/{pluginId}/accounts", "createPluginAccount", ConsoleAndExternal, s.createPluginAccount},
		{"PUT", "/projects/{projectId}/plugins/{pluginId}/accounts/{accountId}", "updatePluginAccount", ConsoleAndExternal, s.updatePluginAccount},
		{"DELETE", "/projects/{projectId}/plugins/{pluginId}/accounts/{accountId}", "deletePluginAccount", ConsoleAndExternal, s.deletePluginAccount},
		{"GET", "/agents/{agentId}/plugins", "listAgentPlugins", ConsoleAndExternal, s.listAgentPlugins},
		{"GET", "/agents/{agentId}/commands", "listAgentCommands", ConsoleOnly, s.listAgentCommands},
		{"PUT", "/agents/{agentId}/plugins/{pluginId}", "putAgentPlugin", ConsoleAndExternal, s.putAgentPlugin},

		{"POST", "/projects/{projectId}/cli-credential-import/scans", "scanProjectCLICredentialImports", ConsoleOnly, s.scanProjectCLICredentialImports},
		{"POST", "/projects/{projectId}/cli-credential-import/imports", "startProjectCLICredentialImport", ConsoleOnly, s.startProjectCLICredentialImport},
		{"GET", "/projects/{projectId}/cli-credential-import/imports/{operationId}", "getProjectCLICredentialImport", ConsoleOnly, s.getProjectCLICredentialImport},
		{"POST", "/projects/{projectId}/cli-credential-import/imports/{operationId}/cancel", "cancelProjectCLICredentialImport", ConsoleOnly, s.cancelProjectCLICredentialImport},

		{"GET", "/projects/{projectId}/credential-accounts/{accountId}/verification-operations", "getProjectCLIVerificationState", ConsoleAndExternal, s.getProjectCLIVerificationState},
		{"POST", "/projects/{projectId}/credential-accounts/{accountId}/verification-operations", "startProjectCLIVerification", ConsoleAndExternal, s.startProjectCLIVerification},
		{"GET", "/projects/{projectId}/credential-accounts/{accountId}/verification-operations/{operationId}", "getProjectCLIVerificationOperation", ConsoleAndExternal, s.getProjectCLIVerificationOperation},
		{"POST", "/projects/{projectId}/credential-accounts/{accountId}/verification-operations/{operationId}/cancel", "cancelProjectCLIVerificationOperation", ConsoleAndExternal, s.cancelProjectCLIVerificationOperation},
		{"GET", "/projects/{projectId}/credential-accounts", "listProjectCredentialAccounts", ConsoleAndExternal, s.listProjectCredentialAccounts},
		{"POST", "/projects/{projectId}/credential-accounts", "createProjectCredentialAccount", ConsoleAndExternal, s.createProjectCredentialAccount},
		{"GET", "/projects/{projectId}/credential-accounts/{accountId}", "getProjectCredentialAccount", ConsoleAndExternal, s.getProjectCredentialAccount},
		{"PATCH", "/projects/{projectId}/credential-accounts/{accountId}", "renameProjectCredentialAccount", ConsoleAndExternal, s.renameProjectCredentialAccount},
		{"DELETE", "/projects/{projectId}/credential-accounts/{accountId}", "deleteProjectCredentialAccount", ConsoleAndExternal, s.deleteProjectCredentialAccount},
		{"PUT", "/projects/{projectId}/credential-accounts/{accountId}/credential", "replaceProjectCredentialAccount", ConsoleAndExternal, s.replaceProjectCredentialAccount},
		{"GET", "/projects/{projectId}/credential-account-impact", "getProjectCredentialAccountImpact", ConsoleAndExternal, s.getProjectCredentialAccountImpact},
		{"GET", "/agents/{agentId}/db-connections/{cli}", "getAgentDBConnections", ConsoleAndExternal, s.getAgentDBConnections},
		{"PUT", "/agents/{agentId}/db-connections/{cli}", "putAgentDBConnections", ConsoleAndExternal, s.putAgentDBConnections},
		{"DELETE", "/agents/{agentId}/db-connections/{cli}", "deleteAgentDBConnections", ConsoleAndExternal, s.deleteAgentDBConnections},
		{"GET", "/agents/{agentId}/credential-selections", "getAgentCredentialSelections", ConsoleAndExternal, s.getAgentCredentialSelections},
		{"PUT", "/agents/{agentId}/credential-selections", "putAgentCredentialSelection", ConsoleAndExternal, s.putAgentCredentialSelection},
		{"POST", "/projects/{projectId}/cli-auth/operations", "startProjectCLIAuthOperation", ConsoleAndExternal, s.startProjectCLIAuthOperation},
		{"GET", "/projects/{projectId}/cli-auth/operations", "listProjectCLIAuthOperations", ConsoleAndExternal, s.listProjectCLIAuthOperations},
		{"GET", "/projects/{projectId}/cli-auth/operations/{operationId}", "getProjectCLIAuthOperation", ConsoleAndExternal, s.getProjectCLIAuthOperation},
		{"POST", "/projects/{projectId}/cli-auth/operations/{operationId}/cancel", "cancelProjectCLIAuthOperation", ConsoleAndExternal, s.cancelProjectCLIAuthOperation},
		{"GET", "/projects/{projectId}/model-auth/operations", "listProjectModelAuthOperations", ConsoleAndExternal, s.listProjectModelAuthOperations},
		{"POST", "/projects/{projectId}/model-auth/operations", "startProjectModelAuthLogin", ConsoleAndExternal, s.startProjectModelAuthLogin},
		{"GET", "/projects/{projectId}/model-auth/operations/{operationId}", "getProjectModelAuthLogin", ConsoleAndExternal, s.getProjectModelAuthLogin},
		{"POST", "/projects/{projectId}/model-auth/operations/{operationId}/cancel", "cancelProjectModelAuthLogin", ConsoleAndExternal, s.cancelProjectModelAuthLogin},
		{"POST", "/projects/{projectId}/model-auth/operations/{operationId}/input", "answerProjectModelAuthLogin", ConsoleAndExternal, s.answerProjectModelAuthLogin},
		{"GET", "/projects/{projectId}/prompt", "getProjectPrompt", ConsoleOnly, s.getProjectPrompt},
		{"PUT", "/projects/{projectId}/prompt", "putProjectPrompt", ConsoleOnly, s.putProjectPrompt},
		{"GET", "/agents/{agentId}/model-auth", "getModelAuthState", ConsoleOnly, s.getModelAuthState},
		{"POST", "/agents/{agentId}/model-auth/operations", "startModelAuthLogin", ConsoleOnly, s.startModelAuthLogin},
		{"GET", "/agents/{agentId}/model-auth/operations/{operationId}", "getModelAuthLogin", ConsoleOnly, s.getModelAuthLogin},
		{"POST", "/agents/{agentId}/model-auth/operations/{operationId}/cancel", "cancelModelAuthLogin", ConsoleOnly, s.cancelModelAuthLogin},
		{"POST", "/agents/{agentId}/model-auth/operations/{operationId}/input", "answerModelAuthLogin", ConsoleOnly, s.answerModelAuthLogin},
		{"GET", "/tasks/{taskId}", "getTask", ConsoleOnly, s.getTask},
		{"GET", "/tasks/{taskId}/nodes/{nodeRunId}", "getTaskNodeRun", ConsoleOnly, s.getTaskNode},
		{"GET", "/tasks/{taskId}/nodes/{nodeRunId}/messages", "listTaskNodeMessages", ConsoleOnly, s.listTaskNodeMessages},
		{"GET", "/tasks/{taskId}/nodes/{nodeRunId}/message-capability", "getTaskNodeMessageCapability", ConsoleOnly, s.getTaskNodeMessageCapability},
		{"POST", "/tasks/{taskId}/nodes/{nodeRunId}/messages", "sendTaskNodeMessage", ConsoleOnly, s.sendTaskNodeMessage},
		{"POST", "/tasks/{taskId}/nodes/{nodeRunId}/inbox-messages", "sendTaskNodeInboxMessage", ConsoleOnly, s.sendTaskNodeInboxMessage},
		{"GET", "/tasks/{taskId}/nodes/{nodeRunId}/inbox-messages", "listTaskNodeInboxMessages", ConsoleOnly, s.listTaskNodeInboxMessages},
		{"POST", "/tasks/{taskId}/nodes/{nodeRunId}/inbox-messages/{messageId}/promote-to-interrupt", "promoteTaskNodeInboxMessage", ConsoleOnly, s.nodeQueueAction("messageId", s.Tasks.PromoteNodeInboxMessage)},
		{"POST", "/tasks/{taskId}/nodes/{nodeRunId}/inbox-messages/{messageId}/cancel", "cancelTaskNodeInboxMessage", ConsoleOnly, s.nodeQueueAction("messageId", s.Tasks.CancelNodeQueueMessage)},
		{"POST", "/tasks/{taskId}/nodes/{nodeRunId}/runs/{runId}/stop", "stopTaskNodeRun", ConsoleOnly, s.nodeQueueAction("runId", s.Tasks.StopNodeRun)},
		{"GET", "/tasks/{taskId}/nodes/{nodeRunId}/queue", "getTaskNodeQueue", ConsoleOnly, s.getTaskNodeQueue},
		{"GET", "/tasks/{taskId}/nodes/{nodeRunId}/inbox-actions/{actionId}", "getTaskNodeInboxAction", ConsoleOnly, s.getTaskNodeInboxAction},
		{"GET", "/tasks/{taskId}/nodes/{nodeRunId}/runs/{runId}/timeline", "getTaskNodeRunTimeline", ConsoleOnly, s.getTaskNodeRunTimeline},
		{"POST", "/tasks/{taskId}/retry", "retryTask", ConsoleOnly, s.retryTask},
		{"POST", "/tasks/{taskId}/lark-account-switches", "switchTaskLarkAccount", ConsoleOnly, s.switchTaskLarkAccount},
		{"GET", "/tasks/{taskId}/lark-account-switches/{actionId}", "getTaskLarkAccountSwitch", ConsoleOnly, s.getTaskLarkAccountSwitch},
		{"POST", "/tasks/{taskId}/cancel", "cancelTask", ConsoleOnly, s.cancelTask},
		{"GET", "/tasks/{taskId}/artifacts/{artifactId}", "downloadTaskArtifact", ConsoleOnly, s.downloadTaskArtifact},

		{"GET", "/projects/{projectId}/tasks", "listProjectTasks", ConsoleOnly, s.listProjectTasks},
		{"GET", "/projects/{projectId}/tasks/counts", "countProjectTasks", ConsoleOnly, s.countProjectTasks},
		{"GET", "/projects/{projectId}/tasks/events", "streamProjectTaskEvents", ConsoleOnly, s.streamProjectTaskEvents},
		{"GET", "/tasks/{taskId}/events", "streamTaskEvents", ConsoleOnly, s.streamTaskEvents},

		{"GET", "/context", "getContext", ConsoleAndExternal, s.getContext},
		{"GET", "/health", "getHealth", ConsoleAndExternal, s.getHealth},
		{"GET", "/model-providers", "listModelProviders", ConsoleAndExternal, s.listModelProviders},
		{"GET", "/model-settings", "getModelSettings", ConsoleOnly, s.getModelSettings},
		{"PUT", "/model-settings", "putModelSettings", ConsoleOnly, s.putModelSettings},
		{"GET", "/model-providers/{providerId}/models", "listModels", ConsoleAndExternal, s.listModels},
		{"GET", "/cli-templates", "listCliTemplates", ConsoleAndExternal, s.listCliTemplates},
		{"GET", "/projects", "listProjects", ConsoleAndExternal, s.listProjects},
		{"POST", "/projects", "createProject", ConsoleAndExternal, s.createProject},
		{"GET", "/projects/{projectId}", "getProject", ConsoleAndExternal, s.getProject},
		{"PATCH", "/projects/{projectId}", "updateProject", ConsoleAndExternal, s.updateProject},
		{"DELETE", "/projects/{projectId}", "deleteProject", ConsoleAndExternal, s.deleteProject},
		{"PUT", "/projects/{projectId}/manager", "setProjectManager", ConsoleAndExternal, s.setProjectManager},
		{"DELETE", "/projects/{projectId}/manager", "clearProjectManager", ConsoleAndExternal, s.clearProjectManager},
		{"GET", "/projects/{projectId}/agents", "listProjectAgents", ConsoleAndExternal, s.listProjectAgents},
		{"POST", "/projects/{projectId}/agents", "createProjectAgent", ConsoleAndExternal, s.createProjectAgent},
		{"GET", "/platform/agents", "listPlatformAgents", ConsoleAndExternal, s.listPlatformAgents},
		{"POST", "/platform/agents", "createPlatformAgent", ConsoleAndExternal, s.createPlatformAgent},
		{"GET", "/agents/{agentId}", "getAgent", ConsoleAndExternal, s.getAgent},
		{"PATCH", "/agents/{agentId}", "updateAgent", ConsoleAndExternal, s.updateAgent},
		{"DELETE", "/agents/{agentId}", "deleteAgent", ConsoleAndExternal, s.deleteAgent},
		{"GET", "/agents/{agentId}/folder/entries", "listAgentFolderEntries", ConsoleOnly, s.listFolder(s.agentFolder)},
		{"DELETE", "/agents/{agentId}/folder/entries", "deleteAgentFolderEntry", ConsoleOnly, s.deleteFolderEntry(s.agentFolder)},
		{"GET", "/agents/{agentId}/folder/content", "getAgentFolderFileContent", ConsoleOnly, s.downloadFolderFile(s.agentFolder)},
		{"PUT", "/agents/{agentId}/folder/content", "putAgentFolderFileContent", ConsoleOnly, s.putFolderFile(s.agentFolder)},
		{"POST", "/agents/{agentId}/folder/directories", "createAgentFolderDirectory", ConsoleOnly, s.mkdirFolder(s.agentFolder)},
		{"POST", "/agents/{agentId}/folder/move", "moveAgentFolderEntry", ConsoleOnly, s.moveFolderEntry(s.agentFolder)},
		{"GET", "/agents/{agentId}/folder/git-pulls", "listAgentFolderGitPulls", ConsoleOnly, s.listGitPulls},
		{"POST", "/agents/{agentId}/folder/git-pulls", "startAgentFolderGitPull", ConsoleOnly, s.startGitPull},
		{"GET", "/agents/{agentId}/folder/git-pulls/{operationId}", "getAgentFolderGitPull", ConsoleOnly, s.getGitPull},
		{"GET", "/projects/{projectId}/context-folder/entries", "listProjectContextFolderEntries", ConsoleOnly, s.listFolder(s.projectContextFolder)},
		{"DELETE", "/projects/{projectId}/context-folder/entries", "deleteProjectContextFolderEntry", ConsoleOnly, s.deleteFolderEntry(s.projectContextFolder)},
		{"GET", "/projects/{projectId}/context-folder/content", "getProjectContextFolderFileContent", ConsoleOnly, s.downloadFolderFile(s.projectContextFolder)},
		{"PUT", "/projects/{projectId}/context-folder/content", "putProjectContextFolderFileContent", ConsoleOnly, s.putFolderFile(s.projectContextFolder)},
		{"POST", "/projects/{projectId}/context-folder/directories", "createProjectContextFolderDirectory", ConsoleOnly, s.mkdirFolder(s.projectContextFolder)},
		{"POST", "/projects/{projectId}/context-folder/move", "moveProjectContextFolderEntry", ConsoleOnly, s.moveFolderEntry(s.projectContextFolder)},
		{"GET", "/context-folder/entries", "listGlobalContextFolderEntries", ConsoleOnly, s.listFolder(s.globalContextFolder)},
		{"DELETE", "/context-folder/entries", "deleteGlobalContextFolderEntry", ConsoleOnly, s.deleteFolderEntry(s.globalContextFolder)},
		{"GET", "/context-folder/content", "getGlobalContextFolderFileContent", ConsoleOnly, s.downloadFolderFile(s.globalContextFolder)},
		{"PUT", "/context-folder/content", "putGlobalContextFolderFileContent", ConsoleOnly, s.putFolderFile(s.globalContextFolder)},
		{"POST", "/context-folder/directories", "createGlobalContextFolderDirectory", ConsoleOnly, s.mkdirFolder(s.globalContextFolder)},
		{"POST", "/context-folder/move", "moveGlobalContextFolderEntry", ConsoleOnly, s.moveFolderEntry(s.globalContextFolder)},
		{"PUT", "/agents/{agentId}/clis/{cli}", "addAgentCli", ConsoleAndExternal, s.addAgentCli},
		{"DELETE", "/agents/{agentId}/clis/{cli}", "removeAgentCli", ConsoleAndExternal, s.removeAgentCli},
		{"PUT", "/agents/{agentId}/clis/{cli}/credential", "putCliCredential", ConsoleAndExternal, s.putCliCredential},
		{"DELETE", "/agents/{agentId}/clis/{cli}/credential", "deleteCliCredential", ConsoleAndExternal, s.deleteCliCredential},
		{"PUT", "/agents/{agentId}/model-credential", "putModelCredential", ConsoleAndExternal, s.putModelCredential},
		{"DELETE", "/agents/{agentId}/model-credential", "deleteModelCredential", ConsoleAndExternal, s.deleteModelCredential},
		{"GET", "/skills", "listSkillTemplates", ConsoleAndExternal, s.listSkillTemplates},
		{"POST", "/skills", "createSkillTemplate", ConsoleAndExternal, s.createSkillTemplate},
		{"GET", "/skills/{skillId}", "getSkillTemplate", ConsoleAndExternal, s.getSkillTemplate},
		{"PUT", "/skills/{skillId}", "replaceSkillTemplate", ConsoleAndExternal, s.replaceSkillTemplate},
		{"DELETE", "/skills/{skillId}", "deleteSkillTemplate", ConsoleAndExternal, s.deleteSkillTemplate},
		{"PUT", "/skills/{skillId}/skill-md", "updateSkillTemplateMarkdown", ConsoleAndExternal, s.updateSkillTemplateMarkdown},
		{"GET", "/agents/{agentId}/skills", "listAgentSkills", ConsoleAndExternal, s.listAgentSkills},
		{"POST", "/agents/{agentId}/skills", "addAgentSkill", ConsoleAndExternal, s.addAgentSkill},
		{"GET", "/agents/{agentId}/skills/{agentSkillId}", "getAgentSkill", ConsoleAndExternal, s.getAgentSkill},
		{"DELETE", "/agents/{agentId}/skills/{agentSkillId}", "removeAgentSkill", ConsoleAndExternal, s.removeAgentSkill},
		{"PUT", "/agents/{agentId}/skills/{agentSkillId}/skill-md", "updateAgentSkillMarkdown", ConsoleAndExternal, s.updateAgentSkillMarkdown},
		{"POST", "/agents/{agentId}/skills/{agentSkillId}/recopy", "recopyAgentSkill", ConsoleAndExternal, s.recopyAgentSkill},
		{"GET", "/agents/{agentId}/messages", "listMessages", ConsoleAndExternal, s.listMessages},
		{"POST", "/agents/{agentId}/messages", "sendMessage", ConsoleOnly, s.sendMessage},
		{"GET", "/agents/{agentId}/queue", "getAgentQueue", ConsoleOnly, s.getAgentQueue},
		{"POST", "/agents/{agentId}/runs/{runId}/promote-to-interrupt", "promoteRunToInterrupt", ConsoleOnly, s.queueAction("runId", s.Conv.PromoteToInterrupt)},
		{"POST", "/agents/{agentId}/runs/{runId}/cancel", "cancelQueuedRun", ConsoleOnly, s.queueAction("runId", s.Conv.CancelRun)},
		{"POST", "/agents/{agentId}/runs/{runId}/stop", "stopRun", ConsoleOnly, s.queueAction("runId", s.Conv.StopRun)},
		{"POST", "/agents/{agentId}/inbox-messages/{messageId}/promote-to-interrupt", "promoteAgentInboxMessage", ConsoleOnly, s.queueAction("messageId", s.Conv.PromoteInboxMessage)},
		{"POST", "/agents/{agentId}/inbox-messages/{messageId}/cancel", "cancelAgentInboxMessage", ConsoleOnly, s.queueAction("messageId", s.Conv.CancelInboxMessage)},
		{"GET", "/agents/{agentId}/status", "getAgentStatus", ConsoleAndExternal, s.getAgentStatus},
		{"POST", "/agents/{agentId}/session/reset", "resetSession", ConsoleAndExternal, s.resetSession},
		{"GET", "/agents/{agentId}/session/export", "exportAgentSession", ConsoleOnly, s.exportSession},
		{"GET", "/agents/{agentId}/events", "streamAgentEvents", ConsoleOnly, s.streamEvents},
		{"GET", "/agents/{agentId}/runs/{runId}/timeline", "getRunTimeline", ConsoleAndExternal, s.getRunTimeline},
		{"GET", "/overview/events", "streamOverviewEvents", ConsoleOnly, s.streamOverview},
		{"GET", "/usage", "getUsage", ConsoleAndExternal, s.getUsage},
		{"GET", "/anomalies", "listAnomalies", ConsoleAndExternal, s.listAnomalies},
		{"GET", "/stats", "getStats", ConsoleOnly, s.getStats},
		{"GET", "/whoami", "getWhoami", ConsoleAndExternal, s.whoami},
		{"GET", "/agents", "findAgents", ConsoleAndExternal, s.findAgents},
		{"GET", "/inbox/messages", "listInboxMessages", ConsoleAndExternal, s.listInbox},
		{"POST", "/inbox/messages", "sendInboxMessage", ConsoleAndExternal, s.sendInbox},
		{"POST", "/inbox/messages/{messageId}/read", "readInboxMessage", ConsoleAndExternal, s.readInbox},
		{"POST", "/inbox/messages/{messageId}/reply", "replyInboxMessage", ConsoleAndExternal, s.replyInbox},
		{"GET", "/inbox/threads", "listInboxThreads", ConsoleAndExternal, s.listThreads},
		{"GET", "/inbox/threads/{threadId}", "getInboxThread", ConsoleAndExternal, s.getThread},
		{"POST", "/inbox/threads/{threadId}/read", "readInboxThread", ConsoleAndExternal, s.readThread},
		{"GET", "/inbox/pending", "getInboxPending", ConsoleOnly, s.getInboxPending},
		{"GET", "/inbox/events", "streamInboxEvents", ConsoleOnly, s.streamInbox},
		{"GET", "/agents/{agentId}/inbox", "listAgentInbox", ConsoleOnly, s.listAgentInbox},
		{"GET", "/agents/{agentId}/console-messages", "listAgentConsoleMessages", ConsoleOnly, s.listAgentConsoleMessages},
		{"GET", "/agents/{agentId}/inbox/{messageId}", "getAgentInboxMessage", ConsoleOnly, s.getAgentInboxMessage},
		{"GET", "/external-identities", "listExternalIdentities", ConsoleOnly, s.listIdentities},
		{"POST", "/external-identities", "createExternalIdentity", ConsoleOnly, s.createIdentity},
		{"GET", "/external-identities/{identityId}", "getExternalIdentity", ConsoleOnly, s.getIdentity},
		{"DELETE", "/external-identities/{identityId}", "deleteExternalIdentity", ConsoleOnly, s.deleteIdentity},
		{"PUT", "/external-identities/{identityId}/manager", "setExternalIdentityManager", ConsoleOnly, s.setIdentityManager},
		{"GET", "/external-identities/{identityId}/tokens", "listExternalTokens", ConsoleOnly, s.listTokens},
		{"POST", "/external-identities/{identityId}/tokens", "issueExternalToken", ConsoleOnly, s.issueToken},
		{"DELETE", "/external-identities/{identityId}/tokens/{tokenId}", "revokeExternalToken", ConsoleOnly, s.revokeToken},
		{"GET", "/external-identities/{identityId}/inbox", "listExternalIdentityInbox", ConsoleOnly, s.listIdentityInbox},
		{"GET", "/external-identities/{identityId}/inbox/{messageId}", "getExternalIdentityInboxMessage", ConsoleOnly, s.getIdentityInboxMessage},
	}
}

// AgentRoutes returns the routes of the Agent-only API (/agent/v1), whose contract is api/agent-openapi.yaml. The
// handlers are the same as for the console endpoints of the same name; the current identity is determined by the Agent
// token.
func (s *Server) AgentRoutes() []Route {
	return []Route{
		{"GET", "/agents/status", "getProjectAgentStatus", ConsoleOnly, s.getProjectAgentStatus},
		{"GET", "/projects", "listPlatformProjects", ConsoleOnly, s.listPlatformProjects},
		{"POST", "/sleep", "requestSleep", ConsoleOnly, s.requestSleep},
		{"GET", "/sleep", "getSleepState", ConsoleOnly, s.getSleepState},
		{"GET", "/tasks", "listManagerTasks", ConsoleOnly, s.listManagerTasks},
		{"GET", "/tasks/agents", "listTaskAgents", ConsoleOnly, s.listTaskAgents},
		{"GET", "/tasks/{taskId}", "getTask", ConsoleOnly, s.getTask},
		{"GET", "/tasks/{taskId}/nodes/{nodeRunId}", "getTaskNodeRun", ConsoleOnly, s.getTaskNode},
		{"GET", "/tasks/{taskId}/nodes/{nodeRunId}/messages", "listTaskNodeMessages", ConsoleOnly, s.listTaskNodeMessages},
		{"POST", "/tasks/{taskId}/retry", "retryTask", ConsoleOnly, s.retryTask},
		{"POST", "/tasks/{taskId}/cancel", "cancelTask", ConsoleOnly, s.cancelTask},
		{"GET", "/tasks/{taskId}/artifacts/{artifactId}", "downloadTaskArtifact", ConsoleOnly, s.downloadTaskArtifact},

		{"POST", "/tasks", "createTask", ConsoleOnly, s.createTask},
		{"GET", "/tasks/current", "getCurrentTask", ConsoleOnly, s.getCurrentTask},
		{"POST", "/tasks/{taskId}/nodes/{nodeRunId}/submit", "submitTaskNode", ConsoleOnly, s.submitTaskNode},
		{"POST", "/tasks/{taskId}/nodes/{nodeRunId}/fail", "failTaskNode", ConsoleOnly, s.failTaskNode},
		{"POST", "/tasks/{taskId}/artifacts", "publishTaskArtifact", ConsoleOnly, s.publishTaskArtifact},

		{"GET", "/whoami", "getWhoami", ConsoleOnly, s.whoami},
		{"GET", "/inbox/messages", "listInboxMessages", ConsoleOnly, s.listInbox},
		{"POST", "/inbox/messages", "sendInboxMessage", ConsoleOnly, s.sendInbox},
		{"POST", "/inbox/messages/{messageId}/read", "readInboxMessage", ConsoleOnly, s.readInbox},
		{"POST", "/inbox/messages/{messageId}/reply", "replyInboxMessage", ConsoleOnly, s.replyInbox},
		{"GET", "/inbox/threads/{threadId}", "getInboxThread", ConsoleOnly, s.getThread},
		{"POST", "/inbox/threads/{threadId}/read", "readInboxThread", ConsoleOnly, s.readThread},
	}
}

// Handler returns the handler for the loopback listener: the console API /api/v1, the Agent-only API /agent/v1, and
// every other path goes to the console's static files.
func (s *Server) Handler() http.Handler {
	root := http.NewServeMux()
	root.Handle("/api/v1/", s.cors(s.consoleAuth(s.newMux("/api/v1", s.Routes()))))
	ingress := s.cors(s.consoleAuth(s.pluginIngress("console")))
	root.Handle("/api/v1/plugins/{pluginId}/http", ingress)
	root.Handle("/api/v1/plugins/{pluginId}/http/{path...}", ingress)
	root.Handle("/agent/v1/", s.agentAuth(s.newMux("/agent/v1", s.AgentRoutes())))
	root.HandleFunc("GET /healthz", s.getHealth)
	if s.O.Console != nil {
		root.Handle("/", s.O.Console)
	}
	return withCommonHeaders(s.logRequests(s.checkHost(root)))
}

// RequestIDHeader is the response header that returns the request ID; VersionHeader is the response header that returns
// the server version.
const (
	RequestIDHeader = "Loop-Now-Request-Id"
	VersionHeader   = "Loop-Now-Version"
)

// withCommonHeaders is the outermost middleware of every entry point: it generates a request ID (UUIDv7) for the
// request and puts it in the context, and writes the request ID and the server version into the response, on both
// successful and failed responses.
func withCommonHeaders(next http.Handler) http.Handler {
	version := buildinfo.Version()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := uuid.Must(uuid.NewV7()).String()
		w.Header().Set(RequestIDHeader, id)
		w.Header().Set(VersionHeader, version)
		next.ServeHTTP(w, r.WithContext(reqid.With(r.Context(), id)))
	})
}

// ---- Middleware ----

// checkHost rejects requests whose Host is not on the allowlist, to prevent DNS rebinding.
func (s *Server) checkHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]"))
		if !slices.Contains(s.O.AllowedHosts, host) {
			writeError(w, &apperr.Error{Status: http.StatusForbidden, Code: apperr.CodeInvalidHost, Message: "Host 不在允许名单中"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// cors sends CORS headers only for allowlisted origins; a request with an Origin not on the allowlist is always
// rejected, and no write is performed. Same-origin requests (the console served by the server itself) need no
// allowlist: browsers also send Origin on same-origin write requests, and it equals this request's scheme://Host, while
// Host has already passed the checkHost allowlist.
func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && !sameOrigin(r, origin) {
			if !slices.Contains(s.O.ConsoleOrigins, origin) {
				writeError(w, &apperr.Error{Status: http.StatusForbidden, Code: apperr.CodeForbiddenOrigin, Message: "来源不在控制台白名单中"})
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Expose-Headers", "ETag, "+RequestIDHeader+", "+VersionHeader+", Content-Disposition")
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
				h.Set("Access-Control-Allow-Headers", "Content-Type, If-Match, Last-Event-ID")
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin reports whether origin is the origin of this request itself.
func sameOrigin(r *http.Request, origin string) bool {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return strings.EqualFold(origin, scheme+"://"+r.Host)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// logRequests logs the method, path, status and duration; it never logs the request body, nor headers such as
// Authorization.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.Log.Debug("HTTP request", "requestId", reqid.From(r.Context()), "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration", time.Since(start))
	})
}

// ---- Common handling ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func writeError(w http.ResponseWriter, err error) {
	e, ok := apperr.As(err)
	if !ok {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			e = apperr.TooLarge("请求体超过上限")
		} else {
			e = &apperr.Error{Status: http.StatusInternalServerError, Code: apperr.CodeInternal, Message: "服务端内部错误"}
		}
	}
	body := apitypes.ErrorBody{Error: apitypes.ErrorDetail{Code: e.Code, Message: e.Message, Details: e.Details}}
	for _, f := range e.Fields {
		body.Error.Fields = append(body.Error.Fields, apitypes.FieldError{Field: f.Field, Message: f.Message})
	}
	writeJSON(w, e.Status, body)
}

// fail writes the error response and logs internal errors in one place.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if _, ok := apperr.As(err); !ok {
		var tooLarge *http.MaxBytesError
		if !errors.As(err, &tooLarge) && !errors.Is(err, context.Canceled) {
			s.Log.Error("request failed", "requestId", reqid.From(r.Context()), "method", r.Method, "path", r.URL.Path, "err", err)
		}
	}
	writeError(w, err)
}

// readJSON parses a JSON request body, rejecting unknown fields and oversized bodies.
func readJSON(w http.ResponseWriter, r *http.Request, limit int64, v any) error {
	body := http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return apperr.TooLarge("请求体超过上限")
		}
		return apperr.Invalid("body", "请求体不是合法的 JSON 或包含未知字段："+err.Error())
	}
	if dec.More() {
		return apperr.Invalid("body", "请求体只能包含一个 JSON 对象")
	}
	return nil
}

func readBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, apperr.TooLarge("请求体超过上限")
		}
		return nil, err
	}
	return b, nil
}

func pathUUID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		return uuid.Nil, apperr.NotFound("资源") // a malformed ID cannot match any resource
	}
	return id, nil
}

// jsonLimit is the general limit for JSON request bodies.
const jsonLimit = 1 << 20
