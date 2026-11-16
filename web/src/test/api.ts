import { config } from '@/config'
import type { Schemas } from '@/api/client'

// Builds server URLs in tests and constructs data that matches the contract.
export const apiUrl = (path: string) => `${config.apiBase}${path}`

export function errorBody(code: Schemas['ErrorCode'], message: string = code, extra: Partial<Schemas['Error']['error']> = {}): Schemas['Error'] {
  return { error: { code, message, ...extra } }
}

// The server's answer when a route does not exist (as opposed to a missing resource): 404 not_found with
// details.reason unknown_endpoint. Cast because NotFoundDetails is not in the generated contract on this branch yet.
export function unknownEndpointBody(message = 'No such endpoint: GET /api/v1/…'): Schemas['Error'] {
  return errorBody('not_found', message, { details: { reason: 'unknown_endpoint' } as never })
}

let seq = 0
const id = () => `00000000-0000-7000-8000-${String(++seq).padStart(12, '0')}`
const now = '2026-10-12T08:00:00Z'

// One item in the queue tray. By default, a message the console sent in the conversation, queued normally.
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

// One page of conversation history (matching the contract). Turn counts default to treating "this page is the whole history": no
// turns before it, and the total is the number of turns on this page (one run is one turn; runs still queued, or voided while
// queued without ever really running, don't count). Paging fixtures supply hasMore, turnsBefore and turnsTotal themselves.
const VOIDED: ReadonlySet<string | null> = new Set(['user_cancelled', 'merged_into_interrupt', 'no_pending_messages'])
export function makeMessagePage(messages: Schemas['Message'][] = [], overrides: Partial<Schemas['MessagePage']> = {}): Schemas['MessagePage'] {
  const turns = new Set(messages.filter((message) => message.run.status !== 'queued' && !VOIDED.has(message.run.endReason)).map((message) => message.run.id))
  return { messages, hasMore: false, hasMoreAfter: false, turnsBefore: 0, turnsTotal: turns.size, ...overrides }
}

// Belongs to the default project of the test context by default.
export function makeAgent(overrides: Partial<Schemas['Agent']> = {}): Schemas['Agent'] {
  return {
    id: id(),
    scope: 'project',
    projectId: context.project.id,
    isManager: false,
    name: '开发助手',
    // The server builds the address from the name; fixtures default to null (the contract allows it: it
    // is null when the name doesn't meet the naming rules), and tests that need an address supply one.
    address: null,
    model: { provider: 'anthropic', modelId: 'claude-sonnet-5-5' },
    runtime: 'pi',
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
    runtime: agent.runtime,
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

// Default project: the same project as context.project.
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
// Agent list and create URL of the default project.
export const agentsUrl = projectAgentsUrl(context.project.id)

// Console path of the default project, with the project name encoded, the same as the path the console generates itself.
export const defaultAgentsPath = `/p/${encodeURIComponent(context.project.name)}/agents`
export const defaultAgentPath = (agentId: string, tab: 'chat' | 'config') => `${defaultAgentsPath}/${agentId}/${tab}`
