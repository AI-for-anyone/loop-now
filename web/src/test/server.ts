import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { apiUrl, context, defaultProject, errorBody, projectsUrl, unknownEndpointBody } from './api'
import { emptyProjectCredentialHandlers } from './project-credentials'
import { commandList } from './commands'
import { runtimeDefinitions, runtimeList } from './runtimes'
import { automaticSettings } from './text-generation'

// Each test registers its own endpoint handlers with server.use(...). The left sidebar is always present and polls health
// on a timer, and both scope switching and Agent pages read the project list, so by default there is a healthy health
// check and a project list with only the default project; tests that need other results override them with server.use.
export const server = setupServer(
  ...emptyProjectCredentialHandlers,
  http.get(apiUrl('/plugins'), () => HttpResponse.json({ plugins: [] })),
  http.get(apiUrl('/projects/:projectId/plugins'), () => HttpResponse.json({ plugins: [] })),
  http.get(apiUrl('/agents/:agentId/plugins'), () => HttpResponse.json({ plugins: [] })),
  // Chat page "/" menu: by default only the 7 built-in platform commands (all
  // available); the Agent hasn't started Pi yet, so there are no Pi commands.
  http.get(apiUrl('/agents/:agentId/commands'), () => HttpResponse.json(commandList())),
  // Runtimes: by default the local Claude Code is installed and logged in.
  http.get(apiUrl('/runtimes'), () => HttpResponse.json(runtimeList())),
  // A provider's models: none by default (the message box's runner chip reads those of the Agent's provider); tests that need models
  // register catalogHandlers or their own.
  http.get(apiUrl('/model-providers/:providerId/models'), ({ params }) => HttpResponse.json({ providerId: String(params.providerId), models: [] })),
  // Runtime definitions: Pi (bundled, the default) and the local Claude Code, as the server defines them.
  http.get(apiUrl('/runtime-definitions'), () => HttpResponse.json(runtimeDefinitions())),
  // No local ACP command added.
  http.get(apiUrl('/acp-commands'), () => HttpResponse.json({ commands: [] })),
  http.get(apiUrl('/projects/:projectId/cli-auth/operations'), () => HttpResponse.json({ operations: [] })),
  // Unrelated historical fixtures do not exercise node Inbox or task timeline.
  // New feature tests override these; no fake normal Run is manufactured.
  http.get(apiUrl('/tasks/:taskId/nodes/:nodeRunId/inbox-messages'), () => HttpResponse.json({ items: [], nextCursor: null })),
  // Permission modes: new Agents on Full access, and no request waiting, for an Agent or a task node. Pages read these on load; an
  // unanswered read fails like a lost connection (see the background tasks below).
  http.get(apiUrl('/permission-settings'), () => HttpResponse.json({ defaultMode: 'full' })),
  http.get(apiUrl('/agents/:agentId/permission-requests'), () => HttpResponse.json({ requests: [] })),
  http.get(apiUrl('/tasks/:taskId/nodes/:nodeRunId/permission-requests'), ({ params }) =>
    HttpResponse.json({ taskId: String(params.taskId), nodeRunId: String(params.nodeRunId), runtimeId: '00000000-0000-7000-8000-000000000000', requests: [] }),
  ),
  // The current node's running background tasks: none by default. Every task page reads them; an unanswered read fails like a lost
  // connection and puts up the “连不上服务端” (Can't reach the server) banner, which other assertions then trip over.
  http.get(apiUrl('/tasks/:taskId/nodes/:nodeRunId/background-tasks'), ({ params }) =>
    HttpResponse.json({ taskId: String(params.taskId), nodeRunId: String(params.nodeRunId), runtimeId: '00000000-0000-7000-8000-000000000000', tasks: [] }),
  ),
  // Removing a kept worktree: by default nothing is kept, as the server answers before a worktree exists; tests that remove override it.
  http.post(apiUrl('/tasks/:taskId/workspace/remove'), () => HttpResponse.json(errorBody('task_workspace_not_kept'), { status: 409 })),
  // Folders: by default an empty root, a synthetic location, and no Git pull history.
  http.get(apiUrl('/agents/:agentId/folder'), ({ params }) => {
    const path = `/test-work/agents/${String(params.agentId)}/workspace`
    return HttpResponse.json({ path, realPath: path })
  }),
  http.get(apiUrl('/tasks/:taskId/nodes/:nodeRunId/folder'), ({ params }) => {
    const path = `/test-work/tasks/${String(params.taskId)}/nodes/${String(params.nodeRunId)}/workspace`
    return HttpResponse.json({ path, realPath: path })
  }),
  http.get(apiUrl('/agents/:agentId/folder/entries'), ({ request }) => HttpResponse.json({ path: new URL(request.url).searchParams.get('path') ?? '', entries: [], nextCursor: null })),
  http.head(apiUrl('/agents/:agentId/folder/raw/*'), () => new HttpResponse(null, { status: 404 })),
  http.head(apiUrl('/tasks/:taskId/nodes/:nodeRunId/folder/raw/*'), () => new HttpResponse(null, { status: 404 })),
  http.get(apiUrl('/projects/:projectId/context-folder/entries'), ({ request }) => HttpResponse.json({ path: new URL(request.url).searchParams.get('path') ?? '', entries: [], nextCursor: null })),
  http.get(apiUrl('/context-folder/entries'), ({ request }) => HttpResponse.json({ path: new URL(request.url).searchParams.get('path') ?? '', entries: [], nextCursor: null })),
  http.get(apiUrl('/agents/:agentId/folder/git-pulls'), () => HttpResponse.json({ pulls: [] })),
  // Running count next to “任务” (Tasks) in the left sidebar: by default no unfinished tasks.
  http.get(apiUrl('/projects/:projectId/tasks/counts'), () => HttpResponse.json({ preparing: 0, running: 0, waiting: 0, blocked: 0 })),
  // Local mode: by default the first three switches are on and “用本机的登录状态” (Use local logins) is off, and neither projects
  // nor Agents have registered local directories yet (tests that need them override with localModeBackend).
  http.get(apiUrl('/local-mode-settings'), () => HttpResponse.json({ directories: true, git: true, cli: true, logins: false })),
  http.get(apiUrl('/text-generation-settings'), () => HttpResponse.json(automaticSettings())),
  // Remote door, relay account, tunnel & paired devices: off/signed-out and empty by default (local door tests override as needed).
  http.get(apiUrl('/remote-access'), () => HttpResponse.json({ enabled: false, port: null, state: 'off', url: null, error: null })),
  http.get(apiUrl('/relay-account'), () =>
    HttpResponse.json({
      relayUrl: 'https://relay.loop-now.app',
      signedIn: false,
      accountId: null,
      signedInAt: null,
      signIn: null,
      error: null,
    }),
  ),
  http.get(apiUrl('/tunnel'), () =>
    HttpResponse.json({
      enabled: false,
      state: 'off',
      hostname: null,
      url: null,
      error: null,
      errorCode: null,
      retryAt: null,
      download: null,
      cloudflared: null,
    }),
  ),
  http.get(apiUrl('/device-pairings/:pairingId'), ({ params }) =>
    HttpResponse.json({
      id: String(params.pairingId),
      state: 'open',
      expiresAt: new Date(Date.now() + 10 * 60_000).toISOString(),
      device: null,
    }),
  ),
  http.get(apiUrl('/devices'), () => HttpResponse.json({ devices: [] })),
  // Read wherever a path can be chosen (whether this computer's dialog can be shown in a browser): false unless a test says otherwise.
  http.get(apiUrl('/context'), () => HttpResponse.json(context)),
  http.get(apiUrl('/projects/:projectId/local-directories'), () => HttpResponse.json({ revision: 0, directories: [] })),
  http.get(apiUrl('/agents/:agentId/local-directories'), () => HttpResponse.json({ revision: 0, directories: [], projectId: null, inherited: [] })),
  http.get(apiUrl('/agents/:agentId/queue'), ({ params }) => HttpResponse.json({ agentId: params.agentId, items: [] })),
  // Messages to you: by default no exchanges and nothing waiting on you (tests that need them override with fakeInbox).
  http.get(apiUrl('/agents/:agentId/console-messages'), () => HttpResponse.json({ messages: [], nextCursor: null })),
  http.get(apiUrl('/inbox/pending'), () => HttpResponse.json({ questions: [], unread: [], byAgent: [], byTask: [] })),
  http.get(apiUrl('/tasks/:taskId/nodes/:nodeRunId/queue'), ({ params }) => HttpResponse.json({ taskId: params.taskId, nodeRunId: params.nodeRunId, runtimeId: '00000000-0000-7000-8000-000000000000', items: [] })),
  http.get(apiUrl('/tasks/:taskId/nodes/:nodeRunId/runs/:runId/timeline'), () => HttpResponse.json(unknownEndpointBody(), { status: 404 })),
  http.get(apiUrl('/health'), () => HttpResponse.json({ ok: true, database: { ok: true, error: null, walBytes: { data: 0, events: 0 }, walWarning: false } })),
  http.get(apiUrl('/projects/:projectId/prompt'), () => HttpResponse.json({ prompt: '', promptVersion: 1 })),
  http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject] })),
  // Plugin README: none by default (for installed plugins, supply it through the readmes of fakePlugins).
  http.get(apiUrl('/plugins/:pluginId/readme'), () => HttpResponse.json({ file: null, markdown: null, truncated: false })),
  http.get(apiUrl('/plugin-previews/:previewId/readme'), () => HttpResponse.json({ file: null, markdown: null, truncated: false })),
  // The left sidebar lists platform-held Agents in the global scope: none by default.
  http.get(apiUrl('/platform/agents'), () => HttpResponse.json({ agents: [] })),
)
