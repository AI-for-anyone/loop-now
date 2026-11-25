import { TASK_EVENT_NAMES, PROJECT_TASK_EVENT_NAMES } from './task-events'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { AGENT_EVENT_NAMES, INBOX_EVENT_NAMES, OVERVIEW_EVENT_NAMES } from './events'

// The existing locked test dependency has no declarations. Keep its boundary
// unknown and validate the parsed object rather than asserting an API shape.
const { load }: { load: (source: string) => unknown } = createRequire(import.meta.url)('js-yaml')

// Read by path in the parsed document, so nothing depends on quoting, indentation or endpoint order in the YAML/JSON blocks.
function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function objectAt(value: unknown, label: string): Record<string, unknown> {
  if (!isObject(value)) {
    throw new Error(`Contract object is missing or invalid: ${label}`)
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
      throw new Error(`Event schema reference is invalid: ${path}.${name}`)
    }
    return [name, ref.slice('#/components/schemas/'.length)]
  }))
}

describe('event names match the contract', () => {
  it('the event names the console handles are exactly the event names in x-sse-events', () => {
    const contract = sseEventsFromContract()
    expect([...AGENT_EVENT_NAMES].sort()).toEqual(Object.keys(contract).sort())
  })

  it('each event maps to a schema whose name starts with Sse', () => {
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
      'background_tasks.updated': 'SseBackgroundTasksUpdated',
    })
  })

  it('the event names the overview handles are exactly the event names in x-sse-events of /overview/events', () => {
    const contract = sseEventsFromContract('/overview/events')
    expect([...OVERVIEW_EVENT_NAMES].sort()).toEqual(Object.keys(contract).sort())
    expect(contract).toEqual({ snapshot: 'SseOverviewSnapshot', 'agent.status': 'SseAgentStatus', 'agent.removed': 'SseAgentRemoved' })
  })

  it('the event names the inbox handles are exactly the event names in x-sse-events of /inbox/events', () => {
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

it('task and project task list event names and schemas match the contract', () => {
  const task = sseEventsFromContract('/tasks/{taskId}/events')
  expect(Object.keys(task).sort()).toEqual([...TASK_EVENT_NAMES].sort())
  expect(task).toEqual({ snapshot: 'TaskSnapshotEvent', 'task.updated': 'TaskUpdatedEvent', 'node.updated': 'TaskNodeUpdatedEvent', 'message.created': 'TaskMessage', 'task.inbox.updated': 'TaskNodeInboxRecord', 'artifact.published': 'TaskArtifactEvent', 'sleep.updated': 'SseSleepUpdated', 'task.node.queue.updated': 'TaskNodeQueue', 'task.node.background_tasks.updated': 'TaskNodeBackgroundTasks' })
  const project = sseEventsFromContract('/projects/{projectId}/tasks/events')
  expect(Object.keys(project).sort()).toEqual([...PROJECT_TASK_EVENT_NAMES].sort())
  expect(project).toEqual({ snapshot: 'TaskProjectSnapshotEvent', 'task.updated': 'TaskUpdatedEvent' })
})

describe('the contract parser keeps strictly to the target GET', () => {
  const taskPath = '/tasks/{taskId}/events'
  const fixture = {
    paths: {
      [taskPath]: { get: { 'x-sse-events': { 'task.inbox.updated': { $ref: '#/components/schemas/TaskNodeInboxRecord' } } } },
      '/projects/{projectId}/tasks/events': { get: { 'x-sse-events': { snapshot: { $ref: '#/components/schemas/TaskProjectSnapshotEvent' } } } },
    },
  }

  it('valid quoted JSON-in-YAML does not read the next project endpoint', () => {
    expect(sseEventsFromContract(taskPath, JSON.stringify(fixture))).toEqual({ 'task.inbox.updated': 'TaskNodeInboxRecord' })
  })

  it('fails when the target GET lacks the event extension, without scanning on to other endpoints', () => {
    const source = structuredClone(fixture)
    const missing = { paths: { ...source.paths, [taskPath]: { get: {} } } }
    expect(() => sseEventsFromContract(taskPath, JSON.stringify(missing))).toThrow('get.x-sse-events')
  })

  it('a path that does not exist does not fall back to another endpoint', () => {
    expect(() => sseEventsFromContract('/missing', JSON.stringify(fixture))).toThrow('/missing')
  })

  it('an invalid or missing schema reference is not silently skipped', () => {
    for (const schema of [{}, { $ref: '#/components/parameters/TaskNodeInboxRecord' }, { $ref: null }]) {
      const source = { paths: { [taskPath]: { get: { 'x-sse-events': { 'task.inbox.updated': schema } } } } }
      expect(() => sseEventsFromContract(taskPath, JSON.stringify(source))).toThrow('schema reference is invalid')
    }
  })
})
