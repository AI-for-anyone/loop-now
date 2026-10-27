import { config } from '@/config'
import type { Schemas } from '@/api/client'

// 测试中拼接服务端地址，以及构造符合契约的数据。
export const apiUrl = (path: string) => `${config.apiBase}${path}`

export function errorBody(code: Schemas['ErrorCode'], message: string = code, extra: Partial<Schemas['Error']['error']> = {}): Schemas['Error'] {
  return { error: { code, message, ...extra } }
}

let seq = 0
const id = () => `00000000-0000-7000-8000-${String(++seq).padStart(12, '0')}`
const now = '2026-10-12T08:00:00Z'

// 排队托盘中的一项。默认是控制台在对话中发出、普通排队的消息。
export function makeQueueItem(overrides: Partial<Schemas['QueueItem']> = {}): Schemas['QueueItem'] {
  return { kind: 'run', runId: id(), messageId: null, from: { kind: 'console', id: null, address: '@console', name: '控制台', deleted: false, pluginId: null, channelId: null }, text: '排队的消息', images: [], whenBusy: 'queue', queuedAt: '2026-10-12T08:00:00Z', ...overrides }
}

export function makeRun(overrides: Partial<Schemas['Run']> = {}): Schemas['Run'] {
  return {
    id: id(),
    status: 'completed',
    phase: null,
    endReason: null,
    errorCode: null,
    errorMessage: null,
    configVersion: 1,
    whenBusy: null,
    queuedAt: now,
    startedAt: now,
    runningAt: now,
    abortRequestedAt: null,
    endedAt: now,
    tools: [],
    usage: { calls: 0, input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
    model: null,
    ...overrides,
  }
}

export function makeMessage(overrides: Partial<Schemas['Message']> = {}): Schemas['Message'] {
  return { id: id(), role: 'user', origin: 'console', text: '你好', createdAt: now, sessionEpoch: 1, run: makeRun(), ...overrides }
}

// 默认属于测试上下文中的默认项目。
export function makeAgent(overrides: Partial<Schemas['Agent']> = {}): Schemas['Agent'] {
  return {
    id: id(),
    scope: 'project',
    projectId: context.project.id,
    isManager: false,
    name: '开发助手',
    // 地址由服务端按名称拼出；样例默认为 null（契约允许，名称不满足命名规则时就是 null），需要地址的测试自己给出。
    address: null,
    model: { provider: 'anthropic', modelId: 'claude-sonnet-5-5' },
    prompt: '',
    promptVersion: 1,
    configVersion: 1,
    busy: false,
    session: { state: 'active', epoch: 1 },
    clis: [],
    modelCredential: null,
    createdAt: now,
    updatedAt: now,
    ...overrides,
  }
}

export function toListItem(agent: Schemas['Agent'], sessionState: Schemas['SessionState'] = agent.session.state): Schemas['AgentListItem'] {
  return {
    id: agent.id,
    scope: agent.scope,
    projectId: agent.projectId,
    isManager: agent.isManager,
    name: agent.name,
    address: agent.address,
    model: agent.model,
    configVersion: agent.configVersion,
    busy: agent.busy,
    sessionState,
    sleep: null,
    sleepRevision: 0,
    createdAt: agent.createdAt,
    updatedAt: agent.updatedAt,
  }
}

export function makeStatus(agentId: string, overrides: Partial<Schemas['AgentStatus']> = {}): Schemas['AgentStatus'] {
  return {
    sleep: null, sleepRevision: 0,
    agentId,
    busy: false,
    currentRun: null,
    queued: { interrupt: 0, normal: 0 },
    processState: 'idle',
    processPid: null,
    processStartedAt: null,
    session: { state: 'active', epoch: 1 },
    ...overrides,
  }
}

export const limits: Schemas['Limits'] = {
  maxUploadFile: 10 * 1024 * 1024,
  maxFolderUpload: 1024 * 1024 * 1024,
  maxMessage: 64 * 1024,
  maxInboxMessage: 64 * 1024,
  maxSkillArchive: 20 * 1024 * 1024,
  maxSkillEntries: 500,
  maxCredential: 64 * 1024,
  textEdit: { maxBytes: 1024 * 1024, extensions: ['.md', '.txt'] },
}

export const context: Schemas['Context'] = {
  organization: { id: id(), name: '测试组织' },
  project: { id: id(), organizationId: id(), name: '测试项目' },
  limits,
}

// 默认项目：与 context.project 是同一个项目。
export const defaultProject: Schemas['Project'] = {
  id: context.project.id,
  organizationId: context.organization.id,
  name: context.project.name,
  isDefault: true,
  managerAgentId: null,
  agentCount: 0,
  createdAt: now,
  updatedAt: now,
}

export function makeProject(overrides: Partial<Schemas['Project']> = {}): Schemas['Project'] {
  return { ...defaultProject, id: id(), name: '模型接入', isDefault: false, ...overrides }
}

export const projectsUrl = apiUrl('/projects')
export const platformAgentsUrl = apiUrl('/platform/agents')
export const projectAgentsUrl = (projectId: string) => apiUrl(`/projects/${projectId}/agents`)
// 默认项目的 Agent 列表与新建地址。
export const agentsUrl = projectAgentsUrl(context.project.id)

// 控制台中默认项目的地址，项目名已编码，与控制台自己生成的地址相同。
export const defaultAgentsPath = `/p/${encodeURIComponent(context.project.name)}/agents`
export const defaultAgentPath = (agentId: string, tab: 'chat' | 'config') => `${defaultAgentsPath}/${agentId}/${tab}`
