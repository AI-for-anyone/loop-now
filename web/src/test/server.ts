import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { apiUrl, defaultProject, projectsUrl } from './api'
import { emptyProjectCredentialHandlers } from './project-credentials'
import { commandList } from './commands'
import { runtimeList } from './runtimes'

// 各测试用 server.use(...) 注册自己的接口处理器。左侧栏常驻并定时查询健康状态，范围切换与 Agent 页都要读项目列表，
// 所以默认给出正常的健康检查与只有默认项目的项目列表；需要其他结果的测试再用 server.use 覆盖。
export const server = setupServer(
  ...emptyProjectCredentialHandlers,
  http.get(apiUrl('/plugins'), () => HttpResponse.json({ plugins: [] })),
  http.get(apiUrl('/projects/:projectId/plugins'), () => HttpResponse.json({ plugins: [] })),
  http.get(apiUrl('/agents/:agentId/plugins'), () => HttpResponse.json({ plugins: [] })),
  // 对话页 "/" 菜单：默认只有 7 个平台内置命令（都可用）；Agent 还没启动过 Pi，没有 Pi 命令。
  http.get(apiUrl('/agents/:agentId/commands'), () => HttpResponse.json(commandList())),
  // 运行时：默认本机的 Claude Code 装好且已登录。
  http.get(apiUrl('/runtimes'), () => HttpResponse.json(runtimeList())),
  http.get(apiUrl('/projects/:projectId/cli-auth/operations'), () => HttpResponse.json({ operations: [] })),
  // Unrelated historical fixtures do not exercise node Inbox or task timeline.
  // New feature tests override these; no fake normal Run is manufactured.
  http.get(apiUrl('/tasks/:taskId/nodes/:nodeRunId/inbox-messages'), () => HttpResponse.json({ items: [], nextCursor: null })),
  // 文件夹：默认是空的根目录，没有 Git 拉取记录。
  http.get(apiUrl('/agents/:agentId/folder/entries'), ({ request }) => HttpResponse.json({ path: new URL(request.url).searchParams.get('path') ?? '', entries: [], nextCursor: null })),
  http.get(apiUrl('/projects/:projectId/context-folder/entries'), ({ request }) => HttpResponse.json({ path: new URL(request.url).searchParams.get('path') ?? '', entries: [], nextCursor: null })),
  http.get(apiUrl('/context-folder/entries'), ({ request }) => HttpResponse.json({ path: new URL(request.url).searchParams.get('path') ?? '', entries: [], nextCursor: null })),
  http.get(apiUrl('/agents/:agentId/folder/git-pulls'), () => HttpResponse.json({ pulls: [] })),
  // 左侧栏“任务”旁的运行数：默认没有未结束的任务。
  http.get(apiUrl('/projects/:projectId/tasks/counts'), () => HttpResponse.json({ preparing: 0, running: 0, waiting: 0, blocked: 0 })),
  // 本机模式：默认三个开关都开着，项目与 Agent 都还没有登记本机目录（需要的测试用 localModeBackend 覆盖）。
  http.get(apiUrl('/local-mode-settings'), () => HttpResponse.json({ directories: true, git: true, cli: true, logins: false })),
  http.get(apiUrl('/projects/:projectId/local-directories'), () => HttpResponse.json({ revision: 0, directories: [] })),
  http.get(apiUrl('/agents/:agentId/local-directories'), () => HttpResponse.json({ revision: 0, directories: [], projectId: null, inherited: [] })),
  http.get(apiUrl('/agents/:agentId/queue'), ({ params }) => HttpResponse.json({ agentId: params.agentId, items: [] })),
  // 发给你的消息：默认没有往来，也没有待你处理的内容（需要的测试用 fakeInbox 覆盖）。
  http.get(apiUrl('/agents/:agentId/console-messages'), () => HttpResponse.json({ messages: [], nextCursor: null })),
  http.get(apiUrl('/inbox/pending'), () => HttpResponse.json({ questions: [], unread: [], byAgent: [], byTask: [] })),
  http.get(apiUrl('/tasks/:taskId/nodes/:nodeRunId/queue'), ({ params }) => HttpResponse.json({ taskId: params.taskId, nodeRunId: params.nodeRunId, runtimeId: '00000000-0000-7000-8000-000000000000', items: [] })),
  http.get(apiUrl('/tasks/:taskId/nodes/:nodeRunId/runs/:runId/timeline'), () => HttpResponse.json({ error: { code: 'not_found', message: '接口不存在' } }, { status: 404 })),
  http.get(apiUrl('/health'), () => HttpResponse.json({ ok: true, database: { ok: true, error: null, walBytes: { data: 0, events: 0 }, walWarning: false } })),
  http.get(apiUrl('/projects/:projectId/prompt'), () => HttpResponse.json({ prompt: '', promptVersion: 1 })),
  http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject] })),
  // 插件的 README：默认没有（已装插件的用 fakePlugins 的 readmes 给出）。
  http.get(apiUrl('/plugins/:pluginId/readme'), () => HttpResponse.json({ file: null, markdown: null, truncated: false })),
  http.get(apiUrl('/plugin-previews/:previewId/readme'), () => HttpResponse.json({ file: null, markdown: null, truncated: false })),
  // 左侧栏在全局范围列出平台持有的 Agent：默认没有。
  http.get(apiUrl('/platform/agents'), () => HttpResponse.json({ agents: [] })),
)
