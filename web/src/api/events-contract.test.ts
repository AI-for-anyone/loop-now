import { TASK_EVENT_NAMES, PROJECT_TASK_EVENT_NAMES } from './task-events'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { AGENT_EVENT_NAMES, INBOX_EVENT_NAMES, OVERVIEW_EVENT_NAMES } from './events'

// The existing locked test dependency has no declarations. Keep its boundary
// unknown and validate the parsed object rather than asserting an API shape.
const { load }: { load: (source: string) => unknown } = createRequire(import.meta.url)('js-yaml')

// 按解析后的路径读取，不依赖 YAML/JSON 块的引号、缩进或端点顺序。
function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function objectAt(value: unknown, label: string): Record<string, unknown> {
  if (!isObject(value)) {
    throw new Error(`契约对象缺失或无效：${label}`)
  }
  return value
}

function sseEventsFromContract(
  path = '/agents/{agentId}/events',
  source = readFileSync(resolve(process.cwd(), '../api/openapi.yaml'), 'utf8'),
): Record<string, string> {
  const document = objectAt(load(source), 'document')
  const paths = objectAt(document.paths, 'paths')
  const endpoint = objectAt(paths[path], path)
  const get = objectAt(endpoint.get, `${path}.get`)
  const events = objectAt(get['x-sse-events'], `${path}.get.x-sse-events`)
  return Object.fromEntries(Object.entries(events).map(([name, value]) => {
    const schema = objectAt(value, `${path}.${name}`)
    const ref = schema.$ref
    if (typeof ref !== 'string' || !/^#\/components\/schemas\/\w+$/.test(ref)) {
      throw new Error(`事件 schema 引用无效：${path}.${name}`)
    }
    return [name, ref.slice('#/components/schemas/'.length)]
  }))
}

describe('事件名与契约一致', () => {
  it('控制台处理的事件名恰好是 x-sse-events 中的事件名', () => {
    const contract = sseEventsFromContract()
    expect([...AGENT_EVENT_NAMES].sort()).toEqual(Object.keys(contract).sort())
  })

  it('每个事件对应 Sse 开头的 schema', () => {
    const contract = sseEventsFromContract()
    expect(contract).toEqual({
      snapshot: 'SseSnapshot',
      'message.created': 'SseMessageCreated',
      'run.updated': 'SseRunUpdated',
      'run.text': 'SseRunText',
      'run.tool': 'SseRunTool',
      'run.ended': 'SseRunEnded',
      'session.updated': 'SseSessionUpdated',
      'sleep.updated': 'SseSleepUpdated',
      'queue.updated': 'SseQueueUpdated',
    })
  })

  it('概览处理的事件名恰好是 /overview/events 的 x-sse-events 中的事件名', () => {
    const contract = sseEventsFromContract('/overview/events')
    expect([...OVERVIEW_EVENT_NAMES].sort()).toEqual(Object.keys(contract).sort())
    expect(contract).toEqual({ snapshot: 'SseOverviewSnapshot', 'agent.status': 'SseAgentStatus', 'agent.removed': 'SseAgentRemoved' })
  })

  it('收件箱处理的事件名恰好是 /inbox/events 的 x-sse-events 中的事件名', () => {
    const contract = sseEventsFromContract('/inbox/events')
    expect([...INBOX_EVENT_NAMES].sort()).toEqual(Object.keys(contract).sort())
    expect(contract).toEqual({
      snapshot: 'SseInboxSnapshot',
      'inbox.message.created': 'SseInboxMessageCreated',
      'inbox.message.read': 'SseInboxMessageRead',
      'inbox.message.cancelled': 'SseInboxMessageCancelled',
      'inbox.question.updated': 'SseInboxMessageCreated',
    })
  })
})

it('任务与项目列表事件名/schema与正式契约一致', () => {
  const task = sseEventsFromContract('/tasks/{taskId}/events')
  expect(Object.keys(task).sort()).toEqual([...TASK_EVENT_NAMES].sort())
  expect(task).toEqual({ snapshot: 'TaskSnapshotEvent', 'task.updated': 'TaskUpdatedEvent', 'node.updated': 'TaskNodeUpdatedEvent', 'message.created': 'TaskMessage', 'task.inbox.updated': 'TaskNodeInboxRecord', 'artifact.published': 'TaskArtifactEvent', 'sleep.updated': 'SseSleepUpdated', 'task.node.queue.updated': 'TaskNodeQueue' })
  const project = sseEventsFromContract('/projects/{projectId}/tasks/events')
  expect(Object.keys(project).sort()).toEqual([...PROJECT_TASK_EVENT_NAMES].sort())
  expect(project).toEqual({ snapshot: 'TaskProjectSnapshotEvent', 'task.updated': 'TaskUpdatedEvent' })
})

describe('契约解析器严格限定目标 GET', () => {
  const taskPath = '/tasks/{taskId}/events'
  const fixture = {
    paths: {
      [taskPath]: { get: { 'x-sse-events': { 'task.inbox.updated': { $ref: '#/components/schemas/TaskNodeInboxRecord' } } } },
      '/projects/{projectId}/tasks/events': { get: { 'x-sse-events': { snapshot: { $ref: '#/components/schemas/TaskProjectSnapshotEvent' } } } },
    },
  }

  it('合法带引号 JSON-in-YAML 不会读取下一个项目端点', () => {
    expect(sseEventsFromContract(taskPath, JSON.stringify(fixture))).toEqual({ 'task.inbox.updated': 'TaskNodeInboxRecord' })
  })

  it('目标 GET 缺事件扩展时失败，不向后扫描其它端点', () => {
    const source = structuredClone(fixture)
    const missing = { paths: { ...source.paths, [taskPath]: { get: {} } } }
    expect(() => sseEventsFromContract(taskPath, JSON.stringify(missing))).toThrow('get.x-sse-events')
  })

  it('不存在的路径不回退到其它端点', () => {
    expect(() => sseEventsFromContract('/missing', JSON.stringify(fixture))).toThrow('/missing')
  })

  it('非法或缺失 schema 引用不能静默略过', () => {
    for (const schema of [{}, { $ref: '#/components/parameters/TaskNodeInboxRecord' }, { $ref: null }]) {
      const source = { paths: { [taskPath]: { get: { 'x-sse-events': { 'task.inbox.updated': schema } } } } }
      expect(() => sseEventsFromContract(taskPath, JSON.stringify(source))).toThrow('schema 引用无效')
    }
  })
})
