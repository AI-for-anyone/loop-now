import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { apiUrl, defaultProject, projectsUrl, unknownEndpointBody } from './api'
import { emptyProjectCredentialHandlers } from './project-credentials'
import { commandList } from './commands'
import { runtimeList } from './runtimes'

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
  http.get(apiUrl('/projects/:projectId/cli-auth/operations'), () => HttpResponse.json({ operations: [] })),
  // Unrelated historical fixtures do not exercise node Inbox or task timeline.
  // New feature tests override these; no fake normal Run is manufactured.
  http.get(apiUrl('/tasks/:taskId/nodes/:nodeRunId/inbox-messages'), () => HttpResponse.json({ items: [], nextCursor: null })),
  // Folders: by default an empty root with no Git pull history.
  http.get(apiUrl('/agents/:agentId/folder/entries'), ({ request }) => HttpResponse.json({ path: new URL(request.url).searchParams.get('path') ?? '', entries: [], nextCursor: null })),
  http.get(apiUrl('/projects/:projectId/context-folder/entries'), ({ request }) => HttpResponse.json({ path: new URL(request.url).searchParams.get('path') ?? '', entries: [], nextCursor: null })),
  http.get(apiUrl('/context-folder/entries'), ({ request }) => HttpResponse.json({ path: new URL(request.url).searchParams.get('path') ?? '', entries: [], nextCursor: null })),
  http.get(apiUrl('/agents/:agentId/folder/git-pulls'), () => HttpResponse.json({ pulls: [] })),
  // Running count next to “任务” (Tasks) in the left sidebar: by default no unfinished tasks.
  http.get(apiUrl('/projects/:projectId/tasks/counts'), () => HttpResponse.json({ preparing: 0, running: 0, waiting: 0, blocked: 0 })),
  // Local mode: by default the first three switches are on and “用本机的登录状态” (Use local logins) is off, and neither projects
  // nor Agents have registered local directories yet (tests that need them override with localModeBackend).
  http.get(apiUrl('/local-mode-settings'), () => HttpResponse.json({ directories: true, git: true, cli: true, logins: false })),
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
